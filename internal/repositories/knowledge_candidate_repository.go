package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var KnowledgeCandidateRepository = newKnowledgeCandidateRepository()

func newKnowledgeCandidateRepository() *knowledgeCandidateRepository {
	return &knowledgeCandidateRepository{}
}

type knowledgeCandidateRepository struct{}

func (r *knowledgeCandidateRepository) Get(db *gorm.DB, id int64) *models.KnowledgeCandidate {
	ret := &models.KnowledgeCandidate{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *knowledgeCandidateRepository) Take(db *gorm.DB, where ...any) *models.KnowledgeCandidate {
	ret := &models.KnowledgeCandidate{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *knowledgeCandidateRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.KnowledgeCandidate) {
	cnd.Find(db, &list)
	return
}

func (r *knowledgeCandidateRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.KnowledgeCandidate, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *knowledgeCandidateRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.KnowledgeCandidate, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.KnowledgeCandidate{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *knowledgeCandidateRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.KnowledgeCandidate{})
}

func (r *knowledgeCandidateRepository) Create(db *gorm.DB, t *models.KnowledgeCandidate) error {
	return db.Create(t).Error
}

func (r *knowledgeCandidateRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.KnowledgeCandidate{}).Where("id = ?", id).Updates(columns).Error
}
