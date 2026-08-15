package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// 契约 16.3 回放：客户取消转人工后，同一 Handoff 的 handoff_pending、
// handoff 任务与派生 pending/running 任务必须在同一事务中闭合。
func TestCancelHandoffTransactionClosesWholeTaskSet(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	now := time.Now().Add(-time.Minute)
	origin := createAIReplyTurnCustomerMessage(t, db, conversation, "cancel-origin", "给我升大床房", now)
	turn := assignAIReplyTurnMessage(t, db, conversation, origin)

	mk := func(seq int, key string, status enums.AIReplyTurnTaskStatus) models.AIReplyTurnTask {
		return models.AIReplyTurnTask{
			TenantID: turn.TenantID, ConversationID: turn.ConversationID, SessionNo: turn.SessionNo,
			TurnID: turn.ID, IntroducedVersion: turn.Version, SourceMessageID: origin.ID,
			TaskKey: key, SequenceNo: seq,
			TaskType: enums.AIReplyTurnTaskTypeHuman, Stage: enums.AIReplyTurnTaskStageHandoff,
			Status: status, KnowledgeStatus: enums.AIReplyTurnTaskKnowledgeStatusNone,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now, CreateUserName: "test", UpdateUserName: "test"},
		}
	}
	tasks := []models.AIReplyTurnTask{
		mk(1, "cancel_task_a", enums.AIReplyTurnTaskStatusHandoffPending),
		mk(2, "cancel_task_b", enums.AIReplyTurnTaskStatusHandoff),
		mk(3, "cancel_task_c", enums.AIReplyTurnTaskStatusPending),
		mk(4, "cancel_task_d", enums.AIReplyTurnTaskStatusRunning),
		mk(5, "cancel_task_e", enums.AIReplyTurnTaskStatusDelivered),
	}
	for index := range tasks {
		tasks[index].ID = int64(index + 1)
		if err := db.Create(&tasks[index]).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return AIReplyTurnTaskService.CancelHandoffTransactionDB(ctx.Tx, turn.TenantID, turn.ConversationID, origin.ID,
			[]string{"cancel_task_a", "cancel_task_b"}, time.Now())
	}); err != nil {
		t.Fatal(err)
	}

	final := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(db, turn.TenantID, turn.ID)
	byKey := map[string]models.AIReplyTurnTask{}
	for _, task := range final {
		byKey[task.TaskKey] = task
	}
	if got := byKey["cancel_task_a"]; got.Status != enums.AIReplyTurnTaskStatusSkipped || got.ResultCode != "human_handoff_cancelled" {
		t.Fatalf("handoff_pending task must be cancelled: %+v", got)
	}
	if got := byKey["cancel_task_b"]; got.Status != enums.AIReplyTurnTaskStatusSkipped {
		t.Fatalf("handoff task must be cancelled: %+v", got)
	}
	if got := byKey["cancel_task_c"]; got.Status != enums.AIReplyTurnTaskStatusSuperseded {
		t.Fatalf("derived pending task must be superseded: %+v", got)
	}
	if got := byKey["cancel_task_d"]; got.Status != enums.AIReplyTurnTaskStatusSuperseded {
		t.Fatalf("derived running task must be superseded: %+v", got)
	}
	if got := byKey["cancel_task_e"]; got.Status != enums.AIReplyTurnTaskStatusDelivered {
		t.Fatalf("delivered history must not be rewritten: %+v", got)
	}
}
