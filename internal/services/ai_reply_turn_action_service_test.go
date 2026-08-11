package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

func TestAIReplyTurnActionLifecycleTracksCommitAndDeliveryEvidence(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	message := createAIReplyTurnCustomerMessage(t, db, conversation, "action-lifecycle", "酒店定位发我", time.Now())
	turn := assignAIReplyTurnMessage(t, db, conversation, message)
	task := ensureAIReplyTurnActionTask(t, db, turn, message, "酒店定位发我")

	actions, err := AIReplyTurnActionService.EnsureRequestedDB(db, turn, []AIReplyTurnActionInput{{
		TaskKey: task.TaskKey, ActionType: "send_location", ResourceType: "location:store",
	}})
	if err != nil || len(actions) != 1 || actions[0].Status != "requested" {
		t.Fatalf("ensure requested actions=%+v err=%v", actions, err)
	}
	prepared, err := AIReplyTurnActionService.PrepareDB(db, turn.TenantID, turn.ID, turn.Version, actions[0].ActionKey, "location-revision-1")
	if err != nil || prepared == nil || prepared.Status != "prepared" {
		t.Fatalf("prepare action=%+v err=%v", prepared, err)
	}

	reply := createAIReplyTurnReply(t, db, conversation, turn, turn.Version, "action-lifecycle-reply", time.Now())
	outbox := &models.ChannelMessageOutbox{
		TenantID: conversation.TenantID, ChannelType: enums.ChannelTypeWxWorkProtocol,
		ConversationID: conversation.ID, MessageID: reply.ID, SendStatus: "pending",
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	if err := AIReplyTurnActionService.CommitEvidenceDB(db, turn.TenantID, turn.ID, turn.Version, []AIReplyTurnActionCommitEvidence{{
		ActionKey: actions[0].ActionKey, PreparedRevision: "location-revision-1",
		MessageID: reply.ID, OutboxID: outbox.ID, At: time.Now(),
	}}); err != nil {
		t.Fatalf("commit action evidence: %v", err)
	}
	assertAIReplyTurnActionStatus(t, db, turn, task.TaskKey, actions[0].ActionKey, "committed", "")

	if err := AIReplyTurnActionService.MarkDeliveryByOutboxDB(db, turn.TenantID, outbox.ID, false, "delivery_failed", time.Now()); err != nil {
		t.Fatalf("mark delivery failed: %v", err)
	}
	assertAIReplyTurnActionStatus(t, db, turn, task.TaskKey, actions[0].ActionKey, "delivery_failed", "delivery_failed")

	deliveredAt := time.Now()
	if err := AIReplyTurnActionService.MarkDeliveryByOutboxDB(db, turn.TenantID, outbox.ID, true, "", deliveredAt); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	stored := assertAIReplyTurnActionStatus(t, db, turn, task.TaskKey, actions[0].ActionKey, "delivered", "")
	if stored.CommittedMessageID != reply.ID || stored.OutboxID != outbox.ID || stored.DeliveredAt == nil {
		t.Fatalf("delivery evidence is incomplete: %+v", stored)
	}
}

func TestAIReplyTurnActionPrepareFailureIsTerminalEvidence(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	message := createAIReplyTurnCustomerMessage(t, db, conversation, "action-failure", "给我发定位", time.Now())
	turn := assignAIReplyTurnMessage(t, db, conversation, message)
	task := ensureAIReplyTurnActionTask(t, db, turn, message, "给我发定位")
	actions, err := AIReplyTurnActionService.EnsureRequestedDB(db, turn, []AIReplyTurnActionInput{{
		TaskKey: task.TaskKey, ActionType: "send_location", ResourceType: "location:store",
	}})
	if err != nil || len(actions) != 1 {
		t.Fatalf("ensure requested actions=%+v err=%v", actions, err)
	}
	if err := AIReplyTurnActionService.FailDB(db, turn.TenantID, turn.ID, turn.Version, actions[0].ActionKey, "resource_invariant_broken", time.Now()); err != nil {
		t.Fatalf("fail action: %v", err)
	}
	assertAIReplyTurnActionStatus(t, db, turn, task.TaskKey, actions[0].ActionKey, "failed", "resource_invariant_broken")
}

func TestAIReplyTurnActionSupersededVersionCanBeRequestedAgain(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	message := createAIReplyTurnCustomerMessage(t, db, conversation, "action-rerequest", "小程序发我", time.Now())
	turn := assignAIReplyTurnMessage(t, db, conversation, message)
	task := ensureAIReplyTurnActionTask(t, db, turn, message, "小程序发我")
	input := AIReplyTurnActionInput{TaskKey: task.TaskKey, ActionType: "send_mini_program", ResourceType: "mini_program:store"}
	actions, err := AIReplyTurnActionService.EnsureRequestedDB(db, turn, []AIReplyTurnActionInput{input})
	if err != nil || len(actions) != 1 {
		t.Fatalf("ensure requested actions=%+v err=%v", actions, err)
	}

	turn.Version++
	if err := db.Model(&models.AIReplyTurn{}).Where("id = ?", turn.ID).Update("version", turn.Version).Error; err != nil {
		t.Fatalf("advance turn version: %v", err)
	}
	rerequested, err := AIReplyTurnActionService.EnsureRequestedDB(db, turn, []AIReplyTurnActionInput{input})
	if err != nil || len(rerequested) != 1 {
		t.Fatalf("re-request action=%+v err=%v", rerequested, err)
	}
	if rerequested[0].Status != "requested" || rerequested[0].RequestedVersion != turn.Version || rerequested[0].ResultCode != "" {
		t.Fatalf("re-requested action has stale evidence: %+v", rerequested[0])
	}
}

func ensureAIReplyTurnActionTask(t *testing.T, db *gorm.DB, turn *models.AIReplyTurn, message *models.Message, question string) models.AIReplyTurnTask {
	t.Helper()
	tasks, err := AIReplyTurnTaskService.EnsureTasksDB(db, turn, []AIReplyTurnTaskInput{{
		TenantID: turn.TenantID, TurnID: turn.ID, SourceMessageID: message.ID, SequenceNo: 1,
		TaskType: enums.AIReplyTurnTaskTypeResource, Intent: "store_resource", QuestionText: question,
	}})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ensure action task=%+v err=%v", tasks, err)
	}
	return tasks[0]
}

func assertAIReplyTurnActionStatus(t *testing.T, db *gorm.DB, turn *models.AIReplyTurn, taskKey, actionKey, status, resultCode string) *models.AIReplyTurnAction {
	t.Helper()
	stored := repositories.AIReplyTurnActionRepository.GetByKeyInTenant(db, turn.TenantID, turn.ID, taskKey, actionKey)
	if stored == nil || stored.Status != status || stored.ResultCode != resultCode {
		t.Fatalf("action status=%+v want status=%q result=%q", stored, status, resultCode)
	}
	return stored
}
