package services

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/ai/rag"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/newapi"
	"agent-desk/internal/pkg/securex"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
)

const (
	storeModelCredentialStatusUnconfigured = "unconfigured"
	storeModelCredentialStatusActive       = "active"
	storeModelCredentialStatusFailed       = "failed"
	storeModelCandidateStatusTesting       = "testing"
	storeModelCandidateStatusSyncing       = "syncing"
	storeModelCandidateStatusFailed        = "failed"
	storeModelSyncStatusReady              = "ready"
	storeModelSyncStatusFailed             = "failed"

	StoreAIModelSourceStoreCredential = "store_credential"
)

var StoreModelCredentialService = newStoreModelCredentialService()
var auxiliaryModelUsageSequence atomic.Uint64

type storeModelCredentialService struct{}

type ResolvedStoreModelCredential struct {
	CompanyID   int64
	StoreID     int64
	APIKey      string
	Revision    int64
	Fingerprint string
}

type credentialTestUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	UpstreamID       string
}

type credentialModelTest struct {
	code   string
	name   string
	config models.AIConfig
	call   func(context.Context, models.AIConfig) (*credentialTestUsage, error)
}

func newStoreModelCredentialService() *storeModelCredentialService {
	return &storeModelCredentialService{}
}

func init() {
	ai.ResolveConfigForContext = func(ctx context.Context, modelType enums.AIModelType) (*models.AIConfig, error) {
		usageCode := ""
		switch modelType {
		case enums.AIModelTypeLLM:
			usageCode = StoreAIModelUsageReplyLLM
		case enums.AIModelTypeVision:
			usageCode = StoreAIModelUsageMediaUnderstanding
		case enums.AIModelTypeASR:
			usageCode = StoreAIModelUsageASR
		case enums.AIModelTypeEmbedding:
			usageCode = StoreAIModelUsageEmbedding
		case enums.AIModelTypeRerank:
			usageCode = StoreAIModelUsageRerank
		default:
			return nil, errorsx.InvalidParam("模型类型不支持")
		}
		resolved, err := StoreAIModelSettingService.ResolveForContext(ctx, usageCode)
		if err != nil {
			return nil, err
		}
		return &resolved.Config, nil
	}
	ai.RecordModelUsageForContext = func(ctx context.Context, record ai.ModelUsageRecord) {
		scope := usagex.ScopeFromContext(ctx)
		if scope.StoreID > 0 && (scope.CredentialRevision <= 0 || strings.TrimSpace(scope.ModelSource) == "") {
			if credential, err := StoreModelCredentialService.ResolveCurrent(scope.StoreID); err == nil {
				if scope.CompanyID <= 0 {
					scope.CompanyID = credential.CompanyID
				}
				if scope.CredentialRevision <= 0 {
					scope.CredentialRevision = credential.Revision
				}
				if strings.TrimSpace(scope.ModelSource) == "" {
					scope.ModelSource = StoreAIModelSourceStoreCredential
				}
			}
		}
		requestID := strings.TrimSpace(scope.RequestID)
		eventKey := strings.TrimSpace(record.ExternalEventKey)
		if eventKey == "" && record.Receipt != nil && strings.TrimSpace(record.Receipt.RequestID) != "" {
			eventKey = fmt.Sprintf("model:%s:%s", record.Stage, strings.TrimSpace(record.Receipt.RequestID))
		}
		if eventKey == "" {
			eventKey = fmt.Sprintf("%s:%s:%s:%d", requestID, record.Stage, strings.TrimSpace(record.OperationType), auxiliaryModelUsageSequence.Add(1))
		}
		event := models.AIUsageEvent{
			EventKey: eventKey, CompanyID: scope.CompanyID, StoreID: scope.StoreID,
			WxWorkInstanceID: scope.WxWorkInstanceID, ConversationID: scope.ConversationID,
			MessageID: scope.MessageID, KnowledgeBaseID: scope.KnowledgeBaseID,
			RequestID: requestID, Stage: record.Stage, OperationType: record.OperationType,
			Provider: string(record.Config.Provider), Model: record.Config.ModelName,
			AIConfigID: record.Config.ID, ModelSource: scope.ModelSource,
			CredentialRevision: scope.CredentialRevision,
			PromptTokens:       record.PromptTokens, CompletionTokens: record.CompletionTokens,
			RequestCount: 1, LatencyMS: record.LatencyMS,
			MetricSource: AIUsageMetricSourceProviderOperation,
			Status:       firstNonBlank(strings.TrimSpace(record.Status), "completed"),
			ErrorMessage: strings.TrimSpace(record.ErrorClass),
		}
		if event.PromptTokens > 0 || event.CompletionTokens > 0 {
			event.MetricSource = AIUsageMetricSourceUpstreamActual
		}
		if record.Receipt != nil {
			event.Gateway = record.Receipt.Gateway
			event.GatewayRequestID = record.Receipt.RequestID
			event.GatewayUpstreamID = record.Receipt.UpstreamRequestID
			event.CallStartedAt = &record.Receipt.StartedAt
			event.CallFinishedAt = &record.Receipt.FinishedAt
			if record.Receipt.LatencyMS() > 0 {
				event.LatencyMS = record.Receipt.LatencyMS()
			}
		}
		_ = AIUsageEventService.Record(event)
	}
}

func (s *storeModelCredentialService) Get(req request.StoreModelCredentialRequest, operator *dto.AuthPrincipal) (*response.StoreModelCredentialResponse, error) {
	storeID, err := s.resolveOperatorStoreID(req.StoreID, operator)
	if err != nil {
		return nil, err
	}
	return s.buildResponse(storeID), nil
}

func (s *storeModelCredentialService) ListStores(operator *dto.AuthPrincipal) ([]response.StoreModelCredentialStoreOptionResponse, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	cnd := sqls.NewCnd().Where("status <> ?", enums.StatusDeleted).Asc("name").Asc("id")
	switch {
	case slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin):
	case slices.Contains(operator.Roles, constants.RoleCodeStoreStaff):
		scope := AgentTeamScopeService.Resolve(operator)
		if len(scope.StoreIDs) == 0 {
			return []response.StoreModelCredentialStoreOptionResponse{}, nil
		}
		cnd.In("id", scope.StoreIDs)
	default:
		return nil, errorsx.Forbidden("仅超级管理员或门店员工可以查看门店模型凭据")
	}
	stores := repositories.StoreRepository.Find(sqls.DB(), cnd)
	ret := make([]response.StoreModelCredentialStoreOptionResponse, 0, len(stores))
	for index := range stores {
		item := response.StoreModelCredentialStoreOptionResponse{
			StoreID: stores[index].ID, StoreCode: stores[index].StoreCode,
			StoreName: stores[index].Name, CompanyID: stores[index].CompanyID,
			CredentialStatus: storeModelCredentialStatusUnconfigured,
		}
		if credential := repositories.StoreModelCredentialRepository.GetByStoreID(sqls.DB(), stores[index].ID); credential != nil {
			item.HasKey = credential.Status == storeModelCredentialStatusActive &&
				credential.CredentialRevision > 0 && credential.KeyFingerprint != ""
			item.CredentialRevision = credential.CredentialRevision
			item.CredentialStatus = credential.Status
		}
		ret = append(ret, item)
	}
	return ret, nil
}

func (s *storeModelCredentialService) Update(ctx context.Context, req request.UpdateStoreModelCredentialRequest, operator *dto.AuthPrincipal) (*response.StoreModelCredentialUpdateResponse, error) {
	storeID, err := s.resolveOperatorStoreID(req.StoreID, operator)
	if err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return nil, errorsx.InvalidParam("请输入新的模型密钥")
	}
	store := StoreService.Get(storeID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	template := repositories.FastGPTProfileTemplateRepository.Get(sqls.DB())
	if err := validateStoreCredentialTemplate(template); err != nil {
		return nil, err
	}
	cipher, err := s.cipher()
	if err != nil {
		return nil, errorsx.BusinessError(5001, "门店模型密钥加密配置不可用")
	}

	current := repositories.StoreModelCredentialRepository.GetByStoreID(sqls.DB(), storeID)
	revision := int64(1)
	if current != nil && current.CredentialRevision >= revision {
		revision = current.CredentialRevision + 1
	}
	if current != nil && current.CandidateRevision >= revision {
		revision = current.CandidateRevision + 1
	}
	encryptedKey, nonce, err := cipher.Encrypt(apiKey, credentialAAD(storeID, revision))
	if err != nil {
		return nil, errorsx.BusinessError(5001, "门店模型密钥加密失败")
	}
	fingerprint := securex.Fingerprint(apiKey)
	now := time.Now()
	if current == nil {
		current = &models.StoreModelCredential{
			CompanyID:               store.CompanyID,
			StoreID:                 storeID,
			Status:                  storeModelCredentialStatusUnconfigured,
			CandidateEncryptedKey:   encryptedKey,
			CandidateKeyNonce:       nonce,
			CandidateKeyFingerprint: fingerprint,
			CandidateRevision:       revision,
			CandidateStatus:         storeModelCandidateStatusTesting,
			AuditFields: models.AuditFields{
				CreatedAt: now, UpdatedAt: now,
				CreateUserID: operator.UserID, CreateUserName: operator.Username,
				UpdateUserID: operator.UserID, UpdateUserName: operator.Username,
			},
		}
		if err := repositories.StoreModelCredentialRepository.Create(sqls.DB(), current); err != nil {
			return nil, err
		}
	} else if err := repositories.StoreModelCredentialRepository.Updates(sqls.DB(), current.ID, map[string]any{
		"company_id":                store.CompanyID,
		"candidate_encrypted_key":   encryptedKey,
		"candidate_key_nonce":       nonce,
		"candidate_key_fingerprint": fingerprint,
		"candidate_revision":        revision,
		"candidate_status":          storeModelCandidateStatusTesting,
		"last_error_class":          "",
		"updated_at":                now,
		"update_user_id":            operator.UserID,
		"update_user_name":          operator.Username,
	}); err != nil {
		return nil, err
	}

	testStartedAt := time.Now()
	if err := s.testCandidateModels(ctx, store, template, apiKey, revision); err != nil {
		s.markCandidateFailed(current.ID, "model_validation_failed")
		return nil, err
	}
	testedAt := time.Now()
	if err := repositories.StoreModelCredentialRepository.Updates(sqls.DB(), current.ID, map[string]any{
		"candidate_status":     storeModelCandidateStatusSyncing,
		"last_test_status":     "passed",
		"last_tested_at":       testedAt,
		"last_test_latency_ms": testedAt.Sub(testStartedAt).Milliseconds(),
		"last_error_class":     "",
		"updated_at":           testedAt,
		"update_user_id":       operator.UserID,
		"update_user_name":     operator.Username,
	}); err != nil {
		return nil, err
	}

	var oldCredential *ResolvedStoreModelCredential
	if current.CredentialRevision > 0 && strings.TrimSpace(current.EncryptedKey) != "" {
		oldCredential, _ = s.resolveRecord(current)
	}
	if err := s.syncFastGPTProfile(ctx, store, template, apiKey, revision, "profile_template_sync_test"); err != nil {
		s.markCandidateFailed(current.ID, "fastgpt_profile_sync_failed")
		return nil, errorsx.BusinessError(2005, "模型密钥验证通过，但 FastGPT Profile 同步失败，旧密钥仍在使用")
	}
	syncedAt := time.Now()
	activationErr := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		locked := &models.StoreModelCredential{}
		if err := tx.Tx.First(locked, "id = ?", current.ID).Error; err != nil {
			return err
		}
		if locked.CandidateRevision != revision || locked.CandidateKeyFingerprint != fingerprint {
			return fmt.Errorf("candidate credential changed during activation")
		}
		return repositories.StoreModelCredentialRepository.Updates(tx.Tx, locked.ID, map[string]any{
			"encrypted_key":             locked.CandidateEncryptedKey,
			"key_nonce":                 locked.CandidateKeyNonce,
			"key_fingerprint":           locked.CandidateKeyFingerprint,
			"credential_revision":       locked.CandidateRevision,
			"status":                    storeModelCredentialStatusActive,
			"candidate_encrypted_key":   "",
			"candidate_key_nonce":       "",
			"candidate_key_fingerprint": "",
			"candidate_revision":        0,
			"candidate_status":          "",
			"last_fast_gpt_sync_status": storeModelSyncStatusReady,
			"last_fast_gpt_synced_at":   syncedAt,
			"last_error_class":          "",
			"updated_at":                syncedAt,
			"update_user_id":            operator.UserID,
			"update_user_name":          operator.Username,
		})
	})
	if activationErr != nil {
		if oldCredential != nil {
			_ = s.syncFastGPTProfile(context.Background(), store, template, oldCredential.APIKey, oldCredential.Revision, "credential_restore")
		}
		s.markCandidateFailed(current.ID, "database_activation_failed")
		return nil, errorsx.BusinessError(5001, "模型密钥切换失败，旧密钥仍在使用")
	}

	WsService.PublishStoreModelCredentialChanged(storeID, revision, storeModelCredentialStatusActive, syncedAt)
	metadata := s.buildResponse(storeID)
	return &response.StoreModelCredentialUpdateResponse{
		StoreModelCredentialResponse: *metadata,
		ChangedAt:                    syncedAt,
	}, nil
}

func (s *storeModelCredentialService) ResolveCurrent(storeID int64) (*ResolvedStoreModelCredential, error) {
	if storeID <= 0 {
		return nil, errorsx.InvalidParam("门店不能为空")
	}
	if sqls.DB() == nil || !sqls.DB().Migrator().HasTable(&models.StoreModelCredential{}) {
		return nil, errorsx.BusinessError(2005, "当前门店尚未配置可用的模型密钥")
	}
	item := repositories.StoreModelCredentialRepository.GetByStoreID(sqls.DB(), storeID)
	if item == nil || item.Status != storeModelCredentialStatusActive || item.CredentialRevision <= 0 {
		return nil, errorsx.BusinessError(2005, "当前门店尚未配置可用的模型密钥")
	}
	return s.resolveRecord(item)
}

func (s *storeModelCredentialService) ResolveConfig(storeID int64, modelType enums.AIModelType) (*models.AIConfig, int64, error) {
	credential, err := s.ResolveCurrent(storeID)
	if err != nil {
		return nil, 0, err
	}
	template := repositories.FastGPTProfileTemplateRepository.Get(sqls.DB())
	if err := validateStoreCredentialTemplate(template); err != nil {
		return nil, 0, err
	}
	config, err := buildStoreTemplateAIConfig(template, credential.APIKey, modelType)
	if err != nil {
		return nil, 0, err
	}
	return &config, credential.Revision, nil
}

func (s *storeModelCredentialService) QueryBilling(ctx context.Context, req request.BillingQueryRequest, operator *dto.AuthPrincipal) (*response.BillingQueryResponse, error) {
	storeID, err := s.resolveOperatorStoreID(req.StoreID, operator)
	if err != nil {
		return nil, err
	}
	dateRange, err := parseBillingDateRange(req.StartDate, req.EndDate)
	if err != nil {
		return nil, err
	}
	credential, err := s.ResolveCurrent(storeID)
	if err != nil {
		return nil, err
	}
	template := repositories.FastGPTProfileTemplateRepository.Get(sqls.DB())
	if template == nil {
		return nil, errorsx.BusinessError(2005, "模型模板尚未配置")
	}
	baseURL := strings.TrimSpace(config.Current().NewAPIUsage.BaseURL)
	if baseURL == "" {
		baseURL = template.ChatBaseURL
	}
	timeout := time.Duration(config.Current().NewAPIUsage.TimeoutMS) * time.Millisecond
	client, err := newapi.NewTokenClient(baseURL, credential.APIKey, timeout)
	if err != nil {
		return nil, errorsx.BusinessError(2005, "计费查询服务尚未配置")
	}
	billingSettings, err := client.GetBillingSettings(ctx)
	if err != nil {
		return nil, errorsx.BusinessError(2005, "无法读取 New API 人民币计费配置")
	}
	summary, err := client.GetUsageSummary(ctx)
	if err != nil {
		return nil, errorsx.BusinessError(2005, "当前门店计费凭据无效或查询失败")
	}
	endTimestamp := dateRange.EndExclusiveTimestamp
	if endTimestamp > 0 {
		endTimestamp--
	}
	logs, err := client.ListUsageLogs(ctx, dateRange.StartTimestamp, endTimestamp)
	if err != nil {
		return nil, errorsx.BusinessError(2005, "当前门店调用明细查询失败")
	}
	store := StoreService.Get(storeID)
	ret := &response.BillingQueryResponse{
		StoreID: storeID, CredentialRevision: credential.Revision,
		CredentialStatus: storeModelCredentialStatusActive,
		StartDate:        dateRange.StartDate, EndDate: dateRange.EndDate,
		QueriedAt: time.Now(),
		Summary: response.BillingTokenSummaryResponse{
			Name: summary.Name, UnlimitedQuota: summary.UnlimitedQuota,
			TotalGranted: summary.TotalGranted, TotalUsed: summary.TotalUsed, TotalAvailable: summary.TotalAvailable,
			GrantedCNY:   quotaCNY(summary.TotalGranted, billingSettings),
			UsedCNY:      quotaCNY(summary.TotalUsed, billingSettings),
			AvailableCNY: quotaCNY(summary.TotalAvailable, billingSettings),
			ExpiresAt:    summary.ExpiresAt,
		},
		Logs: make([]response.BillingUsageLogResponse, 0, len(logs)),
	}
	if store != nil {
		ret.StoreName = store.Name
	}
	for _, item := range logs {
		if item.Type != 0 && item.Type != 2 {
			continue
		}
		if !dateRange.Contains(item.CreatedAt) {
			continue
		}
		ret.Logs = append(ret.Logs, response.BillingUsageLogResponse{
			ID: item.ID, CreatedAt: item.CreatedAt, ModelName: item.ModelName,
			PromptTokens: item.PromptTokens, CompletionTokens: item.CompletionTokens,
			UseTime: item.UseTime, Quota: item.Quota,
			CostCNY:   quotaCNY(item.Quota, billingSettings),
			RequestID: strings.TrimSpace(item.RequestID),
		})
		ret.PeriodQuota += item.Quota
		ret.PeriodPromptTokens += item.PromptTokens
		ret.PeriodOutputTokens += item.CompletionTokens
	}
	ret.PeriodCostCNY = quotaCNY(ret.PeriodQuota, billingSettings)
	return ret, nil
}

type billingDateRange struct {
	StartDate             string
	EndDate               string
	StartTimestamp        int64
	EndExclusiveTimestamp int64
}

func (r billingDateRange) Contains(timestamp int64) bool {
	if r.StartTimestamp > 0 && timestamp < r.StartTimestamp {
		return false
	}
	return r.EndExclusiveTimestamp <= 0 || timestamp < r.EndExclusiveTimestamp
}

func parseBillingDateRange(startDate string, endDate string) (billingDateRange, error) {
	startDate = strings.TrimSpace(startDate)
	endDate = strings.TrimSpace(endDate)
	if startDate == "" && endDate == "" {
		return billingDateRange{}, nil
	}
	if startDate == "" || endDate == "" {
		return billingDateRange{}, errorsx.InvalidParam("开始日期和结束日期必须同时填写")
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	start, err := time.ParseInLocation("2006-01-02", startDate, location)
	if err != nil {
		return billingDateRange{}, errorsx.InvalidParam("开始日期格式不正确")
	}
	end, err := time.ParseInLocation("2006-01-02", endDate, location)
	if err != nil {
		return billingDateRange{}, errorsx.InvalidParam("结束日期格式不正确")
	}
	if end.Before(start) {
		return billingDateRange{}, errorsx.InvalidParam("结束日期不能早于开始日期")
	}
	if end.Sub(start) > 366*24*time.Hour {
		return billingDateRange{}, errorsx.InvalidParam("单次查询日期范围不能超过 366 天")
	}
	return billingDateRange{
		StartDate: startDate, EndDate: endDate,
		StartTimestamp: start.Unix(), EndExclusiveTimestamp: end.AddDate(0, 0, 1).Unix(),
	}, nil
}

func (s *storeModelCredentialService) resolveRecord(item *models.StoreModelCredential) (*ResolvedStoreModelCredential, error) {
	cipher, err := s.cipher()
	if err != nil {
		return nil, errorsx.BusinessError(5001, "门店模型密钥加密配置不可用")
	}
	apiKey, err := cipher.Decrypt(item.EncryptedKey, item.KeyNonce, credentialAAD(item.StoreID, item.CredentialRevision))
	if err != nil {
		return nil, errorsx.BusinessError(5001, "门店模型密钥解密失败")
	}
	return &ResolvedStoreModelCredential{
		CompanyID: item.CompanyID, StoreID: item.StoreID, APIKey: apiKey,
		Revision: item.CredentialRevision, Fingerprint: item.KeyFingerprint,
	}, nil
}

func (s *storeModelCredentialService) cipher() (*securex.AESGCM, error) {
	return securex.NewAESGCM(config.Current().StoreCredential.MasterKey)
}

func (s *storeModelCredentialService) resolveOperatorStoreID(requestedStoreID int64, operator *dto.AuthPrincipal) (int64, error) {
	if operator == nil {
		return 0, errorsx.Unauthorized("未登录或登录已过期")
	}
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) {
		if requestedStoreID <= 0 {
			return 0, errorsx.InvalidParam("请选择门店")
		}
		return requestedStoreID, nil
	}
	if !slices.Contains(operator.Roles, constants.RoleCodeStoreStaff) {
		return 0, errorsx.Forbidden("仅超级管理员或门店员工可以操作门店模型密钥")
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if requestedStoreID > 0 && slices.Contains(scope.StoreIDs, requestedStoreID) {
		return requestedStoreID, nil
	}
	if requestedStoreID <= 0 && len(scope.StoreIDs) == 1 {
		return scope.StoreIDs[0], nil
	}
	return 0, errorsx.Forbidden("无权操作该门店")
}

func (s *storeModelCredentialService) buildResponse(storeID int64) *response.StoreModelCredentialResponse {
	template := repositories.FastGPTProfileTemplateRepository.Get(sqls.DB())
	credential := repositories.StoreModelCredentialRepository.GetByStoreID(sqls.DB(), storeID)
	ret := &response.StoreModelCredentialResponse{
		StoreID:     storeID,
		ProfileName: "门店模型模板", ProfileStatus: "unconfigured",
		CredentialStatus: storeModelCredentialStatusUnconfigured,
	}
	if store := StoreService.Get(storeID); store != nil {
		ret.StoreName = store.Name
	}
	if template != nil {
		ret.ProfileName = template.Name
		ret.ProfileRevision = template.Revision
		ret.ProfileStatus = template.Status
	}
	if tenant := repositories.FastGPTStoreTenantRepository.GetByStoreID(sqls.DB(), storeID); tenant != nil {
		ret.ProfileStatus = firstNonBlank(tenant.ProfileTemplateSyncStatus, ret.ProfileStatus)
	}
	if credential != nil {
		ret.HasKey = credential.Status == storeModelCredentialStatusActive && credential.CredentialRevision > 0 && credential.KeyFingerprint != ""
		ret.CredentialRevision = credential.CredentialRevision
		ret.CredentialStatus = credential.Status
		ret.LastTestStatus = credential.LastTestStatus
		ret.LastTestedAt = credential.LastTestedAt
		ret.LastTestLatencyMS = credential.LastTestLatencyMS
		ret.FastGPTSyncStatus = credential.LastFastGPTSyncStatus
		ret.FastGPTLastSyncedAt = credential.LastFastGPTSyncedAt
	}
	return ret
}

func (s *storeModelCredentialService) testCandidateModels(ctx context.Context, store *models.Store, template *models.FastGPTProfileTemplate, apiKey string, revision int64) error {
	tests := candidateCredentialTests(template, apiKey)
	for _, item := range tests {
		requestID := "credential-test-" + strings.ReplaceAll(uuid.NewString(), "-", "")
		callCtx := tracex.ContextWithRequestID(ctx, requestID)
		callCtx, capture := usagex.WithCapture(callCtx)
		callCtx = ai.WithoutModelUsageRecording(callCtx)
		startedAt := time.Now()
		usage, callErr := item.call(callCtx, item.config)
		s.recordCredentialTestUsage(store, revision, item.code, item.config, requestID, usage, lastUsageReceipt(capture), time.Since(startedAt).Milliseconds(), callErr)
		if callErr != nil {
			return errorsx.BusinessError(2005, item.name+"连接验证失败，旧密钥仍在使用")
		}
	}
	return nil
}

func candidateCredentialTests(template *models.FastGPTProfileTemplate, apiKey string) []credentialModelTest {
	return []credentialModelTest{
		{code: "chat", name: "对话模型", config: mustBuildStoreTemplateAIConfig(template, apiKey, enums.AIModelTypeLLM), call: testChatCredential},
		{code: "vision", name: "视觉理解模型", config: mustBuildStoreTemplateAIConfig(template, apiKey, enums.AIModelTypeVision), call: testVisionCredential},
		{code: "embedding", name: "向量模型", config: mustBuildStoreTemplateAIConfig(template, apiKey, enums.AIModelTypeEmbedding), call: testEmbeddingCredential},
		{code: "rerank", name: "重排模型", config: mustBuildStoreTemplateAIConfig(template, apiKey, enums.AIModelTypeRerank), call: testRerankCredential},
		{code: "document_parser", name: "文档理解模型", config: buildDocumentParserAIConfig(template, apiKey), call: testChatCredential},
	}
}

func (s *storeModelCredentialService) recordCredentialTestUsage(store *models.Store, revision int64, slot string, aiConfig models.AIConfig, requestID string, usage *credentialTestUsage, receipt *usagex.Receipt, latencyMS int64, callErr error) {
	event := models.AIUsageEvent{
		EventKey: "model_connection_test:" + requestID, RequestID: requestID,
		Stage: "model_connection_test", OperationType: slot,
		Provider: string(aiConfig.Provider), Model: aiConfig.ModelName,
		ModelSource: "store_credential_candidate", CredentialRevision: revision,
		RequestCount: 1, MetricSource: AIUsageMetricSourceProviderOperation,
		LatencyMS: latencyMS, Status: "completed",
	}
	if store != nil {
		event.CompanyID = store.CompanyID
		event.StoreID = store.ID
	}
	if callErr != nil {
		event.Status = "failed"
		event.ErrorMessage = "model_validation_failed"
	}
	if usage != nil {
		event.PromptTokens = usage.PromptTokens
		event.CompletionTokens = usage.CompletionTokens
		event.UpstreamRequestID = usage.UpstreamID
		if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
			event.MetricSource = AIUsageMetricSourceUpstreamActual
		}
	}
	if receipt != nil {
		event.Gateway = receipt.Gateway
		event.GatewayRequestID = receipt.RequestID
		event.GatewayUpstreamID = receipt.UpstreamRequestID
		event.CallStartedAt = &receipt.StartedAt
		event.CallFinishedAt = &receipt.FinishedAt
	}
	_ = AIUsageEventService.Record(event)
}

func (s *storeModelCredentialService) syncFastGPTProfile(ctx context.Context, store *models.Store, template *models.FastGPTProfileTemplate, apiKey string, revision int64, stage string) error {
	if store == nil {
		return fmt.Errorf("store is required")
	}
	knowledgeBases := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("store_id", store.ID).
		Eq("connection_id", "agentdesk_integration").
		Eq("status", enums.StatusOk).
		Asc("id"))
	if len(knowledgeBases) == 0 {
		return fmt.Errorf("managed dataset is unavailable")
	}
	connector, err := NewManagedStoreFastGPTConnector()
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	profile, err := connector.ForStore(store.ID).GetModelProfile(callCtx, knowledgeBases[0].DatasetID)
	if err != nil {
		return err
	}
	input := buildFastGPTTemplateProfileInput(template, knowledgeBases[0].DatasetID, profile)
	input.Embedding.APIKey = apiKey
	input.DocumentParser.APIKey = apiKey
	input.Vision.APIKey = apiKey
	if input.Rerank != nil {
		input.Rerank.APIKey = apiKey
	}
	testResult, err := connector.ForStore(store.ID).TestModelProfile(callCtx, input)
	if err != nil {
		return err
	}
	for index, item := range testResult.Results {
		_ = AIUsageEventService.Record(models.AIUsageEvent{
			EventKey:  fmt.Sprintf("%s:%d:%d:%s:%d", stage, store.ID, revision, item.Stage, index),
			CompanyID: store.CompanyID, StoreID: store.ID,
			Stage: stage, OperationType: item.Stage, ModelSource: "fastgpt_store_profile",
			CredentialRevision: revision, RequestCount: 1,
			PromptTokens: item.PromptTokens, CompletionTokens: item.CompletionTokens,
			MetricSource: AIUsageMetricSourceUpstreamActual, Status: item.Status,
		})
		if !strings.EqualFold(strings.TrimSpace(item.Status), "success") && !strings.EqualFold(strings.TrimSpace(item.Status), "passed") {
			return fmt.Errorf("fastgpt profile stage failed")
		}
	}
	input.TestToken = testResult.TestToken
	result, err := connector.ForStore(store.ID).UpsertModelProfile(callCtx, input)
	if err != nil {
		return err
	}
	systemOperator := &dto.AuthPrincipal{Username: "store_model_credential"}
	if err := FastGPTDatasetService.syncStoreModelProfileSnapshot(store.ID, &result.Profile, systemOperator); err != nil {
		return err
	}
	if tenant := repositories.FastGPTStoreTenantRepository.GetByStoreID(sqls.DB(), store.ID); tenant != nil {
		FastGPTProfileTemplateService.markReady(tenant, template.Revision)
	}
	return nil
}

func (s *storeModelCredentialService) markCandidateFailed(id int64, errorClass string) {
	now := time.Now()
	_ = repositories.StoreModelCredentialRepository.Updates(sqls.DB(), id, map[string]any{
		"candidate_status":          storeModelCandidateStatusFailed,
		"last_test_status":          storeModelCredentialStatusFailed,
		"last_fast_gpt_sync_status": storeModelSyncStatusFailed,
		"last_error_class":          errorClass,
		"updated_at":                now,
		"update_user_name":          "store_model_credential",
	})
}

func validateStoreCredentialTemplate(template *models.FastGPTProfileTemplate) error {
	if template == nil || template.Revision <= 0 || template.Status != fastGPTProfileTemplateStatusActive {
		return errorsx.BusinessError(2005, "门店模型模板尚未配置")
	}
	for _, value := range []string{
		template.ChatProvider, template.ChatBaseURL, template.ChatModel,
		template.VisionProvider, template.VisionBaseURL, template.VisionModel,
		template.ASRProvider, template.ASRBaseURL, template.ASRModel,
		template.EmbeddingProvider, template.EmbeddingBaseURL, template.EmbeddingModel,
		template.RerankProvider, template.RerankBaseURL, template.RerankModel,
		template.DocumentParserProvider, template.DocumentParserBaseURL, template.DocumentParserModel,
	} {
		if strings.TrimSpace(value) == "" {
			return errorsx.BusinessError(2005, "门店模型模板配置不完整")
		}
	}
	if !modelGatewayBaseURLsMatch(
		template.ChatBaseURL,
		template.VisionBaseURL,
		template.ASRBaseURL,
		template.EmbeddingBaseURL,
		template.RerankBaseURL,
		template.DocumentParserBaseURL,
	) {
		return errorsx.BusinessError(2005, "门店模型模板必须统一使用同一个模型网关")
	}
	return nil
}

func buildStoreTemplateAIConfig(template *models.FastGPTProfileTemplate, apiKey string, modelType enums.AIModelType) (models.AIConfig, error) {
	config := models.AIConfig{
		APIKey: apiKey, ModelType: modelType, Status: enums.StatusOk,
		TimeoutMS: 30000, MaxRetryCount: 0,
	}
	switch modelType {
	case enums.AIModelTypeLLM:
		config.Name = "门店对话模型"
		config.Provider = enums.AIProvider(template.ChatProvider)
		config.BaseURL = template.ChatBaseURL
		config.ModelName = template.ChatModel
		config.APIMode = normalizeAIConfigAPIMode(template.ChatAPIMode)
		config.MaxOutputTokens = 1024
	case enums.AIModelTypeVision:
		config.Name = "门店视觉理解模型"
		config.Provider = enums.AIProvider(template.VisionProvider)
		config.BaseURL = template.VisionBaseURL
		config.ModelName = template.VisionModel
		config.APIMode = AIConfigAPIModeChatCompletions
		config.MaxOutputTokens = 256
	case enums.AIModelTypeASR:
		config.Name = "门店语音识别模型"
		config.Provider = enums.AIProvider(template.ASRProvider)
		config.BaseURL = template.ASRBaseURL
		config.ModelName = template.ASRModel
	case enums.AIModelTypeEmbedding:
		config.Name = "门店向量模型"
		config.Provider = enums.AIProvider(template.EmbeddingProvider)
		config.BaseURL = template.EmbeddingBaseURL
		config.ModelName = template.EmbeddingModel
	case enums.AIModelTypeRerank:
		config.Name = "门店重排模型"
		config.Provider = enums.AIProvider(template.RerankProvider)
		config.BaseURL = template.RerankBaseURL
		config.ModelName = template.RerankModel
	default:
		return models.AIConfig{}, errorsx.InvalidParam("模型类型不支持门店统一凭据")
	}
	return config, nil
}

func mustBuildStoreTemplateAIConfig(template *models.FastGPTProfileTemplate, apiKey string, modelType enums.AIModelType) models.AIConfig {
	config, _ := buildStoreTemplateAIConfig(template, apiKey, modelType)
	return config
}

func buildDocumentParserAIConfig(template *models.FastGPTProfileTemplate, apiKey string) models.AIConfig {
	return models.AIConfig{
		Name: "门店文档理解模型", Provider: enums.AIProvider(template.DocumentParserProvider),
		BaseURL: template.DocumentParserBaseURL, APIKey: apiKey,
		APIMode: AIConfigAPIModeChatCompletions, ModelType: enums.AIModelTypeLLM,
		ModelName: template.DocumentParserModel, MaxOutputTokens: 32,
		TimeoutMS: 30000, Status: enums.StatusOk,
	}
}

func testChatCredential(ctx context.Context, config models.AIConfig) (*credentialTestUsage, error) {
	config.MaxOutputTokens = 16
	result, err := ai.LLM.ChatWithConfig(ctx, config, "只回复 OK。", "回复 OK")
	if err != nil {
		return nil, err
	}
	return &credentialTestUsage{
		PromptTokens: int64(result.PromptTokens), CompletionTokens: int64(result.CompletionTokens),
	}, nil
}

func testVisionCredential(ctx context.Context, config models.AIConfig) (*credentialTestUsage, error) {
	_, usage, err := MediaUnderstandingService.callOpenAICompatibleVisionWithUsage(ctx, config, visionConnectionTestImage)
	if usage == nil {
		return nil, err
	}
	return &credentialTestUsage{
		PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, UpstreamID: usage.RequestID,
	}, err
}

func testASRCredential(ctx context.Context, config models.AIConfig) (*credentialTestUsage, error) {
	_, usage, err := MediaUnderstandingService.callOpenAICompatibleASRWithUsage(ctx, config, "credential-test.wav", silentPCM16WAV())
	if err != nil && !strings.Contains(err.Error(), "没有 text 字段") {
		return nil, err
	}
	ret := &credentialTestUsage{}
	if usage != nil {
		ret.PromptTokens = usage.PromptTokens
		ret.CompletionTokens = usage.CompletionTokens
		ret.UpstreamID = usage.RequestID
	}
	return ret, nil
}

func testEmbeddingCredential(ctx context.Context, config models.AIConfig) (*credentialTestUsage, error) {
	result, err := ai.Embedding.GenerateEmbeddingWithConfig(ctx, config, "酒店前台")
	if err != nil {
		return nil, err
	}
	return &credentialTestUsage{PromptTokens: int64(result.TokensUsed)}, nil
}

func testRerankCredential(ctx context.Context, config models.AIConfig) (*credentialTestUsage, error) {
	result, usage, err := rag.Rerank.RerankWithConfigAndUsage(ctx, config, "早餐时间", []string{"早餐时间是 7 点", "停车场在负一层"}, 1)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("rerank returned no result")
	}
	ret := &credentialTestUsage{}
	if usage != nil {
		ret.PromptTokens = usage.PromptTokens
		ret.CompletionTokens = usage.CompletionTokens
	}
	return ret, nil
}

func silentPCM16WAV() []byte {
	const sampleRate = 16000
	const samples = sampleRate / 4
	dataSize := samples * 2
	buf := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}

func credentialAAD(storeID int64, revision int64) []byte {
	return []byte(fmt.Sprintf("store:%d:revision:%d", storeID, revision))
}

func quotaCNY(quota int64, settings *newapi.TokenBillingSettings) float64 {
	if settings == nil || settings.QuotaPerUnit <= 0 || settings.USDExchangeRate <= 0 {
		return 0
	}
	return float64(quota) / settings.QuotaPerUnit * settings.USDExchangeRate
}
