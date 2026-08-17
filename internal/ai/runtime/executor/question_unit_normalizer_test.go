package executor

import (
	"fmt"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

const productionMessage1452Transcript = "这附近有附近有什么地方好玩儿的呀，什么景点啊，好吃的之类的有没有啊？我就换个安静点的房间，别帮我换了吧，你就说有没有安静的房间吧。最后告诉我有什么酒店什么。好困，能不能搞点咖啡来呀？"

// 语音 1354 回放（多模态契约 13）：三个子问题各自绑定真实 Span。
func envelopeTestScope() contextcompiler.EnvelopeScope {
	return contextcompiler.EnvelopeScope{TenantID: 1, StoreID: 1, ConversationID: 2, SessionNo: 1, TurnID: 333, TurnVersion: 2}
}

func voiceEnvelope() contextcompiler.TurnInputEnvelope {
	messages := []models.Message{
		{ID: 1354, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice,
			Content: "v.amr", Payload: `{"mediaText":"现在你给我说一下，你们酒店有拖鞋没有，然后有没有洗发水？然后床单，床单脏了怎么办？","mediaUnderstandingStatus":"understood"}`},
	}
	return contextcompiler.BuildTurnInputEnvelope(envelopeTestScope(), messages)
}

func voiceTasks(e contextcompiler.TurnInputEnvelope) []IntentTaskV3 {
	return []IntentTaskV3{
		{Sequence: 1, Intent: "hotel_info", SubIntent: "supplies_self_help",
			SourceRefs: []string{"U1"}, SourceSpans: []IntentSourceSpan{spanFor(e, "你们酒店有拖鞋没有")},
			NormalizedText: "酒店有拖鞋吗", RequestMode: "answer", Confidence: 0.98},
		{Sequence: 2, Intent: "hotel_info", SubIntent: "supplies_self_help",
			SourceRefs: []string{"U1"}, SourceSpans: []IntentSourceSpan{spanFor(e, "有没有洗发水")},
			NormalizedText: "酒店有洗发水吗", RequestMode: "answer", Confidence: 0.98},
		{Sequence: 3, Intent: "hotel_info", SubIntent: "supplies_self_help",
			SourceRefs: []string{"U1"}, SourceSpans: []IntentSourceSpan{spanFor(e, "床单脏了怎么办")},
			NormalizedText: "床单脏了怎么办", RequestMode: "answer", Confidence: 0.97},
	}
}

// spanFor 从 utterance 文本中按子串定位一个精确 rune Span。
func spanFor(e contextcompiler.TurnInputEnvelope, substr string) IntentSourceSpan {
	text := e.Utterances[0].Text
	runes := []rune(text)
	idx := strings.Index(text, substr)
	if idx < 0 {
		return IntentSourceSpan{SourceRef: "U1", Start: 0, End: len(runes), Quote: text}
	}
	start := len([]rune(text[:idx]))
	return IntentSourceSpan{SourceRef: "U1", Start: start, End: start + len([]rune(substr)), Quote: substr}
}

func TestValidateIntentTaskSourcesAcceptsRealSpans(t *testing.T) {
	e := voiceEnvelope()
	issues := ValidateIntentTaskSources(e, voiceTasks(e))
	if len(issues) != 0 {
		t.Fatalf("expected valid spans, got %+v", issues)
	}
}

func TestValidateIntentTaskSourcesRejectsBadQuote(t *testing.T) {
	e := voiceEnvelope()
	tasks := []IntentTaskV3{{
		Sequence: 1, Intent: "hotel_info", SubIntent: "supplies_self_help",
		SourceRefs:     []string{"U1"},
		SourceSpans:    []IntentSourceSpan{{SourceRef: "U1", Start: 0, End: 5, Quote: "不存在的文字"}},
		NormalizedText: "x", RequestMode: "answer",
	}}
	issues := ValidateIntentTaskSources(e, tasks)
	if len(issues) == 0 || issues[0].Code != "intent_source_span_invalid" {
		t.Fatalf("expected span invalid, got %+v", issues)
	}
}

// 语音 1362 回放（多模态契约 28.3）：四个相同全文 Task 必须被抑制为单 QuestionUnit。
func TestNormalizeSuppressesSameFullTextDuplicates(t *testing.T) {
	messages := []models.Message{
		{ID: 1362, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice,
			Content: "v.amr", Payload: `{"mediaText":"我要吃本地菜，还要吃点面啊，火锅啥烧的都可以","mediaUnderstandingStatus":"understood"}`},
	}
	e := contextcompiler.BuildTurnInputEnvelope(envelopeTestScope(), messages)
	full := e.Utterances[0].Text
	runes := []rune(full)
	tasks := make([]IntentTaskV3, 4)
	for i := range tasks {
		tasks[i] = IntentTaskV3{Sequence: i + 1, Intent: "hotel_info", SubIntent: "surrounding_facilities",
			SourceRefs:     []string{"U1"},
			SourceSpans:    []IntentSourceSpan{{SourceRef: "U1", Start: 0, End: len(runes), Quote: full}},
			NormalizedText: full, RequestMode: "answer", Confidence: 0.9}
	}
	result := NormalizeIntentTasks(e, tasks)
	if len(result.AcceptedUnits) != 1 {
		t.Fatalf("expected 1 accepted unit, got %d (%+v)", len(result.AcceptedUnits), result)
	}
	if len(result.SuppressedUnits) != 3 {
		t.Fatalf("expected 3 suppressed, got %d", len(result.SuppressedUnits))
	}
	if result.SuppressedUnits[0].ReasonCode != "same_source_duplicate" {
		t.Fatalf("unexpected reason: %s", result.SuppressedUnits[0].ReasonCode)
	}
}

func TestNormalizeKeepsDistinctSpans(t *testing.T) {
	e := voiceEnvelope()
	result := NormalizeIntentTasks(e, voiceTasks(e))
	if len(result.AcceptedUnits) != 3 {
		t.Fatalf("expected 3 accepted units, got %d", len(result.AcceptedUnits))
	}
	if result.Status != "accepted" {
		t.Fatalf("expected accepted status, got %s", result.Status)
	}
	texts := map[string]bool{}
	for _, unit := range result.AcceptedUnits {
		texts[unit.Text] = true
	}
	if len(texts) != 3 {
		t.Fatalf("expected 3 distinct texts, got %d", len(texts))
	}
}

// 生产消息 1452 精确回放：同一段语音包含游玩、安静房和咖啡三个问题。
// 每个 QuestionUnit 与知识 Query 必须只使用自己的真实来源片段；旧实现把
// 整段 transcript 复制给三个 Task，会造成三次相同检索和三次相同图片发送。
func TestProductionMessage1452KeepsAtomicVoiceQuestions(t *testing.T) {
	envelope := contextcompiler.BuildTurnInputEnvelope(contextcompiler.EnvelopeScope{
		TenantID: 2, StoreID: 1, ConversationID: 2, SessionNo: 5, TurnID: 370, TurnVersion: 1,
	}, []models.Message{{
		ID: 1452, TenantID: 2, ConversationID: 2, SessionNo: 5,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeVoice,
		Content: "wx_protocol_1006354.mp3",
		Payload: `{"mediaText":"` + productionMessage1452Transcript + `","mediaUnderstandingStatus":"understood"}`,
	}})
	fragments := []string{
		"这附近有附近有什么地方好玩儿的呀，什么景点啊，好吃的之类的有没有啊？",
		"我就换个安静点的房间，别帮我换了吧，你就说有没有安静的房间吧。",
		"好困，能不能搞点咖啡来呀？",
	}
	tasks := []IntentTaskV3{
		{Sequence: 1, Intent: "hotel_info", SubIntent: "surrounding_facilities", SourceRefs: []string{"U1"}, SourceSpans: []IntentSourceSpan{spanFor(envelope, fragments[0])}, RequestMode: "answer"},
		{Sequence: 2, Intent: "hotel_info", SubIntent: "room_change", SourceRefs: []string{"U1"}, SourceSpans: []IntentSourceSpan{spanFor(envelope, fragments[1])}, RequestMode: "answer"},
		{Sequence: 3, Intent: "service_request", SubIntent: "coffee_delivery", SourceRefs: []string{"U1"}, SourceSpans: []IntentSourceSpan{spanFor(envelope, fragments[2])}, RequestMode: "answer"},
	}

	if issues := ValidateIntentTaskSources(envelope, tasks); len(issues) != 0 {
		t.Fatalf("production 1452 spans were rejected: %+v", issues)
	}
	normalized := NormalizeIntentTasks(envelope, tasks)
	if normalized.Status != "accepted" || len(normalized.AcceptedUnits) != len(fragments) {
		t.Fatalf("production 1452 normalization=%+v", normalized)
	}
	seenSpans := make(map[string]struct{}, len(fragments))
	for index, unit := range normalized.AcceptedUnits {
		if unit.Text != fragments[index] {
			t.Fatalf("task %d source text=%q want %q", index+1, unit.Text, fragments[index])
		}
		span := unit.SourceSpans[0]
		spanKey := fmt.Sprintf("%s:%d:%d", span.SourceRef, span.Start, span.End)
		if _, duplicate := seenSpans[spanKey]; duplicate {
			t.Fatalf("task %d reused another task span: %+v", index+1, span)
		}
		seenSpans[spanKey] = struct{}{}
		query := runtimeTaskKnowledgeQuery(callbacks.ReplyTaskPlanTraceData{
			TaskKey: "production-1452", Sequence: index + 1, Text: unit.Text,
			SourceMessageID: unit.PrimarySourceMessageID, SourceSpanStart: span.Start, SourceSpanEnd: span.End,
		})
		if query != fragments[index] || query == productionMessage1452Transcript {
			t.Fatalf("task %d knowledge query=%q want atomic fragment %q", index+1, query, fragments[index])
		}
	}

	fullRunes := []rune(productionMessage1452Transcript)
	duplicatedFullTextTasks := make([]IntentTaskV3, len(tasks))
	for index := range duplicatedFullTextTasks {
		duplicatedFullTextTasks[index] = tasks[index]
		duplicatedFullTextTasks[index].SourceSpans = []IntentSourceSpan{{
			SourceRef: "U1", Start: 0, End: len(fullRunes), Quote: productionMessage1452Transcript,
		}}
	}
	issues := ValidateIntentTaskSources(envelope, duplicatedFullTextTasks)
	if len(issues) != 2 {
		t.Fatalf("three duplicated full-text tasks must produce two duplicate issues, got %+v", issues)
	}
	for _, issue := range issues {
		if issue.Code != "intent_duplicate_full_span" {
			t.Fatalf("unexpected duplicated full-text issue: %+v", issue)
		}
	}
}

func TestNormalizeDegradesWhenAllSpansInvalid(t *testing.T) {
	e := voiceEnvelope()
	tasks := []IntentTaskV3{{
		Sequence: 1, Intent: "x", SubIntent: "y", SourceRefs: []string{"U9"},
		SourceSpans:    []IntentSourceSpan{{SourceRef: "U9", Start: 0, End: 1, Quote: "z"}},
		NormalizedText: "q", RequestMode: "answer",
	}}
	result := NormalizeIntentTasks(e, tasks)
	if result.Status != "degraded_clause_tasks" {
		t.Fatalf("expected degraded_clause_tasks, got %s", result.Status)
	}
	if len(result.AcceptedUnits) != 5 {
		t.Fatalf("expected 5 punctuation-delimited degraded units, got %d", len(result.AcceptedUnits))
	}
	want := []string{"现在你给我说一下，", "你们酒店有拖鞋没有，", "然后有没有洗发水？", "然后床单，", "床单脏了怎么办？"}
	for index, unit := range result.AcceptedUnits {
		if unit.Text != want[index] {
			t.Fatalf("degraded unit %d text=%q want %q", index+1, unit.Text, want[index])
		}
		if unit.Text == e.Utterances[0].Text {
			t.Fatalf("degraded unit duplicated the full transcript: %+v", unit)
		}
	}
}

func TestFallbackClauseSplitterKeepsProductionVoiceQuestionsIndependent(t *testing.T) {
	spans := splitFallbackUtteranceClauses(productionMessage1452Transcript, 12)
	if len(spans) != 9 {
		t.Fatalf("expected nine punctuation-delimited fallback tasks, got %d: %+v", len(spans), spans)
	}
	runes := []rune(productionMessage1452Transcript)
	seen := map[string]struct{}{}
	for _, span := range spans {
		if span.Quote != string(runes[span.Start:span.End]) {
			t.Fatalf("fallback span does not match source: %+v", span)
		}
		if _, exists := seen[span.Quote]; exists {
			t.Fatalf("fallback duplicated a source clause: %q", span.Quote)
		}
		seen[span.Quote] = struct{}{}
	}
}

func relationEnvelope(text string) contextcompiler.TurnInputEnvelope {
	return contextcompiler.BuildTurnInputEnvelope(envelopeTestScope(), []models.Message{{
		ID: 2001, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: text,
	}})
}

func relationTask(e contextcompiler.TurnInputEnvelope, intent, subIntent, requestMode string) IntentTaskV3 {
	return IntentTaskV3{
		Sequence: 1, Intent: intent, SubIntent: subIntent,
		SourceRefs: []string{"U1"}, SourceSpans: []IntentSourceSpan{spanFor(e, e.Utterances[0].Text)},
		RequestMode: requestMode, Requirements: []RequirementSeed{{Sequence: 1, Kind: "answer", Required: true}},
	}
}

func TestNormalizeRelationSameTopicFollowUpInheritsUnresolvedTask(t *testing.T) {
	e := relationEnvelope("收费呢？")
	e.UnresolvedTasks = []contextcompiler.EnvelopeUnresolvedTask{{
		TaskKey: "task-parking", Intent: "hotel_info", SubIntent: "parking", Status: "pending",
		ResolvedTopic: "parking", Requirements: []contextcompiler.EnvelopeRequirement{{
			Sequence: 1, Kind: "parking_fee", Required: true,
		}},
	}}
	result := NormalizeIntentTasksWithDialogueAct(e, []IntentTaskV3{relationTask(e, "hotel_info", "parking", "answer")}, "follow_up")
	if len(result.AcceptedUnits) != 1 {
		t.Fatalf("expected one unit, got %+v", result)
	}
	unit := result.AcceptedUnits[0]
	if unit.Relation != "follow_up" || unit.ParentTaskKey != "task-parking" || unit.ResolvedTopic != "parking" {
		t.Fatalf("unexpected follow-up relation: %+v", unit)
	}
	if len(unit.InheritedRequirements) != 1 || unit.InheritedRequirements[0].Kind != "parking_fee" {
		t.Fatalf("inherited requirements=%+v", unit.InheritedRequirements)
	}
}

func TestNormalizeRelationNewTopicDoesNotBorrowUnresolvedTopic(t *testing.T) {
	e := relationEnvelope("早餐几点？")
	e.UnresolvedTasks = []contextcompiler.EnvelopeUnresolvedTask{{
		TaskKey: "task-parking", Intent: "hotel_info", SubIntent: "parking", Status: "pending",
	}}
	result := NormalizeIntentTasksWithDialogueAct(e, []IntentTaskV3{relationTask(e, "hotel_info", "breakfast_time", "answer")}, "new_topic")
	unit := result.AcceptedUnits[0]
	if unit.Relation != "new_topic" || unit.ParentTaskKey != "" || unit.ResolvedTopic != "breakfast_time" {
		t.Fatalf("new topic borrowed prior context: %+v", unit)
	}
	if len(unit.InheritedRequirements) != 0 {
		t.Fatalf("new topic inherited requirements: %+v", unit.InheritedRequirements)
	}
}

func TestNormalizeRelationRepeatMatchesUnresolvedQuestionHash(t *testing.T) {
	e := relationEnvelope("停车怎么收费？")
	task := relationTask(e, "hotel_info", "parking", "answer")
	hash := CanonicalQuestionHash(task.Intent, task.SubIntent, []string{e.Utterances[0].Text}, task.RequestMode)
	e.UnresolvedTasks = []contextcompiler.EnvelopeUnresolvedTask{{
		TaskKey: "task-parking", Intent: "hotel_info", SubIntent: "parking", Status: "pending",
		CanonicalQuestionHash: hash,
	}}
	result := NormalizeIntentTasksWithDialogueAct(e, []IntentTaskV3{task}, "repeat")
	unit := result.AcceptedUnits[0]
	if unit.Relation != "repeat" || unit.ParentTaskKey != "task-parking" || unit.ResolvedTopic != "parking" {
		t.Fatalf("unexpected repeat relation: %+v", unit)
	}
}

func TestNormalizeRelationAmbiguousClarificationDoesNotInheritArbitraryTask(t *testing.T) {
	e := relationEnvelope("可以吗？")
	e.UnresolvedTasks = []contextcompiler.EnvelopeUnresolvedTask{
		{TaskKey: "task-parking", Intent: "hotel_info", SubIntent: "parking", Status: "pending"},
		{TaskKey: "task-breakfast", Intent: "hotel_info", SubIntent: "breakfast_time", Status: "pending"},
	}
	task := relationTask(e, "interaction", "clarify", "clarify_previous")
	result := NormalizeIntentTasksWithDialogueAct(e, []IntentTaskV3{task}, "follow_up")
	unit := result.AcceptedUnits[0]
	if unit.Relation != "follow_up" || unit.ParentTaskKey != "" || unit.ResolvedTopic != "" {
		t.Fatalf("ambiguous clarification inherited a topic: %+v", unit)
	}
	if len(unit.InheritedRequirements) != 0 {
		t.Fatalf("ambiguous clarification inherited requirements: %+v", unit.InheritedRequirements)
	}
}

func TestNormalizeRelationRecoversEllipticalNewTopicFromLatestFailedTask(t *testing.T) {
	e := relationEnvelope("送到哪里")
	e.UnresolvedTasks = []contextcompiler.EnvelopeUnresolvedTask{
		{TaskKey: "task-old", SourceMessageID: 1990, SequenceNo: 1, Intent: "hotel_info", SubIntent: "parking", Status: "failed"},
		{TaskKey: "task-takeout", SourceMessageID: 2000, SequenceNo: 1, Intent: "hotel_info", SubIntent: "takeout", Status: "failed", ResolvedTopic: "外卖怎么点"},
	}
	task := relationTask(e, "hotel_info", "delivery_location", "answer")
	result := NormalizeIntentTasksWithDialogueAct(e, []IntentTaskV3{task}, "new_topic")
	if len(result.AcceptedUnits) != 1 {
		t.Fatalf("expected one accepted unit, got %+v", result)
	}
	unit := result.AcceptedUnits[0]
	if unit.Relation != "follow_up" || unit.ParentTaskKey != "task-takeout" || unit.ResolvedTopic != "外卖怎么点" {
		t.Fatalf("elliptical follow-up was not recovered: %+v", unit)
	}
}

func TestNormalizeRelationDoesNotGuessWhenLatestSourceHasMultipleTasks(t *testing.T) {
	e := relationEnvelope("送到哪里")
	e.UnresolvedTasks = []contextcompiler.EnvelopeUnresolvedTask{
		{TaskKey: "task-a", SourceMessageID: 2000, SequenceNo: 1, Intent: "hotel_info", SubIntent: "takeout", Status: "failed"},
		{TaskKey: "task-b", SourceMessageID: 2000, SequenceNo: 2, Intent: "hotel_info", SubIntent: "parking", Status: "failed"},
	}
	unit := NormalizeIntentTasksWithDialogueAct(e, []IntentTaskV3{relationTask(e, "hotel_info", "delivery_location", "answer")}, "new_topic").AcceptedUnits[0]
	if unit.Relation != "new_topic" || unit.ParentTaskKey != "" {
		t.Fatalf("ambiguous same-message context was guessed: %+v", unit)
	}
}
