package services

import (
	"context"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	StoreAIModelUsageReplyLLM           = "reply_llm"
	StoreAIModelUsageIntentDetectLLM    = "intent_detect_llm"
	StoreAIModelUsageKnowledgeJudgeLLM  = "knowledge_judge_llm"
	StoreAIModelUsageMediaUnderstanding = "media_understanding"
	StoreAIModelUsageMemorySummaryLLM   = "memory_summary_llm"
	StoreAIModelUsageASR                = "media_asr"
	StoreAIModelUsageEmbedding          = "embedding"
	StoreAIModelUsageRerank             = "rerank"
	StoreAIModelUsageDocumentParser     = "document_parser"
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
	Config             models.AIConfig
	Source             string
	ModelSettingID     int64
	CredentialRevision int64
}

func StoreAIModelUsageMetas() []StoreAIModelUsageMeta {
	return []StoreAIModelUsageMeta{
		{Code: StoreAIModelUsageReplyLLM, Name: "回复生成模型", ExpectedType: enums.AIModelTypeLLM},
		{Code: StoreAIModelUsageIntentDetectLLM, Name: "意图识别模型", ExpectedType: enums.AIModelTypeLLM},
		{Code: StoreAIModelUsageKnowledgeJudgeLLM, Name: "知识证据判断模型", ExpectedType: enums.AIModelTypeLLM},
		{Code: StoreAIModelUsageMediaUnderstanding, Name: "媒体理解模型", ExpectedType: enums.AIModelTypeVision},
		{Code: StoreAIModelUsageMemorySummaryLLM, Name: "长期记忆摘要模型", ExpectedType: enums.AIModelTypeLLM},
		{Code: StoreAIModelUsageASR, Name: "语音识别模型", ExpectedType: enums.AIModelTypeASR},
		{Code: StoreAIModelUsageEmbedding, Name: "向量模型", ExpectedType: enums.AIModelTypeEmbedding},
		{Code: StoreAIModelUsageRerank, Name: "重排模型", ExpectedType: enums.AIModelTypeRerank},
		{Code: StoreAIModelUsageDocumentParser, Name: "文档理解模型", ExpectedType: enums.AIModelTypeLLM},
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
		LastTestStatus:    strings.TrimSpace(setting.LastTestStatus),
		LastTestedAt:      utils.FormatTimePtr(setting.LastTestedAt),
		LastTestLatencyMS: setting.LastTestLatencyMS,
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

func (s *storeAIModelSettingService) TestStoreSetting(req request.TestStoreAIModelSettingRequest, operator *dto.AuthPrincipal) (*response.TestStoreAIModelSettingResponse, error) {
	_ = req
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	return nil, errorsx.Forbidden("旧的按用途独立模型密钥入口已停用，请使用平台模型模板和门店唯一模型密钥")
}

func (s *storeAIModelSettingService) UpdateStoreSettings(req request.UpdateStoreAIModelSettingsRequest, operator *dto.AuthPrincipal) error {
	_ = req
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	return errorsx.Forbidden("旧的按用途独立模型密钥入口已停用，请使用平台模型模板和门店唯一模型密钥")
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
	if storeID <= 0 && wxWorkInstanceID > 0 {
		if instance := WxWorkProtocolInstanceService.Get(wxWorkInstanceID); instance != nil {
			storeID = instance.StoreID
		}
	}
	if storeID <= 0 {
		return nil, errorsx.BusinessError(2005, "模型调用缺少有效门店绑定")
	}
	slotUsageCode := modelProfileUsageCodeForStoreUsage(meta.Code)
	resolved, err := ModelProfileTemplateService.ResolveSlot(storeID, slotUsageCode)
	if err != nil {
		return nil, err
	}
	if resolved.Config.ModelType != meta.ExpectedType {
		return nil, errorsx.BusinessError(2005, "模型模板用途与模型类型不匹配")
	}
	return &ResolvedAIConfig{
		Config:             resolved.Config,
		Source:             StoreAIModelSourceStoreCredential,
		ModelSettingID:     resolved.Slot.ID,
		CredentialRevision: resolved.CredentialRevision,
	}, nil
}

func (s *storeAIModelSettingService) ResolveForConversation(conversationID int64, usageCode string, legacyAgentConfigID int64) (*ResolvedAIConfig, error) {
	_ = legacyAgentConfigID
	if conversationID <= 0 {
		return nil, errorsx.BusinessError(2005, "模型调用缺少有效会话")
	}
	if route := ConversationRouteService.GetByConversationID(conversationID); route != nil && (route.StoreID > 0 || route.WxWorkInstanceID > 0) {
		return s.ResolveForScope(0, route.StoreID, route.WxWorkInstanceID, usageCode)
	}
	return nil, errorsx.BusinessError(2005, "会话尚未绑定门店，已停止模型调用")
}

func (s *storeAIModelSettingService) ResolveForMessage(message *models.Message, usageCode string) (*ResolvedAIConfig, error) {
	if message == nil || message.ConversationID <= 0 {
		return nil, errorsx.BusinessError(2005, "模型调用缺少有效消息会话")
	}
	route := ConversationRouteService.GetByConversationID(message.ConversationID)
	if route != nil && (route.StoreID > 0 || route.WxWorkInstanceID > 0) {
		return s.ResolveForScope(0, route.StoreID, route.WxWorkInstanceID, usageCode)
	}
	return nil, errorsx.BusinessError(2005, "消息会话尚未绑定门店，已停止模型调用")
}

func (s *storeAIModelSettingService) ResolveForContext(ctx context.Context, usageCode string) (*ResolvedAIConfig, error) {
	scope := usagex.ScopeFromContext(ctx)
	if scope.ConversationID > 0 {
		return s.ResolveForConversation(scope.ConversationID, usageCode, 0)
	}
	storeID := scope.StoreID
	if storeID <= 0 && scope.KnowledgeBaseID > 0 {
		if knowledgeBase := repositories.KnowledgeBaseRepository.Get(sqls.DB(), scope.KnowledgeBaseID); knowledgeBase != nil {
			storeID = knowledgeBase.StoreID
		}
	}
	return s.ResolveForScope(scope.CompanyID, storeID, scope.WxWorkInstanceID, usageCode)
}

func modelProfileUsageCodeForStoreUsage(usageCode string) string {
	switch strings.TrimSpace(usageCode) {
	case StoreAIModelUsageMediaUnderstanding:
		return ModelProfileUsageVision
	case StoreAIModelUsageASR:
		return ModelProfileUsageASR
	default:
		return strings.TrimSpace(usageCode)
	}
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
