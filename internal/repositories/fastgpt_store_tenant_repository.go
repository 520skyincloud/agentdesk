package repositories

import (
	"errors"

	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var FastGPTStoreTenantRepository = newFastGPTStoreTenantRepository()

type fastGPTStoreTenantRepository struct{}

func newFastGPTStoreTenantRepository() *fastGPTStoreTenantRepository {
	return &fastGPTStoreTenantRepository{}
}

func (r *fastGPTStoreTenantRepository) GetByStoreIDInTenant(db *gorm.DB, storeID, tenantID int64) *models.FastGPTStoreTenant {
	if storeID <= 0 || tenantID <= 0 {
		return nil
	}
	item := &models.FastGPTStoreTenant{}
	if err := db.Take(item, "store_id = ? AND tenant_id = ?", storeID, tenantID).Error; err != nil {
		return nil
	}
	return item
}

func (r *fastGPTStoreTenantRepository) GetForUpdateByStoreIDInTenant(db *gorm.DB, storeID, tenantID int64) (*models.FastGPTStoreTenant, error) {
	if db == nil || storeID <= 0 || tenantID <= 0 {
		return nil, errors.New("fastgpt store tenant scope is required")
	}
	item := &models.FastGPTStoreTenant{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(item, "store_id = ? AND tenant_id = ?", storeID, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *fastGPTStoreTenantRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	if db == nil || id <= 0 || tenantID <= 0 {
		return errors.New("fastgpt store tenant id and tenant are required")
	}
	return db.Model(&models.FastGPTStoreTenant{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *fastGPTStoreTenantRepository) MarkActivationFailedIfAppliedRevision(db *gorm.DB, tenantID, storeID, credentialRevision int64, columns map[string]any) error {
	if db == nil || tenantID <= 0 || storeID <= 0 || credentialRevision <= 0 {
		return errors.New("fastgpt activation failure scope is required")
	}
	return db.Model(&models.FastGPTStoreTenant{}).
		Where("tenant_id = ? AND store_id = ? AND applied_credential_revision = ?", tenantID, storeID, credentialRevision).
		Updates(columns).Error
}

func (r *fastGPTStoreTenantRepository) ApplyTargetRevisions(db *gorm.DB, tenantID, storeID, profileID, profileRevision, credentialRevision int64, columns map[string]any) (bool, error) {
	if db == nil || tenantID <= 0 || storeID <= 0 || profileID <= 0 || profileRevision <= 0 || credentialRevision <= 0 {
		return false, errors.New("FastGPT target revision scope is required")
	}
	result := db.Model(&models.FastGPTStoreTenant{}).
		Where("tenant_id = ? AND store_id = ? AND target_profile_id = ? AND target_profile_revision = ? AND target_credential_revision = ?",
			tenantID, storeID, profileID, profileRevision, credentialRevision).
		Updates(columns)
	return result.RowsAffected == 1, result.Error
}

func (r *fastGPTStoreTenantRepository) Save(db *gorm.DB, item *models.FastGPTStoreTenant) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "store_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"tenant_team_id", "tenant_team_name", "status", "last_synced_at", "last_error",
			"target_profile_id", "target_profile_revision", "applied_profile_id", "applied_profile_revision",
			"target_credential_revision", "applied_credential_revision", "applied_key_fingerprint", "readiness_status",
			"updated_at", "update_user_id", "update_user_name",
		}),
	}).Create(item).Error
}

func (r *fastGPTStoreTenantRepository) CreateIfAbsent(db *gorm.DB, item *models.FastGPTStoreTenant) (bool, error) {
	if db == nil || item == nil || item.TenantID <= 0 || item.StoreID <= 0 {
		return false, errors.New("fastgpt store tenant scope is required")
	}
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "store_id"}},
		DoNothing: true,
	}).Create(item)
	return result.RowsAffected == 1, result.Error
}
