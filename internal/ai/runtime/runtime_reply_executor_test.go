package runtime

import (
	"strings"
	"testing"
	"time"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	"agent-desk/internal/ai/runtime/graphs"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/usagex"
	svc "agent-desk/internal/services"
)

func TestRuntimeReplyExecutorResumeMessageTextUsesMediaTranscript(t *testing.T) {
	message := models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Content:     "wx_protocol_1001.mp3",
		Payload:     `{"mediaText":"确认确认","mediaSummary":"确认确认","mediaUnderstandingStatus":"understood"}`,
	}
	got := newRuntimeReplyExecutor().resumeMessageText(message)
	if !strings.Contains(got, "确认确认") {
		t.Fatalf("expected transcript in resume message text, got %q", got)
	}
	if graphs.ParseConfirmationDecision(got) != graphs.ConfirmationDecisionConfirm {
		t.Fatalf("expected transcript to be recognized as confirm, got %q", got)
	}
}

func TestNormalizeRuntimeReplyAIConfigClampsLargeOutputBudget(t *testing.T) {
	config := normalizeRuntimeReplyAIConfig(models.AIConfig{MaxOutputTokens: 64800})
	if config.MaxOutputTokens != runtimeReplyMaxOutputTokens {
		t.Fatalf("expected large reply output budget to clamp to %d, got %d", runtimeReplyMaxOutputTokens, config.MaxOutputTokens)
	}
}

func TestNormalizeRuntimeReplyAIConfigKeepsLowerOutputBudget(t *testing.T) {
	config := normalizeRuntimeReplyAIConfig(models.AIConfig{MaxOutputTokens: 320})
	if config.MaxOutputTokens != 320 {
		t.Fatalf("expected lower reply output budget to be preserved, got %d", config.MaxOutputTokens)
	}
}

func TestNormalizeRuntimeReplyAIConfigAllowsConfiguredMultiTaskBudget(t *testing.T) {
	config := normalizeRuntimeReplyAIConfig(models.AIConfig{MaxOutputTokens: 1024})
	if config.MaxOutputTokens != 1024 {
		t.Fatalf("expected configured multi-task budget to be preserved, got %d", config.MaxOutputTokens)
	}
}

func TestNormalizeRuntimeReplyAIConfigSetsDefaultOutputBudget(t *testing.T) {
	config := normalizeRuntimeReplyAIConfig(models.AIConfig{})
	if config.MaxOutputTokens != runtimeReplyDefaultMaxOutputTokens {
		t.Fatalf("expected empty reply output budget to default to %d, got %d", runtimeReplyDefaultMaxOutputTokens, config.MaxOutputTokens)
	}
}

func TestBuildReplyModelUsageEventsKeepsFailedAndSuccessfulReceiptsAligned(t *testing.T) {
	startedAt := time.Date(2026, time.August, 27, 10, 0, 0, 0, time.UTC)
	receipts := []usagex.Receipt{
		{
			Gateway: usagex.GatewayNewAPI, RequestID: "failed-request", UpstreamRequestID: "failed-upstream",
			StartedAt: startedAt, FinishedAt: startedAt.Add(20 * time.Millisecond), StatusCode: 503,
		},
		{
			Gateway: usagex.GatewayNewAPI, RequestID: "successful-request", UpstreamRequestID: "successful-upstream",
			StartedAt: startedAt.Add(30 * time.Millisecond), FinishedAt: startedAt.Add(70 * time.Millisecond), StatusCode: 200,
		},
	}
	summary := &applicationruntime.Summary{
		RunID: "run-1",
		ModelUsageCalls: []applicationruntime.ModelUsageCall{
			{
				CallSequence: 1, GatewayReceiptOrdinal: 1,
				Status: "failed", ErrorMessage: "model_call_failed",
			},
			{
				CallSequence: 2, GatewayReceiptOrdinal: 2, HasUsage: true,
				PromptTokens: 120, CompletionTokens: 30, CachedPromptTokens: 80, ReasoningTokens: 5,
				Status: "completed",
			},
		},
	}
	events := buildReplyModelUsageEvents(
		models.Conversation{ID: 10},
		models.Message{ID: 20, RequestID: "message-request"},
		models.AIConfig{ID: 30, ModelName: "reply-model"},
		"store_model_setting",
		4,
		summary,
		receipts,
		nil,
		100,
	)
	if len(events) != 2 {
		t.Fatalf("expected one event per gateway receipt, got %+v", events)
	}
	if events[0].GatewayRequestID != "failed-request" || events[0].Status != "failed" || events[0].MetricSource != svc.AIUsageMetricSourceProviderOperation || events[0].PromptTokens != 0 {
		t.Fatalf("failed receipt was dropped or paired with successful usage: %+v", events[0])
	}
	if events[1].GatewayRequestID != "successful-request" || events[1].Status != "completed" || events[1].MetricSource != svc.AIUsageMetricSourceUpstreamActual || events[1].PromptTokens != 120 || events[1].CompletionTokens != 30 {
		t.Fatalf("successful usage was not paired with its own receipt: %+v", events[1])
	}
}

func TestBuildReplyModelUsageEventsRecordsFailureWhenFallbackClearsRunError(t *testing.T) {
	summary := &applicationruntime.Summary{
		RunID: "run-fallback",
		ModelUsageCalls: []applicationruntime.ModelUsageCall{{
			CallSequence: 1,
			Status:       "failed",
			ErrorMessage: "model_call_failed",
		}},
	}
	events := buildReplyModelUsageEvents(
		models.Conversation{ID: 10},
		models.Message{ID: 20, RequestID: "message-request"},
		models.AIConfig{ID: 30, ModelName: "reply-model"},
		"store_model_setting",
		4,
		summary,
		nil,
		nil,
		75,
	)
	if len(events) != 1 || events[0].Status != "failed" || events[0].MetricSource != svc.AIUsageMetricSourceProviderOperation || events[0].LatencyMS != 75 {
		t.Fatalf("fallback must not erase the failed Generate operation: %+v", events)
	}
}
