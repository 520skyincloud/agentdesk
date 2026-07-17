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

func (r *fastGPTUsageSyncStateRepository) GetByKnowledgeBaseID(db *gorm.DB, knowledgeBaseID int64) *models.FastGPTUsageSyncState {
	item := &models.FastGPTUsageSyncState{}
	if err := db.Take(item, "knowledge_base_id = ?", knowledgeBaseID).Error; err != nil {
		return nil
	}
	return item
}

func (r *fastGPTUsageSyncStateRepository) Save(db *gorm.DB, item *models.FastGPTUsageSyncState) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "knowledge_base_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"company_id", "store_id", "tenant_team_id", "cursor", "last_synced_at", "last_error", "updated_at",
		}),
	}).Create(item).Error
}
