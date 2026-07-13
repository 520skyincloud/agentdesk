package services

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestResolveAuthPrincipalEnforcesTenantContext(t *testing.T) {
	db := setupAuthServiceTestDB(t)
	roles := seedAuthorityRoles(t, db)
	legacyTenant := findAuthorityTenant(t, db, constants.LegacyDefaultTenantCode)
	otherTenant := createAuthContextTenant(t, db, "other-tenant", enums.StatusOk)
	platformUser := createAuthorityUser(t, db, "context_platform")
	assignAuthorityRole(t, db, platformUser.ID, roles[constants.RoleCodeSuperAdmin].ID)
	tenantUser := createAuthorityUser(t, db, "context_tenant")
	setAuthContextUserTenant(t, db, tenantUser.ID, legacyTenant.ID)
	tenantUser.TenantID = legacyTenant.ID
	assignAuthorityRole(t, db, tenantUser.ID, roles[constants.RoleCodeCsUser].ID)

	svc := newAuthService()
	platformPrincipal, err := svc.resolveAuthPrincipal(db, platformUser,
		[]string{constants.RoleCodeSuperAdmin},
		[]string{constants.PermissionTenantSwitch.Code},
		"")
	if err != nil {
		t.Fatalf("resolve platform principal: %v", err)
	}
	if !platformPrincipal.IsPlatformAccount || !platformPrincipal.CanSwitchTenant || platformPrincipal.ActiveTenantID != 0 {
		t.Fatalf("unexpected platform principal: %+v", platformPrincipal)
	}

	platformPrincipal, err = svc.resolveAuthPrincipal(db, platformUser,
		[]string{constants.RoleCodeSuperAdmin},
		[]string{constants.PermissionTenantSwitch.Code},
		stringInt64(legacyTenant.ID))
	if err != nil {
		t.Fatalf("switch platform principal tenant: %v", err)
	}
	if platformPrincipal.ActiveTenantID != legacyTenant.ID {
		t.Fatalf("active tenant = %d, want %d", platformPrincipal.ActiveTenantID, legacyTenant.ID)
	}

	_, err = svc.resolveAuthPrincipal(db, platformUser,
		[]string{constants.RoleCodeSuperAdmin}, nil, stringInt64(legacyTenant.ID))
	if !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected tenant switch without permission to be forbidden, got %v", err)
	}

	tenantPrincipal, err := svc.resolveAuthPrincipal(db, tenantUser,
		[]string{constants.RoleCodeCsUser}, nil, "")
	if err != nil {
		t.Fatalf("resolve tenant principal: %v", err)
	}
	if tenantPrincipal.IsPlatformAccount || tenantPrincipal.ActiveTenantID != legacyTenant.ID {
		t.Fatalf("unexpected tenant principal: %+v", tenantPrincipal)
	}
	_, err = svc.resolveAuthPrincipal(db, tenantUser,
		[]string{constants.RoleCodeCsUser}, nil, stringInt64(otherTenant.ID))
	if !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected cross-tenant header to be forbidden, got %v", err)
	}

	if err := db.Model(&models.Tenant{}).Where("id = ?", legacyTenant.ID).Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable tenant: %v", err)
	}
	_, err = svc.resolveAuthPrincipal(db, tenantUser, []string{constants.RoleCodeCsUser}, nil, "")
	if !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected disabled tenant to block authentication, got %v", err)
	}
}

func TestAuthenticateUsesPerRequestTenantHeader(t *testing.T) {
	db := setupAuthServiceTestDB(t)
	roles := seedAuthorityRoles(t, db)
	legacyTenant := findAuthorityTenant(t, db, constants.LegacyDefaultTenantCode)
	permission := createAuthorityPermission(t, db, constants.PermissionTenantSwitch.Code, constants.PermissionScopePlatform)
	platformUser := createAuthTestUser(t, db, "tenant_switch_admin", "secret")
	assignAuthorityRole(t, db, platformUser.ID, roles[constants.RoleCodeSuperAdmin].ID)
	if err := db.Create(&models.RolePermission{RoleID: roles[constants.RoleCodeSuperAdmin].ID, PermissionID: permission.ID}).Error; err != nil {
		t.Fatalf("assign switch permission: %v", err)
	}

	login, err := newAuthService().Login(request.LoginRequest{Username: platformUser.Username, Password: "secret"}, config.AuthConfig{TokenTTLHours: 1}, "127.0.0.1", "go-test")
	if err != nil {
		t.Fatalf("login platform user: %v", err)
	}
	if !login.IsPlatformAccount || login.ActiveTenantID != 0 {
		t.Fatalf("unexpected login context: %+v", login)
	}

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = newAuthContextRequest(login.AccessToken, legacyTenant.ID)
	principal, err := newAuthService().Authenticate(ctx)
	if err != nil {
		t.Fatalf("authenticate switched request: %v", err)
	}
	if principal.ActiveTenantID != legacyTenant.ID || !principal.CanSwitchTenant {
		t.Fatalf("unexpected request principal: %+v", principal)
	}
}

func TestUserTenantScopeAndCrossTenantManagement(t *testing.T) {
	db := setupAuthServiceTestDB(t)
	legacyTenant := findAuthorityTenant(t, db, constants.LegacyDefaultTenantCode)
	otherTenant := createAuthContextTenant(t, db, "scope-other", enums.StatusOk)
	platformUser := createAuthorityUser(t, db, "scope_platform")
	legacyUser := createAuthorityUser(t, db, "scope_legacy")
	otherUser := createAuthorityUser(t, db, "scope_other")
	setAuthContextUserTenant(t, db, legacyUser.ID, legacyTenant.ID)
	setAuthContextUserTenant(t, db, otherUser.ID, otherTenant.ID)

	platformRoot := &dto.AuthPrincipal{UserID: 900, IsPlatformAccount: true, Roles: []string{constants.RoleCodeSuperAdmin}}
	assertScopedUsers(t, platformRoot, []int64{platformUser.ID})
	platformRoot.ActiveTenantID = legacyTenant.ID
	assertScopedUsers(t, platformRoot, []int64{legacyUser.ID})
	tenantOperator := &dto.AuthPrincipal{UserID: 901, TenantID: legacyTenant.ID, ActiveTenantID: legacyTenant.ID, Roles: []string{constants.RoleCodeTenantAdmin}}
	assertScopedUsers(t, tenantOperator, []int64{legacyUser.ID})
	if UserService.CanViewUser(tenantOperator, otherUser) || UserService.CanManageUser(tenantOperator, otherUser) {
		t.Fatal("tenant operator must not view or manage another tenant account")
	}
	if UserService.GetInScope(otherUser.ID, tenantOperator) != nil {
		t.Fatal("tenant-scoped account lookup must not return another tenant account")
	}
	if scoped := UserService.GetInScope(legacyUser.ID, tenantOperator); scoped == nil || scoped.ID != legacyUser.ID {
		t.Fatalf("tenant-scoped account lookup did not return own tenant account: %+v", scoped)
	}
	if err := UserService.UpdateStatus(otherUser.ID, int(enums.StatusDisabled), tenantOperator); !hasCode(err, errorsx.CodeInvalidParam) {
		t.Fatalf("cross-tenant account update must behave as not found, got %v", err)
	}
	if unchanged := UserService.Get(otherUser.ID); unchanged == nil || unchanged.Status != enums.StatusOk {
		t.Fatalf("cross-tenant account was modified: %+v", unchanged)
	}
}

func TestResolveAuthPrincipalRejectsPendingAndInconsistentAccounts(t *testing.T) {
	db := setupAuthServiceTestDB(t)
	roles := seedAuthorityRoles(t, db)
	legacyTenant := findAuthorityTenant(t, db, constants.LegacyDefaultTenantCode)
	pendingUser := createAuthorityUser(t, db, "pending_tenant_user")
	setAuthContextUserTenant(t, db, pendingUser.ID, legacyTenant.ID)
	pendingUser.TenantID = legacyTenant.ID
	if err := db.Model(&models.User{}).Where("id = ?", pendingUser.ID).Update("approval_status", enums.UserApprovalStatusPending).Error; err != nil {
		t.Fatalf("mark user pending: %v", err)
	}
	pendingUser.ApprovalStatus = enums.UserApprovalStatusPending
	_, err := newAuthService().resolveAuthPrincipal(db, pendingUser, nil, nil, "")
	if !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected pending account to be forbidden, got %v", err)
	}

	inconsistentUser := createAuthorityUser(t, db, "inconsistent_platform_user")
	setAuthContextUserTenant(t, db, inconsistentUser.ID, legacyTenant.ID)
	inconsistentUser.TenantID = legacyTenant.ID
	assignAuthorityRole(t, db, inconsistentUser.ID, roles[constants.RoleCodeAdmin].ID)
	_, err = newAuthService().resolveAuthPrincipal(db, inconsistentUser, []string{constants.RoleCodeAdmin}, nil, "")
	if !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected tenant account with platform role to be forbidden, got %v", err)
	}

	mixedUser := createAuthorityUser(t, db, "mixed_scope_account")
	assignAuthorityRole(t, db, mixedUser.ID, roles[constants.RoleCodeAdmin].ID)
	assignAuthorityRole(t, db, mixedUser.ID, roles[constants.RoleCodeCsUser].ID)
	_, err = newAuthService().resolveAuthPrincipal(db, mixedUser,
		[]string{constants.RoleCodeAdmin, constants.RoleCodeCsUser}, nil, "")
	if !hasCode(err, errorsx.CodeAuthForbidden) {
		t.Fatalf("expected mixed platform and tenant roles to be forbidden, got %v", err)
	}
}

func createAuthContextTenant(t *testing.T, db *gorm.DB, code string, status enums.Status) *models.Tenant {
	t.Helper()
	now := time.Now()
	tenant := &models.Tenant{
		TenantCode:         code,
		LegalName:          code,
		ShortName:          code,
		RegistrationType:   "test",
		RegistrationNo:     "REG-" + code,
		VerificationStatus: enums.TenantVerificationStatusVerified,
		Status:             status,
		AuditFields:        models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return tenant
}

func findAuthorityTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	var tenant models.Tenant
	if err := db.Where("tenant_code = ?", code).Take(&tenant).Error; err != nil {
		t.Fatalf("find tenant %s: %v", code, err)
	}
	return &tenant
}

func setAuthContextUserTenant(t *testing.T, db *gorm.DB, userID, tenantID int64) {
	t.Helper()
	if err := db.Model(&models.User{}).Where("id = ?", userID).Update("tenant_id", tenantID).Error; err != nil {
		t.Fatalf("set user tenant: %v", err)
	}
}

func assertScopedUsers(t *testing.T, operator *dto.AuthPrincipal, want []int64) {
	t.Helper()
	cnd := UserService.ApplyTenantScope(sqls.NewCnd().Asc("id"), operator)
	users := UserService.Find(cnd)
	got := make([]int64, 0, len(users))
	for i := range users {
		got = append(got, users[i].ID)
	}
	if len(got) != len(want) {
		t.Fatalf("scoped users = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scoped users = %v, want %v", got, want)
		}
	}
}

func stringInt64(value int64) string {
	return fmt.Sprintf("%d", value)
}

func newAuthContextRequest(token string, tenantID int64) *http.Request {
	req := httptest.NewRequest("GET", "/api/auth/profile", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(constants.TenantHeaderName, stringInt64(tenantID))
	return req
}
