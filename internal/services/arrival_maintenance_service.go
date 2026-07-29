package services

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type arrivalContactWayProvider interface {
	DeleteContactWay(authorization *models.WeComTenantAuthorization, configID string) error
}

var ArrivalMaintenanceService = &arrivalMaintenanceService{
	provider:    WeComProviderService,
	linkService: ArrivalLinkService,
}

type arrivalMaintenanceService struct {
	provider    arrivalContactWayProvider
	linkService *arrivalLinkService
}

type ArrivalMaintenanceResult struct {
	CleanedContactWays int
	RetriedContactWays int
	ReconciledBindings int
}

func (s *arrivalMaintenanceService) ProcessDue(limit int) ArrivalMaintenanceResult {
	if !config.Current().Arrival.Enabled {
		return ArrivalMaintenanceResult{}
	}
	retried := s.RetryFailedContactWays(limit)
	return ArrivalMaintenanceResult{
		CleanedContactWays: s.CleanupExpiredContactWays(limit),
		RetriedContactWays: retried,
		ReconciledBindings: WeComProviderCallbackService.ReconcilePendingBindings(limit),
	}
}

func (s *arrivalMaintenanceService) RetryFailedContactWays(limit int) int {
	now := time.Now()
	items := repositories.ArrivalRepository.FindContactWaysDueForProvision(
		sqls.DB(),
		now,
		now.Add(-arrivalContactWayProvisionStaleAfter),
		arrivalContactWayMaxProvisionAttempts,
		limit,
	)
	retried := 0
	for i := range items {
		item := &items[i]
		event := repositories.ArrivalRepository.GetScanEvent(sqls.DB(), item.ScanEventID, item.TenantID)
		connection := repositories.ArrivalRepository.FindConnectionByStore(sqls.DB(), item.TenantID, item.StoreID)
		if event == nil || connection == nil || connection.Status != enums.StatusOk ||
			connection.ConnectionStatus != enums.ArrivalConnectionStatusActive {
			continue
		}
		requestID := tracex.EnsureRequestID("")
		linkService := s.linkService
		if linkService == nil {
			linkService = ArrivalLinkService
		}
		_, _ = linkService.retryContactWayProvision(event, connection, item, requestID)
		current := repositories.ArrivalRepository.GetContactWay(sqls.DB(), item.ID, item.TenantID)
		if current != nil && current.ProvisionAttemptCount > item.ProvisionAttemptCount {
			retried++
		}
	}
	return retried
}

func (s *arrivalMaintenanceService) CleanupExpiredContactWays(limit int) int {
	now := time.Now()
	items := repositories.ArrivalRepository.FindContactWaysDueForCleanup(sqls.DB(), now, limit)
	cleaned := 0
	for i := range items {
		if s.cleanupContactWay(&items[i], now) {
			cleaned++
		}
	}
	return cleaned
}

func (s *arrivalMaintenanceService) cleanupContactWay(item *models.ArrivalContactWay, now time.Time) bool {
	if item == nil {
		return false
	}
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(
		sqls.DB(),
		item.TenantAuthorizationID,
		item.TenantID,
	)
	if strings.TrimSpace(item.ConfigID) != "" {
		if authorization != nil && authorization.AuthorizationStatus == enums.WeComAuthorizationStatusRevoked {
			return s.cleanContactWayLocally(item, now, "local_only_authorization_revoked")
		}
		if authorization == nil || authorization.AuthorizationStatus != enums.WeComAuthorizationStatusActive {
			s.deferContactWayCleanup(item, now, "authorization_unavailable")
			return false
		}
		if s.provider == nil {
			s.deferContactWayCleanup(item, now, "official_provider_unavailable")
			return false
		}
		if err := s.provider.DeleteContactWay(authorization, item.ConfigID); err != nil {
			slog.Warn(
				"delete expired arrival contact way failed",
				"contact_way_id", item.ID,
				"tenant_id", item.TenantID,
				"store_id", item.StoreID,
				"error", err,
			)
			s.deferContactWayCleanup(item, now, "official_delete_failed")
			return false
		}
	}
	return s.cleanContactWayLocally(item, now, map[bool]string{
		true:  "official_deleted",
		false: "local_only_no_config",
	}[strings.TrimSpace(item.ConfigID) != ""])
}

func (s *arrivalMaintenanceService) cleanContactWayLocally(item *models.ArrivalContactWay, now time.Time, cleanupMode string) bool {
	if err := repositories.ArrivalRepository.UpdateContactWay(sqls.DB(), item.ID, item.TenantID, map[string]any{
		"contact_way_status":          enums.ArrivalContactWayStatusCleaned,
		"original_qr_code_ciphertext": "",
		"original_qr_code_nonce":      "",
		"original_png_base64":         "",
		"artwork_png_base64":          "",
		"cleaned_at":                  now,
		"failure_code":                "",
		"failure_stage":               "",
		"provider_http_status":        0,
		"provider_error_code":         0,
		"provider_error_message":      "",
		"failure_retryable":           false,
		"next_provision_retry_at":     nil,
		"updated_at":                  now,
		"update_user_name":            "arrival_maintenance",
	}); err != nil {
		slog.Warn(
			"mark expired arrival contact way cleaned failed",
			"contact_way_id", item.ID,
			"tenant_id", item.TenantID,
			"store_id", item.StoreID,
			"error", err,
		)
		return false
	}
	detail, _ := json.Marshal(map[string]any{
		"contactWayId":      item.ID,
		"scanEventRecordId": item.ScanEventID,
		"cleanupMode":       strings.TrimSpace(cleanupMode),
	})
	_ = repositories.ArrivalRepository.CreateAuditLog(sqls.DB(), &models.ArrivalAuditLog{
		TenantID:     item.TenantID,
		StoreID:      item.StoreID,
		Action:       "contact_way.cleanup",
		EntityType:   "ArrivalContactWay",
		EntityID:     item.ID,
		Result:       "success",
		DetailJSON:   string(detail),
		OperatorName: "arrival_maintenance",
		CreatedAt:    now,
	})
	return true
}

func (s *arrivalMaintenanceService) deferContactWayCleanup(item *models.ArrivalContactWay, now time.Time, failureCode string) {
	_ = repositories.ArrivalRepository.UpdateContactWay(sqls.DB(), item.ID, item.TenantID, map[string]any{
		"contact_way_status": enums.ArrivalContactWayStatusExpired,
		"failure_code":       strings.TrimSpace(failureCode),
		"updated_at":         now,
		"update_user_name":   "arrival_maintenance",
	})
}
