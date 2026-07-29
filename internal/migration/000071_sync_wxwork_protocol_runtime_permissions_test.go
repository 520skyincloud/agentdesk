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

func TestSyncWxWorkProtocolRuntimePermissionsSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "w71_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	runSyncWxWorkProtocolRuntimePermissionsScenario(t, db)
}

func TestSyncWxWorkProtocolRuntimePermissionsMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "w71_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	runSyncWxWorkProtocolRuntimePermissionsScenario(t, db)
}

func runSyncWxWorkProtocolRuntimePermissionsScenario(t *testing.T, db *gorm.DB) {
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
	for i := 0; i < 2; i++ {
		if err := syncWxWorkProtocolRuntimePermissions(db); err != nil {
			t.Fatalf("sync pass %d: %v", i+1, err)
		}
	}
	for _, permission := range wxWorkProtocolRuntimePermissionSpecs {
		var count int64
		if err := db.Model(&models.Permission{}).Where("code = ?", permission.Code).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("permission %s count=%d err=%v", permission.Code, count, err)
		}
		for _, roleCode := range []string{constants.RoleCodeSuperAdmin, constants.RoleCodeAdmin} {
			var relationCount int64
			if err := db.Table("w71_role_permission AS rp").
				Joins("JOIN w71_role AS r ON r.id = rp.role_id").
				Joins("JOIN w71_permission AS p ON p.id = rp.permission_id").
				Where("r.code = ? AND p.code = ?", roleCode, permission.Code).
				Count(&relationCount).Error; err != nil || relationCount != 1 {
				t.Fatalf("role=%s permission=%s count=%d err=%v", roleCode, permission.Code, relationCount, err)
			}
		}
	}
}
