package migration

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
)

func TestRestrictStoreStaffRoleToWorkbench(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	permissions, err := ensurePermissions(db)
	if err != nil {
		t.Fatalf("ensure permissions: %v", err)
	}
	roles, err := ensureRoles(db)
	if err != nil {
		t.Fatalf("ensure roles: %v", err)
	}
	storeStaffRole := roles[constants.RoleCodeStoreStaff]
	customRole := &models.Role{
		Name: "自定义门店运营", Code: "custom_store_operator", Scope: constants.RoleScopeTenant,
		AuthorityLevel: constants.RoleAuthorityMember, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(customRole).Error; err != nil {
		t.Fatalf("create custom role: %v", err)
	}
	for _, permissionCode := range []string{
		constants.PermissionChannelView.Code,
		constants.PermissionConversationView.Code,
		constants.PermissionKnowledgeBaseView.Code,
	} {
		permission := permissions[permissionCode]
		if permission == nil {
			t.Fatalf("missing historical permission %s", permissionCode)
		}
		for _, roleID := range []int64{storeStaffRole.ID, customRole.ID} {
			if err := db.Create(&models.RolePermission{
				RoleID: roleID, PermissionID: permission.ID,
				AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
			}).Error; err != nil {
				t.Fatalf("seed role permission %s/%d: %v", permissionCode, roleID, err)
			}
		}
	}

	if err := db.Transaction(restrictStoreStaffRoleToWorkbench); err != nil {
		t.Fatalf("restrict store staff role: %v", err)
	}
	if err := db.Transaction(restrictStoreStaffRoleToWorkbench); err != nil {
		t.Fatalf("repeat store staff role restriction: %v", err)
	}

	for _, spec := range []constants.Permission{
		constants.PermissionStoreWorkbenchView,
		constants.PermissionStoreWorkbenchUpdate,
	} {
		assertRolePermissionCount(t, db, constants.RoleCodeStoreStaff, permissions[spec.Code].ID, 1)
	}
	for _, permissionCode := range []string{
		constants.PermissionChannelView.Code,
		constants.PermissionConversationView.Code,
		constants.PermissionKnowledgeBaseView.Code,
	} {
		permissionID := permissions[permissionCode].ID
		assertRolePermissionCount(t, db, constants.RoleCodeStoreStaff, permissionID, 0)
		var customCount int64
		if err := db.Model(&models.RolePermission{}).
			Where("role_id = ? AND permission_id = ?", customRole.ID, permissionID).
			Count(&customCount).Error; err != nil {
			t.Fatalf("count custom permission %s: %v", permissionCode, err)
		}
		if customCount != 1 {
			t.Fatalf("custom role permission %s was removed", permissionCode)
		}
	}
}
