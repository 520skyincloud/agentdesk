package services

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"mime/multipart"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	fastGPTJobActionCreateDataset = "create_dataset"
	fastGPTJobActionAdoptDataset  = "adopt_dataset"
	fastGPTJobActionUploadFile    = "upload_file"
	fastGPTJobActionSyncProfile   = "sync_profile"
	fastGPTJobStatusPending       = "pending"
	fastGPTJobStatusUploading     = "uploading"
	fastGPTJobStatusParsing       = "parsing"
	fastGPTJobStatusIndexing      = "indexing"
	fastGPTJobStatusReady         = "ready"
	fastGPTJobStatusFailed        = "failed"
	fastGPTJobLeaseDuration       = 3 * time.Minute
)

var errFastGPTJobTargetChanged = errors.New("FastGPT job target revision changed")

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

func (s *fastGPTDatasetService) EnqueueDefaultDataset(storeID, bindingID int64, name string, operator *dto.AuthPrincipal) (*models.FastGPTDatasetJob, error) {
	store, err := s.requireStoreAccess(storeID, operator)
	if err != nil {
		return nil, err
	}
	return s.enqueueDefaultDataset(store, bindingID, name)
}

func (s *fastGPTDatasetService) EnqueueDefaultDatasetForRemoteSetup(storeID, tenantID, bindingID int64, name string) (*models.FastGPTDatasetJob, error) {
	store := StoreService.GetInTenant(storeID, tenantID)
	return s.enqueueDefaultDataset(store, bindingID, name)
}

// AdoptManagedDataset attaches a pre-existing Dataset that the FastGPT
// integration has already proven belongs to this Store's managed Team. It is
// a recovery/import path and does not claim that the Store model credential is
// ready; normal runtime readiness checks remain unchanged.
func (s *fastGPTDatasetService) AdoptManagedDataset(
	ctx context.Context,
	storeID int64,
	datasetID string,
	expectedDatasetName string,
	verificationQuery string,
	operator *dto.AuthPrincipal,
) (*response.AdoptFastGPTDatasetResponse, error) {
	store, err := s.requireStoreAccess(storeID, operator)
	if err != nil {
		return nil, err
	}
	datasetID = strings.TrimSpace(datasetID)
	expectedDatasetName = strings.TrimSpace(expectedDatasetName)
	verificationQuery = strings.TrimSpace(verificationQuery)
	if datasetID == "" || expectedDatasetName == "" || verificationQuery == "" {
		return nil, errorsx.InvalidParam("请提供已有 FastGPT 数据集、准确名称和验收问题")
	}
	connector, err := NewManagedStoreFastGPTConnector()
	if err != nil {
		return nil, err
	}
	connector = connector.ForStore(store.ID)
	dataset, err := connector.GetDataset(ctx, datasetID)
	if err != nil {
		return nil, publicFastGPTError(err)
	}
	if dataset == nil || strings.TrimSpace(dataset.ID) != datasetID || strings.TrimSpace(dataset.Name) != expectedDatasetName {
		return nil, errorsx.InvalidParam("FastGPT 数据集名称或门店归属校验失败")
	}
	collections, err := connector.ListCollections(ctx, datasetID)
	if err != nil {
		return nil, publicFastGPTError(err)
	}
	dataAmount := 0
	trainingAmount := 0
	for _, collection := range collections {
		dataAmount += collection.DataAmount
		trainingAmount += collection.TrainingAmount
	}
	if len(collections) == 0 || dataAmount <= 0 {
		return nil, errorsx.InvalidParam("FastGPT 数据集没有可接入的知识内容")
	}
	if trainingAmount > 0 {
		return nil, errorsx.InvalidParam("FastGPT 数据集仍在索引，请完成后重试")
	}
	profile, err := connector.GetDatasetProfileSnapshot(ctx, datasetID)
	if err != nil {
		return nil, publicFastGPTError(err)
	}
	if profile == nil || strings.TrimSpace(profile.ProfileID) == "" || strings.TrimSpace(profile.ProfileRevision) == "" {
		return nil, errorsx.InvalidParam("FastGPT 数据集缺少可验证的模型配置")
	}
	profileStatus := strings.ToLower(strings.TrimSpace(profile.ProfileStatus))
	if profileStatus != "configured" && profileStatus != "ready" {
		return nil, errorsx.InvalidParam("FastGPT 数据集模型配置尚未就绪")
	}
	searchResult, err := connector.SearchTest(ctx, datasetID, verificationQuery)
	if err != nil {
		return nil, publicFastGPTError(err)
	}
	if searchResult == nil || len(searchResult.Hits) == 0 {
		return nil, errorsx.InvalidParam("FastGPT 数据集尚未产生可检索结果")
	}

	now := time.Now()
	var knowledgeBase *models.KnowledgeBase
	if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		lockedStore, err := repositories.StoreRepository.GetForUpdateInTenant(tx.Tx, store.ID, store.TenantID)
		if err != nil {
			return err
		}
		if lockedStore == nil || lockedStore.Status != enums.StatusOk {
			return errorsx.InvalidParam("门店不存在、已停用或归属已变化")
		}
		if lockedStore.KnowledgeBaseID > 0 {
			current := repositories.KnowledgeBaseRepository.GetInTenant(tx.Tx, lockedStore.KnowledgeBaseID, store.TenantID)
			if current == nil || current.StoreID != store.ID || current.DatasetID != datasetID ||
				current.ConnectionID != fastgptapi.ManagedConnectionID || current.Status != enums.StatusOk {
				return errorsx.InvalidParam("门店已有其他启用知识库，不能接入该 FastGPT 数据集")
			}
			knowledgeBase = current
		} else {
			knowledgeBase = repositories.KnowledgeBaseRepository.FindOne(tx.Tx, sqls.NewCnd().
				Eq("tenant_id", store.TenantID).Eq("store_id", store.ID).Eq("dataset_id", datasetID))
			if knowledgeBase != nil && knowledgeBase.Status != enums.StatusOk {
				return errorsx.InvalidParam("该 FastGPT 数据集存在已停用的历史接入记录")
			}
			if knowledgeBase != nil && knowledgeBase.ConnectionID != fastgptapi.ManagedConnectionID {
				return errorsx.InvalidParam("该知识库不是门店 FastGPT 托管连接")
			}
			if knowledgeBase == nil {
				knowledgeBase = &models.KnowledgeBase{
					TenantID: store.TenantID, StoreID: store.ID,
					DatasetID: datasetID, DatasetName: dataset.Name, ConnectionID: fastgptapi.ManagedConnectionID,
					FastGPTProfileID: profile.ProfileID, FastGPTProfileName: profile.ProfileName,
					FastGPTProfileRevision: profile.ProfileRevision, FastGPTProfileFingerprint: fastGPTSnapshotFingerprint(profile.Fingerprint),
					FastGPTProfileStatus: profile.ProfileStatus, FastGPTProfileSyncedAt: &now,
					Name: dataset.Name, Description: "接入已有 FastGPT 门店托管知识库",
					KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud), Status: enums.StatusOk,
					DefaultTopK: 10, DefaultScoreThreshold: 0.2, DefaultRerankLimit: 5,
					AnswerMode: int(enums.KnowledgeAnswerModeStrict),
					AuditFields: models.AuditFields{
						CreatedAt: now, CreateUserID: operator.UserID, CreateUserName: operator.Username,
						UpdatedAt: now, UpdateUserID: operator.UserID, UpdateUserName: operator.Username,
					},
				}
				if err := repositories.KnowledgeBaseRepository.Create(tx.Tx, knowledgeBase); err != nil {
					return err
				}
			}
		}
		if err := repositories.StoreRepository.UpdatesInTenant(tx.Tx, store.ID, store.TenantID, map[string]any{
			"knowledge_base_id": knowledgeBase.ID, "updated_at": now,
			"update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		if err := repositories.ConversationRouteStateRepository.UpdateKnowledgeBaseByStoreInTenant(
			tx.Tx, store.ID, knowledgeBase.ID, store.TenantID, now, operator.Username,
		); err != nil {
			return err
		}
		taskSum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", store.TenantID, store.ID, datasetID)))
		taskKey := fmt.Sprintf("fastgpt-adopt-%x", taskSum[:16])
		existingJob := repositories.FastGPTDatasetJobRepository.Take(tx.Tx, "tenant_id = ? AND task_key = ?", store.TenantID, taskKey)
		if existingJob != nil {
			if existingJob.StoreID != store.ID || existingJob.KnowledgeBaseID != knowledgeBase.ID || existingJob.DatasetID != datasetID ||
				existingJob.Action != fastGPTJobActionAdoptDataset || existingJob.Status != fastGPTJobStatusReady || existingJob.CompletedAt == nil {
				return errorsx.InvalidParam("FastGPT 数据集历史接入任务状态不一致")
			}
		} else {
			completedAt := now
			job := &models.FastGPTDatasetJob{
				TenantID: store.TenantID, TaskKey: taskKey, StoreID: store.ID,
				KnowledgeBaseID: knowledgeBase.ID, Action: fastGPTJobActionAdoptDataset,
				Status: fastGPTJobStatusReady, DatasetID: datasetID, Filename: dataset.Name,
				CompletedAt: &completedAt, CreatedAt: now, UpdatedAt: now,
			}
			if err := repositories.FastGPTDatasetJobRepository.Create(tx.Tx, job); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	publishFastGPTConfigurationState(store.TenantID, store.ID, now)
	return &response.AdoptFastGPTDatasetResponse{
		KnowledgeBaseID: knowledgeBase.ID, Name: knowledgeBase.Name,
		CollectionCount: len(collections), DataAmount: dataAmount,
		ProfileStatus: profile.ProfileStatus,
	}, nil
}

func fastGPTSnapshotFingerprint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func (s *fastGPTDatasetService) enqueueDefaultDataset(store *models.Store, bindingID int64, name string) (*models.FastGPTDatasetJob, error) {
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在")
	}
	if store.TenantID <= 0 {
		return nil, errorsx.InvalidParam("门店缺少接入公司归属")
	}
	target, credential, err := s.resolveJobTarget(store.TenantID, store.ID, bindingID)
	if err != nil {
		return nil, err
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
				"status":                        fastGPTJobStatusPending,
				"attempt_count":                 0,
				"next_retry_at":                 now,
				"started_at":                    nil,
				"completed_at":                  nil,
				"last_error":                    "",
				"last_error_class":              "",
				"target_profile_id":             target.Template.ID,
				"target_profile_revision":       target.Template.Revision,
				"target_store_staff_binding_id": credential.StoreStaffBindingID,
				"target_credential_revision":    credential.Revision,
				"lease_owner":                   "", "lease_expires_at": nil,
				"updated_at": now,
			}); err != nil {
				return nil, err
			}
			existing.Status = fastGPTJobStatusPending
			existing.AttemptCount = 0
			existing.NextRetryAt = &now
			existing.StartedAt = nil
			existing.CompletedAt = nil
			existing.LastError = ""
			existing.LastErrorClass = ""
			existing.TargetProfileID = target.Template.ID
			existing.TargetProfileRevision = target.Template.Revision
			existing.TargetStoreStaffBindingID = credential.StoreStaffBindingID
			existing.TargetCredentialRevision = credential.Revision
			existing.LeaseOwner = ""
			existing.LeaseExpiresAt = nil
			existing.UpdatedAt = now
		}
		if existing.TargetStoreStaffBindingID != credential.StoreStaffBindingID || existing.TargetProfileID != target.Template.ID || existing.TargetProfileRevision != target.Template.Revision || existing.TargetCredentialRevision != credential.Revision {
			return nil, errorsx.InvalidParam("已有 FastGPT 任务的模型目标已变化，请等待旧任务结束后重试")
		}
		return existing, nil
	}
	now := time.Now()
	job := &models.FastGPTDatasetJob{
		TenantID:                  store.TenantID,
		TaskKey:                   taskKey,
		StoreID:                   store.ID,
		TargetStoreStaffBindingID: credential.StoreStaffBindingID,
		Action:                    fastGPTJobActionCreateDataset,
		Status:                    fastGPTJobStatusPending,
		Filename:                  name,
		TargetProfileID:           target.Template.ID,
		TargetProfileRevision:     target.Template.Revision,
		TargetCredentialRevision:  credential.Revision,
		CreatedAt:                 now,
		UpdatedAt:                 now,
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
	target, credential, err := s.resolveJobTarget(knowledgeBase.TenantID, knowledgeBase.StoreID, knowledgeBase.FastGPTAppliedStoreStaffBindingID)
	if err != nil {
		return nil, err
	}
	if err := s.requireDatasetTarget(knowledgeBase, target, credential); err != nil {
		return nil, err
	}
	asset, err := AssetService.UploadFile(file, fmt.Sprintf("fastgpt-upload-tmp/%d", knowledgeBase.StoreID), operator)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	job := &models.FastGPTDatasetJob{
		TenantID:                  knowledgeBase.TenantID,
		TaskKey:                   "fastgpt-upload-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		StoreID:                   knowledgeBase.StoreID,
		TargetStoreStaffBindingID: credential.StoreStaffBindingID,
		KnowledgeBaseID:           knowledgeBase.ID,
		Action:                    fastGPTJobActionUploadFile,
		Status:                    fastGPTJobStatusPending,
		DatasetID:                 knowledgeBase.DatasetID,
		Filename:                  file.Filename,
		TemporaryAssetID:          asset.AssetID,
		TargetProfileID:           target.Template.ID,
		TargetProfileRevision:     target.Template.Revision,
		TargetCredentialRevision:  credential.Revision,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}
	if err := repositories.FastGPTDatasetJobRepository.Create(sqls.DB(), job); err != nil {
		_ = AssetService.DeleteTemporaryAsset(asset.AssetID, knowledgeBase.TenantID)
		return nil, err
	}
	return job, nil
}

func (s *fastGPTDatasetService) EnqueueProfileSync(storeID int64, operator *dto.AuthPrincipal) (*models.FastGPTDatasetJob, error) {
	store, err := s.requireStoreAccess(storeID, operator)
	if err != nil {
		return nil, err
	}
	bindingID := int64(0)
	if state := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), store.ID, store.TenantID); state != nil {
		bindingID = firstPositiveInt64(state.TargetStoreStaffBindingID, state.AppliedStoreStaffBindingID)
	}
	target, credential, err := s.resolveJobTarget(store.TenantID, store.ID, bindingID)
	if err != nil {
		return nil, err
	}
	knowledgeBase := repositories.KnowledgeBaseRepository.GetInTenant(sqls.DB(), store.KnowledgeBaseID, store.TenantID)
	if knowledgeBase == nil || strings.TrimSpace(knowledgeBase.DatasetID) == "" {
		return nil, errorsx.InvalidParam("当前门店尚无可同步的 FastGPT 托管知识库")
	}
	if knowledgeBase.StoreID != store.ID || knowledgeBase.Status != enums.StatusOk || knowledgeBase.ConnectionID != fastgptapi.ManagedConnectionID {
		return nil, errorsx.InvalidParam("当前门店权威知识库不是 FastGPT 托管知识库")
	}
	now := time.Now()
	job := &models.FastGPTDatasetJob{
		TenantID: store.TenantID, TaskKey: "fastgpt-profile-sync-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		StoreID: store.ID, KnowledgeBaseID: knowledgeBase.ID, DatasetID: knowledgeBase.DatasetID,
		TargetStoreStaffBindingID: credential.StoreStaffBindingID,
		Action:                    fastGPTJobActionSyncProfile, Status: fastGPTJobStatusPending,
		Filename: knowledgeBase.Name, TargetProfileID: target.Template.ID,
		TargetProfileRevision: target.Template.Revision, TargetCredentialRevision: credential.Revision,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repositories.FastGPTDatasetJobRepository.Create(sqls.DB(), job); err != nil {
		return nil, err
	}
	return job, nil
}

func (s *fastGPTDatasetService) ProcessDue(limit int) int {
	if limit <= 0 {
		limit = 10
	}
	now := time.Now()
	leaseOwner := "fastgpt-worker-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	jobs, err := repositories.FastGPTDatasetJobRepository.ClaimDue(sqls.DB(), []string{
		fastGPTJobStatusPending, fastGPTJobStatusUploading, fastGPTJobStatusParsing, fastGPTJobStatusIndexing,
	}, now, now.Add(fastGPTJobLeaseDuration), leaseOwner, limit)
	if err != nil {
		return 0
	}
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
	if job == nil || job.TenantID <= 0 || strings.TrimSpace(job.LeaseOwner) == "" {
		return errorsx.InvalidParam("FastGPT 任务缺少接入公司归属")
	}
	target, credential, err := s.resolveClaimedJobTarget(job)
	if err != nil {
		return err
	}
	connector, err := NewManagedStoreFastGPTConnector()
	if err != nil {
		return err
	}
	if job.Action != fastGPTJobActionCreateDataset {
		knowledgeBase := KnowledgeBaseService.GetInTenant(job.KnowledgeBaseID, job.TenantID)
		if knowledgeBase == nil || knowledgeBase.StoreID != job.StoreID || strings.TrimSpace(knowledgeBase.ConnectionID) != fastgptapi.ManagedConnectionID {
			return errorsx.InvalidParam("知识库不存在")
		}
	}
	connector = connector.ForStore(job.StoreID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if job.CollectionID != "" || job.Status == fastGPTJobStatusParsing || job.Status == fastGPTJobStatusIndexing {
		return s.pollUpload(ctx, connector, job)
	}
	now := time.Now()
	if err := repositories.FastGPTDatasetJobRepository.UpdatesClaimed(sqls.DB(), job.ID, job.TenantID, job.LeaseOwner, map[string]any{
		"status": fastGPTJobStatusUploading, "started_at": now, "updated_at": now,
	}); err != nil {
		return err
	}
	var processErr error
	switch job.Action {
	case fastGPTJobActionCreateDataset:
		processErr = s.createDataset(ctx, connector, job, target, credential)
	case fastGPTJobActionUploadFile:
		processErr = s.uploadFile(ctx, connector, job)
	case fastGPTJobActionSyncProfile:
		processErr = s.syncProfile(ctx, connector, job, target, credential)
	default:
		processErr = errorsx.InvalidParam("未知 FastGPT 任务类型")
	}
	if processErr == nil {
		publishFastGPTConfigurationState(job.TenantID, job.StoreID, time.Now())
	}
	return processErr
}

func (s *fastGPTDatasetService) createDataset(ctx context.Context, connector *FastGPTConnector, job *models.FastGPTDatasetJob, target *storeCredentialActivationTarget, credential *resolvedStoreModelCredential) error {
	startedAt := time.Now()
	store := StoreService.GetInTenant(job.StoreID, job.TenantID)
	if store == nil {
		return errorsx.InvalidParam("门店不存在")
	}
	if target == nil || credential == nil {
		return errFastGPTJobTargetChanged
	}
	if err := s.ensureStoreTenant(ctx, connector, store, target, credential); err != nil {
		s.recordJobUsage(job, "tenant_ensure", 0, time.Since(startedAt).Milliseconds(), err)
		return err
	}
	dataset := &FastGPTDataset{ID: strings.TrimSpace(job.DatasetID), Name: job.Filename}
	if dataset.ID == "" {
		created, err := connector.CreateDataset(ctx, job.Filename, "Agent Desk 门店知识库")
		if err != nil {
			s.recordJobUsage(job, "dataset_create", 0, time.Since(startedAt).Milliseconds(), err)
			return err
		}
		dataset = created
		job.DatasetID = dataset.ID
		if err := repositories.FastGPTDatasetJobRepository.UpdatesClaimed(sqls.DB(), job.ID, job.TenantID, job.LeaseOwner, map[string]any{
			"dataset_id": dataset.ID, "updated_at": time.Now(),
		}); err != nil {
			return err
		}
		s.recordJobUsage(job, "dataset_create", 0, time.Since(startedAt).Milliseconds(), nil)
	} else {
		remote, err := connector.GetDataset(ctx, dataset.ID)
		if err != nil {
			return err
		}
		if remote != nil {
			dataset = remote
		}
	}
	profile, syncClass, err := syncManagedStoreFastGPTProfile(ctx, connector, *target, credential.APIKey, dataset.ID)
	if err != nil {
		return &storeCredentialFastGPTSyncError{Class: syncClass}
	}
	if _, err := connector.SearchTest(ctx, dataset.ID, "Agent Desk readiness check"); err != nil {
		return err
	}
	if _, _, err := s.resolveClaimedJobTarget(job); err != nil {
		return err
	}
	now := time.Now()
	systemOperator := operatorOrSystem(nil)
	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := requireCurrentFastGPTJobTargetDB(tx.Tx, *target, credential); err != nil {
			return err
		}
		knowledgeBase := repositories.KnowledgeBaseRepository.FindOne(tx.Tx, sqls.NewCnd().
			Eq("tenant_id", job.TenantID).Eq("store_id", store.ID).Eq("dataset_id", dataset.ID))
		if knowledgeBase == nil {
			knowledgeBase = &models.KnowledgeBase{
				TenantID: job.TenantID, StoreID: store.ID,
				DatasetID: dataset.ID, DatasetName: firstNonBlank(dataset.Name, job.Filename),
				ConnectionID: fastgptapi.ManagedConnectionID,
				Name:         firstNonBlank(dataset.Name, job.Filename), KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud),
				Status: enums.StatusOk, DefaultTopK: 10, DefaultScoreThreshold: 0.2, DefaultRerankLimit: 5,
				AnswerMode:       int(enums.KnowledgeAnswerModeStrict),
				FastGPTProfileID: profile.ID, FastGPTProfileName: profile.Name,
				FastGPTProfileRevision: strconv.FormatInt(profile.Revision, 10), FastGPTProfileStatus: "ready", FastGPTProfileSyncedAt: &now,
				AuditFields: models.AuditFields{CreatedAt: now, CreateUserName: "fastgpt_dataset_job", UpdatedAt: now, UpdateUserName: "fastgpt_dataset_job"},
			}
			if err := repositories.KnowledgeBaseRepository.Create(tx.Tx, knowledgeBase); err != nil {
				return err
			}
		}
		lockedStore, err := repositories.StoreRepository.GetForUpdateInTenant(tx.Tx, store.ID, job.TenantID)
		if err != nil || lockedStore == nil {
			return errors.New("FastGPT store disappeared during provisioning")
		}
		if lockedStore.KnowledgeBaseID <= 0 || lockedStore.KnowledgeBaseID == knowledgeBase.ID {
			if err := commitManagedStoreFastGPTProfileDB(tx.Tx, *target, credential.Revision, credential.Fingerprint, profile, knowledgeBase.ID, systemOperator, now); err != nil {
				return err
			}
		} else if err := commitManagedKnowledgeBaseFastGPTProfileDB(tx.Tx, *target, credential.Revision, profile, knowledgeBase.ID, systemOperator, now); err != nil {
			return err
		}
		if lockedStore.KnowledgeBaseID <= 0 {
			if err := repositories.StoreRepository.UpdatesInTenant(tx.Tx, store.ID, job.TenantID, map[string]any{
				"knowledge_base_id": knowledgeBase.ID, "updated_at": now, "update_user_name": "fastgpt_dataset_job",
			}); err != nil {
				return err
			}
			if err := repositories.ConversationRouteStateRepository.UpdateKnowledgeBaseByStoreInTenant(tx.Tx, store.ID, knowledgeBase.ID, job.TenantID, now, "fastgpt_dataset_job"); err != nil {
				return err
			}
		}
		return repositories.FastGPTDatasetJobRepository.UpdatesClaimed(tx.Tx, job.ID, job.TenantID, job.LeaseOwner, map[string]any{
			"status": fastGPTJobStatusReady, "dataset_id": dataset.ID, "knowledge_base_id": knowledgeBase.ID,
			"completed_at": now, "next_retry_at": nil, "last_error": "", "last_error_class": "",
			"lease_owner": "", "lease_expires_at": nil, "updated_at": now,
		})
	})
}

func (s *fastGPTDatasetService) ensureStoreTenant(ctx context.Context, connector *FastGPTConnector, store *models.Store, target *storeCredentialActivationTarget, credential *resolvedStoreModelCredential) error {
	if connector == nil || store == nil || target == nil || credential == nil {
		return errorsx.InvalidParam("FastGPT 门店租户参数无效")
	}
	tenant, err := connector.EnsureStoreTenant(ctx, firstNonBlank(store.Name, "Agent Desk 门店"))
	if err != nil || tenant == nil {
		return err
	}
	return sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := requireCurrentFastGPTJobTargetDB(tx.Tx, *target, credential); err != nil {
			return err
		}
		now := time.Now()
		binding, err := repositories.FastGPTStoreTenantRepository.GetForUpdateByStoreIDInTenant(tx.Tx, store.ID, store.TenantID)
		if err != nil {
			return err
		}
		if binding == nil {
			candidate := &models.FastGPTStoreTenant{
				TenantID: store.TenantID, StoreID: store.ID,
				TenantTeamID: tenant.TeamID, TenantTeamName: firstNonBlank(tenant.TeamName, store.Name),
				Status:          firstNonBlank(tenant.Status, "active"),
				TargetProfileID: target.Template.ID, TargetProfileRevision: target.Template.Revision,
				TargetStoreStaffBindingID: credential.StoreStaffBindingID,
				TargetCredentialRevision:  credential.Revision, ReadinessStatus: "syncing",
				LastSyncedAt: &now, LastError: "",
				AuditFields: models.AuditFields{CreatedAt: now, CreateUserName: "fastgpt_integration", UpdatedAt: now, UpdateUserName: "fastgpt_integration"},
			}
			created, err := repositories.FastGPTStoreTenantRepository.CreateIfAbsent(tx.Tx, candidate)
			if err != nil {
				return err
			}
			if created {
				return nil
			}
			binding = repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(tx.Tx, store.ID, store.TenantID)
			if binding == nil {
				return errors.New("FastGPT Store binding changed during creation")
			}
		}
		readiness := binding.ReadinessStatus
		if readiness == "" {
			readiness = "syncing"
		}
		return repositories.FastGPTStoreTenantRepository.UpdatesInTenant(tx.Tx, binding.ID, store.TenantID, map[string]any{
			"tenant_team_id": tenant.TeamID, "tenant_team_name": firstNonBlank(tenant.TeamName, store.Name),
			"status": firstNonBlank(tenant.Status, "active"), "target_profile_id": target.Template.ID,
			"target_profile_revision": target.Template.Revision, "target_credential_revision": credential.Revision,
			"target_store_staff_binding_id": credential.StoreStaffBindingID,
			"readiness_status":              readiness, "last_synced_at": now, "last_error": "", "updated_at": now,
			"update_user_name": "fastgpt_integration",
		})
	})
}

// ActivateKnowledgeBase switches the one current knowledge base owned by a
// Store. WxWork rows remain a compatibility projection and cannot override it.
func (s *fastGPTDatasetService) ActivateKnowledgeBase(storeID, knowledgeBaseID int64, operator *dto.AuthPrincipal) error {
	if storeID <= 0 || knowledgeBaseID <= 0 {
		return errorsx.InvalidParam("请选择门店和知识库")
	}
	tenantID, err := requireActiveTenantID(operator, "FastGPT 知识库")
	if err != nil {
		return err
	}
	store, err := s.requireStoreAccess(storeID, operator)
	if err != nil {
		return err
	}
	knowledgeBase := KnowledgeBaseService.GetInTenant(knowledgeBaseID, tenantID)
	if knowledgeBase == nil || knowledgeBase.Status != enums.StatusOk || knowledgeBase.StoreID != store.ID || knowledgeBase.DatasetID == "" || knowledgeBase.ConnectionID != fastgptapi.ManagedConnectionID {
		return errorsx.InvalidParam("只能启用当前门店已完成配置的 FastGPT 知识库")
	}
	state := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), store.ID, tenantID)
	bindingID := knowledgeBase.FastGPTAppliedStoreStaffBindingID
	if bindingID <= 0 && state != nil {
		bindingID = firstPositiveInt64(state.TargetStoreStaffBindingID, state.AppliedStoreStaffBindingID)
	}
	target, credential, err := s.resolveJobTarget(tenantID, store.ID, bindingID)
	if err != nil {
		return err
	}
	if err := s.requireDatasetTarget(knowledgeBase, target, credential); err != nil {
		return err
	}
	now := time.Now()
	if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := requireCurrentFastGPTJobTargetDB(tx.Tx, *target, credential); err != nil {
			return err
		}
		if lockedStore, err := repositories.StoreRepository.GetForUpdateInTenant(tx.Tx, store.ID, tenantID); err != nil || lockedStore == nil {
			return errors.New("FastGPT store disappeared during activation")
		}
		updated, err := repositories.FastGPTStoreTenantRepository.ApplyTargetRevisions(
			tx.Tx, tenantID, store.ID, credential.StoreStaffBindingID, target.Template.ID, target.Template.Revision, credential.Revision,
			map[string]any{
				"applied_profile_id": target.Template.ID, "applied_profile_revision": target.Template.Revision,
				"applied_store_staff_binding_id": credential.StoreStaffBindingID,
				"applied_credential_revision":    credential.Revision, "applied_key_fingerprint": credential.Fingerprint,
				"readiness_status": "ready", "last_synced_at": now, "last_error": "", "updated_at": now,
				"update_user_id": operator.UserID, "update_user_name": operator.Username,
			},
		)
		if err != nil {
			return err
		}
		if !updated {
			return errors.New("FastGPT target changed during knowledge-base activation")
		}
		if err := repositories.StoreRepository.UpdatesInTenant(tx.Tx, store.ID, tenantID, map[string]any{
			"knowledge_base_id": knowledgeBase.ID, "updated_at": now,
			"update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		if err := repositories.ConversationRouteStateRepository.UpdateKnowledgeBaseByStoreInTenant(tx.Tx, store.ID, knowledgeBase.ID, tenantID, now, operator.Username); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	publishFastGPTConfigurationState(tenantID, store.ID, now)
	return nil
}

func (s *fastGPTDatasetService) resolveJobTarget(tenantID, storeID, bindingID int64) (*storeCredentialActivationTarget, *resolvedStoreModelCredential, error) {
	if _, err := TenantIndustryService.ResolveTenantProfileDB(sqls.DB(), tenantID); err != nil {
		return nil, nil, err
	}
	target, err := StoreModelCredentialService.loadActiveTargetDB(sqls.DB(), tenantID, storeID)
	if err != nil || target == nil {
		return nil, nil, errorsx.BusinessError(2005, "门店 active 模型方案尚未就绪")
	}
	if target.Store.Status != enums.StatusOk || target.Assignment.Status != enums.StoreModelAssignmentStatusReady ||
		target.Template.Status != enums.ModelProfileStatusActive || target.Template.ID != target.Assignment.TemplateID ||
		target.Template.Revision != target.Assignment.TemplateRevision {
		return nil, nil, errorsx.BusinessError(2005, "门店 active 模型方案尚未就绪")
	}
	if issues := ValidateModelProfileForPublication(&target.Template, target.Slots); len(issues) > 0 {
		return nil, nil, errorsx.BusinessError(2005, "门店 active 模型方案九槽不完整")
	}
	binding, err := StoreModelCredentialService.requireStoreStaffCredentialScopeDB(sqls.DB(), tenantID, storeID, bindingID, true)
	if err != nil {
		return nil, nil, err
	}
	target.StoreStaffBindingID = binding.ID
	credential, err := StoreModelCredentialService.ResolveActiveForBinding(tenantID, storeID, binding.ID)
	if err != nil {
		return nil, nil, err
	}
	return target, credential, nil
}

func (s *fastGPTDatasetService) resolveClaimedJobTarget(job *models.FastGPTDatasetJob) (*storeCredentialActivationTarget, *resolvedStoreModelCredential, error) {
	if job == nil || job.TenantID <= 0 || job.StoreID <= 0 || job.TargetStoreStaffBindingID <= 0 || job.TargetProfileID <= 0 || job.TargetProfileRevision <= 0 || job.TargetCredentialRevision <= 0 {
		return nil, nil, errFastGPTJobTargetChanged
	}
	target, credential, err := s.resolveJobTarget(job.TenantID, job.StoreID, job.TargetStoreStaffBindingID)
	if err != nil {
		return nil, nil, err
	}
	if credential.StoreStaffBindingID != job.TargetStoreStaffBindingID || target.Template.ID != job.TargetProfileID || target.Template.Revision != job.TargetProfileRevision || credential.Revision != job.TargetCredentialRevision {
		return nil, nil, errFastGPTJobTargetChanged
	}
	return target, credential, nil
}

func (s *fastGPTDatasetService) requireAppliedTarget(knowledgeBase *models.KnowledgeBase, target *storeCredentialActivationTarget, credential *resolvedStoreModelCredential) error {
	if err := s.requireDatasetTarget(knowledgeBase, target, credential); err != nil {
		return err
	}
	if target.Store.KnowledgeBaseID != knowledgeBase.ID {
		return errorsx.BusinessError(2005, "门店 FastGPT 知识库不是当前启用版本")
	}
	binding := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), target.Store.ID, target.Store.TenantID)
	if binding == nil || binding.Status != "active" || binding.ReadinessStatus != "ready" ||
		binding.AppliedProfileID != target.Template.ID || binding.AppliedProfileRevision != target.Template.Revision ||
		binding.AppliedStoreStaffBindingID != credential.StoreStaffBindingID ||
		binding.AppliedCredentialRevision != credential.Revision {
		return errorsx.BusinessError(2005, "门店 FastGPT Profile 尚未同步到 active revision")
	}
	return nil
}

func (s *fastGPTDatasetService) requireDatasetTarget(knowledgeBase *models.KnowledgeBase, target *storeCredentialActivationTarget, credential *resolvedStoreModelCredential) error {
	if knowledgeBase == nil || target == nil || credential == nil || knowledgeBase.TenantID != target.Store.TenantID || knowledgeBase.StoreID != target.Store.ID ||
		knowledgeBase.ConnectionID != fastgptapi.ManagedConnectionID || knowledgeBase.FastGPTProfileStatus != "ready" ||
		knowledgeBase.FastGPTAppliedProfileID != target.Template.ID || knowledgeBase.FastGPTAppliedProfileRevision != target.Template.Revision ||
		knowledgeBase.FastGPTAppliedStoreStaffBindingID != credential.StoreStaffBindingID ||
		knowledgeBase.FastGPTAppliedCredentialRevision != credential.Revision {
		return errorsx.BusinessError(2005, "门店 FastGPT 知识库尚未就绪")
	}
	return nil
}

func (s *fastGPTDatasetService) syncProfile(ctx context.Context, connector *FastGPTConnector, job *models.FastGPTDatasetJob, target *storeCredentialActivationTarget, credential *resolvedStoreModelCredential) error {
	knowledgeBase := KnowledgeBaseService.GetInTenant(job.KnowledgeBaseID, job.TenantID)
	if knowledgeBase == nil || knowledgeBase.StoreID != job.StoreID || knowledgeBase.ConnectionID != fastgptapi.ManagedConnectionID || strings.TrimSpace(knowledgeBase.DatasetID) == "" {
		return errorsx.InvalidParam("FastGPT 托管知识库不存在")
	}
	if target == nil || target.Store.KnowledgeBaseID != knowledgeBase.ID {
		return errorsx.InvalidParam("只能同步当前门店权威知识库的 FastGPT Profile")
	}
	if err := s.ensureStoreTenant(ctx, connector, &target.Store, target, credential); err != nil {
		return err
	}
	profile, syncClass, err := syncManagedStoreFastGPTProfile(ctx, connector, *target, credential.APIKey, knowledgeBase.DatasetID)
	if err != nil {
		return &storeCredentialFastGPTSyncError{Class: syncClass}
	}
	if _, err := connector.SearchTest(ctx, knowledgeBase.DatasetID, "Agent Desk readiness check"); err != nil {
		return err
	}
	if _, _, err := s.resolveClaimedJobTarget(job); err != nil {
		return err
	}
	now := time.Now()
	systemOperator := operatorOrSystem(nil)
	if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := requireCurrentFastGPTJobTargetDB(tx.Tx, *target, credential); err != nil {
			return err
		}
		if err := commitManagedStoreFastGPTProfileDB(tx.Tx, *target, credential.Revision, credential.Fingerprint, profile, knowledgeBase.ID, systemOperator, now); err != nil {
			return err
		}
		return repositories.FastGPTDatasetJobRepository.UpdatesClaimed(tx.Tx, job.ID, job.TenantID, job.LeaseOwner, map[string]any{
			"status": fastGPTJobStatusReady, "completed_at": now, "next_retry_at": nil,
			"last_error": "", "last_error_class": "", "lease_owner": "", "lease_expires_at": nil, "updated_at": now,
		})
	}); err != nil {
		return err
	}
	publishFastGPTConfigurationState(job.TenantID, job.StoreID, now)
	return nil
}

func requireCurrentFastGPTJobTargetDB(db *gorm.DB, target storeCredentialActivationTarget, credential *resolvedStoreModelCredential) error {
	if db == nil || credential == nil || target.Store.TenantID <= 0 || target.Store.ID <= 0 {
		return errFastGPTJobTargetChanged
	}
	currentCredential, err := repositories.StoreModelCredentialRepository.GetForUpdateByBinding(db, target.Store.TenantID, target.Store.ID, credential.StoreStaffBindingID)
	if err != nil {
		return err
	}
	if currentCredential == nil || currentCredential.Status != enums.StoreCredentialStatusActive ||
		currentCredential.StoreStaffBindingID != target.StoreStaffBindingID ||
		currentCredential.CredentialRevision != credential.Revision || currentCredential.KeyFingerprint != credential.Fingerprint {
		return errFastGPTJobTargetChanged
	}
	assignment, err := repositories.StoreModelProfileAssignmentRepository.GetForUpdateByStore(db, target.Store.TenantID, target.Store.ID)
	if err != nil {
		return err
	}
	if assignment == nil || assignment.Status != enums.StoreModelAssignmentStatusReady ||
		assignment.TemplateID != target.Template.ID || assignment.TemplateRevision != target.Template.Revision {
		return errFastGPTJobTargetChanged
	}
	template, err := repositories.ModelProfileTemplateRepository.GetForUpdate(db, target.Template.ID)
	if err != nil {
		return err
	}
	if template == nil || template.Status != enums.ModelProfileStatusActive || template.Revision != target.Template.Revision {
		return errFastGPTJobTargetChanged
	}
	return nil
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
	if _, _, err := s.resolveClaimedJobTarget(job); err != nil {
		return err
	}
	next := time.Now().Add(15 * time.Second)
	return repositories.FastGPTDatasetJobRepository.UpdatesClaimed(sqls.DB(), job.ID, job.TenantID, job.LeaseOwner, map[string]any{
		"status": fastGPTJobStatusParsing, "collection_id": collectionID, "next_retry_at": next,
		"last_error": "", "last_error_class": "", "lease_owner": "", "lease_expires_at": nil, "updated_at": time.Now(),
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
		TenantID: job.TenantID,
		EventKey: fmt.Sprintf("fastgpt-job:%s:%s:%d", job.TaskKey, operationType, job.AttemptCount),
		StoreID:  job.StoreID, StoreStaffBindingID: job.TargetStoreStaffBindingID, KnowledgeBaseID: job.KnowledgeBaseID,
		ModelProfileID: job.TargetProfileID, ModelProfileRevision: job.TargetProfileRevision,
		CredentialRevision: job.TargetCredentialRevision,
		Stage:              "knowledge_manage", Provider: "fastgpt", OperationType: operationType,
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
			return repositories.FastGPTDatasetJobRepository.UpdatesClaimed(sqls.DB(), job.ID, job.TenantID, job.LeaseOwner, map[string]any{
				"status": fastGPTJobStatusIndexing, "next_retry_at": next,
				"lease_owner": "", "lease_expires_at": nil, "updated_at": time.Now(),
			})
		}
		if _, _, err := s.resolveClaimedJobTarget(job); err != nil {
			return err
		}
		now := time.Now()
		if err := repositories.FastGPTDatasetJobRepository.UpdatesClaimed(sqls.DB(), job.ID, job.TenantID, job.LeaseOwner, map[string]any{
			"status": fastGPTJobStatusReady, "completed_at": now, "next_retry_at": nil,
			"last_error": "", "last_error_class": "", "lease_owner": "", "lease_expires_at": nil, "updated_at": now,
		}); err != nil {
			return err
		}
		_ = AssetService.DeleteTemporaryAsset(job.TemporaryAssetID, job.TenantID)
		return nil
	}
	return errorsx.InvalidParam("FastGPT 未返回对应文件集合")
}

func (s *fastGPTDatasetService) failOrRetry(job *models.FastGPTDatasetJob, cause error) {
	if job == nil || strings.TrimSpace(job.LeaseOwner) == "" {
		return
	}
	attempts := job.AttemptCount + 1
	job.AttemptCount = attempts
	status := fastGPTJobStatusPending
	if job.CollectionID != "" {
		status = fastGPTJobStatusIndexing
	}
	now := time.Now()
	var next *time.Time
	var completedAt *time.Time
	if attempts >= 5 || errors.Is(cause, errFastGPTJobTargetChanged) {
		status = fastGPTJobStatusFailed
		completedAt = &now
		if job.TemporaryAssetID != "" {
			_ = AssetService.DeleteTemporaryAsset(job.TemporaryAssetID, job.TenantID)
		}
	} else {
		when := now.Add(time.Duration(attempts*attempts) * 30 * time.Second)
		next = &when
	}
	errorClass := fastGPTErrorClass(cause)
	if err := repositories.FastGPTDatasetJobRepository.UpdatesClaimed(sqls.DB(), job.ID, job.TenantID, job.LeaseOwner, map[string]any{
		"status": status, "attempt_count": attempts, "next_retry_at": next, "completed_at": completedAt,
		"last_error": errorClass, "last_error_class": errorClass,
		"lease_owner": "", "lease_expires_at": nil, "updated_at": now,
	}); err == nil {
		publishFastGPTConfigurationState(job.TenantID, job.StoreID, now)
	}
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

func (s *fastGPTDatasetService) GetReadiness(storeID int64, operator *dto.AuthPrincipal) (*response.FastGPTStoreReadinessResponse, error) {
	store, err := s.requireStoreAccess(storeID, operator)
	if err != nil {
		return nil, err
	}
	ret := &response.FastGPTStoreReadinessResponse{
		StoreID: store.ID, KnowledgeBaseID: store.KnowledgeBaseID,
		TeamStatus: "unconfigured", ReadinessStatus: "unconfigured",
	}
	binding := repositories.FastGPTStoreTenantRepository.GetByStoreIDInTenant(sqls.DB(), store.ID, store.TenantID)
	if binding == nil {
		return ret, nil
	}
	ret.TeamStatus = binding.Status
	ret.ReadinessStatus = binding.ReadinessStatus
	ret.TargetProfileID = binding.TargetProfileID
	ret.TargetProfileRevision = binding.TargetProfileRevision
	ret.AppliedProfileID = binding.AppliedProfileID
	ret.AppliedProfileRevision = binding.AppliedProfileRevision
	ret.TargetStoreStaffBindingID = binding.TargetStoreStaffBindingID
	ret.AppliedStoreStaffBindingID = binding.AppliedStoreStaffBindingID
	ret.TargetCredentialRevision = binding.TargetCredentialRevision
	ret.AppliedCredentialRevision = binding.AppliedCredentialRevision
	ret.LastSyncedAt = binding.LastSyncedAt
	ret.LastErrorClass = binding.LastError
	profileID := binding.TargetProfileID
	if profileID <= 0 {
		profileID = binding.AppliedProfileID
	}
	if profile := repositories.ModelProfileTemplateRepository.Get(sqls.DB(), profileID); profile != nil {
		ret.ModelProfileName = profile.Name
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
		TenantID: kb.TenantID,
		EventKey: "fastgpt-search-test:" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		StoreID:  kb.StoreID, KnowledgeBaseID: kb.ID,
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
		TenantID: kb.TenantID,
		EventKey: "fastgpt-delete:" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		StoreID:  kb.StoreID, KnowledgeBaseID: kb.ID,
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
		TenantID: kb.TenantID,
		EventKey: "fastgpt-dataset-delete:" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		StoreID:  kb.StoreID, KnowledgeBaseID: kb.ID,
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
	if err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
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
			return repositories.ConversationRouteStateRepository.UpdateKnowledgeBaseByStoreInTenant(tx.Tx, store.ID, 0, kb.TenantID, now, operatorName)
		}
		return nil
	}); err != nil {
		return err
	}
	publishFastGPTConfigurationState(kb.TenantID, kb.StoreID, now)
	return nil
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
		ID: job.ID, StoreID: job.StoreID, KnowledgeBaseID: job.KnowledgeBaseID, Action: job.Action, Status: job.Status,
		TargetStoreStaffBindingID: job.TargetStoreStaffBindingID,
		DatasetID:                 job.DatasetID, CollectionID: job.CollectionID, Filename: job.Filename, AttemptCount: job.AttemptCount,
		TargetProfileID: job.TargetProfileID, TargetProfileRevision: job.TargetProfileRevision,
		TargetCredentialRevision: job.TargetCredentialRevision, NextRetryAt: job.NextRetryAt,
		LastError: job.LastError, LastErrorClass: job.LastErrorClass, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
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
	if errors.Is(err, errFastGPTJobTargetChanged) {
		return "target_revision_changed"
	}
	var syncErr *storeCredentialFastGPTSyncError
	if errors.As(err, &syncErr) && strings.TrimSpace(syncErr.Class) != "" {
		return syncErr.Class
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
