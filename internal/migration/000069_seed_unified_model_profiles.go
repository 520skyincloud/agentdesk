package migration

import (
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const seedUnifiedModelProfilesMigrationRemark = "seed unified nine-slot model profile"

func init() {
	register(69, seedUnifiedModelProfilesMigrationRemark, func() error {
		return migrateUnifiedModelProfiles(sqls.DB())
	})
}

func migrateUnifiedModelProfiles(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("unified model profile migration database is nil")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if repositories.ModelProfileTemplateRepository.GetLatestByCode(tx, "standard") != nil {
			return nil
		}
		return seedDefaultUnifiedModelProfile(tx)
	})
}

func seedDefaultUnifiedModelProfile(db *gorm.DB) error {
	now := time.Now()
	template := &models.ModelProfileTemplate{
		Code: "standard", Name: "平台标准九槽", Description: "统一架构建立的 NewAPI 九槽模型方案，请配置九个模型用途后发布。",
		Revision: 1, GatewayBaseURL: constants.UnifiedNewAPIGatewayBaseURL, Status: enums.ModelProfileStatusDraft,
		AuditFields: systemModelProfileAuditFields(now),
	}
	if err := repositories.ModelProfileTemplateRepository.Create(db, template); err != nil {
		return err
	}
	slots := make([]models.ModelProfileSlot, 0, len(services.RequiredModelUsageSlotSpecs()))
	for index, spec := range services.RequiredModelUsageSlotSpecs() {
		slot := models.ModelProfileSlot{
			TemplateID: template.ID, UsageCode: spec.UsageCode, DisplayName: spec.DisplayName,
			ModelType: spec.ExpectedModelType, Provider: "newapi", ModelName: "",
			APIMode:   spec.DefaultAPIMode,
			TimeoutMS: 30000, MaxRetryCount: 2, Enabled: true, SortNo: index + 1,
			AuditFields: systemModelProfileAuditFields(now),
		}
		if spec.UsageCode == enums.ModelUsageSlotCustomerTag {
			slot.SchemaVersion = "customer_tag_evolution.v1"
			slot.PromptTemplate = defaultUnifiedCustomerTagPrompt
			slot.JSONSchema = defaultUnifiedCustomerTagSchema
		}
		slots = append(slots, slot)
	}
	if err := repositories.ModelProfileSlotRepository.ReplaceByTemplateID(db, template.ID, slots); err != nil {
		return err
	}
	if len(services.ValidateModelProfileForPublication(template, slots)) == 0 {
		return repositories.ModelProfileTemplateRepository.Updates(db, template.ID, map[string]any{
			"status": enums.ModelProfileStatusCandidate, "published_at": now,
			"published_by": constants.SystemAuditUserID, "published_by_name": constants.SystemAuditUserName,
			"updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		})
	}
	return nil
}

func systemModelProfileAuditFields(now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt: now, UpdatedAt: now,
		CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
		UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
	}
}

const defaultUnifiedCustomerTagPrompt = `你是客户长期偏好标签抽取器。只能根据输入中的 allowedTags 输出操作。禁止创造标签、输出解释、推断敏感属性或使用非客户消息作为证据。仅明确长期偏好或多次稳定偏好可新增；临时请求返回空操作。输出必须严格符合 customer_tag_evolution.v1 JSON。`

const defaultUnifiedCustomerTagSchema = `{"type":"object","additionalProperties":false,"required":["schemaVersion","operations"],"properties":{"schemaVersion":{"const":"customer_tag_evolution.v1"},"operations":{"type":"array","maxItems":10,"items":{"type":"object","additionalProperties":false,"required":["op","tagId","replaces","confidence","persistence","evidenceMessageIds","reasonCode"],"properties":{"op":{"enum":["add","refresh","replace","remove"]},"tagId":{"type":"integer","minimum":1},"replaces":{"type":"array","maxItems":5,"uniqueItems":true,"items":{"type":"integer","minimum":1}},"confidence":{"type":"number","minimum":0,"maximum":1},"persistence":{"enum":["long_term","temporary","unclear"]},"evidenceMessageIds":{"type":"array","minItems":1,"maxItems":5,"uniqueItems":true,"items":{"type":"integer","minimum":1}},"reasonCode":{"enum":["explicit_preference","repeated_preference","semantic_merge","explicit_reversal"]}}}}}}`
