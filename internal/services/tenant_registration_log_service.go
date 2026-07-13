package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var TenantRegistrationLogService = newTenantRegistrationLogService()

func newTenantRegistrationLogService() *tenantRegistrationLogService {
	return &tenantRegistrationLogService{}
}

type tenantRegistrationLogService struct {
}

func (s *tenantRegistrationLogService) Get(id int64) *models.TenantRegistrationLog {
	return repositories.TenantRegistrationLogRepository.Get(sqls.DB(), id)
}

func (s *tenantRegistrationLogService) Take(where ...any) *models.TenantRegistrationLog {
	return repositories.TenantRegistrationLogRepository.Take(sqls.DB(), where...)
}

func (s *tenantRegistrationLogService) Find(cnd *sqls.Cnd) []models.TenantRegistrationLog {
	return repositories.TenantRegistrationLogRepository.Find(sqls.DB(), cnd)
}

func (s *tenantRegistrationLogService) FindOne(cnd *sqls.Cnd) *models.TenantRegistrationLog {
	return repositories.TenantRegistrationLogRepository.FindOne(sqls.DB(), cnd)
}

func (s *tenantRegistrationLogService) FindPageByParams(params *params.QueryParams) (list []models.TenantRegistrationLog, paging *sqls.Paging) {
	return repositories.TenantRegistrationLogRepository.FindPageByParams(sqls.DB(), params)
}

func (s *tenantRegistrationLogService) FindPageByCnd(cnd *sqls.Cnd) (list []models.TenantRegistrationLog, paging *sqls.Paging) {
	return repositories.TenantRegistrationLogRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *tenantRegistrationLogService) Count(cnd *sqls.Cnd) int64 {
	return repositories.TenantRegistrationLogRepository.Count(sqls.DB(), cnd)
}

func (s *tenantRegistrationLogService) Create(t *models.TenantRegistrationLog) error {
	return repositories.TenantRegistrationLogRepository.Create(sqls.DB(), t)
}
