package services

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
)

func TestWxWorkProtocolLocationMessageIsNotVoice(t *testing.T) {
	msg := request.WxProtocolChatMsg{
		MsgID:       "1001491",
		MsgType:     wxProtocolMsgLocation,
		ContentType: 6,
		Longitude:   117.281937,
		Latitude:    31.716152,
		Title:       "丽斯未来酒店(合肥滨湖时代广场店)",
		Address:     "安徽省合肥市包河区西藏路1318号众悦广场1501",
		Zoom:        15,
	}
	msg.Normalize()

	if got := msg.InferMsgType(); got != wxProtocolMsgLocation {
		t.Fatalf("expected inferred location msg_type=%d, got %d", wxProtocolMsgLocation, got)
	}

	svc := &wxWorkProtocolService{}
	if got := svc.resolveInboundMessageType(msg); got != enums.IMMessageTypeLocation {
		t.Fatalf("expected location message type, got %s", got)
	}
	content, payload, err := svc.buildInboundMessageContent(nil, enums.IMMessageTypeLocation, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != msg.Title {
		t.Fatalf("expected location title content, got %q", content)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatalf("invalid payload json: %v", err)
	}
	if body["longitude"] != msg.Longitude || body["latitude"] != msg.Latitude || body["title"] != msg.Title || body["address"] != msg.Address {
		t.Fatalf("unexpected location payload: %#v", body)
	}
}

func TestWxWorkProtocolTextWithStaleVoiceTimeIsNotVoice(t *testing.T) {
	msg := request.WxProtocolChatMsg{
		MsgID:       "1003888",
		ContentType: 16,
		Content:     "我没给你发语音大哥",
		VoiceTime:   3,
	}
	msg.Normalize()

	if got := msg.InferMsgType(); got != wxProtocolMsgText {
		t.Fatalf("expected stale voice_time text to infer text msg_type=%d, got %d", wxProtocolMsgText, got)
	}
	svc := &wxWorkProtocolService{}
	if got := svc.resolveInboundMessageType(msg); got != enums.IMMessageTypeText {
		t.Fatalf("expected text message type, got %s", got)
	}
}

func TestWxWorkProtocolSkipsReferencedMutationMessage(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	instance := &models.WxWorkProtocolInstance{
		Guid:           "guid-refer",
		EmployeeUserID: "employee-1",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	svc := &wxWorkProtocolService{}
	err := svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "1003228",
		Sender:      "external-user-1",
		Receiver:    "employee-1",
		ContentType: 16,
		MsgType:     wxProtocolMsgVoice,
		VoiceTime:   2,
		ReferID:     json.RawMessage(`"1002966"`),
		SendTime:    now.Unix(),
	}, `{"id":"1003228","referid":"1002966","msg_type":6,"content_type":16,"voice_time":2}`)
	if err != nil {
		t.Fatalf("handleChatMessage() error = %v", err)
	}

	var messageCount int64
	if err := db.Model(&models.Message{}).Count(&messageCount).Error; err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("expected referenced mutation callback not to create a message, got %d", messageCount)
	}
	var log models.MessageSyncLog
	if err := db.Where("external_msg_id = ?", "wx_protocol:guid-refer:1003228").First(&log).Error; err != nil {
		t.Fatalf("expected sync log for skipped referenced mutation: %v", err)
	}
	if log.SyncStatus != enums.MessageSyncStatusSkipped || !strings.Contains(log.ErrorMessage, "referid=1002966") {
		t.Fatalf("unexpected sync log: %+v", log)
	}
}

func TestWxWorkProtocolSkipsInboundGroupMessageBeforeConversationCreation(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	instance := &models.WxWorkProtocolInstance{
		Guid:           "guid-group-message",
		EmployeeUserID: "employee-1",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	svc := &wxWorkProtocolService{}
	err := svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "1005538",
		Sender:      "7881301988023128",
		SenderName:  "香雪海",
		Receiver:    "employee-1",
		RoomID:      "10775325120961882",
		ContentType: 101,
		MsgType:     wxProtocolMsgImage,
		URL:         "https://example.com/group-image.jpg",
		SendTime:    now.Unix(),
	}, `{"id":"1005538","sender":"7881301988023128","sender_name":"香雪海","receiver":"employee-1","roomid":"10775325120961882","content_type":101,"msg_type":5}`)
	if err != nil {
		t.Fatalf("handleChatMessage() error = %v", err)
	}

	for name, model := range map[string]any{
		"customers":             &models.Customer{},
		"customer identities":   &models.CustomerIdentity{},
		"conversations":         &models.Conversation{},
		"messages":              &models.Message{},
		"conversation mappings": &models.WxWorkKFConversation{},
		"message refs":          &models.WxWorkKFMessageRef{},
		"outbox messages":       &models.ChannelMessageOutbox{},
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("expected group callback not to create %s, got %d", name, count)
		}
	}

	var syncLog models.MessageSyncLog
	if err := db.Where("external_msg_id = ?", "wx_protocol:guid-group-message:1005538").First(&syncLog).Error; err != nil {
		t.Fatalf("expected skipped group message sync log: %v", err)
	}
	if syncLog.SyncStatus != enums.MessageSyncStatusSkipped || !strings.Contains(syncLog.ErrorMessage, "room_id=10775325120961882") {
		t.Fatalf("unexpected group message sync log: %+v", syncLog)
	}
}

func TestWxWorkProtocolReceivesCustomerMessageBeforeKnowledgeIsConfigured(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	channel := &models.Channel{
		ID:          45,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-new-account",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		Guid:            "guid-new-account",
		ChannelID:       channel.ID,
		EmployeeUserID:  "employee-new",
		EmployeeName:    "新员工号",
		StoreID:         77,
		KnowledgeBaseID: 0,
		AIReplyEnabled:  true,
		Status:          enums.StatusOk,
		AuditFields:     models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	customer := &models.Customer{
		Name:        "新客户",
		Avatar:      "https://example.com/customer-avatar.jpg",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(customer).Error; err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if err := db.Create(&models.CustomerIdentity{
		CustomerID:     customer.ID,
		ExternalSource: enums.ExternalSourceWxWorkProtocol,
		ExternalID:     "wxwork_protocol:guid-new-account:external-new-customer",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create customer identity: %v", err)
	}

	previousHook := TriggerAIReplyAsyncHook
	TriggerAIReplyAsyncHook = nil
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousHook
	})

	svc := &wxWorkProtocolService{}
	err := svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "1009001",
		Sender:      "external-new-customer",
		SenderName:  "新客户",
		Receiver:    "employee-new",
		RoomID:      "0",
		ContentType: wxProtocolMsgText,
		MsgType:     wxProtocolMsgText,
		Content:     "你好",
		SendTime:    now.Unix(),
	}, `{"id":"1009001","sender":"external-new-customer","receiver":"employee-new","roomid":"0","content":"你好","msg_type":2}`)
	if err != nil {
		t.Fatalf("handleChatMessage() error = %v", err)
	}

	var conversation models.Conversation
	if err := db.Order("id DESC").First(&conversation).Error; err != nil {
		t.Fatalf("expected conversation: %v", err)
	}
	state := ConversationRouteService.GetByConversationID(conversation.ID)
	if state == nil {
		t.Fatalf("expected route state")
	}
	if state.WxWorkInstanceID != instance.ID || state.StoreID != instance.StoreID || state.KnowledgeBaseID != 0 {
		t.Fatalf("expected instance-scoped route before knowledge binding, got %+v", state)
	}
	if state.RouteStatus != enums.ConversationRouteStatusHQAgentDeskPending || !state.NeedHumanFollowUp {
		t.Fatalf("expected AI paused and dashboard attention enabled, got %+v", state)
	}

	var customerMessages int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeCustomer).Count(&customerMessages).Error; err != nil {
		t.Fatalf("count customer messages: %v", err)
	}
	if customerMessages != 1 {
		t.Fatalf("expected one received customer message, got %d", customerMessages)
	}
	var aiMessages int64
	if err := db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeAI).Count(&aiMessages).Error; err != nil {
		t.Fatalf("count AI messages: %v", err)
	}
	if aiMessages != 0 {
		t.Fatalf("expected no fake configuration reply to customer, got %d", aiMessages)
	}

	var syncLog models.MessageSyncLog
	externalMsgID := "wx_protocol:guid-new-account:1009001"
	if err := db.Where("external_msg_id = ? AND sync_status = ?", externalMsgID, enums.MessageSyncStatusSuccess).First(&syncLog).Error; err != nil {
		t.Fatalf("expected successful receive sync log: %v", err)
	}
}

func TestWxWorkProtocolReferencedRecallMarksOriginalMessageRecalled(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	channel := &models.Channel{
		ID:          32,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-recall-test",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("external-recall-user")
	conversation, err := ConversationService.Create(external, channel.ID, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		Guid:           "guid-recall",
		ChannelID:      channel.ID,
		EmployeeUserID: "employee-1",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	previousHook := TriggerAIReplyAsyncHook
	TriggerAIReplyAsyncHook = nil
	t.Cleanup(func() {
		TriggerAIReplyAsyncHook = previousHook
	})

	originalWxMsgID := "wx_protocol:guid-recall:1003262"
	message, err := MessageService.SendCustomerMessageWithRequestID(conversation.ID, originalWxMsgID, enums.IMMessageTypeText, "你好", "", external, "incoming-1003262")
	if err != nil {
		t.Fatalf("send original customer message: %v", err)
	}
	if err := db.Create(&models.WxWorkKFMessageRef{
		ConversationID: conversation.ID,
		MessageID:      message.ID,
		WxMsgID:        originalWxMsgID,
		Direction:      string(enums.WxWorkKFMessageDirectionIn),
		OpenKfID:       "wx_protocol:guid-recall:single",
		ExternalUserID: external.ExternalID,
		SendStatus:     string(enums.WxWorkKFMessageSendStatusReceived),
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create original message ref: %v", err)
	}
	var beforeCount int64
	if err := db.Model(&models.Message{}).Count(&beforeCount).Error; err != nil {
		t.Fatalf("count messages before recall: %v", err)
	}

	svc := &wxWorkProtocolService{}
	err = svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "1003267",
		Sender:      "external-recall-user",
		Receiver:    "employee-1",
		ContentType: wxProtocolMsgSystemAlt,
		MsgType:     wxProtocolMsgSystemAlt,
		Content:     "该消息已被撤回",
		ReferID:     json.RawMessage(`"1003262"`),
		SendTime:    now.Add(time.Second).Unix(),
	}, `{"id":"1003267","referid":"1003262","msg_type":1011,"content_type":1011,"content":"该消息已被撤回"}`)
	if err != nil {
		t.Fatalf("handle recall callback: %v", err)
	}

	updated := MessageService.Get(message.ID)
	if updated == nil || updated.SendStatus != enums.IMMessageStatusRecalled || updated.RecalledAt == nil {
		t.Fatalf("expected original message recalled, got %+v", updated)
	}
	ref := WxWorkKFMessageRefService.GetByWxMsgID(originalWxMsgID)
	if ref == nil || ref.SendStatus != string(enums.WxWorkKFMessageSendStatusRecalled) {
		t.Fatalf("expected original ref status recalled, got %+v", ref)
	}
	var afterCount int64
	if err := db.Model(&models.Message{}).Count(&afterCount).Error; err != nil {
		t.Fatalf("count messages after recall: %v", err)
	}
	if afterCount != beforeCount {
		t.Fatalf("expected recall callback not to create message, before=%d after=%d", beforeCount, afterCount)
	}
	var syncLog models.MessageSyncLog
	if err := db.Where("external_msg_id = ?", "wx_protocol:guid-recall:1003267").First(&syncLog).Error; err != nil {
		t.Fatalf("expected recall sync log: %v", err)
	}
	if syncLog.SyncStatus != enums.MessageSyncStatusSuccess || !strings.Contains(syncLog.ErrorMessage, "recall applied") {
		t.Fatalf("unexpected recall sync log: %+v", syncLog)
	}
}

func TestWxWorkProtocolEmployeeOutgoingEchoRepairsLegacyRef(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	channel := &models.Channel{
		ID:          31,
		Name:        "企微员工号",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "wxwork-protocol-test",
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	external := welcomeTestExternalUser("external-user-1")
	conversation, err := ConversationService.Create(external, channel.ID, aiAgent.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		Guid:           "guid-1",
		ChannelID:      channel.ID,
		EmployeeUserID: "employee-1",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := db.Create(&models.WxWorkKFConversation{
		ConversationID: conversation.ID,
		ChannelID:      channel.ID,
		OpenKfID:       "wx_protocol:guid-1:single",
		ExternalUserID: "external-user-1",
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create conversation mapping: %v", err)
	}
	wxMsgID := "wx_protocol:guid-1:wx-msg-1"
	if err := db.Create(&models.WxWorkKFMessageRef{
		ConversationID: conversation.ID,
		MessageID:      0,
		WxMsgID:        wxMsgID,
		Direction:      string(enums.WxWorkKFMessageDirectionOut),
		OpenKfID:       "wx_protocol:guid-1:single",
		ExternalUserID: "external-user-1",
		SendStatus:     string(enums.WxWorkKFMessageSendStatusSent),
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("create legacy ref: %v", err)
	}

	svc := &wxWorkProtocolService{}
	err = svc.handleChatMessage(instance, request.WxProtocolChatMsg{
		ID:          "wx-msg-1",
		Sender:      "employee-1",
		Receiver:    "external-user-1",
		RoomID:      "0",
		ContentType: 0,
		MsgType:     wxProtocolMsgText,
		Content:     "我在企微回复",
		SendTime:    now.Unix(),
	}, `{"id":"wx-msg-1","content":"我在企微回复"}`)
	if err != nil {
		t.Fatalf("handleChatMessage() error = %v", err)
	}

	ref := WxWorkKFMessageRefService.GetByWxMsgID(wxMsgID)
	if ref == nil || ref.MessageID <= 0 || ref.ConversationID != conversation.ID {
		t.Fatalf("expected legacy ref to be repaired with local message id, got %+v", ref)
	}
	message := MessageService.Get(ref.MessageID)
	if message == nil || message.SenderType != enums.IMSenderTypeAgent || message.Content != "我在企微回复" {
		t.Fatalf("expected repaired local agent message, got %+v", message)
	}
	var outboxCount int64
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("message_id = ?", ref.MessageID).Count(&outboxCount).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("expected repaired echo to avoid outbound outbox, got %d", outboxCount)
	}
}

func TestPrepareOutboundMiniProgramMediaKeepsExistingCoverCredentials(t *testing.T) {
	svc := &wxWorkProtocolService{}
	message := &models.Message{Payload: `{"username":"gh_7370f8f46fc0@app","file_id":"cover-file-id","aes_key":"cover-aes-key","md5":"cover-md5","size":20810,"appicon":"http://example.com/icon.png"}`}
	if err := svc.prepareOutboundMiniProgramMedia(nil, &models.WxWorkProtocolInstance{Guid: "guid-1"}, message); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(message.Payload, "cover-file-id") {
		t.Fatalf("expected payload to keep original cover credentials, got %s", message.Payload)
	}
}

func TestPrepareOutboundMiniProgramMediaRequiresMiniProgramUsername(t *testing.T) {
	svc := &wxWorkProtocolService{}
	message := &models.Message{Payload: `{"conversation_id":"S:7881302995969629","file_id":"cover-file-id","aes_key":"cover-aes-key","md5":"cover-md5","size":20810}`}
	err := svc.prepareOutboundMiniProgramMedia(nil, &models.WxWorkProtocolInstance{Guid: "guid-1"}, message)
	if err == nil || !strings.Contains(err.Error(), "username") {
		t.Fatalf("expected username validation error, got %v", err)
	}
}

func TestWxWorkProtocolMiniProgramMessageIsStructuredCard(t *testing.T) {
	msg := request.WxProtocolChatMsg{
		MsgID:       "1001564",
		MsgType:     wxProtocolMsgWeApp,
		ContentType: 78,
		Username:    "gh_7370f8f46fc0@app",
		AppID:       "wx37bef9195b47f085",
		AppName:     "自由家安心宿",
		AppIcon:     "http://mmbiz.qpic.cn/sz_mmbiz_png/example/640?wx_fmt=png",
		Title:       "e秒安心住",
		PagePath:    "pages/home/home.html",
		ThumbWidth:  360,
		ThumbHeight: 288,
		CDN: request.WxProtocolMediaPayload{
			FileID: "306c0201020465",
			AesKey: "6676686A7463676E75797576797A776E",
			MD5:    "c9e083a08b8f6ee8fd36072e138b29cb",
			Size:   20810,
		},
	}
	msg.Normalize()

	if got := msg.InferMsgType(); got != wxProtocolMsgWeApp {
		t.Fatalf("expected inferred mini program msg_type=%d, got %d", wxProtocolMsgWeApp, got)
	}

	svc := &wxWorkProtocolService{}
	if got := svc.resolveInboundMessageType(msg); got != enums.IMMessageTypeMiniProgram {
		t.Fatalf("expected mini program message type, got %s", got)
	}
	content, payload, err := svc.buildInboundMessageContent(nil, enums.IMMessageTypeMiniProgram, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != msg.Title {
		t.Fatalf("expected mini program title content, got %q", content)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatalf("invalid payload json: %v", err)
	}
	for key, want := range map[string]string{
		"appid":     msg.AppID,
		"appname":   msg.AppName,
		"appicon":   msg.AppIcon,
		"title":     msg.Title,
		"page_path": msg.PagePath,
		"username":  msg.Username,
	} {
		if got := body[key]; got != want {
			t.Fatalf("expected payload %s=%q, got %#v in %#v", key, want, got, body)
		}
	}
	if got := body["msg_type"]; got != float64(wxProtocolMsgWeApp) {
		t.Fatalf("expected payload msg_type=%d, got %#v", wxProtocolMsgWeApp, got)
	}
}

func TestNormalizeStoreRoomAtList(t *testing.T) {
	got := normalizeStoreRoomAtList([]string{" staff-1 ", "", "staff-2", "staff-1", "0"})
	if len(got) != 2 || got[0] != "staff-1" || got[1] != "staff-2" {
		t.Fatalf("unexpected normalized at list: %#v", got)
	}
}

func TestWxWorkProtocolPreClaimFailureCannotOverwriteNewerAttempt(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	now := time.Now()
	outbox := &models.ChannelMessageOutbox{
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		MessageID:   401,
		SendStatus:  string(enums.ChannelMessageOutboxStatusPending),
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create outbox: %v", err)
	}
	stale := *outbox
	newerRetryAt := now.Add(3 * time.Minute)
	if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{
		"send_status":   string(enums.ChannelMessageOutboxStatusFailed),
		"retry_count":   1,
		"next_retry_at": newerRetryAt,
		"last_error":    "newer protocol failure",
	}).Error; err != nil {
		t.Fatalf("advance protocol attempt: %v", err)
	}

	if err := WxWorkProtocolService.markOutboxFailed(stale, "stale protocol failure"); err != nil {
		t.Fatalf("markOutboxFailed() error = %v", err)
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusFailed) || reloaded.RetryCount != 1 || reloaded.LastError != "newer protocol failure" {
		t.Fatalf("stale protocol failure overwrote newer state: %+v", reloaded)
	}
}

func TestWxWorkProtocolPostJSONMarksPostCallFailureUncertain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream failed after accepting request", http.StatusInternalServerError)
	}))
	defer server.Close()

	service := newWxWorkProtocolService()
	service.httpClient = server.Client()
	_, err := service.postJSON(&dto.WxWorkProtocolChannelConfig{BaseURL: server.URL}, "/msg/send_text", map[string]any{"content": "测试"})
	if err == nil || !isExternalDispatchResultUncertain(err) {
		t.Fatalf("post-call protocol failure must be classified as delivery-uncertain: %v", err)
	}
}

func TestWxWorkProtocolPostJSONSeparatesKnownFailuresFromUncertainDelivery(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		uncertain  bool
	}{
		{name: "bad request", statusCode: http.StatusBadRequest, body: `{"success":false,"message":"invalid conversation_id"}`},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, body: `{"success":false,"message":"rate limited"}`},
		{name: "business error", statusCode: http.StatusOK, body: `{"error_code":40058,"error_message":"invalid content"}`},
		{name: "server failure", statusCode: http.StatusBadGateway, body: `{"success":false,"message":"upstream failed"}`, uncertain: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			service := newWxWorkProtocolService()
			service.httpClient = server.Client()
			_, err := service.postJSON(&dto.WxWorkProtocolChannelConfig{BaseURL: server.URL}, "/msg/send_text", map[string]any{"content": "测试"})
			if err == nil {
				t.Fatal("expected protocol failure")
			}
			if got := isExternalDispatchResultUncertain(err); got != test.uncertain {
				t.Fatalf("uncertain=%v want %v: %v", got, test.uncertain, err)
			}
		})
	}
}

func TestWxWorkProtocolClaimedDispatchErrorSeparatesSafeRetryAndUncertainDelivery(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus enums.ChannelMessageOutboxStatus
		uncertain  bool
	}{
		{name: "pre-call failure", err: errors.New("payload validation failed"), wantStatus: enums.ChannelMessageOutboxStatusFailed},
		{name: "post-call failure", err: markExternalDispatchResultUncertain(errors.New("request timeout")), wantStatus: enums.ChannelMessageOutboxStatusCancelled, uncertain: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupMessageWelcomeTestDB(t)
			now := time.Now()
			outbox := &models.ChannelMessageOutbox{
				ChannelType: enums.ChannelTypeWxWorkProtocol,
				MessageID:   402,
				SendStatus:  string(enums.ChannelMessageOutboxStatusSending),
				AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
			}
			if err := db.Create(outbox).Error; err != nil {
				t.Fatalf("create claimed outbox: %v", err)
			}
			if err := WxWorkProtocolService.handleClaimedOutboxDispatchError(*outbox, tt.err); err != nil {
				t.Fatalf("handleClaimedOutboxDispatchError() error = %v", err)
			}
			reloaded := ChannelMessageOutboxService.Get(outbox.ID)
			if reloaded == nil || reloaded.SendStatus != string(tt.wantStatus) {
				t.Fatalf("claimed error status=%+v want %s", reloaded, tt.wantStatus)
			}
			if tt.uncertain {
				if reloaded.NextRetryAt != nil || !strings.HasPrefix(reloaded.LastError, channelMessageOutboxDispatchUncertainReasonPrefix) {
					t.Fatalf("uncertain delivery must be terminal and non-replayable: %+v", reloaded)
				}
			} else if reloaded.NextRetryAt == nil {
				t.Fatalf("safe pre-call failure must remain retryable: %+v", reloaded)
			}
		})
	}
}

func TestWxWorkProtocolStoreRoomLateResultCannotOverwriteCancellation(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "late success", statusCode: http.StatusOK, body: `{"success":true}`},
		{name: "late failure", statusCode: http.StatusInternalServerError, body: `{"success":false,"message":"send failed"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupMessageWelcomeTestDB(t)
			var outboxID int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if err := db.Model(&models.ChannelMessageOutbox{}).Where("id = ?", outboxID).Updates(map[string]any{
					"send_status":   string(enums.ChannelMessageOutboxStatusCancelled),
					"next_retry_at": nil,
					"last_error":    "cancelled while external send was in flight",
				}).Error; err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			now := time.Now()
			configJSON, err := json.Marshal(map[string]any{
				"baseUrl":   server.URL,
				"appKey":    "test-key",
				"appSecret": "test-secret",
			})
			if err != nil {
				t.Fatalf("marshal channel config: %v", err)
			}
			channel := &models.Channel{
				Name:        "门店群投递竞态",
				ChannelType: enums.ChannelTypeWxWorkProtocol,
				ChannelID:   "wxwork-store-room-race-" + strings.ReplaceAll(tt.name, " ", "-"),
				ConfigJSON:  string(configJSON),
				Status:      enums.StatusOk,
				AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
			}
			if err := db.Create(channel).Error; err != nil {
				t.Fatalf("create channel: %v", err)
			}
			instance := &models.WxWorkProtocolInstance{
				Guid:        "store-room-race-guid-" + strings.ReplaceAll(tt.name, " ", "-"),
				ChannelID:   channel.ID,
				Status:      enums.StatusOk,
				AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
			}
			if err := db.Create(instance).Error; err != nil {
				t.Fatalf("create instance: %v", err)
			}
			payload, err := json.Marshal(map[string]any{
				"kind":               "store_room_handoff_notice",
				"conversationId":     int64(501),
				"wxWorkInstanceId":   instance.ID,
				"roomConversationId": "R:test-room",
				"content":            "测试门店群提醒",
			})
			if err != nil {
				t.Fatalf("marshal outbox payload: %v", err)
			}
			outbox := &models.ChannelMessageOutbox{
				ChannelType:    enums.ChannelTypeWxWorkProtocol,
				ConversationID: 501,
				MessageID:      -501,
				Payload:        string(payload),
				SendStatus:     string(enums.ChannelMessageOutboxStatusPending),
				AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
			}
			if err := db.Create(outbox).Error; err != nil {
				t.Fatalf("create store-room outbox: %v", err)
			}
			outboxID = outbox.ID

			svc := newWxWorkProtocolService()
			if err := svc.dispatchStoreRoomNoticeOutbox(*outbox); err != nil {
				t.Fatalf("dispatchStoreRoomNoticeOutbox() error = %v", err)
			}
			reloaded := ChannelMessageOutboxService.Get(outbox.ID)
			if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusCancelled) || reloaded.SentAt != nil || reloaded.RetryCount != 0 || reloaded.LastError != "cancelled while external send was in flight" {
				t.Fatalf("late store-room result overwrote cancellation: %+v", reloaded)
			}
			var syncLogCount int64
			if err := db.Model(&models.MessageSyncLog{}).Count(&syncLogCount).Error; err != nil {
				t.Fatalf("count sync logs: %v", err)
			}
			if syncLogCount != 0 {
				t.Fatalf("late result created %d success sync logs", syncLogCount)
			}
		})
	}
}
