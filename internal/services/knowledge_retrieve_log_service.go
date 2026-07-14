package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var KnowledgeRetrieveLogService = newKnowledgeRetrieveLogService()

func newKnowledgeRetrieveLogService() *knowledgeRetrieveLogService {
	return &knowledgeRetrieveLogService{}
}

type knowledgeRetrieveLogService struct {
}

func (s *knowledgeRetrieveLogService) GetInTenant(id, tenantID int64) *models.KnowledgeRetrieveLog {
	return repositories.KnowledgeRetrieveLogRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *knowledgeRetrieveLogService) FindPageInTenant(cnd *sqls.Cnd, tenantID int64) (list []models.KnowledgeRetrieveLog, paging *sqls.Paging) {
	if tenantID <= 0 {
		cnd = cnd.Where("1 = 0")
	} else {
		cnd = cnd.Eq("tenant_id", tenantID)
	}
	return repositories.KnowledgeRetrieveLogRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *knowledgeRetrieveLogService) FindHitsInTenant(retrieveLogID, tenantID int64) []models.KnowledgeRetrieveHit {
	return repositories.KnowledgeRetrieveLogRepository.FindHitsInTenant(sqls.DB(), retrieveLogID, tenantID)
}
