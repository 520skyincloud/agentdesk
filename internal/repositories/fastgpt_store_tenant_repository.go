package repositories

import (
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

func (r *fastGPTStoreTenantRepository) Save(db *gorm.DB, item *models.FastGPTStoreTenant) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "store_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"company_id", "tenant_team_id", "tenant_team_name", "status", "last_synced_at", "last_error",
			"updated_at", "update_user_id", "update_user_name",
		}),
	}).Create(item).Error
}
