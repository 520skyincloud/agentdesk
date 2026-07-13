package migration

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestSyncTenantAuthFoundationRejectsLegacyOverridesWithoutDeletingThem(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	if err := db.Exec(`CREATE TABLE t_user_permission (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id BIGINT NOT NULL,
		permission_id BIGINT NOT NULL,
		effect INTEGER NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create legacy override table: %v", err)
	}
	if err := db.Exec("INSERT INTO t_user_permission(user_id, permission_id, effect) VALUES (1, 2, 1)").Error; err != nil {
		t.Fatalf("seed legacy override: %v", err)
	}

	err := db.Transaction(syncTenantAuthFoundation)
	if err == nil || !strings.Contains(err.Error(), "legacy account permission overrides") {
		t.Fatalf("expected migration to reject legacy overrides, got %v", err)
	}
	var count int64
	if err := db.Table("t_user_permission").Count(&count).Error; err != nil {
		t.Fatalf("count preserved legacy overrides: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected legacy override to be preserved, got %d rows", count)
	}
	var tenantAdminCount int64
	if err := db.Model(&models.Role{}).Where("code = ?", constants.RoleCodeTenantAdmin).Count(&tenantAdminCount).Error; err != nil {
		t.Fatalf("count tenant admin role: %v", err)
	}
	if tenantAdminCount != 0 {
		t.Fatal("expected migration transaction to make no auth changes after override rejection")
	}
}

func TestSyncTenantAuthFoundationCreatesRoleAndRemovesDeadPermission(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	now := time.Now()
	legacyPermission := &models.Permission{
		Name:        "同步权限",
		Code:        "permission.sync",
		Type:        "api",
		Scope:       constants.PermissionScopePlatform,
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(legacyPermission).Error; err != nil {
		t.Fatalf("seed dead permission: %v", err)
	}

	if err := db.Transaction(syncTenantAuthFoundation); err != nil {
		t.Fatalf("sync tenant auth foundation: %v", err)
	}
	var tenantAdmin models.Role
	if err := db.Where("code = ?", constants.RoleCodeTenantAdmin).Take(&tenantAdmin).Error; err != nil {
		t.Fatalf("find tenant admin role: %v", err)
	}
	if tenantAdmin.Scope != constants.RoleScopeTenant || tenantAdmin.AuthorityLevel != constants.RoleAuthorityTenantAdmin {
		t.Fatalf("unexpected tenant admin role: %+v", tenantAdmin)
	}
	var legacyCount int64
	if err := db.Model(&models.Permission{}).Where("code = ?", "permission.sync").Count(&legacyCount).Error; err != nil {
		t.Fatalf("count dead permission: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("expected dead permission to be removed, got %d", legacyCount)
	}
}

func setupTenantAuthFoundationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Role{}, &models.Permission{}, &models.RolePermission{}); err != nil {
		t.Fatalf("migrate auth foundation tables: %v", err)
	}
	return db
}
