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

// 任务 D 验收：无未完成 Task 的 stale Turn 按证据收敛；有未完成 Task 恢复 Job。
func makeTurnStale(t *testing.T, db *gorm.DB, turn *models.AIReplyTurn) {
	t.Helper()
	past := time.Now().Add(-10 * time.Minute)
	if err := db.Model(&models.AIReplyTurn{}).Where("id = ?", turn.ID).Updates(map[string]any{
		"updated_at": past,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestReconcileStaleTurnsConvergesNoWorkTurn(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	defer sqls.SetDB(db)
	customer := createAIReplyTurnCustomerMessage(t, db, conversation, "reconcile-no-work", "你好", time.Now())
	turn := assignAIReplyTurnMessage(t, db, conversation, customer)
	// 无任何 Task：应收敛为 closed/reconciled_no_work。
	makeTurnStale(t, db, turn)
	count, err := AIReplyTurnService.ReconcileStaleTurns(10)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least one reconciled turn, got %d", count)
	}
	final := repositories.AIReplyTurnRepository.GetInTenant(db, turn.ID, turn.TenantID)
	if final == nil || final.Status != enums.AIReplyTurnStatusClosed {
		t.Fatalf("turn not closed: %+v", final)
	}
	if final.TerminalReason != "reconciled_no_work" {
		t.Fatalf("unexpected reason: %q", final.TerminalReason)
	}
}

func TestReconcileStaleTurnsConvergesDeliveredEvidence(t *testing.T) {
	db, conversation := setupAIReplyTurnTestDB(t)
	defer sqls.SetDB(db)
	customer := createAIReplyTurnCustomerMessage(t, db, conversation, "reconcile-delivered", "你好", time.Now())
	turn := assignAIReplyTurnMessage(t, db, conversation, customer)
	// delivered 证据：Task delivered + CommittedMessageID>0。
	if err := db.Create(&models.AIReplyTurnTask{
		TenantID: turn.TenantID, ConversationID: turn.ConversationID, SessionNo: turn.SessionNo,
		TurnID: turn.ID, SourceMessageID: customer.ID, TaskKey: "reconcile-delivered-task", SequenceNo: 1,
		TaskType: enums.AIReplyTurnTaskTypeText, Stage: enums.AIReplyTurnTaskStageComplete,
		Status: enums.AIReplyTurnTaskStatusDelivered, CommittedMessageID: 999,
		KnowledgeStatus: enums.AIReplyTurnTaskKnowledgeStatusNone,
		AuditFields:     models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}).Error; err != nil {
		t.Fatal(err)
	}
	makeTurnStale(t, db, turn)
	if _, err := AIReplyTurnService.ReconcileStaleTurns(10); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	final := repositories.AIReplyTurnRepository.GetInTenant(db, turn.ID, turn.TenantID)
	if final == nil || final.Status != enums.AIReplyTurnStatusDelivered {
		t.Fatalf("turn should converge to delivered: %+v", final)
	}
}

func TestReconcileStaleTurnsSkipsValidLease(t *testing.T) {
	t.Setenv("AI_REPLY_TURN_COORDINATOR_ENABLED", "true")
	t.Setenv("AI_REPLY_TURN_COORDINATOR_BINDING_IDS", "")
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "lease check")
	defer sqls.SetDB(fixture.db)
	// 创建 Turn 并设为 stale。
	turn := assignAIReplyTurnMessage(t, fixture.db, fixture.conversation, fixture.message)
	makeTurnStale(t, fixture.db, turn)
	// 给 Job 有效 lease：processing + 未过期。
	repositories.AIReplyJobRepository.UpdateColumnsInTenant(fixture.db, fixture.job.ID, fixture.job.TenantID, map[string]any{
		"status":      enums.AIReplyJobStatusProcessing,
		"lease_owner": "active-worker", "lease_expires_at": time.Now().Add(2 * time.Minute),
	})
	if _, err := AIReplyTurnService.ReconcileStaleTurns(10); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	final := repositories.AIReplyTurnRepository.GetInTenant(fixture.db, turn.ID, turn.TenantID)
	if final == nil || final.Status != enums.AIReplyTurnStatusOpen {
		t.Fatalf("turn with valid lease must be skipped: %+v", final)
	}
}
