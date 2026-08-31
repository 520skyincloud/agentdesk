package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

func TestValidateRuntimeIntentDetectProtocolTreatsObjectiveAsOptionalMetadata(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent:       "hotel_info",
		Text:         "早餐几点",
		ResolvedText: "早餐几点",
		SourceRefs:   runtimeIntentSourceRefList{"U1"},
	}}}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点")
	if err == nil || !strings.Contains(err.Error(), "relationToPrevious") {
		t.Fatalf("relation metadata is still required to select bounded context: %v", err)
	}
	parsed.IntentTasks[0].RelationToPrevious = "independent"
	parsed.IntentTasks[0].ResolutionState = "clear"
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点"); err != nil {
		t.Fatalf("missing objective metadata must not invalidate a grounded task: %v", err)
	}
	parsed.IntentTasks[0].Objective = "time"
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点"); err != nil {
		t.Fatalf("complete valid V2 semantics must pass: %v", err)
	}
	parsed.IntentTasks[0].Objective = "not_a_real_objective"
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点"); err != nil {
		t.Fatalf("an unknown objective enum must degrade locally instead of retrying Intent: %v", err)
	}
	active := parsed
	active.IntentTasks = append(runtimeIntentTaskList(nil), parsed.IntentTasks...)
	normalizeRuntimeIntentObjectiveMetadata(&active, nil)
	converted := convertRuntimeIntentTasks([]runtimeIntentTaskJSON(active.IntentTasks))
	if len(converted) != 1 || converted[0].Objective != "unknown" {
		t.Fatalf("unknown objective metadata must normalize conservatively, got %#v", converted)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsServiceStateWithEllipticalAction(t *testing.T) {
	currentText := "拖鞋没了，应该去哪里拿？"
	task := validRuntimeIntentProtocolTask(currentText, "location")
	task.Intent = "hotel_info"
	task.SubIntent = "supplies_self_help"
	task.Entities = runtimeIntentEntityList{{Text: "拖鞋", Type: "supply"}}
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, currentText); err != nil {
		t.Fatalf("a service state and its subject-omitted action question must remain one model-owned task: %v", err)
	}
}

// The legacy fixtures below document the retired local task-boundary and
// context-rewrite heuristics. Active model-owned contract coverage lives in
// intent_protocol_model_owned_test.go.
func legacyTestValidateRuntimeIntentDetectProtocolRequiresSelfContainedResolvedReference(t *testing.T) {
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
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "那麦田呢？"); err == nil || !strings.Contains(err.Error(), "literal span") {
		t.Fatalf("context completion belongs in resolvedText; rewritten task text must fail, got %v", err)
	}
	parsed.IntentTasks[0].Text = "那麦田呢？"

	parsed.IntentTasks[0].ResolutionState = "clear"
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "那麦田呢？"); err == nil || !strings.Contains(err.Error(), "must be resolved_from_context") {
		t.Fatalf("genuinely context-dependent text must still require resolved_from_context, got %v", err)
	}
}

func legacyTestRepairRuntimeIntentDetectProtocolRestoresDependentSourceText(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "breakfast", Objective: "time",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Text: "早餐几点", ResolvedText: "早餐几点", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "有早餐吗？",
		AdjacentAIReply:      "酒店不提供早餐。",
	}

	repairRuntimeIntentDetectProtocol(&parsed, "几点？", context, true)
	task := parsed.IntentTasks[0]
	if task.Text != "几点" || task.ResolvedText != "早餐几点" {
		t.Fatalf("repair must restore the literal dependent source without changing its self-contained query: %#v", task)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "几点？"); err != nil {
		t.Fatalf("restored dependent source must pass the normal protocol: %v", err)
	}
}

func legacyTestRepairRuntimeIntentDetectProtocolRejectsUnrelatedResolvedTopic(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "breakfast", Objective: "time",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Entities: runtimeIntentEntityList{{Text: "早餐", Type: "service"}},
		Text:     "早餐几点", ResolvedText: "早餐几点", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "停车收费吗？",
		AdjacentAIReply:      "停车是免费的。",
	}

	repairRuntimeIntentDetectProtocol(&parsed, "几点？", context, true)
	if task := parsed.IntentTasks[0]; task.Text != "早餐几点" || task.ResolvedText != "早餐几点" {
		t.Fatalf("an unrelated topic must not be silently rebound to the current short question: %#v", task)
	}

	parsed.IntentTasks[0].Text = "几点"
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "几点？", context, true); err == nil || !strings.Contains(err.Error(), "not grounded") {
		t.Fatalf("a literal short question with an invented topic must still trigger Intent repair: %v", err)
	}
}

func legacyTestValidateRuntimeIntentResolvedReferenceContextRejectsWelcomeOnlyHistory(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "breakfast", Objective: "time",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Text: "几点", ResolvedText: "早餐几点", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{AdjacentAIReply: "您好，欢迎入住。"}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "几点？", context, true); err == nil || !strings.Contains(err.Error(), "adjacent customer question") {
		t.Fatalf("a welcome message alone cannot supply a business topic: %v", err)
	}
}

func legacyTestRepairRuntimeIntentDetectProtocolRemovesPreviousQuestionFromResolvedReference(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "room_facilities", Objective: "availability",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Entities: runtimeIntentEntityList{
			{Text: "麦田房型", Type: "room_type"},
			{Text: "办公桌", Type: "facility"},
		},
		Text: "那麦田呢", ResolvedText: "合柴房型有办公桌吗？麦田房型有办公桌吗？",
		SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "合柴房型有办公桌吗？",
		AdjacentAIReply:      "合柴房型有办公桌。",
	}

	repairRuntimeIntentDetectProtocol(&parsed, "那麦田呢？", context, true)
	task := parsed.IntentTasks[0]
	if task.Text != "那麦田呢" || task.ResolvedText != "麦田房型有办公桌吗" {
		t.Fatalf("resolved reference must keep only the current answer target: %#v", task)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "那麦田呢？"); err != nil {
		t.Fatalf("cleaned reference must pass the normal protocol: %v", err)
	}
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "那麦田呢？", context, true); err != nil {
		t.Fatalf("cleaned reference must pass the history-aware protocol: %v", err)
	}
}

func legacyTestValidateRuntimeIntentResolvedReferenceContextRejectsRetainedPreviousTarget(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "room_facilities", Objective: "availability",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Entities: runtimeIntentEntityList{
			{Text: "麦田", Type: "room_type"},
			{Text: "办公桌", Type: "facility"},
		},
		Text: "那麦田呢", ResolvedText: "合柴房型有办公桌吗？艺林房型有办公桌吗？麦田房型有办公桌吗？",
		SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "合柴房型有办公桌吗？",
		AdjacentAIReply:      "合柴房型有办公桌。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "那麦田呢？", context, true); err == nil || !strings.Contains(err.Error(), "previous customer question") {
		t.Fatalf("a resolved reference retaining the previous answer target must trigger the existing Intent repair: %v", err)
	}
}

func legacyTestValidateRuntimeIntentResolvedReferenceContextRequiresDeclaredReplacementEntity(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "room_facilities", Objective: "availability",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Text: "那麦田呢", ResolvedText: "合柴房型有办公桌吗？麦田房型有办公桌吗？",
		SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "合柴房型有办公桌吗？",
		AdjacentAIReply:      "合柴房型有办公桌。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "那麦田呢？", context, true); err == nil || !strings.Contains(err.Error(), "declared current entity") {
		t.Fatalf("an explicit replacement without a declared current entity must trigger Intent repair: %v", err)
	}
}

func legacyTestValidateRuntimeIntentResolvedReferenceContextRejectsMergedPreviousRoomType(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "room_facilities", Objective: "availability",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Entities: runtimeIntentEntityList{
			{Text: "麦田", Type: "room_type"},
			{Text: "办公桌", Type: "facility"},
		},
		Text: "那麦田呢", ResolvedText: "合柴和麦田房型都有办公桌吗？",
		SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "合柴房型有办公桌吗？",
		AdjacentAIReply:      "合柴房型有办公桌。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "那麦田呢？", context, true); err == nil || !strings.Contains(err.Error(), "previous answer target") {
		t.Fatalf("a merged resolvedText must not keep the old room type as another answer target: %v", err)
	}
}

func legacyTestValidateRuntimeIntentResolvedReferenceContextRejectsMergedPreviousRoomTypeWithoutSuffix(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "room_facilities", Objective: "availability",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Entities: runtimeIntentEntityList{
			{Text: "麦田", Type: "room_type"},
			{Text: "办公桌", Type: "facility"},
		},
		Text: "那麦田呢", ResolvedText: "合柴和麦田都有办公桌吗？",
		SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "合柴有办公桌吗？",
		AdjacentAIReply:      "合柴有办公桌。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "那麦田呢？", context, true); err == nil || !strings.Contains(err.Error(), "previous answer target") {
		t.Fatalf("old room types must be rejected even when the previous question omitted the room-type suffix: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextRejectsAddedEntity(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "room_facilities", Objective: "availability",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Entities: runtimeIntentEntityList{
			{Text: "麦田", Type: "room_type"},
			{Text: "办公桌", Type: "facility"},
			{Text: "沙发", Type: "facility"},
		},
		Text: "那麦田呢", ResolvedText: "麦田房型有办公桌和沙发吗？",
		SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "合柴房型有办公桌吗？",
		AdjacentAIReply:      "合柴房型有办公桌。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "那麦田呢？", context, true); err == nil || !strings.Contains(err.Error(), "沙发") {
		t.Fatalf("a resolved reference must not add a new entity absent from the adjacent context: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextAcceptsAvailabilityReplacement(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "breakfast", Objective: "availability",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Entities: runtimeIntentEntityList{{Text: "早餐", Type: "service"}},
		Text:     "那早餐呢", ResolvedText: "有早餐吗", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "有夜宵吗？",
		AdjacentAIReply:      "酒店不提供夜宵。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "那早餐呢？", context, true); err != nil {
		t.Fatalf("an explicit replacement with the same adjacent business aspect must remain valid: %v", err)
	}
}

func TestValidateRuntimeIntentResolvedReferenceContextKeepsPronounFollowUp(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "door_access", Objective: "method",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Entities: runtimeIntentEntityList{{Text: "房门", Type: "facility"}},
		Text:     "这个怎么开", ResolvedText: "房门怎么打开", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "房门怎么打开？",
		AdjacentAIReply:      "完成登记后可以刷脸开门。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "这个怎么开？", context, true); err != nil {
		t.Fatalf("a pronoun follow-up must not be mistaken for an explicit new entity: %v", err)
	}
}

func legacyTestValidateRuntimeIntentResolvedReferenceContextRejectsUnsupportedReplacementPredicate(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "room_facilities", Objective: "availability",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Entities: runtimeIntentEntityList{{Text: "麦田", Type: "room_type"}},
		Text:     "那麦田呢", ResolvedText: "麦田有沙发吗", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "合柴有办公桌吗？",
		AdjacentAIReply:      "合柴有办公桌。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "那麦田呢？", context, true); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("an explicit replacement cannot invent a different predicate when the model omits that entity: %v", err)
	}
}

func legacyTestValidateRuntimeIntentResolvedReferenceContextRejectsRetainedServiceTarget(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "breakfast", Objective: "availability",
		RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Entities: runtimeIntentEntityList{{Text: "早餐", Type: "service"}},
		Text:     "那早餐呢", ResolvedText: "夜宵和早餐都有吗", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}
	context := runtimeIntentProtocolRepairContext{
		PreviousCustomerText: "有夜宵吗？",
		AdjacentAIReply:      "酒店不提供夜宵。",
	}

	if err := validateRuntimeIntentResolvedReferenceContext(parsed, "那早餐呢？", context, true); err == nil || !strings.Contains(err.Error(), "previous answer target") {
		t.Fatalf("old service targets must not survive an explicit replacement: %v", err)
	}
}

func legacyTestValidateRuntimeIntentDetectProtocolRejectsFalseContextResolutionForSelfContainedQuestion(t *testing.T) {
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
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "外卖地址怎么填？"); err == nil || !strings.Contains(err.Error(), "requires context-dependent original text") {
		t.Fatalf("self-contained current question with false context resolution must fail strict validation, got %v", err)
	}
	task := parsed.IntentTasks[0]
	if task.Text != "外卖地址怎么填？" || task.ResolvedText != "麦田房型的外卖地址怎么填" || task.ResolutionState != runtimeIntentResolutionResolvedFromContext {
		t.Fatalf("strict validation must not mutate the model task in place, got %#v", task)
	}
}

func TestValidateRuntimeIntentDetectProtocolAcceptsRelationBoundShortContext(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		resolvedText string
		intent       string
		subIntent    string
		objective    string
		relation     string
		knowledge    bool
	}{
		{
			name: "affirmative answer", text: "是的啊", resolvedText: "确认客户询问酒店是否有充电桩",
			intent: "hotel_info", subIntent: "parking_facilities", objective: "availability", relation: "clarification_answer", knowledge: true,
		},
		{
			name: "short confirmation", text: "对", resolvedText: "确认客户询问酒店是否有充电桩",
			intent: "hotel_info", subIntent: "parking_facilities", objective: "availability", relation: "clarification_answer", knowledge: true,
		},
		{
			name: "slot answer", text: "吴朝伟", resolvedText: "住客姓名是吴朝伟",
			intent: "hotel_variable", subIntent: "guest_name", objective: "confirm", relation: "clarification_answer",
		},
		{
			name: "elliptical follow up", text: "玩的勒", resolvedText: "酒店附近有什么适合游玩的地方",
			intent: "hotel_info", subIntent: "surrounding_facilities", objective: "recommendation", relation: "reference_previous", knowledge: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
				Intent: tt.intent, SubIntent: tt.subIntent, Objective: tt.objective,
				RelationToPrevious: tt.relation, ResolutionState: runtimeIntentResolutionResolvedFromContext,
				Text: tt.text, ResolvedText: tt.resolvedText, SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: tt.knowledge,
			}}}

			if err := validateRuntimeIntentDetectProtocol(parsed, nil, tt.text); err != nil {
				t.Fatalf("relation-bound short context must pass protocol validation: %v", err)
			}
			task := parsed.IntentTasks[0]
			if task.Text != tt.text || task.ResolvedText != tt.resolvedText || task.ResolutionState != runtimeIntentResolutionResolvedFromContext {
				t.Fatalf("valid contextual task must remain intact, got %#v", task)
			}
		})
	}
}

func legacyTestValidateRuntimeIntentDetectProtocolRejectsIndependentQuestionWithFalseHistoricalRelation(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "breakfast", Objective: "time",
		RelationToPrevious: "clarification_answer", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Text: "早餐几点", ResolvedText: "麦田房型早餐几点", SourceRefs: runtimeIntentSourceRefList{"U1"}, NeedsKnowledge: true,
	}}}

	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "早餐几点？"); err == nil || !strings.Contains(err.Error(), "requires context-dependent original text") {
		t.Fatalf("independent current question with false historical relation must fail strict validation, got %v", err)
	}
	task := parsed.IntentTasks[0]
	if task.Text != "早餐几点" || task.ResolvedText != "麦田房型早餐几点" || task.ResolutionState != runtimeIntentResolutionResolvedFromContext {
		t.Fatalf("strict validation must leave the rejected model task unchanged, got %#v", task)
	}
}

func TestValidateRuntimeIntentDetectProtocolKeepsSourceRefsStrictForRelationBoundShortContext(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{{
		Intent: "hotel_info", SubIntent: "parking_facilities", Objective: "availability",
		RelationToPrevious: "clarification_answer", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		Text: "是的啊", ResolvedText: "确认客户询问酒店是否有充电桩", SourceRefs: runtimeIntentSourceRefList{"U2"}, NeedsKnowledge: true,
	}}}

	err := validateRuntimeIntentDetectProtocol(parsed, nil, "是的啊")
	if err == nil || !strings.Contains(err.Error(), "invalid ref") {
		t.Fatalf("relation-bound context must not bypass sourceRefs validation, got %v", err)
	}
}

func legacyTestRepairRuntimeIntentDetectProtocolRepairsAdjacentRepeatReference(t *testing.T) {
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

func legacyTestRepairRuntimeIntentDetectProtocolDoesNotInventRepeatContext(t *testing.T) {
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

func legacyTestRepairRuntimeIntentDetectProtocolRejectsConflictingExplicitRepeatSubject(t *testing.T) {
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

func legacyTestRepairRuntimeIntentDetectProtocolRejectsSameObjectiveDifferentRepeatSubject(t *testing.T) {
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

func legacyTestValidateRuntimeIntentDetectProtocolRequiresCoverageForExplicitDistinctTargets(t *testing.T) {
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
		t.Fatalf("explicitly separate answer targets must trigger Intent protocol repair, got %v", err)
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

func legacyTestRepairRuntimeIntentDetectProtocolDoesNotRewriteDuplicateGap(t *testing.T) {
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

	if len(parsed.IntentTasks) != 5 || parsed.IntentTasks[2].Text != "怎么办入住" || parsed.IntentTasks[2].ResolvedText != "酒店房门怎么开" {
		t.Fatalf("local protocol repair must not rewrite model-owned task boundaries: %#v", parsed.IntentTasks)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err == nil {
		t.Fatal("duplicate model task must remain a protocol error for the existing Intent retry")
	}
}

func legacyTestRepairRuntimeIntentDetectProtocolDoesNotInferMissingTaskFromSemantics(t *testing.T) {
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
	if task.Text != "早餐几点" || task.ResolvedText != "这个怎么开" {
		t.Fatalf("local protocol repair must not infer a missing model task: %#v", task)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err == nil {
		t.Fatal("invalid duplicate ownership must remain a protocol error")
	}
}

func legacyTestRepairRuntimeIntentDetectProtocolKeepsAmbiguousDuplicateStrict(t *testing.T) {
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

func TestRepairRuntimeIntentDetectProtocolDoesNotRewriteModelObjective(t *testing.T) {
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

	if got := parsed.IntentTasks[0].Objective; got != "recommendation" {
		t.Fatalf("local protocol repair must not rewrite the model objective, got %q", got)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, currentText); err != nil {
		t.Fatalf("source-grounded model task should remain protocol-valid, got %v", err)
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

func legacyTestValidateRuntimeIntentDetectProtocolRejectsExtraAndDuplicateBusinessTasks(t *testing.T) {
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
			wantError: "not a literal span",
		},
		{
			name: "duplicate task",
			tasks: runtimeIntentTaskList{
				validRuntimeIntentProtocolTask("早餐几点", "time"),
				validRuntimeIntentProtocolTask("早餐几点", "time"),
			},
			wantError: "repeats or overlaps",
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

func legacyTestValidateRuntimeIntentDetectProtocolRejectsInvalidContextResolutionClaims(t *testing.T) {
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

func legacyTestValidateRuntimeIntentDetectProtocolRejectsInteractionForBusinessInformationTarget(t *testing.T) {
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
	customer := models.Message{
		SenderType:  enums.IMSenderTypeCustomer,
		MessageType: enums.IMMessageTypeText,
		Content:     "合柴和艺林有办公桌吗？",
	}
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
		RawItems:      []models.Message{customer, reply},
		LatestRawItem: &notice,
	}
	content, ok := immediatelyPreviousAIReply(history)
	if !ok || !strings.Contains(content, reply.Content) {
		t.Fatalf("expected substantive AI reply behind service notice, got %q ok=%v", content, ok)
	}
}

func TestImmediatelyPreviousAIReplyRequiresPrecedingCustomer(t *testing.T) {
	history := adapter.HistoryBuildResult{RawItems: []models.Message{
		{SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "合柴和艺林有办公桌。"},
	}}
	if content, ok := immediatelyPreviousAIReply(history); ok || content != "" {
		t.Fatalf("an orphan AI reply must not enable adjacent context, got %q ok=%v", content, ok)
	}
	if instruction := buildAdjacentAIReplyRelationInstruction(history); instruction != "" {
		t.Fatalf("an orphan AI reply must not enable the answer-relation prompt: %q", instruction)
	}
}

func TestImmediatelyPreviousServiceReplyGroupDoesNotMixAIAndHuman(t *testing.T) {
	for _, tt := range []struct {
		name    string
		replies []models.Message
	}{
		{
			name: "AI followed by human",
			replies: []models.Message{
				{SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "AI 的答复。"},
				{SenderType: enums.IMSenderTypeAgent, MessageType: enums.IMMessageTypeText, Content: "人工补充。"},
			},
		},
		{
			name: "human followed by AI",
			replies: []models.Message{
				{SenderType: enums.IMSenderTypeAgent, MessageType: enums.IMMessageTypeText, Content: "人工答复。"},
				{SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "AI 补充。"},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			history := adapter.HistoryBuildResult{RawItems: append([]models.Message{{
				SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "房型有什么配置？",
			}}, tt.replies...)}
			if customer, replies, senderType, ok := immediatelyPreviousServiceReplyGroup(history); ok || customer != "" || len(replies) != 0 || senderType != "" {
				t.Fatalf("mixed AI/human replies must not form one adjacent group: customer=%q replies=%#v sender=%q ok=%v", customer, replies, senderType, ok)
			}
		})
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

func TestBuildRuntimeIntentProtocolRepairContextAcceptsHumanReplyPair(t *testing.T) {
	history := adapter.HistoryBuildResult{RawItems: []models.Message{
		{ID: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "有早餐吗？"},
		{ID: 2, SenderType: enums.IMSenderTypeAgent, MessageType: enums.IMMessageTypeText, Content: "有的。"},
	}}
	context := buildRuntimeIntentProtocolRepairContext(history)
	if !strings.Contains(context.PreviousCustomerText, "有早餐吗？") || !strings.Contains(context.AdjacentServiceReply, "有的。") || context.AdjacentAIReply != "" {
		t.Fatalf("ordinary context resolution must accept a real adjacent human-service pair without treating it as AI: %#v", context)
	}
}

func TestBuildRuntimeIntentProtocolRepairContextRejectsServiceNoticeOnly(t *testing.T) {
	history := adapter.HistoryBuildResult{RawItems: []models.Message{
		{ID: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "有早餐吗？"},
		{ID: 2, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "帮您转接同事啦～", ClientMsgID: "ai_handoff_success_direct_1_1"},
	}}
	context := buildRuntimeIntentProtocolRepairContext(history)
	if context.PreviousCustomerText != "" || context.AdjacentServiceReply != "" {
		t.Fatalf("a service notice is not a service answer pair: %#v", context)
	}
}

func TestParseRuntimeIntentDetectJSONAcceptsMissingV2ResourceTaskObjective(t *testing.T) {
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
	if got := parsed.IntentTasks[0].Objective; got != "" {
		t.Fatalf("V2 parse must preserve the missing objective for strict validation, got %q", got)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "入住小程序发我"); err != nil {
		t.Fatalf("missing objective metadata must not reject a structurally valid resource task: %v", err)
	}
	active := parsed
	active.IntentTasks = append(runtimeIntentTaskList(nil), parsed.IntentTasks...)
	normalizeRuntimeIntentObjectiveMetadata(&active, nil)
	converted := convertRuntimeIntentTasks([]runtimeIntentTaskJSON(active.IntentTasks))
	if len(converted) != 1 || converted[0].Objective != "unknown" || !converted[0].NeedsResource {
		t.Fatalf("resource action must survive conservative objective normalization, got %#v", converted)
	}
	legacy := &models.ReplyIntentProfile{IntentJSONSchema: `{"intentTasks":[{"intent":"","text":""}]}`}
	applyLegacyRuntimeIntentProtocolDefaults(&parsed, legacy)
	if parsed.IntentTasks[0].Objective != "action_request" {
		t.Fatalf("legacy compatibility may still default the explicit resource task, got %#v", parsed.IntentTasks[0])
	}
}

func TestParseRuntimeIntentDetectJSONLeavesV2ResourceSemanticsModelOwned(t *testing.T) {
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
		t.Fatalf("objective metadata must not veto the model-owned task set: %v", err)
	}
	if len(parsed.IntentTasks) != 2 {
		t.Fatalf("expected two intent tasks, got %#v", parsed.IntentTasks)
	}
	resourceTask := parsed.IntentTasks[1]
	if resourceTask.Intent != "interaction" || resourceTask.Objective != "" || resourceTask.ResourceAction != "send_miniprogram" {
		t.Fatalf("V2 parse must not rewrite model semantics, got %#v", resourceTask)
	}
	legacy := &models.ReplyIntentProfile{IntentJSONSchema: `{"intentTasks":[{"intent":"","text":""}]}`}
	applyLegacyRuntimeIntentProtocolDefaults(&parsed, legacy)
	resourceTask = parsed.IntentTasks[1]
	if resourceTask.Intent != "hotel_variable" || resourceTask.Objective != "action_request" || !resourceTask.NeedsResource || resourceTask.ResourceAction != "provide_mini_program" {
		t.Fatalf("legacy resource compatibility must remain available, got %#v", resourceTask)
	}

	converted := convertRuntimeIntentTasks([]runtimeIntentTaskJSON(parsed.IntentTasks))
	if len(converted) != 2 || converted[1].Intent != "hotel_variable" || !converted[1].NeedsResource || converted[1].ResourceAction != "provide_mini_program" {
		t.Fatalf("normalized resource task must survive conversion, got %#v", converted)
	}
}

func TestParseRuntimeIntentDetectJSONDegradesV2ResourceObjectiveAlias(t *testing.T) {
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
	if resourceTask.Objective != "resource" || resourceTask.ResourceAction != "" || resourceTask.NeedsResource {
		t.Fatalf("V2 parse must leave resource aliases untouched for validation, got %#v", resourceTask)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, "小程序也发我一下"); err != nil {
		t.Fatalf("V2 resource objective metadata must not reject the task: %v", err)
	}
	active := parsed
	active.IntentTasks = append(runtimeIntentTaskList(nil), parsed.IntentTasks...)
	normalizeRuntimeIntentObjectiveMetadata(&active, nil)
	if got := active.IntentTasks[0].Objective; got != "unknown" {
		t.Fatalf("unknown active objective metadata must degrade conservatively, got %q", got)
	}
	legacy := &models.ReplyIntentProfile{IntentJSONSchema: `{"intentTasks":[{"intent":"","text":""}]}`}
	applyLegacyRuntimeIntentProtocolDefaults(&parsed, legacy)
	resourceTask = parsed.IntentTasks[0]
	if resourceTask.Objective != "action_request" || resourceTask.ResourceAction != "provide_mini_program" || !resourceTask.NeedsResource {
		t.Fatalf("legacy bounded resource alias must remain compatible, got %#v", resourceTask)
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

func legacyTestValidateRuntimeIntentDetectProtocolRejectsMissingObviousAtomicQuestion(t *testing.T) {
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("你们有没有外卖机器人", "availability"),
		validRuntimeIntentProtocolTask("外卖地址怎么填", "method"),
	}}
	err := validateRuntimeIntentDetectProtocol(parsed, nil, "你们有没有外卖机器人？外卖地址怎么填？布草是不是一客一换？")
	if err == nil || !strings.Contains(err.Error(), "atomic question 3 of 3") {
		t.Fatalf("an obvious unanswered atomic question must trigger Intent protocol repair, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolAllowsRelatedAspectsInOneCompoundTask(t *testing.T) {
	for _, tt := range []struct {
		text       string
		entityText string
		entityType string
	}{
		{text: "矿泉水有几瓶？矿泉水免费吗？", entityText: "矿泉水", entityType: "supply"},
		{text: "早餐几点开始？位置在哪里？", entityText: "早餐", entityType: "service"},
		{text: "矿泉水几瓶？收费情况呢？", entityText: "矿泉水", entityType: "supply"},
	} {
		task := validRuntimeIntentProtocolTask(tt.text, "compound_information")
		task.Entities = runtimeIntentEntityList{{Text: tt.entityText, Type: tt.entityType}}
		if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, tt.text); err != nil {
			t.Fatalf("one compound task may cover related aspects of one subject for %q: %v", tt.text, err)
		}
	}
}

func legacyTestValidateRuntimeIntentDetectProtocolRejectsUnrelatedQuestionsHiddenInCompoundTask(t *testing.T) {
	text := "早餐几点？停车免费吗？"
	task := validRuntimeIntentProtocolTask(text, "compound_information")
	task.Entities = runtimeIntentEntityList{
		{Text: "早餐", Type: "service"},
		{Text: "停车", Type: "service"},
	}
	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, text)
	if err == nil || !strings.Contains(err.Error(), "atomic question") {
		t.Fatalf("unrelated answer targets must remain separate model-owned tasks, got %v", err)
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

func TestValidateRuntimeIntentDetectProtocolAcceptsOrderedSpansFromUnpunctuatedParagraph(t *testing.T) {
	currentText := "我饿了 有啥吃的推荐没 以及明天要去附近玩 你知道哪里好玩吗  还有啊 我怎么把门打开啊 有没有停车场 我开电车来的你懂我意思吗  发票咋开"
	tasks := runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("我饿了 有啥吃的推荐没", "recommendation"),
		validRuntimeIntentProtocolTask("明天要去附近玩 你知道哪里好玩吗", "recommendation"),
		validRuntimeIntentProtocolTask("我怎么把门打开啊", "method"),
		validRuntimeIntentProtocolTask("有没有停车场", "availability"),
		validRuntimeIntentProtocolTask("我开电车来的你懂我意思吗", "availability"),
		validRuntimeIntentProtocolTask("发票咋开", "method"),
	}
	tasks[4].ResolvedText = "酒店停车场有没有充电桩"

	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: tasks}, nil, currentText); err != nil {
		t.Fatalf("ordered literal spans must be accepted for an uncertain coarse paragraph: %v", err)
	}
}

func TestRuntimeIntentProtocolDoesNotTreatUnderstandingMarkerAsHistoricalReference(t *testing.T) {
	text := "我开电车来的你懂我意思吗"
	if runtimeIntentAtomicCandidateRequiresContext(text) {
		t.Fatalf("a self-contained implied charging question must not require older history: %q", text)
	}
	task := validRuntimeIntentProtocolTask(text, "availability")
	task.ResolvedText = "酒店有没有电车充电桩"
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{task}}, nil, text); err != nil {
		t.Fatalf("the model may classify the implied question as independent and clear: %v", err)
	}
	if !runtimeIntentAtomicCandidateRequiresContext("你懂我意思吗") {
		t.Fatal("the standalone discourse marker must still require real adjacent context")
	}
}

func legacyTestValidateRuntimeIntentDetectProtocolRequiresPrimaryTaskForEachBusinessSource(t *testing.T) {
	burst := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [文字] 有没有停车场",
		"2. [文字] 我开电车来的你懂我意思吗",
	})
	charging := validRuntimeIntentProtocolTask("我开电车来的你懂我意思吗", "availability")
	charging.RelationToPrevious = "independent"
	charging.ResolutionState = "resolved_from_context"
	charging.ResolvedText = "酒店停车场有没有电车充电桩"
	charging.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}

	err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{charging}}, nil, burst)
	if err == nil || !strings.Contains(err.Error(), "source U1") {
		t.Fatalf("an independent parking question cannot be consumed only as charging context, got %v", err)
	}

	parking := validRuntimeIntentProtocolTask("有没有停车场", "availability")
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{parking, charging}}, nil, burst); err != nil {
		t.Fatalf("parking and context-resolved charging tasks must both pass in source order: %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolUsesModelOwnedBurstSources(t *testing.T) {
	burst := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [文字] 好困啊",
		"2. [文字] 有没有咖啡",
	})
	coffee := validRuntimeIntentProtocolTask("有没有咖啡", "availability")
	coffee.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{coffee}}, nil, burst); err != nil {
		t.Fatalf("one model-owned task may consume an adjacent context source, got %v", err)
	}

	orderedBurst := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [文字] 早餐几点还有停车收费吗",
		"2. [文字] 发票咋开",
	})
	breakfast := validRuntimeIntentProtocolTask("早餐几点", "time")
	parking := validRuntimeIntentProtocolTask("停车收费吗", "price")
	invoice := validRuntimeIntentProtocolTask("发票咋开", "method")
	invoice.SourceRefs = runtimeIntentSourceRefList{"U2"}
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{breakfast, parking, invoice}}, nil, orderedBurst); err != nil {
		t.Fatalf("same-source model tasks followed by the next URef must pass, got %v", err)
	}
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{invoice, breakfast, parking}}, nil, orderedBurst); err == nil || !strings.Contains(err.Error(), "source order") {
		t.Fatalf("out-of-order model tasks must fail source provenance, got %v", err)
	}
}

func TestValidateRuntimeIntentDetectProtocolKeepsBreakfastFollowUpsInSourceOrder(t *testing.T) {
	burst := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [文字] 有早餐吗？",
		"2. [文字] 几点？",
		"3. [文字] 在哪吃？",
	})

	availability := validRuntimeIntentProtocolTask("有早餐吗？", "availability")
	availability.SubIntent = "breakfast"

	timeTask := validRuntimeIntentProtocolTask("几点？", "time")
	timeTask.SubIntent = "breakfast"
	timeTask.RelationToPrevious = "independent"
	timeTask.ResolutionState = runtimeIntentResolutionResolvedFromContext
	timeTask.ResolvedText = "早餐几点提供？"
	timeTask.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}

	location := validRuntimeIntentProtocolTask("在哪吃？", "location")
	location.SubIntent = "breakfast"
	location.RelationToPrevious = "independent"
	location.ResolutionState = runtimeIntentResolutionResolvedFromContext
	location.ResolvedText = "早餐在哪里吃？"
	location.SourceRefs = runtimeIntentSourceRefList{"U3", "U1"}

	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{availability, timeTask, location}}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, burst); err != nil {
		t.Fatalf("breakfast follow-ups must each own their source and preserve source order: %v", err)
	}

	outOfOrder := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{availability, location, timeTask}}
	if err := validateRuntimeIntentDetectProtocol(outOfOrder, nil, burst); err == nil || !strings.Contains(err.Error(), "source order") {
		t.Fatalf("out-of-order breakfast follow-ups must fail validation, got %v", err)
	}
}

func legacyTestRepairRuntimeIntentDetectProtocolRestoresBurstDependentSourceTexts(t *testing.T) {
	burst := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [文字] 有早餐吗？",
		"2. [文字] 几点？",
		"3. [文字] 在哪吃？",
	})
	availability := validRuntimeIntentProtocolTask("有早餐吗？", "availability")
	availability.SubIntent = "breakfast"
	timeTask := validRuntimeIntentProtocolTask("早餐几点提供？", "time")
	timeTask.SubIntent = "breakfast"
	timeTask.RelationToPrevious = "independent"
	timeTask.ResolutionState = runtimeIntentResolutionResolvedFromContext
	timeTask.ResolvedText = "早餐几点提供？"
	timeTask.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}
	location := validRuntimeIntentProtocolTask("早餐在哪里吃？", "location")
	location.SubIntent = "breakfast"
	location.RelationToPrevious = "independent"
	location.ResolutionState = runtimeIntentResolutionResolvedFromContext
	location.ResolvedText = "早餐在哪里吃？"
	location.SourceRefs = runtimeIntentSourceRefList{"U3", "U1"}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{availability, timeTask, location}}

	repairRuntimeIntentDetectProtocol(&parsed, burst, runtimeIntentProtocolRepairContext{}, true)
	if parsed.IntentTasks[1].Text != "几点" || parsed.IntentTasks[2].Text != "在哪吃" {
		t.Fatalf("each burst task must recover its own literal primary source: %#v", parsed.IntentTasks)
	}
	if err := validateRuntimeIntentDetectProtocol(parsed, nil, burst); err != nil {
		t.Fatalf("repaired burst references must pass the base protocol: %v", err)
	}
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, burst, runtimeIntentProtocolRepairContext{}, true); err != nil {
		t.Fatalf("repaired burst references must remain grounded in their declared current-turn sources: %v", err)
	}
}

func legacyTestRepairRuntimeIntentDetectProtocolRejectsWrongBurstTopic(t *testing.T) {
	burst := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [文字] 停车收费吗？",
		"2. [文字] 几点？",
	})
	parking := validRuntimeIntentProtocolTask("停车收费吗？", "price")
	wrong := validRuntimeIntentProtocolTask("早餐几点？", "time")
	wrong.SubIntent = "breakfast"
	wrong.RelationToPrevious = "independent"
	wrong.ResolutionState = runtimeIntentResolutionResolvedFromContext
	wrong.ResolvedText = "早餐几点？"
	wrong.SourceRefs = runtimeIntentSourceRefList{"U2", "U1"}
	parsed := runtimeIntentDetectJSON{IntentTasks: runtimeIntentTaskList{parking, wrong}}

	repairRuntimeIntentDetectProtocol(&parsed, burst, runtimeIntentProtocolRepairContext{}, true)
	if parsed.IntentTasks[1].Text != "早餐几点？" {
		t.Fatalf("an unrelated burst topic must not be silently rebound: %#v", parsed.IntentTasks[1])
	}
	parsed.IntentTasks[1].Text = "几点"
	if err := validateRuntimeIntentResolvedReferenceContext(parsed, burst, runtimeIntentProtocolRepairContext{}, true); err == nil || !strings.Contains(err.Error(), "not grounded") {
		t.Fatalf("a literal burst follow-up with an invented topic must trigger Intent repair: %v", err)
	}
}

func TestNormalizeModelIntentTraceDoesNotLocallyResplitV2Tasks(t *testing.T) {
	text := "早餐几点还有停车收费吗然后发票咋开"
	intent := callbacks.IntentTraceData{
		PrimaryIntent:            "hotel_info",
		SemanticContractExpected: true,
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_info", SubIntent: "breakfast", Objective: "time", RelationToPrevious: "independent", ResolutionState: "clear", Text: "早餐几点", ResolvedText: "早餐几点", SourceRefs: []string{"U1"}, NeedsKnowledge: true},
			{Intent: "hotel_info", SubIntent: "parking", Objective: "price", RelationToPrevious: "independent", ResolutionState: "clear", Text: "停车收费吗", ResolvedText: "停车收费吗", SourceRefs: []string{"U1"}, NeedsKnowledge: true},
			{Intent: "hotel_info", SubIntent: "invoice", Objective: "method", RelationToPrevious: "independent", ResolutionState: "clear", Text: "发票咋开", ResolvedText: "发票咋开", SourceRefs: []string{"U1"}, NeedsKnowledge: true},
		},
	}
	got := normalizeModelIntentTrace(intent, RunInput{UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: text}}, adapter.HistoryBuildResult{}, nil)
	if len(got.IntentTasks) != 3 {
		t.Fatalf("V2 task count must remain model-owned, got %#v", got.IntentTasks)
	}
	for index, want := range []string{"早餐几点", "停车收费吗", "发票咋开"} {
		if got.IntentTasks[index].Text != want || len(got.IntentTasks[index].SourceRefs) != 1 || got.IntentTasks[index].SourceRefs[0] != "U1" {
			t.Fatalf("V2 task %d was locally rewritten: %#v", index, got.IntentTasks[index])
		}
	}
}

func TestNormalizeModelIntentTracePreservesV2PrimaryAndContextSourceOrder(t *testing.T) {
	burst := utils.BuildRuntimeCustomerBurstEnvelope([]string{
		"1. [文字] 好困啊",
		"2. [文字] 有没有咖啡",
	})
	intent := callbacks.IntentTraceData{
		PrimaryIntent:            "hotel_info",
		SemanticContractExpected: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "hotel_info", SubIntent: "coffee", Objective: "availability", RelationToPrevious: "reference_previous", ResolutionState: "resolved_from_context",
			Text: "有没有咖啡", ResolvedText: "酒店有没有咖啡", SourceRefs: []string{"U2", "U1"}, NeedsKnowledge: true,
		}},
	}

	got := normalizeModelIntentTrace(intent, RunInput{UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: burst}}, adapter.HistoryBuildResult{}, nil)
	if len(got.IntentTasks) != 1 || len(got.IntentTasks[0].SourceRefs) != 2 || got.IntentTasks[0].SourceRefs[0] != "U2" || got.IntentTasks[0].SourceRefs[1] != "U1" {
		t.Fatalf("V2 primary/context binding must remain exactly model-owned, got %#v", got.IntentTasks)
	}
}

func legacyTestValidateRuntimeIntentDetectProtocolRejectsInventedOrOmittedCoarseSpanTask(t *testing.T) {
	currentText := "早餐几点 还有停车收费吗 然后发票咋开"
	base := runtimeIntentTaskList{
		validRuntimeIntentProtocolTask("早餐几点", "time"),
		validRuntimeIntentProtocolTask("停车收费吗", "price"),
		validRuntimeIntentProtocolTask("发票咋开", "method"),
	}

	invented := append(runtimeIntentTaskList(nil), base...)
	invented = append(invented, validRuntimeIntentProtocolTask("有没有健身房", "availability"))
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: invented}, nil, currentText); err == nil || !strings.Contains(err.Error(), "literal span") {
		t.Fatalf("invented task must fail ordered span ownership, got %v", err)
	}

	omitted := append(runtimeIntentTaskList(nil), base[:2]...)
	if err := validateRuntimeIntentDetectProtocol(runtimeIntentDetectJSON{IntentTasks: omitted}, nil, currentText); err != nil {
		t.Fatalf("unpunctuated residual text must remain model-owned instead of being locally split, got %v", err)
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
