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

func TestSyncArrivalPermissionsSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), arrivalPermissionTestConfig("a70_"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	runSyncArrivalPermissionsScenario(t, db)
}

func TestSyncArrivalPermissionsMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), arrivalPermissionTestConfig("a70_"))
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	runSyncArrivalPermissionsScenario(t, db)
}

func arrivalPermissionTestConfig(prefix string) *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   prefix,
			SingularTable: true,
		},
	}
}

func runSyncArrivalPermissionsScenario(t *testing.T, db *gorm.DB) {
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

	roles, err := ensureRoles(db)
	if err != nil {
		t.Fatalf("seed roles: %v", err)
	}
	unrelatedPermissions, err := ensurePermissionSpecs(db, []constants.Permission{constants.PermissionDashboardView})
	if err != nil {
		t.Fatalf("seed unrelated permission: %v", err)
	}
	if err := ensureRolePermissionsByCode(
		db,
		roles,
		unrelatedPermissions,
		map[string]struct{}{constants.PermissionDashboardView.Code: {}},
	); err != nil {
		t.Fatalf("seed unrelated role permission: %v", err)
	}

	if err := syncArrivalPermissions(db); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	assertArrivalPermissionAssignments(t, db)

	var firstRelationCount int64
	if err := db.Model(&models.RolePermission{}).Count(&firstRelationCount).Error; err != nil {
		t.Fatalf("count first role permissions: %v", err)
	}
	if err := syncArrivalPermissions(db); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	var secondRelationCount int64
	if err := db.Model(&models.RolePermission{}).Count(&secondRelationCount).Error; err != nil {
		t.Fatalf("count second role permissions: %v", err)
	}
	if secondRelationCount != firstRelationCount {
		t.Fatalf("idempotent sync relation count=%d want=%d", secondRelationCount, firstRelationCount)
	}
	assertArrivalPermissionAssignments(t, db)
}

func assertArrivalPermissionAssignments(t *testing.T, db *gorm.DB) {
	t.Helper()
	wantByRole := map[string]map[string]bool{
		constants.RoleCodeSuperAdmin: {
			constants.PermissionArrivalConnectionView.Code:   true,
			constants.PermissionArrivalConnectionManage.Code: true,
			constants.PermissionArrivalConnectionInvite.Code: true,
			constants.PermissionArrivalAuditView.Code:        true,
		},
		constants.RoleCodeAdmin: {
			constants.PermissionArrivalConnectionView.Code:   true,
			constants.PermissionArrivalConnectionManage.Code: true,
			constants.PermissionArrivalConnectionInvite.Code: true,
			constants.PermissionArrivalAuditView.Code:        true,
		},
		constants.RoleCodeTenantAdmin: {
			constants.PermissionArrivalConnectionView.Code:   true,
			constants.PermissionArrivalConnectionManage.Code: true,
			constants.PermissionArrivalConnectionInvite.Code: true,
			constants.PermissionArrivalAuditView.Code:        true,
		},
		constants.RoleCodeCsTeamLeader: {
			constants.PermissionArrivalConnectionView.Code: true,
			constants.PermissionArrivalAuditView.Code:      true,
		},
		constants.RoleCodeCsUser: {
			constants.PermissionArrivalConnectionView.Code: true,
			constants.PermissionArrivalAuditView.Code:      true,
		},
		constants.RoleCodeStoreStaff: {},
	}
	for _, permission := range arrivalPermissionSpecs {
		var count int64
		if err := db.Model(&models.Permission{}).Where("code = ?", permission.Code).Count(&count).Error; err != nil {
			t.Fatalf("count permission %s: %v", permission.Code, err)
		}
		if count != 1 {
			t.Fatalf("permission %s count=%d want=1", permission.Code, count)
		}
	}
	for roleCode, expected := range wantByRole {
		var rows []string
		if err := db.Table("a70_role_permission AS rp").
			Select("p.code").
			Joins("JOIN a70_role AS r ON r.id = rp.role_id").
			Joins("JOIN a70_permission AS p ON p.id = rp.permission_id").
			Where("r.code = ? AND p.code LIKE ?", roleCode, "arrival%").
			Order("p.code ASC").
			Scan(&rows).Error; err != nil {
			t.Fatalf("load role %s arrival permissions: %v", roleCode, err)
		}
		got := make(map[string]bool, len(rows))
		for _, code := range rows {
			got[code] = true
		}
		if len(got) != len(expected) {
			t.Fatalf("role %s arrival permission count=%d want=%d: %v", roleCode, len(got), len(expected), rows)
		}
		for code := range expected {
			if !got[code] {
				t.Fatalf("role %s missing permission %s", roleCode, code)
			}
		}
	}
	var unrelatedCount int64
	if err := db.Table("a70_role_permission AS rp").
		Joins("JOIN a70_role AS r ON r.id = rp.role_id").
		Joins("JOIN a70_permission AS p ON p.id = rp.permission_id").
		Where("r.code = ? AND p.code = ?", constants.RoleCodeSuperAdmin, constants.PermissionDashboardView.Code).
		Count(&unrelatedCount).Error; err != nil {
		t.Fatalf("count unrelated permission: %v", err)
	}
	if unrelatedCount != 1 {
		t.Fatalf("unrelated role permission count=%d want=1", unrelatedCount)
	}
}
