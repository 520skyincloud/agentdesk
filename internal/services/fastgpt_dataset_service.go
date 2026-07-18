package services

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
)

const (
	fastGPTJobActionCreateDataset = "create_dataset"
	fastGPTJobActionUploadFile    = "upload_file"
	fastGPTJobStatusPending       = "pending"
	fastGPTJobStatusUploading     = "uploading"
	fastGPTJobStatusParsing       = "parsing"
	fastGPTJobStatusIndexing      = "indexing"
	fastGPTJobStatusReady         = "ready"
	fastGPTJobStatusFailed        = "failed"
)

var FastGPTDatasetService = newFastGPTDatasetService()

type fastGPTDatasetService struct{}

func newFastGPTDatasetService() *fastGPTDatasetService { return &fastGPTDatasetService{} }

func (s *fastGPTDatasetService) LatestJobByStore(storeID, tenantID int64) *models.FastGPTDatasetJob {
	if storeID <= 0 || tenantID <= 0 {
		return nil
	}
	items := repositories.FastGPTDatasetJobRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("store_id", storeID).Desc("id").Limit(1))
	if len(items) == 0 {
		return nil
	}
	return &items[0]
}

func (s *fastGPTDatasetService) EnqueueDefaultDataset(storeID int64, name string, operator *dto.AuthPrincipal) (*models.FastGPTDatasetJob, error) {
	store, err := s.requireStoreAccess(storeID, operator)
	if err != nil {
		return nil, err
	}
	return s.enqueueDefaultDataset(store, name)
}

func (s *fastGPTDatasetService) EnqueueDefaultDatasetForRemoteSetup(storeID, tenantID int64, name string) (*models.FastGPTDatasetJob, error) {
	store := StoreService.GetInTenant(storeID, tenantID)
	return s.enqueueDefaultDataset(store, name)
}

func (s *fastGPTDatasetService) enqueueDefaultDataset(store *models.Store, name string) (*models.FastGPTDatasetJob, error) {
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	if store.TenantID <= 0 {
		return nil, errorsx.InvalidParam("门店缺少接入公司归属")
	}
	name = strings.TrimSpace(firstNonBlank(name, store.Name))
	if name == "" {
		return nil, errorsx.InvalidParam("请填写知识库名称")
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", store.TenantID, store.ID, strings.ToLower(name))))
	taskKey := fmt.Sprintf("fastgpt-create-tenant-%d-store-%d-%x", store.TenantID, store.ID, sum[:6])
	if existing := repositories.FastGPTDatasetJobRepository.Take(sqls.DB(), "task_key = ? AND tenant_id = ?", taskKey, store.TenantID); existing != nil {
		if existing.Status == fastGPTJobStatusFailed {
			now := time.Now()
			if err := repositories.FastGPTDatasetJobRepository.UpdatesInTenant(sqls.DB(), existing.ID, store.TenantID, map[string]any{
				"status":        fastGPTJobStatusPending,
				"attempt_count": 0,
				"next_retry_at": now,
				"started_at":    nil,
				"completed_at":  nil,
				"last_error":    "",
				"updated_at":    now,
			}); err != nil {
				return nil, err
			}
			existing.Status = fastGPTJobStatusPending
			existing.AttemptCount = 0
			existing.NextRetryAt = &now
			existing.StartedAt = nil
			existing.CompletedAt = nil
			existing.LastError = ""
			existing.UpdatedAt = now
		}
		return existing, nil
	}
	now := time.Now()
	job := &models.FastGPTDatasetJob{
		TenantID:  store.TenantID,
		TaskKey:   taskKey,
		CompanyID: store.CompanyID,
		StoreID:   store.ID,
		Action:    fastGPTJobActionCreateDataset,
		Status:    fastGPTJobStatusPending,
		Filename:  name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repositories.FastGPTDatasetJobRepository.Create(sqls.DB(), job); err != nil {
		return nil, err
	}
	return job, nil
}

func (s *fastGPTDatasetService) EnqueueUpload(knowledgeBaseID int64, file *multipart.FileHeader, operator *dto.AuthPrincipal) (*models.FastGPTDatasetJob, error) {
	tenantID, err := requireActiveTenantID(operator, "FastGPT 知识库")
	if err != nil {
		return nil, err
	}
	knowledgeBase := KnowledgeBaseService.GetInTenant(knowledgeBaseID, tenantID)
	if knowledgeBase == nil || knowledgeBase.Status != enums.StatusOk || knowledgeBase.StoreID <= 0 || knowledgeBase.DatasetID == "" {
		return nil, errorsx.InvalidParam("知识库尚未完成 FastGPT 数据集配置")
	}
	if _, err := s.requireStoreAccess(knowledgeBase.StoreID, operator); err != nil {
		return nil, err
	}
	asset, err := AssetService.UploadFile(file, fmt.Sprintf("fastgpt-upload-tmp/%d", knowledgeBase.StoreID), operator)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	job := &models.FastGPTDatasetJob{
		TenantID:         knowledgeBase.TenantID,
		TaskKey:          "fastgpt-upload-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		CompanyID:        knowledgeBase.CompanyID,
		StoreID:          knowledgeBase.StoreID,
		KnowledgeBaseID:  knowledgeBase.ID,
		Action:           fastGPTJobActionUploadFile,
		Status:           fastGPTJobStatusPending,
		DatasetID:        knowledgeBase.DatasetID,
		Filename:         file.Filename,
		TemporaryAssetID: asset.AssetID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := repositories.FastGPTDatasetJobRepository.Create(sqls.DB(), job); err != nil {
		_ = AssetService.DeleteTemporaryAsset(asset.AssetID, knowledgeBase.TenantID)
		return nil, err
	}
	return job, nil
}

func (s *fastGPTDatasetService) ProcessDue(limit int) int {
	if limit <= 0 {
		limit = 10
	}
	now := time.Now()
	jobs := repositories.FastGPTDatasetJobRepository.Find(sqls.DB(), sqls.NewCnd().
		In("status", []string{fastGPTJobStatusPending, fastGPTJobStatusUploading, fastGPTJobStatusParsing, fastGPTJobStatusIndexing}).
		Where("next_retry_at IS NULL OR next_retry_at <= ?", now).
		Asc("id").Limit(limit))
	processed := 0
	for i := range jobs {
		if err := s.processJob(&jobs[i]); err != nil {
			s.failOrRetry(&jobs[i], err)
		}
		processed++
	}
	return processed
}

func (s *fastGPTDatasetService) processJob(job *models.FastGPTDatasetJob) error {
	if job == nil || job.TenantID <= 0 {
		return errorsx.InvalidParam("FastGPT 任务缺少接入公司归属")
	}
	var connector *FastGPTConnector
	var err error
	if job.Action == fastGPTJobActionCreateDataset {
		connector, err = NewManagedStoreFastGPTConnector()
	} else {
		knowledgeBase := KnowledgeBaseService.GetInTenant(job.KnowledgeBaseID, job.TenantID)
		if knowledgeBase == nil {
			return errorsx.InvalidParam("知识库不存在")
		}
		connector, err = NewFastGPTConnectorForKnowledgeBase(knowledgeBase)
	}
	if err != nil {
		return err
	}
	connector = connector.ForStore(job.StoreID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if job.CollectionID != "" || job.Status == fastGPTJobStatusParsing || job.Status == fastGPTJobStatusIndexing {
		return s.pollUpload(ctx, connector, job)
	}
	now := time.Now()
	_ = repositories.FastGPTDatasetJobRepository.UpdatesInTenant(sqls.DB(), job.ID, job.TenantID, map[string]any{"status": fastGPTJobStatusUploading, "started_at": now, "attempt_count": job.AttemptCount + 1, "updated_at": now})
	switch job.Action {
	case fastGPTJobActionCreateDataset:
		return s.createDataset(ctx, connector, job)
	case fastGPTJobActionUploadFile:
		return s.uploadFile(ctx, connector, job)
	default:
		return errorsx.InvalidParam("未知 FastGPT 任务类型")
	}
}

func (s *fastGPTDatasetService) createDataset(ctx context.Context, connector *FastGPTConnector, job *models.FastGPTDatasetJob) error {
	startedAt := time.Now()
	store := StoreService.GetInTenant(job.StoreID, job.TenantID)
	if store == nil {
		return errorsx.InvalidParam("门店不存在")
	}
	if err := s.ensureStoreTenant(ctx, connector, store); err != nil {
		s.recordJobUsage(job, "tenant_ensure", 0, time.Since(startedAt).Milliseconds(), err)
		return err
	}
	dataset, err := connector.CreateDataset(ctx, job.Filename, "Agent Desk 门店知识库")
	if err != nil {
		s.recordJobUsage(job, "dataset_create", 0, time.Since(startedAt).Milliseconds(), err)
		return err
	}
	s.recordJobUsage(job, "dataset_create", 0, time.Since(startedAt).Milliseconds(), nil)
	// A missing Profile snapshot must never turn a successfully-created remote
	// dataset into a retried create job. Persist a visible pending state and
	// let the asynchronous sync fill the safe metadata later.
	profile, profileErr := connector.GetDatasetProfileSnapshot(ctx, dataset.ID)
	now := time.Now()
	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		knowledgeBase := &models.KnowledgeBase{
			TenantID:              job.TenantID,
			CompanyID:             store.CompanyID,
			StoreID:               store.ID,
			DatasetID:             dataset.ID,
			DatasetName:           firstNonBlank(dataset.Name, job.Filename),
			ConnectionID:          fastgptapi.ManagedConnectionID,
			Name:                  firstNonBlank(dataset.Name, job.Filename),
			KnowledgeType:         string(enums.KnowledgeBaseTypeFastGPTCloud),
			Status:                enums.StatusOk,
			DefaultTopK:           10,
			DefaultScoreThreshold: 0.2,
			AnswerMode:            int(enums.KnowledgeAnswerModeStrict),
			FastGPTProfileStatus:  "pending",
			AuditFields: models.AuditFields{
				CreatedAt: now, CreateUserName: "fastgpt_dataset_job", UpdatedAt: now, UpdateUserName: "fastgpt_dataset_job",
			},
		}
		if profileErr == nil && profile != nil {
			knowledgeBase.FastGPTProfileID = profile.ProfileID
			knowledgeBase.FastGPTProfileName = profile.ProfileName
			knowledgeBase.FastGPTProfileRevision = profile.ProfileRevision
			knowledgeBase.FastGPTProfileFingerprint = profile.Fingerprint
			knowledgeBase.FastGPTProfileStatus = firstNonBlank(profile.ProfileStatus, "pending")
			knowledgeBase.FastGPTProfileSyncedAt = &now
		}
		if err := repositories.KnowledgeBaseRepository.Create(tx.Tx, knowledgeBase); err != nil {
			return err
		}
		// Only the first knowledge base becomes the initial default. Additional
		// datasets remain inactive until the store explicitly selects one.
		if store.KnowledgeBaseID <= 0 {
			if err := repositories.StoreRepository.UpdatesInTenant(tx.Tx, store.ID, job.TenantID, map[string]any{"knowledge_base_id": knowledgeBase.ID, "updated_at": now, "update_user_name": "fastgpt_dataset_job"}); err != nil {
				return err
			}
			if err := repositories.WxWorkProtocolInstanceRepository.UpdateKnowledgeBaseByStoreInTenant(tx.Tx, store.ID, knowledgeBase.ID, job.TenantID, now, "fastgpt_dataset_job"); err != nil {
				return err
			}
		}
		return repositories.FastGPTDatasetJobRepository.UpdatesInTenant(tx.Tx, job.ID, job.TenantID, map[string]any{
			"status": fastGPTJobStatusReady, "dataset_id": dataset.ID, "knowledge_base_id": knowledgeBase.ID, "completed_at": now, "last_error": "", "updated_at": now,
		})
	})
}

func (s *fastGPTDatasetService) ensureStoreTenant(ctx context.Context, connector *FastGPTConnector, store *models.Store) error {
	if connector == nil || store == nil {
		return errorsx.InvalidParam("FastGPT 门店租户参数无效")
	}
	tenant, err := connector.EnsureStoreTenant(ctx, firstNonBlank(store.Name, "Agent Desk 门店"))
	if err != nil || tenant == nil {
		return err
	}
	now := time.Now()
	return repositories.FastGPTStoreTenantRepository.Save(sqls.DB(), &models.FastGPTStoreTenant{
		TenantID:       store.TenantID,
		CompanyID:      store.CompanyID,
		StoreID:        store.ID,
		TenantTeamID:   tenant.TeamID,
		TenantTeamName: firstNonBlank(tenant.TeamName, store.Name),
		Status:         firstNonBlank(tenant.Status, "active"),
		LastSyncedAt:   &now,
		LastError:      "",
		AuditFields: models.AuditFields{
			CreatedAt: now, CreateUserName: "fastgpt_integration", UpdatedAt: now, UpdateUserName: "fastgpt_integration",
		},
	})
}

// ActivateKnowledgeBase switches the current FastGPT dataset for one employee
// account. Existing conversation history, route state, and other accounts at
// the same store are intentionally not rewritten; subsequent replies resolve
// the current instance binding.
func (s *fastGPTDatasetService) ActivateKnowledgeBase(instanceID, knowledgeBaseID int64, operator *dto.AuthPrincipal) error {
	if instanceID <= 0 || knowledgeBaseID <= 0 {
		return errorsx.InvalidParam("请选择员工号和知识库")
	}
	tenantID, err := requireActiveTenantID(operator, "FastGPT 知识库")
	if err != nil {
		return err
	}
	instance := WxWorkProtocolInstanceService.GetByTenantID(instanceID, tenantID)
	if instance == nil || instance.Status == enums.StatusDeleted || instance.StoreID <= 0 {
		return errorsx.InvalidParam("企微员工号不存在或未绑定门店")
	}
	if _, err := s.requireStoreAccess(instance.StoreID, operator); err != nil {
		return err
	}
	knowledgeBase := KnowledgeBaseService.GetInTenant(knowledgeBaseID, tenantID)
	if knowledgeBase == nil || knowledgeBase.Status != enums.StatusOk || knowledgeBase.StoreID != instance.StoreID || knowledgeBase.DatasetID == "" {
		return errorsx.InvalidParam("只能启用当前门店已完成配置的 FastGPT 知识库")
	}
	now := time.Now()
	return repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(sqls.DB(), instance.ID, tenantID, map[string]any{
		"knowledge_base_id": knowledgeBase.ID,
		"updated_at":        now,
		"update_user_id":    operator.UserID,
		"update_user_name":  operator.Username,
	})
}

func (s *fastGPTDatasetService) GetModelProfile(ctx context.Context, instanceID int64, operator *dto.AuthPrincipal) (*response.FastGPTModelProfileResponse, error) {
	instance, kb, connector, err := s.requireManagedInstanceKnowledgeBase(instanceID, operator)
	if err != nil {
		return nil, err
	}
	profile, err := connector.ForStore(instance.StoreID).GetModelProfile(ctx, kb.DatasetID)
	if err != nil {
		return nil, publicFastGPTError(err)
	}
	if profile == nil {
		return nil, nil
	}
	return buildFastGPTModelProfileResponse(profile), nil
}

func (s *fastGPTDatasetService) TestModelProfile(ctx context.Context, req request.FastGPTModelProfileRequest, operator *dto.AuthPrincipal) (*response.FastGPTModelProfileTestResponse, error) {
	instance, kb, connector, err := s.requireManagedInstanceKnowledgeBase(req.WxWorkInstanceID, operator)
	if err != nil {
		return nil, err
	}
	result, err := connector.ForStore(instance.StoreID).TestModelProfile(ctx, buildFastGPTModelProfileInput(kb.DatasetID, req))
	if err != nil {
		return nil, publicFastGPTModelTestError(err)
	}
	ret := &response.FastGPTModelProfileTestResponse{TestToken: result.TestToken, ExpiresAt: result.ExpiresAt}
	for _, item := range result.Results {
		ret.Results = append(ret.Results, response.FastGPTModelProfileTestStageResponse{
			Stage: item.Stage, Status: item.Status, PromptTokens: item.PromptTokens, CompletionTokens: item.CompletionTokens,
		})
	}
	return ret, nil
}

func (s *fastGPTDatasetService) UpdateModelProfile(ctx context.Context, req request.FastGPTModelProfileRequest, operator *dto.AuthPrincipal) (*response.FastGPTModelProfileSaveResponse, error) {
	instance, kb, connector, err := s.requireManagedInstanceKnowledgeBase(req.WxWorkInstanceID, operator)
	if err != nil {
		return nil, err
	}
	result, err := connector.ForStore(instance.StoreID).UpsertModelProfile(ctx, buildFastGPTModelProfileInput(kb.DatasetID, req))
	if err != nil {
		return nil, publicFastGPTError(err)
	}
	if err := s.syncStoreModelProfileSnapshot(instance.StoreID, kb.TenantID, &result.Profile, operator); err != nil {
		return nil, err
	}
	return &response.FastGPTModelProfileSaveResponse{
		Profile: *buildFastGPTModelProfileResponse(&result.Profile), BoundDatasetCount: result.BoundDatasetCount,
	}, nil
}

func (s *fastGPTDatasetService) requireManagedInstanceKnowledgeBase(instanceID int64, operator *dto.AuthPrincipal) (*models.WxWorkProtocolInstance, *models.KnowledgeBase, *FastGPTConnector, error) {
	if operator == nil {
		return nil, nil, nil, errorsx.Forbidden("无权配置知识库模型")
	}
	if instanceID <= 0 {
		return nil, nil, nil, errorsx.InvalidParam("请选择企微员工号")
	}
	tenantID, err := requireActiveTenantID(operator, "FastGPT 知识库模型")
	if err != nil {
		return nil, nil, nil, err
	}
	instance := WxWorkProtocolInstanceService.GetByTenantID(instanceID, tenantID)
	if instance == nil || instance.Status == enums.StatusDeleted || instance.StoreID <= 0 {
		return nil, nil, nil, errorsx.InvalidParam("企微员工号不存在或未绑定门店")
	}
	if _, err := s.requireStoreAccess(instance.StoreID, operator); err != nil {
		return nil, nil, nil, err
	}
	kb := KnowledgeBaseService.GetInTenant(instance.KnowledgeBaseID, tenantID)
	if kb == nil || kb.Status != enums.StatusOk || kb.StoreID != instance.StoreID || strings.TrimSpace(kb.DatasetID) == "" {
		return nil, nil, nil, errorsx.InvalidParam("当前员工号尚未绑定可用的 FastGPT 知识库")
	}
	if strings.TrimSpace(kb.ConnectionID) != fastgptapi.ManagedConnectionID {
		return nil, nil, nil, errorsx.InvalidParam("当前知识库尚未迁移到门店 FastGPT Team，不能设置独立模型密钥")
	}
	connector, err := NewManagedStoreFastGPTConnector()
	if err != nil {
		return nil, nil, nil, err
	}
	return instance, kb, connector, nil
}

func (s *fastGPTDatasetService) syncStoreModelProfileSnapshot(storeID, tenantID int64, profile *FastGPTModelProfile, operator *dto.AuthPrincipal) error {
	if storeID <= 0 || tenantID <= 0 || profile == nil || strings.TrimSpace(profile.ID) == "" {
		return errorsx.InvalidParam("FastGPT 返回的模型 Profile 无效")
	}
	now := time.Now()
	fingerprintSource := strings.Join([]string{
		profile.Embedding.KeyFingerprint,
		profile.DocumentParser.KeyFingerprint,
		profile.Vision.KeyFingerprint,
		func() string {
			if profile.Rerank != nil {
				return profile.Rerank.KeyFingerprint
			}
			return ""
		}(),
	}, ":")
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprintSource)))
	items := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("store_id", storeID).
		Eq("connection_id", fastgptapi.ManagedConnectionID).
		Eq("status", enums.StatusOk))
	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		for index := range items {
			if err := repositories.KnowledgeBaseRepository.UpdatesInTenant(tx.Tx, items[index].ID, tenantID, map[string]any{
				"fast_gpt_profile_id":          profile.ID,
				"fast_gpt_profile_name":        profile.Name,
				"fast_gpt_profile_revision":    strconv.FormatInt(profile.Revision, 10),
				"fast_gpt_profile_fingerprint": fingerprint,
				"fast_gpt_profile_status":      "ready",
				"fast_gpt_profile_synced_at":   now,
				"updated_at":                   now,
				"update_user_id":               operator.UserID,
				"update_user_name":             operator.Username,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func buildFastGPTModelProfileInput(datasetID string, req request.FastGPTModelProfileRequest) FastGPTModelProfileInput {
	return FastGPTModelProfileInput{
		DatasetID:      datasetID,
		ProfileID:      req.ProfileID,
		Name:           req.Name,
		Embedding:      buildFastGPTModelCredential(req.Embedding),
		DocumentParser: buildFastGPTModelCredential(req.DocumentParser),
		Vision:         buildFastGPTModelCredential(req.Vision),
		Rerank: func() *fastgptapi.ModelCredential {
			if req.Rerank == nil {
				return nil
			}
			value := buildFastGPTModelCredential(*req.Rerank)
			return &value
		}(),
		DisableRerank: !req.RerankEnabled,
		TestToken:     req.TestToken,
	}
}

func buildFastGPTModelCredential(input request.FastGPTModelCredentialRequest) fastgptapi.ModelCredential {
	return fastgptapi.ModelCredential{Provider: input.Provider, BaseURL: input.BaseURL, Model: input.Model, APIKey: input.APIKey}
}

func buildFastGPTModelProfileResponse(profile *FastGPTModelProfile) *response.FastGPTModelProfileResponse {
	if profile == nil {
		return nil
	}
	ret := &response.FastGPTModelProfileResponse{
		ID: profile.ID, Name: profile.Name, Revision: profile.Revision, Status: "ready",
		Embedding:      buildFastGPTModelCredentialResponse(profile.Embedding),
		DocumentParser: buildFastGPTModelCredentialResponse(profile.DocumentParser),
		Vision:         buildFastGPTModelCredentialResponse(profile.Vision),
	}
	if profile.Rerank != nil {
		value := buildFastGPTModelCredentialResponse(*profile.Rerank)
		ret.Rerank = &value
	}
	return ret
}

func buildFastGPTModelCredentialResponse(input fastgptapi.ModelCredential) response.FastGPTModelCredentialResponse {
	return response.FastGPTModelCredentialResponse{
		Provider: input.Provider, BaseURL: input.BaseURL, Model: input.Model,
		KeyConfigured: input.KeyConfigured, KeyFingerprint: input.KeyFingerprint,
	}
}

func (s *fastGPTDatasetService) uploadFile(ctx context.Context, connector *FastGPTConnector, job *models.FastGPTDatasetJob) error {
	asset := AssetService.GetByAssetIDInTenant(job.TemporaryAssetID, job.TenantID)
	if asset == nil || asset.Status != enums.AssetStatusSuccess {
		return errorsx.InvalidParam("上传临时文件不存在")
	}
	reader, err := AssetService.OpenReader(asset)
	if err != nil {
		return err
	}
	defer reader.Close()
	startedAt := time.Now()
	collectionID, err := connector.UploadLocalFile(ctx, job.DatasetID, job.Filename, reader)
	if err != nil {
		s.recordJobUsage(job, "knowledge_upload", asset.FileSize, time.Since(startedAt).Milliseconds(), err)
		return err
	}
	s.recordJobUsage(job, "knowledge_upload", asset.FileSize, time.Since(startedAt).Milliseconds(), nil)
	next := time.Now().Add(15 * time.Second)
	return repositories.FastGPTDatasetJobRepository.UpdatesInTenant(sqls.DB(), job.ID, job.TenantID, map[string]any{
		"status": fastGPTJobStatusParsing, "collection_id": collectionID, "next_retry_at": next, "last_error": "", "updated_at": time.Now(),
	})
}

func (s *fastGPTDatasetService) recordJobUsage(job *models.FastGPTDatasetJob, operationType string, fileBytes int64, latencyMS int64, callErr error) {
	if job == nil {
		return
	}
	status := "completed"
	errorMessage := ""
	if callErr != nil {
		status = "failed"
		errorMessage = fastGPTErrorClass(callErr)
	}
	trainingCount := int64(0)
	if operationType == "knowledge_upload" {
		trainingCount = 1
	}
	_ = AIUsageEventService.Record(models.AIUsageEvent{
		TenantID:  job.TenantID,
		EventKey:  fmt.Sprintf("fastgpt-job:%s:%s:%d", job.TaskKey, operationType, job.AttemptCount+1),
		CompanyID: job.CompanyID, StoreID: job.StoreID, KnowledgeBaseID: job.KnowledgeBaseID,
		Stage: "knowledge_manage", Provider: "fastgpt", OperationType: operationType,
		RequestCount: 1, TrainingCount: trainingCount, FileBytes: fileBytes,
		MetricSource: AIUsageMetricSourceProviderOperation,
		LatencyMS:    latencyMS, Status: status, ErrorMessage: errorMessage,
	})
}

func (s *fastGPTDatasetService) pollUpload(ctx context.Context, connector *FastGPTConnector, job *models.FastGPTDatasetJob) error {
	collections, err := connector.ListCollections(ctx, job.DatasetID)
	if err != nil {
		return err
	}
	for _, collection := range collections {
		if collection.ID != job.CollectionID {
			continue
		}
		if collection.TrainingAmount > 0 {
			next := time.Now().Add(15 * time.Second)
			return repositories.FastGPTDatasetJobRepository.UpdatesInTenant(sqls.DB(), job.ID, job.TenantID, map[string]any{"status": fastGPTJobStatusIndexing, "next_retry_at": next, "updated_at": time.Now()})
		}
		now := time.Now()
		if err := repositories.FastGPTDatasetJobRepository.UpdatesInTenant(sqls.DB(), job.ID, job.TenantID, map[string]any{"status": fastGPTJobStatusReady, "completed_at": now, "next_retry_at": nil, "last_error": "", "updated_at": now}); err != nil {
			return err
		}
		return AssetService.DeleteTemporaryAsset(job.TemporaryAssetID, job.TenantID)
	}
	return errorsx.InvalidParam("FastGPT 未返回对应文件集合")
}

func (s *fastGPTDatasetService) failOrRetry(job *models.FastGPTDatasetJob, cause error) {
	attempts := job.AttemptCount + 1
	status := fastGPTJobStatusPending
	if job.CollectionID != "" {
		status = fastGPTJobStatusIndexing
	}
	var next *time.Time
	if attempts >= 5 {
		status = fastGPTJobStatusFailed
		if job.TemporaryAssetID != "" {
			_ = AssetService.DeleteTemporaryAsset(job.TemporaryAssetID, job.TenantID)
		}
	} else {
		when := time.Now().Add(time.Duration(attempts*attempts) * 30 * time.Second)
		next = &when
	}
	_ = repositories.FastGPTDatasetJobRepository.UpdatesInTenant(sqls.DB(), job.ID, job.TenantID, map[string]any{
		"status": status, "attempt_count": attempts, "next_retry_at": next, "last_error": fastGPTErrorClass(cause), "updated_at": time.Now(),
	})
}

func (s *fastGPTDatasetService) ListCollections(ctx context.Context, knowledgeBaseID int64, operator *dto.AuthPrincipal) ([]response.FastGPTCollectionResponse, error) {
	kb, err := s.requireKnowledgeBaseAccess(knowledgeBaseID, operator)
	if err != nil {
		return nil, err
	}
	connector, err := NewFastGPTConnectorForKnowledgeBase(kb)
	if err != nil {
		return nil, err
	}
	collections, err := connector.ForStore(kb.StoreID).ListCollections(ctx, kb.DatasetID)
	if err != nil {
		return nil, publicFastGPTError(err)
	}
	ret := make([]response.FastGPTCollectionResponse, 0, len(collections))
	for _, item := range collections {
		ret = append(ret, response.FastGPTCollectionResponse{ID: item.ID, Name: item.Name, Type: item.Type, DataAmount: item.DataAmount, TrainingAmount: item.TrainingAmount, Forbid: item.Forbid})
	}
	return ret, nil
}

// ListJobs exposes the durable FastGPT work queue for one authorized knowledge base.
// It is intentionally read-only: the UI uses it to show real create/upload/parse progress.
func (s *fastGPTDatasetService) ListJobs(knowledgeBaseID int64, operator *dto.AuthPrincipal) ([]response.FastGPTDatasetJobResponse, error) {
	kb, err := s.requireKnowledgeBaseAccess(knowledgeBaseID, operator)
	if err != nil {
		return nil, err
	}
	items := repositories.FastGPTDatasetJobRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", kb.TenantID).
		Eq("knowledge_base_id", knowledgeBaseID).
		Desc("id").
		Limit(50))
	result := make([]response.FastGPTDatasetJobResponse, 0, len(items))
	for index := range items {
		result = append(result, buildFastGPTJobResponse(&items[index]))
	}
	return result, nil
}

func (s *fastGPTDatasetService) SearchTest(ctx context.Context, knowledgeBaseID int64, query string, operator *dto.AuthPrincipal) (*FastGPTSearchResult, error) {
	kb, err := s.requireKnowledgeBaseAccess(knowledgeBaseID, operator)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, errorsx.InvalidParam("请输入检索问题")
	}
	connector, err := NewFastGPTConnectorForKnowledgeBase(kb)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	result, err := connector.ForStore(kb.StoreID).SearchTest(ctx, kb.DatasetID, query)
	status := "completed"
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = fastGPTErrorClass(err)
	}
	_ = AIUsageEventService.Record(models.AIUsageEvent{
		TenantID:  kb.TenantID,
		EventKey:  "fastgpt-search-test:" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		CompanyID: kb.CompanyID, StoreID: kb.StoreID, KnowledgeBaseID: kb.ID,
		Stage: "knowledge_search_test", Provider: "fastgpt", OperationType: "knowledge_retrieve",
		RequestCount: 1, RerankCount: 1, MetricSource: AIUsageMetricSourceProviderOperation,
		LatencyMS: time.Since(startedAt).Milliseconds(), Status: status, ErrorMessage: errorMessage,
	})
	if err != nil {
		return nil, publicFastGPTError(err)
	}
	return result, nil
}

func (s *fastGPTDatasetService) DeleteCollection(ctx context.Context, knowledgeBaseID int64, collectionID string, operator *dto.AuthPrincipal) error {
	kb, err := s.requireKnowledgeBaseAccess(knowledgeBaseID, operator)
	if err != nil {
		return err
	}
	if strings.TrimSpace(collectionID) == "" {
		return errorsx.InvalidParam("请选择要删除的文件")
	}
	connector, err := NewFastGPTConnectorForKnowledgeBase(kb)
	if err != nil {
		return err
	}
	startedAt := time.Now()
	err = connector.ForStore(kb.StoreID).DeleteCollections(ctx, kb.DatasetID, []string{strings.TrimSpace(collectionID)})
	status := "completed"
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = fastGPTErrorClass(err)
	}
	_ = AIUsageEventService.Record(models.AIUsageEvent{
		TenantID:  kb.TenantID,
		EventKey:  "fastgpt-delete:" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		CompanyID: kb.CompanyID, StoreID: kb.StoreID, KnowledgeBaseID: kb.ID,
		Stage: "knowledge_manage", Provider: "fastgpt", OperationType: "knowledge_delete",
		RequestCount: 1, MetricSource: AIUsageMetricSourceProviderOperation,
		LatencyMS: time.Since(startedAt).Milliseconds(), Status: status, ErrorMessage: errorMessage,
	})
	return publicFastGPTError(err)
}

// DeleteDataset physically deletes the FastGPT dataset after the caller types
// the exact knowledge-base name. The local row is retained as a deleted audit
// record and can no longer be selected by the reply runtime.
func (s *fastGPTDatasetService) DeleteDataset(ctx context.Context, knowledgeBaseID int64, confirmationName string, operator *dto.AuthPrincipal) error {
	kb, err := s.requireKnowledgeBaseAccess(knowledgeBaseID, operator)
	if err != nil {
		return err
	}
	if err := validateDatasetDeletionConfirmation(kb, confirmationName); err != nil {
		return err
	}
	connector, err := NewFastGPTConnectorForKnowledgeBase(kb)
	if err != nil {
		return err
	}
	startedAt := time.Now()
	err = connector.ForStore(kb.StoreID).DeleteDataset(ctx, kb.DatasetID)
	status := "completed"
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = fastGPTErrorClass(err)
	}
	_ = AIUsageEventService.Record(models.AIUsageEvent{
		TenantID:  kb.TenantID,
		EventKey:  "fastgpt-dataset-delete:" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		CompanyID: kb.CompanyID, StoreID: kb.StoreID, KnowledgeBaseID: kb.ID,
		Stage: "knowledge_manage", Provider: "fastgpt", OperationType: "dataset_delete",
		RequestCount: 1, MetricSource: AIUsageMetricSourceProviderOperation,
		LatencyMS: time.Since(startedAt).Milliseconds(), Status: status, ErrorMessage: errorMessage,
	})
	if err != nil {
		return publicFastGPTError(err)
	}
	return s.finalizeDatasetDeletion(kb, operator)
}

func validateDatasetDeletionConfirmation(kb *models.KnowledgeBase, confirmationName string) error {
	if kb == nil || strings.TrimSpace(kb.Name) == "" {
		return errorsx.InvalidParam("知识库不存在或名称不完整")
	}
	if strings.TrimSpace(confirmationName) != strings.TrimSpace(kb.Name) {
		return errorsx.InvalidParam("请输入完整知识库名称后再删除")
	}
	return nil
}

func (s *fastGPTDatasetService) finalizeDatasetDeletion(kb *models.KnowledgeBase, operator *dto.AuthPrincipal) error {
	if kb == nil {
		return errorsx.InvalidParam("知识库不存在")
	}
	now := time.Now()
	operatorName := "fastgpt_dataset_delete"
	operatorID := int64(0)
	if operator != nil {
		operatorName = operator.Username
		operatorID = operator.UserID
	}
	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := repositories.KnowledgeBaseRepository.UpdatesInTenant(tx.Tx, kb.ID, kb.TenantID, map[string]any{
			"status": enums.StatusDeleted, "updated_at": now, "update_user_id": operatorID, "update_user_name": operatorName,
		}); err != nil {
			return err
		}
		if store := repositories.StoreRepository.GetInTenant(tx.Tx, kb.StoreID, kb.TenantID); store != nil && store.KnowledgeBaseID == kb.ID {
			if err := repositories.StoreRepository.UpdatesInTenant(tx.Tx, store.ID, kb.TenantID, map[string]any{
				"knowledge_base_id": 0, "updated_at": now, "update_user_id": operatorID, "update_user_name": operatorName,
			}); err != nil {
				return err
			}
		}
		return repositories.WxWorkProtocolInstanceRepository.ClearKnowledgeBaseByIDInTenant(tx.Tx, kb.ID, kb.TenantID, now, operatorName)
	})
}

func (s *fastGPTDatasetService) requireKnowledgeBaseAccess(knowledgeBaseID int64, operator *dto.AuthPrincipal) (*models.KnowledgeBase, error) {
	tenantID, err := requireActiveTenantID(operator, "FastGPT 知识库")
	if err != nil {
		return nil, err
	}
	kb := KnowledgeBaseService.GetInTenant(knowledgeBaseID, tenantID)
	if kb == nil || kb.Status != enums.StatusOk || kb.StoreID <= 0 || kb.DatasetID == "" {
		return nil, errorsx.InvalidParam("FastGPT 知识库不存在或未启用")
	}
	if _, err := s.requireStoreAccess(kb.StoreID, operator); err != nil {
		return nil, err
	}
	return kb, nil
}

func (s *fastGPTDatasetService) requireStoreAccess(storeID int64, operator *dto.AuthPrincipal) (*models.Store, error) {
	tenantID, err := requireActiveTenantID(operator, "FastGPT 知识库")
	if err != nil {
		return nil, err
	}
	store := StoreService.GetInTenant(storeID, tenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if scope.Unrestricted {
		return store, nil
	}
	for _, allowedStoreID := range scope.StoreIDs {
		if allowedStoreID == storeID {
			return store, nil
		}
	}
	return nil, errorsx.Forbidden("无权限管理该门店的 FastGPT 知识库")
}

func buildFastGPTJobResponse(job *models.FastGPTDatasetJob) response.FastGPTDatasetJobResponse {
	return response.FastGPTDatasetJobResponse{
		ID: job.ID, StoreID: job.StoreID, KnowledgeBaseID: job.KnowledgeBaseID, Action: job.Action, Status: job.Status, DatasetID: job.DatasetID, CollectionID: job.CollectionID, Filename: job.Filename, AttemptCount: job.AttemptCount, NextRetryAt: job.NextRetryAt, LastError: job.LastError, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
	}
}

// FastGPT error details may include provider text or operational topology.
// Store and display only a stable class; detailed diagnostics remain upstream.
func fastGPTErrorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "fastgpt_timeout"
	}
	var statusErr *fastgptapi.HTTPStatusError
	if errors.As(err, &statusErr) {
		if statusErr.StatusCode >= 500 {
			return "fastgpt_http_5xx"
		}
		return "fastgpt_http_4xx"
	}
	return "fastgpt_request_failed"
}

func publicFastGPTError(err error) error {
	if err == nil {
		return nil
	}
	return errorsx.BusinessError(1, "FastGPT 服务暂不可用，请稍后重试")
}

func publicFastGPTModelTestError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errorsx.BusinessError(2, "模型测试超时，请检查接口地址或稍后重试")
	}
	var statusErr *fastgptapi.HTTPStatusError
	if !errors.As(err, &statusErr) {
		return errorsx.BusinessError(2, "模型测试失败，请检查接口地址、密钥和模型名")
	}
	switch statusErr.StatusCode {
	case http.StatusBadRequest:
		return errorsx.BusinessError(2, "模型测试未通过，请检查模型名和接口兼容格式")
	case http.StatusUnauthorized, http.StatusForbidden:
		return errorsx.BusinessError(2, "模型测试未通过，请检查 API Key 是否有效")
	case http.StatusNotFound:
		return errorsx.BusinessError(2, "模型测试未通过，请检查 Base URL 和模型名")
	case http.StatusTooManyRequests:
		return errorsx.BusinessError(2, "模型服务当前限流，请稍后重新测试")
	default:
		return errorsx.BusinessError(2, "模型服务暂不可用，请稍后重新测试")
	}
}
