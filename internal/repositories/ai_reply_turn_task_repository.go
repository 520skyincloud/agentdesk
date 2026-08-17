package repositories

import (
	"errors"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var AIReplyTurnTaskRepository = newAIReplyTurnTaskRepository()

type aiReplyTurnTaskRepository struct{}

func newAIReplyTurnTaskRepository() *aiReplyTurnTaskRepository {
	return &aiReplyTurnTaskRepository{}
}

func (r *aiReplyTurnTaskRepository) CreateIfAbsent(db *gorm.DB, item *models.AIReplyTurnTask) (bool, error) {
	if db == nil || item == nil {
		return false, nil
	}
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "turn_id"}, {Name: "task_key"}},
		DoNothing: true,
	}).Create(item)
	return result.RowsAffected > 0, result.Error
}

func (r *aiReplyTurnTaskRepository) GetByKeyInTenant(db *gorm.DB, tenantID, turnID int64, taskKey string) *models.AIReplyTurnTask {
	if db == nil || tenantID <= 0 || turnID <= 0 || taskKey == "" {
		return nil
	}
	ret := &models.AIReplyTurnTask{}
	if err := db.Take(ret, "tenant_id = ? AND turn_id = ? AND task_key = ?", tenantID, turnID, taskKey).Error; err != nil {
		return nil
	}
	return ret
}

func (r *aiReplyTurnTaskRepository) GetForUpdateByKeyInTenant(db *gorm.DB, tenantID, turnID int64, taskKey string) (*models.AIReplyTurnTask, error) {
	if db == nil || tenantID <= 0 || turnID <= 0 || taskKey == "" {
		return nil, nil
	}
	ret := &models.AIReplyTurnTask{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(ret,
		"tenant_id = ? AND turn_id = ? AND task_key = ?", tenantID, turnID, taskKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *aiReplyTurnTaskRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.AIReplyTurnTask, error) {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	ret := &models.AIReplyTurnTask{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *aiReplyTurnTaskRepository) FindByFingerprintInTurn(
	db *gorm.DB,
	tenantID, turnID int64,
	fingerprint string,
	taskType enums.AIReplyTurnTaskType,
) []models.AIReplyTurnTask {
	if db == nil || tenantID <= 0 || turnID <= 0 || fingerprint == "" {
		return nil
	}
	ret := make([]models.AIReplyTurnTask, 0)
	query := db.Where(
		"tenant_id = ? AND turn_id = ? AND question_fingerprint = ?",
		tenantID, turnID, fingerprint,
	)
	if taskType != "" {
		query = query.Where("task_type = ?", taskType)
	}
	_ = query.Order("id ASC").Find(&ret).Error
	return ret
}

func (r *aiReplyTurnTaskRepository) FindByCommittedMessageInTenant(db *gorm.DB, tenantID, messageID int64) []models.AIReplyTurnTask {
	if db == nil || tenantID <= 0 || messageID <= 0 {
		return nil
	}
	ret := make([]models.AIReplyTurnTask, 0)
	_ = db.Where("tenant_id = ? AND committed_message_id = ?", tenantID, messageID).Order("id ASC").Find(&ret).Error
	return ret
}

func (r *aiReplyTurnTaskRepository) FindCoverageDependentsForUpdateInTenant(
	db *gorm.DB,
	tenantID, turnID, canonicalTaskID int64,
) ([]models.AIReplyTurnTask, error) {
	if db == nil || tenantID <= 0 || turnID <= 0 || canonicalTaskID <= 0 {
		return nil, nil
	}
	ret := make([]models.AIReplyTurnTask, 0)
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"tenant_id = ? AND turn_id = ? AND covered_by_task_id = ? AND status = ?",
			tenantID, turnID, canonicalTaskID, enums.AIReplyTurnTaskStatusWaitingCoverage,
		).
		Order("sequence_no ASC, id ASC").
		Find(&ret).Error
	return ret, err
}

func (r *aiReplyTurnTaskRepository) FindByTurnInTenant(db *gorm.DB, tenantID, turnID int64) []models.AIReplyTurnTask {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return nil
	}
	ret := make([]models.AIReplyTurnTask, 0)
	_ = db.Where("tenant_id = ? AND turn_id = ?", tenantID, turnID).Order("sequence_no ASC, id ASC").Find(&ret).Error
	return ret
}

func (r *aiReplyTurnTaskRepository) FindUnfinishedByTurnInTenant(db *gorm.DB, tenantID, turnID int64, limit int) []models.AIReplyTurnTask {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return nil
	}
	ret := make([]models.AIReplyTurnTask, 0)
	query := db.Where("tenant_id = ? AND turn_id = ? AND status NOT IN ?", tenantID, turnID, aiReplyTurnTaskTerminalStatuses()).
		Order("sequence_no ASC, id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	_ = query.Find(&ret).Error
	return ret
}

func (r *aiReplyTurnTaskRepository) CountUnfinishedByTurnInTenant(db *gorm.DB, tenantID, turnID int64) int64 {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return 0
	}
	var count int64
	_ = db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status NOT IN ?", tenantID, turnID, aiReplyTurnTaskTerminalStatuses()).
		Count(&count).Error
	return count
}

func (r *aiReplyTurnTaskRepository) CountRunnableByTurnInTenant(db *gorm.DB, tenantID, turnID int64) int64 {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return 0
	}
	var count int64
	_ = db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", tenantID, turnID, []enums.AIReplyTurnTaskStatus{
			enums.AIReplyTurnTaskStatusPending,
			enums.AIReplyTurnTaskStatusReady,
		}, time.Now()).
		Count(&count).Error
	return count
}

func (r *aiReplyTurnTaskRepository) CountWorkPendingByTurnInTenant(db *gorm.DB, tenantID, turnID int64) int64 {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return 0
	}
	var count int64
	_ = db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status IN ?", tenantID, turnID, []enums.AIReplyTurnTaskStatus{
			enums.AIReplyTurnTaskStatusPending,
			enums.AIReplyTurnTaskStatusReady,
			enums.AIReplyTurnTaskStatusRunning,
			enums.AIReplyTurnTaskStatusWaitingCoverage,
		}).
		Count(&count).Error
	return count
}

func (r *aiReplyTurnTaskRepository) NextRetryAtByTurnInTenant(db *gorm.DB, tenantID, turnID int64, now time.Time) *time.Time {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return nil
	}
	var item models.AIReplyTurnTask
	if err := db.Where(
		"tenant_id = ? AND turn_id = ? AND status IN ? AND next_retry_at IS NOT NULL AND next_retry_at > ?",
		tenantID, turnID, []enums.AIReplyTurnTaskStatus{
			enums.AIReplyTurnTaskStatusPending,
			enums.AIReplyTurnTaskStatusReady,
		}, now,
	).Order("next_retry_at ASC, id ASC").First(&item).Error; err != nil || item.NextRetryAt == nil {
		return nil
	}
	next := *item.NextRetryAt
	return &next
}

func (r *aiReplyTurnTaskRepository) CountFailureHandoffsByTurnInTenant(db *gorm.DB, tenantID, turnID int64) int64 {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return 0
	}
	var count int64
	_ = db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status = ? AND result_code <> ''",
			tenantID, turnID, enums.AIReplyTurnTaskStatusHandoffPending).
		Count(&count).Error
	return count
}

func (r *aiReplyTurnTaskRepository) CountFailedByTurnInTenant(db *gorm.DB, tenantID, turnID int64) int64 {
	if db == nil || tenantID <= 0 || turnID <= 0 {
		return 0
	}
	var count int64
	_ = db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status = ?", tenantID, turnID, enums.AIReplyTurnTaskStatusFailed).
		Count(&count).Error
	return count
}

func (r *aiReplyTurnTaskRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	if db == nil || id <= 0 || tenantID <= 0 || len(columns) == 0 {
		return nil
	}
	return db.Model(&models.AIReplyTurnTask{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *aiReplyTurnTaskRepository) UpdatesByTurnInTenant(db *gorm.DB, tenantID, turnID int64, columns map[string]any) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || len(columns) == 0 {
		return nil
	}
	return db.Model(&models.AIReplyTurnTask{}).
		Where("tenant_id = ? AND turn_id = ? AND status NOT IN ?", tenantID, turnID, aiReplyTurnTaskTerminalStatuses()).
		Updates(columns).Error
}

func (r *aiReplyTurnTaskRepository) FindDue(db *gorm.DB, now time.Time, limit int) []models.AIReplyTurnTask {
	if db == nil || limit <= 0 {
		return nil
	}
	ret := make([]models.AIReplyTurnTask, 0, limit)
	_ = db.Where("status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)",
		[]enums.AIReplyTurnTaskStatus{enums.AIReplyTurnTaskStatusPending, enums.AIReplyTurnTaskStatusReady}, now).
		Order("COALESCE(next_retry_at, created_at) ASC, id ASC").Limit(limit).Find(&ret).Error
	return ret
}

func aiReplyTurnTaskTerminalStatuses() []enums.AIReplyTurnTaskStatus {
	return []enums.AIReplyTurnTaskStatus{
		enums.AIReplyTurnTaskStatusDelivered,
		enums.AIReplyTurnTaskStatusCovered,
		enums.AIReplyTurnTaskStatusHandoff,
		enums.AIReplyTurnTaskStatusSkipped,
		enums.AIReplyTurnTaskStatusSuperseded,
		enums.AIReplyTurnTaskStatusFailed,
	}
}
