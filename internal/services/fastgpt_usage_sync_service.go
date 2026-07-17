package services

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// FastGPTUsageSyncService imports immutable, non-sensitive FastGPT usage
// evidence. It is intentionally asynchronous: a failed export never delays a
// customer reply or changes the active knowledge-base binding.
var FastGPTUsageSyncService = newFastGPTUsageSyncService()

type fastGPTUsageSyncService struct{}

func newFastGPTUsageSyncService() *fastGPTUsageSyncService { return &fastGPTUsageSyncService{} }

func (s *fastGPTUsageSyncService) ProcessDue(limit int) int {
	if limit <= 0 {
		limit = 50
	}
	// A deployment without the dedicated integration token remains on the
	// compatibility transport and has no managed FastGPT usage export to poll.
	if _, err := NewManagedStoreFastGPTConnector(); err != nil {
		return 0
	}
	knowledgeBases := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("connection_id", fastgptapi.ManagedConnectionID).
		Gt("tenant_id", 0).
		Eq("status", enums.StatusOk).
		Asc("id").Limit(limit))
	processed := 0
	for i := range knowledgeBases {
		if err := s.SyncKnowledgeBase(knowledgeBases[i].ID); err != nil {
			// Error details from FastGPT may contain upstream/provider text. Keep
			// observability scoped to IDs and persist only a safe class below.
			slog.Warn("FastGPT usage sync failed", "knowledgeBaseID", knowledgeBases[i].ID)
			continue
		}
		processed++
	}
	return processed
}

func (s *fastGPTUsageSyncService) SyncKnowledgeBase(knowledgeBaseID int64) error {
	knowledgeBase := KnowledgeBaseService.Get(knowledgeBaseID)
	if knowledgeBase == nil || knowledgeBase.TenantID <= 0 || knowledgeBase.ConnectionID != fastgptapi.ManagedConnectionID || knowledgeBase.StoreID <= 0 || strings.TrimSpace(knowledgeBase.DatasetID) == "" {
		return fmt.Errorf("managed FastGPT knowledge base is unavailable")
	}
	tenant := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), knowledgeBase.StoreID, knowledgeBase.TenantID)
	if tenant == nil || tenant.TenantTeamID == "" || tenant.Status != "active" {
		s.saveFailure(knowledgeBase, "tenant_unavailable")
		return fmt.Errorf("managed FastGPT tenant is unavailable")
	}
	state := repositories.FastGPTUsageSyncStateRepository.GetByKnowledgeBaseIDInTenant(sqls.DB(), knowledgeBase.ID, knowledgeBase.TenantID)
	if state == nil {
		now := time.Now()
		state = &models.FastGPTUsageSyncState{
			TenantID:        knowledgeBase.TenantID,
			CompanyID:       knowledgeBase.CompanyID,
			StoreID:         knowledgeBase.StoreID,
			KnowledgeBaseID: knowledgeBase.ID,
			TenantTeamID:    tenant.TenantTeamID,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
	}
	connector, err := NewManagedStoreFastGPTConnector()
	if err != nil {
		s.saveFailure(knowledgeBase, "integration_unavailable")
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Profile metadata is deliberately best-effort. A transient FastGPT profile
	// read must never block usage import or customer-facing knowledge retrieval.
	if profile, profileErr := connector.ForStore(knowledgeBase.StoreID).GetDatasetProfileSnapshot(ctx, knowledgeBase.DatasetID); profileErr == nil && profile != nil {
		s.syncProfileSnapshot(knowledgeBase, profile)
	}
	page, err := connector.ForStore(knowledgeBase.StoreID).ListUsageEvents(ctx, knowledgeBase.DatasetID, state.Cursor, 100)
	if err != nil {
		s.saveFailure(knowledgeBase, "usage_export_failed")
		return err
	}
	for _, item := range page.Events {
		if err := AIUsageEventService.Record(toFastGPTUsageEvent(knowledgeBase, tenant, item)); err != nil {
			s.saveFailure(knowledgeBase, "usage_record_failed")
			return err
		}
	}
	now := time.Now()
	state.CompanyID = knowledgeBase.CompanyID
	state.StoreID = knowledgeBase.StoreID
	state.TenantTeamID = tenant.TenantTeamID
	state.Cursor = page.NextCursor
	state.LastSyncedAt = &now
	state.LastError = ""
	state.UpdatedAt = now
	return repositories.FastGPTUsageSyncStateRepository.Save(sqls.DB(), state)
}

func (s *fastGPTUsageSyncService) syncProfileSnapshot(knowledgeBase *models.KnowledgeBase, profile *fastgptapi.DatasetProfileSnapshot) {
	if knowledgeBase == nil || profile == nil {
		return
	}
	now := time.Now()
	_ = repositories.KnowledgeBaseRepository.UpdatesInTenant(sqls.DB(), knowledgeBase.ID, knowledgeBase.TenantID, map[string]any{
		"fastgpt_profile_id":          profile.ProfileID,
		"fastgpt_profile_name":        profile.ProfileName,
		"fastgpt_profile_revision":    profile.ProfileRevision,
		"fastgpt_profile_fingerprint": profile.Fingerprint,
		"fastgpt_profile_status":      firstNonBlank(profile.ProfileStatus, "pending"),
		"fastgpt_profile_synced_at":   now,
		"updated_at":                  now,
		"update_user_name":            "fastgpt_usage_sync",
	})
}

func (s *fastGPTUsageSyncService) saveFailure(knowledgeBase *models.KnowledgeBase, errorClass string) {
	if knowledgeBase == nil || knowledgeBase.ID <= 0 {
		return
	}
	now := time.Now()
	state := repositories.FastGPTUsageSyncStateRepository.GetByKnowledgeBaseIDInTenant(sqls.DB(), knowledgeBase.ID, knowledgeBase.TenantID)
	if state == nil {
		state = &models.FastGPTUsageSyncState{
			TenantID:        knowledgeBase.TenantID,
			CompanyID:       knowledgeBase.CompanyID,
			StoreID:         knowledgeBase.StoreID,
			KnowledgeBaseID: knowledgeBase.ID,
			CreatedAt:       now,
		}
	}
	state.LastError = errorClass
	state.UpdatedAt = now
	_ = repositories.FastGPTUsageSyncStateRepository.Save(sqls.DB(), state)
}

func toFastGPTUsageEvent(knowledgeBase *models.KnowledgeBase, tenant *models.FastGPTStoreTenant, item fastgptapi.UsageEvent) models.AIUsageEvent {
	metricSource := AIUsageMetricSourceProviderOperation
	provider := "fastgpt"
	modelSource := "fastgpt_operation"
	if item.Kind == "model" {
		metricSource = AIUsageMetricSourceUpstreamActual
		provider = firstNonBlank(item.Provider, "fastgpt")
		modelSource = "fastgpt_profile"
	}
	status := "completed"
	if item.Status == "error" || item.Status == "failed" || item.Status == "blocked" {
		status = "failed"
	}
	createdAt := item.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return models.AIUsageEvent{
		TenantID:           knowledgeBase.TenantID,
		EventKey:           fmt.Sprintf("fastgpt:%s:%s", tenant.TenantTeamID, item.ExternalEventID),
		CompanyID:          knowledgeBase.CompanyID,
		StoreID:            knowledgeBase.StoreID,
		KnowledgeBaseID:    knowledgeBase.ID,
		RequestID:          item.RequestID,
		Stage:              firstNonBlank(item.Stage, "knowledge_operation"),
		Provider:           provider,
		Model:              item.Model,
		ModelSource:        modelSource,
		UpstreamRequestID:  item.ExternalEventID,
		PromptTokens:       item.PromptTokens,
		CompletionTokens:   item.CompletionTokens,
		CachedPromptTokens: item.CachedTokens,
		ReasoningTokens:    item.ReasoningTokens,
		OperationType:      item.OperationType,
		RequestCount:       item.RequestCount,
		RerankCount:        item.RerankCount,
		TrainingCount:      item.TrainingCount,
		FileBytes:          item.FileBytes,
		MetricSource:       metricSource,
		LatencyMS:          item.LatencyMS,
		Status:             status,
		ErrorMessage:       strings.TrimSpace(item.ErrorClass),
		CreatedAt:          createdAt,
	}
}
