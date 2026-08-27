package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestAllowAIMessageOnPendingHandoff(t *testing.T) {
	conversation := &models.Conversation{
		Status:            enums.IMConversationStatusPending,
		CurrentAssigneeID: 0,
		HandoffAt:         ptrTime(time.Now()),
	}
	if !MessageService.allowAIMessageOnPendingHandoff(conversation) {
		t.Fatalf("expected pending handoff conversation to allow ai handoff notice")
	}

	conversation.Status = enums.IMConversationStatusAIServing
	if MessageService.allowAIMessageOnPendingHandoff(conversation) {
		t.Fatalf("expected ai serving conversation not to use pending handoff allowance")
	}
}

func ptrTime(v time.Time) *time.Time {
	return &v
}

func setupMessageWelcomeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousAIReplyHook := TriggerAIReplyAsyncHook
	previousStandaloneReplyHook := TriggerStandaloneOneReplyAsyncHook
	TriggerAIReplyAsyncHook = nil
	TriggerStandaloneOneReplyAsyncHook = nil
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousAIReplyHook
		TriggerStandaloneOneReplyAsyncHook = previousStandaloneReplyHook
	})

	dbName := "message_welcome_test_" + strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite db: %v", err)
	}
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close sqlite db: %v", err)
		}
	})
	if err := db.AutoMigrate(
		&models.AIAgent{},
		&models.Channel{},
		&models.ChannelMessageOutbox{},
		&models.WxWorkProtocolInstance{},
		&models.Customer{},
		&models.CustomerIdentity{},
		&models.WxWorkCustomerHandoffSetting{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.AIManualResumeTask{},
		&models.ConversationAssignment{},
		&models.ConversationParticipant{},
		&models.ConversationReadState{},
		&models.ConversationEventLog{},
		&models.Message{},
		&models.Asset{},
		&models.WxWorkKFConversation{},
		&models.WxWorkKFMessageRef{},
		&models.MessageSyncLog{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)
	return db
}

func createWelcomeTestAIAgent(t *testing.T, db *gorm.DB, welcomeMessage string) *models.AIAgent {
	t.Helper()

	now := time.Now()
	aiAgent := &models.AIAgent{
		Name:           "welcome-test-agent",
		Status:         enums.StatusOk,
		ServiceMode:    enums.IMConversationServiceModeAIOnly,
		WelcomeMessage: welcomeMessage,
		AuditFields: models.AuditFields{
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	if err := db.Create(aiAgent).Error; err != nil {
		t.Fatalf("create ai agent: %v", err)
	}
	return aiAgent
}

func welcomeTestExternalUser(id string) openidentity.ExternalUser {
	return openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceUser,
		ExternalID:     id,
		ExternalName:   "访客" + id,
	}
}

func TestConversationCreateCreatesAIWelcomeMessage(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "  您好，请问有什么可以帮您？  ")

	conversation, err := ConversationService.Create(welcomeTestExternalUser("welcome-1"), 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if conversation == nil {
		t.Fatalf("expected conversation")
	}

	var messages []models.Message
	if err := db.Find(&messages).Error; err != nil {
		t.Fatalf("find messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected exactly one welcome message, got %d", len(messages))
	}
	message := messages[0]
	if message.ConversationID != conversation.ID {
		t.Fatalf("expected conversation_id %d, got %d", conversation.ID, message.ConversationID)
	}
	if message.SenderType != enums.IMSenderTypeAI {
		t.Fatalf("expected sender type ai, got %q", message.SenderType)
	}
	if message.SenderID != aiAgent.ID {
		t.Fatalf("expected sender id %d, got %d", aiAgent.ID, message.SenderID)
	}
	if message.MessageType != enums.IMMessageTypeText {
		t.Fatalf("expected message type text, got %q", message.MessageType)
	}
	if message.Content != "您好，请问有什么可以帮您？" {
		t.Fatalf("expected trimmed welcome content, got %q", message.Content)
	}
	if message.SeqNo != 1 {
		t.Fatalf("expected seq no 1, got %d", message.SeqNo)
	}
	if message.SendStatus != enums.IMMessageStatusSent {
		t.Fatalf("expected sent status, got %d", message.SendStatus)
	}

	var updated models.Conversation
	if err := db.First(&updated, conversation.ID).Error; err != nil {
		t.Fatalf("find conversation: %v", err)
	}
	if updated.LastMessageID != message.ID {
		t.Fatalf("expected last message id %d, got %d", message.ID, updated.LastMessageID)
	}
	if updated.LastMessageSummary != "您好，请问有什么可以帮您？" {
		t.Fatalf("expected last message summary, got %q", updated.LastMessageSummary)
	}
	if updated.CustomerUnreadCount != 1 {
		t.Fatalf("expected customer unread count 1, got %d", updated.CustomerUnreadCount)
	}
	if updated.AgentUnreadCount != 0 {
		t.Fatalf("expected agent unread count 0, got %d", updated.AgentUnreadCount)
	}
}

func TestSendCustomerMessageStoresRequestIDOnMessageAndEvent(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("trace-user")
	conversation, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	message, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"client-msg-trace",
		enums.IMMessageTypeText,
		"hello",
		"",
		external,
		"trace-123",
	)
	if err != nil {
		t.Fatalf("SendCustomerMessageWithRequestID() error = %v", err)
	}
	if message.RequestID != "trace-123" {
		t.Fatalf("message.RequestID=%q want %q", message.RequestID, "trace-123")
	}

	var event models.ConversationEventLog
	if err := db.Where("conversation_id = ?", conversation.ID).Order("id DESC").First(&event).Error; err != nil {
		t.Fatalf("find event: %v", err)
	}
	if event.RequestID != "trace-123" {
		t.Fatalf("event.RequestID=%q want %q", event.RequestID, "trace-123")
	}
}

func TestCreateExternalAgentMessageWithoutOutboxMarksStoreManualHandled(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID:          11,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-test",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("self-echo-user")
	conversation, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "需要门店人工跟进", now); err != nil {
		t.Fatalf("EnterStoreWecomManual() error = %v", err)
	}

	message, err := MessageService.CreateExternalAgentMessageWithoutOutbox(conversation.ID, "wx-self-echo-1", enums.IMMessageTypeText, "我来处理。", "", "req-self-echo")
	if err != nil {
		t.Fatalf("CreateExternalAgentMessageWithoutOutbox() error = %v", err)
	}
	if message == nil || message.SenderType != enums.IMSenderTypeAgent || message.Content != "我来处理。" {
		t.Fatalf("expected external agent echo message, got %+v", message)
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.NeedHumanFollowUp || state.ManualExpireAt == nil {
		t.Fatalf("expected self echo to clear manual dot and start idle timeout, got %+v", state)
	}
	var outboxCount int64
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("conversation_id = ?", conversation.ID).Count(&outboxCount).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("expected self echo to avoid outbound outbox, got %d", outboxCount)
	}
}

func TestCreateExternalAgentMessageWithoutOutboxTakesOverAIServingRoute(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID:          12,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-takeover-test",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("self-echo-takeover-user"), 12, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := MessageService.CreateExternalAgentMessageWithoutOutbox(conversation.ID, "wx-self-echo-takeover", enums.IMMessageTypeText, "我来接着处理。", "", "req-self-echo-takeover"); err != nil {
		t.Fatalf("CreateExternalAgentMessageWithoutOutbox() error = %v", err)
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.NeedHumanFollowUp || state.ManualExpireAt == nil {
		t.Fatalf("expected external employee reply to enter manual servicing, got %+v", state)
	}
	if state.LastManualHandoffAt == nil {
		t.Fatalf("expected external employee reply to begin a manual interval")
	}
}

func TestExternalAgentEchoCancelsOrdinaryAIOutboxButKeepsServiceNotice(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID:          13,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-outbox-race-test",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("self-echo-outbox-user"), 13, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	ordinary := &models.Message{
		ConversationID: conversation.ID,
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		ClientMsgID:    "ai_reply_old",
		MessageType:    enums.IMMessageTypeText,
		Content:        "旧的AI回答",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         ptrTime(now),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(ordinary).Error; err != nil {
		t.Fatalf("create ordinary ai message: %v", err)
	}
	serviceNotice := &models.Message{
		ConversationID: conversation.ID,
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		ClientMsgID:    "ai_handoff_success_test",
		MessageType:    enums.IMMessageTypeText,
		Content:        DirectHandoffSuccessMessage,
		SeqNo:          2,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         ptrTime(now),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(serviceNotice).Error; err != nil {
		t.Fatalf("create service notice: %v", err)
	}
	ordinaryOutbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol, ConversationID: conversation.ID, MessageID: ordinary.ID,
		Payload: `{}`, SendStatus: string(enums.ChannelMessageOutboxStatusPending), AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	serviceOutbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol, ConversationID: conversation.ID, MessageID: serviceNotice.ID,
		Payload: `{}`, SendStatus: string(enums.ChannelMessageOutboxStatusPending), AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(ordinaryOutbox).Error; err != nil {
		t.Fatalf("create ordinary outbox: %v", err)
	}
	if err := db.Create(serviceOutbox).Error; err != nil {
		t.Fatalf("create service outbox: %v", err)
	}

	echo, err := MessageService.CreateExternalAgentMessageWithoutOutbox(conversation.ID, "wx-self-echo-outbox", enums.IMMessageTypeText, "我来处理。", "", "req-self-echo-outbox")
	if err != nil {
		t.Fatalf("create external echo: %v", err)
	}
	if echo == nil {
		t.Fatal("expected external echo")
	}
	if err := db.First(ordinaryOutbox, ordinaryOutbox.ID).Error; err != nil {
		t.Fatalf("reload ordinary outbox: %v", err)
	}
	if ordinaryOutbox.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) {
		t.Fatalf("ordinary outbox status=%q want cancelled", ordinaryOutbox.SendStatus)
	}
	if err := db.First(serviceOutbox, serviceOutbox.ID).Error; err != nil {
		t.Fatalf("reload service outbox: %v", err)
	}
	if serviceOutbox.SendStatus != string(enums.ChannelMessageOutboxStatusPending) {
		t.Fatalf("service notice outbox status=%q want pending", serviceOutbox.SendStatus)
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.ManualExpireAt == nil || state.NeedHumanFollowUp {
		t.Fatalf("expected trusted echo to atomically enter idle manual route, got %+v", state)
	}
	if remaining := time.Until(*state.ManualExpireAt); remaining < 9*time.Minute || remaining > 11*time.Minute {
		t.Fatalf("manual timeout=%v want about 10 minutes", remaining)
	}
}

func TestClaimExpiredManualRouteRejectsStaleSnapshot(t *testing.T) {
	setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, sqls.DB(), "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("manual-timeout-cas-user"), 0, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	now := time.Now()
	state, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "等待人工", now.Add(-20*time.Minute))
	if err != nil {
		t.Fatalf("enter manual route: %v", err)
	}
	if err := sqls.DB().Model(&models.ConversationRouteState{}).Where("id = ?", state.ID).Update("manual_expire_at", now.Add(-time.Minute)).Error; err != nil {
		t.Fatalf("expire manual route: %v", err)
	}
	stale := ConversationRouteService.GetByConversationID(conversation.ID)
	if err := ConversationRouteService.MarkCustomerMessage(conversation.ID, now); err != nil {
		t.Fatalf("extend manual route: %v", err)
	}
	if _, claimed, err := ConversationRouteService.ClaimExpiredManualRoute(*stale, now); err != nil || claimed {
		t.Fatalf("stale timeout snapshot claimed=%v err=%v", claimed, err)
	}
}

func TestClaimExpiredManualRouteUsesDatabaseSecondPrecisionForFollowUpCAS(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("manual-timeout-second-precision-user"), 0, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	now := time.Date(2026, time.August, 27, 12, 0, 0, 987654321, time.Local)
	state, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "等待人工", now.Add(-20*time.Minute))
	if err != nil {
		t.Fatalf("enter manual route: %v", err)
	}
	if err := db.Model(&models.ConversationRouteState{}).
		Where("id = ?", state.ID).
		Update("manual_expire_at", now.Add(-time.Minute).Truncate(time.Second)).Error; err != nil {
		t.Fatalf("expire manual route: %v", err)
	}

	expired := ConversationRouteService.GetByConversationID(conversation.ID)
	claimedState, claimed, err := ConversationRouteService.ClaimExpiredManualRoute(*expired, now)
	if err != nil || !claimed || claimedState == nil {
		t.Fatalf("claim expired manual route claimed=%v state=%+v err=%v", claimed, claimedState, err)
	}
	if claimedState.ManualExpireAt == nil || claimedState.ManualExpireAt.Nanosecond() != 0 {
		t.Fatalf("claimed lease must match DATETIME second precision, got %v", claimedState.ManualExpireAt)
	}

	// SQLite preserves fractional seconds, so explicitly reproduce MySQL
	// DATETIME storage before exercising the downstream compare-and-swap.
	if err := db.Model(&models.ConversationRouteState{}).
		Where("id = ?", state.ID).
		Update("manual_expire_at", claimedState.ManualExpireAt.Truncate(time.Second)).Error; err != nil {
		t.Fatalf("simulate DATETIME precision: %v", err)
	}
	restored, err := ConversationRouteService.RestoreAIFromTimeoutClaim(*claimedState, "人工接待超时恢复AI", now, false)
	if err != nil || !restored {
		t.Fatalf("restore from claimed timeout restored=%v err=%v", restored, err)
	}
	updated := ConversationRouteService.GetByConversationID(conversation.ID)
	if updated == nil || updated.RouteStatus != enums.ConversationRouteStatusAIServing || updated.ManualExpireAt != nil {
		t.Fatalf("expected claimed route restored once, got %+v", updated)
	}
}

func TestHQAssignedManualResumeCanCommitAIReply(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("hq-manual-resume-user"), 0, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	now := time.Now()
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{
		"status":              enums.IMConversationStatusActive,
		"current_team_id":     int64(1),
		"current_assignee_id": int64(99),
	}).Error; err != nil {
		t.Fatalf("assign conversation: %v", err)
	}
	if _, err := ConversationRouteService.EnterHQAgentDeskServing(conversation.ID, "HQ人工接待", now); err != nil {
		t.Fatalf("EnterHQAgentDeskServing() error = %v", err)
	}
	source := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      1,
		ClientMsgID:    "hq-manual-resume-source",
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "还有一个问题没处理",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         ptrTime(now),
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(source).Error; err != nil {
		t.Fatalf("create source message: %v", err)
	}
	if err := ConversationRouteService.MarkCustomerMessage(conversation.ID, now); err != nil {
		t.Fatalf("MarkCustomerMessage() error = %v", err)
	}
	token := "hqassignedresume"
	task := &models.AIManualResumeTask{
		TaskKey:                "manual_resume:" + token,
		HandoffToken:           token,
		ConversationID:         conversation.ID,
		OriginMessageID:        source.ID,
		LatestWaitingMessageID: source.ID,
		RouteStatus:            string(enums.ConversationRouteStatusHQAgentDeskServing),
		TaskStatus:             aiManualResumeTaskRunning,
		AuditFields:            models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("create manual resume task: %v", err)
	}
	requestID := "manual_resume_" + token
	if !MessageService.CanSendAIReply(conversation.ID, requestID, source.ID) {
		t.Fatal("expected assigned HQ conversation to allow its running manual resume")
	}
	reply, err := MessageService.SendAIMessageWithRequestID(
		conversation.ID,
		aiAgent.ID,
		"hq-manual-resume-reply",
		enums.IMMessageTypeText,
		"我继续帮你处理。",
		"",
		&dto.AuthPrincipal{Username: "AI"},
		requestID,
	)
	if err != nil {
		t.Fatalf("SendAIMessageWithRequestID() error = %v", err)
	}
	if reply == nil || reply.RequestID != requestID {
		t.Fatalf("expected committed manual resume reply, got %+v", reply)
	}
}

func TestManualRouteCustomerMessageCreatesResumeTaskBeforeReturn(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("manual-resume-atomic-create")
	conversation, err := ConversationService.Create(external, 0, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "等待门店同事", time.Now()); err != nil {
		t.Fatalf("EnterStoreWecomManual() error = %v", err)
	}

	message, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"manual-resume-atomic-create-message",
		enums.IMMessageTypeText,
		"还有一个问题没处理",
		"",
		external,
		"req-manual-resume-atomic-create",
	)
	if err != nil {
		t.Fatalf("SendCustomerMessageWithRequestID() error = %v", err)
	}
	task := AIManualResumeTaskService.latestActiveTask(conversation.ID, []string{aiManualResumeTaskWaiting})
	if task == nil {
		t.Fatal("expected manual resume task to exist when customer message returns")
	}
	if task.OriginMessageID != message.ID || task.LatestWaitingMessageID != message.ID {
		t.Fatalf("expected task to bind customer message %d, got origin=%d latest=%d", message.ID, task.OriginMessageID, task.LatestWaitingMessageID)
	}
}

func TestManualResumeScheduleReusesFollowupCreatedInInitialHandoffWindow(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("manual-resume-initial-handoff-window")
	conversation, err := ConversationService.Create(external, 0, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	origin, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"manual-resume-initial-origin",
		enums.IMMessageTypeText,
		"帮我转人工",
		"",
		external,
		"req-manual-resume-initial-origin",
	)
	if err != nil {
		t.Fatalf("send origin customer message: %v", err)
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "客户明确要求人工接待", time.Now()); err != nil {
		t.Fatalf("EnterStoreWecomManual() error = %v", err)
	}
	followup, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"manual-resume-initial-followup",
		enums.IMMessageTypeText,
		"我再补充一个新问题",
		"",
		external,
		"req-manual-resume-initial-followup",
	)
	if err != nil {
		t.Fatalf("send follow-up customer message: %v", err)
	}
	createdDuringWindow := AIManualResumeTaskService.latestActiveTask(conversation.ID, []string{aiManualResumeTaskWaiting})
	if createdDuringWindow == nil || createdDuringWindow.LatestWaitingMessageID != followup.ID {
		t.Fatalf("expected follow-up task before original schedule, got %+v", createdDuringWindow)
	}

	scheduled, err := AIManualResumeTaskService.Schedule(conversation.ID, origin.ID, "direct-initial-handoff")
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if scheduled == nil || scheduled.ID != createdDuringWindow.ID || scheduled.LatestWaitingMessageID != followup.ID {
		t.Fatalf("expected original schedule to reuse follow-up task and keep latest message %d, got %+v", followup.ID, scheduled)
	}
	var total int64
	if err := db.Model(&models.AIManualResumeTask{}).Where("conversation_id = ?", conversation.ID).Count(&total).Error; err != nil {
		t.Fatalf("count manual resume tasks: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected one manual resume task after interleaved schedule, got %d", total)
	}
}

func TestManualRouteCustomerMessageRequeuesActiveResumeTask(t *testing.T) {
	for _, taskStatus := range []string{aiManualResumeTaskReady, aiManualResumeTaskRetry, aiManualResumeTaskRunning} {
		t.Run(taskStatus, func(t *testing.T) {
			db := setupMessageWelcomeTestDB(t)
			aiAgent := createWelcomeTestAIAgent(t, db, "")
			external := welcomeTestExternalUser("manual-resume-requeue-" + taskStatus)
			conversation, err := ConversationService.Create(external, 0, aiAgent.ID)
			if err != nil {
				t.Fatalf("create conversation: %v", err)
			}
			if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "等待门店同事", time.Now()); err != nil {
				t.Fatalf("EnterStoreWecomManual() error = %v", err)
			}
			origin, err := MessageService.SendCustomerMessageWithRequestID(
				conversation.ID,
				"manual-resume-requeue-origin-"+taskStatus,
				enums.IMMessageTypeText,
				"第一个未解决问题",
				"",
				external,
				"req-manual-resume-requeue-origin-"+taskStatus,
			)
			if err != nil {
				t.Fatalf("send origin customer message: %v", err)
			}
			task := AIManualResumeTaskService.latestActiveTask(conversation.ID, []string{aiManualResumeTaskWaiting})
			if task == nil {
				t.Fatal("expected initial waiting task")
			}
			now := time.Now()
			if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
				"task_status":    taskStatus,
				"ready_at":       now,
				"next_retry_at":  now,
				"retry_count":    3,
				"completed_at":   now,
				"last_error":     "old run failed",
				"notice_sent_at": now,
			}).Error; err != nil {
				t.Fatalf("prepare %s task: %v", taskStatus, err)
			}
			latest, err := MessageService.SendCustomerMessageWithRequestID(
				conversation.ID,
				"manual-resume-requeue-latest-"+taskStatus,
				enums.IMMessageTypeText,
				"刚刚又补充了一个问题",
				"",
				external,
				"req-manual-resume-requeue-latest-"+taskStatus,
			)
			if err != nil {
				t.Fatalf("send latest customer message: %v", err)
			}
			if err := db.First(task, task.ID).Error; err != nil {
				t.Fatalf("reload manual resume task: %v", err)
			}
			if task.TaskStatus != aiManualResumeTaskWaiting || task.OriginMessageID != origin.ID || task.LatestWaitingMessageID != latest.ID {
				t.Fatalf("unexpected requeued task: %+v", task)
			}
			if task.ReadyAt != nil || task.NextRetryAt != nil || task.RetryCount != 0 || task.CompletedAt != nil || task.NoticeSentAt != nil || task.LastError != "" {
				t.Fatalf("expected stale execution fields to be cleared, got %+v", task)
			}
			if task.NextReminderAt == nil || time.Until(*task.NextReminderAt) < 90*time.Second || time.Until(*task.NextReminderAt) > 150*time.Second {
				t.Fatalf("expected normal store reminder to be recalculated near two minutes, got %+v", task.NextReminderAt)
			}
		})
	}
}

func TestManualResumeRequeueResetsReminderForSafetyAndHQRoutings(t *testing.T) {
	tests := []struct {
		name          string
		routeStatus   enums.ConversationRouteStatus
		reason        string
		minimumDelay  time.Duration
		maximumDelay  time.Duration
		expectsNotice bool
	}{
		{name: "store safety", routeStatus: enums.ConversationRouteStatusStoreWecomManual, reason: "客人摔倒，属于安全问题", minimumDelay: 45 * time.Second, maximumDelay: 75 * time.Second, expectsNotice: true},
		{name: "hq pending", routeStatus: enums.ConversationRouteStatusHQAgentDeskPending, reason: "等待总部接入"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupMessageWelcomeTestDB(t)
			aiAgent := createWelcomeTestAIAgent(t, db, "")
			external := welcomeTestExternalUser("manual-resume-reminder-" + strings.ReplaceAll(test.name, " ", "-"))
			conversation, err := ConversationService.Create(external, 0, aiAgent.ID)
			if err != nil {
				t.Fatalf("create conversation: %v", err)
			}
			now := time.Now()
			if test.routeStatus == enums.ConversationRouteStatusStoreWecomManual {
				_, err = ConversationRouteService.EnterStoreWecomManual(conversation.ID, test.reason, now)
			} else {
				_, err = ConversationRouteService.EnterHQAgentDeskPending(conversation.ID, test.reason, now)
			}
			if err != nil {
				t.Fatalf("enter manual route: %v", err)
			}
			origin, err := MessageService.SendCustomerMessageWithRequestID(conversation.ID, "manual-reminder-origin-"+test.name, enums.IMMessageTypeText, "第一个问题", "", external, "req-manual-reminder-origin-"+test.name)
			if err != nil {
				t.Fatalf("send origin message: %v", err)
			}
			task := AIManualResumeTaskService.latestActiveTask(conversation.ID, []string{aiManualResumeTaskWaiting})
			if task == nil || task.OriginMessageID != origin.ID {
				t.Fatalf("expected initial task, got %+v", task)
			}
			farFuture := now.Add(time.Hour)
			if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
				"task_status":      aiManualResumeTaskRunning,
				"next_reminder_at": farFuture,
			}).Error; err != nil {
				t.Fatalf("prepare task reminder: %v", err)
			}
			if _, err := MessageService.SendCustomerMessageWithRequestID(conversation.ID, "manual-reminder-latest-"+test.name, enums.IMMessageTypeText, "最新补充", "", external, "req-manual-reminder-latest-"+test.name); err != nil {
				t.Fatalf("send latest message: %v", err)
			}
			if err := db.First(task, task.ID).Error; err != nil {
				t.Fatalf("reload task: %v", err)
			}
			if test.expectsNotice {
				if task.NextReminderAt == nil {
					t.Fatal("expected store safety reminder to be scheduled")
				}
				delay := time.Until(*task.NextReminderAt)
				if delay < test.minimumDelay || delay > test.maximumDelay {
					t.Fatalf("unexpected safety reminder delay %v", delay)
				}
			} else if task.NextReminderAt != nil {
				t.Fatalf("expected HQ route to clear store reminder, got %v", task.NextReminderAt)
			}
		})
	}
}

func TestOldRunningManualResumeDoesNotMutateReclaimedLatestRun(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("manual-resume-running-requeue")
	conversation, err := ConversationService.Create(external, 0, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "等待门店同事", time.Now()); err != nil {
		t.Fatalf("EnterStoreWecomManual() error = %v", err)
	}
	if _, err := MessageService.SendCustomerMessageWithRequestID(conversation.ID, "manual-running-origin", enums.IMMessageTypeText, "原来的问题", "", external, "req-manual-running-origin"); err != nil {
		t.Fatalf("send origin message: %v", err)
	}
	task := AIManualResumeTaskService.latestActiveTask(conversation.ID, []string{aiManualResumeTaskWaiting})
	if task == nil {
		t.Fatal("expected initial waiting task")
	}
	readyAt := time.Now().Add(-time.Second)
	if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"task_status":   aiManualResumeTaskReady,
		"ready_at":      readyAt,
		"next_retry_at": readyAt,
	}).Error; err != nil {
		t.Fatalf("mark task ready: %v", err)
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatalf("reload ready task: %v", err)
	}
	previousHook := TriggerAIReplySyncHook
	defer func() { TriggerAIReplySyncHook = previousHook }()
	var latest *models.Message
	TriggerAIReplySyncHook = func(_ context.Context, _ models.Conversation, _ models.Message) error {
		latest, err = MessageService.SendCustomerMessageWithRequestID(conversation.ID, "manual-running-latest", enums.IMMessageTypeText, "运行期间的新问题", "", external, "req-manual-running-latest")
		if err != nil {
			return err
		}
		return db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Update("task_status", aiManualResumeTaskRunning).Error
	}
	if !AIManualResumeTaskService.processOne(*task, time.Now()) {
		t.Fatal("expected ready task to be claimed")
	}
	if latest == nil {
		t.Fatal("expected customer follow-up during running hook")
	}
	if err := db.First(task, task.ID).Error; err != nil {
		t.Fatalf("reload requeued task: %v", err)
	}
	if task.TaskStatus != aiManualResumeTaskRunning || task.LatestWaitingMessageID != latest.ID || task.RetryCount != 0 {
		t.Fatalf("expected old run to leave the reclaimed latest run untouched, got %+v", task)
	}
	requestID := manualResumeRequestID(task)
	var replyCount int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ? AND request_id = ?", conversation.ID, enums.IMSenderTypeAI, requestID).Count(&replyCount).Error; err != nil {
		t.Fatalf("count stale resume replies: %v", err)
	}
	if replyCount != 0 {
		t.Fatalf("expected old running process to exit without committing, got %d replies", replyCount)
	}
}

func TestManualResumeRejectsOldSourceAfterCustomerFollowUp(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("manual-resume-stale-source")
	conversation, err := ConversationService.Create(external, 0, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "等待门店同事", time.Now()); err != nil {
		t.Fatalf("EnterStoreWecomManual() error = %v", err)
	}
	origin, err := MessageService.SendCustomerMessageWithRequestID(conversation.ID, "manual-resume-stale-origin", enums.IMMessageTypeText, "原来的问题", "", external, "req-manual-resume-stale-origin")
	if err != nil {
		t.Fatalf("send origin customer message: %v", err)
	}
	task := AIManualResumeTaskService.latestActiveTask(conversation.ID, []string{aiManualResumeTaskWaiting})
	if task == nil {
		t.Fatal("expected initial waiting task")
	}
	if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Update("task_status", aiManualResumeTaskRunning).Error; err != nil {
		t.Fatalf("mark old run running: %v", err)
	}
	latest, err := MessageService.SendCustomerMessageWithRequestID(conversation.ID, "manual-resume-stale-latest", enums.IMMessageTypeText, "这是最新补充", "", external, "req-manual-resume-stale-latest")
	if err != nil {
		t.Fatalf("send latest customer message: %v", err)
	}
	if err := db.Model(&models.AIManualResumeTask{}).Where("id = ?", task.ID).Update("task_status", aiManualResumeTaskRunning).Error; err != nil {
		t.Fatalf("mark latest run running: %v", err)
	}
	requestID := manualResumeRequestID(task)
	if _, err := MessageService.SendAIMessageWithRequestIDAndSourceMessageID(
		conversation.ID,
		aiAgent.ID,
		"manual-resume-old-source-reply",
		enums.IMMessageTypeText,
		"这条旧回复不应提交",
		"",
		&dto.AuthPrincipal{Username: "AI"},
		requestID,
		origin.ID,
	); err == nil {
		t.Fatal("expected stale source message to be rejected")
	}
	if _, err := MessageService.SendAIMessageWithRequestIDAndSourceMessageID(
		conversation.ID,
		aiAgent.ID,
		"manual-resume-latest-source-reply",
		enums.IMMessageTypeText,
		"我继续处理最新问题。",
		"",
		&dto.AuthPrincipal{Username: "AI"},
		requestID,
		latest.ID,
	); err != nil {
		t.Fatalf("expected latest source message to commit: %v", err)
	}
}

func TestExternalAgentTakeoverBlocksLaterOrdinaryAICommit(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("agent-wins-ai-race"), 0, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	staleConversation := *conversation
	if _, err := MessageService.CreateExternalAgentMessageWithoutOutbox(conversation.ID, "agent-wins-echo", enums.IMMessageTypeText, "我来处理。", "", "req-agent-wins"); err != nil {
		t.Fatalf("CreateExternalAgentMessageWithoutOutbox() error = %v", err)
	}
	if _, err := MessageService.sendValidatedMessageWithOptions(
		&staleConversation,
		enums.IMSenderTypeAI,
		aiAgent.ID,
		"ordinary-ai-after-takeover",
		enums.IMMessageTypeText,
		"这条不应提交",
		"",
		&dto.AuthPrincipal{Username: "AI"},
		nil,
		"req-ordinary-ai-after-takeover",
		sendMessageOptions{},
	); err == nil {
		t.Fatal("expected lock-time route recheck to reject stale ordinary AI commit")
	}
	var count int64
	if err := db.Model(&models.Message{}).
		Where("conversation_id = ? AND client_msg_id = ?", conversation.ID, "ordinary-ai-after-takeover").
		Count(&count).Error; err != nil {
		t.Fatalf("count blocked AI messages: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no ordinary AI message after takeover, got %d", count)
	}
}

func TestAIServiceNoticeCanSendDuringManualRoute(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID:          14,
		Name:        "企微客服测试渠道",
		ChannelType: enums.ChannelTypeWxWorkKF,
		ChannelID:   "wxwork-kf-service-notice-test",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("manual-service-notice-user"), 14, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "需要人工", time.Now()); err != nil {
		t.Fatalf("EnterStoreWecomManual() error = %v", err)
	}
	message, err := MessageService.sendValidatedMessageWithOptions(
		conversation,
		enums.IMSenderTypeAI,
		aiAgent.ID,
		"ai_handoff_success_manual_notice",
		enums.IMMessageTypeText,
		DirectHandoffSuccessMessage,
		`{"serviceEvent":"human_handoff_dispatched"}`,
		&dto.AuthPrincipal{Username: "system"},
		nil,
		"req-manual-service-notice",
		sendMessageOptions{skipOutbound: true, aiServiceNotice: true},
	)
	if err != nil {
		t.Fatalf("SendAIServiceNoticeWithClientMsgIDAndRequestID() error = %v", err)
	}
	if message == nil || message.Content != DirectHandoffSuccessMessage {
		t.Fatalf("expected service notice during manual route, got %+v", message)
	}
	if handled, err := MessageService.ensureOutboundChannelMessageWithOptions(conversation, message, true); err != nil || !handled {
		t.Fatalf("enqueue service notice outbox handled=%v err=%v", handled, err)
	}
	outbox := ChannelMessageOutboxService.GetByMessageID(enums.ChannelTypeWxWorkKF, message.ID)
	if outbox == nil || !ChannelMessageOutboxService.isAIServiceNotice(*outbox) {
		t.Fatalf("expected service notice outbox marker, got %+v", outbox)
	}
	if allowed, reason := ChannelMessageOutboxService.CanDispatch(*outbox, message); !allowed {
		t.Fatalf("expected service notice dispatch during manual route, reason=%q", reason)
	}
}

func TestClaimForDispatchCancelsOrdinaryAIUnderManualRoute(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID:          15,
		Name:        "企微客服领取测试渠道",
		ChannelType: enums.ChannelTypeWxWorkKF,
		ChannelID:   "wxwork-kf-claim-test",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("manual-claim-user"), 15, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	message, err := MessageService.sendValidatedMessageWithOptions(
		conversation,
		enums.IMSenderTypeAI,
		aiAgent.ID,
		"ordinary-ai-before-manual-claim",
		enums.IMMessageTypeText,
		"这条不应发送",
		"",
		&dto.AuthPrincipal{Username: "AI"},
		nil,
		"req-ordinary-before-manual-claim",
		sendMessageOptions{skipOutbound: true},
	)
	if err != nil {
		t.Fatalf("create ordinary AI message: %v", err)
	}
	if handled, err := MessageService.ensureOutboundChannelMessage(conversation, message); err != nil || !handled {
		t.Fatalf("enqueue ordinary AI outbox handled=%v err=%v", handled, err)
	}
	outbox := ChannelMessageOutboxService.GetByMessageID(enums.ChannelTypeWxWorkKF, message.ID)
	if outbox == nil {
		t.Fatal("expected ordinary AI outbox")
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "员工接管", now); err != nil {
		t.Fatalf("enter manual route: %v", err)
	}
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status": string(enums.ChannelMessageOutboxStatusPending),
		"last_error":  "",
	}).Error; err != nil {
		t.Fatalf("restore stale pending outbox: %v", err)
	}
	claimed, err := ChannelMessageOutboxService.ClaimForDispatch(*outbox, message)
	if err != nil {
		t.Fatalf("ClaimForDispatch() error = %v", err)
	}
	if claimed {
		t.Fatal("ordinary AI outbox must not be claimed after manual takeover")
	}
	if err := db.First(outbox, outbox.ID).Error; err != nil {
		t.Fatalf("reload outbox: %v", err)
	}
	if outbox.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) {
		t.Fatalf("outbox status=%q want cancelled", outbox.SendStatus)
	}
}

func TestExternalAgentEchoTakesOverHQServingAndClearsAssignment(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("hq-to-store-self-echo"), 0, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	now := time.Now()
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{
		"status":              enums.IMConversationStatusActive,
		"current_team_id":     int64(1),
		"current_assignee_id": int64(101),
	}).Error; err != nil {
		t.Fatalf("assign conversation: %v", err)
	}
	assignment := &models.ConversationAssignment{
		ConversationID: conversation.ID,
		ToUserID:       101,
		Status:         enums.IMAssignmentStatusActive,
		CreatedAt:      now,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if _, err := ConversationRouteService.EnterHQAgentDeskServing(conversation.ID, "HQ人工接待", now); err != nil {
		t.Fatalf("EnterHQAgentDeskServing() error = %v", err)
	}
	if _, err := MessageService.CreateExternalAgentMessageWithoutOutbox(conversation.ID, "hq-store-self-echo", enums.IMMessageTypeText, "门店同事已接手。", "", "req-hq-store-self-echo"); err != nil {
		t.Fatalf("CreateExternalAgentMessageWithoutOutbox() error = %v", err)
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil || state.RouteStatus != enums.ConversationRouteStatusStoreWecomManual || state.NeedHumanFollowUp || state.ManualExpireAt == nil {
		t.Fatalf("expected trusted employee echo to own the store manual route, got %+v", state)
	}
	current := ConversationService.Get(conversation.ID)
	if current == nil || current.Status != enums.IMConversationStatusAIServing || current.CurrentAssigneeID != 0 || current.CurrentTeamID != 0 {
		t.Fatalf("expected old HQ assignment cleared, got %+v", current)
	}
	if err := db.First(assignment, assignment.ID).Error; err != nil {
		t.Fatalf("reload assignment: %v", err)
	}
	if assignment.Status != enums.IMAssignmentStatusInactive || assignment.FinishedAt == nil {
		t.Fatalf("expected old assignment finished, got %+v", assignment)
	}
}

func TestMiniprogramHumanSupportUsesActiveRouteInsteadOfHandoffHistory(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("miniprogram-handoff-history"), 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Update("handoff_at", time.Now()).Error; err != nil {
		t.Fatalf("write historical handoff: %v", err)
	}
	updated := ConversationService.Get(conversation.ID)
	if MiniprogramChatService.needHumanSupport("转人工", updated) {
		t.Fatal("expected historical handoff alone not to report active human support")
	}
	if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "正在门店人工接待", time.Now()); err != nil {
		t.Fatalf("enter store manual: %v", err)
	}
	if !MiniprogramChatService.needHumanSupport("任意消息", ConversationService.Get(conversation.ID)) {
		t.Fatal("expected active manual route to report human support")
	}
}

func TestSendCustomerMessageMiniProgramKeywordsGoThroughAIReplyHook(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("runtime-resource-user")
	conversation, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	previousHook := TriggerAIReplyAsyncHook
	var hookMessageID int64
	TriggerAIReplyAsyncHook = func(conversation models.Conversation, message models.Message) {
		hookMessageID = message.ID
	}
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousHook
	})

	message, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"client-msg-runtime-resource",
		enums.IMMessageTypeText,
		"我要续住怎么操作必须原平台吗",
		"",
		external,
		"runtime-resource-123",
	)
	if err != nil {
		t.Fatalf("SendCustomerMessageWithRequestID() error = %v", err)
	}
	if hookMessageID != message.ID {
		t.Fatalf("expected runtime AI hook for mini-program keywords, got hook message id %d want %d", hookMessageID, message.ID)
	}

	var miniProgramCount int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND message_type = ?", conversation.ID, enums.IMMessageTypeMiniProgram).Count(&miniProgramCount).Error; err != nil {
		t.Fatalf("count mini program messages: %v", err)
	}
	if miniProgramCount != 0 {
		t.Fatalf("expected no pre-runtime mini program message, got %d", miniProgramCount)
	}
}

func TestStandaloneOneUsesIndependentReplyHook(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("standalone-one-hook")
	conversation, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	previousAIHook := TriggerAIReplyAsyncHook
	previousStandaloneHook := TriggerStandaloneOneReplyAsyncHook
	var aiHookCount int
	var standaloneHookCount int
	var standaloneMessageID int64
	TriggerAIReplyAsyncHook = func(models.Conversation, models.Message) {
		aiHookCount++
	}
	TriggerStandaloneOneReplyAsyncHook = func(_ models.Conversation, message models.Message) {
		standaloneHookCount++
		standaloneMessageID = message.ID
	}
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousAIHook
		TriggerStandaloneOneReplyAsyncHook = previousStandaloneHook
	})

	message, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"standalone-one-hook-message",
		enums.IMMessageTypeText,
		" 1 ",
		"",
		external,
		"standalone-one-hook-request",
	)
	if err != nil {
		t.Fatalf("send standalone one: %v", err)
	}
	if standaloneHookCount != 1 || standaloneMessageID != message.ID || aiHookCount != 0 {
		t.Fatalf("unexpected hook routing: standalone=%d message=%d ai=%d", standaloneHookCount, standaloneMessageID, aiHookCount)
	}

	duplicate, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"standalone-one-hook-message",
		enums.IMMessageTypeText,
		"1",
		"",
		external,
		"standalone-one-hook-request",
	)
	if err != nil || duplicate.ID != message.ID {
		t.Fatalf("send duplicate standalone one: message=%#v err=%v", duplicate, err)
	}
	if standaloneHookCount != 2 || aiHookCount != 0 {
		t.Fatalf("duplicate did not retry independent hook: standalone=%d ai=%d", standaloneHookCount, aiHookCount)
	}
}

func TestSendCustomerMessageStoreWecomManualBlocksAIUntilTimeout(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("store-manual-ai-user")
	conversation, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	conversation.LastActiveAt = time.Now()
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Update("last_active_at", conversation.LastActiveAt).Error; err != nil {
		t.Fatalf("set conversation last active at: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		Guid:           "store-manual-ai-guid",
		AIReplyEnabled: true,
		Status:         enums.StatusOk,
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create wx work protocol instance: %v", err)
	}
	route, err := ConversationRouteService.Ensure(conversation.ID)
	if err != nil {
		t.Fatalf("ensure route: %v", err)
	}
	if err := db.Model(&models.ConversationRouteState{}).Where("id = ?", route.ID).Updates(map[string]any{
		"wx_work_instance_id": instance.ID,
		"route_status":        enums.ConversationRouteStatusStoreWecomManual,
		"route_target":        "store_wecom",
	}).Error; err != nil {
		t.Fatalf("update route: %v", err)
	}

	previousHook := TriggerAIReplyAsyncHook
	var hookMessageID int64
	TriggerAIReplyAsyncHook = func(conversation models.Conversation, message models.Message) {
		hookMessageID = message.ID
	}
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousHook
	})

	message, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"client-msg-store-manual-ai",
		enums.IMMessageTypeText,
		"你是哪位啊",
		"",
		external,
		"store-manual-ai-123",
	)
	if err != nil {
		t.Fatalf("SendCustomerMessageWithRequestID() error = %v", err)
	}
	if hookMessageID != 0 {
		t.Fatalf("expected store manual route to block ai hook until timeout, got hook message id %d for message %d", hookMessageID, message.ID)
	}
}

func TestSendCustomerMessageStoreWecomManualRespectsDisabledAIReply(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("store-manual-ai-disabled-user")
	conversation, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	conversation.LastActiveAt = time.Now()
	if err := db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).Update("last_active_at", conversation.LastActiveAt).Error; err != nil {
		t.Fatalf("set conversation last active at: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		Guid:           "store-manual-ai-disabled-guid",
		AIReplyEnabled: false,
		Status:         enums.StatusOk,
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create wx work protocol instance: %v", err)
	}
	if err := db.Model(instance).Update("ai_reply_enabled", false).Error; err != nil {
		t.Fatalf("disable wx work protocol ai reply: %v", err)
	}
	route, err := ConversationRouteService.Ensure(conversation.ID)
	if err != nil {
		t.Fatalf("ensure route: %v", err)
	}
	if err := db.Model(&models.ConversationRouteState{}).Where("id = ?", route.ID).Updates(map[string]any{
		"wx_work_instance_id": instance.ID,
		"route_status":        enums.ConversationRouteStatusStoreWecomManual,
		"route_target":        "store_wecom",
	}).Error; err != nil {
		t.Fatalf("update route: %v", err)
	}

	previousHook := TriggerAIReplyAsyncHook
	called := false
	TriggerAIReplyAsyncHook = func(conversation models.Conversation, message models.Message) {
		called = true
	}
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousHook
	})

	if _, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"client-msg-store-manual-ai-disabled",
		enums.IMMessageTypeText,
		"你是哪位啊",
		"",
		external,
		"store-manual-ai-disabled-123",
	); err != nil {
		t.Fatalf("SendCustomerMessageWithRequestID() error = %v", err)
	}
	if called {
		t.Fatalf("expected disabled employee account ai reply switch to stop ai hook")
	}
}

func TestSendCustomerGifDoesNotTriggerAIReplyHook(t *testing.T) {
	setupMessageWelcomeTestDB(t)
	if shouldTriggerAIReply(enums.IMMessageTypeGIF) {
		t.Fatal("expected pure GIF/sticker messages not to trigger AI reply")
	}
}

func TestConversationCreateDoesNotDuplicateWelcomeMessageForExistingConversation(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "欢迎咨询")
	external := welcomeTestExternalUser("u-2")

	first, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create first conversation: %v", err)
	}
	second, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create second conversation: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected existing conversation id %d, got %d", first.ID, second.ID)
	}

	var count int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ?", first.ID).Count(&count).Error; err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one welcome message, got %d", count)
	}
}

func TestConversationCreateSkipsBlankWelcomeMessage(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "   ")

	conversation, err := ConversationService.Create(welcomeTestExternalUser("blank-welcome-1"), 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if conversation == nil {
		t.Fatalf("expected conversation")
	}

	var count int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ?", conversation.ID).Count(&count).Error; err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no welcome messages, got %d", count)
	}

	var updated models.Conversation
	if err := db.First(&updated, conversation.ID).Error; err != nil {
		t.Fatalf("find conversation: %v", err)
	}
	if updated.LastMessageID != 0 {
		t.Fatalf("expected last message id 0, got %d", updated.LastMessageID)
	}
	if updated.CustomerUnreadCount != 0 {
		t.Fatalf("expected customer unread count 0, got %d", updated.CustomerUnreadCount)
	}
}

func TestConversationCreateWelcomeMessageDoesNotTriggerAIReplyHook(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "欢迎咨询")

	previousHook := TriggerAIReplyAsyncHook
	called := false
	TriggerAIReplyAsyncHook = func(conversation models.Conversation, message models.Message) {
		called = true
	}
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousHook
	})

	if _, err := ConversationService.Create(welcomeTestExternalUser("hook-welcome-1"), 11, aiAgent.ID); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if called {
		t.Fatalf("expected welcome message not to trigger ai reply hook")
	}
}

func TestMediaUnderstandingTriggersRecentTextFollowUp(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	media := models.Message{
		ID:             100,
		ConversationID: 99,
		ClientMsgID:    "media-100",
		SeqNo:          1,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "room.jpg",
		SentAt:         &now,
	}
	followAt := now.Add(2 * time.Second)
	follow := models.Message{
		ID:             101,
		ConversationID: 99,
		ClientMsgID:    "follow-101",
		SeqNo:          2,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeText,
		Content:        "这个多少钱",
		SentAt:         &followAt,
	}
	if err := db.Create(&media).Error; err != nil {
		t.Fatalf("create media: %v", err)
	}
	if err := db.Create(&follow).Error; err != nil {
		t.Fatalf("create follow: %v", err)
	}

	got := MediaUnderstandingService.latestCustomerFollowUp(media)
	if got == nil || got.ID != follow.ID {
		if got == nil {
			t.Fatalf("expected recent text follow-up to be triggered, got nil")
		}
		t.Fatalf("expected recent text follow-up to be triggered, got message %d", got.ID)
	}
}

func TestMediaUnderstandingDoesNotTriggerGifFollowUp(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	media := models.Message{
		ID:             110,
		ConversationID: 99,
		ClientMsgID:    "media-110",
		SeqNo:          1,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "room.jpg",
		SentAt:         &now,
	}
	followAt := now.Add(2 * time.Second)
	follow := models.Message{
		ID:             111,
		ConversationID: 99,
		ClientMsgID:    "gif-111",
		SeqNo:          2,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeGIF,
		Content:        "动画表情",
		SentAt:         &followAt,
	}
	if err := db.Create(&media).Error; err != nil {
		t.Fatalf("create media: %v", err)
	}
	if err := db.Create(&follow).Error; err != nil {
		t.Fatalf("create follow: %v", err)
	}

	if got := MediaUnderstandingService.latestCustomerFollowUp(media); got != nil {
		t.Fatalf("expected gif/sticker follow-up not to trigger ai reply, got message %d", got.ID)
	}
}

func TestMediaUnderstandingDoesNotTriggerWithoutTextFollowUp(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	media := models.Message{
		ID:             102,
		ConversationID: 99,
		ClientMsgID:    "media-102",
		SeqNo:          1,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeImage,
		Content:        "food.jpg",
		SentAt:         &now,
	}
	if err := db.Create(&media).Error; err != nil {
		t.Fatalf("create media: %v", err)
	}
	if got := MediaUnderstandingService.latestCustomerFollowUp(media); got != nil {
		t.Fatalf("expected no ai trigger for media-only message, got message %d", got.ID)
	}
}

func TestMediaUnderstandingActionableImageCanTriggerWithoutTextFollowUp(t *testing.T) {
	message := models.Message{
		MessageType: enums.IMMessageTypeImage,
		Payload:     `{"mediaText":"截图里显示小程序打不开，并有文字提示：怎么处理？","mediaUnderstandingStatus":"understood"}`,
	}
	if !MediaUnderstandingService.mediaUnderstandingLooksActionable(message) {
		t.Fatal("expected actionable image understanding to trigger ai reply")
	}
}

func TestMediaUnderstandingVoiceTranscriptTriggersWithoutActionKeywords(t *testing.T) {
	message := models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Payload:     `{"mediaText":"我没给你发语音大哥。","mediaUnderstandingStatus":"understood"}`,
	}
	if !MediaUnderstandingService.mediaUnderstandingShouldTriggerAI(message) {
		t.Fatal("expected understood voice transcript to trigger ai reply")
	}
}

func TestMediaMessageAlreadyUnderstoodSkipsDuplicateUnderstanding(t *testing.T) {
	message := models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Payload:     `{"mediaText":"早餐几点开始，停车免费吗？","mediaSummary":"客户语音询问早餐时间和停车是否免费。","mediaUnderstandingStatus":"understood"}`,
	}
	if !mediaMessageAlreadyUnderstood(message) {
		t.Fatal("expected understood voice payload to skip duplicate media understanding")
	}
	pending := models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Payload:     `{"filename":"voice.amr","mediaUnderstandingStatus":"pending"}`,
	}
	if mediaMessageAlreadyUnderstood(pending) {
		t.Fatal("pending media must still run media understanding")
	}
}

func TestNormalizeMediaMessagePreservesPreUnderstoodPayload(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	asset := &models.Asset{
		AssetID:    "asset-voice-understood",
		Provider:   enums.AssetProviderLocal,
		StorageKey: "voice/understood.amr",
		Filename:   "understood.amr",
		FileSize:   123,
		MimeType:   "audio/amr",
		Status:     enums.AssetStatusSuccess,
		AuditFields: models.AuditFields{
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	if err := db.Create(asset).Error; err != nil {
		t.Fatalf("create asset: %v", err)
	}
	_, payload, _, err := MessageService.normalizeMessageContent(
		99,
		enums.IMMessageTypeVoice,
		"",
		`{"assetId":"asset-voice-understood","mediaText":"早餐几点开始，停车免费吗？","mediaSummary":"客户语音询问早餐时间和停车是否免费。","mediaUnderstandingStatus":"understood","mediaUnderstandingError":"上一轮失败残留"}`,
	)
	if err != nil {
		t.Fatalf("normalizeMessageContent() error = %v", err)
	}
	message := models.Message{MessageType: enums.IMMessageTypeVoice, Payload: payload}
	if !mediaMessageAlreadyUnderstood(message) {
		t.Fatalf("expected normalized voice payload to keep understood media text, got %s", payload)
	}
	if strings.Contains(payload, "上一轮失败残留") {
		t.Fatalf("expected understood media payload to clear stale error, got %s", payload)
	}
}

func TestPreUnderstoodVoiceMessageTriggersAIHook(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("voice-ready"), 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	now := time.Now()
	message := &models.Message{
		ConversationID: conversation.ID,
		ClientMsgID:    "voice-ready-1",
		SeqNo:          1,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    enums.IMMessageTypeVoice,
		Content:        "voice-ready.amr",
		Payload:        `{"mediaText":"早餐几点开始，停车免费吗？","mediaUnderstandingStatus":"understood"}`,
		RequestID:      "voice-ready-request",
		SentAt:         &now,
		AuditFields: models.AuditFields{
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create voice message: %v", err)
	}
	previousHook := TriggerAIReplyAsyncHook
	var triggeredMessageID int64
	TriggerAIReplyAsyncHook = func(conversation models.Conversation, message models.Message) {
		triggeredMessageID = message.ID
	}
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousHook
	})
	if err := MediaUnderstandingService.UnderstandInboundMessage(context.Background(), message.ID); err != nil {
		t.Fatalf("UnderstandInboundMessage() error = %v", err)
	}
	if triggeredMessageID != message.ID {
		t.Fatalf("expected pre-understood voice to trigger ai hook for message %d, got %d", message.ID, triggeredMessageID)
	}
}

func TestMediaUnderstandingPlainFoodImageDoesNotTriggerWithoutTextFollowUp(t *testing.T) {
	message := models.Message{
		MessageType: enums.IMMessageTypeImage,
		Payload:     `{"mediaText":"图片中可见一份外卖餐食，含白米饭、拌鸡丝菜和饮品。","mediaUnderstandingStatus":"understood"}`,
	}
	if MediaUnderstandingService.mediaUnderstandingLooksActionable(message) {
		t.Fatal("expected plain food image to wait for customer follow-up")
	}
}

func TestMediaUnderstandingNegatedIntentDoesNotTrigger(t *testing.T) {
	message := models.Message{
		MessageType: enums.IMMessageTypeImage,
		Payload:     `{"mediaText":"图片为客人自拍，左上角小窗显示模糊的室内环境，无清晰文字、报错或明确服务诉求信息。","mediaUnderstandingStatus":"understood"}`,
	}
	if MediaUnderstandingService.mediaUnderstandingLooksActionable(message) {
		t.Fatal("expected negated no-intent image understanding to wait for customer follow-up")
	}
}
