package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/utils"
)

func TestValidateRuntimeIntentDetectProtocolRequiresStructuralFieldsAndEnums(t *testing.T) {
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{}, nil, "早餐几点"); err == nil {
		t.Fatal("empty intentTasks must fail protocol validation")
	}

	tests := []struct {
		name   string
		mutate func(*runtimeIntentTaskJSON)
		want   string
	}{
		{name: "intent", mutate: func(task *runtimeIntentTaskJSON) { task.Intent = "unsupported" }, want: ".intent"},
		{name: "text", mutate: func(task *runtimeIntentTaskJSON) { task.Text = "" }, want: ".text"},
		{name: "resolved_text", mutate: func(task *runtimeIntentTaskJSON) { task.ResolvedText = "" }, want: ".resolvedText"},
		{name: "objective", mutate: func(task *runtimeIntentTaskJSON) { task.Objective = "unsupported" }, want: ".objective"},
		{name: "relation", mutate: func(task *runtimeIntentTaskJSON) { task.RelationToPrevious = "unsupported" }, want: ".relationToPrevious"},
		{name: "resolution", mutate: func(task *runtimeIntentTaskJSON) { task.ResolutionState = "unsupported" }, want: ".resolutionState"},
		{name: "entity_text", mutate: func(task *runtimeIntentTaskJSON) {
			task.Entities = runtimeIntentEntityList{{Type: "service"}}
		}, want: ".entities[0].text"},
		{name: "entity_type", mutate: func(task *runtimeIntentTaskJSON) {
			task.Entities = runtimeIntentEntityList{{Text: "早餐", Type: "unsupported"}}
		}, want: ".entities[0].type"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask("早餐几点", "time")
			test.mutate(&task)
			err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, "早餐几点")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q protocol error, got %v", test.want, err)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolChecksSourceRefsAndTextProvenance(t *testing.T) {
	tests := []struct {
		name string
		text string
		refs runtimeIntentSourceRefList
		want string
	}{
		{name: "missing_ref", text: "早餐几点", want: "sourceRefs is missing"},
		{name: "unknown_ref", text: "早餐几点", refs: runtimeIntentSourceRefList{"U2"}, want: "invalid ref"},
		{name: "rewritten_text", text: "早餐供应时间", refs: runtimeIntentSourceRefList{"U1"}, want: "not traceable"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := validRuntimeIntentProtocolTask(test.text, "time")
			task.SourceRefs = test.refs
			err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, "早餐几点")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q protocol error, got %v", test.want, err)
			}
		})
	}

	task := validRuntimeIntentProtocolTask("早餐几点", "time")
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, "请问早餐几点？"); err != nil {
		t.Fatalf("continuous original text must remain traceable after punctuation normalization: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolPreservesPrimarySourceOrder(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有早餐吗？",
		"2. [消息102] 停车免费吗？",
	})
	parking := validRuntimeIntentProtocolTask("停车免费吗", "price")
	parking.SourceRefs = runtimeIntentSourceRefList{"U2"}
	breakfast := validRuntimeIntentProtocolTask("有早餐吗", "availability")
	breakfast.SourceRefs = runtimeIntentSourceRefList{"U1"}

	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{parking, breakfast}}, nil, current)
	if err == nil || !strings.Contains(err.Error(), "out of current-turn source order") {
		t.Fatalf("reversed primary sources must fail, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolLeavesTaskBoundariesAndSemanticsToModel(t *testing.T) {
	current := "早餐几点？停车免费吗？"
	compound := validRuntimeIntentProtocolTask(current, "compound_information")
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{compound}}, nil, current); err != nil {
		t.Fatalf("local protocol must not infer atomic task coverage: %v", err)
	}

	parking := validRuntimeIntentProtocolTask("停车免费吗", "price")
	breakfast := validRuntimeIntentProtocolTask("早餐几点", "time")
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{parking, breakfast}}, nil, current); err != nil {
		t.Fatalf("task order inside one physical source is model-owned: %v", err)
	}

	interaction := validRuntimeIntentProtocolTask("早餐几点", "social")
	interaction.Intent = "interaction"
	interaction.SubIntent = "chat"
	interaction.NeedsKnowledge = false
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{interaction}}, nil, "早餐几点"); err != nil {
		t.Fatalf("local protocol must not reclassify Chinese business semantics: %v", err)
	}

	burst := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有早餐吗？",
		"2. [消息102] 停车免费吗？",
	})
	parking.SourceRefs = runtimeIntentSourceRefList{"U2"}
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{parking}}, nil, burst); err != nil {
		t.Fatalf("local protocol must not infer a missing task from an unclaimed source: %v", err)
	}
}

func TestRepairRuntimeIntentDetectProtocolOnlyCollapsesExactDuplicates(t *testing.T) {
	first := validRuntimeIntentProtocolTask("早餐几点", "time")
	first.SourceRefs = runtimeIntentSourceRefList{"U1"}
	second := first
	second.SourceRefs = runtimeIntentSourceRefList{"U1", "U2"}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{first, second}}

	repairRuntimeIntentDetectProtocol(&parsed, "不参与修复", runtimeIntentProtocolRepairContext{PreviousCustomerText: "也不参与"}, true)
	if len(parsed.IntentTasks) != 1 {
		t.Fatalf("exact duplicate tasks must collapse, got %#v", parsed.IntentTasks)
	}
	if got := strings.Join([]string(parsed.IntentTasks[0].SourceRefs), ","); got != "U1,U2" {
		t.Fatalf("duplicate source refs must merge stably, got %q", got)
	}

	contextOwned := validRuntimeIntentProtocolTask("我开电车来的你懂我意思吗", "availability")
	contextOwned.RelationToPrevious = "independent"
	contextOwned.ResolutionState = runtimeIntentResolutionResolvedFromContext
	contextOwned.ResolvedText = "酒店停车场有没有电车充电桩"
	contextOwned.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}
	original := contextOwned
	parsed = runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{contextOwned}}
	repairRuntimeIntentDetectProtocol(&parsed, "任意文本", runtimeIntentProtocolRepairContext{}, true)
	got := parsed.IntentTasks[0]
	if got.Intent != original.Intent || got.Objective != original.Objective || got.RelationToPrevious != original.RelationToPrevious ||
		got.ResolutionState != original.ResolutionState || got.Text != original.Text || got.ResolvedText != original.ResolvedText ||
		strings.Join([]string(got.SourceRefs), ",") != strings.Join([]string(original.SourceRefs), ",") {
		t.Fatalf("repair must not rewrite model semantics: before=%#v after=%#v", original, got)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextAcceptsEarlierCurrentTurnSource(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有没有停车场？",
		"2. [消息102] 我开电车来的你懂我意思吗？",
	})
	task := validRuntimeIntentProtocolTask("我开电车来的你懂我意思吗", "availability")
	task.SubIntent = "parking_charging"
	task.RelationToPrevious = "independent"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "酒店停车场有没有电车充电桩"
	task.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}

	if err := validateRuntimeIntentDetectProtocol(parsed, nil, current); err != nil {
		t.Fatalf("same-turn model task must pass base protocol: %v", err)
	}
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, current, runtimeIntentProtocolRepairContext{}, true); err != nil {
		t.Fatalf("an earlier current-turn URef must authorize context resolution: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextRequiresIndependentForSameTurnSource(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 有没有停车场？",
		"2. [消息102] 我开电车来的你懂我意思吗？",
	})
	task := validRuntimeIntentProtocolTask("我开电车来的你懂我意思吗", "availability")
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "酒店停车场有没有电车充电桩"
	task.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}

	err := validateRuntimeIntentResolvedReferenceContext(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		current,
		runtimeIntentProtocolRepairContext{
			PreviousCustomerText: "早餐几点",
			AdjacentServiceReply: "早餐七点开始",
		},
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "same-turn context sourceRefs require relationToPrevious independent") {
		t.Fatalf("same-turn sourceRefs must not be treated as a cross-turn relation, got %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextRejectsNonEarlierCurrentTurnSource(t *testing.T) {
	current := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [消息101] 早餐几点？",
		"2. [消息102] 停车免费吗？",
	})
	tests := []runtimeIntentSourceRefList{{"U1", "U2"}, {"U2", "U2"}}
	for _, refs := range tests {
		task := validRuntimeIntentProtocolTask("早餐几点", "time")
		if refs[0] == "U2" {
			task.Text = "停车免费吗"
			task.ResolvedText = "停车免费吗"
		}
		task.ResolutionState = runtimeIntentResolutionResolvedFromContext
		task.SourceRefs = refs
		err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, current, runtimeIntentProtocolRepairContext{}, true)
		if err == nil || !strings.Contains(err.Error(), "earlier current-turn source") {
			t.Fatalf("non-earlier context ref %v must fail mechanically, got %v", refs, err)
		}
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextUsesAdjacentServicePair(t *testing.T) {
	task := validRuntimeIntentProtocolTask("那麦田呢", "availability")
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "月球上的麦田房型有没有办公桌"
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "合柴房型有没有办公桌",
		AdjacentServiceReply: "合柴房型有办公桌",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true); err != nil {
		t.Fatalf("valid previous relation and adjacent service pair must pass without lexical judgment: %v", err)
	}

	context.PreviousCustomerText = ""
	err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true)
	if err == nil || !strings.Contains(err.Error(), "requires bounded conversation history") {
		t.Fatalf("missing all usable context must fail mechanically, got %v", err)
	}
	context.HasBoundedHistory = true
	context.AdjacentServiceReply = ""
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true); err != nil {
		t.Fatalf("an explicit reference may use bounded history without an adjacent reply pair: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextAllowsConversationRecapWithBoundedHistory(t *testing.T) {
	task := validRuntimeIntentProtocolTask("刚才聊了什么", "summary")
	task.Intent = "interaction"
	task.SubIntent = "conversation_recap"
	task.RelationToPrevious = "reference_previous"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.ResolvedText = "回顾最近当前会话"
	task.NeedsKnowledge = false
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}

	if err := validateRuntimeIntentResolvedReferenceContext(
		parsed,
		task.Text,
		runtimeIntentProtocolRepairContext{HasBoundedHistory: true},
		true,
	); err != nil {
		t.Fatalf("conversation recap needs bounded history, not an adjacent customer/service pair: %v", err)
	}

	err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, runtimeIntentProtocolRepairContext{}, true)
	if err == nil || !strings.Contains(err.Error(), "conversation_recap requires bounded conversation history") {
		t.Fatalf("conversation recap without bounded history must fail mechanically, got %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextRequiresDeclaredContext(t *testing.T) {
	task := validRuntimeIntentProtocolTask("早餐几点", "time")
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	err := validateRuntimeIntentResolvedReferenceContext(
		runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}},
		task.Text,
		runtimeIntentProtocolRepairContext{},
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "earlier current-turn source or previous-turn relation") {
		t.Fatalf("resolved_from_context without a declared context pointer must fail, got %v", err)
	}
}

func TestValidateRuntimeIntentAnswerRejectedUsesOnlyMechanicalAdjacency(t *testing.T) {
	task := validRuntimeIntentProtocolTask("好的", "complaint")
	task.Intent = "human_complaint_risk"
	task.SubIntent = "answer_rejected"
	task.RelationToPrevious = "answer_rejected"
	task.ResolutionState = runtimeIntentResolutionResolvedFromContext
	task.NeedsHumanRoute = true
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "早餐几点",
		AdjacentAIReply:      "早餐七点开始",
	}
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, context, true); err != nil {
		t.Fatalf("answer_rejected must not depend on a Chinese rejection keyword list: %v", err)
	}

	mismatch := task
	mismatch.RelationToPrevious = "follow_up"
	err := validateRuntimeIntentResolvedReferenceContext(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{mismatch}}, task.Text, context, true)
	if err == nil || !strings.Contains(err.Error(), "classification and relation must agree") {
		t.Fatalf("answer_rejected intent and relation mismatch must fail, got %v", err)
	}

	humanOnly := context
	humanOnly.AdjacentAIReply = ""
	humanOnly.AdjacentServiceReply = "人工客服回复"
	err = validateRuntimeIntentResolvedReferenceContext(parsed, task.Text, humanOnly, true)
	if err == nil || !strings.Contains(err.Error(), "immediately previous AI reply") {
		t.Fatalf("answer_rejected without adjacent AI must fail, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolKeepsLegacyProfileCompatible(t *testing.T) {
	profile := &models.ReplyIntentProfile{IntentJSONSchema: `{"intentTasks":[{"intent":"","text":""}]}`}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{Intent: "hotel_info", Text: "早餐几点"}}}
	if err := validateRuntimeIntentDetectProtocol(parsed, profile, "早餐几点"); err != nil {
		t.Fatalf("legacy profile must not require V2 semantic fields: %v", err)
	}
}

func validRuntimeIntentProtocolTask(text string, objective string) runtimeIntentTaskJSON {
	return runtimeIntentTaskJSON{
		Intent:             "hotel_info",
		SubIntent:          "store_knowledge",
		Objective:          objective,
		RelationToPrevious: "independent",
		ResolutionState:    runtimeIntentResolutionClear,
		Text:               text,
		ResolvedText:       text,
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsKnowledge:     true,
	}
}
