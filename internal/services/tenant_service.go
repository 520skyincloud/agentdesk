package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
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
		return 0, errorsx.BusinessError(1, "OIDC 兜底公司尚未初始化")
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

func (s *tenantService) FindOperationalStats(tenantIDs []int64) (map[int64]dto.TenantOperationalStats, error) {
	rows, err := repositories.TenantRepository.FindOperationalStats(sqls.DB(), tenantIDs)
	if err != nil {
		return nil, err
	}
	stats := make(map[int64]dto.TenantOperationalStats, len(rows))
	for tenantID, row := range rows {
		lastActiveAt := row.LatestConversationActive
		if row.LatestUserLogin != nil && (lastActiveAt == nil || row.LatestUserLogin.After(*lastActiveAt)) {
			lastActiveAt = row.LatestUserLogin
		}
		stats[tenantID] = dto.TenantOperationalStats{
			AgentCount:     row.AgentCount,
			StoreCount:     row.StoreCount,
			AgentTeamCount: row.AgentTeamCount,
			LastActiveAt:   lastActiveAt,
		}
	}
	return stats, nil
}
