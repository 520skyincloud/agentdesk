package services

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
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
