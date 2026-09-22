package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestHumanRiskClassificationDoesNotAuthorizeAutomaticHandoff(t *testing.T) {
	for _, subIntent := range []string{"refund_compensation", "maintenance", "answer_rejected"} {
		intent := callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", NeedsHumanRoute: true, IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "human_complaint_risk", SubIntent: subIntent, Text: "先回答我的问题，不要转人工", NeedsHumanRoute: true},
		}}
		got := enforceRuntimeHumanRoutePolicy(intent, "先回答我的问题，不要转人工")
		if got.NeedsHumanRoute || got.IntentTasks[0].NeedsHumanRoute {
			t.Fatalf("%s classification bypassed current customer authorization: %#v", subIntent, got)
		}
	}
}

func TestEmergencySafetyClassificationAuthorizesAutomaticHandoff(t *testing.T) {
	for _, text := range []string{"我摔倒了，腿在流血", "房间有人突然晕倒，需要马上处理"} {
		intent := callbacks.IntentTraceData{PrimaryIntent: "human_complaint_risk", NeedsHumanRoute: true, IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "human_complaint_risk", SubIntent: "emergency_safety", Text: text, NeedsHumanRoute: true},
		}}
		got := enforceRuntimeHumanRoutePolicy(intent, text)
		if !got.NeedsHumanRoute || len(got.IntentTasks) != 1 || !got.IntentTasks[0].NeedsHumanRoute || got.IntentTasks[0].SubIntent != "emergency_safety" {
			t.Fatalf("emergency safety route was removed: %#v", got)
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
