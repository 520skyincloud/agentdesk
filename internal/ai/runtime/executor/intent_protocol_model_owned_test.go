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

func TestValidateRuntimeIntentDetectProtocolAllowsDistinctResolvedTasks(t *testing.T) {
	first := validRuntimeIntentProtocolTask("那麦田呢", "availability")
	first.SubIntent = "room_facilities"
	first.RelationToPrevious = "reference_previous"
	first.ResolutionState = runtimeIntentResolutionResolvedFromContext
	first.ResolvedText = "麦田房型有没有办公桌"
	second := first
	second.ResolvedText = "麦田房型是否配有办公桌"

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{first, second}}, nil, "那麦田呢？"); err != nil {
		t.Fatalf("different resolvedText values are not exact duplicates: %v", err)
	}
}

func TestRepairRuntimeIntentDetectProtocolCollapsesExactDuplicatesAndMergesSources(t *testing.T) {
	first := validRuntimeIntentProtocolTask("早餐几点", "time")
	first.SourceRefs = runtimeIntentSourceRefList{"U1"}
	second := first
	second.SourceRefs = runtimeIntentSourceRefList{"U1", "U2"}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{first, second}}

	repairRuntimeIntentDetectProtocol(&parsed, "", runtimeIntentProtocolRepairContext{}, true)
	if len(parsed.IntentTasks) != 1 {
		t.Fatalf("exact duplicate tasks must collapse locally, got %#v", parsed.IntentTasks)
	}
	if got := []string(parsed.IntentTasks[0].SourceRefs); strings.Join(got, ",") != "U1,U2" {
		t.Fatalf("collapsed duplicate must stably merge sourceRefs, got %#v", got)
	}
}

func TestValidateRuntimeIntentDetectProtocolRequiresEveryCurrentSourceReference(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有早餐吗？",
		"2. [消息102] 几点开始？",
		"3. [消息103] 在哪里吃？",
	})
	task := validRuntimeIntentProtocolTask("在哪里吃", "location")
	task.SourceRefs = runtimeIntentSourceRefList{"U3"}
	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, current)
	if err == nil || !strings.Contains(err.Error(), "U1") {
		t.Fatalf("an unreferenced current-turn source must fail without inferring task count, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAllowsManyToManySourceOwnership(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有早餐吗？",
		"2. [消息102] 几点开始？",
	})
	availability := validRuntimeIntentProtocolTask("有早餐吗", "availability")
	availability.SourceRefs = runtimeIntentSourceRefList{"U1"}
	timeTask := validRuntimeIntentProtocolTask("几点开始", "time")
	timeTask.RelationToPrevious = "independent"
	timeTask.ResolutionState = runtimeIntentResolutionResolvedFromContext
	timeTask.ResolvedText = "早餐几点开始"
	timeTask.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}
	location := validRuntimeIntentProtocolTask("早餐在哪里吃", "location")
	location.SourceRefs = runtimeIntentSourceRefList{"U1"}

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{availability, location, timeTask}}, nil, current); err != nil {
		t.Fatalf("one URef may own multiple tasks and one task may consume multiple URefs: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextRequiresAuthorizedContext(t *testing.T) {
	task := validRuntimeIntentProtocolTask("几点", "time")
	task.RelationToPrevious = "independent"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "早餐几点"

	for name, current := range map[string]string{
		"single_source": "几点？",
		"future_source": utils.BuildRuntimeCustomerBurstEnvelope([]string{"1. [消息101] 几点？", "2. [消息102] 有早餐吗？"}),
	} {
		t.Run(name, func(t *testing.T) {
			candidate := task
			if name == "future_source" {
				candidate.SourceRefs = runtimeIntentSourceRefList{"U1", "U2"}
			}
			err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{candidate}}, current, runtimeIntentProtocolRepairContext{}, true)
			if err == nil || !strings.Contains(err.Error(), "authorized context") {
				t.Fatalf("resolved current-turn context must use a second earlier URef, got %v", err)
			}
		})
	}

	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{"1. [消息101] 有早餐吗？", "2. [消息102] 几点？"})
	task.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}
	if err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, current, runtimeIntentProtocolRepairContext{}, true); err != nil {
		t.Fatalf("a grounded earlier current-turn source must authorize resolution: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextAcceptsAdjacentHumanServicePair(t *testing.T) {
	task := validRuntimeIntentProtocolTask("几点", "time")
	task.RelationToPrevious = "follow_up"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "早餐几点开始"
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "有早餐吗？",
		AdjacentServiceReply: "有的。",
	}
	if err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, "几点？", context, true); err != nil {
		t.Fatalf("a real adjacent customer and human-service reply pair must authorize resolution: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextAcceptsSameTurnFollowUpRefs(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有早餐吗？",
		"2. [消息102] 几点开始？",
	})
	task := validRuntimeIntentProtocolTask("几点开始", "time")
	task.RelationToPrevious = "follow_up"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "早餐几点开始"
	task.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}
	if err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, current, runtimeIntentProtocolRepairContext{}, true); err != nil {
		t.Fatalf("real earlier current-turn URefs must authorize resolution even when relation is follow_up: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextRejectsUngroundedSubjectWithoutEntities(t *testing.T) {
	task := validRuntimeIntentProtocolTask("几点", "time")
	task.RelationToPrevious = "follow_up"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "早餐几点开始"
	task.Entities = nil
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "停车场可以停车吗？",
		AdjacentServiceReply: "可以停车。",
	}
	err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, "几点？", context, true)
	if err == nil || !strings.Contains(err.Error(), "not grounded") {
		t.Fatalf("an empty entity list must not allow parking context to become a breakfast question: %v", err)
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
