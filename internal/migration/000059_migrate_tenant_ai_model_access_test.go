package migration

import (
	"path/filepath"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestMigrateTenantAIModelAccessRetiresLegacyModelAccess(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tenant-model-migration.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{}, &models.Company{}, &models.Store{}, &models.WxWorkProtocolInstance{},
		&legacyAIConfig{}, &models.AgentTeam{}, &models.AIAgent{},
		&legacyTenantAIModelGrant{}, &legacyStoreAIModelSetting{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := db.Exec("ALTER TABLE t_ai_agent ADD COLUMN ai_config_id bigint NOT NULL DEFAULT 0").Error; err != nil {
		t.Fatalf("add legacy agent binding: %v", err)
	}

	tenant := &models.Tenant{TenantCode: "legacy-model-tenant", LegalName: "Legacy Tenant", ShortName: "Legacy", RegistrationType: "credit_code", RegistrationNo: "LEGACY-MODEL-REG", Status: enums.StatusOk}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	company := &models.Company{TenantID: tenant.ID, Name: "Legacy Company", Code: "legacy-company", Status: enums.StatusOk}
	if err := db.Create(company).Error; err != nil {
		t.Fatalf("create company: %v", err)
	}
	config := &legacyAIConfig{Name: "Platform LLM", Provider: enums.AIProviderOpenAI, BaseURL: "https://platform.example.com/v1", APIKey: "platform-secret", ModelType: enums.AIModelTypeLLM, ModelName: "platform-model", Status: enums.StatusOk}
	if err := db.Create(config).Error; err != nil {
		t.Fatalf("create config: %v", err)
	}
	legacy := &legacyStoreAIModelSetting{
		CompanyID: company.ID, UsageCode: constants.AIModelUsageReplyLLM, AIConfigID: config.ID,
		Provider: config.Provider, BaseURL: config.BaseURL, APIKey: "copied-tenant-secret",
		ModelType: config.ModelType, ModelName: config.ModelName, Status: enums.StatusOk,
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatalf("create legacy setting: %v", err)
	}
	orphan := &legacyStoreAIModelSetting{
		UsageCode: constants.AIModelUsageReplyLLM, Provider: enums.AIProviderOpenAI,
		BaseURL: "https://orphan.example.com/v1", APIKey: "orphan-secret", APIMode: "responses",
		ModelType: enums.AIModelTypeLLM, ModelName: "orphan-model", MaxContextTokens: 2048,
		TimeoutMS: 10000, Remark: "orphan credentials", Status: enums.StatusOk,
	}
	if err := db.Create(orphan).Error; err != nil {
		t.Fatalf("create orphan legacy setting: %v", err)
	}
	unused := &legacyStoreAIModelSetting{
		CompanyID: company.ID, UsageCode: "memory_summary_llm", AIConfigID: config.ID, Status: enums.StatusOk,
	}
	if err := db.Create(unused).Error; err != nil {
		t.Fatalf("create unsupported legacy setting: %v", err)
	}
	agent := &models.AIAgent{TenantID: tenant.ID, Name: "Legacy Agent", Status: enums.StatusOk}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := db.Model(&legacyAIAgentModelBinding{}).Where("id = ?", agent.ID).Update("ai_config_id", config.ID).Error; err != nil {
		t.Fatalf("bind legacy agent config: %v", err)
	}

	if err := migrateTenantAIModelAccess(db); err != nil {
		t.Fatalf("migrate tenant model access: %v", err)
	}
	if err := migrateTenantAIModelAccess(db); err != nil {
		t.Fatalf("repeat tenant model access migration: %v", err)
	}

	var retiredConfig legacyAIConfig
	if err := db.First(&retiredConfig, config.ID).Error; err != nil {
		t.Fatalf("load retired config: %v", err)
	}
	if retiredConfig.APIKey != "" {
		t.Fatalf("legacy AI config key was not cleared")
	}
	if retiredConfig.ModelName != config.ModelName || retiredConfig.Status != enums.StatusOk {
		t.Fatalf("non-secret legacy model metadata changed before profile seeding: %#v", retiredConfig)
	}

	var migrated legacyStoreAIModelSetting
	if err := db.First(&migrated, legacy.ID).Error; err != nil {
		t.Fatalf("load migrated setting: %v", err)
	}
	if migrated.Status != enums.StatusDisabled || migrated.APIKey != "" || migrated.BaseURL != "" || migrated.Provider != "" || migrated.ModelName != "" || migrated.ConfigFingerprint != "" || migrated.Remark != "" {
		t.Fatalf("legacy model setting remained callable: %#v", migrated)
	}
	var migratedOrphan legacyStoreAIModelSetting
	if err := db.First(&migratedOrphan, orphan.ID).Error; err != nil {
		t.Fatalf("load orphan setting: %v", err)
	}
	if migratedOrphan.Status != enums.StatusDisabled || migratedOrphan.APIKey != "" || migratedOrphan.BaseURL != "" || migratedOrphan.Provider != "" || migratedOrphan.ModelName != "" || migratedOrphan.ConfigFingerprint != "" || migratedOrphan.Remark != "" {
		t.Fatalf("orphan legacy credentials were not cleared: %#v", migratedOrphan)
	}
	var migratedUnused legacyStoreAIModelSetting
	if err := db.First(&migratedUnused, unused.ID).Error; err != nil {
		t.Fatalf("load unsupported legacy setting: %v", err)
	}
	if migratedUnused.Status != enums.StatusDisabled {
		t.Fatalf("unsupported legacy usage remained active: %#v", migratedUnused)
	}

	var grant legacyTenantAIModelGrant
	if err := db.Where("tenant_id = ? AND ai_config_id = ?", tenant.ID, config.ID).Take(&grant).Error; err == nil || err != gorm.ErrRecordNotFound {
		t.Fatalf("legacy migration must not create a model grant: %v", err)
	}
	var binding legacyAIAgentModelBinding
	if err := db.First(&binding, agent.ID).Error; err != nil {
		t.Fatalf("load legacy agent binding: %v", err)
	}
	if binding.AIConfigID != 0 {
		t.Fatalf("legacy agent config reference=%d want=0", binding.AIConfigID)
	}
}

func TestRemoveRetiredAIAgentManagementPermissions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "retired-agent-permissions.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Permission{}, &models.RolePermission{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	retired := &models.Permission{Name: "retired", Code: "aiAgent.update", Type: "api"}
	kept := &models.Permission{Name: "kept", Code: constants.PermissionAIAgentView.Code, Type: "api"}
	if err := db.Create(retired).Error; err != nil {
		t.Fatalf("create retired permission: %v", err)
	}
	if err := db.Create(kept).Error; err != nil {
		t.Fatalf("create kept permission: %v", err)
	}
	if err := db.Create(&models.RolePermission{RoleID: 1, PermissionID: retired.ID}).Error; err != nil {
		t.Fatalf("create role permission: %v", err)
	}

	if err := removeRetiredAIAgentManagementPermissions(db); err != nil {
		t.Fatalf("remove retired permissions: %v", err)
	}
	if err := removeRetiredAIAgentManagementPermissions(db); err != nil {
		t.Fatalf("repeat removal: %v", err)
	}
	var retiredCount, keptCount, relationCount int64
	db.Model(&models.Permission{}).Where("code = ?", retired.Code).Count(&retiredCount)
	db.Model(&models.Permission{}).Where("code = ?", kept.Code).Count(&keptCount)
	db.Model(&models.RolePermission{}).Where("permission_id = ?", retired.ID).Count(&relationCount)
	if retiredCount != 0 || relationCount != 0 || keptCount != 1 {
		t.Fatalf("unexpected cleanup counts: retired=%d relation=%d kept=%d", retiredCount, relationCount, keptCount)
	}
}
