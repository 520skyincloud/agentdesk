package services

import (
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestStoreAIModelSettingResolveForWxWorkConversationPrefersAccountOverride(t *testing.T) {
	setupStoreAIModelSettingTestDB(t)
	legacy := models.AIConfig{
		ID:        9,
		Name:      "legacy agent",
		Provider:  enums.AIProviderOpenAI,
		BaseURL:   "https://api.example.com/v1",
		APIKey:    "sk-legacy",
		ModelType: enums.AIModelTypeLLM,
		ModelName: "legacy-model",
		Status:    enums.StatusOk,
	}
	if err := sqls.DB().Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy config: %v", err)
	}
	if err := sqls.DB().Create(&models.Store{ID: 7, CompanyID: 5, Name: "store", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := sqls.DB().Create(&models.ConversationRouteState{ConversationID: 42, StoreID: 7, WxWorkInstanceID: 3}).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}
	if err := sqls.DB().Create(&models.StoreAIModelSetting{
		CompanyID: 5,
		UsageCode: StoreAIModelUsageIntentDetectLLM,
		Provider:  enums.AIProviderOpenAI,
		BaseURL:   "https://company.example.com/v1",
		APIKey:    "sk-company",
		ModelType: enums.AIModelTypeLLM,
		ModelName: "company-intent-model",
		Status:    enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create company setting: %v", err)
	}
	if err := sqls.DB().Create(&models.StoreAIModelSetting{
		CompanyID:        5,
		StoreID:          7,
		WxWorkInstanceID: 3,
		UsageCode:        StoreAIModelUsageIntentDetectLLM,
		Provider:         enums.AIProviderOpenAI,
		BaseURL:          "https://account.example.com/v1",
		APIKey:           "sk-account",
		ModelType:        enums.AIModelTypeLLM,
		ModelName:        "account-intent-model",
		Status:           enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create account setting: %v", err)
	}

	resolved, err := StoreAIModelSettingService.ResolveForConversation(42, StoreAIModelUsageIntentDetectLLM, legacy.ID)
	if err != nil {
		t.Fatalf("ResolveForConversation() error = %v", err)
	}
	if resolved.Config.ModelName != "account-intent-model" || resolved.Source != StoreAIModelSourceAccountOverride {
		t.Fatalf("expected account override instead of company or legacy agent config, got %#v", resolved)
	}
}

func TestStoreAIModelSettingResolveUsesCompanyFallback(t *testing.T) {
	setupStoreAIModelSettingTestDB(t)
	if err := sqls.DB().Create(&models.Store{ID: 7, CompanyID: 5, Name: "store", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := sqls.DB().Create(&models.StoreAIModelSetting{
		CompanyID: 5,
		UsageCode: StoreAIModelUsageReplyLLM,
		Provider:  enums.AIProviderOpenAI,
		BaseURL:   "https://company.example.com/v1",
		APIKey:    "sk-company",
		ModelType: enums.AIModelTypeLLM,
		ModelName: "company-model",
		Status:    enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create company setting: %v", err)
	}

	resolved, err := StoreAIModelSettingService.Resolve(7, StoreAIModelUsageReplyLLM)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Config.ModelName != "company-model" || resolved.Source != StoreAIModelSourceCompanyOverride {
		t.Fatalf("expected company fallback, got %#v", resolved)
	}
}

func TestStoreAIModelSettingResolveUsesWxWorkInstanceCompanyBeforeStore(t *testing.T) {
	setupStoreAIModelSettingTestDB(t)
	if err := sqls.DB().Create(&models.Store{ID: 7, CompanyID: 5, Name: "store", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := sqls.DB().Create(&models.WxWorkProtocolInstance{ID: 3, Guid: "guid-3", StoreID: 7, CompanyID: 9, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create wxwork instance: %v", err)
	}
	if err := sqls.DB().Create(&models.ConversationRouteState{ConversationID: 42, StoreID: 7, WxWorkInstanceID: 3}).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}
	if err := sqls.DB().Create(&models.StoreAIModelSetting{
		CompanyID: 5,
		UsageCode: StoreAIModelUsageReplyLLM,
		Provider:  enums.AIProviderOpenAI,
		BaseURL:   "https://store-company.example.com/v1",
		APIKey:    "sk-store-company",
		ModelType: enums.AIModelTypeLLM,
		ModelName: "store-company-model",
		Status:    enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create store company setting: %v", err)
	}
	if err := sqls.DB().Create(&models.StoreAIModelSetting{
		CompanyID: 9,
		UsageCode: StoreAIModelUsageReplyLLM,
		Provider:  enums.AIProviderOpenAI,
		BaseURL:   "https://instance-company.example.com/v1",
		APIKey:    "sk-instance-company",
		ModelType: enums.AIModelTypeLLM,
		ModelName: "instance-company-model",
		Status:    enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create instance company setting: %v", err)
	}

	resolved, err := StoreAIModelSettingService.ResolveForConversation(42, StoreAIModelUsageReplyLLM, 0)
	if err != nil {
		t.Fatalf("ResolveForConversation() error = %v", err)
	}
	if resolved.Config.ModelName != "instance-company-model" || resolved.Source != StoreAIModelSourceCompanyOverride {
		t.Fatalf("expected wxwork instance company setting, got %#v", resolved)
	}
}

func TestStoreAIModelSettingResolveFallsBackToGlobalDefault(t *testing.T) {
	setupStoreAIModelSettingTestDB(t)
	global := models.AIConfig{
		ID:        1,
		Name:      "global reply",
		Provider:  enums.AIProviderOpenAI,
		BaseURL:   "https://api.example.com/v1",
		APIKey:    "sk-global",
		ModelType: enums.AIModelTypeLLM,
		ModelName: "global-model",
		Status:    enums.StatusOk,
	}
	if err := sqls.DB().Create(&global).Error; err != nil {
		t.Fatalf("create global config: %v", err)
	}

	resolved, err := StoreAIModelSettingService.Resolve(0, StoreAIModelUsageReplyLLM)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Config.ModelName != "global-model" || resolved.Source != StoreAIModelSourceGlobalDefault {
		t.Fatalf("expected global default, got %#v", resolved)
	}
}

func TestStoreAIModelSettingUpdateKeepsExistingAPIKeyWhenBlank(t *testing.T) {
	setupStoreAIModelSettingTestDB(t)
	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin"}
	if err := sqls.DB().Create(&models.Store{ID: 7, CompanyID: 5, Name: "store", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := sqls.DB().Create(&models.StoreAIModelSetting{
		CompanyID:        5,
		StoreID:          7,
		WxWorkInstanceID: 3,
		UsageCode:        StoreAIModelUsageReplyLLM,
		Provider:         enums.AIProviderOpenAI,
		BaseURL:          "https://old.example.com/v1",
		APIKey:           "sk-old",
		ModelType:        enums.AIModelTypeLLM,
		ModelName:        "old-model",
		Status:           enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create setting: %v", err)
	}

	err := StoreAIModelSettingService.UpdateStoreSettings(request.UpdateStoreAIModelSettingsRequest{
		StoreID:          7,
		WxWorkInstanceID: 3,
		Settings: []request.StoreAIModelSettingUpdateRequest{{
			UsageCode: StoreAIModelUsageReplyLLM,
			Enabled:   true,
			Provider:  enums.AIProviderOpenAI,
			BaseURL:   "https://new.example.com/v1",
			ModelType: enums.AIModelTypeLLM,
			ModelName: "new-model",
		}},
	}, operator)
	if err != nil {
		t.Fatalf("UpdateStoreSettings() error = %v", err)
	}
	setting := repositories.StoreAIModelSettingRepository.Take(sqls.DB(), "company_id = ? AND wx_work_instance_id = ? AND usage_code = ?", 5, 3, StoreAIModelUsageReplyLLM)
	if setting == nil {
		t.Fatalf("expected setting")
	}
	if setting.APIKey != "sk-old" || setting.BaseURL != "https://new.example.com/v1" || setting.ModelName != "new-model" {
		t.Fatalf("unexpected setting after update: %#v", setting)
	}
}

func setupStoreAIModelSettingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite error = %v", err)
	}
	if err := db.AutoMigrate(&models.AIConfig{}, &models.Store{}, &models.WxWorkProtocolInstance{}, &models.StoreAIModelSetting{}, &models.ConversationRouteState{}); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
