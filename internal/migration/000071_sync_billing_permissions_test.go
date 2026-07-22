package migration

import (
	"os"
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestSyncBillingPermissions(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	runSyncBillingPermissionsScenario(t, db)
}

func TestSyncBillingPermissionsMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	if err = db.AutoMigrate(&models.Role{}, &models.Permission{}, &models.RolePermission{}); err != nil {
		t.Fatalf("migrate MySQL auth foundation tables: %v", err)
	}
	t.Cleanup(func() {
		for _, table := range []any{&models.RolePermission{}, &models.Permission{}, &models.Role{}} {
			if dropErr := db.Migrator().DropTable(table); dropErr != nil {
				t.Errorf("drop MySQL billing permission fixture %T: %v", table, dropErr)
			}
		}
	})
	runSyncBillingPermissionsScenario(t, db)
}

func runSyncBillingPermissionsScenario(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := syncBillingPermissions(db); err != nil {
		t.Fatalf("sync billing permissions: %v", err)
	}

	for _, permission := range []constants.Permission{constants.PermissionBillingView, constants.PermissionBillingExport} {
		var stored models.Permission
		if err := db.Where("code = ?", permission.Code).Take(&stored).Error; err != nil {
			t.Fatalf("find permission %s: %v", permission.Code, err)
		}
		for _, roleCode := range []string{
			constants.RoleCodeSuperAdmin,
			constants.RoleCodeAdmin,
			constants.RoleCodeTenantAdmin,
			constants.RoleCodeStoreStaff,
		} {
			assertRolePermissionCount(t, db, roleCode, stored.ID, 1)
		}
		for _, roleCode := range []string{constants.RoleCodeCsTeamLeader, constants.RoleCodeCsUser} {
			assertRolePermissionCount(t, db, roleCode, stored.ID, 0)
		}
	}

	if err := syncBillingPermissions(db); err != nil {
		t.Fatalf("sync billing permissions twice: %v", err)
	}
	for _, permission := range []constants.Permission{constants.PermissionBillingView, constants.PermissionBillingExport} {
		var count int64
		if err := db.Model(&models.Permission{}).Where("code = ?", permission.Code).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("permission %s count=%d err=%v", permission.Code, count, err)
		}
	}
}
