package repositories

import (
	"agent-desk/internal/models"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var PMSOperationRepository = newPMSOperationRepository()

type pmsOperationRepository struct{}

func newPMSOperationRepository() *pmsOperationRepository {
	return &pmsOperationRepository{}
}

func (r *pmsOperationRepository) Get(db *gorm.DB, id int64) *models.PMSOperation {
	item := &models.PMSOperation{}
	if db == nil || id <= 0 || db.Where("id = ?", id).Take(item).Error != nil {
		return nil
	}
	return item
}

func (r *pmsOperationRepository) FindPendingByConversationID(db *gorm.DB, conversationID int64) *models.PMSOperation {
	item := &models.PMSOperation{}
	if db == nil || conversationID <= 0 ||
		db.Where("conversation_id = ? AND status = ?", conversationID, "pending").
			Order("id DESC").Take(item).Error != nil {
		return nil
	}
	return item
}

func (r *pmsOperationRepository) FindByIdempotencyKeyForUpdate(db *gorm.DB, key string) *models.PMSOperation {
	item := &models.PMSOperation{}
	if db == nil || key == "" ||
		db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("idempotency_key = ?", key).Take(item).Error != nil {
		return nil
	}
	return item
}

func (r *pmsOperationRepository) Create(db *gorm.DB, item *models.PMSOperation) error {
	return db.Create(item).Error
}

func (r *pmsOperationRepository) Updates(db *gorm.DB, id int64, values map[string]any) error {
	return db.Model(&models.PMSOperation{}).Where("id = ?", id).Updates(values).Error
}

func (r *pmsOperationRepository) ListExpired(db *gorm.DB, now time.Time, limit int) []models.PMSOperation {
	if limit <= 0 {
		limit = 100
	}
	var items []models.PMSOperation
	db.Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", "pending", now).
		Order("id ASC").Limit(limit).Find(&items)
	return items
}
