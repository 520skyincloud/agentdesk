package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var KnowledgeRetrieveLogRepository = newKnowledgeRetrieveLogRepository()

type knowledgeRetrieveLogRepository struct{}

func newKnowledgeRetrieveLogRepository() *knowledgeRetrieveLogRepository {
	return &knowledgeRetrieveLogRepository{}
}

func (r *knowledgeRetrieveLogRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.KnowledgeRetrieveLog {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.KnowledgeRetrieveLog{}
	if err := db.First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *knowledgeRetrieveLogRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.KnowledgeRetrieveLog, paging *sqls.Paging) {
	cnd.Find(db, &list)
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: cnd.Count(db, &models.KnowledgeRetrieveLog{})}
	return
}

func (r *knowledgeRetrieveLogRepository) FindHitsInTenant(db *gorm.DB, retrieveLogID, tenantID int64) []models.KnowledgeRetrieveHit {
	if retrieveLogID <= 0 || tenantID <= 0 {
		return nil
	}
	var list []models.KnowledgeRetrieveHit
	db.Where("retrieve_log_id = ? AND tenant_id = ?", retrieveLogID, tenantID).Order("rank_no asc, id asc").Find(&list)
	return list
}
