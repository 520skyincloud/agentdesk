package migration

import (
	"os"
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestSyncRoleNavigationPermissionsSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), roleNavigationPermissionTestConfig("r76_"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	runSyncRoleNavigationPermissionsScenario(t, db, "r76_")
}

func TestSyncRoleNavigationPermissionsMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), roleNavigationPermissionTestConfig("r76_"))
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	runSyncRoleNavigationPermissionsScenario(t, db, "r76_")
}

func roleNavigationPermissionTestConfig(prefix string) *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   prefix,
			SingularTable: true,
		},
	}
}

func runSyncRoleNavigationPermissionsScenario(t *testing.T, db *gorm.DB, prefix string) {
	t.Helper()
	fixtures := []any{&models.Permission{}, &models.Role{}, &models.RolePermission{}}
	for i := len(fixtures) - 1; i >= 0; i-- {
		_ = db.Migrator().DropTable(fixtures[i])
	}
	if err := db.AutoMigrate(fixtures...); err != nil {
		t.Fatalf("migrate fixtures: %v", err)
	}
	t.Cleanup(func() {
		for i := len(fixtures) - 1; i >= 0; i-- {
			if err := db.Migrator().DropTable(fixtures[i]); err != nil {
				t.Errorf("drop fixture %T: %v", fixtures[i], err)
			}
		}
	})
	if _, err := ensureRoles(db); err != nil {
		t.Fatalf("seed roles: %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := syncRoleNavigationPermissions(db); err != nil {
			t.Fatalf("sync pass %d: %v", attempt+1, err)
		}
	}
	assertRoleHasPermission(t, db, prefix, constants.RoleCodeAdmin, constants.PermissionStoreWorkbenchView.Code)
	assertRoleHasPermission(t, db, prefix, constants.RoleCodeCsTeamLeader, constants.PermissionBillingView.Code)
	assertRoleHasPermission(t, db, prefix, constants.RoleCodeCsUser, constants.PermissionStoreView.Code)
	assertRoleHasPermission(t, db, prefix, constants.RoleCodeCsUser, constants.PermissionArrivalConnectionView.Code)
	assertRoleHasPermission(t, db, prefix, constants.RoleCodeCsUser, constants.PermissionArrivalAuditView.Code)
}

func assertRoleHasPermission(t *testing.T, db *gorm.DB, prefix, roleCode, permissionCode string) {
	t.Helper()
	var count int64
	if err := db.Table(prefix+"role_permission AS rp").
		Joins("JOIN "+prefix+"role AS r ON r.id = rp.role_id").
		Joins("JOIN "+prefix+"permission AS p ON p.id = rp.permission_id").
		Where("r.code = ? AND p.code = ?", roleCode, permissionCode).
		Count(&count).Error; err != nil {
		t.Fatalf("count role=%s permission=%s: %v", roleCode, permissionCode, err)
	}
	if count != 1 {
		t.Fatalf("role=%s permission=%s count=%d want=1", roleCode, permissionCode, count)
	}
}
