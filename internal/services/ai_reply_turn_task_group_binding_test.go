package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// 文档 §9.3：answer_group_key 绑定覆盖。
func TestBindAnswerGroupsNormalBinding(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	defer sqls.SetDB(db)
	customer := createAIReplyTurnCustomerMessage(t, db, conversation, "bind-normal", "发票抬头", time.Now())
	turn := assignAIReplyTurnMessage(t, db, conversation, customer)
	task := &models.AIReplyTurnTask{
		TenantID: turn.TenantID, ConversationID: turn.ConversationID, SessionNo: turn.SessionNo,
		TurnID: turn.ID, SourceMessageID: customer.ID, TaskKey: "bind_task_1", SequenceNo: 1,
		TaskType: enums.AIReplyTurnTaskTypeKnowledge, Stage: enums.AIReplyTurnTaskStageKnowledge,
		Status: enums.AIReplyTurnTaskStatusRunning, KnowledgeStatus: enums.AIReplyTurnTaskKnowledgeStatusPending,
		ClaimedByJobID: 900, ClaimedVersion: turn.Version,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	err := AIReplyTurnTaskService.BindAnswerGroupsDB(db, turn, 900, []AIReplyTurnTaskGroupBinding{
		{TaskKey: "bind_task_1", AnswerGroupKey: "grp_test_123"},
	}, time.Now())
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	stored := repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, turn.TenantID, turn.ID, "bind_task_1")
	if stored == nil || stored.AnswerGroupKey != "grp_test_123" {
		t.Fatalf("groupKey not persisted: %+v", stored)
	}
	// status 不得被改写。
	if stored.Status != enums.AIReplyTurnTaskStatusRunning {
		t.Fatalf("status must not change: %+v", stored)
	}
}

func TestBindAnswerGroupsWrongJobRejected(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	defer sqls.SetDB(db)
	customer := createAIReplyTurnCustomerMessage(t, db, conversation, "bind-wrong-job", "WiFi", time.Now())
	turn := assignAIReplyTurnMessage(t, db, conversation, customer)
	task := &models.AIReplyTurnTask{
		TenantID: turn.TenantID, ConversationID: turn.ConversationID, SessionNo: turn.SessionNo,
		TurnID: turn.ID, SourceMessageID: customer.ID, TaskKey: "bind_task_2", SequenceNo: 1,
		TaskType: enums.AIReplyTurnTaskTypeKnowledge, Stage: enums.AIReplyTurnTaskStageKnowledge,
		Status: enums.AIReplyTurnTaskStatusRunning, KnowledgeStatus: enums.AIReplyTurnTaskKnowledgeStatusPending,
		ClaimedByJobID: 901, ClaimedVersion: turn.Version,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	err := AIReplyTurnTaskService.BindAnswerGroupsDB(db, turn, 999, []AIReplyTurnTaskGroupBinding{
		{TaskKey: "bind_task_2", AnswerGroupKey: "grp_x"},
	}, time.Now())
	if err == nil {
		t.Fatal("wrong job must be rejected")
	}
}

func TestBindAnswerGroupsCompletedTaskNotOverwritten(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	defer sqls.SetDB(db)
	customer := createAIReplyTurnCustomerMessage(t, db, conversation, "bind-done", "你好", time.Now())
	turn := assignAIReplyTurnMessage(t, db, conversation, customer)
	task := &models.AIReplyTurnTask{
		TenantID: turn.TenantID, ConversationID: turn.ConversationID, SessionNo: turn.SessionNo,
		TurnID: turn.ID, SourceMessageID: customer.ID, TaskKey: "bind_task_3", SequenceNo: 1,
		TaskType: enums.AIReplyTurnTaskTypeText, Stage: enums.AIReplyTurnTaskStageComplete,
		Status: enums.AIReplyTurnTaskStatusDelivered, KnowledgeStatus: enums.AIReplyTurnTaskKnowledgeStatusNone,
		ClaimedByJobID: 902, ClaimedVersion: turn.Version, CommittedMessageID: 777,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	err := AIReplyTurnTaskService.BindAnswerGroupsDB(db, turn, 902, []AIReplyTurnTaskGroupBinding{
		{TaskKey: "bind_task_3", AnswerGroupKey: "grp_new"},
	}, time.Now())
	if err == nil {
		t.Fatal("delivered task binding must fail (no updatable tasks)")
	}
	stored := repositories.AIReplyTurnTaskRepository.GetByKeyInTenant(db, turn.TenantID, turn.ID, "bind_task_3")
	if stored.AnswerGroupKey != "" {
		t.Fatalf("delivered task must not be overwritten: %+v", stored)
	}
}
