package repositories

import (
	"time"

	"agent-desk/internal/models"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var NotificationRepository = newNotificationRepository()

func newNotificationRepository() *notificationRepository {
	return &notificationRepository{}
}

type notificationRepository struct {
}

func (r *notificationRepository) GetForRecipient(db *gorm.DB, id, userID, tenantID int64) *models.Notification {
	if id <= 0 || userID <= 0 || tenantID < 0 {
		return nil
	}
	ret := &models.Notification{}
	if err := db.First(ret, "id = ? AND recipient_user_id = ? AND tenant_id = ?", id, userID, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *notificationRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Notification) {
	cnd.Find(db, &list)
	return
}

func (r *notificationRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Notification, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *notificationRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Notification, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Notification{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *notificationRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.Notification{})
}

func (r *notificationRepository) Create(db *gorm.DB, item *models.Notification) error {
	return db.Create(item).Error
}

func (r *notificationRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.Notification{}).Where("id = ?", id).Updates(columns).Error
}

func (r *notificationRepository) MarkAllRead(db *gorm.DB, userID, tenantID int64, readAt time.Time) error {
	return db.Model(&models.Notification{}).
		Where("recipient_user_id = ? AND tenant_id = ? AND read_at IS NULL", userID, tenantID).
		Updates(map[string]any{"read_at": readAt}).Error
}
