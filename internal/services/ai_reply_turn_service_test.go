package services

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestAIReplyTurnLateDuplicateUsesCommittedCoverage(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-20 * time.Second).Truncate(time.Second)
	first := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-1", "有咖啡吗？", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, first)
	if turn.Version != 1 || first.AIReplyTurnID != turn.ID || first.AIReplyTurnVersion != 1 {
		t.Fatalf("first turn assignment mismatch turn=%+v message=%+v", turn, first)
	}

	deliveredAt := t0.Add(7 * time.Second)
	reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "reply-1", deliveredAt)
	outbox := &models.ChannelMessageOutbox{
		TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID, MessageID: reply.ID,
		SendStatus: string(enums.ChannelMessageOutboxStatusSent), SentAt: &deliveredAt,
		AuditFields: models.AuditFields{CreatedAt: deliveredAt, UpdatedAt: deliveredAt},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatal(err)
	}
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"status": enums.AIReplyTurnStatusDelivered, "last_committed_version": 1,
		"last_delivered_version": 1, "last_committed_request_id": reply.RequestID,
		"last_delivered_request_id": reply.RequestID, "last_delivered_at": deliveredAt,
	}); err != nil {
		t.Fatal(err)
	}

	late := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-2", " 有咖啡吗。 ", t0.Add(5*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, late)
	if turn.ID != first.AIReplyTurnID || turn.Version != 2 || late.AIReplyTurnVersion != 2 {
		t.Fatalf("late message must reopen the delivered turn: turn=%+v late=%+v", turn, late)
	}
	job := aiReplyTurnTestJob(conversation, late)
	coverage := AIReplyTurnService.FindCoverage(job, late, turn)
	if coverage == nil || coverage.ReasonCode != "covered_by_inflight_reply" || coverage.CoveredByMessageID != reply.ID {
		t.Fatalf("late duplicate coverage=%+v want reply=%d", coverage, reply.ID)
	}
	var covered *AIReplyTurnCoveredError
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return AIReplyTurnService.ValidatePreparedReplyDB(ctx.Tx, turn, late.RequestID, []AIOutboundMessageDraft{{
			MessageType: enums.IMMessageTypeText,
			Content:     reply.Content,
		}})
	})
	if !errors.As(err, &covered) || covered.CoveredByMessageID != reply.ID {
		t.Fatalf("prepared duplicate coverage error=%v coverage=%+v", err, covered)
	}

	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		_, validateErr := AIReplyTurnService.ValidateCommitDB(ctx.Tx, conversation.TenantID, conversation.ID, 1, turn.ID, 1, 0, nil)
		return validateErr
	})
	if !errors.Is(err, ErrAIReplyTurnStale) {
		t.Fatalf("old turn version commit error=%v want stale", err)
	}
}

func TestAIReplyTurnFeatureControlsFailClosed(t *testing.T) {
	_, conversation := setupAIReplyTurnTestDB(t)

	t.Setenv("AI_REPLY_TURN_COORDINATOR_ENABLED", "")
	if AIReplyTurnService.EnabledFor(conversation) {
		t.Fatal("turn coordinator must remain disabled when the feature flag is unset")
	}
	t.Setenv("AI_REPLY_TURN_COORDINATOR_ENABLED", "invalid")
	if AIReplyTurnService.EnabledFor(conversation) {
		t.Fatal("turn coordinator must remain disabled when the feature flag is invalid")
	}
	t.Setenv("AI_REPLY_TURN_COORDINATOR_ENABLED", "true")
	t.Setenv("AI_REPLY_TURN_COORDINATOR_BINDING_IDS", strconv.FormatInt(conversation.StoreStaffBindingID+1, 10))
	if AIReplyTurnService.EnabledFor(conversation) {
		t.Fatal("turn coordinator must reject a binding outside the allowlist")
	}
	t.Setenv("AI_REPLY_TURN_COORDINATOR_BINDING_IDS", strconv.FormatInt(conversation.StoreStaffBindingID, 10))
	if !AIReplyTurnService.EnabledFor(conversation) {
		t.Fatal("turn coordinator must enable an allowlisted binding")
	}
	t.Setenv("AI_REPLY_TURN_COORDINATOR_BINDING_IDS", "")
	if !AIReplyTurnService.EnabledFor(conversation) {
		t.Fatal("empty allowlist must enable all bindings only after the feature flag is explicit")
	}
}

func TestAIReplyTurnTaskExactDuplicateIsCoveredByCanonicalTask(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	first := createAIReplyTurnCustomerMessage(t, db, conversation, "task-source-1", "有咖啡吗？", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, first)

	var canonical []models.AIReplyTurnTask
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		canonical, err = AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, []AIReplyTurnTaskInput{{
			SourceMessageID: first.ID, SequenceNo: 1, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
			Intent: "hotel_info", SubIntent: "service_facility", QuestionText: first.Content,
		}})
		return err
	}); err != nil {
		t.Fatalf("create canonical task: %v", err)
	}
	if len(canonical) != 1 || canonical[0].Status != enums.AIReplyTurnTaskStatusPending {
		t.Fatalf("canonical task=%#v", canonical)
	}

	duplicate := createAIReplyTurnCustomerMessage(t, db, conversation, "task-source-2", " 有咖啡吗。 ", t0.Add(time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, duplicate)
	var covered []models.AIReplyTurnTask
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		covered, err = AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, []AIReplyTurnTaskInput{{
			SourceMessageID: duplicate.ID, SequenceNo: 2, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
			Intent: "hotel_info", SubIntent: "service_facility", QuestionText: duplicate.Content,
		}})
		return err
	}); err != nil {
		t.Fatalf("create duplicate task: %v", err)
	}
	if len(covered) != 1 || covered[0].Status != enums.AIReplyTurnTaskStatusCovered ||
		covered[0].CoveredByTaskID != canonical[0].ID || covered[0].ResultCode != "covered_by_existing_task" {
		t.Fatalf("duplicate task was not covered by canonical task: canonical=%#v duplicate=%#v", canonical, covered)
	}
}

func TestAIReplyTurnTaskDoesNotReuseCoveredAliasWithSupersededRoot(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	first := createAIReplyTurnCustomerMessage(t, db, conversation, "invalid-root-source-1", "有咖啡吗？", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, first)

	var canonical []models.AIReplyTurnTask
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		canonical, err = AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, []AIReplyTurnTaskInput{{
			SourceMessageID: first.ID, SequenceNo: 1, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
			Intent: "hotel_info", SubIntent: "coffee", RequestMode: "answer", QuestionText: first.Content,
		}})
		return err
	}); err != nil {
		t.Fatalf("create canonical task: %v", err)
	}
	second := createAIReplyTurnCustomerMessage(t, db, conversation, "invalid-root-source-2", " 有咖啡吗。 ", t0.Add(time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, second)
	var alias []models.AIReplyTurnTask
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		alias, err = AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, []AIReplyTurnTaskInput{{
			SourceMessageID: second.ID, SequenceNo: 1, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
			Intent: "hotel_info", SubIntent: "coffee", RequestMode: "answer", QuestionText: second.Content,
		}})
		return err
	}); err != nil {
		t.Fatalf("create covered alias: %v", err)
	}
	if len(alias) != 1 || alias[0].Status != enums.AIReplyTurnTaskStatusCovered || alias[0].CoveredByTaskID != canonical[0].ID {
		t.Fatalf("duplicate did not create covered alias: canonical=%#v alias=%#v", canonical, alias)
	}
	if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, canonical[0].ID, canonical[0].TenantID, map[string]any{
		"stage": enums.AIReplyTurnTaskStageComplete, "status": enums.AIReplyTurnTaskStatusSuperseded,
		"result_code": "superseded_by_customer_correction", "completed_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	third := createAIReplyTurnCustomerMessage(t, db, conversation, "invalid-root-source-3", "有咖啡吗", t0.Add(2*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, third)
	var replacement []models.AIReplyTurnTask
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		replacement, err = AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, []AIReplyTurnTaskInput{{
			SourceMessageID: third.ID, SequenceNo: 1, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
			Intent: "hotel_info", SubIntent: "coffee", RequestMode: "answer", QuestionText: third.Content,
		}})
		return err
	}); err != nil {
		t.Fatalf("create replacement task: %v", err)
	}
	if len(replacement) != 1 || replacement[0].Status != enums.AIReplyTurnTaskStatusPending || replacement[0].CoveredByTaskID != 0 {
		t.Fatalf("new task reused covered alias whose root was superseded: %#v", replacement)
	}
}

func TestAIReplyTurnCommittedDuplicateHasNoRunnableTask(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	first := createAIReplyTurnCustomerMessage(t, db, conversation, "committed-source-1", "有咖啡吗？", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, first)

	var canonical []models.AIReplyTurnTask
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		canonical, err = AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, []AIReplyTurnTaskInput{{
			SourceMessageID: first.ID, SequenceNo: 1, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
			Intent: "hotel_info", SubIntent: "coffee", RequestMode: "answer", QuestionText: first.Content,
		}})
		return err
	}); err != nil {
		t.Fatalf("create canonical task: %v", err)
	}
	reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "committed-duplicate-reply", t0.Add(time.Second))
	if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, canonical[0].ID, canonical[0].TenantID, map[string]any{
		"stage": enums.AIReplyTurnTaskStageDelivery, "status": enums.AIReplyTurnTaskStatusCommitted,
		"committed_message_id": reply.ID, "result_code": "committed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ChannelMessageOutbox{
		TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID, MessageID: reply.ID, SendStatus: string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{CreatedAt: t0, UpdatedAt: t0},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"status": enums.AIReplyTurnStatusCommitted, "last_committed_version": 1,
	}); err != nil {
		t.Fatal(err)
	}

	duplicate := createAIReplyTurnCustomerMessage(t, db, conversation, "committed-source-2", " 有咖啡吗。 ", t0.Add(2*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, duplicate)
	var covered []models.AIReplyTurnTask
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		covered, err = AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, []AIReplyTurnTaskInput{{
			SourceMessageID: duplicate.ID, SequenceNo: 1, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
			Intent: "hotel_info", SubIntent: "coffee", RequestMode: "answer", QuestionText: duplicate.Content,
		}})
		return err
	}); err != nil {
		t.Fatalf("cover committed duplicate: %v", err)
	}
	if len(covered) != 1 || covered[0].Status != enums.AIReplyTurnTaskStatusCovered || covered[0].CoveredByTaskID != canonical[0].ID {
		t.Fatalf("committed duplicate was not covered by the canonical task: canonical=%#v duplicate=%#v", canonical, covered)
	}
	if AIReplyTurnTaskService.HasRunnable(turn.TenantID, turn.ID) {
		t.Fatal("a committed duplicate must not create runnable work or a second Generate")
	}
	coverage := AIReplyTurnService.FindCoverage(aiReplyTurnTestJob(conversation, duplicate), duplicate, turn)
	if coverage == nil || coverage.ReasonCode != "pending_delivery_reused" || coverage.CoveredByMessageID != reply.ID {
		t.Fatalf("committed duplicate did not reuse pending delivery: %+v", coverage)
	}
}

func TestAIReplyTurnCommittedOutboxSurvivesIndependentTurnAdvance(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	question := createAIReplyTurnCustomerMessage(t, db, conversation, "old-coffee", "有咖啡吗", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, question)
	reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "old-coffee-reply", t0.Add(time.Second))
	task := createCommittedAIReplyTurnTask(t, db, turn, question, reply, "task-old-coffee", 1, "coffee")
	outbox := createPendingAIReplyOutbox(t, db, conversation, reply, t0)

	newQuestion := createAIReplyTurnCustomerMessage(t, db, conversation, "new-breakfast", "早餐几点", t0.Add(2*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, newQuestion)
	allowed, reason, err := AIReplyTurnService.CanDispatchOutbox(reply)
	if err != nil || !allowed || reason != "" {
		t.Fatalf("independent turn advance cancelled a committed task reply allowed=%v reason=%q err=%v task=%#v", allowed, reason, err, task)
	}
	claimed, err := ChannelMessageOutboxService.TryMarkSending(outbox.ID, outbox.TenantID)
	if err != nil || !claimed {
		t.Fatalf("valid committed outbox was not claimable after turn advance claimed=%v err=%v", claimed, err)
	}
	newReply := createAIReplyTurnReply(t, db, conversation, turn, 2, "new-breakfast-reply", t0.Add(3*time.Second))
	createCommittedAIReplyTurnTask(t, db, turn, newQuestion, newReply, "task-new-breakfast", 2, "breakfast")
	if err := AIReplyTurnService.MarkDelivered(newReply, t0.Add(4*time.Second)); err != nil {
		t.Fatalf("mark newer reply delivered: %v", err)
	}
	if err := AIReplyTurnService.MarkDelivered(reply, t0.Add(5*time.Second)); err != nil {
		t.Fatalf("mark older still-valid reply delivered: %v", err)
	}
	updatedTask := repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, task.TenantID, task.TurnID, task.TaskKey)
	if updatedTask == nil || updatedTask.Status != enums.AIReplyTurnTaskStatusDelivered {
		t.Fatalf("older committed task was not marked delivered after newer version: %#v", updatedTask)
	}
}

func TestAIReplyTurnCorrectionSupersedesOnlyReferencedTask(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-20 * time.Second).Truncate(time.Second)
	coffeeQuestion := createAIReplyTurnCustomerMessage(t, db, conversation, "coffee-question-correction", "有咖啡吗", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, coffeeQuestion)
	coffeeReply := createAIReplyTurnReply(t, db, conversation, turn, 1, "coffee-reply-correction", t0.Add(time.Second))
	coffeeTask := createCommittedAIReplyTurnTask(t, db, turn, coffeeQuestion, coffeeReply, "task-coffee-correction", 1, "coffee")
	coffeeOutbox := createPendingAIReplyOutbox(t, db, conversation, coffeeReply, t0)
	coffeeAction := &models.AIReplyTurnAction{
		TenantID: conversation.TenantID, TurnID: turn.ID, TaskKey: coffeeTask.TaskKey, ActionKey: "action-coffee-correction",
		ActionType: "send_knowledge_image", ResourceType: "knowledge_image", Status: "committed", RequestedVersion: 1,
		PreparedRevision: "coffee-asset-v1", CommittedMessageID: coffeeReply.ID, OutboxID: coffeeOutbox.ID,
		CreatedAt: t0, UpdatedAt: t0, CreateUserName: "test", UpdateUserName: "test",
	}
	if err := db.Create(coffeeAction).Error; err != nil {
		t.Fatal(err)
	}

	parkingQuestion := createAIReplyTurnCustomerMessage(t, db, conversation, "parking-question-correction", "停车场在哪", t0.Add(2*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, parkingQuestion)
	parkingReply := createAIReplyTurnReply(t, db, conversation, turn, 2, "parking-reply-correction", t0.Add(3*time.Second))
	parkingTask := createCommittedAIReplyTurnTask(t, db, turn, parkingQuestion, parkingReply, "task-parking-correction", 2, "parking")
	createPendingAIReplyOutbox(t, db, conversation, parkingReply, t0.Add(2*time.Second))

	correction := createAIReplyTurnCustomerMessage(t, db, conversation, "coffee-correction", "不是咖啡，我问的是早餐", t0.Add(4*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, correction)
	bindingsJSON := aiReplyTaskSourceBindingsJSON(t, correction, coffeeQuestion)
	var current []models.AIReplyTurnTask
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		current, err = AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, []AIReplyTurnTaskInput{{
			SourceMessageID: correction.ID, SequenceNo: 1, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
			Intent: "hotel_info", SubIntent: "breakfast", RequestMode: "answer", RelationType: "correction",
			RelatedTaskID: coffeeTask.ID, QuestionText: "早餐几点", SourceBindingsJSON: bindingsJSON,
		}})
		if err != nil {
			return err
		}
		return AIReplyTurnTaskService.SupersedePriorTasksForDialogueActDB(ctx.Tx, turn, "correction", current, time.Now())
	}); err != nil {
		t.Fatalf("apply correction: %v", err)
	}

	updatedCoffee := repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, coffeeTask.TenantID, coffeeTask.TurnID, coffeeTask.TaskKey)
	updatedParking := repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, parkingTask.TenantID, parkingTask.TurnID, parkingTask.TaskKey)
	if updatedCoffee == nil || updatedCoffee.Status != enums.AIReplyTurnTaskStatusSuperseded || updatedCoffee.ResultCode != "superseded_by_customer_correction" {
		t.Fatalf("referenced coffee task was not superseded: %#v", updatedCoffee)
	}
	if updatedParking == nil || updatedParking.Status != enums.AIReplyTurnTaskStatusCommitted {
		t.Fatalf("unrelated parking task was superseded: %#v", updatedParking)
	}
	if allowed, _, err := AIReplyTurnService.CanDispatchOutbox(coffeeReply); err != nil || allowed {
		t.Fatalf("superseded coffee reply remained dispatchable allowed=%v err=%v", allowed, err)
	}
	if allowed, reason, err := AIReplyTurnService.CanDispatchOutbox(parkingReply); err != nil || !allowed || reason != "" {
		t.Fatalf("unrelated parking reply was cancelled allowed=%v reason=%q err=%v", allowed, reason, err)
	}
	claimed, err := ChannelMessageOutboxService.TryMarkSending(coffeeOutbox.ID, coffeeOutbox.TenantID)
	if err != nil || claimed {
		t.Fatalf("superseded coffee outbox claimed=%v err=%v", claimed, err)
	}
	updatedOutbox := repositories.ChannelMessageOutboxRepository.GetInTenant(db, coffeeOutbox.ID, coffeeOutbox.TenantID)
	if updatedOutbox == nil || updatedOutbox.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) {
		t.Fatalf("superseded coffee outbox was not cancelled: %#v", updatedOutbox)
	}
	updatedAction := repositories.AIReplyTurnActionRepository.GetByKeyInTenant(
		db, coffeeAction.TenantID, coffeeAction.TurnID, coffeeAction.TaskKey, coffeeAction.ActionKey,
	)
	if updatedAction == nil || updatedAction.Status != "superseded" || updatedAction.ResultCode != "cancelled_stale_task" {
		t.Fatalf("superseded coffee outbox action was not cancelled: %#v", updatedAction)
	}
}

func TestAIReplyTurnCorrectionWithoutRelatedTaskDoesNotGuessTarget(t *testing.T) {
	for _, testCase := range []struct {
		name               string
		includePriorSource bool
	}{
		{name: "source binding", includePriorSource: true},
		{name: "latest prior message"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, conversation := setupAIReplyTurnTestDB(t)
			t0 := time.Now().Add(-20 * time.Second).Truncate(time.Second)
			coffeeQuestion := createAIReplyTurnCustomerMessage(t, db, conversation, "untargeted-coffee", "有咖啡吗", t0)
			turn := assignAIReplyTurnMessage(t, db, conversation, coffeeQuestion)
			coffeeReply := createAIReplyTurnReply(t, db, conversation, turn, 1, "untargeted-coffee-reply", t0.Add(time.Second))
			coffeeTask := createCommittedAIReplyTurnTask(t, db, turn, coffeeQuestion, coffeeReply, "untargeted-task-coffee", 1, "coffee")

			parkingQuestion := createAIReplyTurnCustomerMessage(t, db, conversation, "untargeted-parking", "停车场在哪", t0.Add(2*time.Second))
			turn = assignAIReplyTurnMessage(t, db, conversation, parkingQuestion)
			parkingReply := createAIReplyTurnReply(t, db, conversation, turn, 2, "untargeted-parking-reply", t0.Add(3*time.Second))
			parkingTask := createCommittedAIReplyTurnTask(t, db, turn, parkingQuestion, parkingReply, "untargeted-task-parking", 2, "parking")

			correction := createAIReplyTurnCustomerMessage(t, db, conversation, "untargeted-correction", "不是这个，我要问早餐", t0.Add(4*time.Second))
			turn = assignAIReplyTurnMessage(t, db, conversation, correction)
			bindingsJSON := aiReplyTaskSourceBindingsJSON(t, correction)
			if testCase.includePriorSource {
				bindingsJSON = aiReplyTaskSourceBindingsJSON(t, correction, coffeeQuestion)
			}
			if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
				current, err := AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, []AIReplyTurnTaskInput{{
					SourceMessageID: correction.ID, SequenceNo: 1, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
					Intent: "hotel_info", SubIntent: "breakfast", RequestMode: "answer", RelationType: "correction",
					QuestionText: "早餐几点", SourceBindingsJSON: bindingsJSON,
				}})
				if err != nil {
					return err
				}
				return AIReplyTurnTaskService.SupersedePriorTasksForDialogueActDB(ctx.Tx, turn, "correction", current, time.Now())
			}); err != nil {
				t.Fatalf("apply untargeted correction: %v", err)
			}
			updatedCoffee := repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, coffeeTask.TenantID, coffeeTask.TurnID, coffeeTask.TaskKey)
			updatedParking := repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, parkingTask.TenantID, parkingTask.TurnID, parkingTask.TaskKey)
			if updatedCoffee == nil || updatedCoffee.Status != enums.AIReplyTurnTaskStatusCommitted {
				t.Fatalf("untargeted correction guessed coffee task: %#v", updatedCoffee)
			}
			if updatedParking == nil || updatedParking.Status != enums.AIReplyTurnTaskStatusCommitted {
				t.Fatalf("untargeted correction guessed latest parking task: %#v", updatedParking)
			}
		})
	}
}

func TestAIReplyTurnSharedMessageRequiresEveryTaskToRemainDispatchable(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	question := createAIReplyTurnCustomerMessage(t, db, conversation, "shared-task-question", "有咖啡吗，停车场在哪", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, question)
	reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "shared-task-reply", t0.Add(time.Second))
	coffeeTask := createCommittedAIReplyTurnTask(t, db, turn, question, reply, "shared-task-coffee", 1, "coffee")
	createCommittedAIReplyTurnTask(t, db, turn, question, reply, "shared-task-parking", 1, "parking")
	if err := repositories.AIReplyTurnTaskRepository.UpdatesInTenant(db, coffeeTask.ID, coffeeTask.TenantID, map[string]any{
		"stage": enums.AIReplyTurnTaskStageComplete, "status": enums.AIReplyTurnTaskStatusSuperseded,
		"result_code": "superseded_by_customer_correction",
	}); err != nil {
		t.Fatal(err)
	}
	if allowed, reason, err := AIReplyTurnService.CanDispatchOutbox(reply); err != nil || allowed || reason != "cancelled_stale_task" {
		t.Fatalf("shared reply remained dispatchable allowed=%v reason=%q err=%v", allowed, reason, err)
	}
}

func TestAIReplyTurnCorrectionRelatedTaskWinsOverSharedSource(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	question := createAIReplyTurnCustomerMessage(t, db, conversation, "shared-correction-question", "有咖啡吗，停车场在哪", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, question)
	reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "shared-correction-reply", t0.Add(time.Second))
	coffeeTask := createCommittedAIReplyTurnTask(t, db, turn, question, reply, "shared-correction-coffee", 1, "coffee")
	parkingTask := createCommittedAIReplyTurnTask(t, db, turn, question, reply, "shared-correction-parking", 1, "parking")
	outbox := createPendingAIReplyOutbox(t, db, conversation, reply, t0)

	correction := createAIReplyTurnCustomerMessage(t, db, conversation, "shared-correction-current", "不是咖啡，我问的是早餐", t0.Add(2*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, correction)
	bindingsJSON := aiReplyTaskSourceBindingsJSON(t, correction, question)
	var current []models.AIReplyTurnTask
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		current, err = AIReplyTurnTaskService.EnsureTasksDB(ctx.Tx, turn, []AIReplyTurnTaskInput{{
			SourceMessageID: correction.ID, SequenceNo: 1, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
			Intent: "hotel_info", SubIntent: "breakfast", RequestMode: "answer", RelationType: "correction",
			RelatedTaskID: coffeeTask.ID, QuestionText: "早餐几点", SourceBindingsJSON: bindingsJSON,
		}})
		if err != nil {
			return err
		}
		return AIReplyTurnTaskService.SupersedePriorTasksForDialogueActDB(ctx.Tx, turn, "correction", current, time.Now())
	}); err != nil {
		t.Fatalf("apply shared-source correction: %v", err)
	}
	updatedCoffee := repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, coffeeTask.TenantID, coffeeTask.TurnID, coffeeTask.TaskKey)
	updatedParking := repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, parkingTask.TenantID, parkingTask.TurnID, parkingTask.TaskKey)
	if updatedCoffee == nil || updatedCoffee.Status != enums.AIReplyTurnTaskStatusSuperseded {
		t.Fatalf("related coffee task was not superseded: %#v", updatedCoffee)
	}
	if updatedParking == nil || updatedParking.Status != enums.AIReplyTurnTaskStatusReady ||
		updatedParking.Stage != enums.AIReplyTurnTaskStageGenerate || updatedParking.CommittedMessageID != 0 ||
		updatedParking.ClaimedByJobID != 0 || updatedParking.ClaimedVersion != 0 || updatedParking.CompletedAt != nil ||
		updatedParking.ResultCode != "requeued_after_shared_reply_superseded" {
		t.Fatalf("unaffected shared-message task was not requeued for Generate: %#v", updatedParking)
	}
	if allowed, reason, err := AIReplyTurnService.CanDispatchOutbox(reply); err != nil || allowed || reason != "cancelled_stale_task" {
		t.Fatalf("reply containing corrected task remained dispatchable allowed=%v reason=%q err=%v", allowed, reason, err)
	}
	updatedOutbox := repositories.ChannelMessageOutboxRepository.GetInTenant(db, outbox.ID, outbox.TenantID)
	if updatedOutbox == nil || updatedOutbox.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) ||
		updatedOutbox.LastError != "cancelled_stale_task" {
		t.Fatalf("shared reply outbox was not cancelled transactionally: %#v", updatedOutbox)
	}
}

func TestAIReplyTurnLateArrivalTimingReplay(t *testing.T) {
	for _, delay := range []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 14 * time.Second} {
		delay := delay
		t.Run(delay.String(), func(t *testing.T) {
			db, conversation := setupAIReplyTurnTestDB(t)
			t0 := time.Now().Add(-30 * time.Second).Truncate(time.Second)
			first := createAIReplyTurnCustomerMessageAt(t, db, conversation, "customer-first", "有咖啡吗？", t0, t0.Add(time.Second))
			turn := assignAIReplyTurnMessage(t, db, conversation, first)

			deliveredAt := t0.Add(4500 * time.Millisecond)
			reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "reply-timing", deliveredAt)
			if err := db.Create(&models.ChannelMessageOutbox{
				TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
				ConversationID: conversation.ID, MessageID: reply.ID,
				SendStatus: string(enums.ChannelMessageOutboxStatusSent), SentAt: &deliveredAt,
				AuditFields: models.AuditFields{CreatedAt: deliveredAt, UpdatedAt: deliveredAt},
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
				"status": enums.AIReplyTurnStatusDelivered, "last_committed_version": 1,
				"last_delivered_version": 1, "last_committed_request_id": reply.RequestID,
				"last_delivered_request_id": reply.RequestID, "last_delivered_at": deliveredAt,
			}); err != nil {
				t.Fatal(err)
			}

			lateSentAt := t0.Add(4 * time.Second)
			lateReceivedAt := lateSentAt.Add(delay)
			if !lateReceivedAt.After(deliveredAt) {
				t.Fatalf("fixture must receive the late message after delivery: received=%s delivered=%s", lateReceivedAt, deliveredAt)
			}
			late := createAIReplyTurnCustomerMessageAt(t, db, conversation, "customer-late", " 有咖啡吗。 ", lateSentAt, lateReceivedAt)
			if got := wxWorkInboundLagMillis(late); got != delay.Milliseconds() {
				t.Fatalf("inbound lag=%dms want=%dms", got, delay.Milliseconds())
			}
			updatedTurn := assignAIReplyTurnMessage(t, db, conversation, late)
			if updatedTurn.ID != turn.ID || updatedTurn.Version != 2 || late.AIReplyTurnVersion != 2 {
				t.Fatalf("late arrival must join the existing turn: original=%+v updated=%+v message=%+v", turn, updatedTurn, late)
			}
			coverage := AIReplyTurnService.FindCoverage(aiReplyTurnTestJob(conversation, late), late, updatedTurn)
			if coverage == nil || coverage.ReasonCode != "covered_by_inflight_reply" || coverage.CoveredByMessageID != reply.ID {
				t.Fatalf("late arrival coverage=%+v want reply=%d", coverage, reply.ID)
			}
		})
	}
}

func TestAIReplyTurnDuplicateCoverageByOutboxState(t *testing.T) {
	cases := []struct {
		name              string
		status            enums.ChannelMessageOutboxStatus
		wantReason        string
		expectRetryPulled bool
	}{
		{name: "pending", status: enums.ChannelMessageOutboxStatusPending, wantReason: "pending_delivery_reused"},
		{name: "sending", status: enums.ChannelMessageOutboxStatusSending, wantReason: "pending_delivery_reused"},
		{name: "failed", status: enums.ChannelMessageOutboxStatusFailed, wantReason: "pending_delivery_reused", expectRetryPulled: true},
		{name: "sent", status: enums.ChannelMessageOutboxStatusSent, wantReason: "covered_by_inflight_reply"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			db, conversation := setupAIReplyTurnTestDB(t)
			t0 := time.Now().Add(-20 * time.Second).Truncate(time.Second)
			question := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-original", "有咖啡吗", t0)
			turn := assignAIReplyTurnMessage(t, db, conversation, question)
			replyAt := t0.Add(4 * time.Second)
			reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "reply-state", replyAt)
			nextRetryAt := time.Now().Add(time.Hour)
			outbox := &models.ChannelMessageOutbox{
				TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
				ConversationID: conversation.ID, MessageID: reply.ID, SendStatus: string(testCase.status),
				NextRetryAt: &nextRetryAt, AuditFields: models.AuditFields{CreatedAt: replyAt, UpdatedAt: replyAt},
			}
			if testCase.status == enums.ChannelMessageOutboxStatusSent {
				outbox.SentAt = &replyAt
				outbox.NextRetryAt = nil
			}
			if err := db.Create(outbox).Error; err != nil {
				t.Fatal(err)
			}
			turnUpdates := map[string]any{
				"status": enums.AIReplyTurnStatusCommitted, "last_committed_version": 1,
				"last_committed_request_id": reply.RequestID,
			}
			if testCase.status == enums.ChannelMessageOutboxStatusSent {
				turnUpdates["status"] = enums.AIReplyTurnStatusDelivered
				turnUpdates["last_delivered_version"] = 1
				turnUpdates["last_delivered_request_id"] = reply.RequestID
				turnUpdates["last_delivered_at"] = replyAt
			}
			if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, turnUpdates); err != nil {
				t.Fatal(err)
			}

			late := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-duplicate", "有咖啡吗。", t0.Add(3*time.Second))
			turn = assignAIReplyTurnMessage(t, db, conversation, late)
			beforeCoverage := time.Now()
			coverage := AIReplyTurnService.FindCoverage(aiReplyTurnTestJob(conversation, late), late, turn)
			if coverage == nil || coverage.ReasonCode != testCase.wantReason || coverage.CoveredByMessageID != reply.ID {
				t.Fatalf("coverage=%+v want reason=%s reply=%d", coverage, testCase.wantReason, reply.ID)
			}
			updatedOutbox := repositories.ChannelMessageOutboxRepository.GetInTenant(db, outbox.ID, outbox.TenantID)
			if testCase.expectRetryPulled {
				if updatedOutbox == nil || updatedOutbox.NextRetryAt == nil || updatedOutbox.NextRetryAt.Before(beforeCoverage) || updatedOutbox.NextRetryAt.After(time.Now()) {
					t.Fatalf("failed outbox retry was not pulled forward: %+v", updatedOutbox)
				}
			} else if updatedOutbox == nil || (outbox.NextRetryAt != nil && (updatedOutbox.NextRetryAt == nil || !updatedOutbox.NextRetryAt.Equal(*outbox.NextRetryAt))) {
				t.Fatalf("non-failed outbox retry changed unexpectedly: before=%+v after=%+v", outbox, updatedOutbox)
			}
		})
	}
}

func TestAIReplyTurnDuplicateCoverageUsesTheMatchingQuestionBatch(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-30 * time.Second).Truncate(time.Second)
	coffeeQuestion := createAIReplyTurnCustomerMessage(t, db, conversation, "coffee-question", "有咖啡吗", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, coffeeQuestion)
	coffeeDeliveredAt := t0.Add(6 * time.Second)
	coffeeReply := createAIReplyTurnReply(t, db, conversation, turn, 1, "coffee-reply", coffeeDeliveredAt)
	if err := db.Create(&models.ChannelMessageOutbox{
		TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID, MessageID: coffeeReply.ID,
		SendStatus: string(enums.ChannelMessageOutboxStatusSent), SentAt: &coffeeDeliveredAt,
		AuditFields: models.AuditFields{CreatedAt: coffeeDeliveredAt, UpdatedAt: coffeeDeliveredAt},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"status": enums.AIReplyTurnStatusDelivered, "last_committed_version": 1,
		"last_delivered_version": 1, "last_delivered_at": coffeeDeliveredAt,
	}); err != nil {
		t.Fatal(err)
	}

	breakfastQuestion := createAIReplyTurnCustomerMessage(t, db, conversation, "breakfast-question", "早餐几点", t0.Add(5*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, breakfastQuestion)
	breakfastReply := createAIReplyTurnReply(t, db, conversation, turn, 2, "breakfast-reply", t0.Add(8*time.Second))
	if err := db.Model(breakfastReply).Update("content", "早餐从早上七点开始。 ").Error; err != nil {
		t.Fatal(err)
	}
	breakfastRetryAt := time.Now().Add(time.Hour)
	breakfastOutbox := &models.ChannelMessageOutbox{
		TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID, MessageID: breakfastReply.ID,
		SendStatus: string(enums.ChannelMessageOutboxStatusFailed), NextRetryAt: &breakfastRetryAt,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(breakfastOutbox).Error; err != nil {
		t.Fatal(err)
	}
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"status": enums.AIReplyTurnStatusCommitted, "last_committed_version": 2,
		"last_committed_request_id": breakfastReply.RequestID,
	}); err != nil {
		t.Fatal(err)
	}

	lateCoffee := createAIReplyTurnCustomerMessage(t, db, conversation, "late-coffee-question", "有咖啡吗。", t0.Add(4*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, lateCoffee)
	coverage := AIReplyTurnService.FindCoverage(aiReplyTurnTestJob(conversation, lateCoffee), lateCoffee, turn)
	if coverage == nil || coverage.CoveredByMessageID != coffeeReply.ID || coverage.ReasonCode != "covered_by_inflight_reply" {
		t.Fatalf("coffee duplicate used the wrong reply batch: coverage=%+v coffee=%d breakfast=%d", coverage, coffeeReply.ID, breakfastReply.ID)
	}
	unchangedBreakfastOutbox := repositories.ChannelMessageOutboxRepository.GetInTenant(db, breakfastOutbox.ID, breakfastOutbox.TenantID)
	if unchangedBreakfastOutbox == nil || unchangedBreakfastOutbox.NextRetryAt == nil || !unchangedBreakfastOutbox.NextRetryAt.Equal(breakfastRetryAt) {
		t.Fatalf("unrelated breakfast outbox was modified: before=%s after=%+v", breakfastRetryAt, unchangedBreakfastOutbox)
	}
}

func TestAIReplyTurnLateDifferentQuestionIsNotSuppressed(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-20 * time.Second).Truncate(time.Second)
	first := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-1", "有咖啡吗", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, first)
	deliveredAt := t0.Add(7 * time.Second)
	reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "reply-1", deliveredAt)
	if err := db.Create(&models.ChannelMessageOutbox{
		TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID, MessageID: reply.ID,
		SendStatus: string(enums.ChannelMessageOutboxStatusSent), SentAt: &deliveredAt,
		AuditFields: models.AuditFields{CreatedAt: deliveredAt, UpdatedAt: deliveredAt},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"status": enums.AIReplyTurnStatusDelivered, "last_committed_version": 1,
		"last_delivered_version": 1, "last_delivered_at": deliveredAt,
	}); err != nil {
		t.Fatal(err)
	}

	late := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-2", "早餐几点", t0.Add(5*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, late)
	if coverage := AIReplyTurnService.FindCoverage(aiReplyTurnTestJob(conversation, late), late, turn); coverage != nil {
		t.Fatalf("different late question must not be suppressed: %+v", coverage)
	}
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return AIReplyTurnService.ValidatePreparedReplyDB(ctx.Tx, turn, late.RequestID, []AIOutboundMessageDraft{{
			MessageType: enums.IMMessageTypeText,
			Content:     reply.Content,
		}})
	})
	if err != nil {
		t.Fatalf("different question may legitimately share the previous answer: %v", err)
	}
	if floor := AIReplyTurnService.InputFloorVersion(*late); floor != 1 {
		t.Fatalf("input floor=%d want delivered version 1", floor)
	}

	followUp := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-3", "谢谢", deliveredAt.Add(2*time.Second))
	newTurn := assignAIReplyTurnMessage(t, db, conversation, followUp)
	if newTurn.ID == turn.ID || newTurn.Version != 1 {
		t.Fatalf("message sent after delivery must start a new turn: old=%+v new=%+v", turn, newTurn)
	}
}

func TestAIReplyTurnOutboxAndActionCancelWhenTurnVersionChanges(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	question := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-1", "有咖啡吗", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, question)
	reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "reply-pending", t0.Add(time.Second))
	outbox := &models.ChannelMessageOutbox{
		TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID, MessageID: reply.ID,
		SendStatus:  string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{CreatedAt: t0, UpdatedAt: t0},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatal(err)
	}
	action := &models.AIReplyTurnAction{
		TenantID: conversation.TenantID, TurnID: turn.ID, TaskKey: "task-coffee", ActionKey: "action-coffee-image",
		ActionType: "send_knowledge_image", ResourceType: "knowledge_image", Status: "committed", RequestedVersion: 1,
		PreparedRevision: "asset-v1", CommittedMessageID: reply.ID, OutboxID: outbox.ID,
		CreatedAt: t0, UpdatedAt: t0, CreateUserName: "test", UpdateUserName: "test",
	}
	if err := db.Create(action).Error; err != nil {
		t.Fatal(err)
	}
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"status": enums.AIReplyTurnStatusCommitted, "last_committed_version": 1,
	}); err != nil {
		t.Fatal(err)
	}

	late := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-2", "早餐几点", t0.Add(2*time.Second))
	turn = assignAIReplyTurnMessage(t, db, conversation, late)
	allowed, reason, err := AIReplyTurnService.CanDispatchOutbox(reply)
	if err != nil || allowed || reason != "cancelled_stale_turn" {
		t.Fatalf("old turn version remained deliverable allowed=%v reason=%q err=%v", allowed, reason, err)
	}
	claimed, err := ChannelMessageOutboxService.TryMarkSending(outbox.ID, outbox.TenantID)
	if err != nil || claimed {
		t.Fatalf("stale outbox claimed=%v err=%v", claimed, err)
	}
	updated := repositories.ChannelMessageOutboxRepository.GetInTenant(db, outbox.ID, outbox.TenantID)
	if updated == nil || updated.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) || updated.LastError != "cancelled_stale_turn" {
		t.Fatalf("stale outbox was not cancelled: %+v", updated)
	}
	updatedAction := repositories.AIReplyTurnActionRepository.GetByKeyInTenant(db, action.TenantID, action.TurnID, action.TaskKey, action.ActionKey)
	if updatedAction == nil || updatedAction.Status != "superseded" || updatedAction.ResultCode != "cancelled_stale_turn" {
		t.Fatalf("stale outbox action was not superseded: %+v", updatedAction)
	}
}

func TestAIReplyTurnDeliveryEvidenceDoesNotReopenInterruptedTurn(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	question := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-terminal", "有咖啡吗", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, question)
	reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "reply-terminal", t0.Add(time.Second))
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"status": enums.AIReplyTurnStatusInterrupted, "last_committed_version": 1,
	}); err != nil {
		t.Fatal(err)
	}
	deliveredAt := time.Now()
	if err := AIReplyTurnService.MarkDelivered(reply, deliveredAt); err != nil {
		t.Fatal(err)
	}
	updated := repositories.AIReplyTurnRepository.GetInTenant(db, turn.ID, turn.TenantID)
	if updated == nil || updated.Status != enums.AIReplyTurnStatusInterrupted || updated.LastDeliveredVersion != 1 ||
		updated.LastDeliveredAt == nil {
		t.Fatalf("terminal turn delivery evidence mismatch: %+v", updated)
	}
}

func TestAIReplyTurnOutboxStopsAfterHumanRoute(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	question := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-human", "有咖啡吗", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, question)
	reply := createAIReplyTurnReply(t, db, conversation, turn, 1, "reply-human", t0.Add(time.Second))
	outbox := &models.ChannelMessageOutbox{
		TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID, MessageID: reply.ID, SendStatus: string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{CreatedAt: t0, UpdatedAt: t0},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatal(err)
	}
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"status": enums.AIReplyTurnStatusCommitted, "last_committed_version": 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.ConversationRouteState{}).Where("conversation_id = ?", conversation.ID).Updates(map[string]any{
		"route_status": enums.ConversationRouteStatusHQAgentDeskPending,
		"route_target": "hq_agentdesk",
	}).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := ChannelMessageOutboxService.TryMarkSending(outbox.ID, outbox.TenantID)
	if err != nil || claimed {
		t.Fatalf("human-route outbox claimed=%v err=%v", claimed, err)
	}
	updated := repositories.ChannelMessageOutboxRepository.GetInTenant(db, outbox.ID, outbox.TenantID)
	if updated == nil || updated.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) || updated.LastError != "cancelled_turn_inactive" {
		t.Fatalf("human-route outbox was not cancelled: %+v", updated)
	}
}

func TestAIReplyTurnCustomerRecallInvalidatesCurrentVersion(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	question := createAIReplyTurnCustomerMessage(t, db, conversation, "customer-recalled", "有咖啡吗", t0)
	turn := assignAIReplyTurnMessage(t, db, conversation, question)
	job := aiReplyTurnTestJob(conversation, question)
	job.Status = enums.AIReplyJobStatusPending
	job.TriggerKind = enums.AIReplyJobTriggerKindText
	job.ExpiresAt = time.Now().Add(15 * time.Minute)
	job.AuditFields = models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(job).Error; err != nil {
		t.Fatal(err)
	}
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return AIReplyTurnService.InvalidateCustomerRecallDB(ctx.Tx, conversation, question)
	}); err != nil {
		t.Fatal(err)
	}
	updatedTurn := repositories.AIReplyTurnRepository.GetInTenant(db, turn.ID, turn.TenantID)
	if updatedTurn == nil || updatedTurn.Status != enums.AIReplyTurnStatusInterrupted || updatedTurn.Version != 2 {
		t.Fatalf("recalled turn was not interrupted: %+v", updatedTurn)
	}
	updatedConversation := repositories.ConversationRepository.GetInTenant(db, conversation.ID, conversation.TenantID)
	if updatedConversation == nil || updatedConversation.CurrentAIReplyTurnID != 0 {
		t.Fatalf("recalled turn remained current: %+v", updatedConversation)
	}
	updatedJob := repositories.AIReplyJobRepository.GetInTenant(db, job.ID, job.TenantID)
	if updatedJob == nil || updatedJob.Status != enums.AIReplyJobStatusSuperseded {
		t.Fatalf("recalled turn job was not superseded: %+v", updatedJob)
	}
}

func setupAIReplyTurnTestDB(t *testing.T) (*gorm.DB, *models.Conversation) {
	t.Helper()
	t.Setenv("AI_REPLY_TURN_COORDINATOR_ENABLED", "true")
	t.Setenv("AI_REPLY_TURN_COORDINATOR_BINDING_IDS", "")
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Store{}, &models.StoreStaffBinding{}, &models.Channel{}, &models.WxWorkProtocolInstance{},
		&models.Conversation{}, &models.ConversationRouteState{}, &models.WxWorkKFConversation{},
		&models.Message{}, &models.AIReplyTurn{}, &models.AIReplyTurnTask{}, &models.AIReplyTurnAction{}, &models.AIReplyJob{}, &models.ChannelMessageOutbox{},
	); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })
	now := time.Now()
	store := &models.Store{
		TenantID: 101, StoreCode: "turn-test-store", Name: "Turn Test Store", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	activeUserID := int64(9001)
	binding := &models.StoreStaffBinding{
		TenantID: 101, UserID: activeUserID, ActiveUserID: &activeUserID, StoreID: store.ID, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatal(err)
	}
	channel := &models.Channel{
		TenantID: 101, Name: "turn protocol", ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID: "turn-protocol", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: 101, Guid: "turn-test-guid", ChannelID: channel.ID, StoreID: store.ID,
		StoreStaffBindingID: binding.ID, AIReplyEnabled: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatal(err)
	}
	conversation := &models.Conversation{
		TenantID: 101, StoreID: store.ID, StoreStaffBindingID: binding.ID, ChannelID: channel.ID,
		Status: enums.IMConversationStatusAIServing, ServiceMode: enums.IMConversationServiceModeAIOnly,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ConversationRouteState{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, StoreID: store.ID,
		StoreStaffBindingID: binding.ID, WxWorkInstanceID: instance.ID, RouteStatus: enums.ConversationRouteStatusAIServing,
		RouteTarget: "ai", SessionNo: 1, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.WxWorkKFConversation{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, ChannelID: channel.ID,
		OpenKfID: "wx_protocol:" + instance.Guid, ExternalUserID: "S:turn-customer", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db, conversation
}

func createAIReplyTurnCustomerMessage(t *testing.T, db *gorm.DB, conversation *models.Conversation, clientMsgID, content string, sentAt time.Time) *models.Message {
	t.Helper()
	return createAIReplyTurnCustomerMessageAt(t, db, conversation, clientMsgID, content, sentAt, time.Now())
}

func createAIReplyTurnCustomerMessageAt(t *testing.T, db *gorm.DB, conversation *models.Conversation, clientMsgID, content string, sentAt, receivedAt time.Time) *models.Message {
	t.Helper()
	message := &models.Message{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: 1,
		RequestID: "request-" + clientMsgID, ClientMsgID: clientMsgID,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: content, SeqNo: time.Now().UnixNano(), SendStatus: enums.IMMessageStatusSent, SentAt: &sentAt,
		AuditFields: models.AuditFields{CreatedAt: receivedAt, UpdatedAt: receivedAt},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	return message
}

func assignAIReplyTurnMessage(t *testing.T, db *gorm.DB, conversation *models.Conversation, message *models.Message) *models.AIReplyTurn {
	t.Helper()
	var turn *models.AIReplyTurn
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		var err error
		turn, _, err = AIReplyTurnService.AssignCustomerMessageDB(ctx.Tx, conversation, message)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if turn == nil {
		t.Fatal("AI reply turn was not assigned")
	}
	return turn
}

func createAIReplyTurnReply(t *testing.T, db *gorm.DB, conversation *models.Conversation, turn *models.AIReplyTurn, version int, clientMsgID string, sentAt time.Time) *models.Message {
	t.Helper()
	message := &models.Message{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: 1,
		RequestID: "request-" + clientMsgID, ClientMsgID: "ai_reply_" + clientMsgID,
		SenderType: enums.IMSenderTypeAI, SenderID: 1, MessageType: enums.IMMessageTypeText,
		Content: "酒店提供免费速溶咖啡。", SeqNo: time.Now().UnixNano(), SendStatus: enums.IMMessageStatusSent,
		OutboundChannelType: enums.ChannelTypeWxWorkProtocol,
		AIReplyTurnID:       turn.ID, AIReplyTurnVersion: version, SentAt: &sentAt,
		AuditFields: models.AuditFields{CreatedAt: sentAt, UpdatedAt: sentAt},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	return message
}

func aiReplyTurnTestJob(conversation *models.Conversation, message *models.Message) *models.AIReplyJob {
	return &models.AIReplyJob{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, MessageID: message.ID,
		SessionNo: message.SessionNo, StoreID: conversation.StoreID, StoreStaffBindingID: conversation.StoreStaffBindingID,
		TurnID: message.AIReplyTurnID, TurnVersion: message.AIReplyTurnVersion, RequestID: message.RequestID,
	}
}

func createCommittedAIReplyTurnTask(
	t *testing.T,
	db *gorm.DB,
	turn *models.AIReplyTurn,
	source *models.Message,
	reply *models.Message,
	taskKey string,
	introducedVersion int,
	subIntent string,
) *models.AIReplyTurnTask {
	t.Helper()
	task := &models.AIReplyTurnTask{
		TenantID: turn.TenantID, ConversationID: turn.ConversationID, SessionNo: turn.SessionNo,
		TurnID: turn.ID, IntroducedVersion: introducedVersion, SourceMessageID: source.ID,
		TaskKey: taskKey, SequenceNo: introducedVersion, TaskType: enums.AIReplyTurnTaskTypeKnowledge,
		Intent: "hotel_info", SubIntent: subIntent, QuestionFingerprint: taskKey,
		Stage: enums.AIReplyTurnTaskStageDelivery, Status: enums.AIReplyTurnTaskStatusCommitted,
		CommittedMessageID: reply.ID, ResultCode: "committed",
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

func createPendingAIReplyOutbox(t *testing.T, db *gorm.DB, conversation *models.Conversation, reply *models.Message, now time.Time) *models.ChannelMessageOutbox {
	t.Helper()
	outbox := &models.ChannelMessageOutbox{
		TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID, MessageID: reply.ID, SendStatus: string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatal(err)
	}
	return outbox
}

func aiReplyTaskSourceBindingsJSON(t *testing.T, primary *models.Message, contexts ...*models.Message) string {
	t.Helper()
	bindings := contracts.TaskSourceBindingsV1{
		SchemaVersion:    contracts.TaskSourceBindingsV1SchemaVersion,
		PrimaryMessageID: primary.ID,
	}
	for _, message := range append([]*models.Message{primary}, contexts...) {
		bindings.Bindings = append(bindings.Bindings, contracts.TaskSourceBindingItemV1{
			MessageID: message.ID, Start: 0, End: len([]rune(message.Content)), ObservationMessageIDs: []int64{},
		})
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
