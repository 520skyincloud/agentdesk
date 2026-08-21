package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
)

func TestSyncLegacyFastGPTTemplateIgnoresAuxiliaryOnlyChanges(t *testing.T) {
	db := setupConversationEvolutionTestDB(t)
	now := time.Now()
	legacy := &models.FastGPTProfileTemplate{
		ID: 1, Name: "门店知识库模型模板", Revision: 5, Status: fastGPTProfileTemplateStatusActive,
		ChatProvider: "openai", ChatBaseURL: "https://gateway.example.com/v1",
		ChatModel: "reply-model", ChatAPIMode: "chat_completions",
		EmbeddingProvider: "openai", EmbeddingBaseURL: "https://gateway.example.com/v1",
		EmbeddingModel: "embedding-model",
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatal(err)
	}
	template := &models.ModelProfileTemplate{
		ID: 1, Name: legacy.Name, Revision: 9, GatewayBaseURL: legacy.ChatBaseURL,
		CustomerTagEvolutionEnabled: true, CustomerTagEvolutionStoreIDs: "[3]",
		ReplyTagContextEnabled: false, Status: "active",
	}
	slots := []models.ModelProfileSlot{
		{
			UsageCode: ModelProfileUsageReplyLLM, ModelType: enums.AIModelTypeLLM,
			Provider: legacy.ChatProvider, ModelName: legacy.ChatModel, APIMode: legacy.ChatAPIMode,
		},
		{
			UsageCode: ModelProfileUsageEmbedding, ModelType: enums.AIModelTypeEmbedding,
			Provider: legacy.EmbeddingProvider, ModelName: legacy.EmbeddingModel,
		},
		{
			UsageCode: ModelProfileUsageCustomerTag, ModelType: enums.AIModelTypeLLM,
			Provider: "openai", ModelName: "changed-tag-model", APIMode: "chat_completions",
		},
		{
			UsageCode: ModelProfileUsageKnowledgeJudge, ModelType: enums.AIModelTypeLLM,
			Provider: "openai", ModelName: "deepseek-v4-flash", APIMode: "chat_completions",
		},
	}
	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin"}

	changed, err := ModelProfileTemplateService.syncLegacyFastGPTTemplate(db, template, slots, operator)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("auxiliary-only template changes must not queue a FastGPT profile sync")
	}
	current := db.First(&models.FastGPTProfileTemplate{}, "id = ?", 1)
	if current.Error != nil {
		t.Fatal(current.Error)
	}
	var saved models.FastGPTProfileTemplate
	if err := db.First(&saved, "id = ?", 1).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 5 {
		t.Fatalf("auxiliary-only change modified FastGPT template revision: %d", saved.Revision)
	}

	slots[0].ModelName = "reply-model-v2"
	changed, err = ModelProfileTemplateService.syncLegacyFastGPTTemplate(db, template, slots, operator)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("reply model change must update the FastGPT profile template")
	}
	if err := db.First(&saved, "id = ?", 1).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 6 || saved.ChatModel != "reply-model-v2" {
		t.Fatalf("FastGPT template was not updated correctly: %#v", saved)
	}
}
