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
	knowledgeBase := &models.KnowledgeBase{ID: 31, StoreID: 9}
	tenant := &models.FastGPTStoreTenant{TenantTeamID: "team-abc"}
	attribution := fastGPTUsageAttribution{
		ModelProfileID: 17, ProfileRevision: 4, CredentialRevision: 3, KeyFingerprint: "fingerprint-3",
		FastGPTProfileID: "fastgpt-profile-4", FastGPTRevision: "9",
	}
	createdAt := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	event := toFastGPTUsageEvent(knowledgeBase, tenant, attribution, fastgptapi.UsageEvent{
		ExternalEventID: "model-42", Kind: "model", CreatedAt: createdAt,
		Stage: "document_parse", Provider: "dashscope", Model: "qwen3.6-flash", ProfileID: "fastgpt-profile-4", ProfileRevision: 9,
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
	if event.ModelProfileID != 17 || event.ModelProfileRevision != 4 || event.CredentialRevision != 3 || event.KeyFingerprint != "fingerprint-3" || event.UsageSlot != "document_parser" {
		t.Fatalf("unexpected immutable revision attribution: %#v", event)
	}
}

func TestFastGPTOperationUsageDoesNotPretendToBeModelTokens(t *testing.T) {
	knowledgeBase := &models.KnowledgeBase{ID: 32, StoreID: 9}
	tenant := &models.FastGPTStoreTenant{TenantTeamID: "team-abc"}
	event := toFastGPTUsageEvent(knowledgeBase, tenant, fastGPTUsageAttribution{
		ModelProfileID: 17, ProfileRevision: 4, CredentialRevision: 3, KeyFingerprint: "fingerprint-3",
	}, fastgptapi.UsageEvent{
		ExternalEventID: "operation-43", Kind: "operation", OperationType: "knowledge_upload",
		RequestCount: 1, FileBytes: 2048, Status: "blocked", ErrorClass: "operation_blocked",
	})
	if event.MetricSource != AIUsageMetricSourceProviderOperation || event.Provider != "fastgpt" || event.Model != "" {
		t.Fatalf("unexpected operation attribution: %#v", event)
	}
	if event.PromptTokens != 0 || event.CompletionTokens != 0 || event.Status != "failed" || event.ErrorClass != "operation_blocked" || event.ErrorMessage != "" {
		t.Fatalf("unexpected operation usage mapping: %#v", event)
	}
}

func TestFastGPTUsageAttributionUsesCursorWindowAndCurrentRevision(t *testing.T) {
	window := fastGPTUsageAttribution{ModelProfileID: 1, ProfileRevision: 2, CredentialRevision: 3, KeyFingerprint: "old", FastGPTProfileID: "remote", FastGPTRevision: "5"}
	current := fastGPTUsageAttribution{ModelProfileID: 7, ProfileRevision: 8, CredentialRevision: 9, KeyFingerprint: "new", FastGPTProfileID: "remote", FastGPTRevision: "6"}
	oldEvent, err := selectFastGPTUsageAttribution(fastgptapi.UsageEvent{Kind: "model", ProfileID: "remote", ProfileRevision: 5}, window, current)
	if err != nil || oldEvent.ModelProfileID != 1 || oldEvent.CredentialRevision != 3 {
		t.Fatalf("cursor-window attribution=%#v err=%v", oldEvent, err)
	}
	newEvent, err := selectFastGPTUsageAttribution(fastgptapi.UsageEvent{Kind: "model", ProfileID: "remote", ProfileRevision: 6}, window, current)
	if err != nil || newEvent.ModelProfileID != 7 || newEvent.CredentialRevision != 9 {
		t.Fatalf("current attribution=%#v err=%v", newEvent, err)
	}
	if _, err := selectFastGPTUsageAttribution(fastgptapi.UsageEvent{Kind: "model", ProfileID: "remote", ProfileRevision: 4}, window, current); err == nil {
		t.Fatal("unknown FastGPT Profile revision must fail closed")
	}
}

func TestNextFastGPTUsageCursorRequiresProgressForEvents(t *testing.T) {
	if _, err := nextFastGPTUsageCursor("cursor-1", &fastgptapi.UsageEventPage{
		Events: []fastgptapi.UsageEvent{{ExternalEventID: "event-1"}}, NextCursor: "cursor-1",
	}); err == nil {
		t.Fatal("usage events without cursor progress must fail closed")
	}
	if _, err := nextFastGPTUsageCursor("cursor-1", &fastgptapi.UsageEventPage{
		Events: []fastgptapi.UsageEvent{{ExternalEventID: "event-1"}},
	}); err == nil {
		t.Fatal("usage events without a next cursor must fail closed")
	}
	if next, err := nextFastGPTUsageCursor("cursor-1", &fastgptapi.UsageEventPage{
		Events: []fastgptapi.UsageEvent{{ExternalEventID: "event-1"}}, NextCursor: "cursor-2",
	}); err != nil || next != "cursor-2" {
		t.Fatalf("next cursor=%q err=%v", next, err)
	}
}

func TestNextFastGPTUsageCursorPreservesCursorOnEmptyPage(t *testing.T) {
	if next, err := nextFastGPTUsageCursor("cursor-1", &fastgptapi.UsageEventPage{}); err != nil || next != "cursor-1" {
		t.Fatalf("empty page cursor=%q err=%v", next, err)
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
