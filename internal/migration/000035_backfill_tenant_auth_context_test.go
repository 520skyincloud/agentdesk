package migration

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestBackfillTenantAuthContextSeparatesPlatformAndLegacyAccounts(t *testing.T) {
	db := setupTenantAuthBackfillTestDB(t)
	platformRole := createTenantBackfillRole(t, db, "platform_admin", constants.RoleScopePlatform)
	tenantRole := createTenantBackfillRole(t, db, "tenant_agent", constants.RoleScopeTenant)
	platformUser := createTenantBackfillUser(t, db, "platform_user")
	tenantUser := createTenantBackfillUser(t, db, "tenant_user")
	unassignedUser := createTenantBackfillUser(t, db, "unassigned_user")
	createTenantBackfillUserRole(t, db, platformUser.ID, platformRole.ID)
	createTenantBackfillUserRole(t, db, tenantUser.ID, tenantRole.ID)

	if err := db.Transaction(backfillTenantAuthContext); err != nil {
		t.Fatalf("backfill tenant auth context: %v", err)
	}
	legacyTenant := findLegacyTenant(t, db)

	assertBackfilledUser(t, db, platformUser.ID, 0, enums.UserRegistrationSourcePlatform)
	assertBackfilledUser(t, db, tenantUser.ID, legacyTenant.ID, enums.UserRegistrationSourceLegacyMigration)
	assertBackfilledUser(t, db, unassignedUser.ID, legacyTenant.ID, enums.UserRegistrationSourceLegacyMigration)

	if err := db.Transaction(backfillTenantAuthContext); err != nil {
		t.Fatalf("repeat backfill tenant auth context: %v", err)
	}
	var tenantCount int64
	if err := db.Model(&models.Tenant{}).Where("tenant_code = ?", constants.LegacyDefaultTenantCode).Count(&tenantCount).Error; err != nil {
		t.Fatalf("count legacy tenant: %v", err)
	}
	if tenantCount != 1 {
		t.Fatalf("expected idempotent legacy tenant creation, got %d", tenantCount)
	}

	otherTenant := createTenantBackfillTenant(t, db, "future-tenant")
	futureUser := createTenantBackfillUser(t, db, "future_invited_user")
	if err := db.Model(&models.User{}).Where("id = ?", futureUser.ID).Updates(map[string]any{
		"tenant_id":           otherTenant.ID,
		"registration_source": enums.UserRegistrationSourceInvitation,
		"approval_status":     enums.UserApprovalStatusPending,
	}).Error; err != nil {
		t.Fatalf("prepare future tenant user: %v", err)
	}
	if err := db.Transaction(backfillTenantAuthContext); err != nil {
		t.Fatalf("repeat backfill after future account: %v", err)
	}
	var preserved models.User
	if err := db.Take(&preserved, "id = ?", futureUser.ID).Error; err != nil {
		t.Fatalf("find future tenant user: %v", err)
	}
	if preserved.TenantID != otherTenant.ID || preserved.RegistrationSource != enums.UserRegistrationSourceInvitation || preserved.ApprovalStatus != enums.UserApprovalStatusPending {
		t.Fatalf("future tenant user was overwritten by repeated backfill: %+v", preserved)
	}
}

func TestBackfillTenantAuthContextRejectsMixedRoleScopes(t *testing.T) {
	db := setupTenantAuthBackfillTestDB(t)
	platformRole := createTenantBackfillRole(t, db, "mixed_platform", constants.RoleScopePlatform)
	tenantRole := createTenantBackfillRole(t, db, "mixed_tenant", constants.RoleScopeTenant)
	user := createTenantBackfillUser(t, db, "mixed_scope_user")
	createTenantBackfillUserRole(t, db, user.ID, platformRole.ID)
	createTenantBackfillUserRole(t, db, user.ID, tenantRole.ID)

	err := db.Transaction(backfillTenantAuthContext)
	if err == nil {
		t.Fatal("expected mixed role scopes to stop tenant backfill")
	}
	var tenantCount int64
	if countErr := db.Model(&models.Tenant{}).Count(&tenantCount).Error; countErr != nil {
		t.Fatalf("count tenants after rejected backfill: %v", countErr)
	}
	if tenantCount != 0 {
		t.Fatalf("rejected backfill must roll back tenant creation, got %d tenants", tenantCount)
	}
}

func setupTenantAuthBackfillTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Tenant{}, &models.User{}, &models.Role{}, &models.UserRole{}); err != nil {
		t.Fatalf("migrate tenant auth tables: %v", err)
	}
	return db
}

func createTenantBackfillRole(t *testing.T, db *gorm.DB, code, scope string) *models.Role {
	t.Helper()
	now := time.Now()
	role := &models.Role{
		Name:           code,
		Code:           code,
		Scope:          scope,
		AuthorityLevel: 20,
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(role).Error; err != nil {
		t.Fatalf("create role: %v", err)
	}
	return role
}

func createTenantBackfillUser(t *testing.T, db *gorm.DB, username string) *models.User {
	t.Helper()
	now := time.Now()
	user := &models.User{
		Username:    username,
		Nickname:    username,
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func createTenantBackfillTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	now := time.Now()
	tenant := &models.Tenant{
		TenantCode:         code,
		LegalName:          code,
		ShortName:          code,
		RegistrationType:   "test",
		RegistrationNo:     "REG-" + code,
		VerificationStatus: enums.TenantVerificationStatusVerified,
		Status:             enums.StatusOk,
		AuditFields:        models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	return tenant
}

func createTenantBackfillUserRole(t *testing.T, db *gorm.DB, userID, roleID int64) {
	t.Helper()
	if err := db.Create(&models.UserRole{UserID: userID, RoleID: roleID}).Error; err != nil {
		t.Fatalf("create user role: %v", err)
	}
}

func findLegacyTenant(t *testing.T, db *gorm.DB) *models.Tenant {
	t.Helper()
	var tenant models.Tenant
	if err := db.Where("tenant_code = ?", constants.LegacyDefaultTenantCode).Take(&tenant).Error; err != nil {
		t.Fatalf("find legacy tenant: %v", err)
	}
	return &tenant
}

func assertBackfilledUser(t *testing.T, db *gorm.DB, userID, tenantID int64, source enums.UserRegistrationSource) {
	t.Helper()
	var user models.User
	if err := db.Take(&user, "id = ?", userID).Error; err != nil {
		t.Fatalf("find user %d: %v", userID, err)
	}
	if user.TenantID != tenantID || user.RegistrationSource != source || user.ApprovalStatus != enums.UserApprovalStatusApproved {
		t.Fatalf("unexpected backfilled user: %+v", user)
	}
}
