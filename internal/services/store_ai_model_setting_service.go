package services

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	StoreAIModelUsageReplyLLM           = "reply_llm"
	StoreAIModelUsageIntentDetectLLM    = "intent_detect_llm"
	StoreAIModelUsageMediaUnderstanding = "media_understanding"
	StoreAIModelUsageMemorySummaryLLM   = "memory_summary_llm"

	StoreAIModelSourceAccountOverride = "account_override"
	StoreAIModelSourceCompanyOverride = "company_override"
	StoreAIModelSourceGlobalDefault   = "global_default"
	StoreAIModelSourceAgentLegacy     = "agent_legacy"
)

var StoreAIModelSettingService = newStoreAIModelSettingService()

func newStoreAIModelSettingService() *storeAIModelSettingService {
	return &storeAIModelSettingService{}
}

type storeAIModelSettingService struct{}

type StoreAIModelUsageMeta struct {
	Code          string
	Name          string
	ExpectedType  enums.AIModelType
	AllowDisabled bool
}

type ResolvedAIConfig struct {
	Config         models.AIConfig
	Source         string
	ModelSettingID int64
}

func StoreAIModelUsageMetas() []StoreAIModelUsageMeta {
	return []StoreAIModelUsageMeta{
		{Code: StoreAIModelUsageReplyLLM, Name: "回复生成模型", ExpectedType: enums.AIModelTypeLLM},
		{Code: StoreAIModelUsageIntentDetectLLM, Name: "意图识别模型", ExpectedType: enums.AIModelTypeLLM},
		{Code: StoreAIModelUsageMediaUnderstanding, Name: "媒体理解模型", ExpectedType: enums.AIModelTypeVision},
		{Code: StoreAIModelUsageMemorySummaryLLM, Name: "长期记忆摘要模型", ExpectedType: enums.AIModelTypeLLM},
	}
}

func StoreAIModelUsageMetaByCode(code string) (StoreAIModelUsageMeta, bool) {
	code = strings.TrimSpace(code)
	for _, item := range StoreAIModelUsageMetas() {
		if item.Code == code {
			return item, true
		}
	}
	return StoreAIModelUsageMeta{}, false
}

func (s *storeAIModelSettingService) FindByStoreID(storeID int64) []models.StoreAIModelSetting {
	return s.FindByScope(0, storeID, 0)
}

func (s *storeAIModelSettingService) FindByScope(companyID int64, storeID int64, wxWorkInstanceID int64) []models.StoreAIModelSetting {
	companyID = s.resolveCompanyIDForScope(companyID, storeID, wxWorkInstanceID)
	wxWorkInstanceID = normalizeStoreAIModelWxWorkInstanceID(wxWorkInstanceID)
	if companyID <= 0 && wxWorkInstanceID <= 0 {
		return nil
	}
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.StoreAIModelSetting{}) {
		return nil
	}
	cnd := sqls.NewCnd().Asc("usage_code").Asc("id")
	if wxWorkInstanceID > 0 {
		cnd.Eq("wx_work_instance_id", wxWorkInstanceID)
		return repositories.StoreAIModelSettingRepository.Find(db, cnd)
	}
	return repositories.StoreAIModelSettingRepository.Find(db, sqls.NewCnd().
		Eq("company_id", companyID).
		Eq("wx_work_instance_id", 0).
		Asc("usage_code").
		Asc("id"))
}

func (s *storeAIModelSettingService) ListResponses(companyID int64, storeID int64, wxWorkInstanceID int64) []response.StoreAIModelSettingResponse {
	companyID = s.resolveCompanyIDForScope(companyID, storeID, wxWorkInstanceID)
	wxWorkInstanceID = normalizeStoreAIModelWxWorkInstanceID(wxWorkInstanceID)
	if wxWorkInstanceID <= 0 {
		storeID = 0
	}
	settings := s.FindByScope(companyID, storeID, wxWorkInstanceID)
	byUsage := make(map[string]models.StoreAIModelSetting, len(settings))
	for _, item := range settings {
		if existing, ok := byUsage[item.UsageCode]; ok && wxWorkInstanceID > 0 && existing.CompanyID == companyID && item.CompanyID != companyID {
			continue
		}
		byUsage[item.UsageCode] = item
	}
	ret := make([]response.StoreAIModelSettingResponse, 0, len(StoreAIModelUsageMetas()))
	for _, meta := range StoreAIModelUsageMetas() {
		setting := byUsage[meta.Code]
		ret = append(ret, s.buildResponse(companyID, storeID, wxWorkInstanceID, meta, setting))
	}
	return ret
}

func (s *storeAIModelSettingService) buildResponse(companyID int64, storeID int64, wxWorkInstanceID int64, meta StoreAIModelUsageMeta, setting models.StoreAIModelSetting) response.StoreAIModelSettingResponse {
	settingForDisplay := setting
	if config := AIConfigService.Get(setting.AIConfigID); shouldFillStoreAIModelSettingFromLegacy(settingForDisplay, config, meta.ExpectedType) {
		settingForDisplay = storeAIModelSettingFromAIConfig(settingForDisplay, *config)
	}

	ret := response.StoreAIModelSettingResponse{
		CompanyID:         companyID,
		StoreID:           storeID,
		WxWorkInstanceID:  wxWorkInstanceID,
		UsageCode:         meta.Code,
		UsageName:         meta.Name,
		ExpectedModelType: string(meta.ExpectedType),
		AIConfigID:        setting.AIConfigID,
		Enabled:           setting.Status == enums.StatusOk && isStoreAIModelSettingUsable(&settingForDisplay, meta.ExpectedType),
		Provider:          string(settingForDisplay.Provider),
		BaseURL:           settingForDisplay.BaseURL,
		HasAPIKey:         strings.TrimSpace(settingForDisplay.APIKey) != "",
		APIMode:           normalizeAIConfigAPIMode(settingForDisplay.APIMode),
		ModelType:         string(firstNonBlankAIModelType(settingForDisplay.ModelType, meta.ExpectedType)),
		ModelName:         settingForDisplay.ModelName,
		Dimension:         settingForDisplay.Dimension,
		MaxContextTokens:  settingForDisplay.MaxContextTokens,
		MaxOutputTokens:   settingForDisplay.MaxOutputTokens,
		TimeoutMS:         normalizePositiveInt(settingForDisplay.TimeoutMS, 30000),
		MaxRetryCount:     normalizeNonNegativeInt(settingForDisplay.MaxRetryCount),
		RPMLimit:          normalizeNonNegativeInt(settingForDisplay.RPMLimit),
		TPMLimit:          normalizeNonNegativeInt(settingForDisplay.TPMLimit),
		Remark:            utils.RepairMojibakeText(setting.Remark),
	}
	if config := AIConfigService.Get(setting.AIConfigID); config != nil {
		ret.AIConfigName = utils.RepairMojibakeText(config.Name)
	}
	if resolved, err := s.ResolveForScope(companyID, storeID, wxWorkInstanceID, meta.Code); err == nil {
		ret.EffectiveAIConfigID = resolved.Config.ID
		ret.EffectiveModelSettingID = resolved.ModelSettingID
		ret.EffectiveAIConfigName = utils.RepairMojibakeText(resolved.Config.Name)
		ret.EffectiveModelName = resolved.Config.ModelName
		ret.EffectiveProvider = string(resolved.Config.Provider)
		ret.EffectiveBaseURL = resolved.Config.BaseURL
		ret.EffectiveModelSource = resolved.Source
	}
	return ret
}

func (s *storeAIModelSettingService) UpdateStoreSettings(req request.UpdateStoreAIModelSettingsRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	companyID, storeID, wxWorkInstanceID, err := s.normalizeAIModelSettingScope(req.CompanyID, req.StoreID, req.WxWorkInstanceID)
	if err != nil {
		return err
	}
	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		for _, item := range req.Settings {
			usageCode := strings.TrimSpace(item.UsageCode)
			meta, ok := StoreAIModelUsageMetaByCode(usageCode)
			if !ok {
				return errorsx.InvalidParam("模型用途不合法")
			}

			var existing *models.StoreAIModelSetting
			if wxWorkInstanceID > 0 {
				existing = repositories.StoreAIModelSettingRepository.Take(ctx.Tx,
					"wx_work_instance_id = ? AND usage_code = ?",
					wxWorkInstanceID, usageCode)
			} else {
				existing = repositories.StoreAIModelSettingRepository.Take(ctx.Tx,
					"company_id = ? AND wx_work_instance_id = 0 AND usage_code = ?",
					companyID, usageCode)
			}
			apiKey := strings.TrimSpace(item.APIKey)
			if existing != nil && apiKey == "" {
				apiKey = existing.APIKey
			}
			modelType := firstNonBlankAIModelType(item.ModelType, meta.ExpectedType)
			if item.Enabled {
				if err := validateStoreAIModelSettingPayload(item, apiKey, modelType, meta.ExpectedType); err != nil {
					return err
				}
			}

			status := enums.StatusDisabled
			if item.Enabled {
				status = enums.StatusOk
			}
			columns := map[string]any{
				"company_id":          companyID,
				"store_id":            storeID,
				"wx_work_instance_id": wxWorkInstanceID,
				"usage_code":          usageCode,
				"provider":            item.Provider,
				"base_url":            strings.TrimSpace(item.BaseURL),
				"api_mode":            normalizeAIConfigAPIMode(item.APIMode),
				"model_type":          modelType,
				"model_name":          strings.TrimSpace(item.ModelName),
				"dimension":           normalizeNonNegativeInt(item.Dimension),
				"max_context_tokens":  normalizeNonNegativeInt(item.MaxContextTokens),
				"max_output_tokens":   normalizeNonNegativeInt(item.MaxOutputTokens),
				"timeout_ms":          normalizePositiveInt(item.TimeoutMS, 30000),
				"max_retry_count":     normalizeNonNegativeInt(item.MaxRetryCount),
				"rpm_limit":           normalizeNonNegativeInt(item.RPMLimit),
				"tpm_limit":           normalizeNonNegativeInt(item.TPMLimit),
				"status":              status,
				"remark":              strings.TrimSpace(item.Remark),
				"updated_at":          now,
				"update_user_id":      operator.UserID,
				"update_user_name":    operator.Username,
			}
			if strings.TrimSpace(item.APIKey) != "" || existing == nil {
				columns["api_key"] = apiKey
			}
			if existing != nil {
				if err := repositories.StoreAIModelSettingRepository.Updates(ctx.Tx, existing.ID, columns); err != nil {
					return err
				}
				continue
			}
			if err := repositories.StoreAIModelSettingRepository.Create(ctx.Tx, &models.StoreAIModelSetting{
				CompanyID:        companyID,
				StoreID:          storeID,
				WxWorkInstanceID: wxWorkInstanceID,
				UsageCode:        usageCode,
				Provider:         item.Provider,
				BaseURL:          strings.TrimSpace(item.BaseURL),
				APIKey:           apiKey,
				APIMode:          normalizeAIConfigAPIMode(item.APIMode),
				ModelType:        modelType,
				ModelName:        strings.TrimSpace(item.ModelName),
				Dimension:        normalizeNonNegativeInt(item.Dimension),
				MaxContextTokens: normalizeNonNegativeInt(item.MaxContextTokens),
				MaxOutputTokens:  normalizeNonNegativeInt(item.MaxOutputTokens),
				TimeoutMS:        normalizePositiveInt(item.TimeoutMS, 30000),
				MaxRetryCount:    normalizeNonNegativeInt(item.MaxRetryCount),
				RPMLimit:         normalizeNonNegativeInt(item.RPMLimit),
				TPMLimit:         normalizeNonNegativeInt(item.TPMLimit),
				Status:           status,
				Remark:           strings.TrimSpace(item.Remark),
				AuditFields: models.AuditFields{
					CreatedAt:      now,
					CreateUserID:   operator.UserID,
					CreateUserName: operator.Username,
					UpdatedAt:      now,
					UpdateUserID:   operator.UserID,
					UpdateUserName: operator.Username,
				},
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *storeAIModelSettingService) Resolve(storeID int64, usageCode string) (*ResolvedAIConfig, error) {
	return s.ResolveForScope(0, storeID, 0, usageCode)
}

func (s *storeAIModelSettingService) ResolveForScope(companyID int64, storeID int64, wxWorkInstanceID int64, usageCode string) (*ResolvedAIConfig, error) {
	meta, ok := StoreAIModelUsageMetaByCode(usageCode)
	if !ok {
		return nil, errorsx.InvalidParam("模型用途不合法")
	}
	db := sqls.DB()
	if db == nil {
		return nil, errorsx.BusinessError(2005, "未配置可用的 AI 配置")
	}
	companyID = s.resolveCompanyIDForScope(companyID, storeID, wxWorkInstanceID)
	wxWorkInstanceID = normalizeStoreAIModelWxWorkInstanceID(wxWorkInstanceID)
	if db.Migrator().HasTable(&models.StoreAIModelSetting{}) {
		if wxWorkInstanceID > 0 {
			setting := repositories.StoreAIModelSettingRepository.Take(db,
				"company_id = ? AND wx_work_instance_id = ? AND usage_code = ? AND status = ?",
				companyID, wxWorkInstanceID, meta.Code, enums.StatusOk)
			if resolved, ok := s.resolveSettingConfig(setting, meta, StoreAIModelSourceAccountOverride); ok {
				return resolved, nil
			}
			if companyID > 0 {
				setting = repositories.StoreAIModelSettingRepository.Take(db,
					"company_id = 0 AND wx_work_instance_id = ? AND usage_code = ? AND status = ?",
					wxWorkInstanceID, meta.Code, enums.StatusOk)
				if resolved, ok := s.resolveSettingConfig(setting, meta, StoreAIModelSourceAccountOverride); ok {
					return resolved, nil
				}
			}
		}
	}
	if companyID > 0 && db.Migrator().HasTable(&models.StoreAIModelSetting{}) {
		setting := repositories.StoreAIModelSettingRepository.Take(db,
			"company_id = ? AND store_id = 0 AND wx_work_instance_id = 0 AND usage_code = ? AND status = ?",
			companyID, meta.Code, enums.StatusOk)
		if resolved, ok := s.resolveSettingConfig(setting, meta, StoreAIModelSourceCompanyOverride); ok {
			return resolved, nil
		}
	}
	return s.resolveGlobalDefault(meta)
}

func (s *storeAIModelSettingService) ResolveForConversation(conversationID int64, usageCode string, legacyAgentConfigID int64) (*ResolvedAIConfig, error) {
	if route := ConversationRouteService.GetByConversationID(conversationID); route != nil && route.WxWorkInstanceID > 0 {
		return s.ResolveForScope(0, route.StoreID, route.WxWorkInstanceID, usageCode)
	}
	if legacyAgentConfigID > 0 {
		config := AIConfigService.Get(legacyAgentConfigID)
		meta, ok := StoreAIModelUsageMetaByCode(usageCode)
		if ok && isUsableAIConfigForUsage(config, meta.ExpectedType) {
			return &ResolvedAIConfig{Config: *config, Source: StoreAIModelSourceAgentLegacy}, nil
		}
	}
	return s.ResolveForScope(0, 0, 0, usageCode)
}

func (s *storeAIModelSettingService) ResolveForMessage(message *models.Message, usageCode string) (*ResolvedAIConfig, error) {
	if message == nil {
		return s.ResolveForScope(0, 0, 0, usageCode)
	}
	route := ConversationRouteService.GetByConversationID(message.ConversationID)
	if route != nil && route.WxWorkInstanceID > 0 {
		return s.ResolveForScope(0, route.StoreID, route.WxWorkInstanceID, usageCode)
	}
	return s.ResolveForScope(0, 0, 0, usageCode)
}

func (s *storeAIModelSettingService) resolveSettingConfig(setting *models.StoreAIModelSetting, meta StoreAIModelUsageMeta, source string) (*ResolvedAIConfig, bool) {
	if setting == nil || setting.Status != enums.StatusOk {
		return nil, false
	}
	if config, ok := storeAIModelSettingToAIConfig(setting, meta); ok {
		return &ResolvedAIConfig{Config: config, Source: source, ModelSettingID: setting.ID}, true
	}
	if setting.AIConfigID > 0 {
		config := AIConfigService.Get(setting.AIConfigID)
		if isUsableAIConfigForUsage(config, meta.ExpectedType) {
			return &ResolvedAIConfig{Config: *config, Source: source, ModelSettingID: setting.ID}, true
		}
	}
	return nil, false
}

func (s *storeAIModelSettingService) resolveGlobalDefault(meta StoreAIModelUsageMeta) (*ResolvedAIConfig, error) {
	db := sqls.DB()
	if meta.Code == StoreAIModelUsageIntentDetectLLM {
		if config := repositories.AIConfigRepository.FindOne(db, sqls.NewCnd().
			Eq("status", enums.StatusOk).
			Eq("model_type", enums.AIModelTypeLLM).
			Eq("intent_detect_enabled", true).
			Desc("sort_no").
			Desc("id")); config != nil {
			return &ResolvedAIConfig{Config: *config, Source: StoreAIModelSourceGlobalDefault}, nil
		}
	}
	config := repositories.AIConfigRepository.GetEnabled(db, meta.ExpectedType)
	if config == nil {
		return nil, errorsx.BusinessError(2005, "未配置可用的 AI 配置")
	}
	return &ResolvedAIConfig{Config: *config, Source: StoreAIModelSourceGlobalDefault}, nil
}

func isUsableAIConfigForUsage(config *models.AIConfig, expectedType enums.AIModelType) bool {
	return config != nil && config.Status == enums.StatusOk && config.ModelType == expectedType && strings.TrimSpace(config.ModelName) != "" && strings.TrimSpace(config.APIKey) != ""
}

func (s *storeAIModelSettingService) DisableLegacyWxWorkDedicatedAgents() error {
	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if ctx.Tx.Migrator().HasColumn("t_wx_work_protocol_instance", "ai_agent_id") {
			if err := ctx.Tx.Table("t_wx_work_protocol_instance").
				Where("ai_agent_id > 0").
				Updates(map[string]any{
					"ai_agent_id":      0,
					"updated_at":       now,
					"update_user_id":   constants.SystemAuditUserID,
					"update_user_name": constants.SystemAuditUserName,
				}).Error; err != nil {
				return err
			}
		}
		return ctx.Tx.Model(&models.AIAgent{}).
			Where("name LIKE ?", "%独立配置%").
			Updates(map[string]any{
				"status":           enums.StatusDisabled,
				"updated_at":       now,
				"update_user_id":   constants.SystemAuditUserID,
				"update_user_name": constants.SystemAuditUserName,
			}).Error
	})
}

func (s *storeAIModelSettingService) BackfillStoreAIModelSettings() error {
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.StoreAIModelSetting{}) {
		return nil
	}
	if db.Migrator().HasIndex(&models.StoreAIModelSetting{}, "uk_store_ai_model_usage") {
		if err := db.Migrator().DropIndex(&models.StoreAIModelSetting{}, "uk_store_ai_model_usage"); err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex(&models.StoreAIModelSetting{}, "uk_store_ai_model_scope_usage") {
		if err := db.Migrator().CreateIndex(&models.StoreAIModelSetting{}, "uk_store_ai_model_scope_usage"); err != nil {
			return err
		}
	}
	settings := repositories.StoreAIModelSettingRepository.Find(db, sqls.NewCnd().Gt("ai_config_id", 0).Asc("id"))
	for _, setting := range settings {
		if strings.TrimSpace(setting.ModelName) != "" && strings.TrimSpace(setting.APIKey) != "" {
			continue
		}
		config := AIConfigService.Get(setting.AIConfigID)
		if config == nil {
			continue
		}
		if err := repositories.StoreAIModelSettingRepository.Updates(db, setting.ID, map[string]any{
			"provider":           config.Provider,
			"base_url":           config.BaseURL,
			"api_key":            config.APIKey,
			"api_mode":           normalizeAIConfigAPIMode(config.APIMode),
			"model_type":         config.ModelType,
			"model_name":         config.ModelName,
			"dimension":          config.Dimension,
			"max_context_tokens": config.MaxContextTokens,
			"max_output_tokens":  config.MaxOutputTokens,
			"timeout_ms":         normalizePositiveInt(config.TimeoutMS, 30000),
			"max_retry_count":    normalizeNonNegativeInt(config.MaxRetryCount),
			"rpm_limit":          normalizeNonNegativeInt(config.RPMLimit),
			"tpm_limit":          normalizeNonNegativeInt(config.TPMLimit),
			"updated_at":         time.Now(),
			"update_user_id":     constants.SystemAuditUserID,
			"update_user_name":   constants.SystemAuditUserName,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *storeAIModelSettingService) RebuildStoreAIModelSettingScopeIndex() error {
	db := sqls.DB()
	if db == nil || !db.Migrator().HasTable(&models.StoreAIModelSetting{}) {
		return nil
	}
	for _, indexName := range []string{"uk_store_ai_model_usage", "uk_store_ai_model_scope_usage", "uk_company_ai_model_usage"} {
		if db.Migrator().HasIndex(&models.StoreAIModelSetting{}, indexName) {
			if err := db.Migrator().DropIndex(&models.StoreAIModelSetting{}, indexName); err != nil {
				return err
			}
		}
	}
	return db.Migrator().CreateIndex(&models.StoreAIModelSetting{}, "uk_store_ai_model_scope_usage")
}

func validateStoreAIModelSettingPayload(item request.StoreAIModelSettingUpdateRequest, apiKey string, modelType enums.AIModelType, expectedType enums.AIModelType) error {
	if strings.TrimSpace(string(item.Provider)) == "" {
		return errorsx.InvalidParam("供应商不能为空")
	}
	if strings.TrimSpace(item.BaseURL) == "" {
		return errorsx.InvalidParam("基础地址不能为空")
	}
	if modelType != expectedType {
		return errorsx.InvalidParam("模型类型不匹配")
	}
	if strings.TrimSpace(item.ModelName) == "" {
		return errorsx.InvalidParam("模型名称不能为空")
	}
	if strings.TrimSpace(apiKey) == "" {
		return errorsx.InvalidParam("API Key 不能为空")
	}
	return nil
}

func (s *storeAIModelSettingService) normalizeAIModelSettingScope(companyID int64, storeID int64, wxWorkInstanceID int64) (int64, int64, int64, error) {
	wxWorkInstanceID = normalizeStoreAIModelWxWorkInstanceID(wxWorkInstanceID)
	if wxWorkInstanceID > 0 && storeID <= 0 {
		if instance := WxWorkProtocolInstanceService.Get(wxWorkInstanceID); instance != nil {
			storeID = instance.StoreID
		}
	}
	companyID = s.resolveCompanyIDForScope(companyID, storeID, wxWorkInstanceID)
	if wxWorkInstanceID > 0 {
		return companyID, storeID, wxWorkInstanceID, nil
	}
	if companyID > 0 {
		return companyID, 0, 0, nil
	}
	return 0, 0, 0, errorsx.InvalidParam("公司或员工号不能为空")
}

func normalizeStoreAIModelCompanyID(companyID int64) int64 {
	if companyID < 0 {
		return 0
	}
	return companyID
}

func normalizeStoreAIModelWxWorkInstanceID(wxWorkInstanceID int64) int64 {
	if wxWorkInstanceID < 0 {
		return 0
	}
	return wxWorkInstanceID
}

func normalizeNonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func normalizePositiveInt(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func firstNonBlankAIModelType(value enums.AIModelType, fallback enums.AIModelType) enums.AIModelType {
	if strings.TrimSpace(string(value)) == "" {
		return fallback
	}
	return value
}

func isStoreAIModelSettingUsable(setting *models.StoreAIModelSetting, expectedType enums.AIModelType) bool {
	if setting == nil {
		return false
	}
	return strings.TrimSpace(string(setting.Provider)) != "" &&
		strings.TrimSpace(setting.BaseURL) != "" &&
		strings.TrimSpace(setting.APIKey) != "" &&
		setting.ModelType == expectedType &&
		strings.TrimSpace(setting.ModelName) != ""
}

func storeAIModelSettingToAIConfig(setting *models.StoreAIModelSetting, meta StoreAIModelUsageMeta) (models.AIConfig, bool) {
	if !isStoreAIModelSettingUsable(setting, meta.ExpectedType) {
		return models.AIConfig{}, false
	}
	scopeName := "公司模型覆盖"
	if setting.WxWorkInstanceID > 0 {
		scopeName = "员工号模型覆盖"
	}
	return models.AIConfig{
		ID:               0,
		Name:             fmt.Sprintf("%s · %s", scopeName, meta.Name),
		Provider:         setting.Provider,
		BaseURL:          strings.TrimSpace(setting.BaseURL),
		APIKey:           strings.TrimSpace(setting.APIKey),
		APIMode:          normalizeAIConfigAPIMode(setting.APIMode),
		ModelType:        setting.ModelType,
		ModelName:        strings.TrimSpace(setting.ModelName),
		Dimension:        normalizeNonNegativeInt(setting.Dimension),
		MaxContextTokens: normalizeNonNegativeInt(setting.MaxContextTokens),
		MaxOutputTokens:  normalizeNonNegativeInt(setting.MaxOutputTokens),
		TimeoutMS:        normalizePositiveInt(setting.TimeoutMS, 30000),
		MaxRetryCount:    normalizeNonNegativeInt(setting.MaxRetryCount),
		RPMLimit:         normalizeNonNegativeInt(setting.RPMLimit),
		TPMLimit:         normalizeNonNegativeInt(setting.TPMLimit),
		Status:           enums.StatusOk,
		Remark:           strings.TrimSpace(setting.Remark),
	}, true
}

func shouldFillStoreAIModelSettingFromLegacy(setting models.StoreAIModelSetting, config *models.AIConfig, expectedType enums.AIModelType) bool {
	return setting.ID > 0 &&
		config != nil &&
		config.ModelType == expectedType &&
		(strings.TrimSpace(setting.ModelName) == "" || strings.TrimSpace(setting.APIKey) == "")
}

func storeAIModelSettingFromAIConfig(setting models.StoreAIModelSetting, config models.AIConfig) models.StoreAIModelSetting {
	setting.Provider = config.Provider
	setting.BaseURL = config.BaseURL
	setting.APIKey = config.APIKey
	setting.APIMode = normalizeAIConfigAPIMode(config.APIMode)
	setting.ModelType = config.ModelType
	setting.ModelName = config.ModelName
	setting.Dimension = config.Dimension
	setting.MaxContextTokens = config.MaxContextTokens
	setting.MaxOutputTokens = config.MaxOutputTokens
	setting.TimeoutMS = normalizePositiveInt(config.TimeoutMS, 30000)
	setting.MaxRetryCount = normalizeNonNegativeInt(config.MaxRetryCount)
	setting.RPMLimit = normalizeNonNegativeInt(config.RPMLimit)
	setting.TPMLimit = normalizeNonNegativeInt(config.TPMLimit)
	return setting
}

func (s *storeAIModelSettingService) resolveCompanyIDForStore(companyID int64, storeID int64) int64 {
	companyID = normalizeStoreAIModelCompanyID(companyID)
	if companyID > 0 || storeID <= 0 {
		return companyID
	}
	if store := StoreService.Get(storeID); store != nil {
		return normalizeStoreAIModelCompanyID(store.CompanyID)
	}
	return 0
}

func (s *storeAIModelSettingService) resolveCompanyIDForScope(companyID int64, storeID int64, wxWorkInstanceID int64) int64 {
	companyID = normalizeStoreAIModelCompanyID(companyID)
	if companyID > 0 {
		return companyID
	}
	if wxWorkInstanceID > 0 {
		if instance := WxWorkProtocolInstanceService.Get(wxWorkInstanceID); instance != nil && instance.CompanyID > 0 {
			return normalizeStoreAIModelCompanyID(instance.CompanyID)
		}
	}
	return s.resolveCompanyIDForStore(0, storeID)
}
