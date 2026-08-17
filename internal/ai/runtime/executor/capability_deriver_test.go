package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestDeriveRuntimeIntentCapabilitiesUsesPublishedConfig(t *testing.T) {
	contract := contracts.IntentTasksV2{
		SchemaVersion: contracts.IntentTasksV2SchemaVersion,
		DialogueAct:   "new_topic",
		Tasks: []contracts.IntentTaskV2{
			{Sequence: 1, Intent: "hotel_info", SubIntent: "parking", Text: "停车场在哪里", RequestMode: "answer", Confidence: 0.92},
			{Sequence: 2, Intent: "hotel_variable", SubIntent: "location", Text: "发酒店定位", RequestMode: "request_action", Confidence: 0.9},
		},
	}
	configs := []models.ReplyIntentConfig{
		{ID: 11, Code: "hotel_info", Name: "酒店信息", NeedsKnowledge: true, Status: enums.StatusOk},
		{ID: 12, Code: "hotel_variable", Name: "酒店变量", NeedsResource: true, ResourceType: "store_variable", Status: enums.StatusOk},
	}
	derived, err := DeriveRuntimeIntentCapabilities(contract, configs)
	if err != nil {
		t.Fatalf("derive capabilities: %v", err)
	}
	if len(derived) != 2 || !derived[0].NeedsKnowledge || derived[0].OutputMode != "text" {
		t.Fatalf("unexpected knowledge capability: %#v", derived)
	}
	if !derived[1].NeedsResource || derived[1].ResourceAction != "provide_location" || derived[1].OutputMode != "resource_only" {
		t.Fatalf("unexpected resource capability: %#v", derived[1])
	}
	trace := AdaptIntentV2ToLegacyTrace(contract, derived)
	if trace.PrimaryIntent != "hotel_info" || !trace.NeedsKnowledge || !trace.NeedsResource || len(trace.ResourceActions) != 1 {
		t.Fatalf("unexpected compatibility trace: %#v", trace)
	}
	if trace.IntentTasks[0].MatchedConfigID != 11 || trace.IntentTasks[1].MatchedConfigID != 12 {
		t.Fatalf("config evidence missing from trace: %#v", trace.IntentTasks)
	}
}

func TestAdaptIntentV2ToLegacyTraceKeepsMixedHumanTaskTaskScoped(t *testing.T) {
	contract := contracts.IntentTasksV2{
		SchemaVersion: contracts.IntentTasksV2SchemaVersion,
		DialogueAct:   "new_topic",
		Tasks: []contracts.IntentTaskV2{
			{Sequence: 1, Intent: "human_complaint_risk", SubIntent: "explicit_handoff", Text: "转人工", RequestMode: "request_action", Confidence: 0.95},
			{Sequence: 2, Intent: "hotel_info", SubIntent: "parking", Text: "停车场在哪里", RequestMode: "answer", Confidence: 0.9},
		},
	}
	configs := []models.ReplyIntentConfig{
		{ID: 11, Code: "human_complaint_risk", Name: "人工", NeedsHumanRoute: true, HumanRoutePolicy: "managed_mode", Status: enums.StatusOk},
		{ID: 12, Code: "hotel_info", Name: "酒店信息", NeedsKnowledge: true, Status: enums.StatusOk},
	}
	derived, err := DeriveRuntimeIntentCapabilities(contract, configs)
	if err != nil {
		t.Fatalf("derive capabilities: %v", err)
	}
	trace := AdaptIntentV2ToLegacyTrace(contract, derived)
	if trace.NeedsHumanRoute || !trace.NeedsKnowledge || trace.PrimaryIntent != "hotel_info" {
		t.Fatalf("mixed human task must not become top-level handoff while retaining task data: %#v", trace)
	}
	if len(trace.IntentTasks) != 2 || !trace.IntentTasks[0].NeedsHumanRoute {
		t.Fatalf("expected task-level human capability: %#v", trace.IntentTasks)
	}
}

func TestDeriveRuntimeIntentCapabilitiesRejectsUnknownOrDisabledIntent(t *testing.T) {
	contract := contracts.IntentTasksV2{
		SchemaVersion: contracts.IntentTasksV2SchemaVersion,
		DialogueAct:   "new_topic",
		Tasks:         []contracts.IntentTaskV2{{Sequence: 1, Intent: "unknown_code", Text: "test", RequestMode: "answer", Confidence: 0.8}},
	}
	if _, err := DeriveRuntimeIntentCapabilities(contract, []models.ReplyIntentConfig{{Code: "unknown_code", Status: enums.StatusDisabled}}); err == nil {
		t.Fatal("expected disabled intent to be rejected")
	}
}

func TestDeriveRuntimeIntentCapabilitiesRejectsDuplicateSequence(t *testing.T) {
	contract := contracts.IntentTasksV2{
		SchemaVersion: contracts.IntentTasksV2SchemaVersion,
		DialogueAct:   "new_topic",
		Tasks: []contracts.IntentTaskV2{
			{Sequence: 1, Intent: "interaction", Text: "你好", RequestMode: "social", Confidence: 0.9},
			{Sequence: 1, Intent: "interaction", Text: "谢谢", RequestMode: "social", Confidence: 0.9},
		},
	}
	if _, err := DeriveRuntimeIntentCapabilities(contract, []models.ReplyIntentConfig{{Code: "interaction", Status: enums.StatusOk}}); err == nil {
		t.Fatal("expected duplicate sequence to be rejected")
	}
}
