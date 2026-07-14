package services

import (
	"slices"
	"strings"
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
	db := setupRoleAuthorityTestDB(t)
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
	db := setupRoleAuthorityTestDB(t)
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
	db := setupRoleAuthorityTestDB(t)
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
	db := setupRoleAuthorityTestDB(t)
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
	db := setupRoleAuthorityTestDB(t)
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

func TestUserServiceAssignRolesPreservesDutyRoleDependencies(t *testing.T) {
	db := setupRoleAuthorityTestDB(t)
	roles := seedAuthorityRoles(t, db)
	tenantID := authorityLegacyTenantID(t, db)
	operator := &dto.AuthPrincipal{
		UserID: 900, Username: "tenant_admin", TenantID: tenantID, ActiveTenantID: tenantID,
		Roles: []string{constants.RoleCodeTenantAdmin},
	}
	target := createAuthorityUser(t, db, "duty_role_target")
	if err := db.Model(&models.User{}).Where("id = ?", target.ID).Update("tenant_id", tenantID).Error; err != nil {
		t.Fatalf("assign duty target tenant: %v", err)
	}
	target.TenantID = tenantID
	for _, code := range []string{constants.RoleCodeCsUser, constants.RoleCodeCsTeamLeader, constants.RoleCodeStoreStaff} {
		assignAuthorityRole(t, db, target.ID, roles[code].ID)
	}
	conversation := &models.Conversation{TenantID: tenantID, CurrentAssigneeID: target.ID, Status: enums.IMConversationStatusActive}
	team := &models.AgentTeam{TenantID: tenantID, Name: "duty team", LeaderUserID: target.ID, Status: enums.StatusOk}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create assigned conversation: %v", err)
	}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create led team: %v", err)
	}
	profile := &models.AgentProfile{TenantID: tenantID, UserID: target.ID, TeamID: team.ID, AgentCode: "duty-agent", DisplayName: "Duty Agent", Status: enums.StatusOk}
	binding := &models.StoreStaffBinding{TenantID: tenantID, UserID: target.ID, StoreID: 1, AgentTeamID: team.ID, Status: enums.StatusOk}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create agent profile: %v", err)
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}

	withoutAgent := []int64{roles[constants.RoleCodeCsTeamLeader].ID, roles[constants.RoleCodeStoreStaff].ID}
	if err := UserService.AssignRoles(target.ID, withoutAgent, operator); err == nil || !strings.Contains(err.Error(), "未完成会话") {
		t.Fatalf("remove agent role with active conversation error = %v", err)
	}
	if err := db.Model(conversation).Update("status", enums.IMConversationStatusClosed).Error; err != nil {
		t.Fatalf("close assigned conversation: %v", err)
	}
	if err := UserService.AssignRoles(target.ID, withoutAgent, operator); err == nil || !strings.Contains(err.Error(), "客服档案") {
		t.Fatalf("remove agent role with profile error = %v", err)
	}
	withoutLeader := []int64{roles[constants.RoleCodeCsUser].ID, roles[constants.RoleCodeStoreStaff].ID}
	if err := UserService.AssignRoles(target.ID, withoutLeader, operator); err == nil || !strings.Contains(err.Error(), "综合客服组组长") {
		t.Fatalf("remove team leader role error = %v", err)
	}
	withoutStoreStaff := []int64{roles[constants.RoleCodeCsUser].ID, roles[constants.RoleCodeCsTeamLeader].ID}
	if err := UserService.AssignRoles(target.ID, withoutStoreStaff, operator); err == nil || !strings.Contains(err.Error(), "门店员工身份") {
		t.Fatalf("remove store staff role error = %v", err)
	}
	assertUserRoleCodes(t, db, target.ID,
		constants.RoleCodeCsTeamLeader,
		constants.RoleCodeCsUser,
		constants.RoleCodeStoreStaff,
	)

	if err := db.Model(conversation).Update("status", enums.IMConversationStatusClosed).Error; err != nil {
		t.Fatalf("keep conversation closed: %v", err)
	}
	if err := db.Model(profile).Update("status", enums.StatusDeleted).Error; err != nil {
		t.Fatalf("delete agent profile: %v", err)
	}
	if err := db.Model(team).Update("leader_user_id", 0).Error; err != nil {
		t.Fatalf("clear team leader: %v", err)
	}
	if err := db.Model(binding).Update("status", enums.StatusDeleted).Error; err != nil {
		t.Fatalf("delete store staff binding: %v", err)
	}
	if err := UserService.AssignRoles(target.ID, nil, operator); err != nil {
		t.Fatalf("remove duty roles after clearing dependencies: %v", err)
	}
	assertUserRoleCodes(t, db, target.ID)
}

func seedAuthorityRoles(t *testing.T, db *gorm.DB) map[string]*models.Role {
	t.Helper()
	roles := map[string]*models.Role{
		constants.RoleCodeSuperAdmin:   createAuthorityRole(t, db, constants.RoleCodeSuperAdmin, constants.RoleScopePlatform, constants.RoleAuthoritySuperAdmin),
		constants.RoleCodeAdmin:        createAuthorityRole(t, db, constants.RoleCodeAdmin, constants.RoleScopePlatform, constants.RoleAuthorityAdmin),
		constants.RoleCodeTenantAdmin:  createAuthorityRole(t, db, constants.RoleCodeTenantAdmin, constants.RoleScopeTenant, constants.RoleAuthorityTenantAdmin),
		constants.RoleCodeCsTeamLeader: createAuthorityRole(t, db, constants.RoleCodeCsTeamLeader, constants.RoleScopeTenant, constants.RoleAuthorityTeamLeader),
		constants.RoleCodeCsUser:       createAuthorityRole(t, db, constants.RoleCodeCsUser, constants.RoleScopeTenant, constants.RoleAuthorityMember),
		constants.RoleCodeStoreStaff:   createAuthorityRole(t, db, constants.RoleCodeStoreStaff, constants.RoleScopeTenant, constants.RoleAuthorityMember),
	}
	return roles
}

func setupRoleAuthorityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupAuthServiceTestDB(t)
	if err := db.AutoMigrate(
		&models.Conversation{},
		&models.AgentTeam{},
		&models.AgentProfile{},
		&models.StoreStaffBinding{},
	); err != nil {
		t.Fatalf("migrate role authority dependencies: %v", err)
	}
	return db
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

func assertUserRoleCodes(t *testing.T, db *gorm.DB, userID int64, want ...string) {
	t.Helper()
	var codes []string
	if err := db.Table("t_user_role AS ur").
		Select("r.code").
		Joins("JOIN t_role AS r ON r.id = ur.role_id").
		Where("ur.user_id = ?", userID).
		Order("r.code ASC").
		Scan(&codes).Error; err != nil {
		t.Fatalf("find user role codes: %v", err)
	}
	slices.Sort(want)
	if !slices.Equal(codes, want) {
		t.Fatalf("user role codes = %v, want %v", codes, want)
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
