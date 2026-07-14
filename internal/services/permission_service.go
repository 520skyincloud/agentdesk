package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var PermissionService = newPermissionService()

func newPermissionService() *permissionService {
	return &permissionService{}
}

type permissionService struct {
}

func (s *permissionService) Get(id int64) *models.Permission {
	return repositories.PermissionRepository.Get(sqls.DB(), id)
}

func (s *permissionService) Take(where ...interface{}) *models.Permission {
	return repositories.PermissionRepository.Take(sqls.DB(), where...)
}

func (s *permissionService) Find(cnd *sqls.Cnd) []models.Permission {
	return repositories.PermissionRepository.Find(sqls.DB(), cnd)
}

func (s *permissionService) FindOne(cnd *sqls.Cnd) *models.Permission {
	return repositories.PermissionRepository.FindOne(sqls.DB(), cnd)
}

func (s *permissionService) FindPageByParams(params *params.QueryParams) (list []models.Permission, paging *sqls.Paging) {
	return repositories.PermissionRepository.FindPageByParams(sqls.DB(), params)
}

func (s *permissionService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Permission, paging *sqls.Paging) {
	return repositories.PermissionRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *permissionService) Count(cnd *sqls.Cnd) int64 {
	return repositories.PermissionRepository.Count(sqls.DB(), cnd)
}
