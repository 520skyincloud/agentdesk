package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type recordingWxWorkProtocolAdapter struct {
	sendCalls int
}

func (a *recordingWxWorkProtocolAdapter) SendMessage(
	_ *dto.WxWorkProtocolChannelConfig,
	_ *models.WxWorkProtocolInstance,
	_ string,
	_ *models.Message,
) (string, error) {
	a.sendCalls++
	return "sent", nil
}

func (a *recordingWxWorkProtocolAdapter) CallDocumented(_ *dto.WxWorkProtocolChannelConfig, _ string, _ map[string]any) (string, error) {
	return "", nil
}

func TestWxWorkProtocolFinalDispatchCheckBlocksClaimCancelledByCorrection(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	question := createAIReplyTurnCustomerMessage(t, db, conversation, "claimed-correction-question", "有咖啡吗", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, question)
	reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "claimed-correction-reply", t0.Add(time.Second))
	task := createCommittedAIReplyTurnTask(t, db, turn, question, reply, "claimed-correction-task", 1, "coffee")
	outbox := createPendingAIReplyOutbox(t, db, conversation, reply, t0)

	claimed, err := ChannelMessageOutboxService.TryMarkSending(outbox.ID, outbox.TenantID)
	if err != nil || !claimed {
		t.Fatalf("claim outbox: claimed=%v err=%v", claimed, err)
	}
	if current := repositories.ChannelMessageOutboxRepository.GetInTenant(db, outbox.ID, outbox.TenantID); current == nil || current.SendStatus != string(enums.ChannelMessageOutboxStatusSending) {
		t.Fatalf("claimed outbox status=%#v", current)
	}

	now := time.Now()
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(ctx.Tx, task.ID, task.TenantID, map[string]any{
			"stage": enums.AIReplyTurnTaskStageComplete, "status": enums.AIReplyTurnTaskStatusSuperseded,
			"result_code": "superseded_by_customer_correction", "updated_at": now,
		}); err != nil {
			return err
		}
		return repositories.ChannelMessageOutboxRepository.UpdatesInTenant(ctx.Tx, outbox.ID, outbox.TenantID, map[string]any{
			"send_status": string(enums.ChannelMessageOutboxStatusCancelled),
			"last_error":  "cancelled_stale_task",
			"updated_at":  now,
		})
	}); err != nil {
		t.Fatalf("cancel claimed outbox for correction: %v", err)
	}

	adapter := &recordingWxWorkProtocolAdapter{}
	svc := &wxWorkProtocolService{adapter: adapter}
	_, attempted, err := svc.sendClaimedOutboxMessage(
		*outbox,
		&dto.WxWorkProtocolChannelConfig{},
		&models.WxWorkProtocolInstance{},
		"S:claimed-correction-customer",
		reply,
	)
	if err != nil {
		t.Fatalf("final dispatch check: %v", err)
	}
	if attempted || adapter.sendCalls != 0 {
		t.Fatalf("cancelled claimed outbox reached protocol adapter: attempted=%v calls=%d", attempted, adapter.sendCalls)
	}
	current := repositories.ChannelMessageOutboxRepository.GetInTenant(db, outbox.ID, outbox.TenantID)
	if current == nil || current.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) || current.LastError != "cancelled_stale_task" {
		t.Fatalf("final dispatch check changed correction cancellation: %#v", current)
	}
}
