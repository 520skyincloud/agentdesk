package services

import (
	"testing"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"

	"golang.org/x/crypto/bcrypt"
)

func TestUserServicePasswordLifecycleMaintainsInitialPasswordFlag(t *testing.T) {
	db := setupRoleAuthorityTestDB(t)
	roles := seedAuthorityRoles(t, db)
	target := createAuthorityUser(t, db, "password_lifecycle_user")
	assignAuthorityRole(t, db, target.ID, roles[constants.RoleCodeCsUser].ID)

	manager := &dto.AuthPrincipal{
		UserID: 9001, Username: "password_manager",
		Roles: []string{constants.RoleCodeSuperAdmin}, IsPlatformAccount: true,
	}
	resetPassword, err := UserService.ResetPassword(target.ID, manager)
	if err != nil {
		t.Fatalf("reset password: %v", err)
	}
	afterReset := UserService.Get(target.ID)
	if afterReset == nil || !afterReset.MustChangePassword {
		t.Fatalf("reset password must require an initial password change: %+v", afterReset)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(afterReset.Password), []byte(resetPassword)); err != nil {
		t.Fatalf("reset password hash mismatch: %v", err)
	}
	if err := AuthService.VerifyCurrentPassword(target.ID, resetPassword); err == nil {
		t.Fatal("initial reset password must not authorize sensitive operations")
	}

	user := &dto.AuthPrincipal{UserID: target.ID, Username: target.Username}
	const ownPassword = "changed-by-user-2026"
	if err := UserService.ChangeOwnPassword(ownPassword, user); err != nil {
		t.Fatalf("change own password: %v", err)
	}
	afterChange := UserService.Get(target.ID)
	if afterChange == nil || afterChange.MustChangePassword {
		t.Fatalf("own password change must clear the initial password flag: %+v", afterChange)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(afterChange.Password), []byte(ownPassword)); err != nil {
		t.Fatalf("own password hash mismatch: %v", err)
	}
	if err := AuthService.VerifyCurrentPassword(target.ID, ownPassword); err != nil {
		t.Fatalf("changed password must authorize sensitive operations: %v", err)
	}
}
