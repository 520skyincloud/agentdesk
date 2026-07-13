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

func (s *tenantInvitationService) Create(t *models.TenantInvitation) error {
	return repositories.TenantInvitationRepository.Create(sqls.DB(), t)
}

func (s *tenantInvitationService) Update(t *models.TenantInvitation) error {
	return repositories.TenantInvitationRepository.Update(sqls.DB(), t)
}

func (s *tenantInvitationService) Updates(id int64, columns map[string]any) error {
	return repositories.TenantInvitationRepository.Updates(sqls.DB(), id, columns)
}

func (s *tenantInvitationService) UpdateColumn(id int64, name string, value any) error {
	return repositories.TenantInvitationRepository.UpdateColumn(sqls.DB(), id, name, value)
}
