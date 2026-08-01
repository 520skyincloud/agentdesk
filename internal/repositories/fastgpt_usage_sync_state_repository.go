package repositories

import (
	"errors"
	"time"

	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var FastGPTUsageSyncStateRepository = newFastGPTUsageSyncStateRepository()

type fastGPTUsageSyncStateRepository struct{}

func newFastGPTUsageSyncStateRepository() *fastGPTUsageSyncStateRepository {
	return &fastGPTUsageSyncStateRepository{}
}

func (r *fastGPTUsageSyncStateRepository) GetByKnowledgeBaseIDInTenant(db *gorm.DB, knowledgeBaseID, tenantID int64) *models.FastGPTUsageSyncState {
	if knowledgeBaseID <= 0 || tenantID <= 0 {
		return nil
	}
	item := &models.FastGPTUsageSyncState{}
	if err := db.Take(item, "knowledge_base_id = ? AND tenant_id = ?", knowledgeBaseID, tenantID).Error; err != nil {
		return nil
	}
	return item
}

func (r *fastGPTUsageSyncStateRepository) CreateIfAbsent(db *gorm.DB, item *models.FastGPTUsageSyncState) (bool, error) {
	if db == nil || item == nil || item.TenantID <= 0 || item.KnowledgeBaseID <= 0 {
		return false, errors.New("FastGPT usage sync state scope is required")
	}
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "knowledge_base_id"}},
		DoNothing: true,
	}).Create(item)
	return result.RowsAffected == 1, result.Error
}

// CompareAndSwap advances one cursor window only while the database still
// contains the exact snapshot read by the worker. Opaque FastGPT cursors cannot
// be ordered, so stale workers must never perform an unconditional update.
func (r *fastGPTUsageSyncStateRepository) CompareAndSwap(db *gorm.DB, expected, next *models.FastGPTUsageSyncState) (bool, error) {
	if db == nil || expected == nil || next == nil || expected.ID <= 0 || expected.TenantID <= 0 || expected.KnowledgeBaseID <= 0 ||
		expected.ID != next.ID || expected.TenantID != next.TenantID || expected.KnowledgeBaseID != next.KnowledgeBaseID {
		return false, errors.New("FastGPT usage cursor compare-and-swap scope is invalid")
	}
	result := db.Model(&models.FastGPTUsageSyncState{}).
		Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?", expected.ID, expected.TenantID, expected.KnowledgeBaseID).
		Where("store_id = ? AND tenant_team_id = ?", expected.StoreID, expected.TenantTeamID).
		Where("cursor = ? AND store_staff_binding_id = ? AND model_profile_id = ? AND profile_revision = ? AND credential_revision = ?", expected.Cursor, expected.StoreStaffBindingID, expected.ModelProfileID, expected.ProfileRevision, expected.CredentialRevision).
		Where("key_fingerprint = ? AND fast_gpt_profile_id = ? AND fast_gpt_revision = ?", expected.KeyFingerprint, expected.FastGPTProfileID, expected.FastGPTRevision).
		Updates(map[string]any{
			"store_id": next.StoreID, "tenant_team_id": next.TenantTeamID,
			"store_staff_binding_id": next.StoreStaffBindingID,
			"cursor":                 next.Cursor, "model_profile_id": next.ModelProfileID, "profile_revision": next.ProfileRevision,
			"credential_revision": next.CredentialRevision, "key_fingerprint": next.KeyFingerprint,
			"fast_gpt_profile_id": next.FastGPTProfileID, "fast_gpt_revision": next.FastGPTRevision,
			"last_synced_at": next.LastSyncedAt, "last_error": next.LastError, "updated_at": next.UpdatedAt,
		})
	return result.RowsAffected == 1, result.Error
}

// SaveFailure records only a stable error class while the cursor snapshot is
// unchanged. A late failed worker must not make a newer successful window look
// unhealthy, even when it leaves the cursor and attribution columns untouched.
func (r *fastGPTUsageSyncStateRepository) SaveFailure(db *gorm.DB, item *models.FastGPTUsageSyncState, errorClass string, updatedAt time.Time) (bool, error) {
	if db == nil || item == nil || item.TenantID <= 0 || item.KnowledgeBaseID <= 0 {
		return false, errors.New("FastGPT usage failure scope is required")
	}
	if item.ID <= 0 {
		initial := *item
		initial.LastError = errorClass
		initial.UpdatedAt = updatedAt
		return r.CreateIfAbsent(db, &initial)
	}
	result := db.Model(&models.FastGPTUsageSyncState{}).
		Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?", item.ID, item.TenantID, item.KnowledgeBaseID).
		Where("store_id = ? AND tenant_team_id = ?", item.StoreID, item.TenantTeamID).
		Where("cursor = ? AND store_staff_binding_id = ? AND model_profile_id = ? AND profile_revision = ? AND credential_revision = ?", item.Cursor, item.StoreStaffBindingID, item.ModelProfileID, item.ProfileRevision, item.CredentialRevision).
		Where("key_fingerprint = ? AND fast_gpt_profile_id = ? AND fast_gpt_revision = ?", item.KeyFingerprint, item.FastGPTProfileID, item.FastGPTRevision).
		Where("updated_at = ?", item.UpdatedAt).
		Updates(map[string]any{"last_error": errorClass, "updated_at": updatedAt})
	return result.RowsAffected == 1, result.Error
}
