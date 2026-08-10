package services

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type aiReplyTaskLedgerJobFixture struct {
	*aiReplyJobTestFixture
	turn  *models.AIReplyTurn
	tasks []models.AIReplyTurnTask
}

func TestAIReplyTaskLedgerContinuesPastSixTasksWithoutNewCustomerMessage(t *testing.T) {
	fixture := setupAIReplyTaskLedgerJobFixture(t, []enums.AIReplyTurnTaskType{
		enums.AIReplyTurnTaskTypeText,
		enums.AIReplyTurnTaskTypeText,
		enums.AIReplyTurnTaskTypeText,
		enums.AIReplyTurnTaskTypeText,
		enums.AIReplyTurnTaskTypeText,
		enums.AIReplyTurnTaskTypeText,
		enums.AIReplyTurnTaskTypeText,
	})
	var runtimeCalls atomic.Int32
	batchSizes := make([]int, 0, 2)
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		call := int(runtimeCalls.Add(1))
		result, batchSize, err := commitAIReplyTaskLedgerBatch(fixture, call)
		batchSizes = append(batchSizes, batchSize)
		return result, err
	})

	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	first, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first.Status != enums.AIReplyJobStatusRetry || first.ResultCode != "turn_tasks_remaining" {
		t.Fatalf("first continuation job=%+v", first)
	}

	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	final, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final == nil || final.Status != enums.AIReplyJobStatusCompleted || runtimeCalls.Load() != 2 {
		t.Fatalf("final job=%+v runtimeCalls=%d", final, runtimeCalls.Load())
	}
	if fmt.Sprint(batchSizes) != "[6 1]" {
		t.Fatalf("batch sizes=%v want [6 1]", batchSizes)
	}
	stored := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(fixture.db, fixture.turn.TenantID, fixture.turn.ID)
	if len(stored) != 7 {
		t.Fatalf("stored tasks=%d want 7", len(stored))
	}
	for _, task := range stored {
		if task.Status != enums.AIReplyTurnTaskStatusDelivered || task.CommittedMessageID <= 0 {
			t.Fatalf("task was not delivered: %+v", task)
		}
	}
}

func TestAIReplyTaskLedgerPartialKnowledgeFailureCommitsSuccessAndDispatchesOnce(t *testing.T) {
	fixture := setupAIReplyTaskLedgerJobFixture(t, []enums.AIReplyTurnTaskType{
		enums.AIReplyTurnTaskTypeKnowledge,
		enums.AIReplyTurnTaskTypeKnowledge,
	})
	var runtimeCalls atomic.Int32
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		runtimeCalls.Add(1)
		return commitPartialKnowledgeTaskLedgerBatch(fixture)
	})
	var dispatchCalls atomic.Int32
	fixture.service.humanDispatch = func(*aiReplyJobExecutionState, *models.AIReplyJob, string) error {
		dispatchCalls.Add(1)
		return nil
	}

	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != enums.AIReplyJobStatusCompleted || current.ResultCode != "partial_success_human_dispatch" ||
		runtimeCalls.Load() != 1 || dispatchCalls.Load() != 1 {
		t.Fatalf("job=%+v runtimeCalls=%d dispatchCalls=%d", current, runtimeCalls.Load(), dispatchCalls.Load())
	}
	assertPartialKnowledgeTaskStates(t, fixture)
}

func TestAIReplyTaskLedgerDispatchRetryDoesNotRunModelsAgain(t *testing.T) {
	fixture := setupAIReplyTaskLedgerJobFixture(t, []enums.AIReplyTurnTaskType{
		enums.AIReplyTurnTaskTypeKnowledge,
		enums.AIReplyTurnTaskTypeKnowledge,
	})
	var runtimeCalls atomic.Int32
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		runtimeCalls.Add(1)
		return commitPartialKnowledgeTaskLedgerBatch(fixture)
	})
	var dispatchCalls atomic.Int32
	fixture.service.humanDispatch = func(*aiReplyJobExecutionState, *models.AIReplyJob, string) error {
		if dispatchCalls.Add(1) == 1 {
			return errors.New("dispatch unavailable")
		}
		return nil
	}

	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	first, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first.Status != enums.AIReplyJobStatusRetry || first.ResultCode != "human_dispatch_retry" ||
		runtimeCalls.Load() != 1 || dispatchCalls.Load() != 1 {
		t.Fatalf("first job=%+v runtimeCalls=%d dispatchCalls=%d", first, runtimeCalls.Load(), dispatchCalls.Load())
	}

	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	final, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final == nil || final.Status != enums.AIReplyJobStatusCompleted || runtimeCalls.Load() != 1 || dispatchCalls.Load() != 2 {
		t.Fatalf("final job=%+v runtimeCalls=%d dispatchCalls=%d", final, runtimeCalls.Load(), dispatchCalls.Load())
	}
	assertPartialKnowledgeTaskStates(t, fixture)
}

func TestAIReplyTaskLedgerGenerationFailureIncludesTasksThatPassedKnowledge(t *testing.T) {
	fixture := setupAIReplyTaskLedgerJobFixture(t, []enums.AIReplyTurnTaskType{
		enums.AIReplyTurnTaskTypeKnowledge,
		enums.AIReplyTurnTaskTypeKnowledge,
	})
	var runtimeCalls atomic.Int32
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		runtimeCalls.Add(1)
		var batch []models.AIReplyTurnTask
		if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			turn, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(ctx.Tx, fixture.turn.ID, fixture.turn.TenantID)
			if err != nil {
				return err
			}
			batch, _, err = AIReplyTurnTaskService.ClaimBatchDB(ctx.Tx, turn, fixture.job.ID)
			if err != nil {
				return err
			}
			if len(batch) != 2 {
				return fmt.Errorf("claimed %d tasks, want 2", len(batch))
			}
			return AIReplyTurnTaskService.MarkKnowledgeResultsDB(ctx.Tx, turn.TenantID, turn.ID, fixture.job.ID, []AIReplyTurnTaskKnowledgeUpdate{
				{TaskKey: batch[0].TaskKey, Status: enums.AIReplyTurnTaskKnowledgeStatusFailed, ResultCode: "knowledge_unavailable"},
				{TaskKey: batch[1].TaskKey, Status: enums.AIReplyTurnTaskKnowledgeStatusHit, HitCount: 2, ResultCode: "hit"},
			})
		}); err != nil {
			return AIReplyExecutionResult{}, err
		}
		return AIReplyExecutionResult{
			TaskLedgerEnabled: true,
			TaskKeys:          []string{batch[0].TaskKey, batch[1].TaskKey},
			FailedTaskKeys:    []string{batch[0].TaskKey},
		}, NewAIReplyExecutionError(AIReplyExecutionErrorGenerationFailed, errors.New("generation retries exhausted"))
	})
	var dispatchCalls atomic.Int32
	fixture.service.humanDispatch = func(*aiReplyJobExecutionState, *models.AIReplyJob, string) error {
		dispatchCalls.Add(1)
		return nil
	}

	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != enums.AIReplyJobStatusFailed || runtimeCalls.Load() != 1 || dispatchCalls.Load() != 1 {
		t.Fatalf("job=%+v runtimeCalls=%d dispatchCalls=%d", current, runtimeCalls.Load(), dispatchCalls.Load())
	}
	stored := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(fixture.db, fixture.turn.TenantID, fixture.turn.ID)
	if len(stored) != 2 {
		t.Fatalf("stored tasks=%d want 2", len(stored))
	}
	for _, task := range stored {
		if task.Status != enums.AIReplyTurnTaskStatusHandoff {
			t.Fatalf("task should be handed off without rerunning the model: %+v", task)
		}
	}

	if _, err := fixture.service.ProcessMessageNow(fixture.message.ID); err != nil {
		t.Fatal(err)
	}
	if runtimeCalls.Load() != 1 || dispatchCalls.Load() != 1 {
		t.Fatalf("terminal job reran work: runtimeCalls=%d dispatchCalls=%d", runtimeCalls.Load(), dispatchCalls.Load())
	}
}

func TestAIReplyJobPersistsCoveredByTaskEvidence(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "有咖啡吗")
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		return AIReplyExecutionResult{
			Status:          AIReplyExecutionStatusSuperseded,
			ReasonCode:      "covered_by_existing_task",
			CoveredByTaskID: 91,
		}, nil
	})
	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != enums.AIReplyJobStatusSuperseded || current.CoveredByTaskID != 91 {
		t.Fatalf("covered job=%+v", current)
	}
}

func TestAIReplyHumanTaskRemainsClaimableByExistingRouteEngine(t *testing.T) {
	fixture := setupAIReplyTaskLedgerJobFixture(t, []enums.AIReplyTurnTaskType{enums.AIReplyTurnTaskTypeHuman})
	if len(fixture.tasks) != 1 || fixture.tasks[0].Status != enums.AIReplyTurnTaskStatusPending ||
		fixture.tasks[0].Stage != enums.AIReplyTurnTaskStageHandoff {
		t.Fatalf("human task=%+v", fixture.tasks)
	}
	var claimed []models.AIReplyTurnTask
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		turn, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(ctx.Tx, fixture.turn.ID, fixture.turn.TenantID)
		if err != nil {
			return err
		}
		claimed, _, err = AIReplyTurnTaskService.ClaimBatchDB(ctx.Tx, turn, fixture.job.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].TaskType != enums.AIReplyTurnTaskTypeHuman {
		t.Fatalf("claimed human tasks=%+v", claimed)
	}
}

func TestAIReplyTurnOutboxDistinguishesFailureFallbackFromRequestedHandoff(t *testing.T) {
	tests := []struct {
		name           string
		terminalReason string
		wantAllowed    bool
	}{
		{name: "AI failure keeps committed success deliverable", terminalReason: "ai_failure_hq_agentdesk_handoff", wantAllowed: true},
		{name: "requested handoff cancels pending AI reply", terminalReason: "hq_agentdesk_handoff", wantAllowed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, conversation := setupAIReplyTurnTestDB(t)
			t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
			question := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-handoff-gate", "咖啡和停车场", t0)
			turn := assignAIReplyTurnMessage(t, db, conversation, question)
			reply := createAIReplyTurnReply(t, db, conversation, turn, turn.Version, "reply-handoff-gate", t0.Add(time.Second))
			task := &models.AIReplyTurnTask{
				TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: question.SessionNo,
				TurnID: turn.ID, IntroducedVersion: turn.Version, SourceMessageID: question.ID,
				TaskKey: "turn-task-handoff-gate", SequenceNo: 1, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
				Stage: enums.AIReplyTurnTaskStageDelivery, Status: enums.AIReplyTurnTaskStatusCommitted,
				KnowledgeStatus: enums.AIReplyTurnTaskKnowledgeStatusHit, CommittedMessageID: reply.ID,
				AuditFields: models.AuditFields{CreatedAt: t0, UpdatedAt: t0},
			}
			if err := db.Create(task).Error; err != nil {
				t.Fatal(err)
			}
			if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
				"status": enums.AIReplyTurnStatusInterrupted, "terminal_reason": tt.terminalReason,
				"last_committed_version": turn.Version,
			}); err != nil {
				t.Fatal(err)
			}
			allowed, reason, err := AIReplyTurnService.CanDispatchOutbox(reply)
			if err != nil || allowed != tt.wantAllowed {
				t.Fatalf("allowed=%v reason=%q err=%v", allowed, reason, err)
			}
			if !tt.wantAllowed && reason != "cancelled_turn_inactive" {
				t.Fatalf("cancel reason=%q", reason)
			}
		})
	}
}

func setupAIReplyTaskLedgerJobFixture(t *testing.T, taskTypes []enums.AIReplyTurnTaskType) *aiReplyTaskLedgerJobFixture {
	t.Helper()
	t.Setenv("AI_REPLY_TURN_COORDINATOR_ENABLED", "true")
	t.Setenv("AI_REPLY_TURN_COORDINATOR_BINDING_IDS", "")
	base := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "连续问题")
	fixture := &aiReplyTaskLedgerJobFixture{aiReplyJobTestFixture: base}
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		turn, _, err := AIReplyTurnService.AssignCustomerMessageDB(ctx.Tx, base.conversation, base.message)
		if err != nil {
			return err
		}
		fixture.turn = turn
		inputs := make([]AIReplyTurnTaskInput, 0, len(taskTypes))
		for index, taskType := range taskTypes {
			intent := "interaction"
			if taskType == enums.AIReplyTurnTaskTypeKnowledge {
				intent = "hotel_info"
			}
			inputs = append(inputs, AIReplyTurnTaskInput{
				SourceMessageID: base.message.ID,
				SequenceNo:      index + 1,
				TaskType:        taskType,
				Intent:          intent,
				SubIntent:       fmt.Sprintf("task_%d", index+1),
				QuestionText:    fmt.Sprintf("问题%d", index+1),
			})
		}
		fixture.tasks, err = AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, inputs)
		if err != nil {
			return fmt.Errorf("ensure tasks for turn %+v: %w", turn, err)
		}
		return ctx.Tx.Model(&models.AIReplyJob{}).
			Where("id = ? AND tenant_id = ?", base.job.ID, base.job.TenantID).
			Updates(map[string]any{"turn_id": turn.ID, "turn_version": turn.Version}).Error
	}); err != nil {
		t.Fatal(err)
	}
	fixture.message = repositories.MessageRepository.GetInTenant(base.db, base.message.ID, base.message.TenantID)
	fixture.job = repositories.AIReplyJobRepository.GetInTenant(base.db, base.job.ID, base.job.TenantID)
	fixture.conversation = repositories.ConversationRepository.GetInTenant(base.db, base.conversation.ID, base.conversation.TenantID)
	if fixture.message == nil || fixture.job == nil || fixture.conversation == nil || fixture.turn == nil {
		t.Fatal("AI reply task ledger fixture scope unavailable")
	}
	return fixture
}

func commitAIReplyTaskLedgerBatch(fixture *aiReplyTaskLedgerJobFixture, call int) (AIReplyExecutionResult, int, error) {
	var (
		batch   []models.AIReplyTurnTask
		hasMore bool
		reply   *models.Message
	)
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		turn, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(ctx.Tx, fixture.turn.ID, fixture.turn.TenantID)
		if err != nil {
			return err
		}
		batch, hasMore, err = AIReplyTurnTaskService.ClaimBatchDB(ctx.Tx, turn, fixture.job.ID)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return errors.New("no task batch claimed")
		}
		now := time.Now()
		reply = &models.Message{
			TenantID: fixture.conversation.TenantID, ConversationID: fixture.conversation.ID, SessionNo: fixture.message.SessionNo,
			RequestID: fixture.job.RequestID, ClientMsgID: fmt.Sprintf("ai_reply_task_batch_%d", call),
			SenderType: enums.IMSenderTypeAI, SenderID: fixture.conversation.AIAgentID,
			MessageType: enums.IMMessageTypeText, Content: fmt.Sprintf("第%d批回复", call),
			SeqNo:      repositories.MessageRepository.NextSeqNoInTenant(ctx.Tx, fixture.conversation.ID, fixture.conversation.TenantID),
			SendStatus: enums.IMMessageStatusSent, SentAt: &now,
			AIReplyTurnID: turn.ID, AIReplyTurnVersion: turn.Version,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}
		if err := ctx.Tx.Create(reply).Error; err != nil {
			return err
		}
		taskMessages := make(map[string]int64, len(batch))
		for _, task := range batch {
			taskMessages[task.TaskKey] = reply.ID
		}
		if err := AIReplyTurnTaskService.MarkCommittedMessagesDB(ctx.Tx, turn, fixture.job.ID, taskMessages, true, now); err != nil {
			return err
		}
		return AIReplyTurnService.MarkCommittedDB(ctx.Tx, turn, turn.Version, fixture.job.RequestID, true, now)
	})
	keys := make([]string, 0, len(batch))
	for _, task := range batch {
		keys = append(keys, task.TaskKey)
	}
	result := AIReplyExecutionResult{
		Status: AIReplyExecutionStatusCompleted, ReasonCode: "runtime_completed",
		TaskLedgerEnabled: true, TaskKeys: keys, HasRemainingTasks: hasMore,
	}
	if reply != nil {
		result.CommittedMessageIDs = []int64{reply.ID}
	}
	return result, len(batch), err
}

func commitPartialKnowledgeTaskLedgerBatch(fixture *aiReplyTaskLedgerJobFixture) (AIReplyExecutionResult, error) {
	var (
		batch []models.AIReplyTurnTask
		reply *models.Message
	)
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		turn, err := repositories.AIReplyTurnRepository.GetForUpdateInTenant(ctx.Tx, fixture.turn.ID, fixture.turn.TenantID)
		if err != nil {
			return err
		}
		batch, _, err = AIReplyTurnTaskService.ClaimBatchDB(ctx.Tx, turn, fixture.job.ID)
		if err != nil {
			return err
		}
		if len(batch) != 2 {
			return fmt.Errorf("claimed %d tasks, want 2", len(batch))
		}
		if err := AIReplyTurnTaskService.MarkKnowledgeResultsDB(ctx.Tx, turn.TenantID, turn.ID, fixture.job.ID, []AIReplyTurnTaskKnowledgeUpdate{
			{TaskKey: batch[0].TaskKey, Status: enums.AIReplyTurnTaskKnowledgeStatusHit, HitCount: 2, ResultCode: "hit"},
			{TaskKey: batch[1].TaskKey, Status: enums.AIReplyTurnTaskKnowledgeStatusFailed, ResultCode: "knowledge_unavailable"},
		}); err != nil {
			return err
		}
		now := time.Now()
		reply = &models.Message{
			TenantID: fixture.conversation.TenantID, ConversationID: fixture.conversation.ID, SessionNo: fixture.message.SessionNo,
			RequestID: fixture.job.RequestID, ClientMsgID: "ai_reply_partial_knowledge",
			SenderType: enums.IMSenderTypeAI, SenderID: fixture.conversation.AIAgentID,
			MessageType: enums.IMMessageTypeText, Content: "已回答成功的问题",
			SeqNo:      repositories.MessageRepository.NextSeqNoInTenant(ctx.Tx, fixture.conversation.ID, fixture.conversation.TenantID),
			SendStatus: enums.IMMessageStatusSent, SentAt: &now,
			AIReplyTurnID: turn.ID, AIReplyTurnVersion: turn.Version,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}
		if err := ctx.Tx.Create(reply).Error; err != nil {
			return err
		}
		if err := AIReplyTurnTaskService.MarkCommittedMessagesDB(ctx.Tx, turn, fixture.job.ID, map[string]int64{
			batch[0].TaskKey: reply.ID,
		}, true, now); err != nil {
			return err
		}
		return AIReplyTurnService.MarkCommittedDB(ctx.Tx, turn, turn.Version, fixture.job.RequestID, true, now)
	})
	if err != nil {
		return AIReplyExecutionResult{}, err
	}
	return AIReplyExecutionResult{
		Status: AIReplyExecutionStatusCompleted, ReasonCode: "runtime_partial_completed",
		CommittedMessageIDs: []int64{reply.ID}, TaskLedgerEnabled: true,
		TaskKeys: []string{batch[0].TaskKey, batch[1].TaskKey}, FailedTaskKeys: []string{batch[1].TaskKey},
	}, NewAIReplyExecutionError(AIReplyExecutionErrorKnowledgeUnavailable, errors.New("knowledge retries exhausted"))
}

func assertPartialKnowledgeTaskStates(t *testing.T, fixture *aiReplyTaskLedgerJobFixture) {
	t.Helper()
	stored := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(fixture.db, fixture.turn.TenantID, fixture.turn.ID)
	if len(stored) != 2 {
		t.Fatalf("stored tasks=%d want 2", len(stored))
	}
	if stored[0].Status != enums.AIReplyTurnTaskStatusDelivered || stored[0].CommittedMessageID <= 0 {
		t.Fatalf("successful task=%+v", stored[0])
	}
	if stored[1].Status != enums.AIReplyTurnTaskStatusHandoff {
		t.Fatalf("failed task=%+v", stored[1])
	}
}
