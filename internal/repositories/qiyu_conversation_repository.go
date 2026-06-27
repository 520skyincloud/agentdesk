package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var QiyuConversationRepository = newQiyuConversationRepository()

func newQiyuConversationRepository() *qiyuConversationRepository {
	return &qiyuConversationRepository{}
}

type qiyuConversationRepository struct{}

func (r *qiyuConversationRepository) Get(db *gorm.DB, id int64) *models.QiyuConversation {
	ret := &models.QiyuConversation{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *qiyuConversationRepository) Take(db *gorm.DB, where ...any) *models.QiyuConversation {
	ret := &models.QiyuConversation{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *qiyuConversationRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.QiyuConversation) {
	cnd.Find(db, &list)
	return
}

func (r *qiyuConversationRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.QiyuConversation, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *qiyuConversationRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.QiyuConversation, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.QiyuConversation{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *qiyuConversationRepository) Create(db *gorm.DB, t *models.QiyuConversation) error {
	return db.Create(t).Error
}

func (r *qiyuConversationRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.QiyuConversation{}).Where("id = ?", id).Updates(columns).Error
}
