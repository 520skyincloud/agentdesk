package migration

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestRetireLocalKnowledgePermissions(t *testing.T) {
	db := openRetireLocalKnowledgePermissionsSQLite(t)
	runRetireLocalKnowledgePermissionsScenario(t, db, time.Now().UnixNano())
}

func TestRetireLocalKnowledgePermissionsMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), retireLocalKnowledgePermissionsGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateRetireLocalKnowledgePermissionTables(db); err != nil {
		t.Fatal(err)
	}
	runRetireLocalKnowledgePermissionsScenario(t, db, time.Now().UnixNano())
}

func runRetireLocalKnowledgePermissionsScenario(t *testing.T, db *gorm.DB, suffix int64) {
	t.Helper()
	role := &models.Role{Name: "legacy role", Code: fmt.Sprintf("legacy-knowledge-%d", suffix), Status: enums.StatusOk}
	if err := db.Create(role).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("code IN ?", retiredLocalKnowledgePermissionCodes).Delete(&models.Permission{}).Error; err != nil {
		t.Fatal(err)
	}
	permissionIDs := make([]int64, 0, len(retiredLocalKnowledgePermissionCodes))
	for _, code := range retiredLocalKnowledgePermissionCodes {
		permission := &models.Permission{Name: code, Code: code, Status: enums.StatusOk}
		if err := db.Create(permission).Error; err != nil {
			t.Fatal(err)
		}
		permissionIDs = append(permissionIDs, permission.ID)
		if err := db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = db.Where("role_id = ?", role.ID).Delete(&models.RolePermission{}).Error
		_ = db.Where("id IN ?", permissionIDs).Delete(&models.Permission{}).Error
		_ = db.Delete(role).Error
	})

	for run := 0; run < 2; run++ {
		if err := retireLocalKnowledgePermissions(db); err != nil {
			t.Fatalf("run %d: %v", run+1, err)
		}
	}
	for _, code := range retiredLocalKnowledgePermissionCodes {
		var permission models.Permission
		if err := db.Where("code = ?", code).Take(&permission).Error; err != nil {
			t.Fatal(err)
		}
		if permission.Status != enums.StatusDisabled {
			t.Fatalf("permission %s status=%d want disabled", code, permission.Status)
		}
		var bindingCount int64
		if err := db.Model(&models.RolePermission{}).Where("permission_id = ?", permission.ID).Count(&bindingCount).Error; err != nil {
			t.Fatal(err)
		}
		if bindingCount != 0 {
			t.Fatalf("permission %s role bindings=%d want=0", code, bindingCount)
		}
	}
}

func openRetireLocalKnowledgePermissionsSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), retireLocalKnowledgePermissionsGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateRetireLocalKnowledgePermissionTables(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func migrateRetireLocalKnowledgePermissionTables(db *gorm.DB) error {
	return db.AutoMigrate(&models.Role{}, &models.Permission{}, &models.RolePermission{})
}

func retireLocalKnowledgePermissionsGORMConfig() *gorm.Config {
	return &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	}
}
