package services

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

const standaloneOneTestMiniProgramPayload = `{
	"title":"入住小程序",
	"username":"gh_standalone_one@app",
	"file_id":"cover-file-id",
	"aes_key":"cover-aes-key",
	"md5":"cover-md5",
	"size":20810,
	"page_path":"pages/order/index"
}`

func TestStandaloneOneReplyCommitsTextMiniProgramAndOutboxWithoutModel(t *testing.T) {
	fixture := setupStandaloneOneReplyFixture(t, standaloneOneTestMiniProgramPayload)
	var modelCalls atomic.Int32
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		modelCalls.Add(1)
		return AIReplyExecutionResult{}, nil
	})

	message, err := MessageService.SendCustomerMessageInSession(
		fixture.conversation.ID,
		"standalone-one-success",
		enums.IMMessageTypeText,
		" 1 ",
		"",
		fixture.external,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if message.AIReplyTurnID != 0 || message.AIReplyTurnVersion != 0 {
		t.Fatalf("standalone source entered ordinary turn: %#v", message)
	}
	job := repositories.AIReplyJobRepository.GetByMessageInTenant(fixture.db, message.TenantID, message.ConversationID, message.ID)
	if job == nil || job.TriggerKind != enums.AIReplyJobTriggerKindStandaloneOne || job.TurnID != 0 || job.TurnVersion != 0 {
		t.Fatalf("standalone job scope mismatch: %#v", job)
	}
	makeAIReplyJobDue(t, fixture.db, job.ID)
	current, err := AIReplyJobService.ProcessMessageNow(message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != enums.AIReplyJobStatusCompleted || current.ResultCode != "standalone_one_completed" {
		t.Fatalf("standalone job did not complete: %#v", current)
	}
	if modelCalls.Load() != 0 {
		t.Fatalf("standalone reply called the model %d times", modelCalls.Load())
	}

	var replies []models.Message
	if err := fixture.db.Order("id ASC").Find(&replies).Error; err != nil {
		t.Fatal(err)
	}
	standaloneReplies := make([]models.Message, 0, 2)
	for _, reply := range replies {
		if reply.ConversationID == fixture.conversation.ID && strings.HasPrefix(reply.ClientMsgID, "ai_reply_faq_one_") {
			standaloneReplies = append(standaloneReplies, reply)
		}
	}
	if len(standaloneReplies) != 2 {
		t.Fatalf("standalone reply count=%d want 2: %#v", len(standaloneReplies), standaloneReplies)
	}
	wantTextID := "ai_reply_faq_one_" + formatInt64(message.ID) + "_text"
	wantMiniProgramID := "ai_reply_faq_one_" + formatInt64(message.ID) + "_mini_program"
	seenText := false
	seenMiniProgram := false
	for _, reply := range standaloneReplies {
		if reply.AIReplyTurnID != 0 || reply.AIReplyTurnVersion != 0 || reply.RequestID != message.RequestID {
			t.Fatalf("standalone reply leaked turn/request scope: %#v", reply)
		}
		switch reply.ClientMsgID {
		case wantTextID:
			seenText = reply.MessageType == enums.IMMessageTypeText && reply.Content == standaloneOneReplyText
		case wantMiniProgramID:
			seenMiniProgram = reply.MessageType == enums.IMMessageTypeMiniProgram && strings.Contains(reply.Payload, `"username":"gh_standalone_one@app"`)
		}
	}
	if !seenText || !seenMiniProgram {
		t.Fatalf("standalone reply payload mismatch: text=%v mini_program=%v replies=%#v", seenText, seenMiniProgram, standaloneReplies)
	}
	var outboxes []models.ChannelMessageOutbox
	if err := fixture.db.Where("tenant_id = ? AND conversation_id = ?", message.TenantID, message.ConversationID).
		Order("id ASC").Find(&outboxes).Error; err != nil {
		t.Fatal(err)
	}
	if len(outboxes) != 2 {
		t.Fatalf("standalone outbox count=%d want 2: %#v", len(outboxes), outboxes)
	}
	for _, outbox := range outboxes {
		if outbox.ChannelType != enums.ChannelTypeWxWorkProtocol ||
			(outbox.SendStatus != string(enums.ChannelMessageOutboxStatusPending) &&
				outbox.SendStatus != string(enums.ChannelMessageOutboxStatusSending)) {
			t.Fatalf("standalone outbox scope mismatch: %#v", outbox)
		}
	}

	if err := fixture.db.Model(&models.AIReplyJob{}).Where("id = ?", job.ID).Updates(map[string]any{
		"status":           enums.AIReplyJobStatusRetry,
		"result_code":      "runtime_retry",
		"next_retry_at":    time.Now().Add(-time.Second),
		"lease_owner":      "",
		"lease_expires_at": nil,
	}).Error; err != nil {
		t.Fatal(err)
	}
	current, err = AIReplyJobService.ProcessMessageNow(message.ID)
	if err != nil || current == nil || current.Status != enums.AIReplyJobStatusCompleted {
		t.Fatalf("standalone retry did not recover committed reply: job=%#v err=%v", current, err)
	}
	if modelCalls.Load() != 0 {
		t.Fatalf("standalone retry called the model %d times", modelCalls.Load())
	}
	var replyCount int64
	if err := fixture.db.Model(&models.Message{}).
		Where("conversation_id = ? AND client_msg_id LIKE ?", message.ConversationID, "ai_reply_faq_one_%").
		Count(&replyCount).Error; err != nil {
		t.Fatal(err)
	}
	var outboxCount int64
	if err := fixture.db.Model(&models.ChannelMessageOutbox{}).Where("conversation_id = ?", message.ConversationID).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if replyCount != 2 || outboxCount != 2 {
		t.Fatalf("standalone retry duplicated durable output: messages=%d outboxes=%d", replyCount, outboxCount)
	}
}

func TestStandaloneOneReplyInvalidMiniProgramRetriesWithoutPartialCommit(t *testing.T) {
	fixture := setupStandaloneOneReplyFixture(t, "")
	var modelCalls atomic.Int32
	setAIReplyJobTestHook(t, func(context.Context, models.Conversation, models.Message) (AIReplyExecutionResult, error) {
		modelCalls.Add(1)
		return AIReplyExecutionResult{}, nil
	})
	message, err := MessageService.SendCustomerMessageInSession(
		fixture.conversation.ID,
		"standalone-one-invalid-resource",
		enums.IMMessageTypeText,
		"1",
		"",
		fixture.external,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	job := repositories.AIReplyJobRepository.GetByMessageInTenant(fixture.db, message.TenantID, message.ConversationID, message.ID)
	if job == nil {
		t.Fatal("standalone job missing")
	}
	makeAIReplyJobDue(t, fixture.db, job.ID)
	current, err := AIReplyJobService.ProcessMessageNow(message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.Status != enums.AIReplyJobStatusRetry || current.LastErrorClass != string(AIReplyExecutionErrorResourceInvariantBroken) || current.NextRetryAt == nil {
		t.Fatalf("invalid resource did not enter durable retry: %#v", current)
	}
	if modelCalls.Load() != 0 {
		t.Fatalf("invalid standalone resource called the model %d times", modelCalls.Load())
	}
	var replyCount int64
	if err := fixture.db.Model(&models.Message{}).
		Where("conversation_id = ? AND client_msg_id LIKE ?", message.ConversationID, "ai_reply_faq_one_%").
		Count(&replyCount).Error; err != nil {
		t.Fatal(err)
	}
	var outboxCount int64
	if err := fixture.db.Model(&models.ChannelMessageOutbox{}).Where("conversation_id = ?", message.ConversationID).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if replyCount != 0 || outboxCount != 0 {
		t.Fatalf("invalid resource partially committed: messages=%d outboxes=%d", replyCount, outboxCount)
	}
}

type standaloneOneReplyFixture struct {
	db           *gorm.DB
	instance     *models.WxWorkProtocolInstance
	external     openidentity.ExternalUser
	conversation *models.Conversation
}

func setupStandaloneOneReplyFixture(t *testing.T, miniProgramPayload string) standaloneOneReplyFixture {
	t.Helper()
	db, store, channel, _, instance, external, conversation := setupStoreConversationContinuityFixture(t, "standalone-one-"+testNameKey(t.Name()))
	now := time.Now()
	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", instance.ID).Updates(map[string]any{
		"ai_reply_enabled":             true,
		"default_mini_program_payload": miniProgramPayload,
	}).Error; err != nil {
		t.Fatal(err)
	}
	instance.AIReplyEnabled = true
	instance.DefaultMiniProgramPayload = miniProgramPayload
	if sessionNo, err := ConversationChannelSessionService.PrepareInbound(conversation.ID, instance, now); err != nil || sessionNo != 1 {
		t.Fatalf("prepare standalone session=%d err=%v", sessionNo, err)
	}
	externalID := strings.TrimPrefix(external.ExternalID, "wxwork_protocol:")
	if err := db.Create(&models.WxWorkKFConversation{
		TenantID: store.TenantID, ConversationID: conversation.ID, ChannelID: channel.ID,
		OpenKfID: "wx_protocol:" + instance.Guid + ":single", ExternalUserID: externalID,
		SessionStatus: string(enums.WxWorkKFSessionStatusActive), Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	return standaloneOneReplyFixture{db: db, instance: instance, external: external, conversation: conversation}
}
