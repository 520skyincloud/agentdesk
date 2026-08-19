package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestIntentV2ClauseCoverageKeepsEverySocialAndUnclearVoiceClause(t *testing.T) {
	text := "你是谁呀？我是谁呀？你知道李白吗？我想带s go了"
	scope := intentV2ClauseCoverageTestScope(enums.IMMessageTypeVoice, text)
	parsed := contracts.IntentTasksV2{
		SchemaVersion: contracts.IntentTasksV2SchemaVersion,
		DialogueAct:   "new_topic",
		Tasks: []contracts.IntentTaskV2{{
			Sequence: 1, Intent: "interaction", SubIntent: "identity", Text: "你是谁呀",
			RequestMode: "social", Confidence: 0.9, SourceRefs: []string{"U1"},
		}},
	}
	if err := validateIntentV2ClauseCoverage(parsed, scope); err == nil {
		t.Fatal("one task must not cover four independent voice clauses")
	} else if invariant, ok := runtimeIntentInvariantDetails(err); !ok || invariant.Code != intentInvariantSourceClauseMissing {
		t.Fatalf("unexpected coverage error: %v", err)
	}

	completed, derived, err := completeIntentV2ClauseCoverage(parsed, scope, intentV2ClauseCoverageTestConfigs())
	if err != nil {
		t.Fatalf("complete clause coverage: %v", err)
	}
	want := []string{"你是谁呀", "我是谁呀", "你知道李白吗", "我想带s go了"}
	if len(completed.Tasks) != len(want) || len(derived) != len(want) {
		t.Fatalf("tasks=%d derived=%d want=%d: %#v", len(completed.Tasks), len(derived), len(want), completed.Tasks)
	}
	for index, task := range completed.Tasks {
		if task.Text != want[index] || task.Sequence != index+1 {
			t.Fatalf("task[%d]=%#v want text=%q sequence=%d", index, task, want[index], index+1)
		}
		if task.Intent != "interaction" || task.RequestMode != "social" {
			t.Fatalf("social voice clause was routed as hotel knowledge: %#v", task)
		}
		if len(task.SourceMessageIDs) != 1 || task.SourceMessageIDs[0] != 2307 {
			t.Fatalf("voice source binding missing: %#v", task)
		}
	}
}

func TestIntentV2ClauseCoverageRejectsAggregateInteractionTask(t *testing.T) {
	text := "你是谁呀？我是谁呀？你知道李白吗？我想带s go了"
	scope := intentV2ClauseCoverageTestScope(enums.IMMessageTypeVoice, text)
	parsed := contracts.IntentTasksV2{SchemaVersion: contracts.IntentTasksV2SchemaVersion, DialogueAct: "new_topic", Tasks: []contracts.IntentTaskV2{{
		Sequence: 1, Intent: "interaction", SubIntent: "social", Text: text, RequestMode: "social", Confidence: 0.9,
		SourceRefs: []string{"U1"},
	}}}
	if err := validateIntentV2ClauseCoverage(parsed, scope); err == nil {
		t.Fatal("one aggregate interaction task must not masquerade as four covered questions")
	}
	completed, _, err := completeIntentV2ClauseCoverage(parsed, scope, intentV2ClauseCoverageTestConfigs())
	if err != nil {
		t.Fatalf("split aggregate interaction task: %v", err)
	}
	want := []string{"你是谁呀", "我是谁呀", "你知道李白吗", "我想带s go了"}
	if len(completed.Tasks) != len(want) {
		t.Fatalf("completed tasks=%d want=%d: %#v", len(completed.Tasks), len(want), completed.Tasks)
	}
	for index, task := range completed.Tasks {
		if task.Text != want[index] {
			t.Fatalf("task[%d]=%#v want text=%q", index, task, want[index])
		}
	}
}

func TestIntentV2ClauseCoverageCompletesSixQuestionsForTextAndVoice(t *testing.T) {
	text := "附近哪里吃饭？附近有什么好玩的？入住后怎么开门？停车场能充电吗？发票怎么开？退房时间是几点？"
	want := []string{"附近哪里吃饭", "附近有什么好玩的", "入住后怎么开门", "停车场能充电吗", "发票怎么开", "退房时间是几点"}
	for _, messageType := range []enums.IMMessageType{enums.IMMessageTypeText, enums.IMMessageTypeVoice} {
		t.Run(string(messageType), func(t *testing.T) {
			scope := intentV2ClauseCoverageTestScope(messageType, text)
			parsed := contracts.IntentTasksV2{
				SchemaVersion: contracts.IntentTasksV2SchemaVersion,
				DialogueAct:   "new_topic",
				Tasks: []contracts.IntentTaskV2{{
					Sequence: 1, Intent: "hotel_info", SubIntent: "surrounding_facilities", Text: want[0],
					RequestMode: "answer", Confidence: 0.9, SourceRefs: []string{"U1"},
				}},
			}
			completed, derived, err := completeIntentV2ClauseCoverage(parsed, scope, intentV2ClauseCoverageTestConfigs())
			if err != nil {
				t.Fatalf("complete %s clauses: %v", messageType, err)
			}
			if len(completed.Tasks) != len(want) || len(derived) != len(want) {
				t.Fatalf("%s tasks=%d derived=%d want=%d: %#v", messageType, len(completed.Tasks), len(derived), len(want), completed.Tasks)
			}
			for index, task := range completed.Tasks {
				if task.Text != want[index] || task.Sequence != index+1 {
					t.Fatalf("%s task[%d]=%#v want text=%q sequence=%d", messageType, index, task, want[index], index+1)
				}
				if task.Intent != "hotel_info" || !derived[index].NeedsKnowledge {
					t.Fatalf("%s question did not remain a knowledge task: task=%#v derived=%#v", messageType, task, derived[index])
				}
			}
		})
	}
}

func TestIntentV2ClauseCoverageRejectsAggregateHotelInfoTaskForTextAndVoice(t *testing.T) {
	text := "附近哪里吃饭？附近有什么好玩的？入住后怎么开门？停车场能充电吗？发票怎么开？退房时间是几点？"
	want := []string{"附近哪里吃饭", "附近有什么好玩的", "入住后怎么开门", "停车场能充电吗", "发票怎么开", "退房时间是几点"}
	for _, messageType := range []enums.IMMessageType{enums.IMMessageTypeText, enums.IMMessageTypeVoice} {
		t.Run(string(messageType), func(t *testing.T) {
			scope := intentV2ClauseCoverageTestScope(messageType, text)
			parsed := contracts.IntentTasksV2{SchemaVersion: contracts.IntentTasksV2SchemaVersion, DialogueAct: "new_topic", Tasks: []contracts.IntentTaskV2{{
				Sequence: 1, Intent: "hotel_info", SubIntent: "surrounding_facilities", Text: text,
				RequestMode: "answer", Confidence: 0.9, SourceRefs: []string{"U1"},
			}}}
			if err := validateIntentV2ClauseCoverage(parsed, scope); err == nil {
				t.Fatal("one aggregate hotel_info task must not cover six independent questions")
			}

			completed, derived, err := completeIntentV2ClauseCoverage(parsed, scope, intentV2ClauseCoverageTestConfigs())
			if err != nil {
				t.Fatalf("complete aggregate %s task: %v", messageType, err)
			}
			if len(completed.Tasks) != len(want) || len(derived) != len(want) {
				t.Fatalf("%s tasks=%d derived=%d want=%d: %#v", messageType, len(completed.Tasks), len(derived), len(want), completed.Tasks)
			}
			for index, task := range completed.Tasks {
				if task.Text != want[index] || task.Sequence != index+1 {
					t.Fatalf("%s task[%d]=%#v want text=%q sequence=%d", messageType, index, task, want[index], index+1)
				}
				if task.Intent != "hotel_info" || !derived[index].NeedsKnowledge {
					t.Fatalf("%s aggregate question was not rebuilt as knowledge task: task=%#v derived=%#v", messageType, task, derived[index])
				}
			}
		})
	}
}

func TestIntentV2ClauseCoverageDoesNotSplitAdjacentContextMessage(t *testing.T) {
	envelope := contextcompiler.BuildTurnInputEnvelope(contextcompiler.EnvelopeScope{ConversationID: 7, SessionNo: 1}, []models.Message{
		{ID: 61, ConversationID: 7, SessionNo: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "好困啊"},
		{ID: 62, ConversationID: 7, SessionNo: 1, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "有没有咖啡"},
	})
	scope := intentV2SourceScope{Envelope: envelope, RequiredRefs: map[string]struct{}{"U1": {}, "U2": {}}}
	parsed := contracts.IntentTasksV2{SchemaVersion: contracts.IntentTasksV2SchemaVersion, DialogueAct: "new_topic", Tasks: []contracts.IntentTaskV2{{
		Sequence: 1, Intent: "hotel_info", SubIntent: "coffee", Text: "有没有咖啡", RequestMode: "answer", Confidence: 0.9,
		SourceRefs: []string{"U2", "U1"},
	}}}
	if err := validateIntentV2ClauseCoverage(parsed, scope); err != nil {
		t.Fatalf("adjacent context must stay bound to the coffee task: %v", err)
	}
}

func TestIntentV2ClauseCoverageAllowsOneTaskToCoverSameTopicDimensions(t *testing.T) {
	text := "早餐几点？在哪里？"
	scope := intentV2ClauseCoverageTestScope(enums.IMMessageTypeText, text)
	for _, taskText := range []string{"早餐时间和地点", text} {
		parsed := contracts.IntentTasksV2{SchemaVersion: contracts.IntentTasksV2SchemaVersion, DialogueAct: "new_topic", Tasks: []contracts.IntentTaskV2{{
			Sequence: 1, Intent: "hotel_info", SubIntent: "breakfast", Text: taskText, RequestMode: "answer", Confidence: 0.9,
			SourceRefs: []string{"U1"},
		}}}
		if err := validateIntentV2ClauseCoverage(parsed, scope); err != nil {
			t.Fatalf("one breakfast task %q may cover its time and location dimensions: %v", taskText, err)
		}
	}
}

func TestIntentV2ClauseCoverageDoesNotPromoteLifeBackgroundBesideHotelQuestion(t *testing.T) {
	for _, text := range []string{"我想明天出门。停车场在哪里？", "我明天入住。停车场在哪里？"} {
		scope := intentV2ClauseCoverageTestScope(enums.IMMessageTypeText, text)
		parsed := contracts.IntentTasksV2{SchemaVersion: contracts.IntentTasksV2SchemaVersion, DialogueAct: "new_topic", Tasks: []contracts.IntentTaskV2{{
			Sequence: 1, Intent: "hotel_info", SubIntent: "parking", Text: "停车场在哪里", RequestMode: "answer", Confidence: 0.9,
			SourceRefs: []string{"U1"},
		}}}
		if err := validateIntentV2ClauseCoverage(parsed, scope); err != nil {
			t.Fatalf("life background must not become a second task for %q: %v", text, err)
		}
	}
}

func TestConditionalKnowledgeRestoresConcreteClarifyPreviousTopic(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent: "interaction", SubIntent: "clarify", ShouldReply: true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Sequence: 1, Intent: "interaction", SubIntent: "parking", RequestMode: "clarify_previous", Text: "停车场在哪里",
		}},
	}
	marked := markConditionalKnowledgeTasksForFormalRetrieval(intent, "停车场在哪里")
	if !marked.NeedsKnowledge || len(marked.IntentTasks) != 1 || !marked.IntentTasks[0].NeedsKnowledge {
		t.Fatalf("concrete clarify_previous topic must restore formal knowledge retrieval: %#v", marked)
	}
	if marked.IntentTasks[0].SubIntent != "parking" {
		t.Fatalf("concrete business subIntent was lost: %#v", marked.IntentTasks[0])
	}
	plans := buildReplyTaskPlans(marked)
	if len(plans) != 1 || plans[0].Output != "knowledge_text_reply" {
		t.Fatalf("concrete clarification did not become a knowledge task: %#v", plans)
	}
}

func intentV2ClauseCoverageTestScope(messageType enums.IMMessageType, text string) intentV2SourceScope {
	message := models.Message{
		ID: 2307, ConversationID: 7, SessionNo: 1, SenderType: enums.IMSenderTypeCustomer,
		MessageType: messageType, Content: text,
	}
	if messageType == enums.IMMessageTypeVoice {
		message.Content = "voice.amr"
		message.Payload = `{"mediaText":"` + text + `","mediaUnderstandingStatus":"understood"}`
	}
	envelope := contextcompiler.BuildTurnInputEnvelope(contextcompiler.EnvelopeScope{ConversationID: 7, SessionNo: 1}, []models.Message{message})
	return intentV2SourceScope{Envelope: envelope, Messages: []models.Message{message}, RequiredRefs: map[string]struct{}{"U1": {}}}
}

func intentV2ClauseCoverageTestConfigs() []models.ReplyIntentConfig {
	return []models.ReplyIntentConfig{
		{ID: 1, Code: "hotel_info", NeedsKnowledge: true, Status: enums.StatusOk},
		{ID: 2, Code: "interaction", Status: enums.StatusOk},
	}
}
