package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
)

func prepareWxWorkCLIOutboxTest(t *testing.T, status enums.ChannelMessageOutboxStatus) (*models.ChannelMessageOutbox, *models.WxWorkKFConversation) {
	t.Helper()
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	channel := &models.Channel{
		ID:          31,
		Name:        "企微CLI回执测试",
		ChannelType: enums.ChannelTypeWxWorkCLI,
		ChannelID:   "wxwork-cli-ack-test",
		ConfigJSON:  `{"bridgeToken":"test-token","defaultChatType":1}`,
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("wxwork-cli-ack-user"), channel.ID, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	message := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      1,
		RequestID:      "ordinary_cli_reply",
		ClientMsgID:    "ordinary-cli-reply",
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "测试回复",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}
	mapping := &models.WxWorkKFConversation{
		ConversationID: conversation.ID,
		ChannelID:      channel.ID,
		OpenKfID:       "1",
		ExternalUserID: "S:test-user",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkCLI,
		ConversationID: conversation.ID,
		MessageID:      message.ID,
		Payload:        `{}`,
		SendStatus:     string(status),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	return outbox, mapping
}

func TestWxWorkCLILateFailureCannotOverwriteSentOutbox(t *testing.T) {
	outbox, _ := prepareWxWorkCLIOutboxTest(t, enums.ChannelMessageOutboxStatusSent)
	if err := WxWorkCLIBridgeService.MarkOutboxFailed(request.WxWorkCLIOutboxFailedRequest{
		ChannelID:   "wxwork-cli-ack-test",
		BridgeToken: "test-token",
		OutboxID:    outbox.ID,
		Error:       "late failure",
	}); err != nil {
		t.Fatalf("MarkOutboxFailed() error = %v", err)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusSent) || reloaded.RetryCount != 0 {
		t.Fatalf("late failure changed sent outbox: %+v", reloaded)
	}
}

func TestWxWorkCLILateSuccessCannotReviveCancelledOutbox(t *testing.T) {
	outbox, _ := prepareWxWorkCLIOutboxTest(t, enums.ChannelMessageOutboxStatusCancelled)
	if err := WxWorkCLIBridgeService.MarkOutboxSent(request.WxWorkCLIOutboxSentRequest{
		ChannelID:      "wxwork-cli-ack-test",
		BridgeToken:    "test-token",
		OutboxID:       outbox.ID,
		ExternalMsgID:  "late-success",
		ExternalResult: `{}`,
	}); err != nil {
		t.Fatalf("MarkOutboxSent() error = %v", err)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) || reloaded.SentAt != nil {
		t.Fatalf("late success changed cancelled outbox: %+v", reloaded)
	}
	if ref := WxWorkKFMessageRefService.Take("message_id = ? AND direction = ?", outbox.MessageID, string(enums.WxWorkKFMessageDirectionOut)); ref != nil {
		t.Fatalf("late success created an outbound reference: %+v", ref)
	}
}

func TestWxWorkCLISendingSuccessTransitionsOnce(t *testing.T) {
	outbox, _ := prepareWxWorkCLIOutboxTest(t, enums.ChannelMessageOutboxStatusSending)
	req := request.WxWorkCLIOutboxSentRequest{
		ChannelID:      "wxwork-cli-ack-test",
		BridgeToken:    "test-token",
		OutboxID:       outbox.ID,
		ExternalMsgID:  "wx-cli-success",
		ExternalResult: `{"ok":true}`,
	}
	if err := WxWorkCLIBridgeService.MarkOutboxSent(req); err != nil {
		t.Fatalf("first MarkOutboxSent() error = %v", err)
	}
	if err := WxWorkCLIBridgeService.MarkOutboxSent(req); err != nil {
		t.Fatalf("duplicate MarkOutboxSent() error = %v", err)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusSent) || reloaded.SentAt == nil {
		t.Fatalf("sending outbox did not become sent: %+v", reloaded)
	}
	refCount := WxWorkKFMessageRefService.Count(sqls.NewCnd())
	if refCount != 1 {
		t.Fatalf("duplicate success created %d refs, want 1", refCount)
	}
}

func TestWxWorkCLIFailureAckCancelsUncertainDeliveryPermanently(t *testing.T) {
	outbox, _ := prepareWxWorkCLIOutboxTest(t, enums.ChannelMessageOutboxStatusSending)
	future := time.Now().Add(time.Minute)
	if err := sqls.DB().Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Update("next_retry_at", future).Error; err != nil {
		t.Fatalf("set previous retry time: %v", err)
	}
	failureReq := request.WxWorkCLIOutboxFailedRequest{
		ChannelID:   "wxwork-cli-ack-test",
		BridgeToken: "test-token",
		OutboxID:    outbox.ID,
		Error:       "bridge send returned failure",
	}
	if err := WxWorkCLIBridgeService.MarkOutboxFailed(failureReq); err != nil {
		t.Fatalf("MarkOutboxFailed() error = %v", err)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) {
		t.Fatalf("failed CLI acknowledgement must cancel the uncertain delivery: %+v", reloaded)
	}
	if reloaded.RetryCount != 1 || reloaded.NextRetryAt != nil || !strings.HasPrefix(reloaded.LastError, channelMessageOutboxDispatchUncertainReasonPrefix) {
		t.Fatalf("uncertain CLI cancellation metadata is invalid: %+v", reloaded)
	}
	firstError := reloaded.LastError

	if err := WxWorkCLIBridgeService.MarkOutboxSent(request.WxWorkCLIOutboxSentRequest{
		ChannelID:      "wxwork-cli-ack-test",
		BridgeToken:    "test-token",
		OutboxID:       outbox.ID,
		ExternalMsgID:  "late-cli-success",
		ExternalResult: `{}`,
	}); err != nil {
		t.Fatalf("late MarkOutboxSent() error = %v", err)
	}
	if err := WxWorkCLIBridgeService.MarkOutboxFailed(failureReq); err != nil {
		t.Fatalf("duplicate MarkOutboxFailed() error = %v", err)
	}
	reloaded = ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) || reloaded.RetryCount != 1 || reloaded.SentAt != nil || reloaded.LastError != firstError {
		t.Fatalf("late CLI result revived or rewrote terminal cancellation: %+v", reloaded)
	}
	if ref := WxWorkKFMessageRefService.Take("message_id = ? AND direction = ?", outbox.MessageID, string(enums.WxWorkKFMessageDirectionOut)); ref != nil {
		t.Fatalf("late CLI success created an outbound reference: %+v", ref)
	}
}
