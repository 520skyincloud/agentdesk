package services

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	fastGPTProfileTemplateStatusActive = "active"
	fastGPTTemplateSyncPending         = "pending"
	fastGPTTemplateSyncSyncing         = "syncing"
	fastGPTTemplateSyncReady           = "ready"
	fastGPTTemplateSyncFailed          = "failed"
	fastGPTTemplateSyncBlocked         = "blocked"
)

var FastGPTProfileTemplateService = newFastGPTProfileTemplateService()

type fastGPTProfileTemplateService struct{}

func newFastGPTProfileTemplateService() *fastGPTProfileTemplateService {
	return &fastGPTProfileTemplateService{}
}

func (s *fastGPTProfileTemplateService) Get(ctx context.Context, operator *dto.AuthPrincipal) (*response.FastGPTProfileTemplateResponse, error) {
	if err := requireFastGPTProfileTemplateAdmin(operator); err != nil {
		return nil, err
	}
	template := repositories.FastGPTProfileTemplateRepository.Get(sqls.DB())
	if template == nil {
		template = s.draftFromExistingProfile(ctx)
	}
	return s.buildResponse(template), nil
}

func (s *fastGPTProfileTemplateService) Update(ctx context.Context, req request.UpdateFastGPTProfileTemplateRequest, operator *dto.AuthPrincipal) (*response.FastGPTProfileTemplateResponse, error) {
	if err := requireFastGPTProfileTemplateAdmin(operator); err != nil {
		return nil, err
	}
	normalizeFastGPTProfileTemplateRequest(&req)
	if err := validateFastGPTProfileTemplateRequest(req); err != nil {
		return nil, err
	}
	current := repositories.FastGPTProfileTemplateRepository.Get(sqls.DB())
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
	template := &models.FastGPTProfileTemplate{
		ID:                     1,
		Name:                   req.Name,
		Revision:               revision,
		ChatProvider:           req.Chat.Provider,
		ChatBaseURL:            req.Chat.BaseURL,
		ChatModel:              req.Chat.Model,
		ChatAPIMode:            normalizeAIConfigAPIMode(req.Chat.APIMode),
		ASRProvider:            req.ASR.Provider,
		ASRBaseURL:             req.ASR.BaseURL,
		ASRModel:               req.ASR.Model,
		EmbeddingProvider:      req.Embedding.Provider,
		EmbeddingBaseURL:       req.Embedding.BaseURL,
		EmbeddingModel:         req.Embedding.Model,
		DocumentParserProvider: req.DocumentParser.Provider,
		DocumentParserBaseURL:  req.DocumentParser.BaseURL,
		DocumentParserModel:    req.DocumentParser.Model,
		VisionProvider:         req.Vision.Provider,
		VisionBaseURL:          req.Vision.BaseURL,
		VisionModel:            req.Vision.Model,
		RerankProvider:         req.Rerank.Provider,
		RerankBaseURL:          req.Rerank.BaseURL,
		RerankModel:            req.Rerank.Model,
		Status:                 fastGPTProfileTemplateStatusActive,
		AuditFields: models.AuditFields{
			CreatedAt:      createdAt,
			CreateUserID:   createUserID,
			CreateUserName: createUserName,
			UpdatedAt:      now,
			UpdateUserID:   operator.UserID,
			UpdateUserName: operator.Username,
		},
	}
	storeIDs := s.managedStoreIDs()
	if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := repositories.FastGPTProfileTemplateRepository.Save(tx.Tx, template); err != nil {
			return err
		}
		return repositories.FastGPTStoreTenantRepository.QueueTemplateSync(tx.Tx, storeIDs, template.Revision, now)
	}); err != nil {
		return nil, err
	}
	s.publishStoreProfileStatus(storeIDs, template.Revision, fastGPTTemplateSyncPending, now)
	return s.buildResponse(template), nil
}

func (s *fastGPTProfileTemplateService) QueueAll(operator *dto.AuthPrincipal) (*response.FastGPTProfileTemplateResponse, error) {
	if err := requireFastGPTProfileTemplateAdmin(operator); err != nil {
		return nil, err
	}
	template := repositories.FastGPTProfileTemplateRepository.Get(sqls.DB())
	if template == nil || template.Revision <= 0 {
		return nil, errorsx.InvalidParam("请先保存知识库 Profile 模板")
	}
	now := time.Now()
	storeIDs := s.managedStoreIDs()
	if err := repositories.FastGPTStoreTenantRepository.QueueTemplateSync(sqls.DB(), storeIDs, template.Revision, now); err != nil {
		return nil, err
	}
	s.publishStoreProfileStatus(storeIDs, template.Revision, fastGPTTemplateSyncPending, now)
	return s.buildResponse(template), nil
}

func (s *fastGPTProfileTemplateService) QueueStore(storeID int64) {
	if storeID <= 0 {
		return
	}
	template := repositories.FastGPTProfileTemplateRepository.Get(sqls.DB())
	if template == nil || template.Revision <= 0 {
		return
	}
	_ = repositories.FastGPTStoreTenantRepository.QueueTemplateSync(sqls.DB(), []int64{storeID}, template.Revision, time.Now())
}

func (s *fastGPTProfileTemplateService) ProcessDue(limit int) int {
	template := repositories.FastGPTProfileTemplateRepository.Get(sqls.DB())
	if template == nil || template.Revision <= 0 || template.Status != fastGPTProfileTemplateStatusActive {
		return 0
	}
	items := repositories.FastGPTStoreTenantRepository.FindTemplateSyncDue(sqls.DB(), time.Now(), limit)
	processed := 0
	for index := range items {
		s.processTenant(template, &items[index])
		processed++
	}
	return processed
}

func (s *fastGPTProfileTemplateService) processTenant(template *models.FastGPTProfileTemplate, tenant *models.FastGPTStoreTenant) {
	if template == nil || tenant == nil {
		return
	}
	now := time.Now()
	_ = repositories.FastGPTStoreTenantRepository.UpdateTemplateSync(sqls.DB(), tenant.ID, map[string]any{
		"profile_template_sync_status":   fastGPTTemplateSyncSyncing,
		"profile_template_next_retry_at": now.Add(2 * time.Minute),
		"updated_at":                     now,
		"update_user_name":               "fastgpt_profile_template",
	})
	WsService.PublishStoreModelProfileChanged(tenant.StoreID, template.Revision, fastGPTTemplateSyncSyncing, now)
	store := StoreService.Get(tenant.StoreID)
	if store == nil {
		s.markBlocked(tenant, "store_unavailable")
		return
	}
	credential, err := StoreModelCredentialService.ResolveCurrent(tenant.StoreID)
	if err != nil {
		s.markBlocked(tenant, "store_credential_unconfigured")
		return
	}
	if err := StoreModelCredentialService.syncFastGPTProfile(
		context.Background(),
		store,
		template,
		credential.APIKey,
		credential.Revision,
		"profile_template_sync_test",
	); err != nil {
		s.markFailed(tenant, err)
		return
	}
	s.markReady(tenant, template.Revision)
}

func (s *fastGPTProfileTemplateService) MarkStoreReadyIfMatching(storeID int64, profile *FastGPTModelProfile) {
	template := repositories.FastGPTProfileTemplateRepository.Get(sqls.DB())
	tenant := repositories.FastGPTStoreTenantRepository.GetByStoreID(sqls.DB(), storeID)
	if template == nil || tenant == nil || !profileMatchesTemplate(profile, template) {
		return
	}
	s.markReady(tenant, template.Revision)
}

func (s *fastGPTProfileTemplateService) markReady(tenant *models.FastGPTStoreTenant, revision int64) {
	now := time.Now()
	_ = repositories.FastGPTStoreTenantRepository.UpdateTemplateSync(sqls.DB(), tenant.ID, map[string]any{
		"profile_template_revision":        revision,
		"profile_template_target_revision": revision,
		"profile_template_sync_status":     fastGPTTemplateSyncReady,
		"profile_template_attempt_count":   0,
		"profile_template_next_retry_at":   nil,
		"profile_template_synced_at":       now,
		"profile_template_last_error":      "",
		"updated_at":                       now,
		"update_user_name":                 "fastgpt_profile_template",
	})
	WsService.PublishStoreModelProfileChanged(tenant.StoreID, revision, fastGPTTemplateSyncReady, now)
}

func (s *fastGPTProfileTemplateService) markBlocked(tenant *models.FastGPTStoreTenant, errorClass string) {
	now := time.Now()
	_ = repositories.FastGPTStoreTenantRepository.UpdateTemplateSync(sqls.DB(), tenant.ID, map[string]any{
		"profile_template_sync_status":   fastGPTTemplateSyncBlocked,
		"profile_template_next_retry_at": nil,
		"profile_template_last_error":    errorClass,
		"updated_at":                     now,
		"update_user_name":               "fastgpt_profile_template",
	})
	WsService.PublishStoreModelProfileChanged(tenant.StoreID, tenant.ProfileTemplateTargetRevision, fastGPTTemplateSyncBlocked, now)
}

func (s *fastGPTProfileTemplateService) markFailed(tenant *models.FastGPTStoreTenant, syncErr error) {
	attemptCount := tenant.ProfileTemplateAttemptCount + 1
	delay := time.Duration(attemptCount) * 30 * time.Second
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	now := time.Now()
	_ = repositories.FastGPTStoreTenantRepository.UpdateTemplateSync(sqls.DB(), tenant.ID, map[string]any{
		"profile_template_sync_status":   fastGPTTemplateSyncFailed,
		"profile_template_attempt_count": attemptCount,
		"profile_template_next_retry_at": now.Add(delay),
		"profile_template_last_error":    fastGPTErrorClass(syncErr),
		"updated_at":                     now,
		"update_user_name":               "fastgpt_profile_template",
	})
	WsService.PublishStoreModelProfileChanged(tenant.StoreID, tenant.ProfileTemplateTargetRevision, fastGPTTemplateSyncFailed, now)
}

func (s *fastGPTProfileTemplateService) publishStoreProfileStatus(storeIDs []int64, revision int64, status string, changedAt time.Time) {
	for _, storeID := range storeIDs {
		WsService.PublishStoreModelProfileChanged(storeID, revision, status, changedAt)
	}
}

func (s *fastGPTProfileTemplateService) draftFromExistingProfile(ctx context.Context) *models.FastGPTProfileTemplate {
	template := &models.FastGPTProfileTemplate{
		Name:   "门店知识库模型模板",
		Status: "unconfigured",
	}
	if config := repositories.AIConfigRepository.GetEnabled(sqls.DB(), enums.AIModelTypeLLM); config != nil {
		template.ChatProvider = string(config.Provider)
		template.ChatBaseURL = config.BaseURL
		template.ChatModel = config.ModelName
		template.ChatAPIMode = normalizeAIConfigAPIMode(config.APIMode)
	}
	if config := repositories.AIConfigRepository.GetEnabled(sqls.DB(), enums.AIModelTypeASR); config != nil {
		template.ASRProvider = string(config.Provider)
		template.ASRBaseURL = config.BaseURL
		template.ASRModel = config.ModelName
	}
	knowledgeBases := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("connection_id", fastgptapi.ManagedConnectionID).
		Eq("status", enums.StatusOk).
		Where("fast_gpt_profile_id <> ''").
		Asc("id").
		Limit(1))
	if len(knowledgeBases) == 0 {
		return template
	}
	connector, err := NewManagedStoreFastGPTConnector()
	if err != nil {
		return template
	}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	profile, err := connector.ForStore(knowledgeBases[0].StoreID).GetModelProfile(callCtx, knowledgeBases[0].DatasetID)
	if err != nil || profile == nil {
		return template
	}
	template.EmbeddingProvider = profile.Embedding.Provider
	template.EmbeddingBaseURL = profile.Embedding.BaseURL
	template.EmbeddingModel = profile.Embedding.Model
	template.DocumentParserProvider = profile.DocumentParser.Provider
	template.DocumentParserBaseURL = profile.DocumentParser.BaseURL
	template.DocumentParserModel = profile.DocumentParser.Model
	template.VisionProvider = profile.Vision.Provider
	template.VisionBaseURL = profile.Vision.BaseURL
	template.VisionModel = profile.Vision.Model
	if profile.Rerank != nil {
		template.RerankProvider = profile.Rerank.Provider
		template.RerankBaseURL = profile.Rerank.BaseURL
		template.RerankModel = profile.Rerank.Model
	}
	return template
}

func (s *fastGPTProfileTemplateService) buildResponse(template *models.FastGPTProfileTemplate) *response.FastGPTProfileTemplateResponse {
	if template == nil {
		template = &models.FastGPTProfileTemplate{Name: "门店知识库模型模板", Status: "unconfigured"}
	}
	ret := &response.FastGPTProfileTemplateResponse{
		ID:       template.ID,
		Name:     template.Name,
		Revision: template.Revision,
		Status:   template.Status,
		Chat: response.FastGPTProfileTemplateCredentialResponse{
			Provider: template.ChatProvider, BaseURL: template.ChatBaseURL, Model: template.ChatModel, APIMode: normalizeAIConfigAPIMode(template.ChatAPIMode),
		},
		ASR: response.FastGPTProfileTemplateCredentialResponse{
			Provider: template.ASRProvider, BaseURL: template.ASRBaseURL, Model: template.ASRModel,
		},
		Embedding: response.FastGPTProfileTemplateCredentialResponse{
			Provider: template.EmbeddingProvider, BaseURL: template.EmbeddingBaseURL, Model: template.EmbeddingModel,
		},
		DocumentParser: response.FastGPTProfileTemplateCredentialResponse{
			Provider: template.DocumentParserProvider, BaseURL: template.DocumentParserBaseURL, Model: template.DocumentParserModel,
		},
		Vision: response.FastGPTProfileTemplateCredentialResponse{
			Provider: template.VisionProvider, BaseURL: template.VisionBaseURL, Model: template.VisionModel,
		},
		Rerank: response.FastGPTProfileTemplateCredentialResponse{
			Provider: template.RerankProvider, BaseURL: template.RerankBaseURL, Model: template.RerankModel,
		},
		UpdatedAt: template.UpdatedAt,
	}
	storeIDs := s.managedStoreIDs()
	tenants := repositories.FastGPTStoreTenantRepository.FindByStoreIDs(sqls.DB(), storeIDs)
	tenantByStore := make(map[int64]models.FastGPTStoreTenant, len(tenants))
	for index := range tenants {
		tenantByStore[tenants[index].StoreID] = tenants[index]
	}
	knowledgeBases := make([]models.KnowledgeBase, 0)
	if len(storeIDs) > 0 {
		knowledgeBases = repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
			In("store_id", storeIDs).
			Eq("connection_id", fastgptapi.ManagedConnectionID).
			Eq("status", enums.StatusOk).
			Asc("id"))
	}
	kbByStore := make(map[int64]models.KnowledgeBase, len(storeIDs))
	for index := range knowledgeBases {
		if _, exists := kbByStore[knowledgeBases[index].StoreID]; !exists {
			kbByStore[knowledgeBases[index].StoreID] = knowledgeBases[index]
		}
	}
	for _, storeID := range storeIDs {
		tenant := tenantByStore[storeID]
		kb := kbByStore[storeID]
		storeName := ""
		if store := StoreService.Get(storeID); store != nil {
			storeName = store.Name
		}
		status := firstNonBlank(tenant.ProfileTemplateSyncStatus, "unconfigured")
		item := response.FastGPTProfileTemplateStoreSyncResponse{
			StoreID: storeID, StoreName: storeName,
			ProfileName: kb.FastGPTProfileName, ProfileRevision: kb.FastGPTProfileRevision,
			TargetRevision: tenant.ProfileTemplateTargetRevision, Status: status,
			LastError: tenant.ProfileTemplateLastError, LastSyncedAt: tenant.ProfileTemplateSyncedAt,
		}
		ret.Stores = append(ret.Stores, item)
		ret.Sync.Total++
		switch status {
		case fastGPTTemplateSyncReady:
			ret.Sync.Ready++
		case fastGPTTemplateSyncFailed:
			ret.Sync.Failed++
		case fastGPTTemplateSyncBlocked:
			ret.Sync.Blocked++
		default:
			ret.Sync.Pending++
		}
	}
	return ret
}

func (s *fastGPTProfileTemplateService) managedStoreIDs() []int64 {
	knowledgeBases := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("connection_id", fastgptapi.ManagedConnectionID).
		Eq("status", enums.StatusOk).
		Asc("store_id"))
	seen := map[int64]struct{}{}
	storeIDs := make([]int64, 0, len(knowledgeBases))
	for index := range knowledgeBases {
		if knowledgeBases[index].StoreID <= 0 {
			continue
		}
		if _, exists := seen[knowledgeBases[index].StoreID]; exists {
			continue
		}
		seen[knowledgeBases[index].StoreID] = struct{}{}
		storeIDs = append(storeIDs, knowledgeBases[index].StoreID)
	}
	return storeIDs
}

func requireFastGPTProfileTemplateAdmin(operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if !slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) {
		return errorsx.Forbidden("仅超级管理员可以管理知识库 Profile 模板")
	}
	return nil
}

func normalizeFastGPTProfileTemplateRequest(req *request.UpdateFastGPTProfileTemplateRequest) {
	if req == nil {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	normalizeFastGPTProfileTemplateCredential(&req.Chat)
	normalizeFastGPTProfileTemplateCredential(&req.ASR)
	normalizeFastGPTProfileTemplateCredential(&req.Embedding)
	normalizeFastGPTProfileTemplateCredential(&req.DocumentParser)
	normalizeFastGPTProfileTemplateCredential(&req.Vision)
	normalizeFastGPTProfileTemplateCredential(&req.Rerank)
}

func normalizeFastGPTProfileTemplateCredential(value *request.FastGPTProfileTemplateCredentialRequest) {
	value.Provider = strings.TrimSpace(value.Provider)
	value.BaseURL = strings.TrimRight(strings.TrimSpace(value.BaseURL), "/")
	value.Model = strings.TrimSpace(value.Model)
	value.APIMode = normalizeAIConfigAPIMode(value.APIMode)
}

func validateFastGPTProfileTemplateRequest(req request.UpdateFastGPTProfileTemplateRequest) error {
	if req.Name == "" {
		return errorsx.InvalidParam("请填写 Profile 模板名称")
	}
	baseURLs := make([]string, 0, 6)
	for _, item := range []struct {
		name  string
		value request.FastGPTProfileTemplateCredentialRequest
	}{
		{name: "对话模型", value: req.Chat},
		{name: "语音识别模型", value: req.ASR},
		{name: "向量模型", value: req.Embedding},
		{name: "文档理解模型", value: req.DocumentParser},
		{name: "视觉理解模型", value: req.Vision},
		{name: "重排模型", value: req.Rerank},
	} {
		if item.value.Provider == "" || item.value.BaseURL == "" || item.value.Model == "" {
			return errorsx.InvalidParam("请完整填写" + item.name + "的 Provider、Base URL 和模型名")
		}
		parsed, err := url.Parse(item.value.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errorsx.InvalidParam(item.name + "的 Base URL 格式不正确")
		}
		baseURLs = append(baseURLs, item.value.BaseURL)
	}
	if !modelGatewayBaseURLsMatch(baseURLs...) {
		return errorsx.InvalidParam("六个模型槽必须使用同一个模型网关 Base URL")
	}
	return nil
}

func modelGatewayBaseURLsMatch(values ...string) bool {
	gateway := ""
	for _, value := range values {
		normalized := strings.TrimRight(strings.TrimSpace(value), "/")
		if normalized == "" {
			return false
		}
		if gateway == "" {
			gateway = normalized
			continue
		}
		if normalized != gateway {
			return false
		}
	}
	return gateway != ""
}

func profileHasAllKeys(profile *FastGPTModelProfile) bool {
	return profile != nil &&
		strings.TrimSpace(profile.ID) != "" &&
		profile.Embedding.KeyConfigured &&
		profile.DocumentParser.KeyConfigured &&
		profile.Vision.KeyConfigured &&
		profile.Rerank != nil &&
		profile.Rerank.KeyConfigured
}

func buildFastGPTTemplateProfileInput(template *models.FastGPTProfileTemplate, datasetID string, profile *FastGPTModelProfile) FastGPTModelProfileInput {
	name := strings.TrimSpace(template.Name)
	if name == "" {
		name = strings.TrimSpace(profile.Name)
	}
	if name == "" {
		name = "门店知识库模型"
	}
	rerank := fastgptapi.ModelCredential{
		Provider: template.RerankProvider,
		BaseURL:  template.RerankBaseURL,
		Model:    template.RerankModel,
	}
	return FastGPTModelProfileInput{
		DatasetID: datasetID,
		ProfileID: profile.ID,
		Name:      name,
		Embedding: fastgptapi.ModelCredential{
			Provider: template.EmbeddingProvider,
			BaseURL:  template.EmbeddingBaseURL,
			Model:    template.EmbeddingModel,
		},
		DocumentParser: fastgptapi.ModelCredential{
			Provider: template.DocumentParserProvider,
			BaseURL:  template.DocumentParserBaseURL,
			Model:    template.DocumentParserModel,
		},
		Vision: fastgptapi.ModelCredential{
			Provider: template.VisionProvider,
			BaseURL:  template.VisionBaseURL,
			Model:    template.VisionModel,
		},
		Rerank: &rerank,
	}
}

func profileMatchesTemplate(profile *FastGPTModelProfile, template *models.FastGPTProfileTemplate) bool {
	if profile == nil || template == nil || profile.Rerank == nil {
		return false
	}
	return strings.TrimSpace(profile.Name) == strings.TrimSpace(template.Name) &&
		credentialMatchesTemplate(profile.Embedding, template.EmbeddingProvider, template.EmbeddingBaseURL, template.EmbeddingModel) &&
		credentialMatchesTemplate(profile.DocumentParser, template.DocumentParserProvider, template.DocumentParserBaseURL, template.DocumentParserModel) &&
		credentialMatchesTemplate(profile.Vision, template.VisionProvider, template.VisionBaseURL, template.VisionModel) &&
		credentialMatchesTemplate(*profile.Rerank, template.RerankProvider, template.RerankBaseURL, template.RerankModel)
}

func credentialMatchesTemplate(value fastgptapi.ModelCredential, provider, baseURL, model string) bool {
	return strings.TrimSpace(value.Provider) == strings.TrimSpace(provider) &&
		strings.TrimRight(strings.TrimSpace(value.BaseURL), "/") == strings.TrimRight(strings.TrimSpace(baseURL), "/") &&
		strings.TrimSpace(value.Model) == strings.TrimSpace(model)
}
