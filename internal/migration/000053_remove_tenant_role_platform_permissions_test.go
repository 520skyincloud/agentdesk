package migration

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

func TestRemoveTenantRolePlatformPermissions(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	if err := db.Transaction(syncPlatformSystemPermissions); err != nil {
		t.Fatalf("seed auth data: %v", err)
	}

	customTenantRole := &models.Role{Code: "custom_tenant_platform_access", Name: "Custom tenant platform access", Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	customPlatformRole := &models.Role{Code: "custom_platform_access", Name: "Custom platform access", Scope: constants.RoleScopePlatform, Status: enums.StatusOk}
	for _, role := range []*models.Role{customTenantRole, customPlatformRole} {
		if err := db.Create(role).Error; err != nil {
			t.Fatalf("create role %s: %v", role.Code, err)
		}
	}

	platformPermissions := []constants.Permission{
		constants.PermissionSessionView,
		constants.PermissionAIConfigUpdate,
		constants.PermissionMCPView,
	}
	var teamLeader models.Role
	if err := db.Where("code = ?", constants.RoleCodeCsTeamLeader).Take(&teamLeader).Error; err != nil {
		t.Fatalf("find team leader role: %v", err)
	}
	seedPlatformPermissionRelations(t, db, teamLeader.ID, []constants.Permission{constants.PermissionSessionView})
	seedPlatformPermissionRelations(t, db, customTenantRole.ID, platformPermissions)
	seedPlatformPermissionRelations(t, db, customPlatformRole.ID, platformPermissions)

	if err := db.Transaction(removeTenantRolePlatformPermissions); err != nil {
		t.Fatalf("remove tenant platform permissions: %v", err)
	}
	if err := db.Transaction(removeTenantRolePlatformPermissions); err != nil {
		t.Fatalf("repeat tenant platform permission cleanup: %v", err)
	}

	var leaked int64
	if err := db.Table("t_role_permission rp").
		Joins("JOIN t_role r ON r.id = rp.role_id").
		Joins("JOIN t_permission p ON p.id = rp.permission_id").
		Where("r.scope <> ? AND p.scope = ?", constants.RoleScopePlatform, constants.PermissionScopePlatform).
		Count(&leaked).Error; err != nil {
		t.Fatalf("count tenant role platform permissions: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("tenant roles retain %d platform permissions", leaked)
	}

	for _, spec := range platformPermissions {
		var permission models.Permission
		if err := db.Where("code = ?", spec.Code).Take(&permission).Error; err != nil {
			t.Fatalf("find permission %s: %v", spec.Code, err)
		}
		assertRolePermissionCount(t, db, customPlatformRole.Code, permission.ID, 1)
		assertRolePermissionCount(t, db, constants.RoleCodeAdmin, permission.ID, 1)
		if spec.Code == constants.PermissionSessionView.Code {
			assertRolePermissionCount(t, db, constants.RoleCodeCsTeamLeader, permission.ID, 0)
		}
	}
}

func seedPlatformPermissionRelations(t *testing.T, db *gorm.DB, roleID int64, permissions []constants.Permission) {
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
