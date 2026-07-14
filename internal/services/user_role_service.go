package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var UserRoleService = newUserRoleService()

func newUserRoleService() *userRoleService {
	return &userRoleService{}
}

type userRoleService struct {
}

func (s *userRoleService) Get(id int64) *models.UserRole {
	return repositories.UserRoleRepository.Get(sqls.DB(), id)
}

func (s *userRoleService) Take(where ...interface{}) *models.UserRole {
	return repositories.UserRoleRepository.Take(sqls.DB(), where...)
}

func (s *userRoleService) Find(cnd *sqls.Cnd) []models.UserRole {
	return repositories.UserRoleRepository.Find(sqls.DB(), cnd)
}

func (s *userRoleService) FindOne(cnd *sqls.Cnd) *models.UserRole {
	return repositories.UserRoleRepository.FindOne(sqls.DB(), cnd)
}

func (s *userRoleService) FindPageByParams(params *params.QueryParams) (list []models.UserRole, paging *sqls.Paging) {
	return repositories.UserRoleRepository.FindPageByParams(sqls.DB(), params)
}

func (s *userRoleService) FindPageByCnd(cnd *sqls.Cnd) (list []models.UserRole, paging *sqls.Paging) {
	return repositories.UserRoleRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *userRoleService) Count(cnd *sqls.Cnd) int64 {
	return repositories.UserRoleRepository.Count(sqls.DB(), cnd)
}
