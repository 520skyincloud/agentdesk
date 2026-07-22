package migration

import (
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestSyncCustomerTagPermissionsPreservesStableCodeAndRetiresLegacyActions(t *testing.T) {
	db := setupTenantAuthFoundationTestDB(t)
	runSyncCustomerTagPermissionsScenario(t, db)
}

func TestSyncCustomerTagPermissionsMySQL(t *testing.T) {
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
				t.Errorf("drop MySQL customer-tag permission fixture %T: %v", table, dropErr)
			}
		}
	})
	runSyncCustomerTagPermissionsScenario(t, db)
}

func runSyncCustomerTagPermissionsScenario(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now()
	legacySpecs := []constants.Permission{
		constants.PermissionConversationTag,
		constants.PermissionTagCreate,
		constants.PermissionTagUpdate,
		constants.PermissionTagDelete,
	}
	legacyByCode := make(map[string]*models.Permission, len(legacySpecs))
	for _, spec := range legacySpecs {
		name := spec.Name
		apiPath := spec.APIPath
		if spec.Code == constants.PermissionConversationTag.Code {
			name = "管理会话标签"
			apiPath = "/api/dashboard/conversation/add_tag"
		}
		item := &models.Permission{
			Name: name, Code: spec.Code, Type: spec.Type, Scope: constants.NormalizePermissionScope(spec.Scope),
			GroupName: spec.GroupName, Method: spec.Method, APIPath: apiPath, SortNo: spec.SortNo,
			Status: enums.StatusOk, IsBuiltin: true,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
		legacyByCode[item.Code] = item
	}
	stableConversationPermissionID := legacyByCode[constants.PermissionConversationTag.Code].ID

	roles, err := ensureRoles(db)
	if err != nil {
		t.Fatal(err)
	}
	customRole := &models.Role{
		Name: "历史自定义角色", Code: "legacy_tag_role", Scope: constants.RoleScopeTenant,
		AuthorityLevel: constants.RoleAuthorityMember, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(customRole).Error; err != nil {
		t.Fatal(err)
	}
	for _, binding := range []struct {
		roleID       int64
		permissionID int64
	}{
		{roles[constants.RoleCodeCsTeamLeader].ID, legacyByCode[constants.PermissionTagCreate.Code].ID},
		{roles[constants.RoleCodeCsTeamLeader].ID, legacyByCode[constants.PermissionTagUpdate.Code].ID},
		{roles[constants.RoleCodeCsTeamLeader].ID, legacyByCode[constants.PermissionTagDelete.Code].ID},
		{customRole.ID, legacyByCode[constants.PermissionTagCreate.Code].ID},
		{customRole.ID, legacyByCode[constants.PermissionTagDelete.Code].ID},
	} {
		if err := db.Create(&models.RolePermission{
			RoleID: binding.roleID, PermissionID: binding.permissionID,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	for run := 0; run < 2; run++ {
		if err := syncCustomerTagPermissions(db); err != nil {
			t.Fatalf("sync run %d: %v", run+1, err)
		}
	}

	var conversationPermission models.Permission
	if err := db.Where("code = ?", constants.PermissionConversationTag.Code).Take(&conversationPermission).Error; err != nil {
		t.Fatal(err)
	}
	if conversationPermission.ID != stableConversationPermissionID ||
		conversationPermission.Name != constants.PermissionConversationTag.Name ||
		conversationPermission.APIPath != constants.PermissionConversationTag.APIPath ||
		conversationPermission.Status != enums.StatusOk {
		t.Fatalf("stable customer-tag permission=%#v", conversationPermission)
	}

	for code, wantName := range map[string]string{
		constants.PermissionTagCreate.Code: "已废弃：创建自定义标签",
		constants.PermissionTagDelete.Code: "已废弃：删除自定义标签",
	} {
		var permission models.Permission
		if err := db.Where("code = ?", code).Take(&permission).Error; err != nil {
			t.Fatal(err)
		}
		if permission.Status != enums.StatusDisabled || permission.Name != wantName {
			t.Fatalf("retired permission %s=%#v", code, permission)
		}
		var bindings int64
		if err := db.Model(&models.RolePermission{}).Where("permission_id = ?", permission.ID).Count(&bindings).Error; err != nil {
			t.Fatal(err)
		}
		if bindings != 0 {
			t.Fatalf("retired permission %s still has %d role bindings", code, bindings)
		}
	}

	var tagView, tagUpdate models.Permission
	if err := db.Where("code = ?", constants.PermissionTagView.Code).Take(&tagView).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("code = ?", constants.PermissionTagUpdate.Code).Take(&tagUpdate).Error; err != nil {
		t.Fatal(err)
	}
	assertRolePermissionCount(t, db, constants.RoleCodeCsTeamLeader, conversationPermission.ID, 1)
	assertRolePermissionCount(t, db, constants.RoleCodeCsTeamLeader, tagView.ID, 1)
	assertRolePermissionCount(t, db, constants.RoleCodeCsTeamLeader, tagUpdate.ID, 0)
	assertRolePermissionCount(t, db, constants.RoleCodeTenantAdmin, conversationPermission.ID, 1)
	assertRolePermissionCount(t, db, constants.RoleCodeTenantAdmin, tagView.ID, 1)
	assertRolePermissionCount(t, db, constants.RoleCodeTenantAdmin, tagUpdate.ID, 1)

	permissions, err := ensurePermissions(db)
	if err != nil {
		t.Fatal(err)
	}
	roles, err = ensureRoles(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRolePermissions(db, roles, permissions); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{constants.PermissionTagCreate.Code, constants.PermissionTagDelete.Code} {
		var permission models.Permission
		if err := db.Where("code = ?", code).Take(&permission).Error; err != nil {
			t.Fatal(err)
		}
		if permission.Status != enums.StatusDisabled {
			t.Fatalf("later permission sync re-enabled %s", code)
		}
	}
}
