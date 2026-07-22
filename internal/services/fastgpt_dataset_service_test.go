package services

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestFastGPTModelProfileRejectsStoreStaff(t *testing.T) {
	operator := &dto.AuthPrincipal{
		UserID: 1,
		Roles:  []string{constants.RoleCodeStoreStaff},
		Permissions: []string{
			constants.PermissionAIConfigUpdate.Code,
		},
	}
	_, err := FastGPTDatasetService.GetModelProfile(context.Background(), 1, operator)
	if err == nil || !strings.Contains(err.Error(), "仅平台管理员") {
		t.Fatalf("store staff must not access model credentials, err=%v", err)
	}
}

func TestFastGPTModelProfileRequiresRerank(t *testing.T) {
	req := request.FastGPTModelProfileRequest{}
	if err := validateFastGPTModelProfileRequest(req); err == nil || !strings.Contains(err.Error(), "重排模型") {
		t.Fatalf("disabled rerank must be rejected, err=%v", err)
	}

	req.RerankEnabled = true
	req.Rerank = &request.FastGPTModelCredentialRequest{Provider: "openai", BaseURL: "https://rerank.example/v1"}
	if err := validateFastGPTModelProfileRequest(req); err == nil || !strings.Contains(err.Error(), "模型名") {
		t.Fatalf("incomplete rerank must be rejected, err=%v", err)
	}

	req.Rerank.Model = "rerank-v3"
	if err := validateFastGPTModelProfileRequest(req); err != nil {
		t.Fatalf("complete rerank should pass, err=%v", err)
	}
}

func TestFastGPTFailedDatasetJobCanBeRetried(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.KnowledgeBase{}, &models.FastGPTDatasetJob{}, &models.WxWorkProtocolInstance{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	store := &models.Store{Name: "测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	completedAt := time.Now()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", store.ID, store.Name)))
	failed := &models.FastGPTDatasetJob{
		TaskKey: fmt.Sprintf("fastgpt-create-store-%d-%x", store.ID, sum[:6]), StoreID: store.ID,
		Action: fastGPTJobActionCreateDataset, Status: fastGPTJobStatusFailed,
		AttemptCount: 5, CompletedAt: &completedAt, LastError: "platform unavailable",
		CreatedAt: completedAt, UpdatedAt: completedAt,
	}
	if err := db.Create(failed).Error; err != nil {
		t.Fatalf("create failed job: %v", err)
	}

	retried, err := FastGPTDatasetService.enqueueDefaultDataset(store.ID, store.Name)
	if err != nil {
		t.Fatalf("retry failed job: %v", err)
	}
	if retried.ID != failed.ID || retried.Status != fastGPTJobStatusPending || retried.AttemptCount != 0 || retried.CompletedAt != nil || retried.LastError != "" {
		t.Fatalf("unexpected retried job: %#v", retried)
	}
}

func TestFastGPTDatasetJobsAllowMultipleKnowledgeBasesForOneStore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.KnowledgeBase{}, &models.FastGPTDatasetJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	store := &models.Store{Name: "测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	first, err := FastGPTDatasetService.enqueueDefaultDataset(store.ID, "前厅资料")
	if err != nil {
		t.Fatalf("enqueue first knowledge base: %v", err)
	}
	second, err := FastGPTDatasetService.enqueueDefaultDataset(store.ID, "房间设施资料")
	if err != nil {
		t.Fatalf("enqueue second knowledge base: %v", err)
	}
	if first.ID == second.ID || first.TaskKey == second.TaskKey {
		t.Fatalf("different knowledge-base names must create independent jobs: %#v %#v", first, second)
	}
	var count int64
	if err := db.Model(&models.FastGPTDatasetJob{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("expected two jobs, count=%d err=%v", count, err)
	}
}

func TestFastGPTKnowledgeBaseActivationOnlyChangesSelectedInstance(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.KnowledgeBase{}, &models.WxWorkProtocolInstance{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	store := &models.Store{Name: "测试门店", KnowledgeBaseID: 101, Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	active := &models.KnowledgeBase{StoreID: store.ID, DatasetID: "dataset-active", Name: "当前库", Status: enums.StatusOk}
	candidate := &models.KnowledgeBase{StoreID: store.ID, DatasetID: "dataset-next", Name: "新库", Status: enums.StatusOk}
	if err := db.Create(active).Error; err != nil {
		t.Fatalf("create active knowledge base: %v", err)
	}
	if err := db.Create(candidate).Error; err != nil {
		t.Fatalf("create candidate knowledge base: %v", err)
	}
	if err := db.Model(store).Update("knowledge_base_id", active.ID).Error; err != nil {
		t.Fatalf("set store default: %v", err)
	}
	first := &models.WxWorkProtocolInstance{Guid: "gateway-instance-1", StoreID: store.ID, KnowledgeBaseID: active.ID, Status: enums.StatusOk}
	second := &models.WxWorkProtocolInstance{Guid: "gateway-instance-2", StoreID: store.ID, KnowledgeBaseID: active.ID, Status: enums.StatusOk}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("create first instance: %v", err)
	}
	if err := db.Create(second).Error; err != nil {
		t.Fatalf("create second instance: %v", err)
	}

	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin", Roles: []string{constants.RoleCodeAdmin}}
	if err := FastGPTDatasetService.ActivateKnowledgeBase(first.ID, candidate.ID, operator); err != nil {
		t.Fatalf("activate knowledge base: %v", err)
	}
	updatedFirst := WxWorkProtocolInstanceService.Get(first.ID)
	updatedSecond := WxWorkProtocolInstanceService.Get(second.ID)
	updatedStore := StoreService.Get(store.ID)
	if updatedFirst == nil || updatedFirst.KnowledgeBaseID != candidate.ID {
		t.Fatalf("first instance did not switch: %#v", updatedFirst)
	}
	if updatedSecond == nil || updatedSecond.KnowledgeBaseID != active.ID {
		t.Fatalf("second instance was changed: %#v", updatedSecond)
	}
	if updatedStore == nil || updatedStore.KnowledgeBaseID != active.ID {
		t.Fatalf("store default was changed: %#v", updatedStore)
	}
}

func TestFastGPTDatasetJobsAreScopedToAuthorizedKnowledgeBase(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.KnowledgeBase{}, &models.FastGPTDatasetJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	store := &models.Store{Name: "测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	current := &models.KnowledgeBase{StoreID: store.ID, DatasetID: "dataset-current", Name: "当前库", Status: enums.StatusOk}
	other := &models.KnowledgeBase{StoreID: store.ID, DatasetID: "dataset-other", Name: "其他库", Status: enums.StatusOk}
	if err := db.Create(current).Error; err != nil {
		t.Fatalf("create current knowledge base: %v", err)
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create other knowledge base: %v", err)
	}
	now := time.Now()
	if err := db.Create(&models.FastGPTDatasetJob{TaskKey: "job-current", StoreID: store.ID, KnowledgeBaseID: current.ID, Action: fastGPTJobActionUploadFile, Status: fastGPTJobStatusReady, Filename: "前厅资料.pdf", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create current job: %v", err)
	}
	if err := db.Create(&models.FastGPTDatasetJob{TaskKey: "job-other", StoreID: store.ID, KnowledgeBaseID: other.ID, Action: fastGPTJobActionUploadFile, Status: fastGPTJobStatusReady, Filename: "其他资料.pdf", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create other job: %v", err)
	}

	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin", Roles: []string{constants.RoleCodeAdmin}}
	jobs, err := FastGPTDatasetService.ListJobs(current.ID, operator)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].KnowledgeBaseID != current.ID || jobs[0].Filename != "前厅资料.pdf" {
		t.Fatalf("jobs leaked across knowledge bases: %#v", jobs)
	}
}

func TestFastGPTDatasetDeletionRequiresExactKnowledgeBaseName(t *testing.T) {
	knowledgeBase := &models.KnowledgeBase{Name: "南七店前厅资料"}
	if err := validateDatasetDeletionConfirmation(knowledgeBase, "南七店前厅资料"); err != nil {
		t.Fatalf("exact confirmation should pass: %v", err)
	}
	if err := validateDatasetDeletionConfirmation(knowledgeBase, "南七店资料"); err == nil {
		t.Fatal("different confirmation name must be rejected")
	}
}

func TestFinalizeFastGPTDatasetDeletionClearsOnlyBoundInstances(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Store{}, &models.KnowledgeBase{}, &models.WxWorkProtocolInstance{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})

	store := &models.Store{Name: "测试门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	deleting := &models.KnowledgeBase{StoreID: store.ID, DatasetID: "dataset-delete", Name: "待删除资料", Status: enums.StatusOk}
	other := &models.KnowledgeBase{StoreID: store.ID, DatasetID: "dataset-keep", Name: "保留资料", Status: enums.StatusOk}
	if err := db.Create(deleting).Error; err != nil {
		t.Fatalf("create deleting knowledge base: %v", err)
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create retained knowledge base: %v", err)
	}
	if err := db.Model(store).Update("knowledge_base_id", deleting.ID).Error; err != nil {
		t.Fatalf("set store knowledge base: %v", err)
	}
	bound := &models.WxWorkProtocolInstance{Guid: "instance-bound", StoreID: store.ID, KnowledgeBaseID: deleting.ID, Status: enums.StatusOk}
	retained := &models.WxWorkProtocolInstance{Guid: "instance-retained", StoreID: store.ID, KnowledgeBaseID: other.ID, Status: enums.StatusOk}
	if err := db.Create(bound).Error; err != nil {
		t.Fatalf("create bound instance: %v", err)
	}
	if err := db.Create(retained).Error; err != nil {
		t.Fatalf("create retained instance: %v", err)
	}

	operator := &dto.AuthPrincipal{UserID: 1, Username: "admin", Roles: []string{constants.RoleCodeAdmin}}
	if err := FastGPTDatasetService.finalizeDatasetDeletion(deleting, operator); err != nil {
		t.Fatalf("finalize dataset deletion: %v", err)
	}

	updatedKnowledgeBase := KnowledgeBaseService.Get(deleting.ID)
	updatedStore := StoreService.Get(store.ID)
	updatedBound := WxWorkProtocolInstanceService.Get(bound.ID)
	updatedRetained := WxWorkProtocolInstanceService.Get(retained.ID)
	if updatedKnowledgeBase == nil || updatedKnowledgeBase.Status != enums.StatusDeleted {
		t.Fatalf("knowledge base was not marked deleted: %#v", updatedKnowledgeBase)
	}
	if updatedStore == nil || updatedStore.KnowledgeBaseID != 0 {
		t.Fatalf("store retained deleted knowledge base: %#v", updatedStore)
	}
	if updatedBound == nil || updatedBound.KnowledgeBaseID != 0 {
		t.Fatalf("bound instance retained deleted knowledge base: %#v", updatedBound)
	}
	if updatedRetained == nil || updatedRetained.KnowledgeBaseID != other.ID {
		t.Fatalf("unrelated instance changed: %#v", updatedRetained)
	}
}
