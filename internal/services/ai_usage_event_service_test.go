package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
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
