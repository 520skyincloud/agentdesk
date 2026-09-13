package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pms"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func setupPMSOperationServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.PMSOperation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		config.SetCurrent(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return db
}

func renewTestRequest() pms.RenewRequest {
	return pms.RenewRequest{
		ReceptOrderID:       123,
		RenewType:           "ORIGINAL",
		RenewPriceMode:      "LAST_DAY",
		RenewHomeHandleType: "KEEP_CURRENT_HOME",
		StartTime:           "2026-09-13",
		EndTime:             "2026-09-14",
	}
}

func TestPMSRenewDraftIsIdempotentAndCancelStopsExecution(t *testing.T) {
	setupPMSOperationServiceTestDB(t)

	first, err := PMSOperationService.CreateRenewDraft(10, 20, renewTestRequest(), "续住预览")
	if err != nil {
		t.Fatal(err)
	}
	second, err := PMSOperationService.CreateRenewDraft(10, 20, renewTestRequest(), "不同预览")
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != second.OperationID || first.IdempotencyKey != second.IdempotencyKey {
		t.Fatalf("same source must reuse draft: first=%+v second=%+v", first, second)
	}
	if err := PMSOperationService.CancelRenew(10, first.OperationID); err != nil {
		t.Fatal(err)
	}
	outcome, err := PMSOperationService.ConfirmRenew(context.Background(), 10, first.OperationID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != PMSOperationCancelled {
		t.Fatalf("cancelled draft must not execute: %+v", outcome)
	}
}

func TestPMSRenewExpiredDraftDoesNotCallPMS(t *testing.T) {
	db := setupPMSOperationServiceTestDB(t)
	draft, err := PMSOperationService.CreateRenewDraft(10, 20, renewTestRequest(), "续住预览")
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Minute)
	if err := db.Model(&models.PMSOperation{}).Where("id = ?", draft.OperationID).Update("expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	config.SetCurrent(&config.Config{PMS: config.PMSConfig{Enabled: true, BaseURL: "http://127.0.0.1:1", AllowWrite: true}})

	outcome, err := PMSOperationService.ConfirmRenew(context.Background(), 10, draft.OperationID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != PMSOperationExpired {
		t.Fatalf("expected expired, got %+v", outcome)
	}
}

func TestPMSRenewConfirmWritesOnceAndReadsBack(t *testing.T) {
	setupPMSOperationServiceTestDB(t)
	var renewCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin-api/hpms/orderManage/receptOrder/renew":
			atomic.AddInt32(&renewCalls, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"data":null}`))
		case "/admin-api/hpms/orderManage/receptOrder/detail":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"data":{"receptOrderId":123,"status":"CHECK_IN"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	config.SetCurrent(&config.Config{PMS: config.PMSConfig{Enabled: true, BaseURL: server.URL, AllowWrite: true}})

	draft, err := PMSOperationService.CreateRenewDraft(10, 20, renewTestRequest(), "续住预览")
	if err != nil {
		t.Fatal(err)
	}
	first, err := PMSOperationService.ConfirmRenew(context.Background(), 10, draft.OperationID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != PMSOperationSucceeded {
		t.Fatalf("expected success, got %+v", first)
	}
	second, err := PMSOperationService.ConfirmRenew(context.Background(), 10, draft.OperationID, 31)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != PMSOperationSucceeded || atomic.LoadInt32(&renewCalls) != 1 {
		t.Fatalf("duplicate confirmation must reuse result: second=%+v renewCalls=%d", second, renewCalls)
	}
}

func TestPMSRenewReadbackFailureIsNotSuccess(t *testing.T) {
	setupPMSOperationServiceTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin-api/hpms/orderManage/receptOrder/renew" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"data":null}`))
			return
		}
		http.Error(w, "readback unavailable", http.StatusBadGateway)
	}))
	defer server.Close()
	config.SetCurrent(&config.Config{PMS: config.PMSConfig{Enabled: true, BaseURL: server.URL, AllowWrite: true}})

	draft, err := PMSOperationService.CreateRenewDraft(10, 20, renewTestRequest(), "续住预览")
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := PMSOperationService.ConfirmRenew(context.Background(), 10, draft.OperationID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != PMSOperationFailed || outcome.ReplyText == "续住已经办理好了。" {
		t.Fatalf("readback failure must not be reported as success: %+v", outcome)
	}
}
