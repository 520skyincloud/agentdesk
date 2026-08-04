package constants

import "testing"

func TestBuiltinTenantRolesDoNotReceivePlatformPermissions(t *testing.T) {
	roleScopes := make(map[string]string, len(Roles))
	for _, role := range Roles {
		roleScopes[role.Code] = role.Scope
	}
	for roleCode, permissions := range RolePermissions {
		if roleScopes[roleCode] == RoleScopePlatform {
			continue
		}
		for _, permission := range permissions {
			if NormalizePermissionScope(permission.Scope) == PermissionScopePlatform {
				t.Errorf("tenant role %s includes platform permission %s", roleCode, permission.Code)
			}
		}
	}
}

func TestStoreStaffRoleIncludesOwnConversationWorkspacePermissions(t *testing.T) {
	permissions := RolePermissions[RoleCodeStoreStaff]
	want := map[string]bool{
		PermissionConversationView.Code: false,
		PermissionConversationSend.Code: false,
	}
	for _, permission := range permissions {
		if _, ok := want[permission.Code]; ok {
			want[permission.Code] = true
		}
	}
	for permissionCode, found := range want {
		if !found {
			t.Fatalf("store staff role missing permission %s", permissionCode)
		}
	}
}
