package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestValidateRuntimeIntentDetectProtocolRejectsMissingTaskSemantics(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:       "hotel_info",
		Text:         "早餐几点",
		ResolvedText: "早餐几点",
		SourceRefs:   runtimeIntentSourceRefList{"U1"},
	}}}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点")
	if err == nil || !strings.Contains(err.Error(), "objective") {
		t.Fatalf("expected missing semantic field error, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRequiresSelfContainedResolvedReference(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:             "hotel_info",
		SubIntent:          "room_facilities",
		Objective:          "availability",
		RelationToPrevious: "reference_previous",
		ResolutionState:    "resolved_from_context",
		Entities:           runtimeIntentEntityList{{Text: "麦田", Type: "room_type"}},
		Text:               "那麦田呢？",
		ResolvedText:       "那麦田呢？",
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsKnowledge:     true,
	}}}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "那麦田呢？")
	if err == nil || !strings.Contains(err.Error(), "self-contained") {
		t.Fatalf("expected unresolved dependent reference to fail protocol validation, got %v", err)
	}

	parsed.IntentTasks[0].ResolvedText = "麦田房型有没有办公桌？"
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "那麦田呢？"); err != nil {
		t.Fatalf("self-contained reference must pass protocol validation: %v", err)
	}

	parsed.IntentTasks[0].Text = "麦田房型有没有办公桌？"
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "那麦田呢？"); err != nil {
		t.Fatalf("model-completed text must not invalidate a self-contained resolved reference: %v", err)
	}
	if parsed.IntentTasks[0].Text != "那麦田呢" {
		t.Fatalf("protocol validation must restore customer text while preserving resolvedText, got %#v", parsed.IntentTasks[0])
	}

	parsed.IntentTasks[0].ResolutionState = "clear"
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "那麦田呢？"); err == nil || !strings.Contains(err.Error(), "must be resolved_from_context") {
		t.Fatalf("genuinely context-dependent text must still require resolved_from_context, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolClearsFalseContextResolutionForSelfContainedQuestion(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:             "hotel_info",
		SubIntent:          "delivery_address",
		Objective:          "method",
		RelationToPrevious: "reference_previous",
		ResolutionState:    "resolved_from_context",
		Text:               "外卖地址怎么填？",
		ResolvedText:       "麦田房型的外卖地址怎么填",
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsKnowledge:     true,
	}}}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "外卖地址怎么填？"); err != nil {
		t.Fatalf("self-contained current question must be repaired instead of rejecting the whole intent: %v", err)
	}
	task := parsed.IntentTasks[0]
	if task.Text != "外卖地址怎么填" || task.ResolvedText != "外卖地址怎么填" || task.ResolutionState != runtimeIntentResolutionClear {
		t.Fatalf("false context resolution must be cleared without old-topic leakage, got %#v", task)
	}
}

func TestRepairRuntimeIntentDetectProtocolRepairsAdjacentRepeatReference(t *testing.T) {
	currentText := "再说一遍，只要正确地址。"
	adjacentAIReply := models.Message{
		SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText,
		Content: "外卖地址填写酒店名加楼层房间号。",
	}
	history := adapter.HistoryBuildResult{
		RawItems: []models.Message{
			{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "外卖地址怎么填？"},
			adjacentAIReply,
		},
		LatestRawItem: &adjacentAIReply,
	}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:             "hotel_info",
		SubIntent:          "delivery_address",
		Objective:          "method",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
		Entities:           runtimeIntentEntityList{{Text: "外卖地址", Type: "location"}},
		Text:               "再说一遍，只要正确地址",
		ResolvedText:       "再说一遍，只要正确地址",
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsKnowledge:     true,
	}}}

	repairRuntimeIntentDetectProtocol(&parsed, currentText, buildRuntimeIntentProtocolRepairContext(history), true)

	task := parsed.IntentTasks[0]
	if task.Text != "再说一遍，只要正确地址" || task.ResolvedText != "外卖地址怎么填" ||
		task.RelationToPrevious != "reference_previous" || task.ResolutionState != runtimeIntentResolutionResolvedFromContext {
		t.Fatalf("bounded repeat repair produced unexpected task: %#v", task)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err != nil {
		t.Fatalf("repaired repeat reference must pass protocol validation: %v", err)
	}
}

func TestRepairRuntimeIntentDetectProtocolDoesNotInventRepeatContext(t *testing.T) {
	currentText := "再说一遍，只要正确地址。"
	base := runtimeIntentTaskJSON{
		Intent:             "hotel_info",
		SubIntent:          "delivery_address",
		Objective:          "method",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
		Entities:           runtimeIntentEntityList{{Text: "外卖地址", Type: "location"}},
		Text:               "再说一遍，只要正确地址",
		ResolvedText:       "再说一遍，只要正确地址",
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsKnowledge:     true,
	}
	for _, tt := range []struct {
		name    string
		context runtimeIntentProtocolRepairContext
	}{
		{name: "no adjacent AI", context: runtimeIntentProtocolRepairContext{PreviousCustomerText: "外卖地址怎么填？"}},
		{name: "no previous customer question", context: runtimeIntentProtocolRepairContext{AdjacentAIReply: "酒店名加房间号。"}},
		{name: "dependent previous customer text", context: runtimeIntentProtocolRepairContext{AdjacentAIReply: "好的。", PreviousCustomerText: "那地址呢？"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{base}}
			repairRuntimeIntentDetectProtocol(&parsed, currentText, tt.context, true)
			if parsed.IntentTasks[0].ResolutionState != "clear" || parsed.IntentTasks[0].ResolvedText != base.ResolvedText {
				t.Fatalf("unsafe repeat context must remain untouched: %#v", parsed.IntentTasks[0])
			}
			if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err == nil || !strings.Contains(err.Error(), "must be resolved_from_context") {
				t.Fatalf("unrepaired repeat reference must retain the protocol error, got %v", err)
			}
		})
	}
}

func TestRepairRuntimeIntentDetectProtocolRejectsConflictingExplicitRepeatSubject(t *testing.T) {
	currentText := "地址再说一遍。"
	adjacentAIReply := models.Message{
		SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText,
		Content: "早餐时间是早上7点到10点。",
	}
	history := adapter.HistoryBuildResult{
		RawItems: []models.Message{
			{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "早餐几点？"},
			adjacentAIReply,
		},
		LatestRawItem: &adjacentAIReply,
	}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:             "hotel_info",
		SubIntent:          "delivery_address",
		Objective:          "location",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
		Entities:           runtimeIntentEntityList{{Text: "地址", Type: "location"}},
		Text:               "地址再说一遍",
		ResolvedText:       "这个再说一遍",
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsKnowledge:     true,
	}}}

	repairRuntimeIntentDetectProtocol(&parsed, currentText, buildRuntimeIntentProtocolRepairContext(history), true)

	if task := parsed.IntentTasks[0]; task.ResolvedText != "这个再说一遍" || task.ResolutionState != "clear" {
		t.Fatalf("conflicting explicit repeat subject must not inherit the previous topic: %#v", task)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err == nil || !strings.Contains(err.Error(), "must be resolved_from_context") {
		t.Fatalf("conflicting repeat subject must retain a protocol error for retry, got %v", err)
	}
}

func TestRepairRuntimeIntentDetectProtocolRejectsSameObjectiveDifferentRepeatSubject(t *testing.T) {
	currentText := "停车怎么走再说一遍。"
	adjacentAIReply := models.Message{
		SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText,
		Content: "可以通过入住机或小程序办理入住。",
	}
	history := adapter.HistoryBuildResult{
		RawItems: []models.Message{
			{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "怎么办入住？"},
			adjacentAIReply,
		},
		LatestRawItem: &adjacentAIReply,
	}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:             "hotel_info",
		SubIntent:          "parking_route",
		Objective:          "method",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
		Entities:           runtimeIntentEntityList{{Text: "停车", Type: "facility"}},
		Text:               "停车怎么走再说一遍",
		ResolvedText:       "这个怎么走",
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsKnowledge:     true,
	}}}

	repairRuntimeIntentDetectProtocol(&parsed, currentText, buildRuntimeIntentProtocolRepairContext(history), true)

	if task := parsed.IntentTasks[0]; task.ResolvedText != "这个怎么走" || task.ResolutionState != "clear" {
		t.Fatalf("same objective must not override an explicit subject mismatch: %#v", task)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err == nil || !strings.Contains(err.Error(), "must be resolved_from_context") {
		t.Fatalf("same-objective subject mismatch must retain a protocol error for retry, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRequiresDistinctTasksForExplicitCandidates(t *testing.T) {
	currentText := "入住方式和开门方式分别说，不要混在一起。"
	compound := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:             "hotel_info",
		SubIntent:          "checkin_process",
		Objective:          "method",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
		Text:               currentText,
		ResolvedText:       currentText,
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsKnowledge:     true,
	}}}
	if err := validateRuntimeIntentDetectProtocol(compound, nil, currentText); err == nil || !strings.Contains(err.Error(), "atomic question") {
		t.Fatalf("one compound task must not cover two explicit candidates, got %v", err)
	}

	distinct := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		{
			Intent: "hotel_info", SubIntent: "checkin_process", Objective: "method",
			RelationToPrevious: "independent", ResolutionState: "clear",
			Text: "入住方式", ResolvedText: "酒店怎么办理入住", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
		},
		{
			Intent: "hotel_info", SubIntent: "room_access", Objective: "method",
			RelationToPrevious: "independent", ResolutionState: "clear",
			Text: "开门方式", ResolvedText: "酒店房门怎么打开", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
		},
	}}
	if err := validateRuntimeIntentDetectProtocol(distinct, nil, currentText); err != nil {
		t.Fatalf("distinct tasks must satisfy explicit candidate coverage: %v", err)
	}

	reusedCompound := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		{
			Intent: "hotel_info", SubIntent: "checkin_process", Objective: "method",
			RelationToPrevious: "independent", ResolutionState: "clear",
			Text: currentText, ResolvedText: currentText, SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
		},
		{
			Intent: "hotel_info", SubIntent: "room_access", Objective: "method",
			RelationToPrevious: "independent", ResolutionState: "clear",
			Text: currentText, ResolvedText: currentText, SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
		},
	}}
	if err := validateRuntimeIntentDetectProtocol(reusedCompound, nil, currentText); err == nil {
		t.Fatalf("reused compound tasks must be rejected, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolUsesExactTextOwnershipForOverlappingQuestions(t *testing.T) {
	currentText := "早餐几点？早餐几点结束？"
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("早餐几点", "time"),
		validRuntimeIntentProtocolTask("早餐几点结束", "time"),
	}}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err != nil {
		t.Fatalf("overlapping but distinct original questions must pass one-to-one ownership: %v", err)
	}
}

func TestRepairRuntimeIntentDetectProtocolRestoresUniqueDuplicateGap(t *testing.T) {
	currentText := "我一次问五个：WiFi账号密码是什么、怎么办入住、房门怎么开、发票怎么开、停车收费吗？"
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("WiFi账号密码是什么", "general_guidance"),
		validRuntimeIntentProtocolTask("怎么办入住", "method"),
		{
			Intent: "hotel_info", SubIntent: "room_access", Objective: "method",
			RelationToPrevious: "independent", ResolutionState: "clear",
			Entities: runtimeIntentEntityList{{Text: "房门", Type: "facility"}},
			Text:     "怎么办入住", ResolvedText: "酒店房门怎么开", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
		},
		validRuntimeIntentProtocolTask("发票怎么开", "method"),
		validRuntimeIntentProtocolTask("停车收费吗", "price"),
	}}

	repairRuntimeIntentDetectProtocol(&parsed, currentText, runtimeIntentProtocolRepairContext{}, true)

	if len(parsed.IntentTasks) != 5 || parsed.IntentTasks[2].Text != "房门怎么开" || parsed.IntentTasks[2].ResolvedText != "酒店房门怎么开" {
		t.Fatalf("unique duplicate gap must restore only task text: %#v", parsed.IntentTasks)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err != nil {
		t.Fatalf("repaired five-question ownership must pass validation: %v", err)
	}
}

func TestRepairRuntimeIntentDetectProtocolUsesUniqueTaskSemanticsForDependentResolvedText(t *testing.T) {
	currentText := "早餐几点？房门怎么开？发票怎么开？"
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("早餐几点", "time"),
		{
			Intent: "hotel_info", SubIntent: "room_access", Objective: "method",
			RelationToPrevious: "independent", ResolutionState: "clear",
			Entities: runtimeIntentEntityList{{Text: "房门", Type: "facility"}},
			Text:     "早餐几点", ResolvedText: "这个怎么开", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
		},
		validRuntimeIntentProtocolTask("发票怎么开", "method"),
	}}

	repairRuntimeIntentDetectProtocol(&parsed, currentText, runtimeIntentProtocolRepairContext{}, true)

	task := parsed.IntentTasks[1]
	if task.Text != "房门怎么开" || task.ResolvedText != "房门怎么开" {
		t.Fatalf("unique entity and objective must restore a dependent resolvedText safely: %#v", task)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err != nil {
		t.Fatalf("semantically repaired ownership must pass validation: %v", err)
	}
}

func TestRepairRuntimeIntentDetectProtocolKeepsAmbiguousDuplicateStrict(t *testing.T) {
	currentText := "早餐几点？停车收费吗？发票怎么开？"
	for _, tt := range []struct {
		name  string
		tasks runtimeIntentTaskList
	}{
		{
			name: "duplicated task semantics also point to duplicate",
			tasks: runtimeIntentTaskList{
				validRuntimeIntentProtocolTask("早餐几点", "time"),
				validRuntimeIntentProtocolTask("早餐几点", "time"),
				validRuntimeIntentProtocolTask("发票怎么开", "method"),
			},
		},
		{
			name: "replacement would break task order",
			tasks: runtimeIntentTaskList{
				validRuntimeIntentProtocolTask("早餐几点", "time"),
				validRuntimeIntentProtocolTask("发票怎么开", "method"),
				{
					Intent: "hotel_info", SubIntent: "parking", Objective: "price",
					RelationToPrevious: "independent", ResolutionState: "clear",
					Entities: runtimeIntentEntityList{{Text: "停车", Type: "service"}},
					Text:     "发票怎么开", ResolvedText: "停车收费吗", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parsed := runtimeIntentDetectJSON{IntentTasks: append(runtimeIntentTaskList(nil), tt.tasks...)}
			repairRuntimeIntentDetectProtocol(&parsed, currentText, runtimeIntentProtocolRepairContext{}, true)
			if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err == nil {
				t.Fatalf("ambiguous duplicate ownership must remain a protocol error: %#v", parsed.IntentTasks)
			}
		})
	}
}

func TestRepairRuntimeIntentDetectProtocolNormalizesRoomFeatureIntersectionObjective(t *testing.T) {
	currentText := "哪些房型既有沙发又有办公桌？"
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "room_type_features", Objective: "recommendation",
		RelationToPrevious: "independent", ResolutionState: "clear",
		Entities: runtimeIntentEntityList{
			{Text: "房型", Type: "room_type"},
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
		Text: "哪些房型既有沙发又有办公桌", ResolvedText: "哪些房型既有沙发又有办公桌",
		SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}

	repairRuntimeIntentDetectProtocol(&parsed, currentText, runtimeIntentProtocolRepairContext{}, true)

	if got := parsed.IntentTasks[0].Objective; got != "compound_information" {
		t.Fatalf("room-feature intersection must use compound_information, got %q", got)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err != nil {
		t.Fatalf("normalized room-feature intersection must pass validation: %v", err)
	}
}

func TestRepairRuntimeIntentDetectProtocolPreservesRealRecommendationObjective(t *testing.T) {
	currentText := "推荐一个既有沙发又有办公桌的房型。"
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "room_type_features", Objective: "recommendation",
		RelationToPrevious: "independent", ResolutionState: "clear",
		Entities: runtimeIntentEntityList{
			{Text: "房型", Type: "room_type"},
			{Text: "沙发", Type: "facility"},
			{Text: "办公桌", Type: "facility"},
		},
		Text: currentText, ResolvedText: currentText, SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}

	repairRuntimeIntentDetectProtocol(&parsed, currentText, runtimeIntentProtocolRepairContext{}, true)

	if got := parsed.IntentTasks[0].Objective; got != "recommendation" {
		t.Fatalf("real recommendation must keep recommendation objective, got %q", got)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsExtraAndDuplicateBusinessTasks(t *testing.T) {
	currentText := "早餐几点？停车免费吗？"
	for _, tt := range []struct {
		name      string
		tasks     runtimeIntentTaskList
		wantError string
	}{
		{
			name: "extra task",
			tasks: runtimeIntentTaskList{
				validRuntimeIntentProtocolTask("早餐几点", "time"),
				validRuntimeIntentProtocolTask("停车免费吗", "price"),
				validRuntimeIntentProtocolTask("发票怎么开", "method"),
			},
			wantError: "extra executable business task",
		},
		{
			name: "duplicate task",
			tasks: runtimeIntentTaskList{
				validRuntimeIntentProtocolTask("早餐几点", "time"),
				validRuntimeIntentProtocolTask("早餐几点", "time"),
			},
			wantError: "duplicates atomic question",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: tt.tasks}, nil, currentText)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected %q error, got %v", tt.wantError, err)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsInvalidContextResolutionClaims(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		resolvedText string
		wantError    string
	}{
		{name: "confirmation remains dependent", text: "对吗", resolvedText: "对吗", wantError: "self-contained"},
		{name: "demonstrative remains dependent", text: "那呢", resolvedText: "那呢", wantError: "self-contained"},
		{name: "recent reference remains dependent", text: "刚才那个再说一遍", resolvedText: "刚才那个", wantError: "self-contained"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
				Intent: "hotel_info", SubIntent: "hotel_information", Objective: "general_guidance",
				RelationToPrevious: "reference_previous", ResolutionState: "resolved_from_context",
				Text: tt.text, ResolvedText: tt.resolvedText, SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
			}}}
			if err := validateRuntimeIntentDetectProtocol(parsed, nil, tt.text); err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected %q validation error, got %v", tt.wantError, err)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsCountedQuestionLead(t *testing.T) {
	currentText := "我一次问五个：WiFi账号密码是什么、停车收费吗、早餐几点、外卖地址怎么填、发票怎么开？"
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("WiFi账号密码是什么", "general_guidance"),
		validRuntimeIntentProtocolTask("停车收费吗", "price"),
		validRuntimeIntentProtocolTask("早餐几点", "time"),
		validRuntimeIntentProtocolTask("外卖地址怎么填", "method"),
		validRuntimeIntentProtocolTask("发票怎么开", "method"),
	}}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err != nil {
		t.Fatalf("counted question lead must not pollute first-task ownership: %v", err)
	}
	if parsed.IntentTasks[0].Text != "WiFi账号密码是什么" {
		t.Fatalf("first task text must contain only the first atomic question, got %#v", parsed.IntentTasks[0])
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsInteractionForBusinessInformationTarget(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "interaction", SubIntent: "clarify", Objective: "method",
		RelationToPrevious: "independent", ResolutionState: "clear",
		Entities: runtimeIntentEntityList{{Text: "发票", Type: "service"}},
		Text:     "发票流程", ResolvedText: "酒店发票怎么申请", SourceRefs: runtimeIntentSourceRefList{"U1"},
	}}}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "发票流程"); err == nil || !strings.Contains(err.Error(), "business information target") {
		t.Fatalf("business information target must not be interaction/clarify, got %v", err)
	}
}

func TestImmediatelyPreviousAIReplySkipsServiceNotice(t *testing.T) {
	reply := models.Message{
		SenderType:  enums.IMSenderTypeAI,
		MessageType: enums.IMMessageTypeText,
		Content:     "合柴和艺林有办公桌。",
		ClientMsgID: "ai_reply_100",
	}
	notice := models.Message{
		SenderType:  enums.IMSenderTypeAI,
		MessageType: enums.IMMessageTypeText,
		Content:     "帮您转接同事啦～",
		ClientMsgID: "ai_handoff_success_direct_10_100",
	}
	history := adapter.HistoryBuildResult{
		RawItems:      []models.Message{reply},
		LatestRawItem: &notice,
	}
	content, ok := immediatelyPreviousAIReply(history)
	if !ok || !strings.Contains(content, reply.Content) {
		t.Fatalf("expected substantive AI reply behind service notice, got %q ok=%v", content, ok)
	}
}

func TestImmediatelyPreviousAIReplyDoesNotCrossNonAIMessageBehindServiceNotice(t *testing.T) {
	notice := models.Message{
		SenderType:  enums.IMSenderTypeAI,
		MessageType: enums.IMMessageTypeText,
		Content:     "帮您转接同事啦～",
		ClientMsgID: "ai_handoff_success_direct_10_100",
	}
	for _, senderType := range []enums.IMSenderType{enums.IMSenderTypeCustomer, enums.IMSenderTypeAgent} {
		history := adapter.HistoryBuildResult{
			RawItems: []models.Message{
				{SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "更早的业务答复"},
				{SenderType: senderType, MessageType: enums.IMMessageTypeText, Content: "紧邻通知前的消息"},
			},
			LatestRawItem: &notice,
		}
		if content, ok := immediatelyPreviousAIReply(history); ok || content != "" {
			t.Fatalf("must not cross sender %q to reuse an older AI reply, got %q ok=%v", senderType, content, ok)
		}
	}
}

func TestParseRuntimeIntentDetectJSONDefaultsMissingResourceTaskObjective(t *testing.T) {
	parsed, err := parseRuntimeIntentDetectJSON(`{
		"intentTasks":[{
			"intent":"hotel_variable",
			"subIntent":"mini_program",
			"objective":"",
			"relationToPrevious":"independent",
			"resolutionState":"clear",
			"entities":[],
			"text":"入住小程序发我",
			"resolvedText":"发送入住小程序",
			"sourceRefs":["U1"],
			"needsResource":true,
			"resourceAction":"provide_mini_program"
		}]
	}`)
	if err != nil {
		t.Fatalf("parse resource task: %v", err)
	}
	if got := parsed.IntentTasks[0].Objective; got != "action_request" {
		t.Fatalf("expected resource task objective default, got %q", got)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "入住小程序发我"); err != nil {
		t.Fatalf("defaulted resource task must pass strict protocol validation: %v", err)
	}
}

func TestParseRuntimeIntentDetectJSONRepairsExplicitResourceTaskInsideMultipleTasks(t *testing.T) {
	parsed, err := parseRuntimeIntentDetectJSON(`{
		"primaryIntent":"hotel_info",
		"intentTasks":[
			{
				"intent":"hotel_info",
				"subIntent":"checkin_process",
				"objective":"method",
				"relationToPrevious":"independent",
				"resolutionState":"clear",
				"entities":[],
				"text":"怎么办理入住",
				"resolvedText":"怎么办理入住",
				"sourceRefs":["U1"],
				"needsKnowledge":true
			},
			{
				"intent":"interaction",
				"subIntent":"mini_program",
				"objective":"",
				"relationToPrevious":"independent",
				"resolutionState":"clear",
				"entities":[{"text":"小程序","type":"resource"}],
				"text":"小程序也发我一下",
				"resolvedText":"发送入住小程序",
				"sourceRefs":["U1"],
				"needsResource":true,
				"resourceAction":"send_miniprogram"
			}
		]
	}`)
	if err != nil {
		t.Fatalf("parse multi-task intent: %v", err)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "怎么办理入住？小程序也发我一下。"); err != nil {
		t.Fatalf("repaired multi-task intent must pass strict protocol validation: %v", err)
	}
	if len(parsed.IntentTasks) != 2 {
		t.Fatalf("expected two intent tasks, got %#v", parsed.IntentTasks)
	}
	resourceTask := parsed.IntentTasks[1]
	if resourceTask.Intent != "hotel_variable" || resourceTask.Objective != "action_request" || !resourceTask.NeedsResource || resourceTask.ResourceAction != "provide_mini_program" {
		t.Fatalf("expected explicit resource task to be normalized, got %#v", resourceTask)
	}

	converted := convertRuntimeIntentTasks([]runtimeIntentTaskJSON(parsed.IntentTasks))
	if len(converted) != 2 || converted[1].Intent != "hotel_variable" || !converted[1].NeedsResource || converted[1].ResourceAction != "provide_mini_program" {
		t.Fatalf("normalized resource task must survive conversion, got %#v", converted)
	}
}

func TestParseRuntimeIntentDetectJSONRepairsBoundedResourceObjectiveAlias(t *testing.T) {
	parsed, err := parseRuntimeIntentDetectJSON(`{
		"intentTasks":[{
			"intent":"hotel_variable",
			"subIntent":"mini_program",
			"objective":"resource",
			"relationToPrevious":"independent",
			"resolutionState":"clear",
			"entities":[{"text":"小程序","type":"resource"}],
			"text":"小程序也发我一下",
			"resolvedText":"发送入住小程序",
			"sourceRefs":["U1"]
		}]
	}`)
	if err != nil {
		t.Fatalf("parse bounded resource alias: %v", err)
	}
	resourceTask := parsed.IntentTasks[0]
	if resourceTask.Objective != "action_request" || resourceTask.ResourceAction != "provide_mini_program" || !resourceTask.NeedsResource {
		t.Fatalf("expected bounded resource alias to be normalized, got %#v", resourceTask)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "小程序也发我一下"); err != nil {
		t.Fatalf("normalized bounded resource alias must pass strict validation: %v", err)
	}
}

func TestApplyRuntimeIntentProtocolDefaultsUsesOnlyExplicitResourceSignals(t *testing.T) {
	tests := []struct {
		name          string
		task          runtimeIntentTaskJSON
		wantObjective string
	}{
		{
			name: "recognized action without needs resource",
			task: runtimeIntentTaskJSON{
				Intent:         "hotel_variable",
				ResourceAction: "provide_location",
			},
			wantObjective: "action_request",
		},
		{
			name: "non resource intent",
			task: runtimeIntentTaskJSON{
				Intent:        "hotel_info",
				NeedsResource: true,
			},
		},
		{
			name: "handoff intent is not rewritten by resource action",
			task: runtimeIntentTaskJSON{
				Intent:          "human_complaint_risk",
				NeedsHumanRoute: true,
				ResourceAction:  "provide_mini_program",
			},
		},
		{
			name: "hotel variable without resource signal",
			task: runtimeIntentTaskJSON{
				Intent: "hotel_variable",
			},
		},
		{
			name: "unknown action without needs resource",
			task: runtimeIntentTaskJSON{
				Intent:         "hotel_variable",
				SubIntent:      "mini_program",
				ResourceAction: "provide_unknown",
			},
		},
		{
			name: "invalid nonempty objective is not overwritten",
			task: runtimeIntentTaskJSON{
				Intent:        "hotel_variable",
				Objective:     "not_valid",
				NeedsResource: true,
			},
			wantObjective: "not_valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{tt.task}}
			applyRuntimeIntentProtocolDefaults(&parsed)
			if got := parsed.IntentTasks[0].Objective; got != tt.wantObjective {
				t.Fatalf("expected objective %q, got %q", tt.wantObjective, got)
			}
		})
	}
}

func TestValidateRuntimeIntentDetectProtocolKeepsOtherFieldsStrictAfterResourceDefault(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:             "hotel_variable",
		RelationToPrevious: "independent",
		ResolutionState:    "",
		Text:               "定位发我",
		ResolvedText:       "发送酒店定位",
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsResource:      true,
		ResourceAction:     "provide_location",
	}}}
	applyRuntimeIntentProtocolDefaults(&parsed)
	if parsed.IntentTasks[0].Objective != "action_request" {
		t.Fatalf("expected resource objective default, got %q", parsed.IntentTasks[0].Objective)
	}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "定位发我")
	if err == nil || !strings.Contains(err.Error(), "resolutionState") {
		t.Fatalf("expected remaining protocol error, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsMissingSourceRefs(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:             "hotel_info",
		Objective:          "time",
		RelationToPrevious: "independent",
		ResolutionState:    "clear",
		Text:               "早餐几点",
		ResolvedText:       "早餐几点",
	}}}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点")
	if err == nil || !strings.Contains(err.Error(), "sourceRefs") {
		t.Fatalf("expected missing sourceRefs error, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsUnknownSourceRef(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("早餐几点", "time"),
	}}
	parsed.IntentTasks[0].SourceRefs = runtimeIntentSourceRefList{"U2"}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点")
	if err == nil || !strings.Contains(err.Error(), "invalid ref") {
		t.Fatalf("expected invalid source ref error, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolRejectsMissingAtomicQuestion(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("你们有没有外卖机器人", "availability"),
		validRuntimeIntentProtocolTask("外卖地址怎么填", "method"),
	}}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "你们有没有外卖机器人？外卖地址怎么填？布草是不是一客一换？")
	if err == nil || !strings.Contains(err.Error(), "atomic question 3 of 3") {
		t.Fatalf("expected atomic coverage error, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsCompleteAtomicQuestions(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("你们有没有外卖机器人", "availability"),
		validRuntimeIntentProtocolTask("外卖地址怎么填", "method"),
		validRuntimeIntentProtocolTask("布草是不是一客一换", "policy"),
	}}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "你们有没有外卖机器人？外卖地址怎么填？布草是不是一客一换？"); err != nil {
		t.Fatalf("expected complete protocol to pass, got %v", err)
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
		ResolutionState:    "clear",
		Text:               text,
		ResolvedText:       text,
		SourceRefs:         runtimeIntentSourceRefList{"U1"},
		NeedsKnowledge:     true,
	}
}
