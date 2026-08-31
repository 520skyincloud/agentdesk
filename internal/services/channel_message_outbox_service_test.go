package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

type manualResumeOutboxFixture struct {
	db           *gorm.DB
	aiAgent      *models.AIAgent
	conversation *models.Conversation
	source       *models.Message
	task         *models.AIManualResumeTask
	reply        *models.Message
	requestID    string
}

func prepareManualResumeOutboxFixture(t *testing.T, key string) manualResumeOutboxFixture {
	t.Helper()
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	channel := &models.Channel{
		Name:        "企微员工号-" + key,
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-" + key,
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("manual-resume-outbox-" + key)
	conversation, err := ConversationService.Create(external, channel.ID, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "等待门店同事", now); err != nil {
		t.Fatalf("EnterStoreWecomManual() error = %v", err)
	}
	source, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"manual-resume-outbox-source-"+key,
		enums.IMMessageTypeText,
		"刚才的问题还没处理",
		"",
		external,
		"req-manual-resume-outbox-source-"+key,
	)
	if err != nil {
		t.Fatalf("send source message: %v", err)
	}
	task := AIManualResumeTaskService.latestActiveTask(conversation.ID, []string{aiManualResumeTaskWaiting})
	if task == nil {
		t.Fatal("expected waiting manual resume task")
	}
	if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"task_status": aiManualResumeTaskRunning,
		"updated_at":  now,
	}).Error; err != nil {
		t.Fatalf("mark manual resume task running: %v", err)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatalf("reload manual resume task: %v", err)
	}
	requestID := manualResumeRequestID(task)
	reply := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      source.SessionNo,
		RequestID:      requestID,
		ClientMsgID:    "ai-manual-resume-outbox-" + key,
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "我继续帮你处理这个问题。",
		SeqNo:          source.SeqNo + 1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserName: "AI",
			UpdatedAt:      now,
			UpdateUserName: "AI",
		},
	}
	if err := db.Create(reply).Error; err != nil {
		t.Fatalf("create manual resume reply: %v", err)
	}
	return manualResumeOutboxFixture{
		db:           db,
		aiAgent:      aiAgent,
		conversation: conversation,
		source:       source,
		task:         task,
		reply:        reply,
		requestID:    requestID,
	}
}

func TestEnsureManualResumeMessageRepairsMissingOutbox(t *testing.T) {
	fixture := prepareManualResumeOutboxFixture(t, "missing")
	repaired, err := ChannelMessageOutboxService.ensureManualResumeMessage(fixture.conversation, fixture.reply, fixture.source.ID)
	if err != nil {
		t.Fatalf("ensureManualResumeMessage() error = %v", err)
	}
	if !repaired {
		t.Fatal("expected missing manual resume outbox to be repaired")
	}
	outbox := ChannelMessageOutboxService.GetByMessageID(enums.ChannelTypeWxWorkProtocol, fixture.reply.ID)
	if outbox == nil || outbox.SendStatus != string(enums.ChannelMessageOutboxStatusPending) {
		t.Fatalf("repaired outbox = %+v, want pending", outbox)
	}
}

func TestEnsureManualResumeMessagePreservesDeliveryStates(t *testing.T) {
	tests := []struct {
		name         string
		status       enums.ChannelMessageOutboxStatus
		wantStatus   enums.ChannelMessageOutboxStatus
		wantRepaired bool
	}{
		{name: "pending", status: enums.ChannelMessageOutboxStatusPending, wantStatus: enums.ChannelMessageOutboxStatusPending},
		{name: "sending", status: enums.ChannelMessageOutboxStatusSending, wantStatus: enums.ChannelMessageOutboxStatusSending},
		{name: "sent", status: enums.ChannelMessageOutboxStatusSent, wantStatus: enums.ChannelMessageOutboxStatusSent},
		{name: "failed", status: enums.ChannelMessageOutboxStatusFailed, wantStatus: enums.ChannelMessageOutboxStatusFailed},
		{name: "cancelled", status: enums.ChannelMessageOutboxStatusCancelled, wantStatus: enums.ChannelMessageOutboxStatusPending, wantRepaired: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := prepareManualResumeOutboxFixture(t, "state-"+tt.name)
			if _, err := ChannelMessageOutboxService.ensureManualResumeMessage(fixture.conversation, fixture.reply, fixture.source.ID); err != nil {
				t.Fatalf("create outbox: %v", err)
			}
			outbox := ChannelMessageOutboxService.GetByMessageID(enums.ChannelTypeWxWorkProtocol, fixture.reply.ID)
			if outbox == nil {
				t.Fatal("expected manual resume outbox")
			}
			if err := fixture.db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
				"send_status": string(tt.status),
				"last_error":  "existing state",
			}).Error; err != nil {
				t.Fatalf("set outbox state: %v", err)
			}
			repaired, err := ChannelMessageOutboxService.ensureManualResumeMessage(fixture.conversation, fixture.reply, fixture.source.ID)
			if err != nil {
				t.Fatalf("ensureManualResumeMessage() error = %v", err)
			}
			if repaired != tt.wantRepaired {
				t.Fatalf("repaired=%v want %v", repaired, tt.wantRepaired)
			}
			if err := fixture.db.First(outbox, outbox.ID).Error; err != nil {
				t.Fatalf("reload outbox: %v", err)
			}
			if outbox.SendStatus != string(tt.wantStatus) {
				t.Fatalf("outbox status=%q want %q", outbox.SendStatus, tt.wantStatus)
			}
			if tt.status == enums.ChannelMessageOutboxStatusFailed && outbox.LastError != "existing state" {
				t.Fatalf("failed outbox error changed to %q", outbox.LastError)
			}
		})
	}
}

func TestEnsureManualResumeMessageDoesNotReviveCancelledOutboxAfterRequestExpires(t *testing.T) {
	fixture := prepareManualResumeOutboxFixture(t, "expired")
	if _, err := ChannelMessageOutboxService.ensureManualResumeMessage(fixture.conversation, fixture.reply, fixture.source.ID); err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	outbox := ChannelMessageOutboxService.GetByMessageID(enums.ChannelTypeWxWorkProtocol, fixture.reply.ID)
	if outbox == nil {
		t.Fatal("expected manual resume outbox")
	}
	if err := fixture.db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Update("send_status", string(enums.ChannelMessageOutboxStatusCancelled)).Error; err != nil {
		t.Fatalf("cancel outbox: %v", err)
	}
	if err := fixture.db.Model(&models.AIManualResumeTask{}).Where("id = ?", fixture.task.ID).Update("task_status", aiManualResumeTaskSucceeded).Error; err != nil {
		t.Fatalf("expire manual resume task: %v", err)
	}
	repaired, err := ChannelMessageOutboxService.ensureManualResumeMessage(fixture.conversation, fixture.reply, fixture.source.ID)
	if err != nil {
		t.Fatalf("ensureManualResumeMessage() error = %v", err)
	}
	if repaired {
		t.Fatal("expired manual resume request must not revive its outbox")
	}
	if err := fixture.db.First(outbox, outbox.ID).Error; err != nil {
		t.Fatalf("reload outbox: %v", err)
	}
	if outbox.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) {
		t.Fatalf("expired request changed outbox to %q", outbox.SendStatus)
	}
}

func TestEnsureManualResumeMessageDoesNotReviveUncertainCancelledOutbox(t *testing.T) {
	fixture := prepareManualResumeOutboxFixture(t, "uncertain-cancelled")
	if _, err := ChannelMessageOutboxService.ensureManualResumeMessage(fixture.conversation, fixture.reply, fixture.source.ID); err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	outbox := ChannelMessageOutboxService.GetByMessageID(enums.ChannelTypeWxWorkProtocol, fixture.reply.ID)
	if outbox == nil {
		t.Fatal("expected manual resume outbox")
	}
	if err := fixture.db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status": string(enums.ChannelMessageOutboxStatusCancelled),
		"last_error":  channelMessageOutboxDispatchUncertainReasonPrefix + "employee takeover",
	}).Error; err != nil {
		t.Fatalf("mark uncertain cancellation: %v", err)
	}
	repaired, err := ChannelMessageOutboxService.ensureManualResumeMessage(fixture.conversation, fixture.reply, fixture.source.ID)
	if err != nil {
		t.Fatalf("ensureManualResumeMessage() error = %v", err)
	}
	if repaired {
		t.Fatal("uncertain delivery must never be revived for automatic resend")
	}
	if err := fixture.db.First(outbox, outbox.ID).Error; err != nil {
		t.Fatalf("reload outbox: %v", err)
	}
	if outbox.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) ||
		!strings.HasPrefix(outbox.LastError, channelMessageOutboxDispatchUncertainReasonPrefix) {
		t.Fatalf("uncertain cancellation changed unexpectedly: %+v", outbox)
	}
}

func TestListPendingSkipsTerminalAndFutureFailedOutbox(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	items := []*models.ChannelMessageOutbox{
		{ChannelType: enums.ChannelTypeWxWorkCLI, MessageID: 201, SendStatus: string(enums.ChannelMessageOutboxStatusPending), AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		{ChannelType: enums.ChannelTypeWxWorkCLI, MessageID: 202, SendStatus: string(enums.ChannelMessageOutboxStatusFailed), NextRetryAt: &past, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		{ChannelType: enums.ChannelTypeWxWorkCLI, MessageID: 203, SendStatus: string(enums.ChannelMessageOutboxStatusFailed), NextRetryAt: &future, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		{ChannelType: enums.ChannelTypeWxWorkCLI, MessageID: 204, SendStatus: string(enums.ChannelMessageOutboxStatusFailed), AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
	}
	for _, item := range items {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create outbox %d: %v", item.MessageID, err)
		}
	}

	ready := ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkCLI, 20)
	if len(ready) != 2 || ready[0].MessageID != 201 || ready[1].MessageID != 202 {
		t.Fatalf("ListPending()=%+v, want pending and due failed only", ready)
	}
}

func TestListPendingKeepsDeferredHandoffNoticeBehindItsAnswer(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	answer := &models.Message{
		ConversationID: 701,
		RequestID:      "req-answer-then-handoff",
		ClientMsgID:    "ai_reply_answer_before_handoff",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        "早餐时间是7:00-9:30。",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	notice := &models.Message{
		ConversationID: 701,
		RequestID:      answer.RequestID,
		ClientMsgID:    "ai_handoff_success_direct_701_801",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        DirectHandoffSuccessMessage,
		SeqNo:          2,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	for _, message := range []*models.Message{answer, notice} {
		if err := db.Create(message).Error; err != nil {
			t.Fatalf("create message: %v", err)
		}
	}
	answerOutbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkProtocol,
		ConversationID: answer.ConversationID,
		MessageID:      answer.ID,
		Payload:        `{"replyBeforeDeferredHandoff":true}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	noticeOutbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkProtocol,
		ConversationID: notice.ConversationID,
		MessageID:      notice.ID,
		Payload:        `{"aiServiceNotice":true}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	for _, outbox := range []*models.ChannelMessageOutbox{answerOutbox, noticeOutbox} {
		if err := db.Create(outbox).Error; err != nil {
			t.Fatalf("create outbox: %v", err)
		}
	}

	ready := ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkProtocol, 20)
	if len(ready) != 1 || ready[0].ID != answerOutbox.ID {
		t.Fatalf("handoff notice must wait behind its pending answer, got %+v", ready)
	}
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", answerOutbox.ID).Update("send_status", string(enums.ChannelMessageOutboxStatusSending)).Error; err != nil {
		t.Fatalf("mark answer sending: %v", err)
	}
	if ready = ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkProtocol, 20); len(ready) != 0 {
		t.Fatalf("handoff notice must wait while the answer is in external flight, got %+v", ready)
	}
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", answerOutbox.ID).Updates(map[string]any{
		"send_status": string(enums.ChannelMessageOutboxStatusSent),
		"sent_at":     now,
	}).Error; err != nil {
		t.Fatalf("mark answer sent: %v", err)
	}
	ready = ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkProtocol, 20)
	if len(ready) != 1 || ready[0].ID != noticeOutbox.ID {
		t.Fatalf("handoff notice must become dispatchable only after the answer is sent, got %+v", ready)
	}
}

func TestListPendingDoesNotBlockDeferredHandoffNoticeOnTerminalAnswerFailure(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	answer := &models.Message{
		ConversationID: 702,
		RequestID:      "req-terminal-answer-failure",
		ClientMsgID:    "ai_reply_terminal_answer_failure",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        "已确认的知识答案。",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	notice := &models.Message{
		ConversationID: answer.ConversationID,
		RequestID:      answer.RequestID,
		ClientMsgID:    "ai_handoff_success_direct_702_802",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        DirectHandoffSuccessMessage,
		SeqNo:          2,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	for _, message := range []*models.Message{answer, notice} {
		if err := db.Create(message).Error; err != nil {
			t.Fatalf("create message: %v", err)
		}
	}
	answerOutbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkProtocol,
		ConversationID: answer.ConversationID,
		MessageID:      answer.ID,
		Payload:        `{"replyBeforeDeferredHandoff":true}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusFailed),
		LastError:      "terminal delivery failure",
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	noticeOutbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkProtocol,
		ConversationID: notice.ConversationID,
		MessageID:      notice.ID,
		Payload:        `{"aiServiceNotice":true}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	for _, outbox := range []*models.ChannelMessageOutbox{answerOutbox, noticeOutbox} {
		if err := db.Create(outbox).Error; err != nil {
			t.Fatalf("create outbox: %v", err)
		}
	}
	ready := ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkProtocol, 20)
	if len(ready) != 1 || ready[0].ID != noticeOutbox.ID {
		t.Fatalf("a terminal answer failure must not strand the real handoff notice, got %+v", ready)
	}
}

func TestListPendingNeverReplaysSendingDeliveryWithoutExternalIdempotency(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	stableMessage := &models.Message{
		ID:             101,
		ConversationID: 1,
		RequestID:      "manual_resume_stable_101",
		ClientMsgID:    "ai_manual_resume_0123456789abcdef0123456789abcdef0123456789abcdef_task_abcdef0123456789_101",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        "稳定人工恢复消息",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now.Add(-10 * time.Minute), UpdatedAt: now.Add(-10 * time.Minute)},
	}
	legacyMessage := &models.Message{
		ID:             102,
		ConversationID: 1,
		RequestID:      "manual_resume_legacy_102",
		ClientMsgID:    "ai_manual_resume_legacy_text_1_102",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        "历史人工恢复消息",
		SeqNo:          2,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now.Add(-10 * time.Minute), UpdatedAt: now.Add(-10 * time.Minute)},
	}
	freshMessage := &models.Message{
		ID:             103,
		ConversationID: 1,
		RequestID:      "manual_resume_stable_103",
		ClientMsgID:    "ai_manual_resume_0123456789abcdef0123456789abcdef0123456789abcdef_task_1234567890abcdef_103",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        "仍在租约内的人工恢复消息",
		SeqNo:          3,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	for _, message := range []*models.Message{stableMessage, legacyMessage, freshMessage} {
		if err := db.Create(message).Error; err != nil {
			t.Fatalf("create message %d: %v", message.ID, err)
		}
	}

	stable := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkCLI,
		MessageID:   stableMessage.ID,
		SendStatus:  string(enums.ChannelMessageOutboxStatusSending),
		AuditFields: models.AuditFields{CreatedAt: now.Add(-10 * time.Minute), UpdatedAt: now.Add(-10 * time.Minute)},
	}
	legacy := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkCLI,
		MessageID:   legacyMessage.ID,
		SendStatus:  string(enums.ChannelMessageOutboxStatusSending),
		AuditFields: models.AuditFields{CreatedAt: now.Add(-10 * time.Minute), UpdatedAt: now.Add(-10 * time.Minute)},
	}
	fresh := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkCLI,
		MessageID:   freshMessage.ID,
		SendStatus:  string(enums.ChannelMessageOutboxStatusSending),
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	for _, outbox := range []*models.ChannelMessageOutbox{stable, legacy, fresh} {
		if err := db.Create(outbox).Error; err != nil {
			t.Fatalf("create Outbox for message %d: %v", outbox.MessageID, err)
		}
	}

	items := ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkCLI, 20)
	if len(items) != 0 {
		t.Fatalf("ListPending() = %#v, want no sending delivery to be replayed", items)
	}
	if err := db.First(stable, stable.ID).Error; err != nil {
		t.Fatalf("reload stable Outbox: %v", err)
	}
	if stable.SendStatus != string(enums.ChannelMessageOutboxStatusSending) || stable.NextRetryAt != nil || stable.RetryCount != 0 {
		t.Fatalf("expired stable manual-resume Outbox changed unexpectedly: %+v", stable)
	}
	if err := db.First(legacy, legacy.ID).Error; err != nil {
		t.Fatalf("reload legacy Outbox: %v", err)
	}
	if legacy.SendStatus != string(enums.ChannelMessageOutboxStatusSending) || legacy.NextRetryAt != nil || legacy.RetryCount != 0 {
		t.Fatalf("historical legacy sending Outbox changed unexpectedly: %+v", legacy)
	}
	if err := db.First(fresh, fresh.ID).Error; err != nil {
		t.Fatalf("reload fresh Outbox: %v", err)
	}
	if fresh.SendStatus != string(enums.ChannelMessageOutboxStatusSending) {
		t.Fatalf("fresh sending lease changed unexpectedly: %+v", fresh)
	}
}

func TestFailUnclaimedDispatchUsesOriginalAttemptCAS(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	outbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		MessageID:   301,
		SendStatus:  string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	stale := *outbox
	newerRetryAt := now.Add(5 * time.Minute)
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status":   string(enums.ChannelMessageOutboxStatusFailed),
		"retry_count":   1,
		"next_retry_at": newerRetryAt,
		"last_error":    "newer failure",
	}).Error; err != nil {
		t.Fatalf("advance outbox attempt: %v", err)
	}

	staleRetryAt := now.Add(time.Minute)
	updated, err := ChannelMessageOutboxService.failUnclaimedDispatchWithDB(db, stale, &staleRetryAt, "stale failure")
	if err != nil {
		t.Fatalf("failUnclaimedDispatchWithDB() error = %v", err)
	}
	if updated {
		t.Fatal("stale pre-claim failure must not overwrite a newer attempt")
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusFailed) || reloaded.RetryCount != 1 || reloaded.LastError != "newer failure" {
		t.Fatalf("stale failure changed newer state: %+v", reloaded)
	}
	if reloaded.NextRetryAt == nil || !reloaded.NextRetryAt.Equal(newerRetryAt) {
		t.Fatalf("stale failure changed next retry time: %+v", reloaded)
	}

	updated, err = ChannelMessageOutboxService.failUnclaimedDispatchWithDB(db, *reloaded, nil, "current failure")
	if err != nil || !updated {
		t.Fatalf("current failure updated=%v err=%v", updated, err)
	}
	reloaded = ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.RetryCount != 2 || reloaded.NextRetryAt != nil || reloaded.LastError != "current failure" {
		t.Fatalf("current attempt was not failed exactly once: %+v", reloaded)
	}
}

func TestClaimForDispatchRejectsStaleAttempt(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	outbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		MessageID:   302,
		SendStatus:  string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	stale := *outbox
	nextRetryAt := now.Add(time.Minute)
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status":   string(enums.ChannelMessageOutboxStatusFailed),
		"retry_count":   1,
		"next_retry_at": nextRetryAt,
	}).Error; err != nil {
		t.Fatalf("advance outbox attempt: %v", err)
	}

	claimed, err := ChannelMessageOutboxService.ClaimForDispatch(stale, nil)
	if err != nil {
		t.Fatalf("ClaimForDispatch(stale) error = %v", err)
	}
	if claimed {
		t.Fatal("stale worker must not claim a newer failed attempt")
	}
	current := ChannelMessageOutboxService.Get(outbox.ID)
	if current == nil || current.SendStatus != string(enums.ChannelMessageOutboxStatusFailed) || current.RetryCount != 1 {
		t.Fatalf("stale claim changed current state: %+v", current)
	}

	claimed, err = ChannelMessageOutboxService.ClaimForDispatch(*current, nil)
	if err != nil || !claimed {
		t.Fatalf("ClaimForDispatch(current) claimed=%v err=%v", claimed, err)
	}
	current = ChannelMessageOutboxService.Get(outbox.ID)
	if current == nil || current.SendStatus != string(enums.ChannelMessageOutboxStatusSending) || current.RetryCount != 1 {
		t.Fatalf("current attempt was not claimed: %+v", current)
	}
}

func TestTryMarkSendingRejectsStaleStoreRoomAttempt(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	outbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		MessageID:   -303,
		SendStatus:  string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create store-room outbox: %v", err)
	}
	stale := *outbox
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status": string(enums.ChannelMessageOutboxStatusFailed),
		"retry_count": 1,
	}).Error; err != nil {
		t.Fatalf("advance store-room attempt: %v", err)
	}

	claimed, err := ChannelMessageOutboxService.TryMarkSending(stale)
	if err != nil {
		t.Fatalf("TryMarkSending(stale) error = %v", err)
	}
	if claimed {
		t.Fatal("stale store-room worker must not reclaim a newer attempt")
	}
	current := ChannelMessageOutboxService.Get(outbox.ID)
	claimed, err = ChannelMessageOutboxService.TryMarkSending(*current)
	if err != nil || !claimed {
		t.Fatalf("TryMarkSending(current) claimed=%v err=%v", claimed, err)
	}
}

func TestRevalidateClaimedForDispatchStopsAfterHumanTakeover(t *testing.T) {
	fixture := prepareManualResumeOutboxFixture(t, "revalidate-human")
	if _, err := ChannelMessageOutboxService.ensureManualResumeMessage(fixture.conversation, fixture.reply, fixture.source.ID); err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	outbox := ChannelMessageOutboxService.GetByMessageID(enums.ChannelTypeWxWorkProtocol, fixture.reply.ID)
	if outbox == nil {
		t.Fatal("expected outbox")
	}
	fixture.reply.RequestID = "ordinary_ai_reply"
	fixture.reply.ClientMsgID = "ordinary-ai-revalidate"
	if err := fixture.db.Model(&models.Message{}).Where("id = ?", fixture.reply.ID).Updates(map[string]any{
		"request_id":    fixture.reply.RequestID,
		"client_msg_id": fixture.reply.ClientMsgID,
	}).Error; err != nil {
		t.Fatalf("make reply ordinary: %v", err)
	}
	if err := ConversationRouteService.RestoreAI(fixture.conversation.ID, "prepare dispatch test", time.Now()); err != nil {
		t.Fatalf("RestoreAI() error = %v", err)
	}
	claimed, err := ChannelMessageOutboxService.ClaimForDispatch(*outbox, fixture.reply)
	if err != nil || !claimed {
		t.Fatalf("ClaimForDispatch() claimed=%v err=%v", claimed, err)
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(fixture.conversation.ID, "employee takeover", time.Now()); err != nil {
		t.Fatalf("EnterStoreWecomManual() error = %v", err)
	}
	allowed, err := ChannelMessageOutboxService.RevalidateClaimedForDispatch(*outbox, fixture.reply)
	if err != nil {
		t.Fatalf("RevalidateClaimedForDispatch() error = %v", err)
	}
	if allowed {
		t.Fatal("claimed ordinary AI delivery must stop after persisted human takeover")
	}
	if err := fixture.db.First(outbox, outbox.ID).Error; err != nil {
		t.Fatalf("reload outbox: %v", err)
	}
	if outbox.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) {
		t.Fatalf("outbox status=%q want cancelled", outbox.SendStatus)
	}
}

func TestDeferredHandoffReplySurvivesAIRouteEntryButEmployeeStillWins(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	channel := &models.Channel{
		Name:        "deferred-handoff-outbox",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "deferred-handoff-outbox",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create deferred handoff channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("deferred-handoff-outbox"), channel.ID, aiAgent.ID)
	if err != nil {
		t.Fatalf("create deferred handoff conversation: %v", err)
	}
	message, err := MessageService.sendValidatedMessageWithOptions(
		conversation,
		enums.IMSenderTypeAI,
		aiAgent.ID,
		"deferred-answer-before-handoff",
		enums.IMMessageTypeText,
		"早餐时间是7:00-9:30。",
		"",
		&dto.AuthPrincipal{Username: "AI"},
		nil,
		"req-deferred-answer-before-handoff",
		sendMessageOptions{skipOutbound: true},
	)
	if err != nil {
		t.Fatalf("create deferred sibling answer: %v", err)
	}
	if handled, err := MessageService.ensureOutboundChannelMessage(conversation, message); err != nil || !handled {
		t.Fatalf("enqueue deferred sibling answer handled=%v err=%v", handled, err)
	}
	outbox := ChannelMessageOutboxService.GetByMessageID(enums.ChannelTypeWxWorkProtocol, message.ID)
	if outbox == nil {
		t.Fatal("expected deferred sibling Outbox")
	}
	staleBeforeMarker := *outbox
	if err := ChannelMessageOutboxService.MarkReplyBeforeDeferredHandoff(conversation.ID, message.RequestID); err != nil {
		t.Fatalf("mark deferred sibling answer: %v", err)
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "AI deferred handoff", now); err != nil {
		t.Fatalf("enter deferred manual route: %v", err)
	}
	if err := db.First(outbox, outbox.ID).Error; err != nil {
		t.Fatalf("reload preserved Outbox: %v", err)
	}
	if outbox.SendStatus != string(enums.ChannelMessageOutboxStatusPending) || !ChannelMessageOutboxService.isReplyBeforeDeferredHandoff(*outbox) {
		t.Fatalf("AI route entry must preserve only the marked sibling answer: %+v", outbox)
	}
	claimed, err := ChannelMessageOutboxService.ClaimForDispatch(staleBeforeMarker, message)
	if err != nil || !claimed {
		t.Fatalf("marked answer must claim under the deferred manual route, claimed=%v err=%v", claimed, err)
	}
	allowed, err := ChannelMessageOutboxService.RevalidateClaimedForDispatch(staleBeforeMarker, message)
	if err != nil || !allowed {
		t.Fatalf("marked answer must pass pre-send revalidation, allowed=%v err=%v", allowed, err)
	}
	if _, err := MessageService.CreateExternalAgentMessageWithoutOutbox(
		conversation.ID,
		"employee-cancels-deferred-answer",
		enums.IMMessageTypeText,
		"我来处理。",
		"",
		"req-employee-cancels-deferred-answer",
	); err != nil {
		t.Fatalf("create employee takeover message: %v", err)
	}
	if err := db.First(outbox, outbox.ID).Error; err != nil {
		t.Fatalf("reload employee-cancelled Outbox: %v", err)
	}
	if outbox.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) ||
		!strings.HasPrefix(outbox.LastError, channelMessageOutboxDispatchUncertainReasonPrefix) {
		t.Fatalf("a real employee message must still cancel the marked delivery: %+v", outbox)
	}
}

func TestCompleteClaimedDispatchCannotOverwriteHumanCancellation(t *testing.T) {
	fixture := prepareManualResumeOutboxFixture(t, "complete-after-human")
	if _, err := ChannelMessageOutboxService.ensureManualResumeMessage(fixture.conversation, fixture.reply, fixture.source.ID); err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	outbox := ChannelMessageOutboxService.GetByMessageID(enums.ChannelTypeWxWorkProtocol, fixture.reply.ID)
	if outbox == nil {
		t.Fatal("expected outbox")
	}
	if err := fixture.db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status": string(enums.ChannelMessageOutboxStatusCancelled),
		"last_error":  channelMessageOutboxDispatchUncertainReasonPrefix + "employee takeover",
	}).Error; err != nil {
		t.Fatalf("cancel claimed outbox: %v", err)
	}
	completed, err := ChannelMessageOutboxService.completeClaimedDispatchWithDB(fixture.db, *outbox, time.Now())
	if err != nil {
		t.Fatalf("completeClaimedDispatchWithDB() error = %v", err)
	}
	if completed {
		t.Fatal("late success must not complete a human-cancelled outbox")
	}
	if err := fixture.db.First(outbox, outbox.ID).Error; err != nil {
		t.Fatalf("reload outbox: %v", err)
	}
	if outbox.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) || outbox.SentAt != nil || !strings.HasPrefix(outbox.LastError, channelMessageOutboxDispatchUncertainReasonPrefix) {
		t.Fatalf("late success overwrote human cancellation: %+v", outbox)
	}
}

func TestCompleteClaimedDispatchTransitionsSendingOnce(t *testing.T) {
	fixture := prepareManualResumeOutboxFixture(t, "complete-sending")
	if _, err := ChannelMessageOutboxService.ensureManualResumeMessage(fixture.conversation, fixture.reply, fixture.source.ID); err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	outbox := ChannelMessageOutboxService.GetByMessageID(enums.ChannelTypeWxWorkProtocol, fixture.reply.ID)
	if outbox == nil {
		t.Fatal("expected outbox")
	}
	if err := fixture.db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Update("send_status", string(enums.ChannelMessageOutboxStatusSending)).Error; err != nil {
		t.Fatalf("mark outbox sending: %v", err)
	}
	now := time.Now()
	completed, err := ChannelMessageOutboxService.completeClaimedDispatchWithDB(fixture.db, *outbox, now)
	if err != nil || !completed {
		t.Fatalf("first completion completed=%v err=%v", completed, err)
	}
	completed, err = ChannelMessageOutboxService.completeClaimedDispatchWithDB(fixture.db, *outbox, now.Add(time.Second))
	if err != nil {
		t.Fatalf("second completion error = %v", err)
	}
	if completed {
		t.Fatal("a claimed outbox must complete only once")
	}
}
