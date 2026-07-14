package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var CompanyService = newCompanyService()

func newCompanyService() *companyService {
	return &companyService{}
}

type companyService struct {
}

func (s *companyService) Get(id int64) *models.Company {
	if id <= 0 {
		return nil
	}
	return repositories.CompanyRepository.Get(sqls.DB(), id)
}

func (s *companyService) GetInTenant(id int64, operator *dto.AuthPrincipal) *models.Company {
	return repositories.CompanyRepository.GetInTenant(sqls.DB(), id, companyTenantID(operator))
}

func (s *companyService) Take(where ...interface{}) *models.Company {
	return repositories.CompanyRepository.Take(sqls.DB(), where...)
}

func (s *companyService) Find(cnd *sqls.Cnd) []models.Company {
	return repositories.CompanyRepository.Find(sqls.DB(), cnd)
}

func (s *companyService) FindOne(cnd *sqls.Cnd) *models.Company {
	return repositories.CompanyRepository.FindOne(sqls.DB(), cnd)
}

func (s *companyService) FindPageByParams(params *params.QueryParams) (list []models.Company, paging *sqls.Paging) {
	return repositories.CompanyRepository.FindPageByParams(sqls.DB(), params)
}

func (s *companyService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Company, paging *sqls.Paging) {
	return repositories.CompanyRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *companyService) FindPageInTenant(cnd *sqls.Cnd, operator *dto.AuthPrincipal) (list []models.Company, paging *sqls.Paging) {
	if cnd == nil {
		cnd = sqls.NewCnd()
	}
	tenantID := companyTenantID(operator)
	if tenantID <= 0 {
		return repositories.CompanyRepository.FindPageByCnd(sqls.DB(), cnd.Where("1 = 0"))
	}
	return repositories.CompanyRepository.FindPageByCnd(sqls.DB(), cnd.Eq("tenant_id", tenantID))
}

func (s *companyService) Count(cnd *sqls.Cnd) int64 {
	return repositories.CompanyRepository.Count(sqls.DB(), cnd)
}

func (s *companyService) CreateCompany(req request.CreateCompanyRequest, operator *dto.AuthPrincipal) (*models.Company, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := companyTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理客户企业的接入公司")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errorsx.InvalidParam("公司名称不能为空")
	}

	existing := repositories.CompanyRepository.GetByNameInTenant(sqls.DB(), tenantID, name)
	if existing != nil {
		return nil, errorsx.InvalidParam("公司名称已存在")
	}

	item := &models.Company{
		TenantID:    tenantID,
		Name:        name,
		Code:        strings.TrimSpace(req.Code),
		Status:      enums.StatusOk,
		Remark:      strings.TrimSpace(req.Remark),
		AuditFields: utils.BuildAuditFields(operator),
	}
	if err := repositories.CompanyRepository.Create(sqls.DB(), item); err != nil {
		if isDuplicateKeyError(err) {
			return nil, errorsx.InvalidParam("公司名称已存在")
		}
		return nil, err
	}
	return item, nil
}

func (s *companyService) UpdateCompany(req request.UpdateCompanyRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := s.GetInTenant(req.ID, operator)
	if item == nil {
		return errorsx.InvalidParam("公司不存在")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return errorsx.InvalidParam("公司名称不能为空")
	}

	existing := repositories.CompanyRepository.GetByNameInTenant(sqls.DB(), item.TenantID, name)
	if existing != nil && existing.ID != req.ID {
		return errorsx.InvalidParam("公司名称已存在")
	}

	now := time.Now()
	if err := repositories.CompanyRepository.UpdatesInTenant(sqls.DB(), req.ID, item.TenantID, map[string]any{
		"name":             name,
		"code":             strings.TrimSpace(req.Code),
		"remark":           strings.TrimSpace(req.Remark),
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       now,
	}); err != nil {
		if isDuplicateKeyError(err) {
			return errorsx.InvalidParam("公司名称已存在")
		}
		return err
	}
	return nil
}

func (s *companyService) DeleteCompany(id int64, operator dto.AuthPrincipal) error {
	item := s.GetInTenant(id, &operator)
	if item == nil {
		return errorsx.InvalidParam("公司不存在")
	}

	return repositories.CompanyRepository.UpdatesInTenant(sqls.DB(), id, item.TenantID, map[string]any{
		"status":           enums.StatusDeleted,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       time.Now(),
	})
}

func (s *companyService) UpdateStatus(id int64, status int, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := s.GetInTenant(id, operator)
	if item == nil {
		return errorsx.InvalidParam("公司不存在")
	}
	if status != int(enums.StatusOk) && status != int(enums.StatusDisabled) {
		return errorsx.InvalidParam("状态值不合法")
	}
	now := time.Now()
	return repositories.CompanyRepository.UpdatesInTenant(sqls.DB(), id, item.TenantID, map[string]any{
		"status":           status,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       now,
	})
}

func companyTenantID(operator *dto.AuthPrincipal) int64 {
	if operator == nil || operator.ActiveTenantID <= 0 {
		return 0
	}
	return operator.ActiveTenantID
}
