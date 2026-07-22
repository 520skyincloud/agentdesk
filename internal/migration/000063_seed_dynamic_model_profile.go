package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(63, "seed dynamic model profile slots from current FastGPT template", func() error {
		return seedDynamicModelProfile(sqls.DB())
	})
}

func seedDynamicModelProfile(db *gorm.DB) error {
	if db == nil || repositories.ModelProfileTemplateRepository.Get(db) != nil {
		return nil
	}
	legacy := repositories.FastGPTProfileTemplateRepository.Get(db)
	if legacy == nil {
		return nil
	}
	now := time.Now()
	template := &models.ModelProfileTemplate{
		ID:             1,
		Name:           legacy.Name,
		Revision:       legacy.Revision,
		GatewayBaseURL: legacy.ChatBaseURL,
		Status:         "active",
		AuditFields: models.AuditFields{
			CreatedAt: legacy.CreatedAt, UpdatedAt: now,
			CreateUserID: legacy.CreateUserID, CreateUserName: legacy.CreateUserName,
			UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
		},
	}
	if template.Revision <= 0 {
		template.Revision = 1
	}
	if template.CreatedAt.IsZero() {
		template.CreatedAt = now
	}
	slots := []models.ModelProfileSlot{
		newSeedModelProfileSlot(1, "reply_llm", "回复生成模型", enums.AIModelTypeLLM, legacy.ChatProvider, legacy.ChatModel, legacy.ChatAPIMode, 1, now),
		newSeedModelProfileSlot(1, "intent_detect_llm", "意图识别模型", enums.AIModelTypeLLM, legacy.ChatProvider, legacy.ChatModel, legacy.ChatAPIMode, 2, now),
		newSeedModelProfileSlot(1, "memory_summary_llm", "会话摘要模型", enums.AIModelTypeLLM, legacy.ChatProvider, legacy.ChatModel, legacy.ChatAPIMode, 3, now),
		newSeedModelProfileSlot(1, "customer_tag_llm", "客户标签模型", enums.AIModelTypeLLM, legacy.ChatProvider, legacy.ChatModel, legacy.ChatAPIMode, 4, now),
		newSeedModelProfileSlot(1, "vision", "视觉理解模型", enums.AIModelTypeVision, legacy.VisionProvider, legacy.VisionModel, "chat_completions", 5, now),
		newSeedModelProfileSlot(1, "asr", "语音识别模型", enums.AIModelTypeASR, legacy.ASRProvider, legacy.ASRModel, "", 6, now),
		newSeedModelProfileSlot(1, "embedding", "向量模型", enums.AIModelTypeEmbedding, legacy.EmbeddingProvider, legacy.EmbeddingModel, "", 7, now),
		newSeedModelProfileSlot(1, "rerank", "重排模型", enums.AIModelTypeRerank, legacy.RerankProvider, legacy.RerankModel, "", 8, now),
		newSeedModelProfileSlot(1, "document_parser", "文档理解模型", enums.AIModelTypeLLM, legacy.DocumentParserProvider, legacy.DocumentParserModel, "chat_completions", 9, now),
	}
	slots[3].MaxContextTokens = 4000
	slots[3].MaxOutputTokens = 1200
	slots[3].SchemaVersion = "customer_tag_evolution.v1"
	slots[3].PromptTemplate = defaultCustomerTagPromptV1
	slots[3].JSONSchema = defaultCustomerTagSchemaV1
	return db.Transaction(func(tx *gorm.DB) error {
		if err := repositories.ModelProfileTemplateRepository.Save(tx, template); err != nil {
			return err
		}
		return repositories.ModelProfileSlotRepository.ReplaceTemplateSlots(tx, template.ID, slots)
	})
}

func newSeedModelProfileSlot(templateID int64, usageCode, displayName string, modelType enums.AIModelType, provider, model, apiMode string, sortNo int, now time.Time) models.ModelProfileSlot {
	return models.ModelProfileSlot{
		TemplateID: templateID, UsageCode: usageCode, DisplayName: displayName,
		ModelType: modelType, Provider: provider, ModelName: model, APIMode: apiMode,
		MaxOutputTokens: 1024, TimeoutMS: 30000, Enabled: true, SortNo: sortNo,
		AuditFields: models.AuditFields{
			CreatedAt: now, UpdatedAt: now,
			CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
			UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
		},
	}
}

const defaultCustomerTagPromptV1 = `你是客户长期偏好标签抽取器。只能根据输入中的 allowedTags 输出操作。
禁止创造标签、输出解释、推断敏感属性或使用非客户消息作为证据。
仅明确长期偏好或多次稳定偏好可新增；临时请求返回空操作。
输出必须严格符合 customer_tag_evolution.v1 JSON。`

const defaultCustomerTagSchemaV1 = `{"type":"object","additionalProperties":false,"required":["schemaVersion","operations"],"properties":{"schemaVersion":{"const":"customer_tag_evolution.v1"},"operations":{"type":"array","maxItems":10,"items":{"type":"object","additionalProperties":false,"required":["op","tagId","replaces","confidence","persistence","evidenceMessageIds","reasonCode"],"properties":{"op":{"enum":["add","refresh","replace","remove"]},"tagId":{"type":"integer","minimum":1},"replaces":{"type":"array","maxItems":5,"uniqueItems":true,"items":{"type":"integer","minimum":1}},"confidence":{"type":"number","minimum":0,"maximum":1},"persistence":{"enum":["long_term","temporary","unclear"]},"evidenceMessageIds":{"type":"array","minItems":1,"maxItems":5,"uniqueItems":true,"items":{"type":"integer","minimum":1}},"reasonCode":{"enum":["explicit_preference","repeated_preference","semantic_merge","explicit_reversal"]}}}}}}`
