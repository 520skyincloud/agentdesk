package repositories

import (
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

func (r *fastGPTUsageSyncStateRepository) Save(db *gorm.DB, item *models.FastGPTUsageSyncState) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "knowledge_base_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"company_id", "store_id", "tenant_team_id", "cursor", "last_synced_at", "last_error", "updated_at",
		}),
	}).Create(item).Error
}
