package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// 任务 B 验收：媒体 ready/failed/empty 后唤醒 pending/retry Job（只提前 next_retry_at），
// 不打断 processing，无有效 Job 时补建。
func TestWakeAfterMediaAnalysisAdvancesPendingJob(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeImage, "图片")
	defer sqls.SetDB(fixture.db)
	now := time.Now()
	future := now.Add(5 * time.Minute)
	repositories.AIReplyJobRepository.UpdateColumnsInTenant(fixture.db, fixture.job.ID, fixture.job.TenantID, map[string]any{
		"status": enums.AIReplyJobStatusRetry, "next_retry_at": future, "updated_at": now,
	})
	if err := fixture.service.WakeAfterMediaAnalysis(fixture.message.ID); err != nil {
		t.Fatalf("wake: %v", err)
	}
	job := repositories.AIReplyJobRepository.GetInTenant(fixture.db, fixture.job.ID, fixture.job.TenantID)
	if job == nil || job.Status != enums.AIReplyJobStatusRetry {
		t.Fatalf("job status changed: %+v", job)
	}
	if job.NextRetryAt == nil || job.NextRetryAt.After(future) {
		t.Fatalf("next_retry_at not advanced: %v want before %v", job.NextRetryAt, future)
	}
}

func TestWakeAfterMediaAnalysisDoesNotInterruptProcessing(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeVoice, "语音")
	defer sqls.SetDB(fixture.db)
	now := time.Now()
	lease := now.Add(2 * time.Minute)
	repositories.AIReplyJobRepository.UpdateColumnsInTenant(fixture.db, fixture.job.ID, fixture.job.TenantID, map[string]any{
		"status": enums.AIReplyJobStatusProcessing, "next_retry_at": now.Add(time.Hour),
		"lease_owner": "worker-1", "lease_expires_at": lease, "updated_at": now,
	})
	if err := fixture.service.WakeAfterMediaAnalysis(fixture.message.ID); err != nil {
		t.Fatalf("wake: %v", err)
	}
	job := repositories.AIReplyJobRepository.GetInTenant(fixture.db, fixture.job.ID, fixture.job.TenantID)
	if job == nil || job.Status != enums.AIReplyJobStatusProcessing {
		t.Fatalf("processing job was interrupted: %+v", job)
	}
	if job.NextRetryAt == nil || !job.NextRetryAt.After(now) {
		t.Fatalf("processing next_retry_at was modified: %+v", job)
	}
}

func TestWakeAfterMediaAnalysisSkipsNonCustomerMessage(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "消息")
	defer sqls.SetDB(fixture.db)
	now := time.Now()
	aiMessage := &models.Message{
		TenantID: 101, ConversationID: fixture.conversation.ID, SessionNo: 1,
		RequestID: "ai-msg-" + testNameKey(t.Name()), ClientMsgID: "ai-client-" + testNameKey(t.Name()),
		SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "AI 回复",
		SeqNo: 2, SendStatus: enums.IMMessageStatusSent, SentAt: &now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := fixture.db.Create(aiMessage).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.WakeAfterMediaAnalysis(aiMessage.ID); err != nil {
		t.Fatalf("wake: %v", err)
	}
	// 不应崩溃也不应改变客户 Job。
	job := repositories.AIReplyJobRepository.GetInTenant(fixture.db, fixture.job.ID, fixture.job.TenantID)
	if job == nil || job.Status != enums.AIReplyJobStatusPending {
		t.Fatalf("job unexpectedly changed: %+v", job)
	}
}
