package executor

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestCompleteGeneratedReplyProtocolFailureReturnsExecutorError(t *testing.T) {
	protocolErr := fmt.Errorf("%w: missing content for task-2", errGeneratedReplyProtocol)
	summary := &RunResult{Status: "fallback", ReplyText: `{"replyParts":[]}`}
	collector := callbacks.NewRuntimeTraceCollector()

	got, err := completeGeneratedReplyProtocolFailure(summary, collector, protocolErr, "generate")

	if got != summary || !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("protocol failure must return the executor error, summary=%#v err=%v", got, err)
	}
	if summary.Status != "error" || summary.ReplyText != "" || summary.ErrorMessage == "" {
		t.Fatalf("protocol failure must suppress output and mark the run failed, got %#v", summary)
	}
	if collector.Data.Output.FinishReason != "generated_reply_protocol_error" || collector.Data.Pipeline.Validate.Status != "failed" {
		t.Fatalf("protocol failure trace must remain retryable and diagnosable, got %#v", collector.Data)
	}
	if strings.Contains(summary.ReplyText, "replyParts") {
		t.Fatalf("internal protocol leaked into the final reply: %q", summary.ReplyText)
	}
}

func TestCompleteIntentDetectUnavailableBlocksUngroundedGenerate(t *testing.T) {
	summary := &RunResult{Status: "started"}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Model.Name = "deepseek-v4-pro"

	got, err := completeIntentDetectUnavailable(summary, collector)

	if err != nil || got != summary || summary.Status != "completed" || strings.TrimSpace(summary.ReplyText) == "" {
		t.Fatalf("intent failure must produce one deterministic customer-visible safe reply, summary=%#v err=%v", summary, err)
	}
	if collector.Data.Pipeline.Generate.Status != "skipped" || collector.Data.Pipeline.Generate.FallbackMode != "intent_detect_safe_reply" {
		t.Fatalf("intent failure must never enter free Generate, got %#v", collector.Data.Pipeline.Generate)
	}
	for _, inventedFact := range []string{"家常菜", "老街", "门卡", "充电桩", "电子发票"} {
		if strings.Contains(summary.ReplyText, inventedFact) {
			t.Fatalf("safe fallback must not contain hotel facts, got %q", summary.ReplyText)
		}
	}
}

func TestUngroundedKnowledgeReplyTaskIDsRequiresSelectedFacts(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "missing-layer", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{Statement: "早餐时间是7:00到9:30。"}}},
		{TaskID: "missing-facts", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, SelectedLayer: "store"},
		{TaskID: "grounded", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, SelectedLayer: "store", SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{Statement: "停车免费。"}}},
		{TaskID: "interaction", Intent: "interaction", OutputKind: "text", ReplyRequired: true},
	}}

	got := ungroundedKnowledgeReplyTaskIDs(plan)
	if len(got) != 2 || got[0] != "missing-layer" || got[1] != "missing-facts" {
		t.Fatalf("expected only ungrounded knowledge text tasks, got %#v", got)
	}
}

func TestCompleteUngroundedKnowledgeFallbackKeepsGroundedSiblingFacts(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Model.Name = "deepseek-v4-pro"
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, SelectedLayer: "store",
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "task-1F1", Statement: "停车免费。", CriticalValues: []string{"免费"}}},
		},
		{TaskID: "task-2", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true},
	}}
	summary := &RunResult{Status: "started"}

	got, err := completeUngroundedKnowledgeFallback(summary, collector, []string{"task-2"})
	if err != nil || got != summary || summary.Status != "completed" {
		t.Fatalf("unexpected fallback result summary=%#v err=%v", summary, err)
	}
	if !strings.Contains(summary.ReplyText, "停车免费") || !strings.Contains(summary.ReplyText, "暂时没法准确回答") {
		t.Fatalf("fallback must preserve grounded siblings and fail closed for missing evidence, got %q", summary.ReplyText)
	}
	if collector.Data.Pipeline.Generate.Status != "skipped" || collector.Data.Pipeline.Generate.FallbackMode != "deterministic_knowledge_evidence_guard" {
		t.Fatalf("ungrounded knowledge must not enter Generate, got %#v", collector.Data.Pipeline.Generate)
	}
}

func TestIsolateUngroundedKnowledgeReplyTasksKeepsIndependentToolTask(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "interaction", SubIntent: "weather_query", OutputKind: "text", ReplyRequired: true, Output: "text_reply"},
	}}

	got, isolated := isolateUngroundedKnowledgeReplyTasks(plan)
	if len(isolated) != 1 || isolated[0] != "task-1" {
		t.Fatalf("expected only the ungrounded knowledge task to be isolated, got %#v", isolated)
	}
	if got.TaskPlans[0].SelectedLayer != "runtime_safe_fallback" || len(got.TaskPlans[0].SupportedFacts) != 1 || got.TaskPlans[0].SupportedFacts[0].Statement != ungroundedKnowledgeSafeReply {
		t.Fatalf("ungrounded task must become a constrained safe reply, got %#v", got.TaskPlans[0])
	}
	if got.TaskPlans[1].TaskID != plan.TaskPlans[1].TaskID || got.TaskPlans[1].SubIntent != plan.TaskPlans[1].SubIntent || got.TaskPlans[1].Output != plan.TaskPlans[1].Output || len(got.TaskPlans[1].SupportedFacts) != 0 {
		t.Fatalf("independent tool task must remain unchanged, got %#v", got.TaskPlans[1])
	}
	if blocked := ungroundedKnowledgeReplyTaskIDs(got); len(blocked) != 0 {
		t.Fatalf("isolated plan must be safe to continue into Generate, got %#v", blocked)
	}
}

func TestIsolateUngroundedKnowledgeReplyTasksLeavesOnlyTaskForLocalFallback(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
	}}}

	got, isolated := isolateUngroundedKnowledgeReplyTasks(plan)
	if len(isolated) != 0 || got.TaskPlans[0].SelectedLayer != "" || len(got.TaskPlans[0].SupportedFacts) != 0 {
		t.Fatalf("a lone ungrounded task must still use the deterministic local fallback, got plan=%#v isolated=%#v", got, isolated)
	}
}
