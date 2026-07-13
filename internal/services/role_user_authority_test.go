package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"

	"gorm.io/gorm"
)

func TestRoleAuthorityAssignmentMatrix(t *testing.T) {
	db := setupAuthServiceTestDB(t)
	roles := seedAuthorityRoles(t, db)
	platformOperatorRole := createAuthorityRole(t, db, "platform_operator", constants.RoleScopePlatform, 70)

	superAdmin := &dto.AuthPrincipal{UserID: 101, Roles: []string{constants.RoleCodeSuperAdmin}}
	admin := &dto.AuthPrincipal{UserID: 102, Roles: []string{constants.RoleCodeAdmin}}
	tenantAdmin := &dto.AuthPrincipal{UserID: 103, Roles: []string{constants.RoleCodeTenantAdmin}}

	checks := []struct {
		name     string
		operator *dto.AuthPrincipal
		role     *models.Role
		want     bool
	}{
		{name: "super admin assigns admin", operator: superAdmin, role: roles[constants.RoleCodeAdmin], want: true},
		{name: "admin assigns tenant admin", operator: admin, role: roles[constants.RoleCodeTenantAdmin], want: true},
		{name: "admin cannot assign platform role", operator: admin, role: platformOperatorRole, want: false},
		{name: "tenant admin assigns team leader", operator: tenantAdmin, role: roles[constants.RoleCodeCsTeamLeader], want: true},
		{name: "tenant admin cannot assign peer", operator: tenantAdmin, role: roles[constants.RoleCodeTenantAdmin], want: false},
		{name: "nobody assigns super admin", operator: superAdmin, role: roles[constants.RoleCodeSuperAdmin], want: false},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if got := RoleService.CanAssignRole(check.operator, check.role); got != check.want {
				t.Fatalf("CanAssignRole() = %v, want %v", got, check.want)
			}
		})
	}
}

func TestUserServiceAssignRolesEnforcesAuthority(t *testing.T) {
	db := setupAuthServiceTestDB(t)
	roles := seedAuthorityRoles(t, db)
	platformOperatorRole := createAuthorityRole(t, db, "platform_operator", constants.RoleScopePlatform, 70)
	adminUser := createAuthorityUser(t, db, "authority_admin")
	legacyTenantID := authorityLegacyTenantID(t, db)
	platformAdmin := &dto.AuthPrincipal{UserID: adminUser.ID, Username: adminUser.Username, Roles: []string{constants.RoleCodeAdmin}, IsPlatformAccount: true}
	tenantAdmin := &dto.AuthPrincipal{UserID: adminUser.ID, Username: adminUser.Username, Roles: []string{constants.RoleCodeAdmin}, ActiveTenantID: legacyTenantID, IsPlatformAccount: true}

	higherTarget := createAuthorityUser(t, db, "higher_target")
	assignAuthorityRole(t, db, higherTarget.ID, roles[constants.RoleCodeSuperAdmin].ID)
	if err := UserService.AssignRoles(higherTarget.ID, []int64{roles[constants.RoleCodeCsUser].ID}, platformAdmin); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected higher account assignment to be forbidden, got %v", err)
	}

	lowerTarget := createAuthorityUser(t, db, "lower_target")
	if err := db.Model(&models.User{}).Where("id = ?", lowerTarget.ID).Update("tenant_id", legacyTenantID).Error; err != nil {
		t.Fatalf("assign lower target tenant: %v", err)
	}
	lowerTarget.TenantID = legacyTenantID
	assignAuthorityRole(t, db, lowerTarget.ID, roles[constants.RoleCodeCsUser].ID)
	if err := UserService.AssignRoles(lowerTarget.ID, []int64{roles[constants.RoleCodeCsTeamLeader].ID}, tenantAdmin); err != nil {
		t.Fatalf("assign lower tenant role: %v", err)
	}
	assertOnlyUserRole(t, db, lowerTarget.ID, roles[constants.RoleCodeCsTeamLeader].ID)

	if err := UserService.AssignRoles(lowerTarget.ID, []int64{platformOperatorRole.ID}, tenantAdmin); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected platform role assignment to be forbidden, got %v", err)
	}
	assertOnlyUserRole(t, db, lowerTarget.ID, roles[constants.RoleCodeCsTeamLeader].ID)

	if err := UserService.AssignRoles(adminUser.ID, []int64{roles[constants.RoleCodeCsUser].ID}, platformAdmin); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected self role assignment to be forbidden, got %v", err)
	}
}

func TestUserServicePrivilegedMutationsEnforceAuthority(t *testing.T) {
	db := setupAuthServiceTestDB(t)
	roles := seedAuthorityRoles(t, db)
	adminUser := createAuthorityUser(t, db, "mutation_admin")
	assignAuthorityRole(t, db, adminUser.ID, roles[constants.RoleCodeAdmin].ID)
	admin := &dto.AuthPrincipal{UserID: adminUser.ID, Username: adminUser.Username, Roles: []string{constants.RoleCodeAdmin}, IsPlatformAccount: true}
	superUser := createAuthorityUser(t, db, "mutation_super")
	assignAuthorityRole(t, db, superUser.ID, roles[constants.RoleCodeSuperAdmin].ID)

	updateReq := request.UpdateUserRequest{ID: superUser.ID, Nickname: "changed"}
	if err := UserService.UpdateUser(updateReq, admin); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected higher profile update to be forbidden, got %v", err)
	}
	if _, err := UserService.ResetPassword(superUser.ID, admin); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected higher password reset to be forbidden, got %v", err)
	}
	if err := UserService.UpdateStatus(superUser.ID, int(enums.StatusDisabled), admin); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected higher status update to be forbidden, got %v", err)
	}
	if err := UserService.DeleteUser(superUser.ID, admin); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected higher deletion to be forbidden, got %v", err)
	}
	if _, err := UserService.ResetPassword(adminUser.ID, admin); !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected self password reset through user management to be forbidden, got %v", err)
	}

	if err := UserService.UpdateUser(request.UpdateUserRequest{ID: adminUser.ID, Nickname: "updated self"}, admin); err != nil {
		t.Fatalf("expected basic self profile update to remain available, got %v", err)
	}
	updated := UserService.Get(adminUser.ID)
	if updated == nil || updated.Nickname != "updated self" {
		t.Fatalf("unexpected updated self profile: %+v", updated)
	}
}

func TestRoleServiceTenantRoleRejectsPlatformPermission(t *testing.T) {
	db := setupAuthServiceTestDB(t)
	roles := seedAuthorityRoles(t, db)
	tenantPermission := createAuthorityPermission(t, db, "tenant.feature.view", constants.PermissionScopeTenant)
	platformPermission := createAuthorityPermission(t, db, "platform.feature.update", constants.PermissionScopePlatform)
	if err := db.Create(&models.RolePermission{RoleID: roles[constants.RoleCodeCsUser].ID, PermissionID: tenantPermission.ID}).Error; err != nil {
		t.Fatalf("seed tenant role permission: %v", err)
	}

	operator := &dto.AuthPrincipal{UserID: 100, Username: "super", Roles: []string{constants.RoleCodeSuperAdmin}}
	err := RoleService.AssignPermissions(roles[constants.RoleCodeCsUser].ID, []int64{tenantPermission.ID, platformPermission.ID}, operator)
	if !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected platform permission assignment to be forbidden, got %v", err)
	}

	var tenantCount int64
	if err := db.Model(&models.RolePermission{}).
		Where("role_id = ? AND permission_id = ?", roles[constants.RoleCodeCsUser].ID, tenantPermission.ID).
		Count(&tenantCount).Error; err != nil {
		t.Fatalf("count preserved tenant permission: %v", err)
	}
	if tenantCount != 1 {
		t.Fatalf("expected transaction rollback to preserve tenant permission, got %d", tenantCount)
	}
}

func TestTenantAdminCreatesAccountWithLowerRoleOnly(t *testing.T) {
	db := setupAuthServiceTestDB(t)
	roles := seedAuthorityRoles(t, db)
	operator := &dto.AuthPrincipal{UserID: 300, Username: "tenant_admin", Roles: []string{constants.RoleCodeTenantAdmin}}
	legacyTenantID := authorityLegacyTenantID(t, db)
	operator.TenantID = legacyTenantID
	operator.ActiveTenantID = legacyTenantID

	user, password, err := UserService.CreateUser(request.CreateUserRequest{
		Username: "new_tenant_agent",
		RoleIDs:  []int64{roles[constants.RoleCodeCsUser].ID},
	}, operator)
	if err != nil {
		t.Fatalf("create tenant agent: %v", err)
	}
	if user == nil || password == "" {
		t.Fatalf("expected created user and generated password, got user=%+v password=%q", user, password)
	}
	if user.TenantID != legacyTenantID || user.RegistrationSource != enums.UserRegistrationSourceTenant || user.ApprovalStatus != enums.UserApprovalStatusApproved {
		t.Fatalf("unexpected tenant account context: %+v", user)
	}
	assertOnlyUserRole(t, db, user.ID, roles[constants.RoleCodeCsUser].ID)

	_, _, err = UserService.CreateUser(request.CreateUserRequest{
		Username: "new_tenant_admin",
		RoleIDs:  []int64{roles[constants.RoleCodeTenantAdmin].ID},
	}, operator)
	if !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected peer role creation to be forbidden, got %v", err)
	}
	if UserService.GetByUsername("new_tenant_admin") != nil {
		t.Fatal("expected failed account creation transaction to roll back user")
	}
}

func seedAuthorityRoles(t *testing.T, db *gorm.DB) map[string]*models.Role {
	t.Helper()
	roles := map[string]*models.Role{
		constants.RoleCodeSuperAdmin:   createAuthorityRole(t, db, constants.RoleCodeSuperAdmin, constants.RoleScopePlatform, constants.RoleAuthoritySuperAdmin),
		constants.RoleCodeAdmin:        createAuthorityRole(t, db, constants.RoleCodeAdmin, constants.RoleScopePlatform, constants.RoleAuthorityAdmin),
		constants.RoleCodeTenantAdmin:  createAuthorityRole(t, db, constants.RoleCodeTenantAdmin, constants.RoleScopeTenant, constants.RoleAuthorityTenantAdmin),
		constants.RoleCodeCsTeamLeader: createAuthorityRole(t, db, constants.RoleCodeCsTeamLeader, constants.RoleScopeTenant, constants.RoleAuthorityTeamLeader),
		constants.RoleCodeCsUser:       createAuthorityRole(t, db, constants.RoleCodeCsUser, constants.RoleScopeTenant, constants.RoleAuthorityMember),
	}
	return roles
}

func createAuthorityRole(t *testing.T, db *gorm.DB, code, scope string, level int) *models.Role {
	t.Helper()
	role := &models.Role{
		Name:           code,
		Code:           code,
		Scope:          scope,
		AuthorityLevel: level,
		Status:         enums.StatusOk,
		AuditFields:    authorityAuditFields(),
	}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("create role %s: %v", code, err)
	}
	return role
}

func createAuthorityPermission(t *testing.T, db *gorm.DB, code, scope string) *models.Permission {
	t.Helper()
	permission := &models.Permission{
		Name:        code,
		Code:        code,
		Type:        "api",
		Scope:       scope,
		Status:      enums.StatusOk,
		AuditFields: authorityAuditFields(),
	}
	if err := db.Create(permission).Error; err != nil {
		t.Fatalf("create permission %s: %v", code, err)
	}
	return permission
}

func createAuthorityUser(t *testing.T, db *gorm.DB, username string) *models.User {
	t.Helper()
	user := &models.User{
		Username:    username,
		Nickname:    username,
		Password:    "unused-test-password",
		Status:      enums.StatusOk,
		AuditFields: authorityAuditFields(),
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return user
}

func assignAuthorityRole(t *testing.T, db *gorm.DB, userID, roleID int64) {
	t.Helper()
	if err := db.Create(&models.UserRole{
		UserID:      userID,
		RoleID:      roleID,
		AuditFields: authorityAuditFields(),
	}).Error; err != nil {
		t.Fatalf("assign role %d to user %d: %v", roleID, userID, err)
	}
}

func assertOnlyUserRole(t *testing.T, db *gorm.DB, userID, roleID int64) {
	t.Helper()
	var relations []models.UserRole
	if err := db.Where("user_id = ?", userID).Find(&relations).Error; err != nil {
		t.Fatalf("find user roles: %v", err)
	}
	if len(relations) != 1 || relations[0].RoleID != roleID {
		t.Fatalf("unexpected user roles: %+v", relations)
	}
}

func authorityAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, UpdatedAt: now}
}

func authorityLegacyTenantID(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var tenant models.Tenant
	if err := db.Where("tenant_code = ?", constants.LegacyDefaultTenantCode).Take(&tenant).Error; err != nil {
		t.Fatalf("find legacy tenant: %v", err)
	}
	return tenant.ID
}
