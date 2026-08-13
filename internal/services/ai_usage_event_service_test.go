package services

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestAIUsageEventRecordIsIdempotentAndImmutable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AIUsageEvent{}, &models.AIUsageGatewayCall{}); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })

	event := models.AIUsageEvent{
		TenantID: 101,
		EventKey: "request-1:intent_detect:1", RequestID: "request-1", Stage: "intent_detect",
		Provider: "openai", Model: "test-model", PromptTokens: 12, CompletionTokens: 3,
		MetricSource: AIUsageMetricSourceUpstreamActual, Status: "completed",
	}
	if err := AIUsageEventService.Record(event); err != nil {
		t.Fatal(err)
	}
	event.PromptTokens = 999
	if err := AIUsageEventService.Record(event); err != nil {
		t.Fatal(err)
	}
	stored := repositories.AIUsageEventRepository.TakeByEventKey(db, event.EventKey)
	if stored == nil || stored.PromptTokens != 12 {
		t.Fatalf("stored=%#v", stored)
	}
	var count int64
	if err := db.Model(&models.AIUsageEvent{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestAIUsageEventCreatesGatewayReconciliationEvidence(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AIUsageEvent{}, &models.AIUsageGatewayCall{}); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })

	now := time.Now()
	event := models.AIUsageEvent{
		TenantID: 101,
		EventKey: "request-2:reply_generate:1", RequestID: "request-2", Stage: "reply_generate",
		Gateway: AIUsageGatewayNewAPI, GatewayRequestID: "new-api-request-2",
		CallStartedAt: &now, CallFinishedAt: &now,
		MetricSource: AIUsageMetricSourceUpstreamActual, Status: "completed",
	}
	if err := AIUsageEventService.Record(event); err != nil {
		t.Fatal(err)
	}
	call := repositories.AIUsageGatewayCallRepository.TakeByGatewayRequestID(db, AIUsageGatewayNewAPI, "new-api-request-2")
	if call == nil || call.EventKey != event.EventKey || call.ReconcileStatus != AIUsageReconcilePending {
		t.Fatalf("gateway call=%#v", call)
	}
}

func TestAIUsageEventRecordsEveryGatewayRetryReceipt(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AIUsageEvent{}, &models.AIUsageGatewayCall{}); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })

	now := time.Now()
	receipts := []usagex.Receipt{
		{Gateway: AIUsageGatewayNewAPI, RequestID: "newapi-retry-1", Attempt: 1, StartedAt: now, FinishedAt: now.Add(time.Millisecond), StatusCode: 200, ErrorClass: "upstream_error", ProviderStatus: "failed", ProviderCode: "provider_unavailable"},
		{Gateway: AIUsageGatewayNewAPI, RequestID: "newapi-retry-2", Attempt: 2, StartedAt: now.Add(2 * time.Millisecond), FinishedAt: now.Add(3 * time.Millisecond), StatusCode: 200, ErrorClass: "upstream_error", ProviderStatus: "failed", ProviderCode: "provider_unavailable"},
		{Gateway: AIUsageGatewayNewAPI, RequestID: "newapi-retry-3", Attempt: 3, StartedAt: now.Add(4 * time.Millisecond), FinishedAt: now.Add(5 * time.Millisecond), StatusCode: 200, ErrorClass: "upstream_error", ProviderStatus: "failed", ProviderCode: "provider_unavailable"},
	}
	event := models.AIUsageEvent{
		TenantID: 101, EventKey: "request-retry:intent_detect:1", RequestID: "request-retry", Stage: "intent_detect",
		MetricSource: AIUsageMetricSourceProviderOperation, Status: "failed", ErrorClass: "upstream_error",
	}
	if err := AIUsageEventService.RecordWithGatewayReceipts(event, receipts); err != nil {
		t.Fatal(err)
	}
	var calls []models.AIUsageGatewayCall
	if err := db.Order("id ASC").Find(&calls).Error; err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("gateway calls=%d want=3: %#v", len(calls), calls)
	}
	for index, call := range calls {
		if call.GatewayRequestID != receipts[index].RequestID || call.HTTPStatus != 200 ||
			call.LastErrorClass != "upstream_error" || !strings.Contains(call.LastError, fmt.Sprintf("attempt=%d", index+1)) {
			t.Fatalf("call[%d]=%#v", index, call)
		}
	}
}
