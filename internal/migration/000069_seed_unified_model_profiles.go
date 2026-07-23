package migration

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const seedUnifiedModelProfilesMigrationRemark = "seed unified nine-slot model profiles and permissions"

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
		if err := syncUnifiedModelProfilePermissions(tx); err != nil {
			return err
		}
		if repositories.ModelProfileTemplateRepository.GetLatestByCode(tx, "standard") != nil {
			return nil
		}
		return seedDefaultUnifiedModelProfile(tx)
	})
}

func syncUnifiedModelProfilePermissions(db *gorm.DB) error {
	permissions, err := ensurePermissions(db)
	if err != nil {
		return err
	}
	roles, err := ensureRoles(db)
	if err != nil {
		return err
	}
	if err := ensureRolePermissions(db, roles, permissions); err != nil {
		return err
	}
	retiredCodes := []string{
		"aiConfig.create",
		"aiConfig.delete",
		"tenantModelGrant.view",
		"tenantModelGrant.update",
		"tenantModelAssignment.view",
		"tenantModelAssignment.update",
	}
	var retired []models.Permission
	if err := db.Where("code IN ?", retiredCodes).Find(&retired).Error; err != nil {
		return err
	}
	if len(retired) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(retired))
	now := time.Now()
	for _, permission := range retired {
		ids = append(ids, permission.ID)
		if err := repositories.PermissionRepository.Updates(db, permission.ID, map[string]any{
			"name": "已废弃：" + permission.Name, "status": enums.StatusDisabled,
			"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			"updated_at": now,
		}); err != nil {
			return err
		}
	}
	return db.Where("permission_id IN ?", ids).Delete(&models.RolePermission{}).Error
}

func seedDefaultUnifiedModelProfile(db *gorm.DB) error {
	legacyConfigs := make([]legacyAIConfig, 0)
	if db.Migrator().HasTable(&legacyAIConfig{}) {
		if err := db.Where("status = ?", enums.StatusOk).Order("sort_no DESC").Order("id DESC").Find(&legacyConfigs).Error; err != nil {
			return err
		}
	}
	byType := make(map[enums.AIModelType]legacyAIConfig)
	var intentConfig *legacyAIConfig
	for i := range legacyConfigs {
		item := legacyConfigs[i]
		if _, exists := byType[item.ModelType]; !exists {
			byType[item.ModelType] = item
		}
		if item.ModelType == enums.AIModelTypeLLM && item.IntentDetectEnabled && intentConfig == nil {
			copy := item
			intentConfig = &copy
		}
	}
	llm := byType[enums.AIModelTypeLLM]
	gatewayBaseURL := strings.TrimRight(strings.TrimSpace(llm.BaseURL), "/")
	now := time.Now()
	template := &models.ModelProfileTemplate{
		Code: "standard", Name: "平台标准九槽", Description: "统一集成迁移建立的 NewAPI 九槽模型方案，请完成结构校验与门店 readiness 后启用。",
		Revision: 1, GatewayBaseURL: gatewayBaseURL, Status: enums.ModelProfileStatusDraft,
		AuditFields: systemModelProfileAuditFields(now),
	}
	if err := repositories.ModelProfileTemplateRepository.Create(db, template); err != nil {
		return err
	}
	slots := make([]models.ModelProfileSlot, 0, len(services.RequiredModelUsageSlotSpecs()))
	for index, spec := range services.RequiredModelUsageSlotSpecs() {
		legacy := byType[spec.ExpectedModelType]
		if spec.UsageCode == enums.ModelUsageSlotIntentDetectLLM && intentConfig != nil {
			legacy = *intentConfig
		}
		if spec.UsageCode == enums.ModelUsageSlotDocumentParser {
			legacy = llm
		}
		maxContextTokens := legacy.MaxContextTokens
		maxOutputTokens := legacy.MaxOutputTokens
		if spec.ExpectedModelType == enums.AIModelTypeLLM || spec.ExpectedModelType == enums.AIModelTypeVision {
			if maxContextTokens <= 0 && strings.TrimSpace(legacy.ModelName) != "" {
				maxContextTokens = 8192
			}
			if maxOutputTokens <= 0 && strings.TrimSpace(legacy.ModelName) != "" {
				maxOutputTokens = 1024
			}
		}
		slot := models.ModelProfileSlot{
			TemplateID: template.ID, UsageCode: spec.UsageCode, DisplayName: spec.DisplayName,
			ModelType: spec.ExpectedModelType, Provider: "newapi", ModelName: strings.TrimSpace(legacy.ModelName),
			APIMode: spec.DefaultAPIMode, Dimension: legacy.Dimension,
			MaxContextTokens: maxContextTokens, MaxOutputTokens: maxOutputTokens,
			TimeoutMS: 30000, MaxRetryCount: legacy.MaxRetryCount, Enabled: true, SortNo: index + 1,
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
