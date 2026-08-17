package executor

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/models"
)

// Schema 和 Go Wire 必须使用同一字段名。生产模型按 Schema 输出
// normalizedText 时，DisallowUnknownFields 不得把合法结果拒绝掉。
func TestParseIntentTasksV3AcceptsSchemaNormalizedText(t *testing.T) {
	content := fmt.Sprintf(`{
  "schemaVersion": "intent_tasks.v3",
  "dialogueAct": "new_topic",
  "utteranceCoverage": [{
    "sourceRef": "U1",
    "status": "covered",
    "taskSequences": [1],
    "ignoredReason": ""
  }],
  "tasks": [{
    "sequence": 1,
    "intent": "hotel_info",
    "subIntent": "coffee",
    "sourceRefs": ["U1"],
    "sourceSpans": [{"sourceRef": "U1", "start": 0, "end": 4, "quote": "有咖啡吗"}],
    "dependsOnObservationRefs": [],
    "normalizedText": "有咖啡吗",
    "answerRequirements": [{"sequence": 1, "kind": "answer_question", "required": true}],
    "requestMode": "answer",
    "confidence": 0.98
  }]
}`)
	parsed, err := parseIntentTasksV3Wire(content)
	if err != nil {
		t.Fatalf("schema-compliant normalizedText rejected: %v", err)
	}
	if len(parsed.Tasks) != 1 || parsed.Tasks[0].Normalized != "有咖啡吗" {
		t.Fatalf("normalized text not decoded: %+v", parsed.Tasks)
	}
}

func v3EnvelopeFixture() contextcompiler.TurnInputEnvelope {
	return contextcompiler.BuildTurnInputEnvelope(contextcompiler.EnvelopeScope{TenantID: 1, StoreID: 1, ConversationID: 2}, []models.Message{
		{ID: 1399, SenderType: "customer", MessageType: "voice", Content: "",
			Payload: `{"mediaUnderstandingStatus":"understood","mediaText":"有没有刮胡刀，还有咖啡和其他用品？"}`},
		{ID: 1400, SenderType: "customer", MessageType: "voice", Content: "",
			Payload: `{"mediaUnderstandingStatus":"understood","mediaText":"床单想换怎么办"}`},
	})
}

// 契约 10.7：每个非空 URef 必须在 utteranceCoverage 恰好出现一次。
func TestV3UtteranceCoverageSetEquality(t *testing.T) {
	envelope := v3EnvelopeFixture()
	tasks := []intentTaskV3Wire{
		{Sequence: 1, SourceRefs: []string{"U1"}, SourceSpans: []intentSpanWire{{SourceRef: "U1", Start: 0, End: 4, Quote: "有没有刮"}}},
		{Sequence: 2, SourceRefs: []string{"U2"}, SourceSpans: []intentSpanWire{{SourceRef: "U2", Start: 0, End: 4, Quote: "床单想换"}}},
	}
	good := []intentCoverageItemWire{
		{SourceRef: "U1", Status: "covered", TaskSequences: []int{1}},
		{SourceRef: "U2", Status: "covered", TaskSequences: []int{2}},
	}
	if issues := validateV3UtteranceCoverage(envelope, good, tasks); len(issues) != 0 {
		t.Fatalf("valid coverage rejected: %v", issues)
	}
	missingU2 := good[:1]
	if issues := validateV3UtteranceCoverage(envelope, missingU2, tasks); len(issues) == 0 {
		t.Fatal("missing U2 coverage must be rejected (1399/1400 串线场景)")
	}
	dup := append(append([]intentCoverageItemWire{}, good...), intentCoverageItemWire{SourceRef: "U1", Status: "ignored", IgnoredReason: "policy_no_reply"})
	if issues := validateV3UtteranceCoverage(envelope, dup, tasks); len(issues) == 0 {
		t.Fatal("duplicate coverage must be rejected")
	}
}

func TestV3UtteranceCoverageRejectsDanglingTaskSequence(t *testing.T) {
	envelope := v3EnvelopeFixture()
	tasks := []intentTaskV3Wire{{
		Sequence: 1, SourceRefs: []string{"U1"},
		SourceSpans: []intentSpanWire{{SourceRef: "U1", Start: 0, End: 4, Quote: "有没有刮"}},
	}}
	coverage := []intentCoverageItemWire{
		{SourceRef: "U1", Status: "covered", TaskSequences: []int{2}},
		{SourceRef: "U2", Status: "ignored", IgnoredReason: "policy_no_reply"},
	}
	if issues := validateV3UtteranceCoverage(envelope, coverage, tasks); len(issues) == 0 {
		t.Fatal("coverage must reject a task sequence that does not exist")
	}
}

func TestV3UtteranceCoverageOnlyIgnoresExactDuplicate(t *testing.T) {
	envelope := contextcompiler.BuildTurnInputEnvelope(contextcompiler.EnvelopeScope{}, []models.Message{
		{ID: 1, SenderType: "customer", MessageType: "text", Content: "有咖啡吗？"},
		{ID: 2, SenderType: "customer", MessageType: "text", Content: " 有咖啡吗。 "},
		{ID: 3, SenderType: "customer", MessageType: "text", Content: "好无聊啊"},
	})
	tasks := []intentTaskV3Wire{{
		Sequence: 1, SourceRefs: []string{"U1"},
		SourceSpans: []intentSpanWire{{SourceRef: "U1", Start: 0, End: 5, Quote: "有咖啡吗？"}},
	}}
	valid := []intentCoverageItemWire{
		{SourceRef: "U1", Status: "covered", TaskSequences: []int{1}},
		{SourceRef: "U2", Status: "ignored", IgnoredReason: "duplicate_equivalent"},
		{SourceRef: "U3", Status: "covered", TaskSequences: []int{2}},
	}
	tasks = append(tasks, intentTaskV3Wire{
		Sequence: 2, SourceRefs: []string{"U3"},
		SourceSpans: []intentSpanWire{{SourceRef: "U3", Start: 0, End: 4, Quote: "好无聊啊"}},
	})
	if issues := validateV3UtteranceCoverage(envelope, valid, tasks); len(issues) != 0 {
		t.Fatalf("exact duplicate coverage rejected: %v", issues)
	}
	invalid := append([]intentCoverageItemWire(nil), valid...)
	invalid[2] = intentCoverageItemWire{SourceRef: "U3", Status: "ignored", IgnoredReason: "duplicate_equivalent"}
	if issues := validateV3UtteranceCoverage(envelope, invalid, tasks[:1]); len(issues) == 0 {
		t.Fatal("social input without an equivalent covered utterance must not be ignored")
	}
}

func TestV3SemanticFragmentCoverageRejectsPartiallyCoveredVoice(t *testing.T) {
	envelope := contextcompiler.TurnInputEnvelope{Utterances: []contextcompiler.EnvelopeUtterance{{
		Ref: "U1", MessageID: 1452, MessageType: "voice", Text: productionMessage1452Transcript,
	}}}
	coveredFragments := []string{
		"这附近有附近有什么地方好玩儿的呀，什么景点啊，好吃的之类的有没有啊？",
		"我就换个安静点的房间，别帮我换了吧，你就说有没有安静的房间吧。",
		"好困，能不能搞点咖啡来呀？",
	}
	tasks := make([]intentTaskV3Wire, 0, len(coveredFragments))
	for index, fragment := range coveredFragments {
		span := spanFor(envelope, fragment)
		tasks = append(tasks, intentTaskV3Wire{
			Sequence: index + 1, SourceRefs: []string{"U1"},
			SourceSpans: []intentSpanWire{{SourceRef: span.SourceRef, Start: span.Start, End: span.End, Quote: span.Quote}},
		})
	}
	coverage := []intentCoverageItemWire{{SourceRef: "U1", Status: "covered", TaskSequences: []int{1, 2, 3}}}
	issues := validateV3SemanticFragmentCoverage(envelope, coverage, tasks)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " "), "最后告诉我有什么酒店什么") {
		t.Fatalf("missing production voice fragment must be rejected: %v", issues)
	}

	missing := "最后告诉我有什么酒店什么。"
	span := spanFor(envelope, missing)
	tasks = append(tasks, intentTaskV3Wire{
		Sequence: 4, SourceRefs: []string{"U1"},
		SourceSpans: []intentSpanWire{{SourceRef: span.SourceRef, Start: span.Start, End: span.End, Quote: span.Quote}},
	})
	coverage[0].TaskSequences = append(coverage[0].TaskSequences, 4)
	if issues := validateV3SemanticFragmentCoverage(envelope, coverage, tasks); len(issues) != 0 {
		t.Fatalf("complete production voice coverage rejected: %v", issues)
	}
}

func TestParseIntentTasksV3RejectsArbitraryIgnoredReason(t *testing.T) {
	content := `{
	  "schemaVersion":"intent_tasks.v3",
	  "dialogueAct":"new_topic",
	  "utteranceCoverage":[{"sourceRef":"U1","status":"ignored","taskSequences":[],"ignoredReason":"policy_no_reply"}],
	  "tasks":[{
	    "sequence":1,"intent":"interaction","subIntent":"social","sourceRefs":["U1"],
	    "sourceSpans":[{"sourceRef":"U1","start":0,"end":2,"quote":"哈哈"}],
	    "dependsOnObservationRefs":[],"normalizedText":"哈哈",
	    "answerRequirements":[{"sequence":1,"kind":"social_reply","required":true}],
	    "requestMode":"social","confidence":0.9
	  }]
	}`
	if _, err := parseIntentTasksV3Wire(content); err == nil {
		t.Fatal("schema must reject arbitrary ignored reasons")
	}
}

func TestSplitIntentV3EnvelopeBatchesAndKeepsAdjacentMedia(t *testing.T) {
	messages := make([]models.Message, 0, 13)
	for id := int64(1); id <= 11; id++ {
		messages = append(messages, models.Message{ID: id, SenderType: "customer", MessageType: "text", Content: fmt.Sprintf("问题%d", id)})
	}
	messages = append(messages,
		models.Message{ID: 12, SenderType: "customer", MessageType: "image", Content: "room.jpg", Payload: `{"mediaText":"房型截图","mediaUnderstandingStatus":"understood"}`},
		models.Message{ID: 13, SenderType: "customer", MessageType: "text", Content: "这张图能升级吗"},
	)
	envelope := contextcompiler.BuildTurnInputEnvelope(contextcompiler.EnvelopeScope{}, messages)
	batches := splitIntentV3Envelope(envelope, 12)
	if len(batches) != 2 || len(batches[0].Utterances) != 11 || len(batches[1].Utterances) != 2 {
		t.Fatalf("unexpected batches: %#v", batches)
	}
	if batches[1].Utterances[0].MessageID != 12 || batches[1].Utterances[1].MessageID != 13 {
		t.Fatalf("adjacent image and question were split: %#v", batches[1].Utterances)
	}
	if len(batches[1].Observations) != 1 || batches[1].Observations[0].MessageID != 12 {
		t.Fatalf("image observation missing from question batch: %#v", batches[1].Observations)
	}
}

func TestMergeIntentV3BatchOutputsRenumbersTasksAndCoverage(t *testing.T) {
	merged := mergeIntentV3BatchOutputs([]intentTasksV3Wire{
		{SchemaVersion: "intent_tasks.v3", DialogueAct: "new_topic",
			Tasks:             []intentTaskV3Wire{{Sequence: 1}},
			UtteranceCoverage: []intentCoverageItemWire{{SourceRef: "U1", Status: "covered", TaskSequences: []int{1}}}},
		{SchemaVersion: "intent_tasks.v3", DialogueAct: "follow_up",
			Tasks:             []intentTaskV3Wire{{Sequence: 1}, {Sequence: 2}},
			UtteranceCoverage: []intentCoverageItemWire{{SourceRef: "U13", Status: "covered", TaskSequences: []int{1, 2}}}},
	})
	if len(merged.Tasks) != 3 || merged.Tasks[1].Sequence != 2 || merged.Tasks[2].Sequence != 3 {
		t.Fatalf("task sequences not renumbered: %#v", merged.Tasks)
	}
	got := merged.UtteranceCoverage[1].TaskSequences
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("coverage sequences not renumbered: %#v", got)
	}
}

// 契约 10.5：协议失败时按真实标点片段降级，不复制整段语音，也不转人工。
func TestV3DegradePerUtterance(t *testing.T) {
	envelope := v3EnvelopeFixture()
	trace, err := degradeIntentV3(envelope, intentTasksV3Wire{}, nil, nil)
	if err != nil {
		t.Fatalf("degrade must not fail: %v", err)
	}
	if !strings.Contains(trace.Reason, "intent_tasks.v3") {
		t.Fatalf("degraded trace reason: %q", trace.Reason)
	}
	if len(trace.IntentTasks) != 3 {
		t.Fatalf("expected one task per punctuation-delimited source fragment, got %d", len(trace.IntentTasks))
	}
	want := []string{"有没有刮胡刀，", "还有咖啡和其他用品？", "床单想换怎么办"}
	for index, task := range trace.IntentTasks {
		if task.Text != want[index] {
			t.Fatalf("degraded task %d text=%q want %q", index+1, task.Text, want[index])
		}
		if task.Text == envelope.Utterances[0].Text && len(want) > 1 {
			t.Fatalf("degraded task copied a complete multi-question utterance: %+v", task)
		}
	}
	if trace.PrimaryIntent != "interaction" || trace.NeedsKnowledge || trace.NeedsHumanRoute {
		t.Fatalf("protocol degradation must clarify without knowledge or handoff: %#v", trace)
	}
}

func TestV3ProtocolDegradeSplitsOneMultiQuestionVoiceWithoutFullTextCopies(t *testing.T) {
	envelope := contextcompiler.TurnInputEnvelope{
		Utterances: []contextcompiler.EnvelopeUtterance{{
			Ref: "U1", MessageID: 1452, MessageType: "voice", Text: productionMessage1452Transcript,
		}},
	}
	fallback, err := degradeIntentV3Wire(envelope)
	if err != nil {
		t.Fatalf("degradeIntentV3Wire() error = %v", err)
	}
	if len(fallback.Tasks) != 9 || len(fallback.UtteranceCoverage) != 1 {
		t.Fatalf("unexpected fallback shape: tasks=%d coverage=%+v", len(fallback.Tasks), fallback.UtteranceCoverage)
	}
	if got := fallback.UtteranceCoverage[0].TaskSequences; !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}) {
		t.Fatalf("coverage must bind all clause tasks, got %v", got)
	}
	for _, task := range fallback.Tasks {
		if len(task.SourceSpans) != 1 || task.SourceSpans[0].Quote == productionMessage1452Transcript {
			t.Fatalf("fallback task copied the full transcript: %+v", task)
		}
	}
}

// 契约 2.1：成组开关只允许整组（Intent V3 + Context V2）。
func TestV3GroupFlagForcesIntentContract(t *testing.T) {
	t.Setenv("AI_RUNTIME_MULTIMODAL_V3", "on")
	t.Setenv("AI_RUNTIME_INTENT_CONTRACT", "v2")
	resolved := resolveRuntimeFeatureModes(RunInput{})
	if resolved.IntentContract != runtimeIntentContractV3 {
		t.Fatalf("group flag must force intent v3, got %s", resolved.IntentContract)
	}
	if resolved.ContextCompiler != runtimeContextCompilerV2 {
		t.Fatalf("group flag must force context v2, got %s", resolved.ContextCompiler)
	}
}

func TestNormalizeIntentTasksForDialogueActPerTask(t *testing.T) {
	tasks := []IntentTaskV3{
		{Sequence: 1, Intent: "hotel_info", SubIntent: "store_knowledge", RequestMode: "answer", SourceSpans: []IntentSourceSpan{{Quote: "你好"}}},
		{Sequence: 2, Intent: "hotel_info", SubIntent: "checkin_process", RequestMode: "answer", SourceSpans: []IntentSourceSpan{{Quote: "给我办个入住"}}},
	}
	got := normalizeIntentTasksForDialogueAct("greeting", tasks)
	if got[0].Intent != "interaction" || got[0].SubIntent != "greeting" || got[0].RequestMode != "social" {
		t.Fatalf("greeting task was not corrected: %#v", got[0])
	}
	if got[1].Intent != "hotel_info" || got[1].SubIntent != "checkin_process" || got[1].RequestMode != "answer" {
		t.Fatalf("business task was overwritten by global dialogue act: %#v", got[1])
	}
}

func TestNormalizeIntentTasksForDialogueActCorrectsSocialAndAmbiguousTasks(t *testing.T) {
	tasks := []IntentTaskV3{
		{Sequence: 1, Intent: "hotel_info", SubIntent: "store_knowledge", RequestMode: "answer", SourceSpans: []IntentSourceSpan{{Quote: "好无聊啊"}}},
		{Sequence: 2, Intent: "hotel_info", SubIntent: "store_knowledge", RequestMode: "answer", SourceSpans: []IntentSourceSpan{{Quote: "怎么说"}}},
	}
	got := normalizeIntentTasksForDialogueAct("new_topic", tasks)
	if got[0].Intent != "interaction" || got[0].RequestMode != "social" {
		t.Fatalf("social task was not corrected: %#v", got[0])
	}
	if got[1].Intent != "interaction" || got[1].SubIntent != "clarify" || got[1].RequestMode != "clarify_previous" {
		t.Fatalf("ambiguous task was not corrected: %#v", got[1])
	}
}

func TestNormalizeIntentTasksForDialogueActUsesGeneralNonBusinessClasses(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		dialogueAct string
		wantSub     string
		wantMode    string
	}{
		{name: "emoji", text: "🤪🤪🤪", dialogueAct: "new_topic", wantSub: "social", wantMode: "social"},
		{name: "full width question", text: "？！", dialogueAct: "unknown", wantSub: "clarify", wantMode: "clarify_previous"},
		{name: "repeated vocalization", text: "咦嘻嘻", dialogueAct: "new_topic", wantSub: "social", wantMode: "social"},
		{name: "incomplete reduplicated fragment", text: "一丝丝", dialogueAct: "unknown", wantSub: "clarify", wantMode: "clarify_previous"},
		{name: "model level social", text: "随便聊聊", dialogueAct: "social", wantSub: "social", wantMode: "social"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tasks := []IntentTaskV3{{
				Sequence: 1, Intent: "hotel_info", SubIntent: "store_knowledge", RequestMode: "answer",
				SourceSpans: []IntentSourceSpan{{Quote: tt.text}},
			}}
			got := normalizeIntentTasksForDialogueAct(tt.dialogueAct, tasks)
			if got[0].Intent != "interaction" || got[0].SubIntent != tt.wantSub || got[0].RequestMode != tt.wantMode {
				t.Fatalf("normalized task=%#v", got[0])
			}
		})
	}
}
