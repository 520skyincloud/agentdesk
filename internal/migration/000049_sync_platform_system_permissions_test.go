package migration

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
)

func TestSyncPlatformSystemPermissions(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	if err := db.Transaction(syncPlatformSystemPermissions); err != nil {
		t.Fatalf("sync platform system permissions: %v", err)
	}
	if err := db.Transaction(syncPlatformSystemPermissions); err != nil {
		t.Fatalf("repeat sync platform system permissions: %v", err)
	}

	for _, spec := range []constants.Permission{
		constants.PermissionStorageSettingView,
		constants.PermissionStorageSettingUpdate,
		constants.PermissionWxWorkDevicePoolView,
		constants.PermissionWxWorkDevicePoolUpdate,
		constants.PermissionWxWorkDevicePoolSync,
	} {
		var permission models.Permission
		if err := db.Where("code = ?", spec.Code).Take(&permission).Error; err != nil {
			t.Fatalf("find permission %s: %v", spec.Code, err)
		}
		if permission.Scope != constants.PermissionScopePlatform || permission.APIPath != spec.APIPath {
			t.Fatalf("permission %s = %+v", spec.Code, permission)
		}
		var adminCount int64
		if err := db.Table("t_role_permission rp").
			Joins("JOIN t_role r ON r.id = rp.role_id").
			Where("r.code = ? AND rp.permission_id = ?", constants.RoleCodeAdmin, permission.ID).
			Count(&adminCount).Error; err != nil {
			t.Fatalf("count admin permission %s: %v", spec.Code, err)
		}
		if adminCount != 1 {
			t.Fatalf("admin permission %s count=%d want 1", spec.Code, adminCount)
		}
		var tenantAdminCount int64
		if err := db.Table("t_role_permission rp").
			Joins("JOIN t_role r ON r.id = rp.role_id").
			Where("r.code = ? AND rp.permission_id = ?", constants.RoleCodeTenantAdmin, permission.ID).
			Count(&tenantAdminCount).Error; err != nil {
			t.Fatalf("count tenant admin permission %s: %v", spec.Code, err)
		}
		if tenantAdminCount != 0 {
			t.Fatalf("tenant admin permission %s count=%d want 0", spec.Code, tenantAdminCount)
		}
	}
}
