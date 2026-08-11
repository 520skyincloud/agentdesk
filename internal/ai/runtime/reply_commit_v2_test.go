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

func TestStableTurnClientMsgIDIsOrderIndependentAndVersionScoped(t *testing.T) {
	input := replyCommitInput{
		Conversation: models.Conversation{ID: 22, TenantID: 11},
		Message:      models.Message{AIReplyTurnID: 44, AIReplyTurnVersion: 2},
	}
	first := stableTurnClientMsgID(input, "text", []string{"task_b", "task_a"})
	second := stableTurnClientMsgID(input, "text", []string{"task_a", "task_b", "task_a"})
	if first == "" || first != second {
		t.Fatalf("stable client id first=%q second=%q", first, second)
	}
	input.Message.AIReplyTurnVersion++
	if changed := stableTurnClientMsgID(input, "text", []string{"task_a", "task_b"}); changed == first {
		t.Fatalf("different turn version reused client id %q", changed)
	}
}
