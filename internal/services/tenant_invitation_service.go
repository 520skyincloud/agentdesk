package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var TenantInvitationService = newTenantInvitationService()

func newTenantInvitationService() *tenantInvitationService {
	return &tenantInvitationService{}
}

type tenantInvitationService struct {
}

func (s *tenantInvitationService) Get(id int64) *models.TenantInvitation {
	return repositories.TenantInvitationRepository.Get(sqls.DB(), id)
}

func (s *tenantInvitationService) Take(where ...any) *models.TenantInvitation {
	return repositories.TenantInvitationRepository.Take(sqls.DB(), where...)
}

func (s *tenantInvitationService) Find(cnd *sqls.Cnd) []models.TenantInvitation {
	return repositories.TenantInvitationRepository.Find(sqls.DB(), cnd)
}

func (s *tenantInvitationService) FindOne(cnd *sqls.Cnd) *models.TenantInvitation {
	return repositories.TenantInvitationRepository.FindOne(sqls.DB(), cnd)
}

func (s *tenantInvitationService) FindPageByParams(params *params.QueryParams) (list []models.TenantInvitation, paging *sqls.Paging) {
	return repositories.TenantInvitationRepository.FindPageByParams(sqls.DB(), params)
}

func (s *tenantInvitationService) FindPageByCnd(cnd *sqls.Cnd) (list []models.TenantInvitation, paging *sqls.Paging) {
	return repositories.TenantInvitationRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *tenantInvitationService) Count(cnd *sqls.Cnd) int64 {
	return repositories.TenantInvitationRepository.Count(sqls.DB(), cnd)
}
