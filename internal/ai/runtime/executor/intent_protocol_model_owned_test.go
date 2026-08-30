package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/pkg/utils"
)

func TestValidateRuntimeIntentDetectProtocolAcceptsModelOwnedRewrite(t *testing.T) {
	task := validRuntimeIntentProtocolTask("早餐供应时间", "time")
	task.ResolvedText = "酒店早餐几点开始供应"
	if err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		nil,
		"麻烦问下早饭啥时候开始",
	); err != nil {
		t.Fatalf("model-owned task text must not require a local literal-span match: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsMultipleTasksFromOnePhysicalSource(t *testing.T) {
	first := validRuntimeIntentProtocolTask("早餐几点", "time")
	first.SubIntent = "breakfast"
	second := validRuntimeIntentProtocolTask("停车免费吗", "price")
	second.SubIntent = "parking"
	if err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{first, second}},
		nil,
		"早餐几点，停车免费吗？",
	); err != nil {
		t.Fatalf("IntentDetect must be allowed to split one physical source into multiple semantic tasks: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsModelOwnedCompoundTask(t *testing.T) {
	task := validRuntimeIntentProtocolTask("附近有什么吃的和好玩的", "compound_information")
	task.SubIntent = "surrounding_facilities"
	task.ResolvedText = "酒店附近的餐饮和游玩地点推荐"
	if err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		nil,
		"附近有啥吃的推荐没，哪里好玩？",
	); err != nil {
		t.Fatalf("local candidate heuristics must not override a model-owned compound boundary: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsMissingAndUnknownSourceRefs(t *testing.T) {
	for name, refs := range map[string]runtimeIntentSourceRefList{
		"missing": nil,
		"unknown": {"U2"},
	} {
		t.Run(name, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask("早餐几点", "time")
			task.SourceRefs = refs
			if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, "早餐几点"); err == nil {
				t.Fatalf("invalid source refs must fail protocol validation: %#v", refs)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsReversedPrimarySourceOrder(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有早餐吗？",
		"2. [消息102] 停车免费吗？",
	})
	parking := validRuntimeIntentProtocolTask("停车免费吗", "price")
	parking.SubIntent = "parking"
	parking.SourceRefs = runtimeIntentSourceRefList{"U2"}
	breakfast := validRuntimeIntentProtocolTask("有早餐吗", "availability")
	breakfast.SubIntent = "breakfast"
	breakfast.SourceRefs = runtimeIntentSourceRefList{"U1"}

	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{parking, breakfast}}, nil, current)
	if err == nil || !strings.Contains(err.Error(), "out of current-turn source order") {
		t.Fatalf("reversed primary source order must fail, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsExactDuplicateDespiteDifferentResolution(t *testing.T) {
	first := validRuntimeIntentProtocolTask("那麦田呢", "availability")
	first.SubIntent = "room_facilities"
	first.RelationToPrevious = "reference_previous"
	first.ResolutionState = runtimeIntentResolutionResolvedFromContext
	first.ResolvedText = "麦田房型有没有办公桌"
	second := first
	second.ResolvedText = "麦田房型是否配有办公桌"

	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{first, second}}, nil, "那麦田呢？")
	if err == nil || !strings.Contains(err.Error(), "exactly duplicates") {
		t.Fatalf("resolvedText changes must not let an exact duplicate task evade validation: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAllowsDistinctTasksSharingSource(t *testing.T) {
	first := validRuntimeIntentProtocolTask("有办公桌吗", "availability")
	first.SubIntent = "room_facilities"
	second := validRuntimeIntentProtocolTask("有沙发吗", "availability")
	second.SubIntent = "room_facilities"
	if err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{first, second}},
		nil,
		"房间有办公桌和沙发吗？",
	); err != nil {
		t.Fatalf("distinct model-owned tasks may share one physical source: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextRejectsFabricatedEntity(t *testing.T) {
	task := validRuntimeIntentProtocolTask("那麦田呢", "availability")
	task.SubIntent = "room_facilities"
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "麦田房型有没有办公桌和沙发"
	task.Entities = runtimeIntentEntityList{
		{Text: "麦田", Type: "room_type"},
		{Text: "办公桌", Type: "facility"},
		{Text: "沙发", Type: "facility"},
	}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "合柴房型有没有办公桌？",
		AdjacentAIReply:      "合柴房型有办公桌。",
	}

	err := validateRuntimeIntentResolvedReferenceContext(parsed, "那麦田呢？", context, true)
	if err == nil || !strings.Contains(err.Error(), "沙发") {
		t.Fatalf("a truly fabricated resolved entity must still fail grounding: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextAcceptsModelRelationWithoutLocalVeto(t *testing.T) {
	task := validRuntimeIntentProtocolTask("早餐几点", "time")
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "有早餐吗？",
		AdjacentAIReply:      "有的。",
	}
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "早餐几点？", context, true); err != nil {
		t.Fatalf("local heuristics must not reject the model's bounded relation when no entity is fabricated: %v", err)
	}
}

func TestRepairRuntimeIntentDetectProtocolDoesNotRewriteModelOutput(t *testing.T) {
	task := validRuntimeIntentProtocolTask("几点", "time")
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "早餐几点"
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}

	repairRuntimeIntentDetectProtocol(&parsed, "几点？", runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "有早餐吗？",
		AdjacentAIReply:      "有的。",
	}, true)
	if got := parsed.IntentTasks[0]; got.Text != task.Text || got.ResolvedText != task.ResolvedText || got.RelationToPrevious != task.RelationToPrevious {
		t.Fatalf("local repair must be a no-op for model-owned semantics: %#v", got)
	}
}
