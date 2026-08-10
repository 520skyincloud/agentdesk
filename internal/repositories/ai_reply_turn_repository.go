package repositories

import (
	"errors"

	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var AIReplyTurnRepository = newAIReplyTurnRepository()

type aiReplyTurnRepository struct{}

func newAIReplyTurnRepository() *aiReplyTurnRepository {
	return &aiReplyTurnRepository{}
}

func (r *aiReplyTurnRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.AIReplyTurn {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.AIReplyTurn{}
	if err := db.Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *aiReplyTurnRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.AIReplyTurn, error) {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	ret := &models.AIReplyTurn{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *aiReplyTurnRepository) Create(db *gorm.DB, item *models.AIReplyTurn) error {
	return db.Create(item).Error
}

func (r *aiReplyTurnRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.AIReplyTurn{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}
