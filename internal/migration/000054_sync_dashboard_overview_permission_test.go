package migration

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
)

func TestSyncDashboardOverviewPermission(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	if err := db.Transaction(syncDashboardOverviewPermission); err != nil {
		t.Fatalf("sync dashboard overview permission: %v", err)
	}
	if err := db.Transaction(syncDashboardOverviewPermission); err != nil {
		t.Fatalf("repeat dashboard overview permission sync: %v", err)
	}

	var permission models.Permission
	if err := db.Where("code = ?", constants.PermissionDashboardView.Code).Take(&permission).Error; err != nil {
		t.Fatalf("find dashboard permission: %v", err)
	}
	if permission.Scope != constants.PermissionScopeTenant || permission.APIPath != constants.PermissionDashboardView.APIPath {
		t.Fatalf("dashboard permission = %+v", permission)
	}

	for _, roleCode := range []string{
		constants.RoleCodeSuperAdmin,
		constants.RoleCodeAdmin,
		constants.RoleCodeTenantAdmin,
		constants.RoleCodeCsTeamLeader,
		constants.RoleCodeCsUser,
	} {
		assertRolePermissionCount(t, db, roleCode, permission.ID, 1)
	}
	assertRolePermissionCount(t, db, constants.RoleCodeStoreStaff, permission.ID, 0)
}
