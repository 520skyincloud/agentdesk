package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestHumanRiskClassificationDoesNotAuthorizeAutomaticHandoff(t *testing.T) {
	for _, subIntent := range []string{"emergency_safety", "refund_compensation", "maintenance", "answer_rejected"} {
		intent := callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", NeedsHumanRoute: true, IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "human_complaint_risk", SubIntent: subIntent, Text: "先回答我的问题，不要转人工", NeedsHumanRoute: true},
		}}
		got := enforceRuntimeHumanRoutePolicy(intent, "先回答我的问题，不要转人工")
		if got.NeedsHumanRoute || got.IntentTasks[0].NeedsHumanRoute {
			t.Fatalf("%s classification bypassed current customer authorization: %#v", subIntent, got)
		}
	}
}

func TestExplicitHumanRequestStillAuthorizesHandoff(t *testing.T) {
	for _, text := range []string{"请帮我转人工", "我要找同事"} {
		intent := callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", NeedsHumanRoute: true, IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "human_complaint_risk", SubIntent: "explicit_handoff", Text: text, NeedsHumanRoute: true},
		}}
		got := enforceRuntimeHumanRoutePolicy(intent, text)
		if !got.NeedsHumanRoute || !got.IntentTasks[0].NeedsHumanRoute {
			t.Fatalf("explicit request was blocked: %#v", got)
		}
	}
}
