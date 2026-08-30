package executor

import (
	"reflect"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestIntentSemanticGateLegacyContractLeavesCurrentBehaviorUntouched(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:    "service_request",
		IntentConfidence: 0.2,
		NeedsHumanRoute:  true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:          "service_request",
			SubIntent:       "air_conditioner",
			Text:            "有没有空调",
			ResolvedText:    "房间有没有空调",
			NeedsKnowledge:  true,
			NeedsHumanRoute: true,
		}},
	}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, nil, runtimeIntentSemanticGateContext{})
	if got.ContractMode != runtimeIntentSemanticContractLegacy {
		t.Fatalf("expected legacy contract, got %q", got.ContractMode)
	}
	if !reflect.DeepEqual(got.Intent, intent) {
		t.Fatalf("legacy fallback must not alter current behavior:\nwant %#v\n got %#v", intent, got.Intent)
	}
	if got.SuppressLegacyConfidenceFallback {
		t.Fatal("legacy output must keep the existing confidence fallback")
	}
}

func TestIntentSemanticGateRequiredContractRejectsMissingSemanticFields(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "service_request",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:         "service_request",
			SubIntent:      "air_conditioner",
			Text:           "有没有空调",
			ResolvedText:   "有没有空调",
			NeedsKnowledge: true,
		}},
	}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, nil, runtimeIntentSemanticGateContext{RequireSemanticContract: true})
	if got.ContractMode != runtimeIntentSemanticContractInvalid {
		t.Fatalf("new Profile output without semantic fields must not be treated as legacy, got %q", got.ContractMode)
	}
	if task := got.Intent.IntentTasks[0]; task.Intent != "interaction" || task.SubIntent != "clarify" || task.NeedsKnowledge {
		t.Fatalf("missing required semantic fields must fail closed for that task, got %#v", task)
	}
}

func TestRuntimeIntentSemanticContractRequiresCompleteV2Semantics(t *testing.T) {
	tasks := []callbacks.IntentTaskTraceData{{Intent: "hotel_info"}}
	semantics := []runtimeIntentTaskSemantics{{RelationToPrevious: "independent", ResolutionState: "clear"}}
	if got := runtimeIntentSemanticContractMode(tasks, semantics, true); got != runtimeIntentSemanticContractInvalid {
		t.Fatalf("active V2 output must reject a missing objective, got %q", got)
	}
	semantics[0].Objective = "not_real"
	if got := runtimeIntentSemanticContractMode(tasks, semantics, true); got != runtimeIntentSemanticContractInvalid {
		t.Fatalf("a nonempty invalid objective must still fail, got %q", got)
	}
	semantics[0] = runtimeIntentTaskSemantics{Objective: "time", RelationToPrevious: "not_real", ResolutionState: "clear"}
	if got := runtimeIntentSemanticContractMode(tasks, semantics, true); got != runtimeIntentSemanticContractInvalid {
		t.Fatalf("relation enum remains strict, got %q", got)
	}
	semantics[0] = runtimeIntentTaskSemantics{Objective: "time", RelationToPrevious: "independent", ResolutionState: "not_real"}
	if got := runtimeIntentSemanticContractMode(tasks, semantics, true); got != runtimeIntentSemanticContractInvalid {
		t.Fatalf("resolution enum remains strict, got %q", got)
	}
}

func TestIntentSemanticGateInformationObjectiveReclassifiesServiceRequest(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:    "service_request",
		IntentConfidence: 0.91,
		NeedsKnowledge:   true,
		NeedsResource:    true,
		NeedsTool:        true,
		NeedsHumanRoute:  true,
		ResourceAction:   "provide_location",
		ResourceActions:  []string{"provide_location"},
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:          "service_request",
			SubIntent:       "air_conditioner",
			Text:            "房间有空调吗",
			ResolvedText:    "房间有空调吗",
			NeedsKnowledge:  true,
			NeedsResource:   true,
			NeedsTool:       true,
			NeedsHumanRoute: true,
			ResourceAction:  "provide_location",
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "availability",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
	}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{RequireSemanticContract: true})
	task := got.Intent.IntentTasks[0]
	if task.Intent != "hotel_info" || !task.NeedsKnowledge {
		t.Fatalf("information request should become hotel_info knowledge, got %#v", task)
	}
	if task.NeedsResource || task.NeedsTool || task.NeedsHumanRoute || task.ResourceAction != "" {
		t.Fatalf("information request must not execute real-world actions, got %#v", task)
	}
	if got.Intent.PrimaryIntent != "hotel_info" || got.Intent.NeedsResource || got.Intent.NeedsTool || got.Intent.NeedsHumanRoute {
		t.Fatalf("top-level summary must be recomputed from repaired task, got %#v", got.Intent)
	}
}

func TestIntentSemanticGateActionRequestKeepsServiceRequest(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:    "service_request",
		IntentConfidence: 0.88,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:         "service_request",
			SubIntent:      "maintenance",
			Text:           "叫人来看看空调",
			ResolvedText:   "叫人来看看空调",
			NeedsKnowledge: true,
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "action_request",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
	}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{})
	if task := got.Intent.IntentTasks[0]; task.Intent != "service_request" || !task.NeedsKnowledge {
		t.Fatalf("real action request should remain service_request, got %#v", task)
	}
	if !got.SuppressLegacyConfidenceFallback {
		t.Fatal("a complete clear semantic contract should bypass legacy confidence downgrade")
	}
}

func TestIntentSemanticGateServiceStatusDoesNotBecomeStaticHotelInfo(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "service_request",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:         "service_request",
			SubIntent:      "delivery_status",
			Text:           "我刚才要的浴巾送到哪了",
			ResolvedText:   "我刚才要的浴巾送到哪了",
			NeedsKnowledge: true,
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "status",
		RelationToPrevious: "follow_up",
		ResolutionState:    "resolved_from_context",
	}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{HasAdjacentContext: true})
	if task := got.Intent.IntentTasks[0]; task.Intent != "service_request" {
		t.Fatalf("an existing service status is not static hotel information, got %#v", task)
	}
}

func TestIntentSemanticGateSeparatesResolvableContextFromAdjacentAIReply(t *testing.T) {
	referenceIntent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "hotel_info", SubIntent: "surrounding_facilities",
			Text: "玩的勒", ResolvedText: "酒店附近有什么适合游玩的地方", NeedsKnowledge: true,
		}},
	}
	referenceSemantics := []runtimeIntentTaskSemantics{{
		Objective: "recommendation", RelationToPrevious: "reference_previous", ResolutionState: "resolved_from_context",
	}}
	context := runtimeIntentSemanticGateContext{HasResolvableAdjacentContext: true, HasAdjacentAIReply: false}
	reference := applyRuntimeIntentSemanticConsistencyGate(referenceIntent, referenceSemantics, context)
	if task := reference.Intent.IntentTasks[0]; task.Intent != "hotel_info" || task.SubIntent != "surrounding_facilities" || !task.NeedsKnowledge {
		t.Fatalf("ordinary reference must be allowed by adjacent conversational context, got %#v", task)
	}

	rejectedIntent := callbacks.IntentTraceData{
		PrimaryIntent: "human_complaint_risk",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "human_complaint_risk", SubIntent: "answer_rejected",
			Text: "你刚才答非所问", ResolvedText: "你刚才答非所问", NeedsHumanRoute: true,
		}},
	}
	rejectedSemantics := []runtimeIntentTaskSemantics{{
		Objective: "complaint", RelationToPrevious: "answer_rejected", ResolutionState: "clear",
	}}
	rejected := applyRuntimeIntentSemanticConsistencyGate(rejectedIntent, rejectedSemantics, context)
	if task := rejected.Intent.IntentTasks[0]; task.Intent != "interaction" || task.SubIntent != "frustration" || task.NeedsHumanRoute {
		t.Fatalf("answer rejection must still require a genuinely adjacent AI reply, got %#v", task)
	}
}

func TestIntentSemanticGateAcceptsCurrentTurnSourceContextWithoutHistoricalTurn(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "hotel_info", SubIntent: "parking", Objective: "availability",
			RelationToPrevious: "independent", ResolutionState: "resolved_from_context",
			Text: "我开电车来的你懂我意思吗", ResolvedText: "酒店停车场有没有电车充电桩",
			SourceRefs: []string{"U3", "U2"}, NeedsKnowledge: true,
		}},
	}

	got := applyRuntimeIntentSemanticConsistencyGateFromTrace(intent, runtimeIntentSemanticGateContext{
		RequireSemanticContract: true,
		CurrentTurnRefsValid:    true,
	})
	task := got.Intent.IntentTasks[0]
	if task.Intent != "hotel_info" || task.SubIntent != "parking" || !task.NeedsKnowledge || task.ResolutionState != "resolved_from_context" {
		t.Fatalf("current-turn sourceRefs must authorize the model-resolved task without older history: %#v", task)
	}
	if hasSemanticGateViolation(got.Violations, "context_resolution_unavailable") {
		t.Fatalf("current-turn source context must not be rejected as missing historical context: %#v", got.Violations)
	}
}

func TestIntentSemanticGateDoesNotUseCurrentSourcesToFakeHistoricalReference(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "hotel_info", SubIntent: "parking", Objective: "availability",
			RelationToPrevious: "reference_previous", ResolutionState: "resolved_from_context",
			Text: "我开电车来的你懂我意思吗", ResolvedText: "酒店停车场有没有电车充电桩",
			SourceRefs: []string{"U3", "U2"}, NeedsKnowledge: true,
		}},
	}

	got := applyRuntimeIntentSemanticConsistencyGateFromTrace(intent, runtimeIntentSemanticGateContext{
		RequireSemanticContract: true,
		CurrentTurnRefsValid:    true,
	})
	task := got.Intent.IntentTasks[0]
	if task.Intent != "interaction" || task.SubIntent != "clarify" || task.NeedsKnowledge {
		t.Fatalf("a declared historical reference still needs real adjacent history: %#v", task)
	}
	if !hasSemanticGateViolation(got.Violations, "context_resolution_unavailable") {
		t.Fatalf("missing historical context must remain visible in the gate trace: %#v", got.Violations)
	}
}

func TestIntentSemanticGateRequiresHistoricalContextForFollowUpEvenWithCurrentSources(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "hotel_info", SubIntent: "parking", Objective: "availability",
			RelationToPrevious: "follow_up", ResolutionState: "resolved_from_context",
			Text: "我开电车来的你懂我意思吗", ResolvedText: "酒店停车场有没有电车充电桩",
			SourceRefs: []string{"U3", "U2"}, NeedsKnowledge: true,
		}},
	}

	got := applyRuntimeIntentSemanticConsistencyGateFromTrace(intent, runtimeIntentSemanticGateContext{
		RequireSemanticContract: true,
		CurrentTurnRefsValid:    true,
	})
	task := got.Intent.IntentTasks[0]
	if task.Intent != "interaction" || task.SubIntent != "clarify" || task.NeedsKnowledge {
		t.Fatalf("follow_up must continue to require real adjacent history: %#v", task)
	}
	if !hasSemanticGateViolation(got.Violations, "context_resolution_unavailable") {
		t.Fatalf("missing follow-up history must remain visible in the gate trace: %#v", got.Violations)
	}
}

func TestIntentSemanticGateAmbiguousTaskCannotExecuteActions(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:    "human_complaint_risk",
		IntentConfidence: 0.7,
		NeedsKnowledge:   true,
		NeedsResource:    true,
		NeedsTool:        true,
		NeedsHumanRoute:  true,
		ResourceAction:   "provide_location",
		ResourceActions:  []string{"provide_location"},
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:          "human_complaint_risk",
			SubIntent:       "explicit_handoff",
			Text:            "那个多少钱",
			ResolvedText:    "早餐多少钱",
			NeedsKnowledge:  true,
			NeedsResource:   true,
			NeedsTool:       true,
			NeedsHumanRoute: true,
			ResourceAction:  "provide_location",
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "price",
		RelationToPrevious: "reference_previous",
		ResolutionState:    "ambiguous",
	}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{HasAdjacentContext: true})
	task := got.Intent.IntentTasks[0]
	if task.Intent != "interaction" || task.SubIntent != "clarify" || task.ResolvedText != task.Text {
		t.Fatalf("ambiguous task should become a bounded clarification, got %#v", task)
	}
	if task.NeedsKnowledge || task.NeedsResource || task.NeedsTool || task.NeedsHumanRoute || task.ResourceAction != "" {
		t.Fatalf("ambiguous task must not authorize downstream actions, got %#v", task)
	}
	if !got.Intent.NeedsClarification || got.Intent.NeedsKnowledge || got.Intent.NeedsResource || got.Intent.NeedsTool || got.Intent.NeedsHumanRoute {
		t.Fatalf("clarification summary is inconsistent, got %#v", got.Intent)
	}
	if got.SuppressLegacyConfidenceFallback {
		t.Fatal("ambiguous task must not suppress the legacy confidence fallback")
	}
}

func TestIntentSemanticGateAnswerRejectedRequiresMatchingClassification(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:         "hotel_info",
			SubIntent:      "parking",
			Text:           "你刚才不是说要开车吗",
			ResolvedText:   "你刚才不是说要开车吗",
			NeedsKnowledge: true,
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "complaint",
		RelationToPrevious: "answer_rejected",
		ResolutionState:    "clear",
	}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{HasAdjacentContext: true})
	if task := got.Intent.IntentTasks[0]; task.Intent != "interaction" || task.SubIntent != "clarify" || task.NeedsHumanRoute || task.ResolutionState != runtimeIntentResolutionAmbiguous {
		t.Fatalf("relation alone must not authorize a handoff, got %#v", task)
	}
}

func TestIntentSemanticGateAnswerRejectedIntentRequiresMatchingRelation(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "human_complaint_risk",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:          "human_complaint_risk",
			SubIntent:       "answer_rejected",
			Text:            "你刚才答非所问",
			ResolvedText:    "你刚才答非所问",
			NeedsHumanRoute: true,
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "complaint",
		RelationToPrevious: "follow_up",
		ResolutionState:    "clear",
	}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{HasAdjacentContext: true})
	if task := got.Intent.IntentTasks[0]; task.Intent != "interaction" || task.SubIntent != "clarify" || task.NeedsHumanRoute {
		t.Fatalf("handoff classification alone must not bypass relation consistency, got %#v", task)
	}
}

func TestIntentSemanticGateClarifyTaskRepairsResolutionState(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "interaction",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:       "interaction",
			SubIntent:    "clarify",
			Text:         "那个多少钱",
			ResolvedText: "那个多少钱",
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "unknown",
		RelationToPrevious: "reference_previous",
		ResolutionState:    "clear",
	}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{HasAdjacentContext: true})
	if task := got.Intent.IntentTasks[0]; task.ResolutionState != runtimeIntentResolutionAmbiguous || task.SubIntent != "clarify" || !got.Intent.NeedsClarification {
		t.Fatalf("clarify task must carry an ambiguous state, got task=%#v intent=%#v", task, got.Intent)
	}
}

func TestIntentSemanticGatePreservesLegitimateIndependentResolvedText(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_info",
		IntentConfidence: 0.86,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:         "hotel_info",
			SubIntent:      "delivery_robot",
			Text:           "有外卖机器人吗",
			ResolvedText:   "酒店有没有外卖机器人",
			NeedsKnowledge: true,
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "availability",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
	}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{})
	if task := got.Intent.IntentTasks[0]; task.ResolvedText != "酒店有没有外卖机器人" {
		t.Fatalf("semantic gate must preserve the model's legitimate self-contained wording, got %#v", task)
	}
}

func TestIntentSemanticGateContextResolutionRequiresAdjacentContext(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_info",
		IntentConfidence: 0.83,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:         "hotel_info",
			SubIntent:      "room_facility",
			Text:           "那麦田呢",
			ResolvedText:   "麦田房型有没有办公桌",
			NeedsKnowledge: true,
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "availability",
		RelationToPrevious: "follow_up",
		ResolutionState:    "resolved_from_context",
	}}

	withoutContext := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{})
	if task := withoutContext.Intent.IntentTasks[0]; task.Intent != "interaction" || task.SubIntent != "clarify" || task.NeedsKnowledge {
		t.Fatalf("context resolution without adjacent context must clarify, got %#v", task)
	}

	withContext := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{HasAdjacentContext: true})
	if task := withContext.Intent.IntentTasks[0]; task.Intent != "hotel_info" || task.ResolvedText != "麦田房型有没有办公桌" || !task.NeedsKnowledge {
		t.Fatalf("grounded adjacent follow-up should keep its resolved question, got %#v", task)
	}
}

func TestIntentSemanticGateClearNewContractBypassesLowConfidenceFallback(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:    "hotel_info",
		IntentConfidence: 0.2,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:         "hotel_info",
			SubIntent:      "parking",
			Text:           "停车怎么停",
			ResolvedText:   "停车怎么停",
			NeedsKnowledge: true,
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "general_guidance",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
	}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{})
	if !got.SuppressLegacyConfidenceFallback {
		t.Fatal("clear task semantics should be authoritative even when confidence is low")
	}
	if got.Intent.PrimaryIntent != "hotel_info" || got.Intent.NeedsClarification || !got.Intent.NeedsKnowledge {
		t.Fatalf("low confidence must not erase a clear semantic task, got %#v", got.Intent)
	}
}

func TestIntentSemanticGateIncompleteNewContractFailsClosed(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_variable",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:         "hotel_variable",
			SubIntent:      "location",
			Text:           "定位发我",
			ResolvedText:   "定位发我",
			NeedsResource:  true,
			ResourceAction: "provide_location",
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{Objective: "resource_request"}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{RequireSemanticContract: true})
	if got.ContractMode != runtimeIntentSemanticContractInvalid {
		t.Fatalf("expected invalid contract, got %q", got.ContractMode)
	}
	task := got.Intent.IntentTasks[0]
	if task.Intent != "interaction" || task.SubIntent != "clarify" || task.NeedsResource || task.ResourceAction != "" {
		t.Fatalf("partial semantic contract must fail closed, got %#v", task)
	}
}

func TestIntentSemanticGateV2PreservesModelTaskCount(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		IntentTasks: []callbacks.IntentTaskTraceData{
			{
				Intent: "hotel_info", SubIntent: "checkin_process", Text: "怎么办理入住", ResolvedText: "怎么办理入住",
				SourceRefs: []string{"U1"}, NeedsKnowledge: true,
			},
			{
				Intent: "interaction", SubIntent: "clarify", Text: "怎么办理入住", ResolvedText: "怎么办理入住",
				SourceRefs: []string{"U1"},
			},
		},
	}
	semantics := []runtimeIntentTaskSemantics{
		{Objective: "method", RelationToPrevious: "independent", ResolutionState: "clear"},
		{Objective: "resource", RelationToPrevious: "independent", ResolutionState: "clear"},
	}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{RequireSemanticContract: true})
	if len(got.Intent.IntentTasks) != 2 || len(got.TaskSemantics) != 2 {
		t.Fatalf("V2 semantic gate must not delete model-owned tasks, got tasks=%#v semantics=%#v", got.Intent.IntentTasks, got.TaskSemantics)
	}
	if hasSemanticGateViolation(got.Violations, "redundant_invalid_resource_task_dropped") {
		t.Fatalf("V2 must not run legacy task-count repair, got %#v", got.Violations)
	}
}

func TestIntentSemanticGateLegacyProfileIgnoresPartialSemanticFields(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:             "hotel_info",
			SubIntent:          "checkin_process",
			Objective:          "method",
			RelationToPrevious: "independent",
			Text:               "怎么办理入住",
			ResolvedText:       "怎么办理入住",
			NeedsKnowledge:     true,
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{Objective: "method", RelationToPrevious: "independent"}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{})
	if got.ContractMode != runtimeIntentSemanticContractLegacy {
		t.Fatalf("old Profile partial semantics must use legacy mode, got %q", got.ContractMode)
	}
	task := got.Intent.IntentTasks[0]
	if task.Intent != "hotel_info" || !task.NeedsKnowledge || task.SubIntent != "checkin_process" {
		t.Fatalf("legacy compatibility must retain the valid business task, got %#v", task)
	}
	if task.Objective != "" || task.RelationToPrevious != "" || task.ResolutionState != "" || len(got.TaskSemantics) != 0 {
		t.Fatalf("partial semantic fields must be ignored in legacy mode, got task=%#v semantics=%#v", task, got.TaskSemantics)
	}
}

func TestIntentSemanticGateIncompleteTaskDoesNotEraseOtherClearTasks(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_info", SubIntent: "breakfast", Text: "早餐几点", ResolvedText: "早餐几点", NeedsKnowledge: true},
			{Intent: "service_request", SubIntent: "air_conditioner", Text: "有空调吗", ResolvedText: "有空调吗", NeedsKnowledge: true},
		},
	}
	semantics := []runtimeIntentTaskSemantics{
		{Objective: "time", RelationToPrevious: "independent", ResolutionState: "clear"},
		{Objective: "availability"},
	}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{RequireSemanticContract: true})
	if got.ContractMode != runtimeIntentSemanticContractInvalid {
		t.Fatalf("expected partially invalid contract, got %q", got.ContractMode)
	}
	if task := got.Intent.IntentTasks[0]; task.Intent != "hotel_info" || !task.NeedsKnowledge || task.SubIntent != "breakfast" {
		t.Fatalf("clear task must survive another task's contract error, got %#v", task)
	}
	if task := got.Intent.IntentTasks[1]; task.Intent != "interaction" || task.SubIntent != "clarify" || task.NeedsKnowledge {
		t.Fatalf("only the incomplete task should be isolated, got %#v", task)
	}
	if got.Intent.PrimaryIntent != "hotel_info" || !got.Intent.NeedsKnowledge || !got.Intent.NeedsClarification {
		t.Fatalf("summary must retain the clear task and one clarification, got %#v", got.Intent)
	}
}

func TestIntentSemanticGateV2DoesNotDropDuplicateInvalidResourcePseudoTask(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		IntentTasks: []callbacks.IntentTaskTraceData{
			{
				Intent:         "hotel_info",
				SubIntent:      "checkin_process",
				Text:           "怎么办理入住",
				ResolvedText:   "怎么办理入住",
				SourceRefs:     []string{"U1"},
				NeedsKnowledge: true,
			},
			{
				Intent:             "interaction",
				SubIntent:          "clarify",
				Objective:          "resource",
				RelationToPrevious: "independent",
				ResolutionState:    "clear",
				Text:               "怎么办理入住",
				ResolvedText:       "怎么办理入住",
				SourceRefs:         []string{"U1"},
				Entities: []callbacks.IntentEntityTraceData{{
					Text: "小程序",
					Type: "resource",
				}},
				Reason: "用户询问入住办理时隐含入住小程序资源",
			},
			{
				Intent:         "hotel_info",
				SubIntent:      "parking",
				Text:           "停车收费吗",
				ResolvedText:   "停车收费吗",
				SourceRefs:     []string{"U1"},
				NeedsKnowledge: true,
			},
		},
	}
	semantics := []runtimeIntentTaskSemantics{
		{Objective: "method", RelationToPrevious: "independent", ResolutionState: "clear"},
		{Objective: "resource", RelationToPrevious: "independent", ResolutionState: "clear"},
		{Objective: "price", RelationToPrevious: "independent", ResolutionState: "clear"},
	}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{RequireSemanticContract: true})
	if got.ContractMode != runtimeIntentSemanticContractInvalid {
		t.Fatalf("invalid V2 task must remain visible for fail-closed handling, got %q", got.ContractMode)
	}
	if len(got.Intent.IntentTasks) != 3 || len(got.TaskSemantics) != 3 {
		t.Fatalf("V2 semantic gate must preserve model-owned task count, got tasks=%#v semantics=%#v", got.Intent.IntentTasks, got.TaskSemantics)
	}
	if !got.Intent.NeedsClarification || got.Intent.PrimaryIntent != "hotel_info" || !got.Intent.NeedsKnowledge {
		t.Fatalf("valid business tasks must survive beside the isolated invalid task, got %#v", got.Intent)
	}
	if got.SuppressLegacyConfidenceFallback {
		t.Fatal("an invalid V2 task must not suppress fail-closed handling")
	}
	if hasSemanticGateViolation(got.Violations, "redundant_invalid_resource_task_dropped") {
		t.Fatalf("V2 must not run legacy task-count repair, got %#v", got.Violations)
	}
}

func TestIntentSemanticGateKeepsIndependentInvalidResourceTask(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_info",
		IntentTasks: []callbacks.IntentTaskTraceData{
			{
				Intent:         "hotel_info",
				SubIntent:      "checkin_process",
				Text:           "怎么办理入住",
				ResolvedText:   "怎么办理入住",
				SourceRefs:     []string{"U1"},
				NeedsKnowledge: true,
			},
			{
				Intent:             "interaction",
				SubIntent:          "clarify",
				Objective:          "resource",
				RelationToPrevious: "independent",
				ResolutionState:    "clear",
				Text:               "定位发我",
				ResolvedText:       "定位发我",
				SourceRefs:         []string{"U2"},
			},
		},
	}
	semantics := []runtimeIntentTaskSemantics{
		{Objective: "method", RelationToPrevious: "independent", ResolutionState: "clear"},
		{Objective: "resource", RelationToPrevious: "independent", ResolutionState: "clear"},
	}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{RequireSemanticContract: true})
	if got.ContractMode != runtimeIntentSemanticContractInvalid {
		t.Fatalf("an independently sourced invalid task must remain visible for fail-closed handling, got %q", got.ContractMode)
	}
	if len(got.Intent.IntentTasks) != 2 {
		t.Fatalf("independent task must not be swallowed, got %#v", got.Intent.IntentTasks)
	}
	if task := got.Intent.IntentTasks[1]; task.Intent != "interaction" || task.SubIntent != "clarify" {
		t.Fatalf("independent invalid resource request should be isolated as clarification, got %#v", task)
	}
	if !got.Intent.NeedsClarification || !got.Intent.NeedsKnowledge {
		t.Fatalf("valid task should survive alongside bounded clarification, got %#v", got.Intent)
	}
}

func hasSemanticGateViolation(items []runtimeIntentSemanticViolation, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}

func TestIntentSemanticGateRecomputesMixedTaskSummary(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:   "human_complaint_risk",
		NeedsHumanRoute: true,
		IntentTasks: []callbacks.IntentTaskTraceData{
			{
				Intent:         "hotel_info",
				SubIntent:      "breakfast",
				Text:           "早餐几点",
				ResolvedText:   "早餐几点",
				NeedsKnowledge: true,
			},
			{
				Intent:          "human_complaint_risk",
				SubIntent:       "explicit_handoff",
				Text:            "那个找谁",
				ResolvedText:    "联系人工客服",
				NeedsHumanRoute: true,
			},
		},
	}
	semantics := []runtimeIntentTaskSemantics{
		{Objective: "time", RelationToPrevious: "independent", ResolutionState: "clear"},
		{Objective: "handoff", RelationToPrevious: "reference_previous", ResolutionState: "ambiguous"},
	}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{HasAdjacentContext: true})
	if got.Intent.PrimaryIntent != "hotel_info" || !got.Intent.NeedsKnowledge || !got.Intent.NeedsClarification || got.Intent.NeedsHumanRoute {
		t.Fatalf("valid task must survive while ambiguous handoff is isolated, got %#v", got.Intent)
	}
	if task := got.Intent.IntentTasks[1]; task.Intent != "interaction" || task.SubIntent != "clarify" {
		t.Fatalf("ambiguous task should remain visible as clarification, got %#v", task)
	}
}

func TestIntentSemanticGateFromTraceUsesCurrentTaskContract(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:    "service_request",
		IntentConfidence: 0.31,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:             "service_request",
			SubIntent:          "air_conditioner",
			Objective:          "availability",
			RelationToPrevious: "independent",
			ResolutionState:    "clear",
			Text:               "有空调吗",
			ResolvedText:       "房间有空调吗",
			NeedsKnowledge:     true,
		}},
	}

	got := applyRuntimeIntentSemanticConsistencyGateFromTrace(intent, runtimeIntentSemanticGateContext{})
	if got.ContractMode != runtimeIntentSemanticContractActive || !got.SuppressLegacyConfidenceFallback {
		t.Fatalf("expected active clear trace contract, got %#v", got)
	}
	if task := got.Intent.IntentTasks[0]; task.Intent != "hotel_info" || task.ResolvedText != "房间有空调吗" {
		t.Fatalf("trace-backed gate must preserve the legitimate resolved question, got %#v", task)
	}
}

func TestIntentSemanticGateInvalidResourceActionClarifiesWithoutCommit(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "hotel_variable",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent:         "hotel_variable",
			SubIntent:      "location",
			Text:           "把那个地方的定位发我",
			ResolvedText:   "把那个地方的定位发我",
			NeedsResource:  true,
			ResourceAction: "provide_external_location",
		}},
	}
	semantics := []runtimeIntentTaskSemantics{{
		Objective:          "resource_request",
		RelationToPrevious: "reference_previous",
		ResolutionState:    "clear",
	}}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{HasAdjacentContext: true})
	task := got.Intent.IntentTasks[0]
	if task.Intent != "interaction" || task.SubIntent != "clarify" || task.NeedsResource || task.ResourceAction != "" {
		t.Fatalf("unsupported resource must not reach Commit, got %#v", task)
	}
}

func TestIntentSemanticGateKeepsCheckinKnowledgeAsMixedPrimary(t *testing.T) {
	intent := callbacks.IntentTraceData{
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_info", SubIntent: "checkin_process", Text: "怎么办理入住", ResolvedText: "怎么办理入住", NeedsKnowledge: true},
			{Intent: "hotel_variable", SubIntent: "mini_program", Text: "发送入住小程序", ResolvedText: "发送入住小程序", NeedsResource: true, ResourceAction: "provide_mini_program"},
		},
	}
	semantics := []runtimeIntentTaskSemantics{
		{Objective: "how", RelationToPrevious: "independent", ResolutionState: "clear"},
		{Objective: "resource_request", RelationToPrevious: "independent", ResolutionState: "clear"},
	}

	got := applyRuntimeIntentSemanticConsistencyGate(intent, semantics, runtimeIntentSemanticGateContext{})
	if got.Intent.PrimaryIntent != "hotel_info" || !got.Intent.NeedsKnowledge || !got.Intent.NeedsResource {
		t.Fatalf("check-in knowledge must remain primary over its attached resource, got %#v", got.Intent)
	}
}
