package repositories

import (
	"strconv"

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

// FindBySourceID returns inbound sync records that can identify a source
// message either by its local message id, sync-log id, or protocol message id.
func (r *messageSyncLogRepository) FindBySourceID(db *gorm.DB, sourceID int64) (list []models.MessageSyncLog) {
	if sourceID <= 0 {
		return nil
	}
	suffix := "%:" + strconv.FormatInt(sourceID, 10)
	_ = db.Where("id = ? OR message_id = ? OR external_msg_id LIKE ?", sourceID, sourceID, suffix).
		Order("id DESC").Limit(20).Find(&list).Error
	return list
}
