package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
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
	previousHook := TriggerAIReplyAsyncHook
	TriggerAIReplyAsyncHook = nil
	t.Cleanup(func() {
		sqls.SetDB(nil)
		TriggerAIReplyAsyncHook = previousHook
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

func TestPreUnderstoodVoiceMessageTriggersAIHook(t *testing.T) {
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
