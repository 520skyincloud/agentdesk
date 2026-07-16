package migration

import (
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(59, "migrate tenant AI model access", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			if err := removeRetiredAIAgentManagementPermissions(ctx.Tx); err != nil {
				return err
			}
			if err := syncPlatformSystemPermissions(ctx.Tx); err != nil {
				return err
			}
			if err := removeTenantRolePlatformPermissions(ctx.Tx); err != nil {
				return err
			}
			return migrateTenantAIModelAccess(ctx.Tx)
		})
	})
}

func removeRetiredAIAgentManagementPermissions(tx *gorm.DB) error {
	var permissions []models.Permission
	if err := tx.Where("code IN ?", []string{"aiAgent.create", "aiAgent.update", "aiAgent.delete"}).Find(&permissions).Error; err != nil {
		return err
	}
	for _, permission := range permissions {
		if err := tx.Where("permission_id = ?", permission.ID).Delete(&models.RolePermission{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&models.Permission{}, "id = ?", permission.ID).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateTenantAIModelAccess(tx *gorm.DB) error {
	for _, indexName := range []string{"uk_store_ai_model_usage", "uk_store_ai_model_scope_usage", "uk_company_ai_model_usage", "uk_tenant_ai_model_scope_usage"} {
		if tx.Migrator().HasIndex(&models.StoreAIModelSetting{}, indexName) {
			if err := tx.Migrator().DropIndex(&models.StoreAIModelSetting{}, indexName); err != nil {
				return err
			}
		}
	}

	var settings []models.StoreAIModelSetting
	if err := tx.Order("id DESC").Find(&settings).Error; err != nil {
		return err
	}
	seenScopes := make(map[string]int64)
	now := time.Now()
	for i := range settings {
		setting := &settings[i]
		tenantID, storeID := resolveLegacyModelSettingTenant(tx, setting)
		if tenantID <= 0 {
			if err := repositories.StoreAIModelSettingRepository.Updates(tx, setting.ID, map[string]any{
				"status":   enums.StatusDisabled,
				"provider": "", "base_url": "", "api_key": "", "api_mode": "chat_completions",
				"model_type": "", "model_name": "", "dimension": 0, "max_context_tokens": 0,
				"max_output_tokens": 0, "timeout_ms": 30000, "max_retry_count": 0, "rpm_limit": 0, "tpm_limit": 0,
				"config_fingerprint": "", "last_test_status": "", "last_tested_at": nil, "last_test_latency_ms": 0,
				"remark": "", "updated_at": now,
				"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}); err != nil {
				return err
			}
			continue
		}
		config := resolveLegacyModelSettingConfig(tx, setting)
		configID := int64(0)
		if config != nil {
			configID = config.ID
		}
		spec, supportedUsage := constants.AIModelUsageSpecByCode(strings.TrimSpace(setting.UsageCode))
		wxWorkInstanceID := setting.WxWorkInstanceID
		if wxWorkInstanceID <= 0 {
			storeID = 0
		}
		scopeKey := modelAssignmentScopeKey(tenantID, wxWorkInstanceID, setting.UsageCode)
		if _, exists := seenScopes[scopeKey]; exists {
			if err := repositories.StoreAIModelSettingRepository.Delete(tx, setting.ID); err != nil {
				return err
			}
			continue
		}
		seenScopes[scopeKey] = setting.ID
		status := setting.Status
		if configID <= 0 || !supportedUsage || config.ModelType != spec.ExpectedType {
			status = enums.StatusDisabled
		}
		if err := repositories.StoreAIModelSettingRepository.Updates(tx, setting.ID, map[string]any{
			"tenant_id": tenantID, "company_id": 0, "store_id": storeID,
			"wx_work_instance_id": wxWorkInstanceID, "ai_config_id": configID, "status": status,
			"provider": "", "base_url": "", "api_key": "", "api_mode": "chat_completions",
			"model_type": "", "model_name": "", "dimension": 0, "max_context_tokens": 0,
			"max_output_tokens": 0, "timeout_ms": 30000, "max_retry_count": 0, "rpm_limit": 0, "tpm_limit": 0,
			"config_fingerprint": "", "last_test_status": "", "last_tested_at": nil, "last_test_latency_ms": 0,
			"updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}); err != nil {
			return err
		}
		if configID > 0 && status == enums.StatusOk {
			if err := ensureTenantAIModelGrant(tx, tenantID, configID, now); err != nil {
				return err
			}
		}
	}

	var agents []models.AIAgent
	if err := tx.Where("tenant_id > 0 AND ai_config_id > 0 AND status <> ?", enums.StatusDeleted).Order("id DESC").Find(&agents).Error; err != nil {
		return err
	}
	for i := range agents {
		agent := &agents[i]
		config := repositories.AIConfigRepository.Get(tx, agent.AIConfigID)
		if config == nil || config.Status != enums.StatusOk {
			continue
		}
		if err := ensureTenantAIModelGrant(tx, agent.TenantID, config.ID, now); err != nil {
			return err
		}
		if config.ModelType != enums.AIModelTypeLLM {
			continue
		}
		if err := ensureTenantDefaultModelAssignment(tx, agent.TenantID, constants.AIModelUsageReplyLLM, config.ID, now); err != nil {
			return err
		}
		if err := ensureTenantDefaultModelAssignment(tx, agent.TenantID, constants.AIModelUsageIntentDetectLLM, config.ID, now); err != nil {
			return err
		}
	}
	var tenants []models.Tenant
	if err := tx.Where("status <> ?", enums.StatusDeleted).Order("id").Find(&tenants).Error; err != nil {
		return err
	}
	for i := range tenants {
		if repositories.AIAgentRepository.FindOne(tx, sqls.NewCnd().Eq("tenant_id", tenants[i].ID).Where("status <> ?", enums.StatusDeleted)) != nil {
			continue
		}
		team := repositories.AgentTeamRepository.FindOne(tx, sqls.NewCnd().Eq("tenant_id", tenants[i].ID).Eq("is_default", true).Where("status <> ?", enums.StatusDeleted))
		teamIDs := ""
		if team != nil {
			teamIDs = strconv.FormatInt(team.ID, 10)
		}
		if err := repositories.AIAgentRepository.Create(tx, &models.AIAgent{
			TenantID: tenants[i].ID, Name: "默认接待策略", Description: "接入公司内部运行身份",
			Status: enums.StatusOk, ServiceMode: enums.IMConversationServiceModeAIFirst,
			SystemPrompt:        "回答应简短、准确；需要真实动作时必须进入现有服务路由，不得虚构已处理结果。",
			ReplyTimeoutSeconds: 180, TeamIDs: teamIDs, HandoffMode: enums.AIAgentHandoffModeWaitPool,
			FallbackMode: enums.AIAgentFallbackModeNoAnswer, AuditFields: systemModelAuditFields(now),
		}); err != nil {
			return err
		}
	}

	if !tx.Migrator().HasIndex(&models.StoreAIModelSetting{}, "uk_tenant_ai_model_scope_usage") {
		if err := tx.Exec("CREATE UNIQUE INDEX uk_tenant_ai_model_scope_usage ON t_store_ai_model_setting (tenant_id, wx_work_instance_id, usage_code)").Error; err != nil {
			return err
		}
	}
	return nil
}

func resolveLegacyModelSettingTenant(tx *gorm.DB, setting *models.StoreAIModelSetting) (int64, int64) {
	if setting == nil {
		return 0, 0
	}
	if setting.WxWorkInstanceID > 0 {
		if instance := repositories.WxWorkProtocolInstanceRepository.Get(tx, setting.WxWorkInstanceID); instance != nil {
			return instance.TenantID, instance.StoreID
		}
	}
	if setting.CompanyID > 0 {
		if company := repositories.CompanyRepository.Get(tx, setting.CompanyID); company != nil {
			return company.TenantID, 0
		}
	}
	if setting.StoreID > 0 {
		if store := repositories.StoreRepository.Get(tx, setting.StoreID); store != nil {
			return store.TenantID, store.ID
		}
	}
	return setting.TenantID, setting.StoreID
}

func resolveLegacyModelSettingConfig(tx *gorm.DB, setting *models.StoreAIModelSetting) *models.AIConfig {
	if setting == nil {
		return nil
	}
	if setting.AIConfigID > 0 {
		if config := repositories.AIConfigRepository.Get(tx, setting.AIConfigID); config != nil {
			return config
		}
	}
	modelName := strings.TrimSpace(setting.ModelName)
	if modelName == "" {
		return nil
	}
	cnd := sqls.NewCnd().Eq("model_name", modelName).Where("status <> ?", enums.StatusDeleted)
	if setting.Provider != "" {
		cnd.Eq("provider", setting.Provider)
	}
	if strings.TrimSpace(setting.BaseURL) != "" {
		cnd.Eq("base_url", strings.TrimSpace(setting.BaseURL))
	}
	return repositories.AIConfigRepository.FindOne(tx, cnd.Desc("id"))
}

func ensureTenantAIModelGrant(tx *gorm.DB, tenantID, aiConfigID int64, now time.Time) error {
	grant := repositories.TenantAIModelGrantRepository.Take(tx, "tenant_id = ? AND ai_config_id = ?", tenantID, aiConfigID)
	if grant != nil {
		return repositories.TenantAIModelGrantRepository.Updates(tx, grant.ID, map[string]any{
			"status": enums.StatusOk, "updated_at": now,
			"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		})
	}
	return repositories.TenantAIModelGrantRepository.Create(tx, &models.TenantAIModelGrant{
		TenantID: tenantID, AIConfigID: aiConfigID, Status: enums.StatusOk,
		AuditFields: systemModelAuditFields(now),
	})
}

func ensureTenantDefaultModelAssignment(tx *gorm.DB, tenantID int64, usageCode string, aiConfigID int64, now time.Time) error {
	existing := repositories.StoreAIModelSettingRepository.Take(tx,
		"tenant_id = ? AND wx_work_instance_id = 0 AND usage_code = ?",
		tenantID, usageCode)
	if existing != nil {
		if existing.Status == enums.StatusOk && existing.AIConfigID > 0 {
			return nil
		}
		return repositories.StoreAIModelSettingRepository.Updates(tx, existing.ID, map[string]any{
			"ai_config_id": aiConfigID, "status": enums.StatusOk, "updated_at": now,
			"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		})
	}
	return repositories.StoreAIModelSettingRepository.Create(tx, &models.StoreAIModelSetting{
		TenantID: tenantID, UsageCode: usageCode, AIConfigID: aiConfigID, Status: enums.StatusOk,
		AuditFields: systemModelAuditFields(now),
	})
}

func systemModelAuditFields(now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt: now, CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
		UpdatedAt: now, UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
	}
}

func modelAssignmentScopeKey(tenantID, wxWorkInstanceID int64, usageCode string) string {
	return strings.Join([]string{
		fmtInt64(tenantID), fmtInt64(wxWorkInstanceID), strings.TrimSpace(usageCode),
	}, ":")
}

func fmtInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
