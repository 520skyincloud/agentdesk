package migration

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

func TestRestrictSkillDefinitionWritesToPlatform(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	if err := db.Transaction(syncPlatformSystemPermissions); err != nil {
		t.Fatalf("seed auth data: %v", err)
	}

	customTenantRole := &models.Role{Code: "custom_tenant_skill_editor", Name: "Custom tenant skill editor", Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	customPlatformRole := &models.Role{Code: "custom_platform_skill_editor", Name: "Custom platform skill editor", Scope: constants.RoleScopePlatform, Status: enums.StatusOk}
	for _, role := range []*models.Role{customTenantRole, customPlatformRole} {
		if err := db.Create(role).Error; err != nil {
			t.Fatalf("create role %s: %v", role.Code, err)
		}
	}

	writePermissions := []constants.Permission{
		constants.PermissionSkillDefinitionCreate,
		constants.PermissionSkillDefinitionUpdate,
		constants.PermissionSkillDefinitionDelete,
	}
	for _, roleCode := range []string{constants.RoleCodeTenantAdmin, constants.RoleCodeCsTeamLeader} {
		var role models.Role
		if err := db.Where("code = ?", roleCode).Take(&role).Error; err != nil {
			t.Fatalf("find role %s: %v", roleCode, err)
		}
		seedSkillWriteRelations(t, db, role.ID, writePermissions)
	}
	seedSkillWriteRelations(t, db, customTenantRole.ID, writePermissions)
	seedSkillWriteRelations(t, db, customPlatformRole.ID, writePermissions)

	if err := db.Transaction(restrictSkillDefinitionWritesToPlatform); err != nil {
		t.Fatalf("restrict skill writes: %v", err)
	}
	if err := db.Transaction(restrictSkillDefinitionWritesToPlatform); err != nil {
		t.Fatalf("repeat skill write restriction: %v", err)
	}

	for _, spec := range writePermissions {
		var permission models.Permission
		if err := db.Where("code = ?", spec.Code).Take(&permission).Error; err != nil {
			t.Fatalf("find permission %s: %v", spec.Code, err)
		}
		if permission.Scope != constants.PermissionScopePlatform {
			t.Fatalf("permission %s scope=%q want platform", spec.Code, permission.Scope)
		}
		assertRolePermissionCount(t, db, constants.RoleCodeSuperAdmin, permission.ID, 1)
		assertRolePermissionCount(t, db, constants.RoleCodeAdmin, permission.ID, 1)
		assertRolePermissionCount(t, db, constants.RoleCodeTenantAdmin, permission.ID, 0)
		assertRolePermissionCount(t, db, constants.RoleCodeCsTeamLeader, permission.ID, 0)
		assertRolePermissionCount(t, db, customTenantRole.Code, permission.ID, 0)
		assertRolePermissionCount(t, db, customPlatformRole.Code, permission.ID, 1)
	}
}

func seedSkillWriteRelations(t *testing.T, db *gorm.DB, roleID int64, permissions []constants.Permission) {
	t.Helper()
	for _, spec := range permissions {
		var permission models.Permission
		if err := db.Where("code = ?", spec.Code).Take(&permission).Error; err != nil {
			t.Fatalf("find permission %s: %v", spec.Code, err)
		}
		if err := db.Create(&models.RolePermission{RoleID: roleID, PermissionID: permission.ID}).Error; err != nil {
			t.Fatalf("seed role %d permission %s: %v", roleID, spec.Code, err)
		}
	}
}

func assertRolePermissionCount(t *testing.T, db *gorm.DB, roleCode string, permissionID int64, want int64) {
	t.Helper()
	var count int64
	if err := db.Table("t_role_permission rp").
		Joins("JOIN t_role r ON r.id = rp.role_id").
		Where("r.code = ? AND rp.permission_id = ?", roleCode, permissionID).
		Count(&count).Error; err != nil {
		t.Fatalf("count role %s permission %d: %v", roleCode, permissionID, err)
	}
	if count != want {
		t.Fatalf("role %s permission %d count=%d want %d", roleCode, permissionID, count, want)
	}
}
