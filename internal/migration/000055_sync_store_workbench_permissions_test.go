package migration

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
)

func TestSyncStoreWorkbenchPermissions(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	if err := db.Transaction(syncStoreWorkbenchPermissions); err != nil {
		t.Fatalf("sync store workbench permissions: %v", err)
	}
	if err := db.Transaction(syncStoreWorkbenchPermissions); err != nil {
		t.Fatalf("repeat store workbench permission sync: %v", err)
	}

	for _, spec := range []constants.Permission{
		constants.PermissionStoreWorkbenchView,
		constants.PermissionStoreWorkbenchUpdate,
	} {
		var permission models.Permission
		if err := db.Where("code = ?", spec.Code).Take(&permission).Error; err != nil {
			t.Fatalf("find permission %s: %v", spec.Code, err)
		}
		if permission.Scope != constants.PermissionScopeTenant || permission.APIPath != spec.APIPath {
			t.Fatalf("permission %s = %+v", spec.Code, permission)
		}
		assertRolePermissionCount(t, db, constants.RoleCodeSuperAdmin, permission.ID, 1)
		assertRolePermissionCount(t, db, constants.RoleCodeStoreStaff, permission.ID, 1)
		for _, roleCode := range []string{
			constants.RoleCodeAdmin,
			constants.RoleCodeTenantAdmin,
			constants.RoleCodeCsTeamLeader,
			constants.RoleCodeCsUser,
		} {
			assertRolePermissionCount(t, db, roleCode, permission.ID, 0)
		}
	}
}
