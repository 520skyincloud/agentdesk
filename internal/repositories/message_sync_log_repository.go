package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var MessageSyncLogRepository = newMessageSyncLogRepository()

func newMessageSyncLogRepository() *messageSyncLogRepository {
	return &messageSyncLogRepository{}
}

type messageSyncLogRepository struct{}

func (r *messageSyncLogRepository) Take(db *gorm.DB, where ...any) *models.MessageSyncLog {
	ret := &models.MessageSyncLog{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *messageSyncLogRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.MessageSyncLog) {
	cnd.Find(db, &list)
	return
}

func (r *messageSyncLogRepository) Create(db *gorm.DB, t *models.MessageSyncLog) error {
	return db.Create(t).Error
}

func (r *messageSyncLogRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.MessageSyncLog{}).Where("id = ?", id).Updates(columns).Error
}
