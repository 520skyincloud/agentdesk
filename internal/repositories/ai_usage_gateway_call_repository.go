package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var AIUsageGatewayCallRepository = newAIUsageGatewayCallRepository()

type aiUsageGatewayCallRepository struct{}

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
