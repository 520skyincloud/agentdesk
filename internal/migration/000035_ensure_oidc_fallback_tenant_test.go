package migration

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestEnsureOIDCFallbackTenantCreatesOnlyReservedTenant(t *testing.T) {
	db := setupOIDCFallbackTenantTestDB(t)
	user := &models.User{
		Username: "unassigned-user",
		Nickname: "Unassigned User",
		Status:   enums.StatusOk,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create untouched user: %v", err)
	}
	for run := 0; run < 2; run++ {
		if err := db.Transaction(ensureOIDCFallbackTenant); err != nil {
			t.Fatalf("ensure OIDC fallback tenant run %d: %v", run+1, err)
		}
	}
	var tenantCount int64
	if err := db.Model(&models.Tenant{}).Where("tenant_code = ?", constants.LegacyDefaultTenantCode).Count(&tenantCount).Error; err != nil {
		t.Fatalf("count OIDC fallback tenants: %v", err)
	}
	if tenantCount != 1 {
		t.Fatalf("OIDC fallback tenant count=%d want=1", tenantCount)
	}
	var tenant models.Tenant
	if err := db.Where("tenant_code = ?", constants.LegacyDefaultTenantCode).Take(&tenant).Error; err != nil {
		t.Fatalf("load OIDC fallback tenant: %v", err)
	}
	if tenant.RegistrationType != "system" || tenant.RegistrationNo != "SYSTEM-OIDC-FALLBACK" ||
		tenant.Status != enums.StatusOk || tenant.VerificationStatus != enums.TenantVerificationStatusVerified {
		t.Fatalf("unexpected OIDC fallback tenant: %+v", tenant)
	}
	var preserved models.User
	if err := db.First(&preserved, user.ID).Error; err != nil {
		t.Fatalf("load untouched user: %v", err)
	}
	if preserved.TenantID != 0 || preserved.RegistrationSource != enums.UserRegistrationSourcePlatform {
		t.Fatalf("fresh migration mutated an existing user: %+v", preserved)
	}
}

func setupOIDCFallbackTenantTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	if err := db.AutoMigrate(&models.Tenant{}, &models.User{}); err != nil {
		t.Fatalf("migrate OIDC fallback fixtures: %v", err)
	}
	return db
}
