package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/securex"
	"agent-desk/internal/pkg/usagex"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const storeAIModelSettingTestMasterKey = "0123456789abcdef0123456789abcdef"

func TestStoreAIModelSettingResolveUsesEachStoresCurrentCredential(t *testing.T) {
	db := setupStoreAIModelSettingTestDB(t)
	createStoreAIModelTestStore(t, db, 7, 5, "南七店")
	createStoreAIModelTestStore(t, db, 8, 5, "高铁南站店")
	createStoreAIModelTestCredential(t, db, 7, 5, "sk-store-seven", 3)
	createStoreAIModelTestCredential(t, db, 8, 5, "sk-store-eight", 6)
	if err := db.Create(&models.ConversationRouteState{ConversationID: 41, StoreID: 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ConversationRouteState{ConversationID: 42, StoreID: 8}).Error; err != nil {
		t.Fatal(err)
	}

	first, err := StoreAIModelSettingService.ResolveForConversation(41, StoreAIModelUsageReplyLLM, 999)
	if err != nil {
		t.Fatal(err)
	}
	second, err := StoreAIModelSettingService.ResolveForConversation(42, StoreAIModelUsageReplyLLM, 999)
	if err != nil {
		t.Fatal(err)
	}
	if first.Config.APIKey != "sk-store-seven" || first.CredentialRevision != 3 {
		t.Fatalf("unexpected first store credential: %#v", first)
	}
	if second.Config.APIKey != "sk-store-eight" || second.CredentialRevision != 6 {
		t.Fatalf("unexpected second store credential: %#v", second)
	}
	if first.Config.APIKey == second.Config.APIKey {
		t.Fatal("different stores must never share the same resolved credential")
	}
	if first.Source != StoreAIModelSourceStoreCredential || second.Source != StoreAIModelSourceStoreCredential {
		t.Fatalf("runtime source must be store credential: first=%q second=%q", first.Source, second.Source)
	}
}

func TestStoreAIModelSettingUsesUsageSpecificTemplateSlotWithSameStoreKey(t *testing.T) {
	db := setupStoreAIModelSettingTestDB(t)
	createStoreAIModelTestStore(t, db, 7, 5, "南七店")
	createStoreAIModelTestCredential(t, db, 7, 5, "sk-store-seven", 4)
	if err := db.Create(&models.ConversationRouteState{ConversationID: 41, StoreID: 7}).Error; err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		usageCode string
		modelName string
	}{
		{StoreAIModelUsageReplyLLM, "reply-model"},
		{StoreAIModelUsageIntentDetectLLM, "intent-model"},
		{StoreAIModelUsageMemorySummaryLLM, "summary-model"},
		{StoreAIModelUsageMediaUnderstanding, "vision-model"},
		{StoreAIModelUsageASR, "asr-model"},
		{StoreAIModelUsageEmbedding, "embedding-model"},
		{StoreAIModelUsageRerank, "rerank-model"},
		{StoreAIModelUsageDocumentParser, "parser-model"},
	}
	for _, item := range tests {
		resolved, err := StoreAIModelSettingService.ResolveForConversation(41, item.usageCode, 0)
		if err != nil {
			t.Fatalf("resolve usage %q: %v", item.usageCode, err)
		}
		if resolved.Config.ModelName != item.modelName {
			t.Fatalf("usage %q resolved model %q, want %q", item.usageCode, resolved.Config.ModelName, item.modelName)
		}
		if resolved.Config.APIKey != "sk-store-seven" || resolved.CredentialRevision != 4 {
			t.Fatalf("usage %q must reuse the store credential: %#v", item.usageCode, resolved)
		}
	}
}

func TestStoreAIModelSettingFailsClosedWithoutConversationStore(t *testing.T) {
	db := setupStoreAIModelSettingTestDB(t)
	if err := db.Create(&models.AIConfig{
		Name: "legacy global", Provider: enums.AIProviderOpenAI,
		BaseURL: "https://legacy.example.com/v1", APIKey: "sk-global",
		ModelType: enums.AIModelTypeLLM, ModelName: "legacy-global", Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}

	resolved, err := StoreAIModelSettingService.ResolveForConversation(404, StoreAIModelUsageReplyLLM, 1)
	if err == nil || resolved != nil {
		t.Fatalf("missing conversation store must fail closed, resolved=%#v err=%v", resolved, err)
	}
	if !strings.Contains(err.Error(), "绑定门店") {
		t.Fatalf("unexpected fail-closed error: %v", err)
	}
}

func TestStoreAIModelSettingFailsClosedWhenStoreCredentialMissing(t *testing.T) {
	db := setupStoreAIModelSettingTestDB(t)
	createStoreAIModelTestStore(t, db, 7, 5, "未配置密钥门店")
	if err := db.Create(&models.ConversationRouteState{ConversationID: 41, StoreID: 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreAIModelSetting{
		CompanyID: 5, StoreID: 7, UsageCode: StoreAIModelUsageReplyLLM,
		Provider: enums.AIProviderOpenAI, BaseURL: "https://legacy.example.com/v1",
		APIKey: "sk-legacy-store", ModelType: enums.AIModelTypeLLM,
		ModelName: "legacy-store", Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}

	resolved, err := StoreAIModelSettingService.ResolveForConversation(41, StoreAIModelUsageReplyLLM, 0)
	if err == nil || resolved != nil {
		t.Fatalf("missing current store credential must not use legacy key, resolved=%#v err=%v", resolved, err)
	}
}

func TestStoreAIModelSettingResolvesKnowledgeBaseStoreScope(t *testing.T) {
	db := setupStoreAIModelSettingTestDB(t)
	createStoreAIModelTestStore(t, db, 7, 5, "南七店")
	createStoreAIModelTestCredential(t, db, 7, 5, "sk-store-seven", 9)
	if err := db.Create(&models.KnowledgeBase{
		ID: 71, StoreID: 7, Name: "南七知识库", Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}

	ctx := usagex.WithScope(context.Background(), usagex.Scope{KnowledgeBaseID: 71})
	resolved, err := StoreAIModelSettingService.ResolveForContext(ctx, StoreAIModelUsageEmbedding)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.APIKey != "sk-store-seven" || resolved.Config.ModelName != "embedding-model" || resolved.CredentialRevision != 9 {
		t.Fatalf("knowledge base model scope resolved incorrectly: %#v", resolved)
	}
}

func TestLegacyPerPurposeCredentialMutationIsFrozen(t *testing.T) {
	setupStoreAIModelSettingTestDB(t)
	operator := &dto.AuthPrincipal{UserID: 1, Username: "root"}
	if _, err := StoreAIModelSettingService.TestStoreSetting(request.TestStoreAIModelSettingRequest{}, operator); err == nil {
		t.Fatal("legacy per-purpose model test must remain disabled")
	}
	if err := StoreAIModelSettingService.UpdateStoreSettings(request.UpdateStoreAIModelSettingsRequest{}, operator); err == nil {
		t.Fatal("legacy per-purpose model update must remain disabled")
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
	if err := db.AutoMigrate(
		&models.AIConfig{},
		&models.Store{},
		&models.WxWorkProtocolInstance{},
		&models.StoreAIModelSetting{},
		&models.StoreModelCredential{},
		&models.ModelProfileTemplate{},
		&models.ModelProfileSlot{},
		&models.ConversationRouteState{},
		&models.KnowledgeBase{},
	); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	masterKey := base64.StdEncoding.EncodeToString([]byte(storeAIModelSettingTestMasterKey))
	config.SetCurrent(&config.Config{StoreCredential: config.StoreCredentialConfig{MasterKey: masterKey}})
	if err := db.Create(&models.ModelProfileTemplate{
		ID: 1, Name: "测试模板", Revision: 11,
		GatewayBaseURL: "https://gateway.example.com/v1", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	slots := []models.ModelProfileSlot{
		storeAIModelTestSlot(ModelProfileUsageReplyLLM, "reply-model", enums.AIModelTypeLLM, 1),
		storeAIModelTestSlot(ModelProfileUsageIntentDetectLLM, "intent-model", enums.AIModelTypeLLM, 2),
		storeAIModelTestSlot(ModelProfileUsageMemorySummary, "summary-model", enums.AIModelTypeLLM, 3),
		storeAIModelTestSlot(ModelProfileUsageCustomerTag, "tag-model", enums.AIModelTypeLLM, 4),
		storeAIModelTestSlot(ModelProfileUsageVision, "vision-model", enums.AIModelTypeVision, 5),
		storeAIModelTestSlot(ModelProfileUsageASR, "asr-model", enums.AIModelTypeASR, 6),
		storeAIModelTestSlot(ModelProfileUsageEmbedding, "embedding-model", enums.AIModelTypeEmbedding, 7),
		storeAIModelTestSlot(ModelProfileUsageRerank, "rerank-model", enums.AIModelTypeRerank, 8),
		storeAIModelTestSlot(ModelProfileUsageDocumentParser, "parser-model", enums.AIModelTypeLLM, 9),
	}
	if err := db.Create(&slots).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		config.SetCurrent(&config.Config{})
		sqls.SetDB(nil)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func storeAIModelTestSlot(usageCode string, modelName string, modelType enums.AIModelType, sortNo int) models.ModelProfileSlot {
	return models.ModelProfileSlot{
		TemplateID: 1, UsageCode: usageCode, DisplayName: usageCode,
		ModelType: modelType, Provider: string(enums.AIProviderOpenAI),
		ModelName: modelName, APIMode: AIConfigAPIModeChatCompletions,
		TimeoutMS: 30000, Enabled: true, SortNo: sortNo,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
}

func createStoreAIModelTestStore(t *testing.T, db *gorm.DB, storeID int64, companyID int64, name string) {
	t.Helper()
	if err := db.Create(&models.Store{
		ID: storeID, CompanyID: companyID, StoreCode: fmt.Sprintf("store-%d", storeID), Name: name, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func createStoreAIModelTestCredential(t *testing.T, db *gorm.DB, storeID int64, companyID int64, apiKey string, revision int64) {
	t.Helper()
	masterKey := base64.StdEncoding.EncodeToString([]byte(storeAIModelSettingTestMasterKey))
	cipher, err := securex.NewAESGCM(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	encryptedKey, nonce, err := cipher.Encrypt(apiKey, credentialAAD(storeID, revision))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreModelCredential{
		CompanyID: companyID, StoreID: storeID,
		EncryptedKey: encryptedKey, KeyNonce: nonce,
		KeyFingerprint:     securex.Fingerprint(apiKey),
		CredentialRevision: revision, Status: storeModelCredentialStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
}
