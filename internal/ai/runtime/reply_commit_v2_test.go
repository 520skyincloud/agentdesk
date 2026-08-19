package runtime

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	services "agent-desk/internal/services"
)

func TestAuthoritativeActionLedgerNeverFallsBackToTraceResources(t *testing.T) {
	trace := &aiReplyTraceData{Runtime: []byte(`{"pipeline":{"intent":{"needsResource":true,"resourceActions":["location"]}}}`)}
	_, err := newReplyCommitService().SendAIReplyBatch(replyCommitInput{
		Conversation: models.Conversation{ID: 22, TenantID: 11},
		Message:      models.Message{ID: 33, TenantID: 11, ConversationID: 22, AIReplyTurnID: 44, AIReplyTurnVersion: 1},
		ActionLedgerV2: &contracts.ActionLedgerV1{
			SchemaVersion: contracts.ActionLedgerV1SchemaVersion, TurnVersion: 1, Actions: []contracts.ActionLedgerItemV1{},
		},
		ActionLedgerAuthoritative: true,
		Trace:                     trace,
	})
	code, ok := services.AIReplyExecutionErrorCodeOf(err)
	if !ok || code != services.AIReplyExecutionErrorEmptyOutput {
		t.Fatalf("authoritative ledger used trace fallback, err=%v code=%q", err, code)
	}
}

func TestStableTurnClientMsgIDUsesTurnAndTaskIdentity(t *testing.T) {
	input := replyCommitInput{
		Conversation: models.Conversation{ID: 22, TenantID: 11},
		Message:      models.Message{AIReplyTurnID: 44, AIReplyTurnVersion: 2},
	}
	first := stableTurnClientMsgID(input, "text", 1, []string{"task_b", "task_a"})
	second := stableTurnClientMsgID(input, "text", 1, []string{"task_a", "task_b", "task_a"})
	if first == "" || first != second {
		t.Fatalf("stable client id first=%q second=%q", first, second)
	}
	input.Message.AIReplyTurnVersion++
	if same := stableTurnClientMsgID(input, "text", 1, []string{"task_a", "task_b"}); same != first {
		t.Fatalf("turn version advance changed stable client id first=%q same=%q", first, same)
	}
	input.Message.AIReplyTurnID++
	if changed := stableTurnClientMsgID(input, "text", 1, []string{"task_a", "task_b"}); changed == first {
		t.Fatalf("different turn reused client id %q", changed)
	}
	input.Message.AIReplyTurnID--
	if changed := stableTurnClientMsgID(input, "text", 1, []string{"task_a", "task_c"}); changed == first {
		t.Fatalf("different task identity reused client id %q", changed)
	}
	if changed := stableTurnClientMsgID(input, "text", 2, []string{"task_a", "task_b"}); changed == first {
		t.Fatalf("different reply part reused client id %q", changed)
	}
}
