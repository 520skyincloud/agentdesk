package repositories

import (
	"errors"
	"time"

	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var AIReplyTurnActionRepository = &aiReplyTurnActionRepository{}

type aiReplyTurnActionRepository struct{}

func (r *aiReplyTurnActionRepository) CreateIfAbsent(db *gorm.DB, item *models.AIReplyTurnAction) (bool, error) {
	if db == nil || item == nil {
		return false, nil
	}
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "turn_id"}, {Name: "task_key"}, {Name: "action_key"}},
		DoNothing: true,
	}).Create(item)
	return result.RowsAffected == 1, result.Error
}

func (r *aiReplyTurnActionRepository) GetByKeyInTenant(db *gorm.DB, tenantID, turnID int64, taskKey, actionKey string) *models.AIReplyTurnAction {
	if db == nil || tenantID <= 0 || turnID <= 0 || taskKey == "" || actionKey == "" {
		return nil
	}
	ret := &models.AIReplyTurnAction{}
	if err := db.Take(ret, "tenant_id = ? AND turn_id = ? AND task_key = ? AND action_key = ?", tenantID, turnID, taskKey, actionKey).Error; err != nil {
		return nil
	}
	return ret
}

func (r *aiReplyTurnActionRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.AIReplyTurnAction, error) {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	ret := &models.AIReplyTurnAction{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return ret, err
}

func (r *aiReplyTurnActionRepository) FindByTurnInTenant(db *gorm.DB, tenantID, turnID int64) []models.AIReplyTurnAction {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return nil
	}
	ret := make([]models.AIReplyTurnAction, 0)
	_ = db.Where("tenant_id = ? AND turn_id = ?", tenantID, turnID).Order("id ASC").Find(&ret).Error
	return ret
}

func (r *aiReplyTurnActionRepository) FindPreparedByTurnInTenant(db *gorm.DB, tenantID, turnID int64, requestedVersion int) []models.AIReplyTurnAction {
	if db == nil || tenantID <= 0 || turnID <= 0 || requestedVersion <= 0 {
		return nil
	}
	ret := make([]models.AIReplyTurnAction, 0)
	_ = db.Where("tenant_id = ? AND turn_id = ? AND requested_version = ? AND status = ?", tenantID, turnID, requestedVersion, "prepared").Order("id ASC").Find(&ret).Error
	return ret
}

func (r *aiReplyTurnActionRepository) FindByOutboxInTenant(db *gorm.DB, tenantID, outboxID int64) []models.AIReplyTurnAction {
	if db == nil || tenantID <= 0 || outboxID <= 0 {
		return nil
	}
	ret := make([]models.AIReplyTurnAction, 0)
	_ = db.Where("tenant_id = ? AND outbox_id = ?", tenantID, outboxID).Order("id ASC").Find(&ret).Error
	return ret
}

func (r *aiReplyTurnActionRepository) CASStatusInTenant(db *gorm.DB, id, tenantID int64, from []string, columns map[string]any) (bool, error) {
	if db == nil || id <= 0 || tenantID <= 0 || len(from) == 0 || len(columns) == 0 {
		return false, nil
	}
	result := db.Model(&models.AIReplyTurnAction{}).Where("id = ? AND tenant_id = ? AND status IN ?", id, tenantID, from).Updates(columns)
	return result.RowsAffected == 1, result.Error
}

func (r *aiReplyTurnActionRepository) SupersedeOlderVersions(db *gorm.DB, tenantID, turnID int64, currentVersion int, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || currentVersion <= 0 {
		return nil
	}
	return db.Model(&models.AIReplyTurnAction{}).
		Where("tenant_id = ? AND turn_id = ? AND requested_version < ? AND status IN ?", tenantID, turnID, currentVersion, []string{"requested", "prepared"}).
		Updates(map[string]any{"status": "superseded", "result_code": "stale_turn_version", "updated_at": now, "update_user_name": "ai_reply_action"}).Error
}
