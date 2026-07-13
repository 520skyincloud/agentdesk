package services

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestFastGPTFailedDatasetJobCanBeRetried(t *testing.T) {
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
	completedAt := time.Now()
	failed := &models.FastGPTDatasetJob{
		TaskKey: fmt.Sprintf("fastgpt-create-store-%d", store.ID), StoreID: store.ID,
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
