package migration

import (
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestMigrateUnifiedModelProfilesSeedsNineSlotsWithoutLegacySecret(t *testing.T) {
	db := openUnifiedModelProfileMigrationDB(t)
	legacyKey := "legacy-secret-must-not-migrate"
	configs := []models.AIConfig{
		{Name: "chat", Provider: enums.AIProviderOpenAI, BaseURL: "https://newapi.example.com/v1", APIKey: legacyKey, ModelType: enums.AIModelTypeLLM, ModelName: "chat-model", MaxContextTokens: 8192, MaxOutputTokens: 1024, TimeoutMS: 30000, Status: enums.StatusOk, SortNo: 10},
		{Name: "intent", Provider: enums.AIProviderOpenAI, BaseURL: "https://newapi.example.com/v1", APIKey: legacyKey, ModelType: enums.AIModelTypeLLM, ModelName: "intent-model", MaxContextTokens: 8192, MaxOutputTokens: 1024, TimeoutMS: 30000, IntentDetectEnabled: true, Status: enums.StatusOk, SortNo: 20},
		{Name: "vision", Provider: enums.AIProviderOpenAI, APIKey: legacyKey, ModelType: enums.AIModelTypeVision, ModelName: "vision-model", MaxContextTokens: 8192, MaxOutputTokens: 1024, Status: enums.StatusOk},
		{Name: "asr", Provider: enums.AIProviderOpenAI, APIKey: legacyKey, ModelType: enums.AIModelTypeASR, ModelName: "asr-model", Status: enums.StatusOk},
		{Name: "embedding", Provider: enums.AIProviderOpenAI, APIKey: legacyKey, ModelType: enums.AIModelTypeEmbedding, ModelName: "embedding-model", Dimension: 1024, Status: enums.StatusOk},
		{Name: "rerank", Provider: enums.AIProviderOpenAI, APIKey: legacyKey, ModelType: enums.AIModelTypeRerank, ModelName: "rerank-model", Status: enums.StatusOk},
	}
	for i := range configs {
		configs[i].AuditFields = models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()}
	}
	if err := db.Create(&configs).Error; err != nil {
		t.Fatalf("create legacy configs: %v", err)
	}

	if err := migrateUnifiedModelProfiles(db); err != nil {
		t.Fatalf("migrateUnifiedModelProfiles() error=%v", err)
	}
	var profile models.ModelProfileTemplate
	if err := db.Where("code = ?", "standard").Take(&profile).Error; err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if profile.Status != enums.ModelProfileStatusCandidate || profile.GatewayBaseURL != "https://newapi.example.com/v1" {
		t.Fatalf("profile=%#v", profile)
	}
	var slots []models.ModelProfileSlot
	if err := db.Where("template_id = ?", profile.ID).Order("sort_no ASC").Find(&slots).Error; err != nil {
		t.Fatal(err)
	}
	if len(slots) != 9 {
		t.Fatalf("slot count=%d want=9", len(slots))
	}
	for _, slot := range slots {
		if slot.Provider != "newapi" {
			t.Fatalf("slot provider=%q", slot.Provider)
		}
		if slot.ModelName == legacyKey || slot.PromptTemplate == legacyKey || slot.JSONSchema == legacyKey {
			t.Fatalf("legacy API key leaked into slot %#v", slot)
		}
	}
	var credentialCount int64
	if err := db.Model(&models.StoreModelCredential{}).Count(&credentialCount).Error; err != nil {
		t.Fatal(err)
	}
	if credentialCount != 0 {
		t.Fatalf("credential count=%d; migration must not create credentials", credentialCount)
	}

	if err := migrateUnifiedModelProfiles(db); err != nil {
		t.Fatalf("idempotent migrate error=%v", err)
	}
	var profileCount, slotCount int64
	if err := db.Model(&models.ModelProfileTemplate{}).Count(&profileCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.ModelProfileSlot{}).Count(&slotCount).Error; err != nil {
		t.Fatal(err)
	}
	if profileCount != 1 || slotCount != 9 {
		t.Fatalf("idempotent counts profiles=%d slots=%d", profileCount, slotCount)
	}
}

func TestMigrateUnifiedModelProfilesReusesPermissionsAndRetiresLegacyBindings(t *testing.T) {
	db := openUnifiedModelProfileMigrationDB(t)
	legacyRole := &models.Role{
		Name: "legacy platform admin", Code: "legacy_platform_admin", Scope: constants.RoleScopePlatform,
		AuthorityLevel: constants.RoleAuthorityAdmin, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(legacyRole).Error; err != nil {
		t.Fatal(err)
	}
	for _, spec := range []constants.Permission{
		constants.PermissionAIConfigCreate, constants.PermissionAIConfigDelete,
		constants.PermissionTenantModelGrantView, constants.PermissionTenantModelGrantUpdate,
		constants.PermissionTenantModelAssignmentView, constants.PermissionTenantModelAssignmentUpdate,
	} {
		permission := &models.Permission{
			Name: spec.Name, Code: spec.Code, Type: spec.Type, Scope: spec.Scope,
			GroupName: spec.GroupName, Method: spec.Method, APIPath: spec.APIPath,
			Status: enums.StatusOk, IsBuiltin: true,
			AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
		}
		if err := db.Create(permission).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&models.RolePermission{
			RoleID: legacyRole.ID, PermissionID: permission.ID,
			AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateUnifiedModelProfiles(db); err != nil {
		t.Fatal(err)
	}
	var tenantAdmin models.Role
	if err := db.Where("code = ?", constants.RoleCodeTenantAdmin).Take(&tenantAdmin).Error; err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{constants.PermissionAIConfigView.Code, constants.PermissionAIConfigUpdate.Code} {
		var permission models.Permission
		if err := db.Where("code = ?", code).Take(&permission).Error; err != nil {
			t.Fatal(err)
		}
		if permission.Scope != constants.PermissionScopeTenant || permission.Status != enums.StatusOk {
			t.Fatalf("permission %s=%#v", code, permission)
		}
		var count int64
		if err := db.Model(&models.RolePermission{}).Where("role_id = ? AND permission_id = ?", tenantAdmin.ID, permission.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("tenant admin permission %s count=%d", code, count)
		}
	}
	for _, code := range []string{
		constants.PermissionAIConfigCreate.Code, constants.PermissionAIConfigDelete.Code,
		constants.PermissionTenantModelGrantView.Code, constants.PermissionTenantModelGrantUpdate.Code,
		constants.PermissionTenantModelAssignmentView.Code, constants.PermissionTenantModelAssignmentUpdate.Code,
	} {
		var permission models.Permission
		if err := db.Where("code = ?", code).Take(&permission).Error; err != nil {
			t.Fatal(err)
		}
		if permission.Status != enums.StatusDisabled {
			t.Fatalf("retired permission %s status=%v", code, permission.Status)
		}
		var count int64
		if err := db.Model(&models.RolePermission{}).Where("permission_id = ?", permission.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("retired permission %s still has %d bindings", code, count)
		}
	}
	permissions, err := ensurePermissions(db)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := ensureRoles(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureRolePermissions(db, roles, permissions); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{
		constants.PermissionAIConfigCreate.Code, constants.PermissionAIConfigDelete.Code,
		constants.PermissionTenantModelGrantView.Code, constants.PermissionTenantModelGrantUpdate.Code,
		constants.PermissionTenantModelAssignmentView.Code, constants.PermissionTenantModelAssignmentUpdate.Code,
	} {
		var permission models.Permission
		if err = db.Where("code = ?", code).Take(&permission).Error; err != nil {
			t.Fatal(err)
		}
		if permission.Status != enums.StatusDisabled {
			t.Fatalf("retired permission %s was re-enabled by a later permission sync", code)
		}
		var count int64
		if err = db.Model(&models.RolePermission{}).Where("permission_id = ?", permission.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("retired permission %s was rebound by a later permission sync", code)
		}
	}
}

func TestMigrateUnifiedModelProfilesMySQL(t *testing.T) {
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
		t.Fatalf("open MySQL migration database: %v", err)
	}
	if err = db.AutoMigrate(
		&models.Permission{}, &models.Role{}, &models.RolePermission{},
		&models.AIConfig{}, &models.ModelProfileTemplate{}, &models.ModelProfileSlot{},
		&models.StoreModelCredential{},
	); err != nil {
		t.Fatalf("MySQL AutoMigrate() error=%v", err)
	}
	if err = migrateUnifiedModelProfiles(db); err != nil {
		t.Fatalf("MySQL migrateUnifiedModelProfiles() error=%v", err)
	}
	if err = migrateUnifiedModelProfiles(db); err != nil {
		t.Fatalf("MySQL idempotent migration error=%v", err)
	}

	var profile models.ModelProfileTemplate
	if err = db.Where("code = ? AND revision = ?", "standard", 1).Take(&profile).Error; err != nil {
		t.Fatalf("load MySQL model profile: %v", err)
	}
	var slotCount, credentialCount int64
	if err = db.Model(&models.ModelProfileSlot{}).Where("template_id = ?", profile.ID).Count(&slotCount).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&models.StoreModelCredential{}).Count(&credentialCount).Error; err != nil {
		t.Fatal(err)
	}
	if slotCount != 9 || credentialCount != 0 {
		t.Fatalf("MySQL migration counts slots=%d credentials=%d", slotCount, credentialCount)
	}
}

func openUnifiedModelProfileMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Permission{}, &models.Role{}, &models.RolePermission{},
		&models.AIConfig{}, &models.ModelProfileTemplate{}, &models.ModelProfileSlot{},
		&models.StoreModelCredential{},
	); err != nil {
		t.Fatalf("AutoMigrate() error=%v", err)
	}
	return db
}
