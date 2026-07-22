package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var FastGPTProfileTemplateRepository = newFastGPTProfileTemplateRepository()

type fastGPTProfileTemplateRepository struct{}

func newFastGPTProfileTemplateRepository() *fastGPTProfileTemplateRepository {
	return &fastGPTProfileTemplateRepository{}
}

func (r *fastGPTProfileTemplateRepository) Get(db *gorm.DB) *models.FastGPTProfileTemplate {
	item := &models.FastGPTProfileTemplate{}
	if err := db.First(item, "id = ?", 1).Error; err != nil {
		return nil
	}
	return item
}

func (r *fastGPTProfileTemplateRepository) Save(db *gorm.DB, item *models.FastGPTProfileTemplate) error {
	item.ID = 1
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "revision",
			"chat_provider", "chat_base_url", "chat_model", "chat_api_mode",
			"asr_provider", "asr_base_url", "asr_model",
			"embedding_provider", "embedding_base_url", "embedding_model",
			"document_parser_provider", "document_parser_base_url", "document_parser_model",
			"vision_provider", "vision_base_url", "vision_model",
			"rerank_provider", "rerank_base_url", "rerank_model",
			"status", "updated_at", "update_user_id", "update_user_name",
		}),
	}).Create(item).Error
}
