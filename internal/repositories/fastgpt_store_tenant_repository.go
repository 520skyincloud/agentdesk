package repositories

import (
	"agent-desk/internal/models"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var FastGPTStoreTenantRepository = newFastGPTStoreTenantRepository()

type fastGPTStoreTenantRepository struct{}

func newFastGPTStoreTenantRepository() *fastGPTStoreTenantRepository {
	return &fastGPTStoreTenantRepository{}
}

func (r *fastGPTStoreTenantRepository) GetByStoreID(db *gorm.DB, storeID int64) *models.FastGPTStoreTenant {
	item := &models.FastGPTStoreTenant{}
	if err := db.Take(item, "store_id = ?", storeID).Error; err != nil {
		return nil
	}
	return item
}

func (r *fastGPTStoreTenantRepository) Save(db *gorm.DB, item *models.FastGPTStoreTenant) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "store_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"company_id", "tenant_team_id", "tenant_team_name", "status", "last_synced_at", "last_error",
			"updated_at", "update_user_id", "update_user_name",
		}),
	}).Create(item).Error
}

func (r *fastGPTStoreTenantRepository) FindTemplateSyncDue(db *gorm.DB, now time.Time, limit int) (list []models.FastGPTStoreTenant) {
	if limit <= 0 {
		limit = 20
	}
	db.Where("status = ?", "active").
		Where("profile_template_target_revision > profile_template_revision").
		Where("profile_template_sync_status IN ?", []string{"pending", "failed", "syncing"}).
		Where("profile_template_next_retry_at IS NULL OR profile_template_next_retry_at <= ?", now).
		Order("profile_template_next_retry_at ASC, id ASC").
		Limit(limit).
		Find(&list)
	return
}

func (r *fastGPTStoreTenantRepository) FindByStoreIDs(db *gorm.DB, storeIDs []int64) (list []models.FastGPTStoreTenant) {
	if len(storeIDs) == 0 {
		return nil
	}
	db.Where("store_id IN ?", storeIDs).Order("store_id ASC").Find(&list)
	return
}

func (r *fastGPTStoreTenantRepository) QueueTemplateSync(db *gorm.DB, storeIDs []int64, targetRevision int64, now time.Time) error {
	if len(storeIDs) == 0 {
		return nil
	}
	return db.Model(&models.FastGPTStoreTenant{}).
		Where("store_id IN ? AND status = ?", storeIDs, "active").
		Updates(map[string]any{
			"profile_template_target_revision": targetRevision,
			"profile_template_sync_status":     "pending",
			"profile_template_attempt_count":   0,
			"profile_template_next_retry_at":   now,
			"profile_template_last_error":      "",
			"updated_at":                       now,
		}).Error
}

func (r *fastGPTStoreTenantRepository) UpdateTemplateSync(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.FastGPTStoreTenant{}).Where("id = ?", id).Updates(columns).Error
}
