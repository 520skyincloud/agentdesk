package repositories

import (
	"agent-desk/internal/models"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var AIUsageEventRepository = newAIUsageEventRepository()

type aiUsageEventRepository struct{}

type AIUsageEvidenceQuery struct {
	TenantIDs []int64
	StoreIDs  []int64
	StartAt   time.Time
	EndAt     time.Time
	ModelName string
	RequestID string
	Limit     int
}

type AIUsageEvidenceAggregate struct {
	EventCount         int64
	RequestCount       int64
	FailedCount        int64
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

func (r *aiUsageEventRepository) FindEvidence(db *gorm.DB, query AIUsageEvidenceQuery) (list []models.AIUsageEvent) {
	if db == nil || len(query.TenantIDs) == 0 || len(query.StoreIDs) == 0 {
		return list
	}
	db = applyAIUsageEvidenceFilters(db.Model(&models.AIUsageEvent{}), query)
	limit := query.Limit
	if limit <= 0 {
		limit = 500
	}
	db.Order("created_at DESC").Order("id DESC").Limit(limit).Find(&list)
	return list
}

func (r *aiUsageEventRepository) AggregateEvidence(db *gorm.DB, query AIUsageEvidenceQuery) (result AIUsageEvidenceAggregate) {
	if db == nil || len(query.TenantIDs) == 0 || len(query.StoreIDs) == 0 {
		return result
	}
	applyAIUsageEvidenceFilters(db.Model(&models.AIUsageEvent{}), query).
		Select(`COUNT(*) AS event_count,
			SUM(CASE WHEN request_count > 0 THEN request_count ELSE 1 END) AS request_count,
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed_count,
			SUM(prompt_tokens) AS prompt_tokens,
			SUM(completion_tokens) AS completion_tokens,
			SUM(cached_prompt_tokens) AS cached_prompt_tokens`).
		Scan(&result)
	return result
}

func applyAIUsageEvidenceFilters(db *gorm.DB, query AIUsageEvidenceQuery) *gorm.DB {
	db = db.Where("tenant_id IN ? AND store_id IN ?", query.TenantIDs, query.StoreIDs)
	if !query.StartAt.IsZero() {
		db = db.Where("created_at >= ?", query.StartAt)
	}
	if !query.EndAt.IsZero() {
		db = db.Where("created_at < ?", query.EndAt)
	}
	if modelName := strings.ToLower(strings.TrimSpace(query.ModelName)); modelName != "" {
		db = db.Where("LOWER(model) = ?", modelName)
	}
	if requestID := strings.TrimSpace(query.RequestID); requestID != "" {
		db = db.Where("(request_id = ? OR upstream_request_id = ? OR gateway_request_id = ? OR gateway_upstream_id = ?)", requestID, requestID, requestID, requestID)
	}
	return db
}
