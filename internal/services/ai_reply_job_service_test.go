package services

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

type aiReplyJobTestFixture struct {
	db           *gorm.DB
	service      *aiReplyJobService
	conversation *models.Conversation
	message      *models.Message
	job          *models.AIReplyJob
	instance     *models.WxWorkProtocolInstance
}

func TestAIReplyJobModelDoesNotPersistRuntimeContent(t *testing.T) {
	modelType := reflect.TypeOf(models.AIReplyJob{})
	for _, forbidden := range []string{"Content", "Payload", "Prompt", "ModelOutput", "APIKey", "Fingerprint", "Ciphertext"} {
		if _, ok := modelType.FieldByName(forbidden); ok {
			t.Fatalf("AIReplyJob must not persist %s", forbidden)
		}
	}
}

func TestAIReplyJobEnqueueIsUniqueAndKeepsBindingScope(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "早餐几点")
	duplicate, created, err := fixture.service.EnsureForMessage(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created || duplicate == nil || duplicate.ID != fixture.job.ID {
		t.Fatalf("duplicate enqueue created=%v item=%#v original=%#v", created, duplicate, fixture.job)
	}
	if duplicate.TenantID != fixture.conversation.TenantID || duplicate.StoreID != fixture.conversation.StoreID ||
		duplicate.StoreStaffBindingID != fixture.conversation.StoreStaffBindingID || duplicate.SessionNo != fixture.message.SessionNo ||
		duplicate.RequestID != fixture.message.RequestID || duplicate.TriggerKind != enums.AIReplyJobTriggerKindText {
		t.Fatalf("AI reply job scope mismatch: %#v", duplicate)
	}
	var count int64
	if err := fixture.db.Model(&models.AIReplyJob{}).
		Where("tenant_id = ? AND conversation_id = ? AND message_id = ?", duplicate.TenantID, duplicate.ConversationID, duplicate.MessageID).
		Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("AI reply job unique count=%d err=%v", count, err)
	}
}

func TestCustomerMessageAndAIReplyJobShareTransaction(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("ai-job-rollback")
	conversation, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&models.AIReplyJob{}); err != nil {
		t.Fatal(err)
	}
	_, err = MessageService.SendCustomerMessageWithRequestID(
		conversation.ID, "ai-job-rollback-message", enums.IMMessageTypeText, "停车在哪", "", external, "ai-job-rollback-request",
	)
	if err == nil {
		t.Fatal("missing AI reply job table must roll back the customer message")
	}
	var count int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND client_msg_id = ?", conversation.ID, "ai-job-rollback-message").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("customer message committed without AI reply job: %d", count)
	}
}

func TestDuplicateCustomerMessageRepairsMissingAIReplyJob(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("ai-job-duplicate-repair")
	conversation, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID, "ai-job-duplicate-message", enums.IMMessageTypeText, "早餐几点", "", external, "ai-job-duplicate-request",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Where("message_id = ?", message.ID).Delete(&models.AIReplyJob{}).Error; err != nil {
		t.Fatal(err)
	}
	duplicate, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID, "ai-job-duplicate-message", enums.IMMessageTypeText, "早餐几点", "", external, "ai-job-duplicate-request",
	)
	if err != nil || duplicate.ID != message.ID {
		t.Fatalf("duplicate message=%#v err=%v", duplicate, err)
	}
	job := repositories.AIReplyJobRepository.GetByMessageInTenant(db, message.TenantID, message.ConversationID, message.ID)
	if job == nil {
		t.Fatal("duplicate inbound message did not repair its missing AI reply job")
	}
}

func TestAIReplyJobCompensationOnlyScansRecentRuntimeMessages(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "停车在哪")
	if err := fixture.db.Delete(&models.AIReplyJob{}, fixture.job.ID).Error; err != nil {
		t.Fatal(err)
	}
	old := createAIReplyJobTestMessage(t, fixture.db, fixture.conversation, 2, "old-message", time.Now().Add(-16*time.Minute), false)
	historical := createAIReplyJobTestMessage(t, fixture.db, fixture.conversation, 3, "historical-message", time.Now(), true)
	repaired, err := fixture.service.RepairMissingRecent(20)
	if err != nil {
		t.Fatal(err)
	}
	if repaired != 1 {
		t.Fatalf("repaired=%d want 1", repaired)
	}
	if repositories.AIReplyJobRepository.GetByMessageInTenant(fixture.db, fixture.message.TenantID, fixture.message.ConversationID, fixture.message.ID) == nil {
		t.Fatal("recent runtime message was not repaired")
	}
	if repositories.AIReplyJobRepository.GetByMessageInTenant(fixture.db, old.TenantID, old.ConversationID, old.ID) != nil {
		t.Fatal("message older than 15 minutes must not be repaired")
	}
	if repositories.AIReplyJobRepository.GetByMessageInTenant(fixture.db, historical.TenantID, historical.ConversationID, historical.ID) != nil {
		t.Fatal("historical message must not be repaired")
	}
}

func TestAIReplyJobConcurrentClaimAndLeaseRecovery(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "早餐几点")
	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	sqlDB, err := fixture.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	now := time.Now()
	start := make(chan struct{})
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for _, owner := range []string{"worker-a", "worker-b"} {
		owner := owner
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, claimErr := repositories.AIReplyJobRepository.TryClaim(
				fixture.db, fixture.job.ID, fixture.job.TenantID, owner, now, now.Add(aiReplyJobLeaseDuration),
			)
			if claimErr != nil {
				t.Errorf("claim %s: %v", owner, claimErr)
				return
			}
			if ok {
				claimed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("concurrent claims=%d want 1", claimed.Load())
	}
	current := repositories.AIReplyJobRepository.GetInTenant(fixture.db, fixture.job.ID, fixture.job.TenantID)
	if current == nil || current.AttemptCount != 1 || current.LeaseOwner == "" {
		t.Fatalf("claimed job=%#v", current)
	}
	otherOwner := "worker-recovery"
	if ok, err := repositories.AIReplyJobRepository.TryClaim(fixture.db, current.ID, current.TenantID, otherOwner, now.Add(time.Second), now.Add(2*time.Minute)); err != nil || ok {
		t.Fatalf("active lease was stolen: ok=%v err=%v", ok, err)
	}
	if ok, err := repositories.AIReplyJobRepository.RenewLease(fixture.db, current.ID, current.TenantID, current.LeaseOwner, now.Add(time.Second), now.Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("lease renewal ok=%v err=%v", ok, err)
	}
	recoverAt := now.Add(3 * time.Minute)
	if ok, err := repositories.AIReplyJobRepository.TryClaim(fixture.db, current.ID, current.TenantID, otherOwner, recoverAt, recoverAt.Add(aiReplyJobLeaseDuration)); err != nil || !ok {
		t.Fatalf("expired lease recovery ok=%v err=%v", ok, err)
	}
	if ok, err := repositories.AIReplyJobRepository.RenewLease(fixture.db, current.ID, current.TenantID, current.LeaseOwner, recoverAt, recoverAt.Add(aiReplyJobLeaseDuration)); err != nil || ok {
		t.Fatalf("old owner renewed recovered lease: ok=%v err=%v", ok, err)
	}
}

func TestAIReplyJobProcessDueDispatchesWithoutWaiting(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "早餐几点")
	started := make(chan struct{})
	release := make(chan struct{})
	setAIReplyJobTestHook(t, func(ctx context.Context, _ models.Conversation, _ models.Message) (AIReplyExecutionResult, error) {
		close(started)
		select {
		case <-release:
			return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted, ReasonCode: "runtime_completed"}, nil
		case <-ctx.Done():
			return AIReplyExecutionResult{}, ctx.Err()
		}
	})
	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)

	if claimed := fixture.service.ProcessDue(1); claimed != 1 {
		t.Fatalf("claimed jobs=%d want 1", claimed)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatched worker did not start")
	}
	current := repositories.AIReplyJobRepository.GetInTenant(fixture.db, fixture.job.ID, fixture.job.TenantID)
	if current == nil || current.Status != enums.AIReplyJobStatusProcessing {
		t.Fatalf("ProcessDue must return while worker is active, job=%#v", current)
	}

	close(release)
	waitForAIReplyJobStatus(t, fixture.db, fixture.job, enums.AIReplyJobStatusCompleted)
}

func TestAIReplyJobNewerMessageCancelsActiveWorkerAndSupersedes(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "早餐几点")
	started := make(chan struct{})
	setAIReplyJobTestHook(t, func(ctx context.Context, _ models.Conversation, _ models.Message) (AIReplyExecutionResult, error) {
		close(started)
		<-ctx.Done()
		return AIReplyExecutionResult{}, ctx.Err()
	})
	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	if claimed := fixture.service.ProcessDue(1); claimed != 1 {
		t.Fatalf("claimed jobs=%d want 1", claimed)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("active worker did not reach runtime")
	}

	newer := createAIReplyJobTestMessage(t, fixture.db, fixture.conversation, 2, "newer-active-message", time.Now(), false)
	fixture.service.NotifyNewerMessage(fixture.conversation.ID, newer.ID)
	current := waitForAIReplyJobStatus(t, fixture.db, fixture.job, enums.AIReplyJobStatusSuperseded)
	if current.ResultCode != "newer_message" || current.AttemptCount != 1 || current.NextRetryAt != nil {
		t.Fatalf("cancelled stale job must be superseded without retry, job=%#v", current)
	}
}

func TestAIReplyJobProcessDueNeverExceedsWorkerLimit(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "第一条消息")
	jobs := []*models.AIReplyJob{fixture.job}
	for ordinal := 2; ordinal <= aiReplyJobMaxConcurrency+1; ordinal++ {
		jobs = append(jobs, createAIReplyJobSibling(t, fixture, ordinal))
	}
	for _, job := range jobs {
		makeAIReplyJobDue(t, fixture.db, job.ID)
	}

	var active atomic.Int32
	var maxActive atomic.Int32
	started := make(chan struct{}, len(jobs))
	release := make(chan struct{})
	setAIReplyJobTestHook(t, func(ctx context.Context, _ models.Conversation, _ models.Message) (AIReplyExecutionResult, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted, ReasonCode: "runtime_completed"}, nil
		case <-ctx.Done():
			return AIReplyExecutionResult{}, ctx.Err()
		}
	})

	if claimed := fixture.service.ProcessDue(len(jobs)); claimed != aiReplyJobMaxConcurrency {
		t.Fatalf("first dispatch claimed=%d want %d", claimed, aiReplyJobMaxConcurrency)
	}
	for index := 0; index < aiReplyJobMaxConcurrency; index++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("worker %d did not start", index+1)
		}
	}
	if claimed := fixture.service.ProcessDue(len(jobs)); claimed != 0 {
		t.Fatalf("dispatch with all slots occupied claimed=%d want 0", claimed)
	}
	if got := maxActive.Load(); got != aiReplyJobMaxConcurrency {
		t.Fatalf("max active workers=%d want %d", got, aiReplyJobMaxConcurrency)
	}

	close(release)
	waitForAIReplyJobTerminalCount(t, fixture.db, len(jobs)-1)
	if claimed := fixture.service.ProcessDue(len(jobs)); claimed != 1 {
		t.Fatalf("remaining dispatch claimed=%d want 1", claimed)
	}
	waitForAIReplyJobTerminalCount(t, fixture.db, len(jobs))
	if got := maxActive.Load(); got > aiReplyJobMaxConcurrency {
		t.Fatalf("max active workers=%d exceeds %d", got, aiReplyJobMaxConcurrency)
	}
}

func TestAIReplyJobStructuredRuntimeResults(t *testing.T) {
	tests := []struct {
		name        string
		result      AIReplyExecutionResult
		wantStatus  enums.AIReplyJobStatus
		wantAttempt int
	}{
		{name: "completed", result: AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted, ReasonCode: "runtime_completed"}, wantStatus: enums.AIReplyJobStatusCompleted, wantAttempt: 1},
		{name: "skipped", result: AIReplyExecutionResult{Status: AIReplyExecutionStatusSkipped, ReasonCode: "runtime_not_eligible"}, wantStatus: enums.AIReplyJobStatusSkipped, wantAttempt: 1},
		{name: "superseded", result: AIReplyExecutionResult{Status: AIReplyExecutionStatusSuperseded, ReasonCode: "newer_message"}, wantStatus: enums.AIReplyJobStatusSuperseded, wantAttempt: 1},
		{name: "deferred", result: AIReplyExecutionResult{Status: AIReplyExecutionStatusDeferred, ReasonCode: "waiting_media_understanding"}, wantStatus: enums.AIReplyJobStatusRetry, wantAttempt: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "早餐几点")
			setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
				return tt.result, nil
			})
			makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
			current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current == nil || current.Status != tt.wantStatus || current.AttemptCount != tt.wantAttempt {
				t.Fatalf("job=%#v want status=%s attempts=%d", current, tt.wantStatus, tt.wantAttempt)
			}
		})
	}
}

func TestAIReplyJobScopeCorruptionFailsWithoutRuntimeOrDispatch(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "停车在哪")
	var runtimeCalls atomic.Int32
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		runtimeCalls.Add(1)
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted}, nil
	})
	var dispatchCalls atomic.Int32
	fixture.service.humanDispatch = func(*aiReplyJobExecutionState, *models.AIReplyJob, string) error {
		dispatchCalls.Add(1)
		return nil
	}
	if err := fixture.db.Model(&models.Conversation{}).Where("id = ?", fixture.conversation.ID).
		Update("store_id", fixture.conversation.StoreID+1000).Error; err != nil {
		t.Fatal(err)
	}
	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != enums.AIReplyJobStatusFailed || current.ResultCode != "scope_invalid" {
		t.Fatalf("scope-corrupt job=%#v", current)
	}
	if runtimeCalls.Load() != 0 || dispatchCalls.Load() != 0 {
		t.Fatalf("scope corruption runtime=%d dispatch=%d", runtimeCalls.Load(), dispatchCalls.Load())
	}
}

func TestAIReplyJobSkipsDisabledAIAndClosedConversation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*aiReplyJobTestFixture)
		code   string
	}{
		{name: "disabled AI", mutate: func(f *aiReplyJobTestFixture) {
			if err := f.db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", f.instance.ID).Update("ai_reply_enabled", false).Error; err != nil {
				t.Fatal(err)
			}
		}, code: "ai_reply_disabled"},
		{name: "closed conversation", mutate: func(f *aiReplyJobTestFixture) {
			if err := f.db.Model(&models.Conversation{}).Where("id = ?", f.conversation.ID).Update("status", enums.IMConversationStatusClosed).Error; err != nil {
				t.Fatal(err)
			}
		}, code: "conversation_closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "早餐几点")
			runtimeCalled := false
			setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
				runtimeCalled = true
				return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted}, nil
			})
			tt.mutate(fixture)
			makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
			current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current == nil || current.Status != enums.AIReplyJobStatusSkipped || current.ResultCode != tt.code || runtimeCalled {
				t.Fatalf("job=%#v runtimeCalled=%v", current, runtimeCalled)
			}
		})
	}
}

func TestAIReplyJobNewerMessageSupersedesWithoutRuntime(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "早餐几点")
	createAIReplyJobTestMessage(t, fixture.db, fixture.conversation, 2, "newer-message", time.Now().Add(time.Second), false)
	runtimeCalled := false
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		runtimeCalled = true
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted}, nil
	})
	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != enums.AIReplyJobStatusSuperseded || current.ResultCode != "newer_message" || runtimeCalled {
		t.Fatalf("job=%#v runtimeCalled=%v", current, runtimeCalled)
	}
}

func TestAIReplyJobRecoversCommittedReplyWithoutRepeatingRuntime(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "早餐几点")
	now := time.Now().Add(time.Second)
	reply := &models.Message{
		TenantID: fixture.message.TenantID, ConversationID: fixture.message.ConversationID, SessionNo: fixture.message.SessionNo,
		RequestID: fixture.message.RequestID, ClientMsgID: "ai_reply_" + fixture.message.ClientMsgID,
		SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "早餐七点开始",
		SeqNo: 2, SendStatus: enums.IMMessageStatusSent, SentAt: &now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := fixture.db.Create(reply).Error; err != nil {
		t.Fatal(err)
	}
	runtimeCalled := false
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		runtimeCalled = true
		return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted}, nil
	})
	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != enums.AIReplyJobStatusCompleted || current.ResultCode != "reply_already_committed" || runtimeCalled {
		t.Fatalf("job=%#v runtimeCalled=%v", current, runtimeCalled)
	}
}

func TestAIReplyJobRetryScheduleAndHumanFallback(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "空调不制冷")
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		return AIReplyExecutionResult{}, errors.New("upstream unavailable")
	})
	var dispatchCalls atomic.Int32
	fixture.service.humanDispatch = func(state *aiReplyJobExecutionState, job *models.AIReplyJob, reason string) error {
		if state == nil || state.Conversation == nil || job == nil || reason == "" {
			t.Fatal("human dispatch lost trusted job scope")
		}
		dispatchCalls.Add(1)
		return nil
	}
	for attempt, wantDelay := range aiReplyJobRetryDelays {
		makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
		startedAt := time.Now()
		current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current == nil || current.Status != enums.AIReplyJobStatusRetry || current.AttemptCount != attempt+1 || current.NextRetryAt == nil {
			t.Fatalf("attempt %d job=%#v", attempt+1, current)
		}
		if got := current.NextRetryAt.Sub(startedAt); got < wantDelay-time.Second || got > wantDelay+2*time.Second {
			t.Fatalf("attempt %d retry delay=%v want %v", attempt+1, got, wantDelay)
		}
	}
	makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
	current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != enums.AIReplyJobStatusFailed || current.AttemptCount != aiReplyJobMaxAttempts ||
		current.ResultCode != "retry_exhausted_human_dispatch" || dispatchCalls.Load() != 1 {
		t.Fatalf("final job=%#v dispatchCalls=%d", current, dispatchCalls.Load())
	}
}

func TestAIReplyJobExpiresIntoExistingHumanPool(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeText, "还在吗")
	var dispatchCalls atomic.Int32
	fixture.service.humanDispatch = func(*aiReplyJobExecutionState, *models.AIReplyJob, string) error {
		dispatchCalls.Add(1)
		return nil
	}
	if err := fixture.db.Model(&models.AIReplyJob{}).Where("id = ?", fixture.job.ID).Updates(map[string]any{
		"expires_at": time.Now().Add(-time.Second), "next_retry_at": time.Now().Add(-time.Second),
	}).Error; err != nil {
		t.Fatal(err)
	}
	current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != enums.AIReplyJobStatusExpired || current.ResultCode != "expired_human_dispatch" || dispatchCalls.Load() != 1 {
		t.Fatalf("expired job=%#v dispatchCalls=%d", current, dispatchCalls.Load())
	}
}

func TestAIReplyJobMediaUsesSingleDurableRuntimePath(t *testing.T) {
	t.Run("actionable voice", func(t *testing.T) {
		fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeVoice, "voice.amr")
		if err := fixture.db.Model(&models.Message{}).Where("id = ?", fixture.message.ID).Update(
			"payload", `{"mediaText":"早餐几点开始","mediaUnderstandingStatus":"understood"}`,
		).Error; err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
			calls.Add(1)
			return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted, ReasonCode: "runtime_completed"}, nil
		})
		makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
		current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
		if err != nil || current == nil || current.Status != enums.AIReplyJobStatusCompleted || calls.Load() != 1 {
			t.Fatalf("job=%#v calls=%d err=%v", current, calls.Load(), err)
		}
	})

	t.Run("non-actionable image", func(t *testing.T) {
		fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeImage, "meal.jpg")
		if err := fixture.db.Model(&models.Message{}).Where("id = ?", fixture.message.ID).Update(
			"payload", `{"mediaText":"图片里是一份普通餐食，无酒店服务诉求。","mediaUnderstandingStatus":"understood"}`,
		).Error; err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
			calls.Add(1)
			return AIReplyExecutionResult{Status: AIReplyExecutionStatusCompleted}, nil
		})
		makeAIReplyJobDue(t, fixture.db, fixture.job.ID)
		current, err := fixture.service.ProcessMessageNow(fixture.message.ID)
		if err != nil || current == nil || current.Status != enums.AIReplyJobStatusSkipped ||
			current.ResultCode != "media_without_actionable_request" || calls.Load() != 0 {
			t.Fatalf("job=%#v calls=%d err=%v", current, calls.Load(), err)
		}
	})
}

func TestVoiceTranscriptionFailureNoticeIsIdempotent(t *testing.T) {
	fixture := setupAIReplyJobFixture(t, enums.IMMessageTypeVoice, "voice.amr")
	MediaUnderstandingService.sendVoiceTranscriptionFailedReply(fixture.message)
	MediaUnderstandingService.sendVoiceTranscriptionFailedReply(fixture.message)
	var count int64
	if err := fixture.db.Model(&models.Message{}).
		Where("conversation_id = ? AND client_msg_id = ?", fixture.conversation.ID, "voice_transcription_failed_"+formatInt64(fixture.message.ID)).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("voice failure notices=%d want 1", count)
	}
}

func setupAIReplyJobFixture(t *testing.T, messageType enums.IMMessageType, content string) *aiReplyJobTestFixture {
	t.Helper()
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	user := &models.User{TenantID: 101, Username: "ai-job-user-" + testNameKey(t.Name()), Nickname: "AI Job User", Status: enums.StatusOk}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	store := &models.Store{
		TenantID: 101, StoreCode: "ai-job-store-" + testNameKey(t.Name()), Name: "AI Job Store", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	activeUserID := user.ID
	binding := &models.StoreStaffBinding{
		TenantID: 101, UserID: user.ID, ActiveUserID: &activeUserID, StoreID: store.ID, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatal(err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: 101, StoreID: store.ID, StoreStaffBindingID: binding.ID,
		Guid: "ai-job-instance-" + testNameKey(t.Name()), AIReplyEnabled: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatal(err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation := &models.Conversation{
		TenantID: 101, StoreID: store.ID, StoreStaffBindingID: binding.ID, AIAgentID: aiAgent.ID, ChannelID: 11,
		CustomerID: 7001, CustomerName: "AI Job Customer", Status: enums.IMConversationStatusAIServing,
		ServiceMode: enums.IMConversationServiceModeAIFirst,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ConversationRouteState{
		TenantID: 101, ConversationID: conversation.ID, StoreID: store.ID, StoreStaffBindingID: binding.ID,
		WxWorkInstanceID: instance.ID, RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai", SessionNo: 1,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ConversationChannelSession{
		TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, StoreID: store.ID,
		StoreStaffBindingID: binding.ID, WxWorkInstanceID: instance.ID, ChannelID: 11,
		StartedAt: now, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	message := &models.Message{
		TenantID: 101, ConversationID: conversation.ID, SessionNo: 1,
		RequestID: "ai-job-request-" + testNameKey(t.Name()), ClientMsgID: "ai-job-message-" + testNameKey(t.Name()),
		SenderType: enums.IMSenderTypeCustomer, MessageType: messageType, Content: content,
		SeqNo: 1, SendStatus: enums.IMMessageStatusSent, SentAt: &now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatal(err)
	}
	service := newAIReplyJobService()
	job, created, err := service.EnsureForMessage(message.ID)
	if err != nil || !created || job == nil {
		t.Fatalf("create AI reply job: item=%#v created=%v err=%v", job, created, err)
	}
	return &aiReplyJobTestFixture{db: db, service: service, conversation: conversation, message: message, job: job, instance: instance}
}

func createAIReplyJobTestMessage(t *testing.T, db *gorm.DB, conversation *models.Conversation, seq int64, clientMsgID string, createdAt time.Time, historical bool) *models.Message {
	t.Helper()
	item := &models.Message{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: 1,
		RequestID: clientMsgID + "-request", ClientMsgID: clientMsgID,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "测试消息",
		SeqNo: seq, HistoricalOnly: historical, SendStatus: enums.IMMessageStatusSent, SentAt: &createdAt,
		AuditFields: models.AuditFields{CreatedAt: createdAt, UpdatedAt: createdAt},
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	return item
}

func createAIReplyJobSibling(t *testing.T, fixture *aiReplyJobTestFixture, ordinal int) *models.AIReplyJob {
	t.Helper()
	now := time.Now()
	conversation := &models.Conversation{
		TenantID: fixture.conversation.TenantID, StoreID: fixture.conversation.StoreID,
		StoreStaffBindingID: fixture.conversation.StoreStaffBindingID, AIAgentID: fixture.conversation.AIAgentID,
		ChannelID: fixture.conversation.ChannelID, CustomerID: fixture.conversation.CustomerID + int64(ordinal),
		CustomerName: fmt.Sprintf("AI Job Customer %d", ordinal), Status: enums.IMConversationStatusAIServing,
		ServiceMode: enums.IMConversationServiceModeAIFirst,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := fixture.db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.ConversationRouteState{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, StoreID: conversation.StoreID,
		StoreStaffBindingID: conversation.StoreStaffBindingID, WxWorkInstanceID: fixture.instance.ID,
		RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai", SessionNo: 1,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.ConversationChannelSession{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: 1, StoreID: conversation.StoreID,
		StoreStaffBindingID: conversation.StoreStaffBindingID, WxWorkInstanceID: fixture.instance.ID,
		ChannelID: conversation.ChannelID, StartedAt: now, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	message := createAIReplyJobTestMessage(t, fixture.db, conversation, 1,
		fmt.Sprintf("ai-job-sibling-%d-%s", ordinal, testNameKey(t.Name())), now, false)
	job, created, err := fixture.service.EnsureForMessage(message.ID)
	if err != nil || !created || job == nil {
		t.Fatalf("create sibling AI reply job: item=%#v created=%v err=%v", job, created, err)
	}
	return job
}

func waitForAIReplyJobStatus(t *testing.T, db *gorm.DB, job *models.AIReplyJob, want enums.AIReplyJobStatus) *models.AIReplyJob {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current := repositories.AIReplyJobRepository.GetInTenant(db, job.ID, job.TenantID)
		if current != nil && current.Status == want {
			return current
		}
		time.Sleep(10 * time.Millisecond)
	}
	current := repositories.AIReplyJobRepository.GetInTenant(db, job.ID, job.TenantID)
	t.Fatalf("job %d status=%v want %s", job.ID, func() any {
		if current == nil {
			return nil
		}
		return current.Status
	}(), want)
	return nil
}

func waitForAIReplyJobTerminalCount(t *testing.T, db *gorm.DB, want int) {
	t.Helper()
	terminal := []enums.AIReplyJobStatus{
		enums.AIReplyJobStatusCompleted, enums.AIReplyJobStatusSkipped, enums.AIReplyJobStatusSuperseded,
		enums.AIReplyJobStatusExpired, enums.AIReplyJobStatusFailed,
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		if err := db.Model(&models.AIReplyJob{}).Where("status IN ?", terminal).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == int64(want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("terminal AI reply jobs did not reach %d", want)
}

func makeAIReplyJobDue(t *testing.T, db *gorm.DB, jobID int64) {
	t.Helper()
	if err := db.Model(&models.AIReplyJob{}).Where("id = ?", jobID).Update("next_retry_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
}

func setAIReplyJobTestHook(t *testing.T, hook func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error)) {
	t.Helper()
	previous := TriggerAIReplySyncHook
	TriggerAIReplySyncHook = hook
	t.Cleanup(func() { TriggerAIReplySyncHook = previous })
}

func testNameKey(value string) string {
	ret := make([]rune, 0, len(value))
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			ret = append(ret, r)
		}
	}
	return string(ret)
}

func formatInt64(value int64) string {
	return fmt.Sprintf("%d", value)
}
