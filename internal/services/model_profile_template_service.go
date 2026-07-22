package services

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	ModelProfileUsageReplyLLM        = "reply_llm"
	ModelProfileUsageIntentDetectLLM = "intent_detect_llm"
	ModelProfileUsageMemorySummary   = "memory_summary_llm"
	ModelProfileUsageCustomerTag     = "customer_tag_llm"
	ModelProfileUsageVision          = "vision"
	ModelProfileUsageASR             = "asr"
	ModelProfileUsageEmbedding       = "embedding"
	ModelProfileUsageRerank          = "rerank"
	ModelProfileUsageDocumentParser  = "document_parser"
)

var ModelProfileTemplateService = newModelProfileTemplateService()

type modelProfileTemplateService struct{}

type ResolvedModelProfileSlot struct {
	Template           models.ModelProfileTemplate
	Slot               models.ModelProfileSlot
	Config             models.AIConfig
	CredentialRevision int64
}

func newModelProfileTemplateService() *modelProfileTemplateService {
	return &modelProfileTemplateService{}
}

func (s *modelProfileTemplateService) Get(operator *dto.AuthPrincipal) (*response.ModelProfileTemplateResponse, error) {
	if err := requireModelProfileAdmin(operator); err != nil {
		return nil, err
	}
	template := repositories.ModelProfileTemplateRepository.Get(sqls.DB())
	if template == nil {
		return &response.ModelProfileTemplateResponse{
			Name: "平台模型模板", Status: "unconfigured", Slots: []response.ModelProfileSlotResponse{},
		}, nil
	}
	return s.buildResponse(template), nil
}

func (s *modelProfileTemplateService) Update(req request.UpdateModelProfileTemplateRequest, operator *dto.AuthPrincipal) (*response.ModelProfileTemplateResponse, error) {
	if err := requireModelProfileAdmin(operator); err != nil {
		return nil, err
	}
	normalizeModelProfileTemplateRequest(&req)
	if err := validateModelProfileTemplateRequest(req); err != nil {
		return nil, err
	}
	current := repositories.ModelProfileTemplateRepository.Get(sqls.DB())
	revision := int64(1)
	createdAt := time.Now()
	createUserID := operator.UserID
	createUserName := operator.Username
	if current != nil {
		revision = current.Revision + 1
		createdAt = current.CreatedAt
		createUserID = current.CreateUserID
		createUserName = current.CreateUserName
	}
	now := time.Now()
	template := &models.ModelProfileTemplate{
		ID:                           1,
		Name:                         req.Name,
		Revision:                     revision,
		GatewayBaseURL:               req.GatewayBaseURL,
		CustomerTagEvolutionEnabled:  req.CustomerTagEvolutionEnabled,
		CustomerTagEvolutionStoreIDs: encodeModelProfileStoreIDs(req.CustomerTagEvolutionStoreIDs),
		ReplyTagContextEnabled:       req.ReplyTagContextEnabled,
		Status:                       "active",
		AuditFields: models.AuditFields{
			CreatedAt: createdAt, CreateUserID: createUserID, CreateUserName: createUserName,
			UpdatedAt: now, UpdateUserID: operator.UserID, UpdateUserName: operator.Username,
		},
	}
	slots := make([]models.ModelProfileSlot, 0, len(req.Slots))
	for _, input := range req.Slots {
		slots = append(slots, models.ModelProfileSlot{
			TemplateID: template.ID, UsageCode: input.UsageCode, DisplayName: input.DisplayName,
			ModelType: enums.AIModelType(input.ModelType), Provider: input.Provider, ModelName: input.ModelName,
			APIMode: input.APIMode, Dimension: input.Dimension,
			MaxContextTokens: input.MaxContextTokens, MaxOutputTokens: input.MaxOutputTokens,
			TimeoutMS: input.TimeoutMS, MaxRetryCount: input.MaxRetryCount, Temperature: input.Temperature,
			SchemaVersion: input.SchemaVersion, PromptTemplate: input.PromptTemplate, JSONSchema: input.JSONSchema,
			Enabled: input.Enabled, SortNo: input.SortNo,
			AuditFields: models.AuditFields{
				CreatedAt: now, UpdatedAt: now,
				CreateUserID: operator.UserID, CreateUserName: operator.Username,
				UpdateUserID: operator.UserID, UpdateUserName: operator.Username,
			},
		})
	}
	fastGPTTemplateChanged := false
	if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := repositories.ModelProfileTemplateRepository.Save(tx.Tx, template); err != nil {
			return err
		}
		if err := repositories.ModelProfileSlotRepository.ReplaceTemplateSlots(tx.Tx, template.ID, slots); err != nil {
			return err
		}
		changed, err := s.syncLegacyFastGPTTemplate(tx.Tx, template, slots, operator)
		fastGPTTemplateChanged = changed
		return err
	}); err != nil {
		return nil, err
	}
	if fastGPTTemplateChanged {
		_, _ = FastGPTProfileTemplateService.QueueAll(operator)
	}
	return s.buildResponse(template), nil
}

func (s *modelProfileTemplateService) ResolveSlot(storeID int64, usageCode string) (*ResolvedModelProfileSlot, error) {
	if storeID <= 0 {
		return nil, errorsx.InvalidParam("门店不能为空")
	}
	template := repositories.ModelProfileTemplateRepository.Get(sqls.DB())
	if template == nil || template.Status != "active" || template.Revision <= 0 {
		return nil, errorsx.BusinessError(2005, "平台模型模板尚未配置")
	}
	slot := repositories.ModelProfileSlotRepository.GetByUsageCode(sqls.DB(), template.ID, strings.TrimSpace(usageCode))
	if slot == nil || !slot.Enabled {
		return nil, errorsx.BusinessError(2005, "当前模型用途尚未启用")
	}
	credential, err := StoreModelCredentialService.ResolveCurrent(storeID)
	if err != nil {
		return nil, err
	}
	config := models.AIConfig{
		Name: "门店模型 · " + slot.DisplayName, Provider: enums.AIProvider(slot.Provider),
		BaseURL: template.GatewayBaseURL, APIKey: credential.APIKey, APIMode: slot.APIMode,
		ModelType: slot.ModelType, ModelName: slot.ModelName, Dimension: slot.Dimension,
		MaxContextTokens: slot.MaxContextTokens, MaxOutputTokens: slot.MaxOutputTokens,
		TimeoutMS: slot.TimeoutMS, MaxRetryCount: slot.MaxRetryCount, Status: enums.StatusOk,
	}
	if config.TimeoutMS <= 0 {
		config.TimeoutMS = 30000
	}
	if config.MaxOutputTokens <= 0 && config.ModelType == enums.AIModelTypeLLM {
		config.MaxOutputTokens = 1024
	}
	return &ResolvedModelProfileSlot{
		Template: *template, Slot: *slot, Config: config, CredentialRevision: credential.Revision,
	}, nil
}

func (s *modelProfileTemplateService) TestSlot(ctx context.Context, req request.TestModelProfileSlotRequest, operator *dto.AuthPrincipal) (*response.TestModelProfileSlotResponse, error) {
	if err := requireModelProfileAdmin(operator); err != nil {
		return nil, err
	}
	resolved, err := s.ResolveSlot(req.StoreID, req.UsageCode)
	if err != nil {
		return nil, err
	}
	if resolved.Config.ModelType != enums.AIModelTypeLLM {
		return nil, errorsx.InvalidParam("当前仅支持测试大语言模型槽")
	}
	requestID := "model-slot-test-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	companyID := int64(0)
	if store := StoreService.Get(req.StoreID); store != nil {
		companyID = store.CompanyID
	}
	callCtx := usagex.WithScope(ctx, usagex.Scope{
		CompanyID: companyID, StoreID: req.StoreID, RequestID: requestID,
		CredentialRevision: resolved.CredentialRevision, ModelSource: "store_credential_model_profile",
	})
	callCtx, capture := usagex.WithCapture(callCtx)
	startedAt := time.Now()
	result, callErr := ai.LLM.ChatWithConfig(callCtx, resolved.Config,
		"你是模型连接测试器。只输出严格 JSON。",
		`{"schemaVersion":"customer_tag_evolution.v1","operations":[]}`)
	latency := time.Since(startedAt).Milliseconds()
	status := "completed"
	errorClass := ""
	if callErr != nil {
		status = "failed"
		errorClass = "model_connection_failed"
	}
	record := ai.ModelUsageRecord{
		Stage: "model_connection_test", OperationType: strings.TrimSpace(req.UsageCode),
		Config: resolved.Config, LatencyMS: latency, Status: status, ErrorClass: errorClass,
		Receipt: lastUsageReceipt(capture), ExternalEventKey: "model_connection_test:" + requestID,
	}
	if result != nil {
		record.PromptTokens = int64(result.PromptTokens)
		record.CompletionTokens = int64(result.CompletionTokens)
	}
	ai.RecordModelUsage(callCtx, record)
	if callErr != nil {
		return nil, errorsx.BusinessError(2005, "模型连接测试失败")
	}
	return &response.TestModelProfileSlotResponse{
		StoreID: req.StoreID, UsageCode: resolved.Slot.UsageCode, ModelName: resolved.Slot.ModelName,
		TemplateRevision: resolved.Template.Revision, CredentialRevision: resolved.CredentialRevision,
		Status: "passed", LatencyMS: latency,
	}, nil
}

func (s *modelProfileTemplateService) buildResponse(template *models.ModelProfileTemplate) *response.ModelProfileTemplateResponse {
	ret := &response.ModelProfileTemplateResponse{
		ID: template.ID, Name: template.Name, Revision: template.Revision,
		GatewayBaseURL:               template.GatewayBaseURL,
		CustomerTagEvolutionEnabled:  template.CustomerTagEvolutionEnabled,
		CustomerTagEvolutionStoreIDs: decodeModelProfileStoreIDs(template.CustomerTagEvolutionStoreIDs),
		ReplyTagContextEnabled:       template.ReplyTagContextEnabled,
		Status:                       template.Status, UpdatedAt: template.UpdatedAt,
		Slots: []response.ModelProfileSlotResponse{},
	}
	for _, item := range repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), template.ID) {
		ret.Slots = append(ret.Slots, response.ModelProfileSlotResponse{
			ID: item.ID, UsageCode: item.UsageCode, DisplayName: item.DisplayName,
			ModelType: string(item.ModelType), Provider: item.Provider, ModelName: item.ModelName,
			APIMode: item.APIMode, Dimension: item.Dimension,
			MaxContextTokens: item.MaxContextTokens, MaxOutputTokens: item.MaxOutputTokens,
			TimeoutMS: item.TimeoutMS, MaxRetryCount: item.MaxRetryCount, Temperature: item.Temperature,
			SchemaVersion: item.SchemaVersion, PromptTemplate: item.PromptTemplate, JSONSchema: item.JSONSchema,
			Enabled: item.Enabled, SortNo: item.SortNo,
		})
	}
	return ret
}

func (s *modelProfileTemplateService) syncLegacyFastGPTTemplate(db *gorm.DB, template *models.ModelProfileTemplate, slots []models.ModelProfileSlot, operator *dto.AuthPrincipal) (bool, error) {
	legacy := repositories.FastGPTProfileTemplateRepository.Get(db)
	isNew := legacy == nil
	if legacy == nil {
		legacy = &models.FastGPTProfileTemplate{ID: 1, AuditFields: models.AuditFields{
			CreatedAt: time.Now(), CreateUserID: operator.UserID, CreateUserName: operator.Username,
		}}
	}
	previous := legacyFastGPTTemplateConfigOf(legacy)
	previousRevision := legacy.Revision
	legacy.Name = template.Name
	legacy.Status = fastGPTProfileTemplateStatusActive
	for _, slot := range slots {
		switch slot.UsageCode {
		case ModelProfileUsageReplyLLM:
			legacy.ChatProvider, legacy.ChatBaseURL, legacy.ChatModel, legacy.ChatAPIMode = slot.Provider, template.GatewayBaseURL, slot.ModelName, slot.APIMode
		case ModelProfileUsageASR:
			legacy.ASRProvider, legacy.ASRBaseURL, legacy.ASRModel = slot.Provider, template.GatewayBaseURL, slot.ModelName
		case ModelProfileUsageEmbedding:
			legacy.EmbeddingProvider, legacy.EmbeddingBaseURL, legacy.EmbeddingModel = slot.Provider, template.GatewayBaseURL, slot.ModelName
		case ModelProfileUsageDocumentParser:
			legacy.DocumentParserProvider, legacy.DocumentParserBaseURL, legacy.DocumentParserModel = slot.Provider, template.GatewayBaseURL, slot.ModelName
		case ModelProfileUsageVision:
			legacy.VisionProvider, legacy.VisionBaseURL, legacy.VisionModel = slot.Provider, template.GatewayBaseURL, slot.ModelName
		case ModelProfileUsageRerank:
			legacy.RerankProvider, legacy.RerankBaseURL, legacy.RerankModel = slot.Provider, template.GatewayBaseURL, slot.ModelName
		}
	}
	if !isNew && previous == legacyFastGPTTemplateConfigOf(legacy) {
		return false, nil
	}
	legacy.Revision = previousRevision + 1
	if legacy.Revision <= 0 {
		legacy.Revision = 1
	}
	legacy.UpdatedAt = time.Now()
	legacy.UpdateUserID = operator.UserID
	legacy.UpdateUserName = operator.Username
	if err := repositories.FastGPTProfileTemplateRepository.Save(db, legacy); err != nil {
		return false, err
	}
	return true, nil
}

type legacyFastGPTTemplateConfig struct {
	Name                   string
	Status                 string
	ChatProvider           string
	ChatBaseURL            string
	ChatModel              string
	ChatAPIMode            string
	ASRProvider            string
	ASRBaseURL             string
	ASRModel               string
	EmbeddingProvider      string
	EmbeddingBaseURL       string
	EmbeddingModel         string
	DocumentParserProvider string
	DocumentParserBaseURL  string
	DocumentParserModel    string
	VisionProvider         string
	VisionBaseURL          string
	VisionModel            string
	RerankProvider         string
	RerankBaseURL          string
	RerankModel            string
}

func legacyFastGPTTemplateConfigOf(template *models.FastGPTProfileTemplate) legacyFastGPTTemplateConfig {
	if template == nil {
		return legacyFastGPTTemplateConfig{}
	}
	return legacyFastGPTTemplateConfig{
		Name: template.Name, Status: template.Status,
		ChatProvider: template.ChatProvider, ChatBaseURL: template.ChatBaseURL,
		ChatModel: template.ChatModel, ChatAPIMode: template.ChatAPIMode,
		ASRProvider: template.ASRProvider, ASRBaseURL: template.ASRBaseURL, ASRModel: template.ASRModel,
		EmbeddingProvider: template.EmbeddingProvider, EmbeddingBaseURL: template.EmbeddingBaseURL,
		EmbeddingModel:         template.EmbeddingModel,
		DocumentParserProvider: template.DocumentParserProvider,
		DocumentParserBaseURL:  template.DocumentParserBaseURL,
		DocumentParserModel:    template.DocumentParserModel,
		VisionProvider:         template.VisionProvider, VisionBaseURL: template.VisionBaseURL, VisionModel: template.VisionModel,
		RerankProvider: template.RerankProvider, RerankBaseURL: template.RerankBaseURL, RerankModel: template.RerankModel,
	}
}

func requireModelProfileAdmin(operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if !slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) {
		return errorsx.Forbidden("仅超级管理员可以管理平台模型模板")
	}
	return nil
}

func normalizeModelProfileTemplateRequest(req *request.UpdateModelProfileTemplateRequest) {
	req.Name = strings.TrimSpace(req.Name)
	req.GatewayBaseURL = strings.TrimRight(strings.TrimSpace(req.GatewayBaseURL), "/")
	req.CustomerTagEvolutionStoreIDs = normalizeModelProfileStoreIDs(req.CustomerTagEvolutionStoreIDs)
	for index := range req.Slots {
		item := &req.Slots[index]
		item.UsageCode = strings.TrimSpace(item.UsageCode)
		item.DisplayName = strings.TrimSpace(item.DisplayName)
		item.ModelType = strings.TrimSpace(item.ModelType)
		item.Provider = strings.TrimSpace(item.Provider)
		item.ModelName = strings.TrimSpace(item.ModelName)
		item.APIMode = normalizeAIConfigAPIMode(item.APIMode)
		item.SchemaVersion = strings.TrimSpace(item.SchemaVersion)
		item.PromptTemplate = strings.TrimSpace(item.PromptTemplate)
		item.JSONSchema = strings.TrimSpace(item.JSONSchema)
		if item.TimeoutMS <= 0 {
			item.TimeoutMS = 30000
		}
		if item.SortNo <= 0 {
			item.SortNo = index + 1
		}
	}
}

func validateModelProfileTemplateRequest(req request.UpdateModelProfileTemplateRequest) error {
	if req.Name == "" {
		return errorsx.InvalidParam("请填写模型模板名称")
	}
	parsed, err := url.Parse(req.GatewayBaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errorsx.InvalidParam("统一模型网关地址格式不正确")
	}
	if len(req.Slots) == 0 {
		return errorsx.InvalidParam("至少保留一个模型槽")
	}
	if req.CustomerTagEvolutionEnabled && len(req.CustomerTagEvolutionStoreIDs) == 0 {
		return errorsx.InvalidParam("开启客户标签进化时必须至少选择一个灰度门店")
	}
	for _, storeID := range req.CustomerTagEvolutionStoreIDs {
		store := StoreService.Get(storeID)
		if store == nil || store.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("客户标签进化灰度门店不存在")
		}
	}
	seen := make(map[string]struct{}, len(req.Slots))
	hasCustomerTag := false
	for _, item := range req.Slots {
		if item.UsageCode == "" || item.DisplayName == "" || item.ModelType == "" || item.Provider == "" || item.ModelName == "" {
			return errorsx.InvalidParam("模型槽的用途、名称、类型、Provider 和模型名不能为空")
		}
		if _, exists := seen[item.UsageCode]; exists {
			return errorsx.InvalidParam("模型用途不能重复: " + item.UsageCode)
		}
		seen[item.UsageCode] = struct{}{}
		switch enums.AIModelType(item.ModelType) {
		case enums.AIModelTypeLLM, enums.AIModelTypeVision, enums.AIModelTypeASR, enums.AIModelTypeEmbedding, enums.AIModelTypeRerank:
		default:
			return errorsx.InvalidParam("模型类型不合法: " + item.ModelType)
		}
		if item.Temperature < 0 || item.Temperature > 2 {
			return errorsx.InvalidParam("Temperature 必须在 0 到 2 之间")
		}
		if item.UsageCode == ModelProfileUsageCustomerTag {
			hasCustomerTag = true
			if item.ModelType != string(enums.AIModelTypeLLM) {
				return errorsx.InvalidParam("客户标签模型必须是大语言模型")
			}
			if item.SchemaVersion != "customer_tag_evolution.v1" {
				return errorsx.InvalidParam("客户标签模型 Schema 版本必须为 customer_tag_evolution.v1")
			}
			var schema any
			if item.JSONSchema == "" || json.Unmarshal([]byte(item.JSONSchema), &schema) != nil {
				return errorsx.InvalidParam("客户标签模型 JSON Schema 不合法")
			}
		}
	}
	if !hasCustomerTag {
		return errorsx.InvalidParam("必须保留 customer_tag_llm 模型槽")
	}
	return nil
}

func normalizeModelProfileStoreIDs(values []int64) []int64 {
	ret := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	slices.Sort(ret)
	return ret
}

func encodeModelProfileStoreIDs(values []int64) string {
	raw, _ := json.Marshal(normalizeModelProfileStoreIDs(values))
	return string(raw)
}

func decodeModelProfileStoreIDs(raw string) []int64 {
	values := []int64{}
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &values) != nil {
		return []int64{}
	}
	return normalizeModelProfileStoreIDs(values)
}
