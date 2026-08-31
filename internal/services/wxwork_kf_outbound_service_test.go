package services

import (
	"errors"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	workkf "github.com/silenceper/wechat/v2/work/kf"
)

func prepareWxWorkKFChunkDispatchTest(t *testing.T, key string) (*models.ChannelMessageOutbox, *models.Message, *models.Conversation) {
	t.Helper()
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("wxwork-kf-chunks-"+key), 0, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	message := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      1,
		RequestID:      "wxwork-kf-chunks-" + key,
		ClientMsgID:    "wxwork-kf-chunks-" + key,
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeHTML,
		Content:        "第一段和第二段",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkKF,
		ConversationID: conversation.ID,
		MessageID:      message.ID,
		Payload:        `{}`,
		SendStatus:     string(enums.ChannelMessageOutboxStatusSending),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	return outbox, message, conversation
}

func TestWxWorkKFClaimedChunksFinishLogicalMessageAfterRouteChanges(t *testing.T) {
	outbox, message, conversation := prepareWxWorkKFChunkDispatchTest(t, "route-change")
	chunks := []wxWorkKFOutboundChunk{
		{MessageType: enums.IMMessageTypeText, Content: "第一段"},
		{MessageType: enums.IMMessageTypeText, Content: "第二段"},
	}
	sent := 0
	ids, completed, err := WxWorkKFOutboundService.sendClaimedOutboundChunks(outbox, message, chunks, func(_ wxWorkKFOutboundChunk, index int) (string, error) {
		sent++
		if index == 0 {
			if _, routeErr := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "员工开始接管", time.Now()); routeErr != nil {
				return "", routeErr
			}
		}
		return "wx-msg-" + string(rune('1'+index)), nil
	})
	if err != nil {
		t.Fatalf("sendClaimedOutboundChunks() error = %v", err)
	}
	if !completed || sent != 2 || len(ids) != 2 {
		t.Fatalf("logical message was truncated: completed=%v sent=%d ids=%v", completed, sent, ids)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) ||
		!strings.HasPrefix(reloaded.LastError, channelMessageOutboxDispatchUncertainReasonPrefix) {
		t.Fatalf("route change must leave the already-started delivery terminal and uncertain: %+v", reloaded)
	}
}

func TestWxWorkKFClaimedChunksDoNotRetryAfterPartialDelivery(t *testing.T) {
	outbox, message, _ := prepareWxWorkKFChunkDispatchTest(t, "partial-failure")
	chunks := []wxWorkKFOutboundChunk{
		{MessageType: enums.IMMessageTypeText, Content: "第一段"},
		{MessageType: enums.IMMessageTypeText, Content: "第二段"},
	}
	sent := 0
	ids, completed, err := WxWorkKFOutboundService.sendClaimedOutboundChunks(outbox, message, chunks, func(_ wxWorkKFOutboundChunk, index int) (string, error) {
		if index == 1 {
			return "", errors.New("second chunk failed")
		}
		sent++
		return "wx-msg-1", nil
	})
	if err != nil {
		t.Fatalf("sendClaimedOutboundChunks() error = %v", err)
	}
	if completed || sent != 1 || len(ids) != 1 {
		t.Fatalf("partial delivery result is invalid: completed=%v sent=%d ids=%v", completed, sent, ids)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) || reloaded.NextRetryAt != nil ||
		!strings.HasPrefix(reloaded.LastError, channelMessageOutboxDispatchUncertainReasonPrefix) {
		t.Fatalf("partial delivery must be terminal and uncertain: %+v", reloaded)
	}
}

func TestWxWorkKFFirstChunkFailureSeparatesSafeRetryAndUncertainDelivery(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus enums.ChannelMessageOutboxStatus
		uncertain  bool
	}{
		{name: "pre-call failure", err: errors.New("kf client unavailable"), wantStatus: enums.ChannelMessageOutboxStatusFailed},
		{name: "known sdk rejection", err: workkf.SDKApiFreqOutOfLimit, wantStatus: enums.ChannelMessageOutboxStatusFailed},
		{name: "post-call failure", err: markExternalDispatchResultUncertain(errors.New("send timeout")), wantStatus: enums.ChannelMessageOutboxStatusCancelled, uncertain: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outbox, message, _ := prepareWxWorkKFChunkDispatchTest(t, strings.ReplaceAll(tt.name, " ", "-"))
			chunks := []wxWorkKFOutboundChunk{{MessageType: enums.IMMessageTypeText, Content: "第一段"}}
			ids, completed, err := WxWorkKFOutboundService.sendClaimedOutboundChunks(outbox, message, chunks, func(_ wxWorkKFOutboundChunk, _ int) (string, error) {
				return "", tt.err
			})
			if err != nil || completed || len(ids) != 0 {
				t.Fatalf("unexpected first-chunk result: ids=%v completed=%v err=%v", ids, completed, err)
			}
			reloaded := ChannelMessageOutboxService.Get(outbox.ID)
			if reloaded == nil || reloaded.SendStatus != string(tt.wantStatus) {
				t.Fatalf("first-chunk status=%+v want %s", reloaded, tt.wantStatus)
			}
			if tt.uncertain {
				if reloaded.NextRetryAt != nil || !strings.HasPrefix(reloaded.LastError, channelMessageOutboxDispatchUncertainReasonPrefix) {
					t.Fatalf("post-call failure must be terminal and non-replayable: %+v", reloaded)
				}
			} else if reloaded.NextRetryAt == nil {
				t.Fatalf("pre-call failure must remain retryable: %+v", reloaded)
			}
		})
	}
}

func TestWxWorkKFPreClaimFailureCannotOverwriteNewerAttempt(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	outbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkKF,
		MessageID:   601,
		SendStatus:  string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	stale := *outbox
	newerRetryAt := now.Add(4 * time.Minute)
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status":   string(enums.ChannelMessageOutboxStatusFailed),
		"retry_count":   1,
		"next_retry_at": newerRetryAt,
		"last_error":    "newer kf failure",
	}).Error; err != nil {
		t.Fatalf("advance kf attempt: %v", err)
	}

	if err := WxWorkKFOutboundService.markOutboxFailed(&stale, "stale kf failure"); err != nil {
		t.Fatalf("markOutboxFailed() error = %v", err)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusFailed) || reloaded.RetryCount != 1 || reloaded.LastError != "newer kf failure" {
		t.Fatalf("stale kf failure overwrote newer state: %+v", reloaded)
	}
}

func TestWxWorkKFClaimedFailureCannotOverwriteCancellation(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	outbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkKF,
		MessageID:   602,
		SendStatus:  string(enums.ChannelMessageOutboxStatusSending),
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create claimed outbox: %v", err)
	}
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status": string(enums.ChannelMessageOutboxStatusCancelled),
		"last_error":  "human cancellation",
	}).Error; err != nil {
		t.Fatalf("cancel claimed outbox: %v", err)
	}

	if err := WxWorkKFOutboundService.markClaimedOutboxFailed(outbox, "late kf failure"); err != nil {
		t.Fatalf("markClaimedOutboxFailed() error = %v", err)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) || reloaded.RetryCount != 0 || reloaded.LastError != "human cancellation" {
		t.Fatalf("late claimed failure overwrote cancellation: %+v", reloaded)
	}
}

func TestWxWorkKFAsyncSingleChunkFailureReopensSentOutbox(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	message := &models.Message{
		ConversationID: 801,
		RequestID:      "req-kf-async-single",
		ClientMsgID:    "kf-async-single",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        "单条消息",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkKF,
		ConversationID: message.ConversationID,
		MessageID:      message.ID,
		SendStatus:     string(enums.ChannelMessageOutboxStatusSent),
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	ref := &models.WxWorkKFMessageRef{
		ConversationID: message.ConversationID,
		MessageID:      message.ID,
		WxMsgID:        "wx-kf-async-single",
		Direction:      string(enums.WxWorkKFMessageDirectionOut),
		SendStatus:     string(enums.WxWorkKFMessageSendStatusFailed),
		RawPayload:     `{"dispatchAttempt":0,"chunkIndex":0}`,
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(ref).Error; err != nil {
		t.Fatalf("create message ref: %v", err)
	}
	applied, err := WxWorkKFOutboundService.markOutboxFailedFromCallback(outbox, ref, "platform confirmed delivery failure")
	if err != nil {
		t.Fatalf("markOutboxFailedFromCallback() error = %v", err)
	}
	if !applied {
		t.Fatal("the current single-chunk callback must update the sent outbox")
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusFailed) || reloaded.RetryCount != 1 || reloaded.NextRetryAt == nil || reloaded.SentAt != nil {
		t.Fatalf("an authoritative single-chunk failure must become retryable, got %+v", reloaded)
	}
}

func TestWxWorkKFAsyncMultiChunkFailureBecomesTerminalPartialDelivery(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	message := &models.Message{
		ConversationID: 802,
		RequestID:      "req-kf-async-multi",
		ClientMsgID:    "kf-async-multi",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeHTML,
		Content:        "两段消息",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkKF,
		ConversationID: message.ConversationID,
		MessageID:      message.ID,
		SendStatus:     string(enums.ChannelMessageOutboxStatusSent),
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	refs := []*models.WxWorkKFMessageRef{
		{ConversationID: message.ConversationID, MessageID: message.ID, WxMsgID: "wx-kf-async-multi-1", Direction: string(enums.WxWorkKFMessageDirectionOut), SendStatus: string(enums.WxWorkKFMessageSendStatusFailed), RawPayload: `{"dispatchAttempt":0,"chunkIndex":0}`, Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		{ConversationID: message.ConversationID, MessageID: message.ID, WxMsgID: "wx-kf-async-multi-2", Direction: string(enums.WxWorkKFMessageDirectionOut), SendStatus: string(enums.WxWorkKFMessageSendStatusSent), RawPayload: `{"dispatchAttempt":0,"chunkIndex":1}`, Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
	}
	for _, ref := range refs {
		if err := db.Create(ref).Error; err != nil {
			t.Fatalf("create message ref: %v", err)
		}
	}
	applied, err := WxWorkKFOutboundService.markOutboxFailedFromCallback(outbox, refs[0], "one accepted chunk later failed")
	if err != nil {
		t.Fatalf("markOutboxFailedFromCallback() error = %v", err)
	}
	if !applied {
		t.Fatal("the current multi-chunk callback must update the sent outbox")
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) || reloaded.NextRetryAt != nil || reloaded.SentAt != nil ||
		!strings.HasPrefix(reloaded.LastError, channelMessageOutboxDispatchUncertainReasonPrefix) {
		t.Fatalf("a multi-chunk asynchronous failure must be terminal and non-replayable, got %+v", reloaded)
	}
}

func TestWxWorkKFStaleAsyncFailureCannotOverwriteNewerSuccessfulAttempt(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	message := &models.Message{
		ConversationID: 803,
		RequestID:      "req-kf-stale-callback",
		ClientMsgID:    "kf-stale-callback",
		SenderType:     enums.IMSenderTypeAI,
		MessageType:    enums.IMMessageTypeText,
		Content:        "重试成功",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkKF,
		ConversationID: message.ConversationID,
		MessageID:      message.ID,
		SendStatus:     string(enums.ChannelMessageOutboxStatusSent),
		RetryCount:     1,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	oldRef := &models.WxWorkKFMessageRef{
		ConversationID: message.ConversationID,
		MessageID:      message.ID,
		WxMsgID:        "wx-kf-old-attempt",
		Direction:      string(enums.WxWorkKFMessageDirectionOut),
		SendStatus:     string(enums.WxWorkKFMessageSendStatusFailed),
		RawPayload:     `{"dispatchAttempt":0,"chunkIndex":0}`,
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now.Add(-time.Minute), UpdatedAt: now},
	}
	newRef := &models.WxWorkKFMessageRef{
		ConversationID: message.ConversationID,
		MessageID:      message.ID,
		WxMsgID:        "wx-kf-new-attempt",
		Direction:      string(enums.WxWorkKFMessageDirectionOut),
		SendStatus:     string(enums.WxWorkKFMessageSendStatusSent),
		RawPayload:     `{"dispatchAttempt":1,"chunkIndex":0}`,
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	for _, ref := range []*models.WxWorkKFMessageRef{oldRef, newRef} {
		if err := db.Create(ref).Error; err != nil {
			t.Fatalf("create message ref: %v", err)
		}
	}
	applied, err := WxWorkKFOutboundService.markOutboxFailedFromCallback(outbox, oldRef, "late failure from attempt zero")
	if err != nil {
		t.Fatalf("markOutboxFailedFromCallback() error = %v", err)
	}
	if applied {
		t.Fatal("a stale callback must not overwrite a newer successful attempt")
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusSent) || reloaded.RetryCount != 1 || reloaded.SentAt == nil {
		t.Fatalf("newer successful attempt changed unexpectedly: %+v", reloaded)
	}
}

func TestWxWorkKFFailureCallbackBeforeAcceptedRefIsReconciled(t *testing.T) {
	outbox, message, conversation := prepareWxWorkKFChunkDispatchTest(t, "callback-before-ref")
	mapping := &models.WxWorkKFConversation{
		ConversationID: conversation.ID,
		OpenKfID:       "open-kf-race",
		ExternalUserID: "external-race",
	}
	failReason := `{"event":{"event_type":"msg_send_fail","fail_msgid":"wx-kf-race","fail_type":10}}`
	placeholder, err := WxWorkKFOutboundService.recordSendFailureCallback(
		conversation.ID,
		"wx-kf-race",
		mapping.OpenKfID,
		mapping.ExternalUserID,
		failReason,
	)
	if err != nil {
		t.Fatalf("recordSendFailureCallback() error = %v", err)
	}
	if placeholder == nil || placeholder.MessageID != 0 || placeholder.SendStatus != string(enums.WxWorkKFMessageSendStatusFailed) {
		t.Fatalf("early callback did not create a failed rendezvous ref: %+v", placeholder)
	}

	accepted, err := WxWorkKFOutboundService.persistAcceptedOutboundChunk(
		outbox,
		message,
		conversation,
		mapping,
		wxWorkKFOutboundChunk{MessageType: enums.IMMessageTypeText, Content: "单段消息"},
		0,
		"wx-kf-race",
	)
	if err != nil {
		t.Fatalf("persistAcceptedOutboundChunk() error = %v", err)
	}
	if accepted == nil || accepted.ID != placeholder.ID || accepted.MessageID != message.ID ||
		accepted.SendStatus != string(enums.WxWorkKFMessageSendStatusFailed) || accepted.FailReason != failReason {
		t.Fatalf("accepted chunk did not claim and preserve the early failure: %+v", accepted)
	}
	if attempt, ok := wxWorkKFMessageRefDispatchAttempt(*accepted); !ok || attempt != outbox.RetryCount {
		t.Fatalf("claimed ref lost dispatch attempt: ref=%+v attempt=%d ok=%v", accepted, attempt, ok)
	}

	completed, err := ChannelMessageOutboxService.completeClaimedDispatchWithDB(sqls.DB(), *outbox, time.Now())
	if err != nil || !completed {
		t.Fatalf("completeClaimedDispatchWithDB() completed=%v error=%v", completed, err)
	}
	if err := WxWorkKFOutboundService.reconcileAcceptedChunkFailures(outbox); err != nil {
		t.Fatalf("reconcileAcceptedChunkFailures() error = %v", err)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusFailed) ||
		reloaded.RetryCount != 1 || reloaded.NextRetryAt == nil || reloaded.SentAt != nil {
		t.Fatalf("early authoritative single-chunk failure was not made retryable: %+v", reloaded)
	}
}

func TestWxWorkKFEarlyFailureAcrossAcceptedChunksStaysNonReplayable(t *testing.T) {
	outbox, message, conversation := prepareWxWorkKFChunkDispatchTest(t, "callback-before-multi-ref")
	mapping := &models.WxWorkKFConversation{
		ConversationID: conversation.ID,
		OpenKfID:       "open-kf-multi-race",
		ExternalUserID: "external-multi-race",
	}
	failReason := `{"event":{"event_type":"msg_send_fail","fail_msgid":"wx-kf-multi-race-1","fail_type":10}}`
	if _, err := WxWorkKFOutboundService.recordSendFailureCallback(
		conversation.ID,
		"wx-kf-multi-race-1",
		mapping.OpenKfID,
		mapping.ExternalUserID,
		failReason,
	); err != nil {
		t.Fatalf("record first chunk failure: %v", err)
	}
	for i, wxMsgID := range []string{"wx-kf-multi-race-1", "wx-kf-multi-race-2"} {
		if _, err := WxWorkKFOutboundService.persistAcceptedOutboundChunk(
			outbox,
			message,
			conversation,
			mapping,
			wxWorkKFOutboundChunk{MessageType: enums.IMMessageTypeText, Content: "分段消息"},
			i,
			wxMsgID,
		); err != nil {
			t.Fatalf("persist accepted chunk %d: %v", i, err)
		}
	}

	completed, err := ChannelMessageOutboxService.completeClaimedDispatchWithDB(sqls.DB(), *outbox, time.Now())
	if err != nil || !completed {
		t.Fatalf("completeClaimedDispatchWithDB() completed=%v error=%v", completed, err)
	}
	if err := WxWorkKFOutboundService.reconcileAcceptedChunkFailures(outbox); err != nil {
		t.Fatalf("reconcileAcceptedChunkFailures() error = %v", err)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) ||
		reloaded.NextRetryAt != nil || reloaded.SentAt != nil ||
		!strings.HasPrefix(reloaded.LastError, channelMessageOutboxDispatchUncertainReasonPrefix) {
		t.Fatalf("early multi-chunk failure must stay terminal and non-replayable: %+v", reloaded)
	}
	refs := WxWorkKFMessageRefService.Find(sqls.NewCnd().
		Eq("message_id", message.ID).
		Eq("direction", string(enums.WxWorkKFMessageDirectionOut)).
		Asc("id"))
	if len(refs) != 2 || refs[0].SendStatus != string(enums.WxWorkKFMessageSendStatusFailed) ||
		refs[1].SendStatus != string(enums.WxWorkKFMessageSendStatusSent) {
		t.Fatalf("accepted chunk refs were not durably preserved: %+v", refs)
	}
}
