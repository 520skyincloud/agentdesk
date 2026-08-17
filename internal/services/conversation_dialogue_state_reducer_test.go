package services

import (
	"reflect"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestReduceDialogueStateUsesEventTimeForFactExpiry(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	expiresLater := now.Add(time.Minute)
	current := contracts.DialogueStateSnapshotV1{
		SchemaVersion:    contracts.DialogueStateSnapshotV1SchemaVersion,
		ConversationID:   11,
		SessionNo:        2,
		Revision:         3,
		ConversationMode: "ai_serving",
		Focus:            contracts.DialogueStateFocus{RelationToPrior: "unknown", ActiveTaskKeys: []string{}},
		ResolvedTasks:    []contracts.DialogueStateResolvedTask{},
		OpenTasks:        []contracts.DialogueStateOpenTask{},
		SessionFacts: []contracts.DialogueStateSessionFact{{
			Key: "arrival_window", Value: "14:00", ExpiresAt: &expiresLater,
		}},
		UpdatedAt: now.Add(-time.Minute),
	}
	event := DialogueStateEvent{Kind: DialogueStateEventTasksChanged, Now: now}

	first := ReduceDialogueState(current, event)
	second := ReduceDialogueState(current, event)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same event must reduce deterministically:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if len(first.SessionFacts) != 1 {
		t.Fatalf("fact valid at event time was removed: %+v", first.SessionFacts)
	}
}

func TestReduceDialogueStateCustomerCommitOnlyAdvancesMessageEvidence(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	current := contracts.DialogueStateSnapshotV1{
		SchemaVersion:      contracts.DialogueStateSnapshotV1SchemaVersion,
		ConversationID:     11,
		SessionNo:          2,
		Revision:           3,
		BasedOnMessageID:   20,
		BasedOnTurnVersion: 4,
		ConversationMode:   "human_serving",
		Focus: contracts.DialogueStateFocus{
			Topic: "停车", RelationToPrior: "follow_up", ActiveTaskKeys: []string{"task_parking"},
		},
		ResolvedTasks: []contracts.DialogueStateResolvedTask{},
		OpenTasks:     []contracts.DialogueStateOpenTask{{TaskKey: "task_parking", Intent: "hotel_info", SubIntent: "parking", State: "awaiting_human"}},
		SessionFacts:  []contracts.DialogueStateSessionFact{},
		UpdatedAt:     now.Add(-time.Second),
	}

	got := ReduceDialogueState(current, DialogueStateEvent{
		Kind: DialogueStateEventCustomerCommitted, MessageID: 21, TurnVersion: 5,
		DialogueAct: "new_topic", Topic: "咖啡", ConversationMode: "ai_serving", Now: now,
	})

	if got.BasedOnMessageID != 21 || got.BasedOnTurnVersion != 4 {
		t.Fatalf("customer commit advanced unsupported evidence: %+v", got)
	}
	if got.ConversationMode != "human_serving" || !reflect.DeepEqual(got.Focus, current.Focus) || !reflect.DeepEqual(got.OpenTasks, current.OpenTasks) {
		t.Fatalf("customer commit fabricated dialogue semantics: %+v", got)
	}
}

func TestReduceDialogueStateLateTerminalTaskClosesWithoutOverwritingNewerFocus(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	current := contracts.DialogueStateSnapshotV1{
		SchemaVersion:      contracts.DialogueStateSnapshotV1SchemaVersion,
		ConversationID:     11,
		SessionNo:          2,
		Revision:           8,
		BasedOnMessageID:   30,
		BasedOnTurnID:      200,
		BasedOnTurnVersion: 7,
		ConversationMode:   "human_serving",
		Focus:              contracts.DialogueStateFocus{Topic: "人工处理中", RelationToPrior: "unknown", ActiveTaskKeys: []string{"task_old", "task_current"}},
		ResolvedTasks:      []contracts.DialogueStateResolvedTask{},
		OpenTasks: []contracts.DialogueStateOpenTask{
			{TaskKey: "task_old", Intent: "hotel_info", State: "ready_to_generate"},
			{TaskKey: "task_current", Intent: "interaction", SubIntent: "human", State: "awaiting_human"},
		},
		SessionFacts:  []contracts.DialogueStateSessionFact{},
		LastAssistant: &contracts.DialogueStateLastAssistant{MessageID: 30, SenderType: "agent", TaskKeys: []string{"task_current"}},
		UpdatedAt:     now,
	}
	lateAI := &models.Message{ID: 29, SenderType: enums.IMSenderTypeAI}
	got := ReduceDialogueState(current, DialogueStateEvent{
		Kind: DialogueStateEventAssistantCommitted, MessageID: 29, TurnID: 199, TurnVersion: 6,
		ConversationMode: "ai_serving", ResolvedTaskKeys: []string{"task_old"}, AssistantMessage: lateAI, Now: now.Add(time.Second),
	})
	if got.ConversationMode != current.ConversationMode || got.Focus.Topic != current.Focus.Topic ||
		got.LastAssistant.MessageID != current.LastAssistant.MessageID || got.BasedOnTurnID != current.BasedOnTurnID {
		t.Fatalf("late event overwrote newer focus: %+v", got)
	}
	if len(got.OpenTasks) != 1 || got.OpenTasks[0].TaskKey != "task_current" ||
		!reflect.DeepEqual(got.Focus.ActiveTaskKeys, []string{"task_current"}) {
		t.Fatalf("late terminal task did not converge open/active state: %+v", got)
	}
	if len(got.ResolvedTasks) != 1 || got.ResolvedTasks[0].TaskKey != "task_old" {
		t.Fatalf("late terminal task evidence missing: %+v", got.ResolvedTasks)
	}
}

func TestReduceDialogueStateNewTurnVersionOneIsNewerThanPriorTurnVersionSeven(t *testing.T) {
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	current := contracts.DialogueStateSnapshotV1{
		SchemaVersion: contracts.DialogueStateSnapshotV1SchemaVersion, ConversationID: 11, SessionNo: 2, Revision: 8,
		BasedOnMessageID: 30, BasedOnTurnID: 200, BasedOnTurnVersion: 7,
		ConversationMode: "ai_serving",
		Focus:            contracts.DialogueStateFocus{Topic: "旧问题", RelationToPrior: "unknown", ActiveTaskKeys: []string{"task_old"}},
		ResolvedTasks:    []contracts.DialogueStateResolvedTask{},
		OpenTasks:        []contracts.DialogueStateOpenTask{{TaskKey: "task_old", Intent: "hotel_info", State: "ready_to_generate"}},
		SessionFacts:     []contracts.DialogueStateSessionFact{}, UpdatedAt: now.Add(-time.Second),
	}
	got := ReduceDialogueState(current, DialogueStateEvent{
		Kind: DialogueStateEventTasksChanged, MessageID: 31, TurnID: 201, TurnVersion: 1,
		DialogueAct: "new_topic", Topic: "新问题", Now: now,
	})
	if got.BasedOnTurnID != 201 || got.BasedOnTurnVersion != 1 || got.Focus.Topic != "新问题" {
		t.Fatalf("new turn must advance even when its version resets to one: %+v", got)
	}
	if len(got.OpenTasks) != 0 || len(got.Focus.ActiveTaskKeys) != 0 {
		t.Fatalf("new turn retained stale open tasks: %+v", got)
	}
}

func TestReduceDialogueStateNormalizesRefinementToFollowUp(t *testing.T) {
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	current := contracts.DialogueStateSnapshotV1{
		SchemaVersion:  contracts.DialogueStateSnapshotV1SchemaVersion,
		ConversationID: 11,
		SessionNo:      2,
		Revision:       3,
		Focus:          contracts.DialogueStateFocus{RelationToPrior: "unknown", ActiveTaskKeys: []string{}},
		ResolvedTasks:  []contracts.DialogueStateResolvedTask{},
		OpenTasks:      []contracts.DialogueStateOpenTask{},
		SessionFacts:   []contracts.DialogueStateSessionFact{},
		UpdatedAt:      now.Add(-time.Second),
	}

	got := ReduceDialogueState(current, DialogueStateEvent{
		Kind: DialogueStateEventTasksChanged, MessageID: 21, TurnVersion: 4,
		DialogueAct: "refinement", Topic: "升级安静房间", Now: now,
	})
	if got.Focus.RelationToPrior != "follow_up" {
		t.Fatalf("refinement relation=%q want follow_up", got.Focus.RelationToPrior)
	}
}
