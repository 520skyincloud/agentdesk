package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"agent-desk/internal/models"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
)

func TestFastGPTUsageEventMapsToImmutableBillingEvidence(t *testing.T) {
	knowledgeBase := &models.KnowledgeBase{ID: 31, CompanyID: 7, StoreID: 9}
	tenant := &models.FastGPTStoreTenant{TenantTeamID: "team-abc"}
	createdAt := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	event := toFastGPTUsageEvent(knowledgeBase, tenant, fastgptapi.UsageEvent{
		ExternalEventID: "model-42", Kind: "model", CreatedAt: createdAt,
		Stage: "document_parse", Provider: "dashscope", Model: "qwen3.6-flash",
		PromptTokens: 10, CompletionTokens: 20, CachedTokens: 3, LatencyMS: 88,
		Status: "success",
	})
	if event.EventKey != "fastgpt:team-abc:model-42" || event.MetricSource != AIUsageMetricSourceUpstreamActual {
		t.Fatalf("unexpected event identity: %#v", event)
	}
	if event.PromptTokens != 10 || event.CompletionTokens != 20 || event.CachedPromptTokens != 3 || event.CreatedAt != createdAt {
		t.Fatalf("unexpected model usage mapping: %#v", event)
	}
	if event.Provider != "dashscope" || event.ModelSource != "fastgpt_profile" || event.Status != "completed" {
		t.Fatalf("unexpected model attribution: %#v", event)
	}
}

func TestFastGPTOperationUsageDoesNotPretendToBeModelTokens(t *testing.T) {
	knowledgeBase := &models.KnowledgeBase{ID: 32, CompanyID: 7, StoreID: 9}
	tenant := &models.FastGPTStoreTenant{TenantTeamID: "team-abc"}
	event := toFastGPTUsageEvent(knowledgeBase, tenant, fastgptapi.UsageEvent{
		ExternalEventID: "operation-43", Kind: "operation", OperationType: "knowledge_upload",
		RequestCount: 1, FileBytes: 2048, Status: "blocked", ErrorClass: "operation_blocked",
	})
	if event.MetricSource != AIUsageMetricSourceProviderOperation || event.Provider != "fastgpt" || event.Model != "" {
		t.Fatalf("unexpected operation attribution: %#v", event)
	}
	if event.PromptTokens != 0 || event.CompletionTokens != 0 || event.Status != "failed" || event.ErrorMessage != "operation_blocked" {
		t.Fatalf("unexpected operation usage mapping: %#v", event)
	}
}

func TestFastGPTErrorClassDoesNotPersistUpstreamText(t *testing.T) {
	if got := fastGPTErrorClass(context.DeadlineExceeded); got != "fastgpt_timeout" {
		t.Fatalf("timeout class=%q", got)
	}
	if got := fastGPTErrorClass(&fastgptapi.HTTPStatusError{StatusCode: 502, Message: "provider topology"}); got != "fastgpt_http_5xx" {
		t.Fatalf("http class=%q", got)
	}
	public := publicFastGPTError(errors.New("FastGPT HTTP 401: raw provider address"))
	if public == nil || public.Error() == "" || public.Error() == "FastGPT HTTP 401: raw provider address" {
		t.Fatalf("public error leaked upstream text: %v", public)
	}
}
