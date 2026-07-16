package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// AIAgentService exposes the read-only runtime strategy identity used by
// channels, conversations and the reply runtime. Tenant model selection is
// handled by StoreAIModelSettingService and is not owned by this service.
var AIAgentService = &aIAgentService{}

type aIAgentService struct{}

func (s *aIAgentService) Get(id int64) *models.AIAgent {
	if id <= 0 {
		return nil
	}
	return repositories.AIAgentRepository.Get(sqls.DB(), id)
}

func (s *aIAgentService) GetInTenant(id int64, operator *dto.AuthPrincipal) *models.AIAgent {
	return s.GetByTenantID(id, AgentTeamScopeService.ActiveTenantID(operator))
}

func (s *aIAgentService) GetByTenantID(id, tenantID int64) *models.AIAgent {
	return repositories.AIAgentRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *aIAgentService) FindInTenant(cnd *sqls.Cnd, operator *dto.AuthPrincipal) []models.AIAgent {
	return repositories.AIAgentRepository.Find(sqls.DB(), AgentTeamScopeService.ApplyTenantFilter(cnd, operator))
}

func (s *aIAgentService) FindPageInTenant(cnd *sqls.Cnd, operator *dto.AuthPrincipal) (list []models.AIAgent, paging *sqls.Paging) {
	return repositories.AIAgentRepository.FindPageByCnd(sqls.DB(), AgentTeamScopeService.ApplyTenantFilter(cnd, operator))
}

func (s *aIAgentService) FindByIdsInTenant(ids []int64, tenantID int64) []models.AIAgent {
	return repositories.AIAgentRepository.FindByIdsInTenant(sqls.DB(), ids, tenantID)
}
