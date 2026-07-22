package repositories

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ReplyIntentProfileRepository = newReplyIntentProfileRepository()

func newReplyIntentProfileRepository() *replyIntentProfileRepository {
	return &replyIntentProfileRepository{}
}

type replyIntentProfileRepository struct{}

func (r *replyIntentProfileRepository) Get(db *gorm.DB, id int64) *models.ReplyIntentProfile {
	ret := &models.ReplyIntentProfile{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *replyIntentProfileRepository) GetForUpdate(db *gorm.DB, id int64) (*models.ReplyIntentProfile, error) {
	ret := &models.ReplyIntentProfile{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(ret, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *replyIntentProfileRepository) Take(db *gorm.DB, where ...any) *models.ReplyIntentProfile {
	ret := &models.ReplyIntentProfile{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *replyIntentProfileRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.ReplyIntentProfile) {
	cnd.Find(db, &list)
	return
}

func (r *replyIntentProfileRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.ReplyIntentProfile, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *replyIntentProfileRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.ReplyIntentProfile, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.ReplyIntentProfile{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *replyIntentProfileRepository) Create(db *gorm.DB, t *models.ReplyIntentProfile) error {
	return db.Create(t).Error
}

func (r *replyIntentProfileRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.ReplyIntentProfile{}).Where("id = ?", id).Updates(columns).Error
}

func (r *replyIntentProfileRepository) Delete(db *gorm.DB, id int64) error {
	return db.Delete(&models.ReplyIntentProfile{}, "id = ?", id).Error
}
