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

func TestRoleNavigationViewPermissions(t *testing.T) {
	tests := []struct {
		roleCode string
		want     []string
	}{
		{
			roleCode: RoleCodeAdmin,
			want:     []string{PermissionStoreWorkbenchView.Code},
		},
		{
			roleCode: RoleCodeCsTeamLeader,
			want:     []string{PermissionBillingView.Code},
		},
		{
			roleCode: RoleCodeCsUser,
			want: []string{
				PermissionStoreView.Code,
				PermissionArrivalConnectionView.Code,
				PermissionArrivalAuditView.Code,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.roleCode, func(t *testing.T) {
			got := make(map[string]bool)
			for _, permission := range RolePermissions[tt.roleCode] {
				got[permission.Code] = true
			}
			for _, permissionCode := range tt.want {
				if !got[permissionCode] {
					t.Fatalf("role %s missing permission %s", tt.roleCode, permissionCode)
				}
			}
		})
	}
}
