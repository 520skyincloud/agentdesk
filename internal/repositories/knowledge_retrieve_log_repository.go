package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var KnowledgeRetrieveLogRepository = newKnowledgeRetrieveLogRepository()

type knowledgeRetrieveLogRepository struct{}

func newKnowledgeRetrieveLogRepository() *knowledgeRetrieveLogRepository {
	return &knowledgeRetrieveLogRepository{}
}

func (r *knowledgeRetrieveLogRepository) FindRecentQuestions(db *gorm.DB, limit int) ([]models.KnowledgeRetrieveLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	items := make([]models.KnowledgeRetrieveLog, 0, limit)
	err := db.Where("knowledge_base_id > 0 AND question <> ''").Order("id DESC").Limit(limit).Find(&items).Error
	return items, err
}
