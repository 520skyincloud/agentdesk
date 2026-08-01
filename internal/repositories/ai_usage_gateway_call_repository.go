package repositories

import (
	"agent-desk/internal/models"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var AIUsageGatewayCallRepository = newAIUsageGatewayCallRepository()

type aiUsageGatewayCallRepository struct{}

type AIUsageGatewayEvidenceQuery struct {
	TenantIDs            []int64
	StoreIDs             []int64
	StoreStaffBindingIDs []int64
	StartAt              time.Time
	EndAt                time.Time
	RequestID            string
	Limit                int
}

func newAIUsageGatewayCallRepository() *aiUsageGatewayCallRepository {
	return &aiUsageGatewayCallRepository{}
}

func (r *aiUsageGatewayCallRepository) CreateIfAbsent(db *gorm.DB, item *models.AIUsageGatewayCall) error {
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error
}

func (r *aiUsageGatewayCallRepository) FindPending(db *gorm.DB, gateway string, limit int) []models.AIUsageGatewayCall {
	if limit <= 0 {
		limit = 50
	}
	items := make([]models.AIUsageGatewayCall, 0)
	_ = db.Where("gateway = ? AND reconcile_status IN ?", gateway, []string{"pending", "retry"}).Order("id ASC").Limit(limit).Find(&items).Error
	return items
}

func (r *aiUsageGatewayCallRepository) Updates(db *gorm.DB, id int64, values map[string]any) error {
	return db.Model(&models.AIUsageGatewayCall{}).Where("id = ?", id).Updates(values).Error
}

func (r *aiUsageGatewayCallRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, values map[string]any) error {
	return db.Model(&models.AIUsageGatewayCall{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(values).Error
}

func (r *aiUsageGatewayCallRepository) TakeByGatewayRequestID(db *gorm.DB, gateway string, requestID string) *models.AIUsageGatewayCall {
	item := &models.AIUsageGatewayCall{}
	if err := db.Where("gateway = ? AND gateway_request_id = ?", gateway, requestID).Take(item).Error; err != nil {
		return nil
	}
	return item
}

func (r *aiUsageGatewayCallRepository) FindEvidence(db *gorm.DB, query AIUsageGatewayEvidenceQuery) (list []models.AIUsageGatewayCall) {
	if db == nil || len(query.TenantIDs) == 0 || len(query.StoreIDs) == 0 {
		return list
	}
	db = db.Model(&models.AIUsageGatewayCall{}).
		Where("tenant_id IN ? AND store_id IN ?", query.TenantIDs, query.StoreIDs)
	if len(query.StoreStaffBindingIDs) > 0 {
		db = db.Where("store_staff_binding_id IN ?", query.StoreStaffBindingIDs)
	}
	if !query.StartAt.IsZero() {
		db = db.Where("started_at >= ?", query.StartAt)
	}
	if !query.EndAt.IsZero() {
		db = db.Where("started_at < ?", query.EndAt)
	}
	if requestID := strings.TrimSpace(query.RequestID); requestID != "" {
		db = db.Where("(gateway_request_id = ? OR upstream_request_id = ? OR local_request_id = ?)", requestID, requestID, requestID)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 500
	}
	db.Order("started_at DESC").Order("id DESC").Limit(limit).Find(&list)
	return list
}
