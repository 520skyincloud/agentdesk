package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ReplyIntentConfigRepository = newReplyIntentConfigRepository()

func newReplyIntentConfigRepository() *replyIntentConfigRepository {
	return &replyIntentConfigRepository{}
}

type replyIntentConfigRepository struct{}

func (r *replyIntentConfigRepository) Get(db *gorm.DB, id int64) *models.ReplyIntentConfig {
	ret := &models.ReplyIntentConfig{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *replyIntentConfigRepository) Take(db *gorm.DB, where ...any) *models.ReplyIntentConfig {
	ret := &models.ReplyIntentConfig{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *replyIntentConfigRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.ReplyIntentConfig) {
	cnd.Find(db, &list)
	return
}

func (r *replyIntentConfigRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.ReplyIntentConfig {
	ret := &models.ReplyIntentConfig{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *replyIntentConfigRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.ReplyIntentConfig, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *replyIntentConfigRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.ReplyIntentConfig, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.ReplyIntentConfig{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *replyIntentConfigRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.ReplyIntentConfig{})
}

func (r *replyIntentConfigRepository) Create(db *gorm.DB, t *models.ReplyIntentConfig) error {
	return db.Create(t).Error
}

func (r *replyIntentConfigRepository) Update(db *gorm.DB, t *models.ReplyIntentConfig) error {
	return db.Save(t).Error
}

func (r *replyIntentConfigRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.ReplyIntentConfig{}).Where("id = ?", id).Updates(columns).Error
}

func (r *replyIntentConfigRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.ReplyIntentConfig{}, "id = ?", id)
}
