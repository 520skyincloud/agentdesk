package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var AIUsageEventRepository = newAIUsageEventRepository()

type aiUsageEventRepository struct{}

func newAIUsageEventRepository() *aiUsageEventRepository { return &aiUsageEventRepository{} }

func (r *aiUsageEventRepository) CreateIfAbsent(db *gorm.DB, item *models.AIUsageEvent) error {
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(item).Error
}

func (r *aiUsageEventRepository) TakeByEventKey(db *gorm.DB, eventKey string) *models.AIUsageEvent {
	item := &models.AIUsageEvent{}
	if err := db.Take(item, "event_key = ?", eventKey).Error; err != nil {
		return nil
	}
	return item
}
