package services

import (
	"encoding/json"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var RoleService = newRoleService()

func newRoleService() *roleService {
	return &roleService{}
}

type roleService struct {
}

func (s *roleService) Get(id int64) *models.Role {
	return repositories.RoleRepository.Get(sqls.DB(), id)
}

func (s *roleService) Take(where ...interface{}) *models.Role {
	return repositories.RoleRepository.Take(sqls.DB(), where...)
}

func (s *roleService) Find(cnd *sqls.Cnd) []models.Role {
	return repositories.RoleRepository.Find(sqls.DB(), cnd)
}

func (s *roleService) FindOne(cnd *sqls.Cnd) *models.Role {
	return repositories.RoleRepository.FindOne(sqls.DB(), cnd)
}

func (s *roleService) FindPageByParams(params *params.QueryParams) (list []models.Role, paging *sqls.Paging) {
	return repositories.RoleRepository.FindPageByParams(sqls.DB(), params)
}

func (s *roleService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Role, paging *sqls.Paging) {
	return repositories.RoleRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *roleService) Count(cnd *sqls.Cnd) int64 {
	return repositories.RoleRepository.Count(sqls.DB(), cnd)
}

func (s *roleService) CreateRole(req request.CreateRoleRequest, operator *dto.AuthPrincipal) (*models.Role, error) {
	if err := s.ensurePlatformRoleOperatorDB(sqls.DB(), operator); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	code := strings.TrimSpace(req.Code)
	if name == "" || code == "" {
		return nil, errorsx.InvalidParam("角色名称和编码不能为空")
	}
	if s.Take("code = ?", code) != nil {
		return nil, errorsx.InvalidParam("角色编码已存在")
	}
	scope := normalizeRoleScope(req.Scope)
	authorityLevel := req.AuthorityLevel
	if authorityLevel <= 0 {
		authorityLevel = 10
	}
	operatorLevel := s.HighestAuthorityLevel(operator)
	if operatorLevel <= 0 || authorityLevel >= operatorLevel {
		return nil, errorsx.Forbidden("只能创建低于自身等级的角色")
	}
	if scope == constants.RoleScopePlatform && operatorLevel < constants.RoleAuthoritySuperAdmin {
		return nil, errorsx.Forbidden("只有超级管理员可以创建平台角色")
	}

	role := &models.Role{
		Name:           name,
		Code:           code,
		Scope:          scope,
		AuthorityLevel: authorityLevel,
		Status:         enums.StatusOk,
		IsSystem:       false,
		SortNo:         s.NextSortNo(),
		Remark:         strings.TrimSpace(req.Remark),
		AuditFields:    utils.BuildAuditFields(operator),
	}
	if err := repositories.RoleRepository.Create(sqls.DB(), role); err != nil {
		return nil, err
	}
	return role, nil
}

func (s *roleService) UpdateRole(req request.UpdateRoleRequest, operator *dto.AuthPrincipal) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		role, err := repositories.RoleRepository.GetForUpdate(ctx.Tx, req.ID)
		if err != nil {
			return err
		}
		if role == nil {
			return errorsx.InvalidParam("角色不存在")
		}
		if err := s.ensureCanMutateRoleDefinitionDB(ctx.Tx, operator, role); err != nil {
			return err
		}
		now := time.Now()
		return repositories.RoleRepository.Updates(ctx.Tx, req.ID, map[string]any{
			"name":             strings.TrimSpace(req.Name),
			"sort_no":          req.SortNo,
			"remark":           strings.TrimSpace(req.Remark),
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		})
	})
}

func (s *roleService) NextSortNo() int {
	if latest := s.FindOne(sqls.NewCnd().Desc("sort_no").Desc("id")); latest != nil {
		return latest.SortNo + 1
	}
	return 0
}

func (s *roleService) UpdateSort(ids []int64, operator *dto.AuthPrincipal) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := s.ensurePlatformRoleOperatorDB(ctx.Tx, operator); err != nil {
			return err
		}
		seen := make(map[int64]struct{}, len(ids))
		lockIDs := make([]int64, 0, len(ids))
		for _, id := range ids {
			if id <= 0 {
				return errorsx.InvalidParam("角色 ID 不合法")
			}
			if _, exists := seen[id]; exists {
				return errorsx.InvalidParam("角色排序列表包含重复项")
			}
			seen[id] = struct{}{}
			lockIDs = append(lockIDs, id)
		}
		slices.Sort(lockIDs)
		for _, id := range lockIDs {
			role, err := repositories.RoleRepository.GetForUpdate(ctx.Tx, id)
			if err != nil {
				return err
			}
			if role == nil {
				return errorsx.InvalidParam("角色不存在")
			}
		}
		for i, id := range ids {
			if err := repositories.RoleRepository.Updates(ctx.Tx, id, map[string]any{
				"sort_no":          i,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
				"updated_at":       time.Now(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *roleService) DeleteRole(id int64, operator *dto.AuthPrincipal) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		role, err := repositories.RoleRepository.GetForUpdate(ctx.Tx, id)
		if err != nil {
			return err
		}
		if role == nil {
			return errorsx.InvalidParam("角色不存在")
		}
		if err := s.ensureCanMutateRoleDefinitionDB(ctx.Tx, operator, role); err != nil {
			return err
		}
		if role.IsSystem {
			return errorsx.Forbidden("系统内置角色不允许删除")
		}
		inUse, err := repositories.UserRoleRepository.ExistsByRoleID(ctx.Tx, id)
		if err != nil {
			return err
		}
		if inUse {
			return errorsx.Forbidden("角色已被用户使用，无法删除")
		}
		if err := replaceRolePermissionsDB(ctx.Tx, role, nil, operator); err != nil {
			return err
		}
		return repositories.RoleRepository.Delete(ctx.Tx, id)
	})
}

func (s *roleService) UpdateStatus(id int64, status enums.Status, operator *dto.AuthPrincipal) error {
	if status != enums.StatusOk && status != enums.StatusDisabled {
		return errorsx.InvalidParam("角色状态只支持启用或禁用，删除请使用角色删除功能")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		role, err := repositories.RoleRepository.GetForUpdate(ctx.Tx, id)
		if err != nil {
			return err
		}
		if role == nil {
			return errorsx.InvalidParam("角色不存在")
		}
		if err := s.ensureCanMutateRoleDefinitionDB(ctx.Tx, operator, role); err != nil {
			return err
		}
		return repositories.RoleRepository.Updates(ctx.Tx, id, map[string]any{
			"status":           status,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       time.Now(),
		})
	})
}

func (s *roleService) AssignPermissions(roleID int64, permissionIDs []int64, operator *dto.AuthPrincipal) error {
	role := s.Get(roleID)
	if role == nil {
		return errorsx.InvalidParam("角色不存在")
	}
	if err := s.ensureCanMutateRoleDefinitionDB(sqls.DB(), operator, role); err != nil {
		return err
	}

	return s.replaceRolePermissions(roleID, permissionIDs, operator)
}

func (s *roleService) replaceRolePermissions(roleID int64, permissionIDs []int64, operator *dto.AuthPrincipal) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		role, err := repositories.RoleRepository.GetForUpdate(ctx.Tx, roleID)
		if err != nil {
			return err
		}
		if role == nil {
			return errorsx.InvalidParam("角色不存在")
		}
		if err := s.ensureCanMutateRoleDefinitionDB(ctx.Tx, operator, role); err != nil {
			return err
		}

		seen := make(map[int64]struct{}, len(permissionIDs))
		permissions := make([]*models.Permission, 0, len(permissionIDs))
		for _, permissionID := range permissionIDs {
			if _, exists := seen[permissionID]; exists {
				continue
			}
			seen[permissionID] = struct{}{}
			permission := repositories.PermissionRepository.Get(ctx.Tx, permissionID)
			if permission == nil {
				return errorsx.InvalidParam("权限不存在")
			}
			if permission.Status != enums.StatusOk {
				return errorsx.InvalidParam("禁用权限不允许分配")
			}
			if normalizeRoleScope(role.Scope) == constants.RoleScopeTenant && normalizePermissionScope(permission.Scope) == constants.PermissionScopePlatform {
				return errorsx.Forbidden("租户角色不能分配平台权限")
			}
			permissions = append(permissions, permission)
		}

		return replaceRolePermissionsDB(ctx.Tx, role, permissions, operator)
	})
}

func replaceRolePermissionsDB(db *gorm.DB, role *models.Role, permissions []*models.Permission, operator *dto.AuthPrincipal) error {
	if role == nil {
		return errorsx.InvalidParam("角色不存在")
	}
	before, err := loadRolePermissionSetSnapshotDB(db, role.ID)
	if err != nil {
		return err
	}
	after := rolePermissionSetSnapshotFromPermissions(permissions)
	if slices.Equal(before.IDs, after.IDs) {
		return nil
	}
	if err := repositories.RolePermissionRepository.DeleteByRoleID(db, role.ID); err != nil {
		return err
	}
	for _, permission := range permissions {
		relation := &models.RolePermission{
			RoleID:       role.ID,
			PermissionID: permission.ID,
			AuditFields:  utils.BuildAuditFields(operator),
		}
		if err := repositories.RolePermissionRepository.Create(db, relation); err != nil {
			return err
		}
	}
	return appendRolePermissionChangeLogDB(db, role, before, after, operator)
}

type rolePermissionSetSnapshot struct {
	IDs   []int64
	Codes []string
}

func loadRolePermissionSetSnapshotDB(db *gorm.DB, roleID int64) (rolePermissionSetSnapshot, error) {
	items, err := repositories.RolePermissionRepository.FindSnapshotByRoleID(db, roleID)
	if err != nil {
		return rolePermissionSetSnapshot{}, err
	}
	snapshot := rolePermissionSetSnapshot{IDs: make([]int64, 0, len(items)), Codes: make([]string, 0, len(items))}
	for _, item := range items {
		snapshot.IDs = append(snapshot.IDs, item.PermissionID)
		if item.PermissionCode != "" {
			snapshot.Codes = append(snapshot.Codes, item.PermissionCode)
		}
	}
	slices.Sort(snapshot.IDs)
	slices.Sort(snapshot.Codes)
	return snapshot, nil
}

func rolePermissionSetSnapshotFromPermissions(permissions []*models.Permission) rolePermissionSetSnapshot {
	snapshot := rolePermissionSetSnapshot{IDs: make([]int64, 0, len(permissions)), Codes: make([]string, 0, len(permissions))}
	for _, permission := range permissions {
		if permission == nil {
			continue
		}
		snapshot.IDs = append(snapshot.IDs, permission.ID)
		snapshot.Codes = append(snapshot.Codes, permission.Code)
	}
	slices.Sort(snapshot.IDs)
	slices.Sort(snapshot.Codes)
	return snapshot
}

func appendRolePermissionChangeLogDB(
	db *gorm.DB,
	role *models.Role,
	before rolePermissionSetSnapshot,
	after rolePermissionSetSnapshot,
	operator *dto.AuthPrincipal,
) error {
	if role == nil || slices.Equal(before.IDs, after.IDs) {
		return nil
	}
	beforeIDsJSON, err := json.Marshal(before.IDs)
	if err != nil {
		return err
	}
	afterIDsJSON, err := json.Marshal(after.IDs)
	if err != nil {
		return err
	}
	beforeCodesJSON, err := json.Marshal(before.Codes)
	if err != nil {
		return err
	}
	afterCodesJSON, err := json.Marshal(after.Codes)
	if err != nil {
		return err
	}
	operatorID := int64(0)
	operatorName := "system"
	if operator != nil {
		operatorID = operator.UserID
		operatorName = operator.Username
	}
	return repositories.RolePermissionChangeLogRepository.Create(db, &models.RolePermissionChangeLog{
		RoleID:                    role.ID,
		RoleCode:                  role.Code,
		BeforePermissionIDsJSON:   string(beforeIDsJSON),
		AfterPermissionIDsJSON:    string(afterIDsJSON),
		BeforePermissionCodesJSON: string(beforeCodesJSON),
		AfterPermissionCodesJSON:  string(afterCodesJSON),
		OperatorID:                operatorID,
		OperatorName:              operatorName,
		CreatedAt:                 time.Now(),
	})
}

func (s *roleService) HighestAuthorityLevel(operator *dto.AuthPrincipal) int {
	return s.highestAuthorityLevelDB(sqls.DB(), operator)
}

func (s *roleService) highestAuthorityLevelDB(db *gorm.DB, operator *dto.AuthPrincipal) int {
	if operator == nil || len(operator.Roles) == 0 {
		return 0
	}
	highest := 0
	for _, code := range operator.Roles {
		role := repositories.RoleRepository.GetByCode(db, strings.TrimSpace(code))
		if role != nil && role.Status == enums.StatusOk && role.AuthorityLevel > highest {
			highest = role.AuthorityLevel
		}
	}
	return highest
}

func (s *roleService) CanManageRole(operator *dto.AuthPrincipal, role *models.Role) bool {
	return s.canManageRoleDB(sqls.DB(), operator, role)
}

func (s *roleService) canManageRoleDB(db *gorm.DB, operator *dto.AuthPrincipal, role *models.Role) bool {
	if operator == nil || role == nil || role.Code == constants.RoleCodeSuperAdmin {
		return false
	}
	return role.AuthorityLevel < s.highestAuthorityLevelDB(db, operator)
}

func (s *roleService) CanAssignRole(operator *dto.AuthPrincipal, role *models.Role) bool {
	return s.canAssignRoleDB(sqls.DB(), operator, role)
}

func (s *roleService) canAssignRoleDB(db *gorm.DB, operator *dto.AuthPrincipal, role *models.Role) bool {
	if !s.canManageRoleDB(db, operator, role) || role.Status != enums.StatusOk {
		return false
	}
	operatorLevel := s.highestAuthorityLevelDB(db, operator)
	if normalizeRoleScope(role.Scope) == constants.RoleScopePlatform && operatorLevel < constants.RoleAuthoritySuperAdmin {
		return false
	}
	return true
}

func (s *roleService) EnsureCanManageRole(operator *dto.AuthPrincipal, role *models.Role) error {
	return s.ensureCanManageRoleDB(sqls.DB(), operator, role)
}

func (s *roleService) ensureCanManageRoleDB(db *gorm.DB, operator *dto.AuthPrincipal, role *models.Role) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if !s.canManageRoleDB(db, operator, role) {
		return errorsx.Forbidden("只能管理低于自身等级的角色")
	}
	return nil
}

func (s *roleService) ensurePlatformRoleOperatorDB(db *gorm.DB, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if !operator.IsPlatformAccount {
		return errorsx.Forbidden("只有平台账号可以管理角色")
	}
	if s.highestAuthorityLevelDB(db, operator) <= 0 {
		return errorsx.Forbidden("当前账号没有可用的角色管理等级")
	}
	return nil
}

func (s *roleService) ensureCanMutateRoleDefinitionDB(db *gorm.DB, operator *dto.AuthPrincipal, role *models.Role) error {
	if err := s.ensurePlatformRoleOperatorDB(db, operator); err != nil {
		return err
	}
	return s.ensureCanManageRoleDB(db, operator, role)
}

func normalizeRoleScope(scope string) string {
	if strings.TrimSpace(scope) == constants.RoleScopePlatform {
		return constants.RoleScopePlatform
	}
	return constants.RoleScopeTenant
}

func normalizePermissionScope(scope string) string {
	if strings.TrimSpace(scope) == constants.PermissionScopePlatform {
		return constants.PermissionScopePlatform
	}
	return constants.PermissionScopeTenant
}
