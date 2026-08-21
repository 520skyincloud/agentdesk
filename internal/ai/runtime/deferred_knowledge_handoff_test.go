package runtime

import (
	"testing"

	applicationruntime "agent-desk/internal/ai/application/runtime"
)

func TestDeferredKnowledgeHandoffFromTrace(t *testing.T) {
	reason, ok := deferredKnowledgeHandoffFromTrace(`{"pipeline":{"evidenceJudge":{"deferredHandoff":true,"deferredHandoffReason":"空调故障需要同事接手"}}}`)
	if !ok || reason != "空调故障需要同事接手" {
		t.Fatalf("unexpected deferred handoff: ok=%v reason=%q", ok, reason)
	}
	if _, ok := deferredKnowledgeHandoffFromTrace(`{"pipeline":{"evidenceJudge":{"deferredHandoff":false}}}`); ok {
		t.Fatal("disabled deferred handoff must not be dispatched")
	}
	if _, ok := deferredKnowledgeHandoffFromTrace(`not-json`); ok {
		t.Fatal("invalid trace must not dispatch a handoff")
	}
}

func TestResolveReplyExecutionActionsDispatchesDeferredHandoffWithoutReplyText(t *testing.T) {
	summary := &applicationruntime.Summary{TraceData: `{"pipeline":{"evidenceJudge":{"deferredHandoff":true,"deferredHandoffReason":"空调故障需要同事接手"}}}`}
	hasCommitPayload, hasDeferred := resolveReplyExecutionActions(summary, false)
	if hasCommitPayload {
		t.Fatal("empty generated text must not enter the reply commit path")
	}
	if !hasDeferred {
		t.Fatal("deferred handoff must still dispatch when generated reply text is empty")
	}
}

func TestResolveReplyExecutionActionsKeepsReplyAndDeferredActionsIndependent(t *testing.T) {
	summary := &applicationruntime.Summary{
		ReplyText: "酒店暂不提供早餐。",
		TraceData: `{"pipeline":{"evidenceJudge":{"deferredHandoff":true}}}`,
	}
	hasCommitPayload, hasDeferred := resolveReplyExecutionActions(summary, false)
	if !hasCommitPayload || !hasDeferred {
		t.Fatalf("expected both answer commit and deferred handoff, commit=%v deferred=%v", hasCommitPayload, hasDeferred)
	}
}
