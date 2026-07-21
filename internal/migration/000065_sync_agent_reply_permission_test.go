package migration

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
)

func TestSyncAgentReplyPermission(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	if err := db.Transaction(syncAgentReplyPermission); err != nil {
		t.Fatalf("sync agent reply permission: %v", err)
	}
	if err := db.Transaction(syncAgentReplyPermission); err != nil {
		t.Fatalf("repeat agent reply permission sync: %v", err)
	}

	var permission models.Permission
	if err := db.Where("code = ?", constants.PermissionConversationSend.Code).Take(&permission).Error; err != nil {
		t.Fatalf("find conversation send permission: %v", err)
	}
	assertRolePermissionCount(t, db, constants.RoleCodeCsUser, permission.ID, 1)

	for _, spec := range []constants.Permission{
		constants.PermissionConversationAssign,
		constants.PermissionConversationTransfer,
	} {
		var dispatchPermission models.Permission
		if err := db.Where("code = ?", spec.Code).Take(&dispatchPermission).Error; err != nil {
			t.Fatalf("find dispatch permission %s: %v", spec.Code, err)
		}
		if dispatchPermission.APIPath != spec.APIPath {
			t.Fatalf("dispatch permission %s path=%s want=%s", spec.Code, dispatchPermission.APIPath, spec.APIPath)
		}
		assertRolePermissionCount(t, db, constants.RoleCodeCsTeamLeader, dispatchPermission.ID, 1)
	}
}
