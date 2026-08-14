package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

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

func TestNormalizeDegradesWhenAllSpansInvalid(t *testing.T) {
	e := voiceEnvelope()
	tasks := []IntentTaskV3{{
		Sequence: 1, Intent: "x", SubIntent: "y", SourceRefs: []string{"U9"},
		SourceSpans:    []IntentSourceSpan{{SourceRef: "U9", Start: 0, End: 1, Quote: "z"}},
		NormalizedText: "q", RequestMode: "answer",
	}}
	result := NormalizeIntentTasks(e, tasks)
	if result.Status != "degraded_single_task" {
		t.Fatalf("expected degraded_single_task, got %s", result.Status)
	}
	if len(result.AcceptedUnits) != 1 {
		t.Fatalf("expected 1 degraded full-text unit, got %d", len(result.AcceptedUnits))
	}
}
