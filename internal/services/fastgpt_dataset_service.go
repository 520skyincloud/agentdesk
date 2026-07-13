package services

import (
	"context"
	"fmt"
	"mime/multipart"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
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

func (s *fastGPTDatasetService) LatestJobByStore(storeID int64) *models.FastGPTDatasetJob {
	if storeID <= 0 {
		return nil
	}
	items := repositories.FastGPTDatasetJobRepository.Find(sqls.DB(), sqls.NewCnd().Eq("store_id", storeID).Desc("id").Limit(1))
	if len(items) == 0 {
		return nil
	}
	return &items[0]
}

func (s *fastGPTDatasetService) EnqueueDefaultDataset(storeID int64, name string, operator *dto.AuthPrincipal) (*models.FastGPTDatasetJob, error) {
	if err := s.requireStoreAccess(storeID, operator); err != nil {
		return nil, err
	}
	return s.enqueueDefaultDataset(storeID, name)
}

func (s *fastGPTDatasetService) EnqueueDefaultDatasetForRemoteSetup(storeID int64, name string) (*models.FastGPTDatasetJob, error) {
	return s.enqueueDefaultDataset(storeID, name)
}

func (s *fastGPTDatasetService) enqueueDefaultDataset(storeID int64, name string) (*models.FastGPTDatasetJob, error) {
	store := StoreService.Get(storeID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	if existing := repositories.KnowledgeBaseRepository.Take(sqls.DB(), "store_id = ? AND status = ? AND dataset_id <> ''", storeID, enums.StatusOk); existing != nil {
		return nil, errorsx.InvalidParam("该门店已经有启用的 FastGPT 知识库")
	}
	taskKey := fmt.Sprintf("fastgpt-create-store-%d", storeID)
	if existing := repositories.FastGPTDatasetJobRepository.Take(sqls.DB(), "task_key = ?", taskKey); existing != nil {
		if existing.Status == fastGPTJobStatusFailed {
			now := time.Now()
			if err := repositories.FastGPTDatasetJobRepository.Updates(sqls.DB(), existing.ID, map[string]any{
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
		TaskKey:   taskKey,
		CompanyID: store.CompanyID,
		StoreID:   storeID,
		Action:    fastGPTJobActionCreateDataset,
		Status:    fastGPTJobStatusPending,
		Filename:  strings.TrimSpace(firstNonBlank(name, store.Name)),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repositories.FastGPTDatasetJobRepository.Create(sqls.DB(), job); err != nil {
		return nil, err
	}
	return job, nil
}

func (s *fastGPTDatasetService) EnqueueUpload(knowledgeBaseID int64, file *multipart.FileHeader, operator *dto.AuthPrincipal) (*models.FastGPTDatasetJob, error) {
	knowledgeBase := KnowledgeBaseService.Get(knowledgeBaseID)
	if knowledgeBase == nil || knowledgeBase.Status != enums.StatusOk || knowledgeBase.StoreID <= 0 || knowledgeBase.DatasetID == "" {
		return nil, errorsx.InvalidParam("知识库尚未完成 FastGPT 数据集配置")
	}
	if err := s.requireStoreAccess(knowledgeBase.StoreID, operator); err != nil {
		return nil, err
	}
	asset, err := AssetService.UploadFile(file, fmt.Sprintf("fastgpt-upload-tmp/%d", knowledgeBase.StoreID), operator)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	job := &models.FastGPTDatasetJob{
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
		_ = AssetService.DeleteTemporaryAsset(asset.AssetID)
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
	connector, err := NewPlatformFastGPTConnector()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if job.CollectionID != "" || job.Status == fastGPTJobStatusParsing || job.Status == fastGPTJobStatusIndexing {
		return s.pollUpload(ctx, connector, job)
	}
	now := time.Now()
	_ = repositories.FastGPTDatasetJobRepository.Updates(sqls.DB(), job.ID, map[string]any{"status": fastGPTJobStatusUploading, "started_at": now, "attempt_count": job.AttemptCount + 1, "updated_at": now})
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
	dataset, err := connector.CreateDataset(ctx, job.Filename, "Agent Desk 门店知识库")
	if err != nil {
		s.recordJobUsage(job, "dataset_create", 0, time.Since(startedAt).Milliseconds(), err)
		return err
	}
	s.recordJobUsage(job, "dataset_create", 0, time.Since(startedAt).Milliseconds(), nil)
	store := StoreService.Get(job.StoreID)
	if store == nil {
		return errorsx.InvalidParam("门店不存在")
	}
	now := time.Now()
	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		knowledgeBase := &models.KnowledgeBase{
			CompanyID:             store.CompanyID,
			StoreID:               store.ID,
			DatasetID:             dataset.ID,
			DatasetName:           firstNonBlank(dataset.Name, job.Filename),
			ConnectionID:          "platform",
			Name:                  firstNonBlank(dataset.Name, job.Filename),
			KnowledgeType:         string(enums.KnowledgeBaseTypeFastGPTCloud),
			Status:                enums.StatusOk,
			DefaultTopK:           10,
			DefaultScoreThreshold: 0.2,
			AnswerMode:            int(enums.KnowledgeAnswerModeStrict),
			AuditFields: models.AuditFields{
				CreatedAt: now, CreateUserName: "fastgpt_dataset_job", UpdatedAt: now, UpdateUserName: "fastgpt_dataset_job",
			},
		}
		if err := repositories.KnowledgeBaseRepository.Create(tx.Tx, knowledgeBase); err != nil {
			return err
		}
		if err := repositories.StoreRepository.Updates(tx.Tx, store.ID, map[string]any{"knowledge_base_id": knowledgeBase.ID, "updated_at": now, "update_user_name": "fastgpt_dataset_job"}); err != nil {
			return err
		}
		if err := repositories.WxWorkProtocolInstanceRepository.UpdateKnowledgeBaseByStore(tx.Tx, store.ID, knowledgeBase.ID, now, "fastgpt_dataset_job"); err != nil {
			return err
		}
		return repositories.FastGPTDatasetJobRepository.Updates(tx.Tx, job.ID, map[string]any{
			"status": fastGPTJobStatusReady, "dataset_id": dataset.ID, "knowledge_base_id": knowledgeBase.ID, "completed_at": now, "last_error": "", "updated_at": now,
		})
	})
}

func (s *fastGPTDatasetService) uploadFile(ctx context.Context, connector *FastGPTConnector, job *models.FastGPTDatasetJob) error {
	asset := AssetService.GetByAssetID(job.TemporaryAssetID)
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
	return repositories.FastGPTDatasetJobRepository.Updates(sqls.DB(), job.ID, map[string]any{
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
		errorMessage = callErr.Error()
	}
	trainingCount := int64(0)
	if operationType == "knowledge_upload" {
		trainingCount = 1
	}
	_ = AIUsageEventService.Record(models.AIUsageEvent{
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
			return repositories.FastGPTDatasetJobRepository.Updates(sqls.DB(), job.ID, map[string]any{"status": fastGPTJobStatusIndexing, "next_retry_at": next, "updated_at": time.Now()})
		}
		now := time.Now()
		if err := repositories.FastGPTDatasetJobRepository.Updates(sqls.DB(), job.ID, map[string]any{"status": fastGPTJobStatusReady, "completed_at": now, "next_retry_at": nil, "last_error": "", "updated_at": now}); err != nil {
			return err
		}
		return AssetService.DeleteTemporaryAsset(job.TemporaryAssetID)
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
			_ = AssetService.DeleteTemporaryAsset(job.TemporaryAssetID)
		}
	} else {
		when := time.Now().Add(time.Duration(attempts*attempts) * 30 * time.Second)
		next = &when
	}
	_ = repositories.FastGPTDatasetJobRepository.Updates(sqls.DB(), job.ID, map[string]any{
		"status": status, "attempt_count": attempts, "next_retry_at": next, "last_error": truncateText(cause.Error(), 2000), "updated_at": time.Now(),
	})
}

func (s *fastGPTDatasetService) ListCollections(ctx context.Context, knowledgeBaseID int64, operator *dto.AuthPrincipal) ([]response.FastGPTCollectionResponse, error) {
	kb, err := s.requireKnowledgeBaseAccess(knowledgeBaseID, operator)
	if err != nil {
		return nil, err
	}
	connector, err := NewPlatformFastGPTConnector()
	if err != nil {
		return nil, err
	}
	collections, err := connector.ListCollections(ctx, kb.DatasetID)
	if err != nil {
		return nil, err
	}
	ret := make([]response.FastGPTCollectionResponse, 0, len(collections))
	for _, item := range collections {
		ret = append(ret, response.FastGPTCollectionResponse{ID: item.ID, Name: item.Name, Type: item.Type, DataAmount: item.DataAmount, TrainingAmount: item.TrainingAmount, Forbid: item.Forbid})
	}
	return ret, nil
}

func (s *fastGPTDatasetService) SearchTest(ctx context.Context, knowledgeBaseID int64, query string, operator *dto.AuthPrincipal) (*FastGPTSearchResult, error) {
	kb, err := s.requireKnowledgeBaseAccess(knowledgeBaseID, operator)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, errorsx.InvalidParam("请输入检索问题")
	}
	connector, err := NewPlatformFastGPTConnector()
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	result, err := connector.SearchTest(ctx, kb.DatasetID, query)
	status := "completed"
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = err.Error()
	}
	_ = AIUsageEventService.Record(models.AIUsageEvent{
		EventKey:  "fastgpt-search-test:" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		CompanyID: kb.CompanyID, StoreID: kb.StoreID, KnowledgeBaseID: kb.ID,
		Stage: "knowledge_search_test", Provider: "fastgpt", OperationType: "knowledge_retrieve",
		RequestCount: 1, RerankCount: 1, MetricSource: AIUsageMetricSourceProviderOperation,
		LatencyMS: time.Since(startedAt).Milliseconds(), Status: status, ErrorMessage: errorMessage,
	})
	return result, err
}

func (s *fastGPTDatasetService) DeleteCollection(ctx context.Context, knowledgeBaseID int64, collectionID string, operator *dto.AuthPrincipal) error {
	kb, err := s.requireKnowledgeBaseAccess(knowledgeBaseID, operator)
	if err != nil {
		return err
	}
	if strings.TrimSpace(collectionID) == "" {
		return errorsx.InvalidParam("请选择要删除的文件")
	}
	connector, err := NewPlatformFastGPTConnector()
	if err != nil {
		return err
	}
	startedAt := time.Now()
	err = connector.DeleteCollections(ctx, []string{strings.TrimSpace(collectionID)})
	status := "completed"
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = err.Error()
	}
	_ = AIUsageEventService.Record(models.AIUsageEvent{
		EventKey:  "fastgpt-delete:" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		CompanyID: kb.CompanyID, StoreID: kb.StoreID, KnowledgeBaseID: kb.ID,
		Stage: "knowledge_manage", Provider: "fastgpt", OperationType: "knowledge_delete",
		RequestCount: 1, MetricSource: AIUsageMetricSourceProviderOperation,
		LatencyMS: time.Since(startedAt).Milliseconds(), Status: status, ErrorMessage: errorMessage,
	})
	return err
}

func (s *fastGPTDatasetService) requireKnowledgeBaseAccess(knowledgeBaseID int64, operator *dto.AuthPrincipal) (*models.KnowledgeBase, error) {
	kb := KnowledgeBaseService.Get(knowledgeBaseID)
	if kb == nil || kb.Status != enums.StatusOk || kb.StoreID <= 0 || kb.DatasetID == "" {
		return nil, errorsx.InvalidParam("FastGPT 知识库不存在或未启用")
	}
	if err := s.requireStoreAccess(kb.StoreID, operator); err != nil {
		return nil, err
	}
	return kb, nil
}

func (s *fastGPTDatasetService) requireStoreAccess(storeID int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if scope.Unrestricted {
		return nil
	}
	for _, allowedStoreID := range scope.StoreIDs {
		if allowedStoreID == storeID {
			return nil
		}
	}
	return errorsx.Forbidden("无权限管理该门店的 FastGPT 知识库")
}

func buildFastGPTJobResponse(job *models.FastGPTDatasetJob) response.FastGPTDatasetJobResponse {
	return response.FastGPTDatasetJobResponse{
		ID: job.ID, StoreID: job.StoreID, KnowledgeBaseID: job.KnowledgeBaseID, Action: job.Action, Status: job.Status, DatasetID: job.DatasetID, CollectionID: job.CollectionID, Filename: job.Filename, AttemptCount: job.AttemptCount, NextRetryAt: job.NextRetryAt, LastError: job.LastError, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
	}
}
