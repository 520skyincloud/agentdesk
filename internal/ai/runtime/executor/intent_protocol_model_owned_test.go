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

func TestValidateRuntimeIntentDetectProtocolRejectsSingleSourceQuestionReplacement(t *testing.T) {
	task := validRuntimeIntentProtocolTask("停车是否免费", "price")
	task.SubIntent = "parking"
	err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		nil,
		"早餐几点？",
	)
	if err == nil || !strings.Contains(err.Error(), "not grounded") {
		t.Fatalf("one unrelated business task must not replace the customer's only question: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsLiteralSpanWithWrongObjectiveAndResolvedTopic(t *testing.T) {
	tests := []runtimeIntentTaskJSON{
		func() runtimeIntentTaskJSON {
			task := validRuntimeIntentProtocolTask("几点", "price")
			task.SubIntent = "parking"
			task.ResolvedText = "停车多少钱"
			task.Entities = runtimeIntentEntityList{{Text: "停车", Type: "service"}}
			return task
		}(),
		func() runtimeIntentTaskJSON {
			task := validRuntimeIntentProtocolTask("早餐几点", "price")
			task.SubIntent = "parking"
			task.ResolvedText = "停车多少钱"
			task.Entities = runtimeIntentEntityList{{Text: "停车", Type: "service"}}
			return task
		}(),
	}
	for _, task := range tests {
		if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, "早餐几点？"); err == nil {
			t.Fatalf("literal source ownership must not hide a wrong objective or resolved topic: %#v", task)
		}
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsWrongTaskOccupyingAnotherCandidate(t *testing.T) {
	wrong := validRuntimeIntentProtocolTask("几点", "price")
	wrong.SubIntent = "parking"
	wrong.ResolvedText = "停车多少钱"
	wrong.Entities = runtimeIntentEntityList{{Text: "停车", Type: "service"}}
	parking := validRuntimeIntentProtocolTask("停车免费吗", "price")
	parking.SubIntent = "parking"
	err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{wrong, parking}},
		nil,
		"早餐几点？停车免费吗？",
	)
	if err == nil {
		t.Fatal("a wrong short task must not occupy the breakfast candidate")
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsDuplicateNestedOwnership(t *testing.T) {
	breakfast := validRuntimeIntentProtocolTask("早餐几点", "time")
	parking := validRuntimeIntentProtocolTask("停车免费吗", "price")
	extra := validRuntimeIntentProtocolTask("免费吗", "price")
	extra.ResolvedText = "停车免费吗"
	extra.Entities = runtimeIntentEntityList{{Text: "停车", Type: "service"}}
	err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{breakfast, parking, extra}},
		nil,
		"早餐几点？停车免费吗？",
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate task ownership") {
		t.Fatalf("a second task for the same answer target must be rejected: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsStoreScopeAsCompoundSubject(t *testing.T) {
	text := "南七店早餐几点？南七店停车免费吗？"
	task := validRuntimeIntentProtocolTask(text, "compound_information")
	task.Entities = runtimeIntentEntityList{{Text: "南七店", Type: "location"}}
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, text); err == nil {
		t.Fatal("a shared store scope must not merge breakfast and parking into one compound task")
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsKnowledgeAndActionCompound(t *testing.T) {
	text := "矿泉水有几瓶？矿泉水能帮我送来吗？"
	task := validRuntimeIntentProtocolTask(text, "compound_information")
	task.Entities = runtimeIntentEntityList{{Text: "矿泉水", Type: "supply"}}
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, text); err == nil {
		t.Fatal("a knowledge question and a real-world action request must not share one compound task")
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsMissingSecondQuestionInOneSource(t *testing.T) {
	breakfast := validRuntimeIntentProtocolTask("早餐几点", "time")
	breakfast.SubIntent = "breakfast"
	err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{breakfast}},
		nil,
		"早餐几点？停车免费吗？",
	)
	if err == nil || !strings.Contains(err.Error(), "atomic question 2 of 2") {
		t.Fatalf("a second independent question in the same source must not be dropped: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsContextRefReplacingPrimaryOwnership(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有没有停车场？",
		"2. [消息102] 我开电车来的你懂我意思吗？",
	})
	charging := validRuntimeIntentProtocolTask("我开电车来的你懂我意思吗", "availability")
	charging.SubIntent = "parking_charging"
	charging.RelationToPrevious = "independent"
	charging.ResolutionState = runtimeIntentResolutionResolvedFromContext
	charging.ResolvedText = "酒店停车场有没有电车充电桩"
	charging.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}
	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{charging}}, nil, current)
	if err == nil || !strings.Contains(err.Error(), "source U1") {
		t.Fatalf("a context ref must not replace primary ownership of a self-contained question: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsContinuousQuestionsCollapsedIntoLastTask(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有早餐吗？",
		"2. [消息102] 几点？",
		"3. [消息103] 在哪吃？",
	})
	location := validRuntimeIntentProtocolTask("在哪吃", "location")
	location.SubIntent = "breakfast"
	location.RelationToPrevious = "independent"
	location.ResolutionState = runtimeIntentResolutionResolvedFromContext
	location.ResolvedText = "酒店早餐在哪里吃"
	location.SourceRefs = runtimeIntentSourceRefList{"U3", "U1", "U2"}
	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{location}}, nil, current)
	if err == nil || !strings.Contains(err.Error(), "source U1") {
		t.Fatalf("earlier business questions must not be counted as covered only because they are context refs: %v", err)
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

func TestValidateRuntimeIntentDetectProtocolRejectsIndependentNearbyTargetsHiddenInCompoundTask(t *testing.T) {
	task := validRuntimeIntentProtocolTask("附近有什么吃的和好玩的", "compound_information")
	task.SubIntent = "surrounding_facilities"
	task.ResolvedText = "酒店附近的餐饮和游玩地点推荐"
	err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		nil,
		"附近有啥吃的推荐没，哪里好玩？",
	)
	if err == nil {
		t.Fatal("independent dining and sightseeing targets must not be hidden in one compound task")
	}

	food := validRuntimeIntentProtocolTask("附近有啥吃的推荐没", "recommendation")
	food.SubIntent = "surrounding_food"
	play := validRuntimeIntentProtocolTask("哪里好玩", "recommendation")
	play.SubIntent = "surrounding_attractions"
	if err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{food, play}},
		nil,
		"附近有啥吃的推荐没，哪里好玩？",
	); err != nil {
		t.Fatalf("independent nearby targets must pass when Intent owns both in order: %v", err)
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

func TestRepairRuntimeIntentDetectProtocolKeepsDistinctResolvedOwnership(t *testing.T) {
	first := validRuntimeIntentProtocolTask("那麦田呢", "availability")
	first.SubIntent = "room_facilities"
	first.RelationToPrevious = "reference_previous"
	first.ResolutionState = runtimeIntentResolutionResolvedFromContext
	first.ResolvedText = "麦田房型有没有办公桌"
	second := first
	second.ResolvedText = "麦田房型是否配有办公桌"
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{first, second}}

	repairRuntimeIntentDetectProtocol(&parsed, "那麦田呢？", runtimeIntentProtocolRepairContext{}, true)
	if len(parsed.IntentTasks) != 2 {
		t.Fatalf("different normalized resolvedText values must not be silently collapsed: %#v", parsed.IntentTasks)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsReversedTaskOrderWithinOneSource(t *testing.T) {
	breakfast := validRuntimeIntentProtocolTask("早餐几点", "time")
	breakfast.SubIntent = "breakfast"
	parking := validRuntimeIntentProtocolTask("停车免费吗", "price")
	parking.SubIntent = "parking"
	current := "早餐几点，停车免费吗？"

	err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{parking, breakfast}},
		nil,
		current,
	)
	if err == nil || !strings.Contains(err.Error(), "source text order") {
		t.Fatalf("uniquely located tasks from one source must preserve the customer's order: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsUnlocatableRewrittenTasksWithinOneSource(t *testing.T) {
	parking := validRuntimeIntentProtocolTask("停车收费情况", "price")
	parking.SubIntent = "parking"
	parking.ResolvedText = "酒店停车是否免费"
	breakfast := validRuntimeIntentProtocolTask("早餐供应时间", "time")
	breakfast.SubIntent = "breakfast"
	breakfast.ResolvedText = "酒店早餐几点供应"
	current := "麻烦说下早餐什么时候供应，停车是不是免费"

	err := validateRuntimeIntentDetectProtocol(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{parking, breakfast}},
		nil,
		current,
	)
	if err == nil || !strings.Contains(err.Error(), "cannot be uniquely located") {
		t.Fatalf("rewritten multi-task text must not bypass source order validation: %v", err)
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

func TestValidateRuntimeIntentDetectProtocolRejectsUnclaimedCurrentSource(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有早餐吗？",
		"2. [消息102] 停车免费吗？",
	})
	parking := validRuntimeIntentProtocolTask("停车免费吗", "price")
	parking.SubIntent = "parking"
	parking.SourceRefs = runtimeIntentSourceRefList{"U2"}

	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{parking}}, nil, current)
	if err == nil || !strings.Contains(err.Error(), "current-turn source U1 is not referenced") {
		t.Fatalf("an omitted physical source must fail without locally inferring task boundaries, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsExplicitInteractionOwnership(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 谢谢",
		"2. [消息102] 早餐几点？",
	})
	thanks := validRuntimeIntentProtocolTask("谢谢", "general_guidance")
	thanks.Intent = "interaction"
	thanks.SubIntent = "acknowledgement"
	thanks.NeedsKnowledge = false
	thanks.SourceRefs = runtimeIntentSourceRefList{"U1"}
	breakfast := validRuntimeIntentProtocolTask("早餐几点", "time")
	breakfast.SubIntent = "breakfast"
	breakfast.SourceRefs = runtimeIntentSourceRefList{"U2"}

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{thanks, breakfast}}, nil, current); err != nil {
		t.Fatalf("politeness may be explicitly owned by an interaction Task without becoming a business Task: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsWeatherToolInteraction(t *testing.T) {
	task := validRuntimeIntentProtocolTask("合肥今天什么天气", "general_guidance")
	task.Intent = "interaction"
	task.SubIntent = "weather_query"
	task.NeedsKnowledge = false
	task.NeedsTool = true

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err != nil {
		t.Fatalf("the documented weather interaction must survive raw Intent protocol validation: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsIndirectWeatherQuestions(t *testing.T) {
	for _, text := range []string{"今天热不热", "明天要带伞吗"} {
		t.Run(text, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(text, "general_guidance")
			task.Intent = "interaction"
			task.SubIntent = "weather_query"
			task.NeedsKnowledge = false
			task.NeedsTool = true

			if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, text); err != nil {
				t.Fatalf("an indirect but explicit weather request must keep the weather tool path: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsHotelOperationDisguisedAsWeather(t *testing.T) {
	task := validRuntimeIntentProtocolTask("房间空调热不热", "general_guidance")
	task.Intent = "interaction"
	task.SubIntent = "weather_query"
	task.NeedsKnowledge = false
	task.NeedsTool = true
	task.Entities = runtimeIntentEntityList{{Text: "空调", Type: "facility"}}

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err == nil {
		t.Fatal("a hotel facility question cannot bypass knowledge by borrowing the weather label")
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsSelfContainedSocialQuestions(t *testing.T) {
	tests := []struct {
		text      string
		subIntent string
		objective string
	}{
		{text: "你是谁", subIntent: "ai_identity", objective: "identity"},
		{text: "你好吗", subIntent: "social", objective: "social"},
		{text: "你在干嘛", subIntent: "social", objective: "social"},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(test.text, test.objective)
			task.Intent = "interaction"
			task.SubIntent = test.subIntent
			task.NeedsKnowledge = false

			if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err != nil {
				t.Fatalf("a model-owned non-business interaction question must remain valid: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsContextResolvedSocialQuestion(t *testing.T) {
	task := validRuntimeIntentProtocolTask("那你呢", "social")
	task.Intent = "interaction"
	task.SubIntent = "social"
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "你最近好吗"
	task.NeedsKnowledge = false
	task.Entities = nil

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err != nil {
		t.Fatalf("a bounded social follow-up must not be rejected as hotel business only because it uses context: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsHotelBusinessQuestionsDisguisedAsInteraction(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		subIntent string
		objective string
	}{
		{name: "facility as social", text: "房间有空调吗", subIntent: "social", objective: "social"},
		{name: "breakfast time as frustration", text: "早餐几点", subIntent: "frustration", objective: "social"},
		{name: "invoice method as frustration", text: "发票怎么开", subIntent: "frustration", objective: "social"},
		{name: "owner as acknowledgement", text: "老板是谁", subIntent: "acknowledgement", objective: "social"},
		{name: "hotel owner as ai identity", text: "你们酒店老板是谁", subIntent: "ai_identity", objective: "identity"},
		{name: "door action as social", text: "你能帮我开门吗", subIntent: "social", objective: "social"},
		{name: "parking correction", text: "我问的是停车入口", subIntent: "correction", objective: "social"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(test.text, test.objective)
			task.Intent = "interaction"
			task.SubIntent = test.subIntent
			task.NeedsKnowledge = false
			task.Entities = nil

			if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err == nil {
				t.Fatalf("a clear hotel business target must be repaired instead of bypassing knowledge: %#v", task)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolKeepsNonBusinessInteractionBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		subIntent string
		objective string
	}{
		{name: "ai identity", text: "你是机器人吗", subIntent: "ai_identity", objective: "identity"},
		{name: "social question", text: "你在做什么", subIntent: "social", objective: "social"},
		{name: "frustration", text: "你怎么这么慢", subIntent: "frustration", objective: "social"},
		{name: "channel correction", text: "我没发语音大哥", subIntent: "correction", objective: "social"},
		{name: "external social question", text: "北京有什么好玩的", subIntent: "social", objective: "social"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(test.text, test.objective)
			task.Intent = "interaction"
			task.SubIntent = test.subIntent
			task.NeedsKnowledge = false

			if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err != nil {
				t.Fatalf("a genuine non-business interaction must remain valid: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsInvalidWeatherInteractionShape(t *testing.T) {
	missingTool := validRuntimeIntentProtocolTask("合肥天气怎么样", "general_guidance")
	missingTool.Intent = "interaction"
	missingTool.SubIntent = "weather_query"
	missingTool.NeedsKnowledge = false
	missingTool.NeedsTool = false
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{missingTool}}, nil, missingTool.Text); err == nil {
		t.Fatal("weather_query without the weather tool must be repaired")
	}

	fakeWeather := validRuntimeIntentProtocolTask("帮我查订单", "action_request")
	fakeWeather.Intent = "interaction"
	fakeWeather.SubIntent = "weather_query"
	fakeWeather.NeedsKnowledge = false
	fakeWeather.NeedsTool = true
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{fakeWeather}}, nil, fakeWeather.Text); err == nil {
		t.Fatal("a business action cannot pass by borrowing the weather tool label")
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsUnexpectedInteractionTool(t *testing.T) {
	task := validRuntimeIntentProtocolTask("帮我查订单", "action_request")
	task.Intent = "interaction"
	task.SubIntent = "social"
	task.NeedsKnowledge = false
	task.NeedsTool = true

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err == nil {
		t.Fatal("only the documented weather interaction may carry a tool action")
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsMixedKnowledgeAndWeatherTasks(t *testing.T) {
	breakfast := validRuntimeIntentProtocolTask("早餐几点", "time")
	breakfast.SubIntent = "breakfast"
	weather := validRuntimeIntentProtocolTask("合肥今天什么天气", "general_guidance")
	weather.Intent = "interaction"
	weather.SubIntent = "weather_query"
	weather.NeedsKnowledge = false
	weather.NeedsTool = true

	current := "早餐几点，合肥今天什么天气？"
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{breakfast, weather}}, nil, current); err != nil {
		t.Fatalf("a valid mixed hotel and weather turn must not lose the whole Intent result: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsNonQuestionContextSourceOwnership(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 嗯，接着说",
		"2. [消息102] 我开电车来的你懂我意思吗？",
	})
	charging := validRuntimeIntentProtocolTask("我开电车来的你懂我意思吗", "availability")
	charging.SubIntent = "parking_charging"
	charging.ResolutionState = runtimeIntentResolutionResolvedFromContext
	charging.ResolvedText = "酒店停车场有没有电车充电桩"
	charging.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{charging}}, nil, current); err != nil {
		t.Fatalf("a non-question URef may remain context-only: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsGenuinelyUnclearInteraction(t *testing.T) {
	task := validRuntimeIntentProtocolTask("那个呢", "unknown")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = "那个呢"
	task.Entities = nil
	task.NeedsKnowledge = false

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, "那个呢"); err != nil {
		t.Fatalf("a genuinely ambiguous interaction without a clear business target must remain valid: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsStandalonePreferenceFragmentAsClarify(t *testing.T) {
	task := validRuntimeIntentProtocolTask("麻辣口味的", "recommendation")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = task.Text
	task.Entities = nil
	task.NeedsKnowledge = false

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err != nil {
		t.Fatalf("a standalone preference fragment without a concrete target may remain a clarification: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextRejectsAdjacentSlotAnswerAsClarify(t *testing.T) {
	task := validRuntimeIntentProtocolTask("麻辣口味的", "recommendation")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = task.Text
	task.Entities = nil
	task.NeedsKnowledge = false
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}

	if err := validateRuntimeIntentDetectProtocol(parsed, nil, task.Text); err != nil {
		t.Fatalf("the base protocol must not infer missing standalone context: %v", err)
	}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "附近有什么好吃的？",
		AdjacentAIReply:      "附近餐饮想吃什么口味？",
	}
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true); err == nil || !strings.Contains(err.Error(), "adjacent service clarification") {
		t.Fatalf("an answer to the adjacent AI slot question must retry as the original business task: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextRejectsExplicitAdjacentAIAnswerDenialAsInteraction(t *testing.T) {
	task := validRuntimeIntentProtocolTask("你刚才答非所问", "social")
	task.Intent = "interaction"
	task.SubIntent = "frustration"
	task.RelationToPrevious = "independent"
	task.ResolutionState = runtimeIntentResolutionClear
	task.NeedsKnowledge = false
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "房间里有没有牙刷？",
		AdjacentAIReply:      "牙刷可以去洗衣房领取。",
		AdjacentServiceReply: "牙刷可以去洗衣房领取。",
	}

	err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true)
	if err == nil || !strings.Contains(err.Error(), "adjacent AI answer") {
		t.Fatalf("an explicit rejection of the adjacent AI answer must retry Intent instead of becoming frustration: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextGroundsContradictionsToAdjacentTopic(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		previous   string
		answer     string
		wantReject bool
	}{
		{
			name: "same route topic", text: "你刚才不是说要开车吗",
			previous: "去小丁小吃怎么走？", answer: "从酒店走路就能到。", wantReject: true,
		},
		{
			name: "unrelated breakfast topic", text: "你刚才不是说早餐七点吗",
			previous: "停车免费吗？", answer: "停车是免费的。", wantReject: false,
		},
		{
			name: "human fact contradiction", text: "客服说可以微信转账",
			previous: "延迟退房可以微信转账吗？", answer: "不支持微信转账。", wantReject: true,
		},
		{
			name: "unrelated human fact", text: "客服说可以微信转账",
			previous: "早餐几点？", answer: "早餐七点开始。", wantReject: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(test.text, "social")
			task.Intent = "interaction"
			task.SubIntent = "frustration"
			task.RelationToPrevious = "independent"
			task.ResolutionState = runtimeIntentResolutionClear
			task.NeedsKnowledge = false
			task.Entities = nil
			context := runtimeIntentProtocolRepairContext{
				PreviousCustomerText: test.previous,
				AdjacentAIReply:      test.answer,
				AdjacentServiceReply: test.answer,
			}

			err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, test.text, context, true)
			if test.wantReject && (err == nil || !strings.Contains(err.Error(), "adjacent AI answer")) {
				t.Fatalf("a grounded contradiction must retry as answer_rejected, got %v", err)
			}
			if !test.wantReject && err != nil {
				t.Fatalf("an unrelated phrase must not become answer_rejected by substring alone: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentResolvedContextValidatesAnswerRejectedGrounding(t *testing.T) {
	build := func(text string) runtimeIntentTaskJSON {
		task := validRuntimeIntentProtocolTask(text, "complaint")
		task.Intent = "human_complaint_risk"
		task.SubIntent = "answer_rejected"
		task.RelationToPrevious = "answer_rejected"
		task.NeedsKnowledge = false
		task.NeedsHumanRoute = true
		return task
	}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "延迟退房可以微信转账吗？",
		AdjacentAIReply:      "不支持微信转账。",
		AdjacentServiceReply: "不支持微信转账。",
	}
	if err := validateRuntimeIntentResolvedReferenceContext(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{build("客服说可以微信转账")}},
		"客服说可以微信转账",
		context,
		true,
	); err != nil {
		t.Fatalf("a grounded human-fact contradiction must remain answer_rejected: %v", err)
	}
	if err := validateRuntimeIntentResolvedReferenceContext(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{build("客服说早餐七点开始")}},
		"客服说早餐七点开始",
		context,
		true,
	); err == nil || !strings.Contains(err.Error(), "not grounded") {
		t.Fatalf("an unrelated answer_rejected task must fail grounding, got %v", err)
	}
	breakfastContext := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "早餐几点？",
		AdjacentAIReply:      "早餐七点开始。",
		AdjacentServiceReply: "早餐七点开始。",
	}
	if err := validateRuntimeIntentResolvedReferenceContext(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{build("早餐几点结束")}},
		"早餐几点结束",
		breakfastContext,
		true,
	); err == nil || !strings.Contains(err.Error(), "not grounded") {
		t.Fatalf("an ordinary same-topic follow-up without rejection semantics must not become answer_rejected, got %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextKeepsIndependentFrustrationAndQuestions(t *testing.T) {
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "停车免费吗？",
		AdjacentAIReply:      "停车免费。",
		AdjacentServiceReply: "停车免费。",
	}
	for _, text := range []string{"你怎么这么慢", "服务太差了", "真的吗", "为什么"} {
		t.Run(text, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(text, "social")
			task.Intent = "interaction"
			task.SubIntent = "frustration"
			task.RelationToPrevious = "independent"
			task.ResolutionState = runtimeIntentResolutionClear
			task.NeedsKnowledge = false

			if err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, text, context, true); err != nil {
				t.Fatalf("ordinary frustration or an isolated follow-up word must not be forced into answer_rejected: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentResolvedContextDoesNotApplyAnswerRejectedToHumanReply(t *testing.T) {
	task := validRuntimeIntentProtocolTask("你刚才答非所问", "social")
	task.Intent = "interaction"
	task.SubIntent = "frustration"
	task.NeedsKnowledge = false
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "房间里有没有牙刷？",
		AdjacentServiceReply: "牙刷可以去洗衣房领取。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, task.Text, context, true); err != nil {
		t.Fatalf("answer_rejected is defined only for an immediately adjacent AI answer: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextRejectsAffirmativeAnswerToAdjacentYesNoQuestionAsClarify(t *testing.T) {
	for _, question := range []string{
		"您是问酒店有没有充电桩吗？",
		"您是问酒店有没有充电桩吗",
	} {
		t.Run(question, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask("是的啊", "unknown")
			task.Intent = "interaction"
			task.SubIntent = "clarify"
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			task.ResolvedText = task.Text
			task.Entities = nil
			task.NeedsKnowledge = false
			parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
			context := runtimeIntentProtocolRepairContext{
				PreviousCustomerText: "我开电车来的，你懂我意思吗？",
				AdjacentAIReply:      question,
			}

			if err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true); err == nil || !strings.Contains(err.Error(), "adjacent service clarification") {
				t.Fatalf("an affirmative answer to an adjacent yes/no clarification must retry as the original business task: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentResolvedContextRejectsAdjacentBusinessAnswerAsAnyInteraction(t *testing.T) {
	for _, subIntent := range []string{"social", "confirm"} {
		t.Run(subIntent, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask("是的啊", "unknown")
			task.Intent = "interaction"
			task.SubIntent = subIntent
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			task.ResolvedText = task.Text
			task.Entities = nil
			task.NeedsKnowledge = false
			context := runtimeIntentProtocolRepairContext{
				PreviousCustomerText: "我开电车来的，你懂我意思吗？",
				AdjacentAIReply:      "您是问酒店有没有充电桩吗",
			}

			err := validateRuntimeIntentResolvedReferenceContext(
				runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
				task.Text,
				context,
				true,
			)
			if err == nil || !strings.Contains(err.Error(), "adjacent service clarification") {
				t.Fatalf("an adjacent business answer must not escape through interaction/%s: %v", subIntent, err)
			}
		})
	}
}

func TestValidateRuntimeIntentResolvedContextUsesHumanServiceSlotQuestion(t *testing.T) {
	tests := []struct {
		name     string
		question string
		answer   string
	}{
		{name: "room number", question: "方便说下是哪个房间吗？", answer: "1208"},
		{name: "guest name", question: "请提供一下入住人姓名？", answer: "吴朝伟"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(test.answer, "unknown")
			task.Intent = "interaction"
			task.SubIntent = "clarify"
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			task.ResolvedText = test.answer
			task.Entities = nil
			task.NeedsKnowledge = false
			context := runtimeIntentProtocolRepairContext{
				PreviousCustomerText: "空调坏了",
				AdjacentServiceReply: test.question,
			}

			err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, test.answer, context, true)
			if err == nil || !strings.Contains(err.Error(), "adjacent service clarification") {
				t.Fatalf("an answer to a human-service slot question must retry as the original business task: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentResolvedContextAllowsGenericOpenHelpQuestion(t *testing.T) {
	for _, question := range []string{
		"您想了解什么？", "您想问什么？", "今天想聊点什么？",
		"您还有什么想了解的吗？", "有什么想咨询的吗？", "还有什么需要吗？",
	} {
		t.Run(question, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask("还没想好", "unknown")
			task.Intent = "interaction"
			task.SubIntent = "clarify"
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			task.ResolvedText = task.Text
			task.Entities = nil
			task.NeedsKnowledge = false
			context := runtimeIntentProtocolRepairContext{
				PreviousCustomerText: "你好",
				AdjacentServiceReply: question,
			}

			if err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, task.Text, context, true); err != nil {
				t.Fatalf("an ambiguous answer after a generic open-help question may remain clarify: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentResolvedContextRejectsBusinessSpecificOpenQuestionAsClarify(t *testing.T) {
	task := validRuntimeIntentProtocolTask("麦田的", "unknown")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = task.Text
	task.Entities = nil
	task.NeedsKnowledge = false
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "想了解房型",
		AdjacentServiceReply: "您想了解什么房型？",
	}

	err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, task.Text, context, true)
	if err == nil || !strings.Contains(err.Error(), "adjacent service clarification") {
		t.Fatalf("an answer to a business-specific open question must retry as the original business task: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextRejectsCompletedAnswerRepeatAsClarify(t *testing.T) {
	task := validRuntimeIntentProtocolTask("外卖地址再说一遍", "unknown")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = task.Text
	task.Entities = nil
	task.NeedsKnowledge = false
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "外卖地址怎么填？",
		AdjacentAIReply:      "丽斯未来酒店合肥南七店加对应楼层房间号。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true); err == nil || !strings.Contains(err.Error(), "completed-answer repeat") {
		t.Fatalf("a completed-answer repeat must re-enter the business task: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextRejectsCompletedAnswerRepeatAsAnyInteraction(t *testing.T) {
	for _, subIntent := range []string{"social", "confirm"} {
		t.Run(subIntent, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask("外卖地址再说一遍", "unknown")
			task.Intent = "interaction"
			task.SubIntent = subIntent
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			task.ResolvedText = task.Text
			task.Entities = nil
			task.NeedsKnowledge = false
			context := runtimeIntentProtocolRepairContext{
				PreviousCustomerText: "外卖地址怎么填？",
				AdjacentAIReply:      "丽斯未来酒店合肥南七店加对应楼层房间号。",
			}

			err := validateRuntimeIntentResolvedReferenceContext(
				runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
				task.Text,
				context,
				true,
			)
			if err == nil || !strings.Contains(err.Error(), "completed-answer repeat") {
				t.Fatalf("a completed business repeat must not escape through interaction/%s: %v", subIntent, err)
			}
		})
	}
}

func TestValidateRuntimeIntentResolvedContextRejectsCompletedStatementAnswerRepeatAsClarify(t *testing.T) {
	for _, test := range []struct {
		name                 string
		previousCustomerText string
		adjacentAIReply      string
	}{
		{
			name:                 "missing slippers",
			previousCustomerText: "拖鞋没了",
			adjacentAIReply:      "可以去1313对面的洗衣房领取。",
		},
		{
			name:                 "broken air conditioner",
			previousCustomerText: "空调坏了",
			adjacentAIReply:      "方便说下是哪个房间吗？",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask("再说一遍", "unknown")
			task.Intent = "interaction"
			task.SubIntent = "clarify"
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			task.ResolvedText = task.Text
			task.Entities = nil
			task.NeedsKnowledge = false
			parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
			context := runtimeIntentProtocolRepairContext{
				PreviousCustomerText: test.previousCustomerText,
				AdjacentAIReply:      test.adjacentAIReply,
			}

			if err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true); err == nil || !strings.Contains(err.Error(), "completed-answer repeat") {
				t.Fatalf("a repeat after a statement-shaped business request must re-enter the business task: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentResolvedContextAllowsPureInteractionRepeatAsClarify(t *testing.T) {
	task := validRuntimeIntentProtocolTask("再说一遍", "unknown")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = task.Text
	task.Entities = nil
	task.NeedsKnowledge = false
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "你好",
		AdjacentAIReply:      "您好，有什么可以帮您？",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true); err != nil {
		t.Fatalf("a repeat after pure interaction must remain interaction/clarify: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsNominalBusinessTargetAsClarify(t *testing.T) {
	tests := []struct {
		text      string
		objective string
	}{
		{text: "早餐时间", objective: "time"},
		{text: "WiFi密码", objective: "method"},
		{text: "停车收费", objective: "price"},
		{text: "发票流程", objective: "method"},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(test.text, test.objective)
			task.Intent = "interaction"
			task.SubIntent = "clarify"
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			task.ResolvedText = task.Text
			task.Entities = nil
			task.NeedsKnowledge = false

			err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text)
			if err == nil || !strings.Contains(err.Error(), "clear business information target") {
				t.Fatalf("a nominal business target must retry through Intent instead of becoming clarify: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsAmbiguousBusinessTargetClarification(t *testing.T) {
	task := validRuntimeIntentProtocolTask("定位发我", "location")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = "定位发我"
	task.Entities = runtimeIntentEntityList{
		{Text: "小丁小吃", Type: "location"},
		{Text: "罍街", Type: "location"},
	}
	task.NeedsKnowledge = false

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text); err != nil {
		t.Fatalf("multiple unresolved business targets must remain a bounded clarification: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsSelfContainedImperativeBusinessClarify(t *testing.T) {
	for _, entities := range []runtimeIntentEntityList{
		{{Text: "早餐", Type: "service"}},
		nil,
	} {
		task := validRuntimeIntentProtocolTask("早餐时间告诉我", "time")
		task.Intent = "interaction"
		task.SubIntent = "clarify"
		task.ResolutionState = runtimeIntentResolutionAmbiguous
		task.Entities = entities
		task.NeedsKnowledge = false

		err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text)
		if err == nil || !strings.Contains(err.Error(), "clear business information target") {
			t.Fatalf("a self-contained information request cannot remain interaction/clarify: entities=%#v err=%v", entities, err)
		}
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsClarifyThatStillRequestsKnowledge(t *testing.T) {
	task := validRuntimeIntentProtocolTask("外卖地址再说一遍", "unknown")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = "外卖地址再说一遍"
	task.Entities = nil
	task.NeedsKnowledge = true

	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text)
	if err == nil || !strings.Contains(err.Error(), "clear business information target") {
		t.Fatalf("interaction/clarify cannot carry an explicit knowledge request, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsResolvedBusinessReferenceDisguisedAsClarify(t *testing.T) {
	task := validRuntimeIntentProtocolTask("外卖地址再说一遍，只要正确地址", "unknown")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.Objective = "unknown"
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "酒店外卖地址怎么填写"
	task.Entities = nil
	task.NeedsKnowledge = false

	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text)
	if err == nil || !strings.Contains(err.Error(), "clear business information target") {
		t.Fatalf("a reference the model says it already resolved cannot remain interaction/clarify: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsClearPersonIdentityDisguisedAsClarify(t *testing.T) {
	task := validRuntimeIntentProtocolTask("老板是谁", "identity")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.ResolutionState = runtimeIntentResolutionClear
	task.ResolvedText = "酒店老板是谁"
	task.Entities = runtimeIntentEntityList{{Text: "老板", Type: "person"}}
	task.NeedsKnowledge = false

	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text)
	if err == nil || !strings.Contains(err.Error(), "clear business information target") {
		t.Fatalf("a clear person identity question must enter the business knowledge path, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsAmbiguousIndependentBusinessQuestionDisguisedAsClarify(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		objective string
		entities  runtimeIntentEntityList
	}{
		{name: "person identity", text: "老板是谁", objective: "identity", entities: runtimeIntentEntityList{{Text: "老板", Type: "person"}}},
		{name: "facility availability", text: "房间有空调吗", objective: "availability", entities: runtimeIntentEntityList{{Text: "空调", Type: "facility"}}},
		{name: "location without entities", text: "酒店外卖地址怎么填", objective: "location"},
		{name: "action request without flags", text: "入住小程序发我", objective: "action_request"},
		{name: "room change action without flags", text: "帮我换个房间", objective: "action_request"},
		{name: "repair action without flags", text: "请派人来修空调", objective: "action_request"},
		{name: "repair homographic noun target without flags", text: "帮我修一下开关", objective: "action_request"},
		{name: "cancel homographic noun target without flags", text: "帮我取消预约", objective: "action_request"},
		{name: "delivery action without flags", text: "安排送拖鞋", objective: "action_request"},
		{name: "book breakfast without flags", text: "预约早餐", objective: "action_request"},
		{name: "reserve room without flags", text: "预订房间", objective: "action_request"},
		{name: "request invoice without flags", text: "申请发票", objective: "action_request"},
		{name: "replenish tissue without flags", text: "补充纸巾", objective: "action_request"},
		{name: "deliver water without flags", text: "配送矿泉水", objective: "action_request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(test.text, test.objective)
			task.Intent = "interaction"
			task.SubIntent = "clarify"
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			task.ResolvedText = test.text
			task.Entities = test.entities
			task.NeedsKnowledge = false

			err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text)
			if err == nil || !strings.Contains(err.Error(), "clear business information target") {
				t.Fatalf("an independent business question must be repaired by Intent instead of bypassing knowledge: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolKeepsIncompleteActionStemAsClarify(t *testing.T) {
	for _, text := range []string{
		"帮我", "我要", "需要", "请发我", "我需要帮助", "帮我说下", "帮我确认下", "帮我获取",
		"我想要一份", "我想要一个", "我想要几个", "我要两瓶", "给我送一个", "预约", "申请",
		"修一下", "来看看", "来修一下", "过来看看", "叫人来修一下",
	} {
		t.Run(text, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(text, "action_request")
			task.Intent = "interaction"
			task.SubIntent = "clarify"
			task.ResolutionState = runtimeIntentResolutionAmbiguous
			task.ResolvedText = text
			task.NeedsKnowledge = false

			if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, text); err != nil {
				t.Fatalf("an incomplete action stem must remain available for clarification: %v", err)
			}
		})
	}
}

func TestRuntimeIntentActionRequestHasTarget(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{text: "帮我", want: false},
		{text: "请发我", want: false},
		{text: "我需要帮助", want: false},
		{text: "帮我确认下", want: false},
		{text: "帮我换个房间", want: true},
		{text: "请派人来修空调", want: true},
		{text: "安排送拖鞋", want: true},
		{text: "请联系前台", want: true},
		{text: "入住小程序发我", want: true},
		{text: "给我发入住小程序", want: true},
		{text: "请开门", want: true},
		{text: "帮我修一下开关", want: true},
		{text: "帮我取消预约", want: true},
		{text: "预约早餐", want: true},
		{text: "预订房间", want: true},
		{text: "申请发票", want: true},
		{text: "补充纸巾", want: true},
		{text: "配送矿泉水", want: true},
		{text: "我想要一份早餐", want: true},
		{text: "我要两个枕头", want: true},
		{text: "我想要一份", want: false},
		{text: "我想要一个", want: false},
		{text: "我想要几个", want: false},
		{text: "我要两瓶", want: false},
		{text: "给我送一个", want: false},
		{text: "租一个", want: false},
		{text: "借一份", want: false},
		{text: "来一个", want: false},
		{text: "点一份", want: false},
		{text: "租一个房间", want: true},
		{text: "借一把雨伞", want: true},
		{text: "来一个枕头", want: true},
		{text: "点一份早餐", want: true},
		{text: "来看看", want: false},
		{text: "修一下", want: false},
		{text: "来修一下", want: false},
		{text: "过来看看", want: false},
		{text: "叫人来修一下", want: false},
		{text: "来看看空调", want: true},
		{text: "来修一下空调", want: true},
		{text: "过来看看空调", want: true},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			if got := runtimeIntentActionRequestHasTarget(test.text); got != test.want {
				t.Fatalf("runtimeIntentActionRequestHasTarget(%q)=%v want %v", test.text, got, test.want)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsClarifyWithExecutableAction(t *testing.T) {
	task := validRuntimeIntentProtocolTask("入住小程序发我", "action_request")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = task.Text
	task.Entities = runtimeIntentEntityList{{Text: "入住小程序", Type: "resource"}}
	task.NeedsKnowledge = false
	task.NeedsResource = true
	task.ResourceAction = "provide_mini_program"

	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, task.Text)
	if err == nil || !strings.Contains(err.Error(), "clear business information target") {
		t.Fatalf("a resource action cannot remain interaction/clarify: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsCompletedAnswerReference(t *testing.T) {
	task := validRuntimeIntentProtocolTask("那麦田呢", "availability")
	task.SubIntent = "room_facilities"
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "麦田房型有没有办公桌"
	task.Entities = runtimeIntentEntityList{
		{Text: "麦田", Type: "room_type"},
		{Text: "办公桌", Type: "facility"},
	}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "那麦田呢"); err != nil {
		t.Fatalf("a model-owned business reference must pass the base protocol: %v", err)
	}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "合柴房型有没有办公桌？",
		AdjacentAIReply:      "合柴房型有办公桌。",
	}
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "那麦田呢", context, true); err != nil {
		t.Fatalf("a comparison after a completed adjacent answer must use bounded business context: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsRepeatAfterNoDataAnswer(t *testing.T) {
	task := validRuntimeIntentProtocolTask("外卖地址再说一遍，只要正确地址", "location")
	task.SubIntent = "delivery_address"
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "酒店外卖地址怎么填写"
	task.Entities = runtimeIntentEntityList{{Text: "外卖地址", Type: "location"}}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "外卖地址再说一遍，只要正确地址"); err != nil {
		t.Fatalf("a model-owned repeat request must pass the base protocol: %v", err)
	}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "外卖地址怎么填？",
		AdjacentAIReply:      "不好意思，当前资料没有写明。",
	}
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "外卖地址再说一遍，只要正确地址", context, true); err != nil {
		t.Fatalf("a repeat after an unhelpful adjacent answer must re-enter the business task: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextRejectsTargetedRepeatAfterMultiQuestionAsInteraction(t *testing.T) {
	task := validRuntimeIntentProtocolTask("外卖地址再说一遍", "unknown")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.Objective = "social"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = task.Text
	task.Entities = nil
	task.NeedsKnowledge = false
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "你们有外卖机器人吗？外卖地址怎么填？布草是一客一换吗？",
		AdjacentAIReply:      "有外卖机器人；外卖地址填写酒店名、楼层和房号；布草是一客一换。",
	}

	err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true)
	if err == nil || !strings.Contains(err.Error(), "completed-answer repeat") {
		t.Fatalf("a repeat anchor that uniquely selects one previous question must re-enter that business task: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextAllowsAmbiguousBareRepeatAfterMultiQuestion(t *testing.T) {
	task := validRuntimeIntentProtocolTask("再说一遍", "unknown")
	task.Intent = "interaction"
	task.SubIntent = "clarify"
	task.Objective = "social"
	task.ResolutionState = runtimeIntentResolutionAmbiguous
	task.ResolvedText = task.Text
	task.Entities = nil
	task.NeedsKnowledge = false
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "早餐几点？停车免费吗？",
		AdjacentAIReply:      "早餐七点开始；停车免费。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true); err != nil {
		t.Fatalf("a bare repeat after several previous questions remains genuinely ambiguous: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextAllowsPureInteractionAfterSlotQuestion(t *testing.T) {
	tests := []struct {
		text      string
		subIntent string
	}{
		{text: "谢谢", subIntent: "thanks"},
		{text: "好的", subIntent: "acknowledgement"},
		{text: "哈哈", subIntent: "chitchat"},
		{text: "晚点再说", subIntent: "chitchat"},
		{text: "不用了", subIntent: "cancellation"},
		{text: "不是，已经解决了", subIntent: "cancellation"},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(test.text, "social")
			task.Intent = "interaction"
			task.SubIntent = test.subIntent
			if test.subIntent == "cancellation" {
				task.RelationToPrevious = "cancel_previous"
			}
			task.ResolvedText = test.text
			task.Entities = nil
			task.NeedsKnowledge = false
			context := runtimeIntentProtocolRepairContext{
				PreviousCustomerText: "马桶堵了",
				AdjacentAIReply:      "方便说下是哪个房间吗？",
			}
			if err := validateRuntimeIntentResolvedReferenceContext(
				runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
				test.text,
				context,
				true,
			); err != nil {
				t.Fatalf("a clear interaction must not be forced into a room-number slot: %v", err)
			}
		})
	}
}

func TestValidateRuntimeIntentResolvedContextDoesNotTreatArbitraryEntityAsSlotAnswer(t *testing.T) {
	task := validRuntimeIntentProtocolTask("北京有什么好玩的", "social")
	task.Intent = "interaction"
	task.SubIntent = "social"
	task.RelationToPrevious = "independent"
	task.ResolutionState = runtimeIntentResolutionClear
	task.NeedsKnowledge = false
	task.Entities = runtimeIntentEntityList{{Text: "北京", Type: "location"}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "空调坏了",
		AdjacentAIReply:      "方便说下是哪个房间吗？",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		task.Text,
		context,
		true,
	); err != nil {
		t.Fatalf("an independent self-contained topic with an unrelated entity must not inherit the old room slot: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextAllowsAIIdentityRepeat(t *testing.T) {
	task := validRuntimeIntentProtocolTask("再说一遍", "identity")
	task.Intent = "interaction"
	task.SubIntent = "ai_identity"
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "你是谁"
	task.NeedsKnowledge = false
	task.Entities = nil
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "你是谁？",
		AdjacentAIReply:      "我是酒店前台同事小七。",
	}

	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, task.Text); err != nil {
		t.Fatalf("ai_identity must use resolvedText when the current text is only a repeat instruction: %v", err)
	}
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true); err != nil {
		t.Fatalf("the completed-answer repeat guard must not turn a previous AI-identity interaction into hotel business: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedContextRequiresCancelPreviousForCancellation(t *testing.T) {
	task := validRuntimeIntentProtocolTask("不用了", "social")
	task.Intent = "interaction"
	task.SubIntent = "cancellation"
	task.ResolvedText = task.Text
	task.Entities = nil
	task.NeedsKnowledge = false
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "马桶堵了",
		AdjacentAIReply:      "方便说下是哪个房间吗？",
	}
	err := validateRuntimeIntentResolvedReferenceContext(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		task.Text,
		context,
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "cancel_previous") {
		t.Fatalf("cancellation of the pending task must use cancel_previous: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAllowsManyToManySourceOwnership(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有早餐吗？早餐在哪里吃？",
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
		"房间有办公桌吗，有沙发吗？",
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

func TestValidateRuntimeIntentResolvedReferenceContextAcceptsSelfContainedNewQuestionWithoutAdjacentPair(t *testing.T) {
	task := validRuntimeIntentProtocolTask("早餐几点", "time")
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "酒店早餐几点开始"

	if err := validateRuntimeIntentResolvedReferenceContext(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		"早餐几点？",
		runtimeIntentProtocolRepairContext{},
		true,
	); err != nil {
		t.Fatalf("a self-contained new question must not fail only because relation metadata asks for unavailable adjacent context: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextRejectsSelfContainedQuestionTopicReplacement(t *testing.T) {
	task := validRuntimeIntentProtocolTask("早餐几点", "time")
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "酒店停车怎么收费"

	err := validateRuntimeIntentResolvedReferenceContext(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		"早餐几点？",
		runtimeIntentProtocolRepairContext{},
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "current self-contained question") {
		t.Fatalf("self-contained context tolerance must not authorize a different resolved topic: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextAcceptsConversationRecapOperation(t *testing.T) {
	task := validRuntimeIntentProtocolTask("刚才聊了什么", "general_guidance")
	task.Intent = "interaction"
	task.SubIntent = "conversation_recap"
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "回顾最近当前会话"

	err := validateRuntimeIntentResolvedReferenceContext(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		"刚才聊了什么？",
		runtimeIntentProtocolRepairContext{},
		true,
	)
	if err != nil {
		t.Fatalf("conversation recap is an operation over bounded history and must not require adjacent lexical grounding: %v", err)
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
