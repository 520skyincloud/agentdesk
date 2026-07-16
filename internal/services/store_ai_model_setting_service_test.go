package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestTenantModelResolvePrefersEmployeeThenTenantDefault(t *testing.T) {
	db := setupTenantModelAccessTestDB(t)
	fixture := createTenantModelFixture(t, db)
	createTenantModelGrant(t, db, fixture.tenant.ID, fixture.defaultConfig.ID)
	createTenantModelGrant(t, db, fixture.tenant.ID, fixture.employeeConfig.ID)
	createTenantModelAssignment(t, db, fixture.tenant.ID, 0, constants.AIModelUsageReplyLLM, fixture.defaultConfig.ID)
	createTenantModelAssignment(t, db, fixture.tenant.ID, fixture.instance.ID, constants.AIModelUsageReplyLLM, fixture.employeeConfig.ID)

	resolved, err := StoreAIModelSettingService.ResolveForConversation(fixture.conversation.ID, constants.AIModelUsageReplyLLM)
	if err != nil {
		t.Fatalf("ResolveForConversation() error = %v", err)
	}
	if resolved.Config.ID != fixture.employeeConfig.ID || resolved.Source != constants.AIModelSourceEmployeeOverride {
		t.Fatalf("expected employee override, got %#v", resolved)
	}

	if err := db.Model(&models.StoreAIModelSetting{}).
		Where("tenant_id = ? AND wx_work_instance_id = ?", fixture.tenant.ID, fixture.instance.ID).
		Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable employee assignment: %v", err)
	}
	resolved, err = StoreAIModelSettingService.ResolveForConversation(fixture.conversation.ID, constants.AIModelUsageReplyLLM)
	if err != nil {
		t.Fatalf("ResolveForConversation() tenant default error = %v", err)
	}
	if resolved.Config.ID != fixture.defaultConfig.ID || resolved.Source != constants.AIModelSourceTenantDefault {
		t.Fatalf("expected tenant default, got %#v", resolved)
	}
}

func TestTenantModelResolveUsesOnlyAuthorizedFallback(t *testing.T) {
	db := setupTenantModelAccessTestDB(t)
	fixture := createTenantModelFixture(t, db)
	unauthorized := createAIConfigForTenantModelTest(t, db, "unauthorized", "unauthorized-model", 100)
	_ = unauthorized
	createTenantModelGrant(t, db, fixture.tenant.ID, fixture.defaultConfig.ID)

	resolved, err := StoreAIModelSettingService.ResolveForTenant(fixture.tenant.ID, 0, constants.AIModelUsageReplyLLM)
	if err != nil {
		t.Fatalf("ResolveForTenant() error = %v", err)
	}
	if resolved.Config.ID != fixture.defaultConfig.ID || resolved.Source != constants.AIModelSourceTenantFallback {
		t.Fatalf("expected authorized fallback, got %#v", resolved)
	}
}

func TestTenantModelAccessSupportsMultipleGrantsAndBlocksUsedRevocation(t *testing.T) {
	db := setupTenantModelAccessTestDB(t)
	fixture := createTenantModelFixture(t, db)
	operator := &dto.AuthPrincipal{UserID: 1, Username: "platform-admin", IsPlatformAccount: true}

	err := StoreAIModelSettingService.UpdateTenantAccess(request.UpdateTenantAIModelAccessRequest{
		TenantID:           fixture.tenant.ID,
		GrantedAIConfigIDs: []int64{fixture.defaultConfig.ID, fixture.employeeConfig.ID},
		Defaults:           []request.TenantAIModelDefaultRequest{{UsageCode: constants.AIModelUsageReplyLLM, AIConfigID: fixture.defaultConfig.ID}},
	}, operator)
	if err != nil {
		t.Fatalf("UpdateTenantAccess() error = %v", err)
	}
	employeeSetting := &models.StoreAIModelSetting{
		TenantID: fixture.tenant.ID, StoreID: 999, WxWorkInstanceID: fixture.instance.ID,
		UsageCode: constants.AIModelUsageReplyLLM, AIConfigID: fixture.defaultConfig.ID,
		Status: enums.StatusDisabled,
	}
	if err := db.Create(employeeSetting).Error; err != nil {
		t.Fatalf("create employee setting: %v", err)
	}
	if err := StoreAIModelSettingService.UpdateEmployeeAssignments(request.UpdateTenantAIModelAssignmentsRequest{
		TenantID: fixture.tenant.ID, WxWorkInstanceID: fixture.instance.ID,
		Assignments: []request.TenantAIModelDefaultRequest{{UsageCode: constants.AIModelUsageReplyLLM, AIConfigID: fixture.employeeConfig.ID}},
	}, operator); err != nil {
		t.Fatalf("UpdateEmployeeAssignments() error = %v", err)
	}

	err = StoreAIModelSettingService.UpdateTenantAccess(request.UpdateTenantAIModelAccessRequest{
		TenantID:           fixture.tenant.ID,
		GrantedAIConfigIDs: []int64{fixture.defaultConfig.ID},
		Defaults:           []request.TenantAIModelDefaultRequest{{UsageCode: constants.AIModelUsageReplyLLM, AIConfigID: fixture.defaultConfig.ID}},
	}, operator)
	if err == nil {
		t.Fatal("expected revocation of an employee-assigned model to fail")
	}

	var setting models.StoreAIModelSetting
	if err := db.Where("tenant_id = ? AND wx_work_instance_id = ? AND usage_code = ?", fixture.tenant.ID, fixture.instance.ID, constants.AIModelUsageReplyLLM).Take(&setting).Error; err != nil {
		t.Fatalf("load employee assignment: %v", err)
	}
	if setting.AIConfigID != fixture.employeeConfig.ID || setting.StoreID != 0 || setting.APIKey != "" || setting.BaseURL != "" || setting.Provider != "" || setting.ModelName != "" || setting.MaxContextTokens != 0 || setting.Remark != "" {
		t.Fatalf("assignment must only reference platform AIConfig, got %#v", setting)
	}
	if setting.APIMode != "chat_completions" || setting.TimeoutMS != 30000 {
		t.Fatalf("legacy model parameters were not reset: %#v", setting)
	}
}

func TestTenantModelResolveUsesAuthorizedSpeechRecognitionModel(t *testing.T) {
	db := setupTenantModelAccessTestDB(t)
	fixture := createTenantModelFixture(t, db)
	asr := &models.AIConfig{
		Name: "asr", Provider: enums.AIProviderOpenAI, BaseURL: "https://example.com/v1", APIKey: "sk-asr",
		ModelType: enums.AIModelTypeASR, ModelName: "asr-model", Status: enums.StatusOk,
	}
	if err := db.Create(asr).Error; err != nil {
		t.Fatalf("create ASR config: %v", err)
	}
	createTenantModelGrant(t, db, fixture.tenant.ID, asr.ID)
	createTenantModelAssignment(t, db, fixture.tenant.ID, fixture.instance.ID, constants.AIModelUsageSpeechRecognition, asr.ID)
	message := &models.Message{ConversationID: fixture.conversation.ID}
	resolved, err := StoreAIModelSettingService.ResolveForMessage(message, constants.AIModelUsageSpeechRecognition)
	if err != nil {
		t.Fatalf("ResolveForMessage() error = %v", err)
	}
	if resolved.Config.ID != asr.ID || resolved.Source != constants.AIModelSourceEmployeeOverride {
		t.Fatalf("expected employee ASR override, got %#v", resolved)
	}
}

func TestTenantModelUpdatesRequirePlatformAccountAndAuthorizedType(t *testing.T) {
	db := setupTenantModelAccessTestDB(t)
	fixture := createTenantModelFixture(t, db)
	tenantOperator := &dto.AuthPrincipal{UserID: 2, Username: "tenant-admin", ActiveTenantID: fixture.tenant.ID}
	if err := StoreAIModelSettingService.UpdateTenantAccess(request.UpdateTenantAIModelAccessRequest{
		TenantID: fixture.tenant.ID, GrantedAIConfigIDs: []int64{fixture.defaultConfig.ID},
	}, tenantOperator); err == nil {
		t.Fatal("expected tenant account to be rejected")
	}

	vision := &models.AIConfig{Name: "vision", Provider: enums.AIProviderOpenAI, BaseURL: "https://example.com/v1", APIKey: "sk-vision", ModelType: enums.AIModelTypeVision, ModelName: "vision-model", Status: enums.StatusOk}
	if err := db.Create(vision).Error; err != nil {
		t.Fatalf("create vision config: %v", err)
	}
	platform := &dto.AuthPrincipal{UserID: 1, Username: "platform", IsPlatformAccount: true}
	err := StoreAIModelSettingService.UpdateTenantAccess(request.UpdateTenantAIModelAccessRequest{
		TenantID:           fixture.tenant.ID,
		GrantedAIConfigIDs: []int64{vision.ID},
		Defaults:           []request.TenantAIModelDefaultRequest{{UsageCode: constants.AIModelUsageReplyLLM, AIConfigID: vision.ID}},
	}, platform)
	if err == nil {
		t.Fatal("expected model type mismatch to fail")
	}
}

type tenantModelFixture struct {
	tenant         models.Tenant
	instance       models.WxWorkProtocolInstance
	conversation   models.Conversation
	defaultConfig  models.AIConfig
	employeeConfig models.AIConfig
}

func createTenantModelFixture(t *testing.T, db *gorm.DB) tenantModelFixture {
	t.Helper()
	fixture := tenantModelFixture{
		tenant: models.Tenant{TenantCode: "tenant-" + t.Name(), LegalName: "Tenant", ShortName: "Tenant", RegistrationType: "credit_code", RegistrationNo: "REG-" + t.Name(), Status: enums.StatusOk},
	}
	if err := db.Create(&fixture.tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	fixture.instance = models.WxWorkProtocolInstance{TenantID: fixture.tenant.ID, Guid: "guid-" + t.Name(), Status: enums.StatusOk}
	if err := db.Create(&fixture.instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	fixture.conversation = models.Conversation{TenantID: fixture.tenant.ID, Status: enums.IMConversationStatusAIServing, LastActiveAt: time.Now()}
	if err := db.Create(&fixture.conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	route := &models.ConversationRouteState{TenantID: fixture.tenant.ID, ConversationID: fixture.conversation.ID, WxWorkInstanceID: fixture.instance.ID}
	if err := db.Create(route).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	fixture.defaultConfig = *createAIConfigForTenantModelTest(t, db, "default", "default-model", 10)
	fixture.employeeConfig = *createAIConfigForTenantModelTest(t, db, "employee", "employee-model", 20)
	return fixture
}

func createAIConfigForTenantModelTest(t *testing.T, db *gorm.DB, name, model string, sortNo int) *models.AIConfig {
	t.Helper()
	item := &models.AIConfig{Name: name, Provider: enums.AIProviderOpenAI, BaseURL: "https://example.com/v1", APIKey: "sk-test", ModelType: enums.AIModelTypeLLM, ModelName: model, SortNo: sortNo, Status: enums.StatusOk}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create AI config: %v", err)
	}
	return item
}

func createTenantModelGrant(t *testing.T, db *gorm.DB, tenantID, configID int64) {
	t.Helper()
	if err := db.Create(&models.TenantAIModelGrant{TenantID: tenantID, AIConfigID: configID, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
}

func createTenantModelAssignment(t *testing.T, db *gorm.DB, tenantID, instanceID int64, usageCode string, configID int64) {
	t.Helper()
	if err := db.Create(&models.StoreAIModelSetting{TenantID: tenantID, WxWorkInstanceID: instanceID, UsageCode: usageCode, AIConfigID: configID, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create assignment: %v", err)
	}
}

func setupTenantModelAccessTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{}, &models.AIConfig{}, &models.TenantAIModelGrant{}, &models.StoreAIModelSetting{},
		&models.Store{}, &models.WxWorkProtocolInstance{}, &models.Conversation{}, &models.ConversationRouteState{}, &models.AIUsageEvent{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
