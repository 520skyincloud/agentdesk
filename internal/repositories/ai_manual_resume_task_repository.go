package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var AIManualResumeTaskRepository = newAIManualResumeTaskRepository()

type aiManualResumeTaskRepository struct{}

func newAIManualResumeTaskRepository() *aiManualResumeTaskRepository {
	return &aiManualResumeTaskRepository{}
}

func (r *aiManualResumeTaskRepository) Get(db *gorm.DB, id int64) *models.AIManualResumeTask {
	ret := &models.AIManualResumeTask{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *aiManualResumeTaskRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.AIManualResumeTask {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.AIManualResumeTask{}
	if err := db.First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *aiManualResumeTaskRepository) Take(db *gorm.DB, where ...any) *models.AIManualResumeTask {
	ret := &models.AIManualResumeTask{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *aiManualResumeTaskRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.AIManualResumeTask) {
	cnd.Find(db, &list)
	return
}

func (r *aiManualResumeTaskRepository) Create(db *gorm.DB, item *models.AIManualResumeTask) error {
	return db.Create(item).Error
}

func (r *aiManualResumeTaskRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.AIManualResumeTask{}).Where("id = ?", id).Updates(columns).Error
}

func (r *aiManualResumeTaskRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.AIManualResumeTask{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *aiManualResumeTaskRepository) Claim(db *gorm.DB, id int64, fromStatuses []string, columns map[string]any) (bool, error) {
	result := db.Model(&models.AIManualResumeTask{}).
		Where("id = ? AND task_status IN ?", id, fromStatuses).
		Updates(columns)
	return result.RowsAffected == 1, result.Error
}

func (r *aiManualResumeTaskRepository) ClaimInTenant(db *gorm.DB, id, tenantID int64, fromStatuses []string, columns map[string]any) (bool, error) {
	result := db.Model(&models.AIManualResumeTask{}).
		Where("id = ? AND tenant_id = ? AND task_status IN ?", id, tenantID, fromStatuses).
		Updates(columns)
	return result.RowsAffected == 1, result.Error
}

func (r *aiManualResumeTaskRepository) CancelActiveByConversation(db *gorm.DB, conversationID int64, columns map[string]any) error {
	return db.Model(&models.AIManualResumeTask{}).
		Where("conversation_id = ? AND task_status IN ?", conversationID, []string{"waiting", "ready", "retry", "running", "blocked_ai_disabled"}).
		Updates(columns).Error
}

func (r *aiManualResumeTaskRepository) CancelActiveByConversationInTenant(db *gorm.DB, conversationID, tenantID int64, columns map[string]any) error {
	return db.Model(&models.AIManualResumeTask{}).
		Where("conversation_id = ? AND tenant_id = ? AND task_status IN ?", conversationID, tenantID, []string{"waiting", "ready", "retry", "running", "blocked_ai_disabled"}).
		Updates(columns).Error
}

func (r *aiManualResumeTaskRepository) UnblockByWxWorkInstance(db *gorm.DB, wxWorkInstanceID int64, columns map[string]any) error {
	return db.Model(&models.AIManualResumeTask{}).
		Where("wx_work_instance_id = ? AND task_status = ?", wxWorkInstanceID, "blocked_ai_disabled").
		Updates(columns).Error
}

func (r *aiManualResumeTaskRepository) UnblockByWxWorkInstanceInTenant(db *gorm.DB, wxWorkInstanceID, tenantID int64, columns map[string]any) error {
	if wxWorkInstanceID <= 0 || tenantID <= 0 {
		return nil
	}
	return db.Model(&models.AIManualResumeTask{}).
		Where("wx_work_instance_id = ? AND tenant_id = ? AND task_status = ?", wxWorkInstanceID, tenantID, "blocked_ai_disabled").
		Updates(columns).Error
}
