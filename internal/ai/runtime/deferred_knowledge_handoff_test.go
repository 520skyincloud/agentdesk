package runtime

import "testing"

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
