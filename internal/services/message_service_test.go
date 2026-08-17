package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/repositories"

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
		&models.User{},
		&models.Role{},
		&models.UserRole{},
		&models.AgentTeam{},
		&models.AIAgent{},
		&models.Channel{},
		&models.ChannelMessageOutbox{},
		&models.Store{},
		&models.StoreStaffBinding{},
		&models.StoreCustomerRelation{},
		&models.KnowledgeBase{},
		&models.WxWorkProtocolInstance{},
		&models.Customer{},
		&models.CustomerIdentity{},
		&models.WxWorkCustomerHandoffSetting{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.ConversationChannelSession{},
		&models.ConversationContinuityLink{},
		&models.ConversationParticipant{},
		&models.ConversationReadState{},
		&models.ConversationEventLog{},
		&models.ConversationAssignment{},
		&models.Message{},
		&models.AIReplyTurn{},
		&models.AIReplyTurnTask{},
		&models.AIReplyTurnAction{},
		&models.AIReplyJob{},
		&models.ConversationInterrupt{},
		&models.AgentRunLog{},
		&models.ConversationServiceSession{},
		&models.ConversationResponseSpan{},
		&models.DispatchDecisionLog{},
		&models.Asset{},
		&models.WxWorkKFConversation{},
		&models.WxWorkKFMessageRef{},
		&models.MessageSyncLog{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)
	if err := db.Create(&models.Channel{
		ID: 11, TenantID: 101, Name: "测试网页渠道", ChannelType: enums.ChannelTypeWeb,
		ChannelID: "message-welcome-test", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}).Error; err != nil {
		t.Fatalf("create default channel: %v", err)
	}
	return db
}

func createWelcomeTestAIAgent(t *testing.T, db *gorm.DB, welcomeMessage string) *models.AIAgent {
	t.Helper()

	now := time.Now()
	aiAgent := &models.AIAgent{
		TenantID:       101,
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

func TestConversationCreateCreatesSystemWelcomeMessage(t *testing.T) {
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
	if message.SenderType != enums.IMSenderTypeSystem {
		t.Fatalf("expected sender type system, got %q", message.SenderType)
	}
	if message.SenderID != 0 {
		t.Fatalf("expected system sender id 0, got %d", message.SenderID)
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

func TestConversationCreatePersistsWelcomeOutboundIntentAndOutbox(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID:          12,
		TenantID:    101,
		Name:        "企微 CLI",
		ChannelType: enums.ChannelTypeWxWorkCLI,
		ChannelID:   "welcome-outbound",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create outbound channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "欢迎来到合成验收")

	conversation, err := ConversationService.Create(welcomeTestExternalUser("welcome-outbound"), 12, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	var message models.Message
	if err := db.Where("conversation_id = ?", conversation.ID).First(&message).Error; err != nil {
		t.Fatalf("find welcome message: %v", err)
	}
	if message.OutboundChannelType != enums.ChannelTypeWxWorkCLI {
		t.Fatalf("outbound channel type=%q want %q", message.OutboundChannelType, enums.ChannelTypeWxWorkCLI)
	}
	var outbox models.ChannelMessageOutbox
	if err := db.Where("tenant_id = ? AND channel_type = ? AND message_id = ?", 101, enums.ChannelTypeWxWorkCLI, message.ID).First(&outbox).Error; err != nil {
		t.Fatalf("find welcome outbox: %v", err)
	}
}

func TestSendMessageClientMsgIDRetryRepairsMissingOutbox(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID:          12,
		TenantID:    101,
		Name:        "企微 CLI",
		ChannelType: enums.ChannelTypeWxWorkCLI,
		ChannelID:   "retry-outbound",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create outbound channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("retry-outbound"), 12, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 1, Username: "system", ActiveTenantID: 101}
	message, err := MessageService.SendAIMessageWithRequestID(
		conversation.ID,
		aiAgent.ID,
		"stable-outbound-message",
		enums.IMMessageTypeText,
		"这是一条稳定回复",
		"",
		operator,
		"request-outbound",
	)
	if err != nil {
		t.Fatalf("send AI message: %v", err)
	}
	if message.OutboundChannelType != enums.ChannelTypeWxWorkCLI {
		t.Fatalf("outbound channel type=%q want %q", message.OutboundChannelType, enums.ChannelTypeWxWorkCLI)
	}
	if err := db.Where("message_id = ?", message.ID).Delete(&models.ChannelMessageOutbox{}).Error; err != nil {
		t.Fatalf("delete outbox to simulate post-commit loss: %v", err)
	}

	retried, err := MessageService.SendAIMessageWithRequestID(
		conversation.ID,
		aiAgent.ID,
		"stable-outbound-message",
		enums.IMMessageTypeText,
		"这是一条稳定回复",
		"",
		operator,
		"request-outbound-retry",
	)
	if err != nil {
		t.Fatalf("retry AI message: %v", err)
	}
	if retried.ID != message.ID {
		t.Fatalf("retry message id=%d want original %d", retried.ID, message.ID)
	}
	var messageCount int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ?", conversation.ID).Count(&messageCount).Error; err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 1 {
		t.Fatalf("message count=%d want 1", messageCount)
	}
	var outboxCount int64
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("message_id = ?", message.ID).Count(&outboxCount).Error; err != nil {
		t.Fatalf("count repaired outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("outbox count=%d want 1", outboxCount)
	}
}

func TestRepairMissingOutboundMessagesIsIdempotent(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID:          12,
		TenantID:    101,
		Name:        "企微 CLI",
		ChannelType: enums.ChannelTypeWxWorkCLI,
		ChannelID:   "repair-outbound",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create outbound channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("repair-outbound"), 12, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	message, err := MessageService.SendAIMessageWithRequestID(
		conversation.ID,
		aiAgent.ID,
		"repair-outbound-message",
		enums.IMMessageTypeText,
		"等待补偿",
		"",
		&dto.AuthPrincipal{UserID: 1, Username: "system", ActiveTenantID: 101},
		"request-repair",
	)
	if err != nil {
		t.Fatalf("send AI message: %v", err)
	}
	if err := db.Where("message_id = ?", message.ID).Delete(&models.ChannelMessageOutbox{}).Error; err != nil {
		t.Fatalf("delete outbox to simulate loss: %v", err)
	}

	repaired, err := ChannelMessageOutboxService.RepairMissingOutboundMessages(10)
	if err != nil {
		t.Fatalf("repair missing outbox: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired=%d want 1", repaired)
	}
	repaired, err = ChannelMessageOutboxService.RepairMissingOutboundMessages(10)
	if err != nil {
		t.Fatalf("repeat repair missing outbox: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repeat repaired=%d want 0", repaired)
	}
	var outboxCount int64
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("message_id = ?", message.ID).Count(&outboxCount).Error; err != nil {
		t.Fatalf("count repaired outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("outbox count=%d want 1", outboxCount)
	}
}

func TestSendAIMessageBatchRollsBackTextAndLocationTogether(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID: 12, TenantID: 101, Name: "企微 CLI", ChannelType: enums.ChannelTypeWxWorkCLI,
		ChannelID: "atomic-batch-outbound", Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("atomic-batch"), 12, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	callbackName := "test:fail-second-ai-batch-message"
	if err := db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		message, ok := tx.Statement.Dest.(*models.Message)
		if ok && message.ClientMsgID == "atomic-batch-2" {
			tx.AddError(errors.New("injected second message failure"))
		}
	}); err != nil {
		t.Fatalf("register create callback: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove(callbackName)
	})

	_, err = MessageService.SendAIMessageBatchWithRequestID(
		conversation.ID,
		aiAgent.ID,
		[]AIOutboundMessageDraft{
			{ClientMsgID: "atomic-batch-1", MessageType: enums.IMMessageTypeText, Content: "第一段"},
			{
				ClientMsgID: "atomic-batch-2", MessageType: enums.IMMessageTypeLocation,
				Content: "测试酒店定位", Payload: `{"longitude":117.2639,"latitude":31.824091,"label":"测试酒店"}`,
			},
		},
		&dto.AuthPrincipal{UserID: 1, Username: "AI", ActiveTenantID: 101},
		"atomic-batch-request",
	)
	if err == nil {
		t.Fatal("expected injected batch commit failure")
	}
	var messageCount int64
	if err := db.Model(&models.Message{}).
		Where("conversation_id = ? AND client_msg_id IN ?", conversation.ID, []string{"atomic-batch-1", "atomic-batch-2"}).
		Count(&messageCount).Error; err != nil {
		t.Fatalf("count rolled back messages: %v", err)
	}
	var outboxCount int64
	if err := db.Model(&models.ChannelMessageOutbox{}).
		Where("conversation_id = ?", conversation.ID).
		Count(&outboxCount).Error; err != nil {
		t.Fatalf("count rolled back outbox rows: %v", err)
	}
	if messageCount != 0 || outboxCount != 0 {
		t.Fatalf("partial batch persisted messages=%d outbox=%d", messageCount, outboxCount)
	}
}

func TestChannelMessageOutboxClaimHonorsNextRetryAt(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	future := now.Add(time.Hour)
	outbox := &models.ChannelMessageOutbox{
		TenantID: 101, ChannelType: enums.ChannelTypeWxWorkCLI,
		ConversationID: 9001, MessageID: 9002,
		SendStatus: string(enums.ChannelMessageOutboxStatusFailed), NextRetryAt: &future,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create future outbox: %v", err)
	}
	if pending := ChannelMessageOutboxService.ListPending(enums.ChannelTypeWxWorkCLI, 10); len(pending) != 0 {
		t.Fatalf("future outbox became pending: %#v", pending)
	}
	if claimed, err := ChannelMessageOutboxService.TryMarkSending(outbox.ID, outbox.TenantID); err != nil || claimed {
		t.Fatalf("future outbox claim claimed=%v err=%v", claimed, err)
	}
	due := now.Add(-time.Second)
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Update("next_retry_at", due).Error; err != nil {
		t.Fatalf("make outbox due: %v", err)
	}
	if claimed, err := ChannelMessageOutboxService.TryMarkSending(outbox.ID, outbox.TenantID); err != nil || !claimed {
		t.Fatalf("due outbox claim claimed=%v err=%v", claimed, err)
	}
	if claimed, err := ChannelMessageOutboxService.TryMarkSending(outbox.ID, outbox.TenantID); err != nil || claimed {
		t.Fatalf("outbox claimed twice claimed=%v err=%v", claimed, err)
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
		ID:          12,
		TenantID:    101,
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
	conversation, err := ConversationService.Create(external, 12, aiAgent.ID)
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
	if message.OutboundChannelType != "" {
		t.Fatalf("expected self echo to have no outbound intent, got %q", message.OutboundChannelType)
	}
	repaired, err := ChannelMessageOutboxService.RepairMissingOutboundMessages(10)
	if err != nil {
		t.Fatalf("repair missing outbox: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("expected self echo to stay excluded from repair, repaired=%d", repaired)
	}
}

func TestCreateExternalAgentMessageWithoutOutboxTakesOverAIServingRoute(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	if err := db.Create(&models.Channel{
		ID:          12,
		TenantID:    101,
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

func TestSendCustomerMessageMiniProgramKeywordsEnqueueAIReplyJob(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("runtime-resource-user")
	conversation, err := ConversationService.Create(external, 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

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
	job := repositories.AIReplyJobRepository.GetByMessageInTenant(db, message.TenantID, message.ConversationID, message.ID)
	if job == nil || job.TriggerKind != enums.AIReplyJobTriggerKindText || job.Status != enums.AIReplyJobStatusPending {
		t.Fatalf("expected one pending text AI reply job, got %#v", job)
	}

	var miniProgramCount int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND message_type = ?", conversation.ID, enums.IMMessageTypeMiniProgram).Count(&miniProgramCount).Error; err != nil {
		t.Fatalf("count mini program messages: %v", err)
	}
	if miniProgramCount != 0 {
		t.Fatalf("expected no pre-runtime mini program message, got %d", miniProgramCount)
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
	if job := repositories.AIReplyJobRepository.GetByMessageInTenant(db, message.TenantID, message.ConversationID, message.ID); job == nil {
		t.Fatal("expected manual-route customer message to retain a durable task for worker state evaluation")
	}
	var aiMessageCount int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ? AND id > ?", conversation.ID, enums.IMSenderTypeAI, message.ID).Count(&aiMessageCount).Error; err != nil {
		t.Fatal(err)
	}
	if aiMessageCount != 0 {
		t.Fatalf("expected no immediate AI reply on manual route, got %d", aiMessageCount)
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

	message, err := MessageService.SendCustomerMessageWithRequestID(
		conversation.ID,
		"client-msg-store-manual-ai-disabled",
		enums.IMMessageTypeText,
		"你是哪位啊",
		"",
		external,
		"store-manual-ai-disabled-123",
	)
	if err != nil {
		t.Fatalf("SendCustomerMessageWithRequestID() error = %v", err)
	}
	if job := repositories.AIReplyJobRepository.GetByMessageInTenant(db, message.TenantID, message.ConversationID, message.ID); job == nil {
		t.Fatal("expected disabled-account customer message to retain a durable task for worker state evaluation")
	}
	var aiMessageCount int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ? AND id > ?", conversation.ID, enums.IMSenderTypeAI, message.ID).Count(&aiMessageCount).Error; err != nil {
		t.Fatal(err)
	}
	if aiMessageCount != 0 {
		t.Fatalf("expected disabled employee account to produce no immediate AI reply, got %d", aiMessageCount)
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

func TestConversationCreateWelcomeMessageDoesNotEnqueueAIReplyJob(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "欢迎咨询")

	if _, err := ConversationService.Create(welcomeTestExternalUser("hook-welcome-1"), 11, aiAgent.ID); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	var count int64
	if err := db.Model(&models.AIReplyJob{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected welcome message not to enqueue AI reply job, got %d", count)
	}
}

func TestMediaUnderstandingTriggersRecentTextFollowUp(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	media := models.Message{
		ID:             100,
		TenantID:       101,
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
		TenantID:       101,
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
		TenantID:       101,
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
		TenantID:       101,
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
		TenantID:       101,
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

func TestMediaUnderstandingUsesStructuredResponseExpectationBeforeLegacyKeywords(t *testing.T) {
	ordinary := models.Message{
		MessageType: enums.IMMessageTypeImage,
		Payload:     `{"mediaText":"画面里有停车、地址和电话文字，但只是普通宣传页。","mediaUnderstandingStatus":"understood","responseExpectation":{"mode":"none","basis":"ordinary_media","confidence":0.99}}`,
	}
	if MediaUnderstandingService.mediaUnderstandingShouldTriggerAI(ordinary) {
		t.Fatal("structured none must not be overridden by legacy keywords")
	}
	visibleError := models.Message{
		MessageType: enums.IMMessageTypeImage,
		Payload:     `{"mediaText":"电视画面停在加载页。","mediaUnderstandingStatus":"understood","responseExpectation":{"mode":"reply","basis":"visible_error","confidence":0.95}}`,
	}
	if !MediaUnderstandingService.mediaUnderstandingShouldTriggerAI(visibleError) {
		t.Fatal("structured visible error must trigger without a keyword match")
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
		TenantID:   101,
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
	if err := db.Create(&models.Conversation{ID: 99, TenantID: 101, Status: enums.IMConversationStatusAIServing}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
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

func TestPreUnderstoodVoiceMessageUsesDurableAIReplyJob(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	conversation, err := ConversationService.Create(welcomeTestExternalUser("voice-ready"), 11, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	now := time.Now()
	message := &models.Message{
		TenantID:       conversation.TenantID,
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
	job, created, err := AIReplyJobService.EnsureForMessage(message.ID)
	if err != nil || !created || job == nil || job.TriggerKind != enums.AIReplyJobTriggerKindMedia {
		t.Fatalf("expected durable media job, created=%v job=%#v err=%v", created, job, err)
	}
	if err := MediaUnderstandingService.UnderstandInboundMessage(context.Background(), message.ID); err != nil {
		t.Fatalf("UnderstandInboundMessage() error = %v", err)
	}
	var aiMessageCount int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeAI).Count(&aiMessageCount).Error; err != nil {
		t.Fatal(err)
	}
	if aiMessageCount != 0 {
		t.Fatalf("media understanding must not bypass the durable worker, got %d AI messages", aiMessageCount)
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
