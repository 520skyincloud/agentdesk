package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"

	"github.com/mlogclub/simple/sqls"
)

// 任务 C 验收：旧版本已提交→允许发送；未提交→取消。
func createStaleTurnAIMessage(t *testing.T, db *gorm.DB, conversation *models.Conversation, turn *models.AIReplyTurn, version int) *models.Message {
	t.Helper()
	now := time.Now()
	message := &models.Message{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: 1,
		RequestID: "stale-ai-" + testNameKey(t.Name()), ClientMsgID: "stale-ai-client-" + testNameKey(t.Name()),
		SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "旧版本回复",
		SeqNo: 90, SendStatus: enums.IMMessageStatusSent, SentAt: &now,
		AIReplyTurnID: turn.ID, AIReplyTurnVersion: version,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	return message
}

func TestCanDispatchOutboxCommittedStaleVersionAllowed(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	defer sqls.SetDB(db)
	customer := createAIReplyTurnCustomerMessage(t, db, conversation, "stale-committed-customer", "旧版本问题", time.Now())
	turn := assignAIReplyTurnMessage(t, db, conversation, customer)
	// 升级 Turn 版本，让 AI 消息成为旧版本。
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"version": turn.Version + 1, "updated_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	aiMessage := createStaleTurnAIMessage(t, db, conversation, turn, turn.Version)
	// 有持久 Commit 证据：Task.CommittedMessageID=消息且 committed。
	task := &models.AIReplyTurnTask{
		TenantID: turn.TenantID, ConversationID: turn.ConversationID, SessionNo: turn.SessionNo,
		TurnID: turn.ID, SourceMessageID: customer.ID, TaskKey: "stale-commit-task", SequenceNo: 1,
		TaskType: enums.AIReplyTurnTaskTypeText, Stage: enums.AIReplyTurnTaskStageDelivery,
		Status: enums.AIReplyTurnTaskStatusCommitted, CommittedMessageID: aiMessage.ID,
		KnowledgeStatus: enums.AIReplyTurnTaskKnowledgeStatusNone,
		AuditFields:     models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	allowed, reason, err := AIReplyTurnService.CanDispatchOutboxDB(db, aiMessage)
	if err != nil {
		t.Fatalf("dispatch check: %v", err)
	}
	if !allowed || reason != "committed_stale_turn_dispatchable" {
		t.Fatalf("committed stale version must be dispatchable: allowed=%v reason=%q", allowed, reason)
	}
}

func TestCanDispatchOutboxUncommittedStaleVersionCancelled(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	defer sqls.SetDB(db)
	customer := createAIReplyTurnCustomerMessage(t, db, conversation, "stale-uncommitted-customer", "旧版本问题", time.Now())
	turn := assignAIReplyTurnMessage(t, db, conversation, customer)
	if err := repositories.AIReplyTurnRepository.UpdatesInTenant(db, turn.ID, turn.TenantID, map[string]any{
		"version": turn.Version + 1, "updated_at": time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	aiMessage := createStaleTurnAIMessage(t, db, conversation, turn, turn.Version)
	// 无 Task Commit 证据：必须取消。
	allowed, reason, err := AIReplyTurnService.CanDispatchOutboxDB(db, aiMessage)
	if err != nil {
		t.Fatalf("dispatch check: %v", err)
	}
	if allowed || reason != "cancelled_stale_turn" {
		t.Fatalf("uncommitted stale version must be cancelled: allowed=%v reason=%q", allowed, reason)
	}
}
