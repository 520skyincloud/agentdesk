package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var AIUsageEventRepository = newAIUsageEventRepository()

type aiUsageEventRepository struct{}

type AIUsageAggregate struct {
	AIConfigID         int64
	RequestCount       int64
	PromptTokens       int64
	CompletionTokens   int64
	CachedPromptTokens int64
}

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

func (r *aiUsageEventRepository) AggregateByTenantAndAIConfig(db *gorm.DB, tenantID int64) (list []AIUsageAggregate) {
	if db == nil || tenantID <= 0 {
		return nil
	}
	db.Model(&models.AIUsageEvent{}).
		Select("ai_config_id, SUM(request_count) AS request_count, SUM(prompt_tokens) AS prompt_tokens, SUM(completion_tokens) AS completion_tokens, SUM(cached_prompt_tokens) AS cached_prompt_tokens").
		Where("tenant_id = ? AND ai_config_id > 0", tenantID).
		Group("ai_config_id").
		Scan(&list)
	return
}
