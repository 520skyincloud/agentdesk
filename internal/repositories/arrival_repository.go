package repositories

import (
	"errors"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ArrivalRepository = &arrivalRepository{}

type arrivalRepository struct{}

func (r *arrivalRepository) FindMiniProgramIdentityByOpenFingerprint(db *gorm.DB, tenantID int64, appID, fingerprint string) *models.MiniProgramIdentity {
	ret := &models.MiniProgramIdentity{}
	if err := db.Take(ret, "tenant_id = ? AND app_id = ? AND open_id_fingerprint = ?", tenantID, appID, fingerprint).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) GetMiniProgramIdentity(db *gorm.DB, id, tenantID int64) *models.MiniProgramIdentity {
	ret := &models.MiniProgramIdentity{}
	if err := db.Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) CreateMiniProgramIdentity(db *gorm.DB, item *models.MiniProgramIdentity) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) UpdateMiniProgramIdentity(db *gorm.DB, id, tenantID int64, updates map[string]any) error {
	return db.Model(&models.MiniProgramIdentity{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates).Error
}

func (r *arrivalRepository) FindSuiteCredential(db *gorm.DB, suiteID string) *models.WeComSuiteCredential {
	ret := &models.WeComSuiteCredential{}
	if err := db.Take(ret, "suite_id = ?", suiteID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) GetSuiteCredential(db *gorm.DB, id int64) *models.WeComSuiteCredential {
	ret := &models.WeComSuiteCredential{}
	if err := db.Take(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) CreateSuiteCredential(db *gorm.DB, item *models.WeComSuiteCredential) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) UpdateSuiteCredential(db *gorm.DB, id int64, updates map[string]any) error {
	return db.Model(&models.WeComSuiteCredential{}).Where("id = ?", id).Updates(updates).Error
}

func (r *arrivalRepository) FindTenantAuthorizationByCorpFingerprint(db *gorm.DB, suiteCredentialID int64, fingerprint string) *models.WeComTenantAuthorization {
	ret := &models.WeComTenantAuthorization{}
	if err := db.Take(ret, "suite_credential_id = ? AND corp_id_fingerprint = ?", suiteCredentialID, fingerprint).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) FindTenantAuthorizationByCorpFingerprintInTenant(db *gorm.DB, tenantID, suiteCredentialID int64, fingerprint string) *models.WeComTenantAuthorization {
	ret := &models.WeComTenantAuthorization{}
	if err := db.Take(ret, "tenant_id = ? AND suite_credential_id = ? AND corp_id_fingerprint = ?", tenantID, suiteCredentialID, fingerprint).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) GetTenantAuthorization(db *gorm.DB, id, tenantID int64) *models.WeComTenantAuthorization {
	ret := &models.WeComTenantAuthorization{}
	if err := db.Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) CreateTenantAuthorization(db *gorm.DB, item *models.WeComTenantAuthorization) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) UpdateTenantAuthorization(db *gorm.DB, id, tenantID int64, updates map[string]any) error {
	return db.Model(&models.WeComTenantAuthorization{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates).Error
}

func (r *arrivalRepository) ClearCorpAccessTokenIfMatches(
	db *gorm.DB,
	id, tenantID int64,
	expectedCiphertext string,
	now time.Time,
) (bool, error) {
	result := db.Model(&models.WeComTenantAuthorization{}).
		Where(
			"id = ? AND tenant_id = ? AND corp_access_token_ciphertext = ?",
			id,
			tenantID,
			expectedCiphertext,
		).
		Updates(map[string]any{
			"corp_access_token_ciphertext": "",
			"corp_access_token_nonce":      "",
			"corp_access_token_expires_at": nil,
			"updated_at":                   now,
			"update_user_name":             "arrival_provider",
		})
	return result.RowsAffected == 1, result.Error
}

func (r *arrivalRepository) FindTenantAuthorizations(db *gorm.DB, cnd *sqls.Cnd) []models.WeComTenantAuthorization {
	ret := make([]models.WeComTenantAuthorization, 0)
	cnd.Find(db, &ret)
	return ret
}

func (r *arrivalRepository) FindConnectionsByAuthorization(db *gorm.DB, authorizationID int64) []models.StoreArrivalConnection {
	ret := make([]models.StoreArrivalConnection, 0)
	db.Where("tenant_authorization_id = ?", authorizationID).Find(&ret)
	return ret
}

func (r *arrivalRepository) FindConnectionByScene(db *gorm.DB, scene string) *models.StoreArrivalConnection {
	ret := &models.StoreArrivalConnection{}
	if err := db.Take(ret, "store_scene = ?", scene).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) FindConnectionByStore(db *gorm.DB, tenantID, storeID int64) *models.StoreArrivalConnection {
	ret := &models.StoreArrivalConnection{}
	if err := db.Take(ret, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) GetConnection(db *gorm.DB, id, tenantID int64) *models.StoreArrivalConnection {
	ret := &models.StoreArrivalConnection{}
	if err := db.Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) FindConnections(db *gorm.DB, cnd *sqls.Cnd) (list []models.StoreArrivalConnection, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.StoreArrivalConnection{})
	return list, &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
}

func (r *arrivalRepository) CreateConnection(db *gorm.DB, item *models.StoreArrivalConnection) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) UpdateConnection(db *gorm.DB, id, tenantID int64, updates map[string]any) error {
	return db.Model(&models.StoreArrivalConnection{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates).Error
}

func (r *arrivalRepository) CountConnections(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.StoreArrivalConnection{})
}

func (r *arrivalRepository) FindInvitationByHash(db *gorm.DB, tokenHash string) *models.StoreArrivalInvitation {
	ret := &models.StoreArrivalInvitation{}
	if err := db.Take(ret, "token_hash = ?", tokenHash).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) FindActiveInvitationByStore(db *gorm.DB, tenantID, storeID int64) *models.StoreArrivalInvitation {
	ret := &models.StoreArrivalInvitation{}
	if err := db.Where("tenant_id = ? AND store_id = ? AND status = ?", tenantID, storeID, 0).
		Order("id DESC").Take(ret).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) CreateInvitation(db *gorm.DB, item *models.StoreArrivalInvitation) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) UpdateInvitation(db *gorm.DB, id, tenantID int64, updates map[string]any) error {
	return db.Model(&models.StoreArrivalInvitation{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates).Error
}

func (r *arrivalRepository) FindAuthorizationAttemptByStateHash(db *gorm.DB, stateHash string) *models.WeComAuthorizationAttempt {
	ret := &models.WeComAuthorizationAttempt{}
	if err := db.Take(ret, "state_hash = ?", stateHash).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) GetAuthorizationAttempt(db *gorm.DB, id, tenantID int64) *models.WeComAuthorizationAttempt {
	ret := &models.WeComAuthorizationAttempt{}
	if err := db.Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) CreateAuthorizationAttempt(db *gorm.DB, item *models.WeComAuthorizationAttempt) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) UpdateAuthorizationAttempt(db *gorm.DB, id, tenantID int64, updates map[string]any) error {
	return db.Model(&models.WeComAuthorizationAttempt{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates).Error
}

func (r *arrivalRepository) FindScanEventByHash(db *gorm.DB, scanHash string) *models.ArrivalScanEvent {
	ret := &models.ArrivalScanEvent{}
	if err := db.Take(ret, "scan_event_hash = ?", scanHash).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) GetScanEvent(db *gorm.DB, id, tenantID int64) *models.ArrivalScanEvent {
	ret := &models.ArrivalScanEvent{}
	if err := db.Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) FindRecentSentScanEvent(db *gorm.DB, tenantID, storeID, identityID int64, after any) *models.ArrivalScanEvent {
	ret := &models.ArrivalScanEvent{}
	if err := db.Where(
		"tenant_id = ? AND store_id = ? AND mini_program_identity_id = ? AND delivery_status = ? AND delivery_completed_at >= ?",
		tenantID, storeID, identityID, "sent", after,
	).Order("delivery_completed_at DESC").Take(ret).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) FindPendingScanEvents(db *gorm.DB, limit int) []models.ArrivalScanEvent {
	ret := make([]models.ArrivalScanEvent, 0)
	db.Where("binding_status = ? AND status = ?", "legacy_unmapped", 0).
		Order("id ASC").Limit(limit).Find(&ret)
	return ret
}

func (r *arrivalRepository) CreateScanEvent(db *gorm.DB, item *models.ArrivalScanEvent) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) UpdateScanEvent(db *gorm.DB, id, tenantID int64, updates map[string]any) error {
	return db.Model(&models.ArrivalScanEvent{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates).Error
}

func (r *arrivalRepository) TryClaimScanEventDelivery(db *gorm.DB, id, tenantID int64, now time.Time) (bool, error) {
	result := db.Model(&models.ArrivalScanEvent{}).
		Where("id = ? AND tenant_id = ? AND delivery_attempted_at IS NULL", id, tenantID).
		Updates(map[string]any{
			"delivery_status":       enums.ArrivalDeliveryStatusFailed,
			"delivery_attempted_at": now,
			"delivery_error_code":   "delivery_interrupted",
			"updated_at":            now,
			"update_user_name":      "arrival",
		})
	return result.RowsAffected == 1, result.Error
}

func (r *arrivalRepository) FindSessionByScanEvent(db *gorm.DB, scanEventID int64) *models.ArrivalSession {
	ret := &models.ArrivalSession{}
	if err := db.Take(ret, "scan_event_id = ?", scanEventID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) GetSession(db *gorm.DB, id int64) *models.ArrivalSession {
	ret := &models.ArrivalSession{}
	if err := db.Take(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) CreateSession(db *gorm.DB, item *models.ArrivalSession) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) UpdateSession(db *gorm.DB, id int64, updates map[string]any) error {
	return db.Model(&models.ArrivalSession{}).Where("id = ?", id).Updates(updates).Error
}

func (r *arrivalRepository) FindContactWayByScanEvent(db *gorm.DB, scanEventID int64) *models.ArrivalContactWay {
	ret := &models.ArrivalContactWay{}
	if err := db.Take(ret, "scan_event_id = ?", scanEventID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) GetContactWay(db *gorm.DB, id, tenantID int64) *models.ArrivalContactWay {
	ret := &models.ArrivalContactWay{}
	if err := db.Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) FindContactWayByStateHash(db *gorm.DB, stateHash string) *models.ArrivalContactWay {
	ret := &models.ArrivalContactWay{}
	if err := db.Take(ret, "contact_state_hash = ?", stateHash).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) FindContactWayByPublicTokenHash(db *gorm.DB, tokenHash string) *models.ArrivalContactWay {
	ret := &models.ArrivalContactWay{}
	if err := db.Take(ret, "public_resource_token_hash = ?", tokenHash).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) CreateContactWay(db *gorm.DB, item *models.ArrivalContactWay) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) UpdateContactWay(db *gorm.DB, id, tenantID int64, updates map[string]any) error {
	return db.Model(&models.ArrivalContactWay{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates).Error
}

func (r *arrivalRepository) TryClaimContactWayProvision(
	db *gorm.DB,
	id, tenantID int64,
	now, staleBefore time.Time,
	requestID string,
	maxAttempts int,
) (bool, error) {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	result := db.Model(&models.ArrivalContactWay{}).
		Where(
			"id = ? AND tenant_id = ? AND status = ? AND provision_attempt_count < ? AND (expires_at IS NULL OR expires_at > ?)",
			id,
			tenantID,
			enums.StatusOk,
			maxAttempts,
			now,
		).
		Where(
			"(contact_way_status = ? AND ((failure_retryable = ? AND (next_provision_retry_at IS NULL OR next_provision_retry_at <= ?)) OR (failure_code = ? AND provision_attempt_count = 0))) OR (contact_way_status = ? AND (last_provision_attempt_at IS NULL OR last_provision_attempt_at <= ?))",
			enums.ArrivalContactWayStatusFailed,
			true,
			now,
			"contact_way_api_failed",
			enums.ArrivalContactWayStatusProvisioning,
			staleBefore,
		).
		Updates(map[string]any{
			"contact_way_status":        enums.ArrivalContactWayStatusProvisioning,
			"provision_attempt_count":   gorm.Expr("CASE WHEN provision_attempt_count < 1 THEN 2 ELSE provision_attempt_count + 1 END"),
			"last_provision_request_id": requestID,
			"last_provision_attempt_at": now,
			"next_provision_retry_at":   nil,
			"updated_at":                now,
			"update_user_name":          "arrival",
		})
	return result.RowsAffected == 1, result.Error
}

func (r *arrivalRepository) FindContactWays(db *gorm.DB, cnd *sqls.Cnd) []models.ArrivalContactWay {
	ret := make([]models.ArrivalContactWay, 0)
	cnd.Find(db, &ret)
	return ret
}

func (r *arrivalRepository) FindContactWaysDueForProvision(
	db *gorm.DB,
	now, staleBefore time.Time,
	maxAttempts, limit int,
) []models.ArrivalContactWay {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if limit <= 0 {
		limit = 50
	}
	ret := make([]models.ArrivalContactWay, 0)
	db.Where(
		"status = ? AND provision_attempt_count < ? AND (expires_at IS NULL OR expires_at > ?)",
		enums.StatusOk,
		maxAttempts,
		now,
	).
		Where(
			"(contact_way_status = ? AND ((failure_retryable = ? AND (next_provision_retry_at IS NULL OR next_provision_retry_at <= ?)) OR (failure_code = ? AND provision_attempt_count = 0))) OR (contact_way_status = ? AND (last_provision_attempt_at IS NULL OR last_provision_attempt_at <= ?))",
			enums.ArrivalContactWayStatusFailed,
			true,
			now,
			"contact_way_api_failed",
			enums.ArrivalContactWayStatusProvisioning,
			staleBefore,
		).
		Order("last_provision_attempt_at ASC, id ASC").
		Limit(limit).
		Find(&ret)
	return ret
}

func (r *arrivalRepository) FindContactWaysDueForCleanup(db *gorm.DB, now time.Time, limit int) []models.ArrivalContactWay {
	if limit <= 0 {
		limit = 50
	}
	ret := make([]models.ArrivalContactWay, 0)
	db.Where(
		"expires_at IS NOT NULL AND expires_at <= ? AND contact_way_status IN ? AND status = ?",
		now,
		[]enums.ArrivalContactWayStatus{
			enums.ArrivalContactWayStatusActive,
			enums.ArrivalContactWayStatusExpired,
			enums.ArrivalContactWayStatusFailed,
		},
		enums.StatusOk,
	).Order("expires_at ASC, id ASC").Limit(limit).Find(&ret)
	return ret
}

func (r *arrivalRepository) FindBinding(db *gorm.DB, tenantID, identityID, storeID int64) *models.ArrivalStoreBinding {
	ret := &models.ArrivalStoreBinding{}
	if err := db.Take(ret, "tenant_id = ? AND mini_program_identity_id = ? AND store_id = ?", tenantID, identityID, storeID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) GetBinding(db *gorm.DB, id, tenantID int64) *models.ArrivalStoreBinding {
	ret := &models.ArrivalStoreBinding{}
	if err := db.Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) CreateBinding(db *gorm.DB, item *models.ArrivalStoreBinding) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) UpdateBinding(db *gorm.DB, id, tenantID int64, updates map[string]any) error {
	return db.Model(&models.ArrivalStoreBinding{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates).Error
}

func (r *arrivalRepository) FindBindingsByOfficialIdentity(db *gorm.DB, tenantID, authorizationID int64, externalFingerprint, memberFingerprint string) []models.ArrivalStoreBinding {
	ret := make([]models.ArrivalStoreBinding, 0)
	db.Where(
		"tenant_id = ? AND tenant_authorization_id = ? AND external_user_id_fingerprint = ? AND contact_member_fingerprint = ?",
		tenantID, authorizationID, externalFingerprint, memberFingerprint,
	).Find(&ret)
	return ret
}

func (r *arrivalRepository) CreateCallbackEventIfAbsent(db *gorm.DB, item *models.WeComProviderCallbackEvent) (bool, error) {
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "event_hash"}},
		DoNothing: true,
	}).Create(item)
	return result.RowsAffected == 1, result.Error
}

func (r *arrivalRepository) FindCallbackEventByHash(db *gorm.DB, eventHash string) *models.WeComProviderCallbackEvent {
	ret := &models.WeComProviderCallbackEvent{}
	if err := db.Take(ret, "event_hash = ?", eventHash).Error; err != nil {
		return nil
	}
	return ret
}

func (r *arrivalRepository) UpdateCallbackEvent(db *gorm.DB, id int64, updates map[string]any) error {
	return db.Model(&models.WeComProviderCallbackEvent{}).Where("id = ?", id).Updates(updates).Error
}

func (r *arrivalRepository) CreateAuditLog(db *gorm.DB, item *models.ArrivalAuditLog) error {
	return db.Create(item).Error
}

func (r *arrivalRepository) FindAuditLogs(db *gorm.DB, cnd *sqls.Cnd) (list []models.ArrivalAuditLog, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.ArrivalAuditLog{})
	return list, &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
}

func (r *arrivalRepository) CountScanEvents(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.ArrivalScanEvent{})
}

func arrivalRepositoryRecordNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
