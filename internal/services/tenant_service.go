package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var TenantService = newTenantService()

func newTenantService() *tenantService {
	return &tenantService{}
}

type tenantService struct {
}

func (s *tenantService) LegacyTenantID(db *gorm.DB) (int64, error) {
	tenant := repositories.TenantRepository.GetByTenantCode(db, constants.LegacyDefaultTenantCode)
	if tenant == nil {
		return 0, errorsx.BusinessError(1, "历史默认公司尚未初始化")
	}
	return tenant.ID, nil
}

func (s *tenantService) Get(id int64) *models.Tenant {
	return repositories.TenantRepository.Get(sqls.DB(), id)
}

func (s *tenantService) Take(where ...any) *models.Tenant {
	return repositories.TenantRepository.Take(sqls.DB(), where...)
}

func (s *tenantService) Find(cnd *sqls.Cnd) []models.Tenant {
	return repositories.TenantRepository.Find(sqls.DB(), cnd)
}

func (s *tenantService) FindOne(cnd *sqls.Cnd) *models.Tenant {
	return repositories.TenantRepository.FindOne(sqls.DB(), cnd)
}

func (s *tenantService) FindPageByParams(params *params.QueryParams) (list []models.Tenant, paging *sqls.Paging) {
	return repositories.TenantRepository.FindPageByParams(sqls.DB(), params)
}

func (s *tenantService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Tenant, paging *sqls.Paging) {
	return repositories.TenantRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *tenantService) Count(cnd *sqls.Cnd) int64 {
	return repositories.TenantRepository.Count(sqls.DB(), cnd)
}

func (s *tenantService) Create(t *models.Tenant) error {
	return repositories.TenantRepository.Create(sqls.DB(), t)
}

func (s *tenantService) Update(t *models.Tenant) error {
	return repositories.TenantRepository.Update(sqls.DB(), t)
}

func (s *tenantService) Updates(id int64, columns map[string]any) error {
	return repositories.TenantRepository.Updates(sqls.DB(), id, columns)
}

func (s *tenantService) UpdateColumn(id int64, name string, value any) error {
	return repositories.TenantRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *tenantService) Delete(id int64) {
	repositories.TenantRepository.Delete(sqls.DB(), id)
}
