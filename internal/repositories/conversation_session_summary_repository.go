package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ConversationSessionSummaryRepository = newConversationSessionSummaryRepository()

func newConversationSessionSummaryRepository() *conversationSessionSummaryRepository {
	return &conversationSessionSummaryRepository{}
}

type conversationSessionSummaryRepository struct{}

func (r *conversationSessionSummaryRepository) Get(db *gorm.DB, id int64) *models.ConversationSessionSummary {
	ret := &models.ConversationSessionSummary{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationSessionSummaryRepository) Take(db *gorm.DB, where ...any) *models.ConversationSessionSummary {
	ret := &models.ConversationSessionSummary{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationSessionSummaryRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.ConversationSessionSummary) {
	cnd.Find(db, &list)
	return
}

func (r *conversationSessionSummaryRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.ConversationSessionSummary {
	ret := &models.ConversationSessionSummary{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *conversationSessionSummaryRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.ConversationSessionSummary, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *conversationSessionSummaryRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.ConversationSessionSummary, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.ConversationSessionSummary{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *conversationSessionSummaryRepository) Create(db *gorm.DB, t *models.ConversationSessionSummary) error {
	return db.Create(t).Error
}

func (r *conversationSessionSummaryRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.ConversationSessionSummary{}).Where("id = ?", id).Updates(columns).Error
}

func (r *conversationSessionSummaryRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.ConversationSessionSummary{}, "id = ?", id)
}
