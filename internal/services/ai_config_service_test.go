package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestUpdateAIConfigKeepsAPIKeyWhenRequestAPIKeyBlank(t *testing.T) {
	db := setupAIConfigServiceTestDB(t)
	item := &models.AIConfig{
		Name:        "old",
		Provider:    enums.AIProviderOpenAI,
		BaseURL:     "https://old.example.com",
		APIKey:      "sk-existing",
		ModelType:   enums.AIModelTypeLLM,
		ModelName:   "old-model",
		TimeoutMS:   30000,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create ai config error = %v", err)
	}

	err := AIConfigService.UpdateAIConfig(request.UpdateAIConfigRequest{
		ID: item.ID,
		CreateAIConfigRequest: request.CreateAIConfigRequest{
			Name:      "new",
			Provider:  enums.AIProviderOpenAI,
			BaseURL:   "https://new.example.com",
			APIKey:    "   ",
			ModelType: enums.AIModelTypeLLM,
			ModelName: "new-model",
			TimeoutMS: 120000,
		},
	}, &dto.AuthPrincipal{UserID: 1, Username: "admin"})
	if err != nil {
		t.Fatalf("UpdateAIConfig() error = %v", err)
	}

	var updated models.AIConfig
	if err := db.First(&updated, item.ID).Error; err != nil {
		t.Fatalf("get updated ai config error = %v", err)
	}
	if updated.APIKey != "sk-existing" {
		t.Fatalf("expected api key to be preserved, got %q", updated.APIKey)
	}
}

func TestAIConfigAPIModeDefaultsAndPersists(t *testing.T) {
	setupAIConfigServiceTestDB(t)
	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin"}
	item, err := AIConfigService.CreateAIConfig(request.CreateAIConfigRequest{
		Name:      "doubao",
		Provider:  enums.AIProviderOpenAI,
		BaseURL:   "https://ark.cn-beijing.volces.com/api/v3",
		APIKey:    "sk-test",
		ModelType: enums.AIModelTypeLLM,
		ModelName: "doubao-seed",
		TimeoutMS: 120000,
	}, operator)
	if err != nil {
		t.Fatalf("CreateAIConfig() error = %v", err)
	}
	if item.APIMode != AIConfigAPIModeChatCompletions {
		t.Fatalf("expected default api mode %q, got %q", AIConfigAPIModeChatCompletions, item.APIMode)
	}

	err = AIConfigService.UpdateAIConfig(request.UpdateAIConfigRequest{
		ID: item.ID,
		CreateAIConfigRequest: request.CreateAIConfigRequest{
			Name:      "doubao responses",
			Provider:  enums.AIProviderOpenAI,
			BaseURL:   "https://ark.cn-beijing.volces.com/api/v3",
			APIMode:   AIConfigAPIModeResponses,
			ModelType: enums.AIModelTypeLLM,
			ModelName: "doubao-seed",
			TimeoutMS: 120000,
		},
	}, operator)
	if err != nil {
		t.Fatalf("UpdateAIConfig() error = %v", err)
	}
	updated := AIConfigService.Get(item.ID)
	if updated == nil {
		t.Fatal("expected updated config")
	}
	if updated.APIMode != AIConfigAPIModeResponses {
		t.Fatalf("expected api mode %q, got %q", AIConfigAPIModeResponses, updated.APIMode)
	}
}

func TestAIConfigIntentDetectEnabledPersistsOnlyForLLM(t *testing.T) {
	setupAIConfigServiceTestDB(t)
	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin"}
	item, err := AIConfigService.CreateAIConfig(request.CreateAIConfigRequest{
		Name:                "intent detector",
		Provider:            enums.AIProviderOpenAI,
		BaseURL:             "https://api.example.com/v1",
		APIKey:              "sk-test",
		ModelType:           enums.AIModelTypeLLM,
		ModelName:           "fast-intent-model",
		IntentDetectEnabled: true,
	}, operator)
	if err != nil {
		t.Fatalf("CreateAIConfig() error = %v", err)
	}
	if !item.IntentDetectEnabled {
		t.Fatal("expected LLM intent detect flag to persist")
	}

	embedding, err := AIConfigService.CreateAIConfig(request.CreateAIConfigRequest{
		Name:                "embedding",
		Provider:            enums.AIProviderOpenAI,
		BaseURL:             "https://api.example.com/v1",
		APIKey:              "sk-test",
		ModelType:           enums.AIModelTypeEmbedding,
		ModelName:           "embedding-model",
		IntentDetectEnabled: true,
	}, operator)
	if err != nil {
		t.Fatalf("CreateAIConfig() embedding error = %v", err)
	}
	if embedding.IntentDetectEnabled {
		t.Fatal("expected non-LLM intent detect flag to be disabled")
	}
}

func setupAIConfigServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open sqlite error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&models.AIConfig{}); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	return db
}
