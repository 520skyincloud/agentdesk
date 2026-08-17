package repositories

import (
	"errors"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

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

// CloseCurrentNoActionVersion is the compare-and-set used when the newest
// customer input has no replyable request and the turn has no unfinished task.
// Keeping the version and active job in the predicate prevents a late worker
// from closing a turn that already received another customer message.
func (r *aiReplyTurnRepository) CloseCurrentNoActionVersion(
	db *gorm.DB,
	tenantID, turnID, activeJobID int64,
	version int,
	reason string,
	now time.Time,
) (bool, error) {
	if db == nil || tenantID <= 0 || turnID <= 0 || activeJobID <= 0 || version <= 0 {
		return false, nil
	}
	result := db.Model(&models.AIReplyTurn{}).
		Where("id = ? AND tenant_id = ? AND version = ? AND active_job_id = ? AND status = ?",
			turnID, tenantID, version, activeJobID, enums.AIReplyTurnStatusRunning).
		Updates(map[string]any{
			"status":           enums.AIReplyTurnStatusClosed,
			"terminal_reason":  reason,
			"completed_at":     now,
			"updated_at":       now,
			"update_user_name": "ai_reply_no_action",
		})
	return result.RowsAffected == 1, result.Error
}

// FindStaleInTenant 扫描超过保护窗口仍停留在 open/running/committed 的 Turn。
func (r *aiReplyTurnRepository) FindStaleInTenant(db *gorm.DB, before time.Time, limit int) []models.AIReplyTurn {
	var list []models.AIReplyTurn
	db.Where("status IN ? AND updated_at < ?", []string{"open", "running", "committed"}, before).
		Order("updated_at ASC").Limit(limit).Find(&list)
	return list
}
