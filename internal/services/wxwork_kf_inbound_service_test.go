package services

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/silenceper/wechat/v2/work/kf/syncmsg"
	"gorm.io/gorm"
)

func prepareWxWorkKFInboundTestChannel(t *testing.T, db *gorm.DB, openKfID string) (*models.AIAgent, *models.Channel) {
	t.Helper()
	aiAgent := createWelcomeTestAIAgent(t, db, "")
	now := time.Now()
	channel := &models.Channel{
		Name:        "wxwork kf inbound test",
		ChannelType: enums.ChannelTypeWxWorkKF,
		ChannelID:   "wxwork-kf-inbound-test",
		AIAgentID:   aiAgent.ID,
		ConfigJSON:  fmt.Sprintf(`{"openKfId":%q}`, openKfID),
		Status:      enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create wxwork kf channel: %v", err)
	}
	return aiAgent, channel
}

func buildWxWorkKFInboundTextItem(t *testing.T, msgID, openKfID, externalUserID, content string) syncmsg.Message {
	t.Helper()
	payload := syncmsg.Text{
		BaseMessage: syncmsg.BaseMessage{
			MsgID:          msgID,
			OpenKFID:       openKfID,
			ExternalUserID: externalUserID,
			SendTime:       uint64(time.Now().Unix()),
			Origin:         3,
		},
		MsgType: "text",
	}
	payload.Text.Content = content
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal wxwork kf text item: %v", err)
	}
	return syncmsg.Message{
		MsgID:          msgID,
		OpenKFID:       openKfID,
		ExternalUserID: externalUserID,
		SendTime:       payload.SendTime,
		Origin:         payload.Origin,
		MsgType:        payload.MsgType,
		OriginData:     raw,
	}
}

func buildWxWorkKFSendFailItem(t *testing.T, eventMsgID, failMsgID, openKfID, externalUserID string) syncmsg.Message {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"msgid":     eventMsgID,
		"send_time": uint64(time.Now().Unix()),
		"origin":    4,
		"msgtype":   "event",
		"event": map[string]any{
			"event_type":      enums.WxWorkKFEventTypeMsgSendFail,
			"open_kfid":       openKfID,
			"external_userid": externalUserID,
			"fail_msgid":      failMsgID,
			"fail_type":       10,
		},
	})
	if err != nil {
		t.Fatalf("marshal wxwork kf send failure item: %v", err)
	}
	return syncmsg.Message{
		MsgID:          eventMsgID,
		OpenKFID:       openKfID,
		ExternalUserID: externalUserID,
		Origin:         4,
		MsgType:        "event",
		EventType:      enums.WxWorkKFEventTypeMsgSendFail,
		OriginData:     raw,
	}
}

func buildWxWorkKFInboundEventItem(t *testing.T, msgID, eventType, openKfID, externalUserID string, eventFields map[string]any) syncmsg.Message {
	t.Helper()
	event := map[string]any{
		"event_type":      eventType,
		"open_kfid":       openKfID,
		"external_userid": externalUserID,
	}
	for key, value := range eventFields {
		event[key] = value
	}
	sendTime := uint64(time.Now().Unix())
	raw, err := json.Marshal(map[string]any{
		"msgid":           msgID,
		"open_kfid":       openKfID,
		"external_userid": externalUserID,
		"send_time":       sendTime,
		"origin":          4,
		"msgtype":         "event",
		"event":           event,
	})
	if err != nil {
		t.Fatalf("marshal wxwork kf event item: %v", err)
	}
	return syncmsg.Message{
		MsgID:          msgID,
		OpenKFID:       openKfID,
		ExternalUserID: externalUserID,
		SendTime:       sendTime,
		Origin:         4,
		MsgType:        "event",
		EventType:      eventType,
		OriginData:     raw,
	}
}

func TestWxWorkKFInboundEventRefAndLogAreAtomicAndIdempotent(t *testing.T) {
	testCases := []struct {
		name        string
		eventType   string
		eventFields map[string]any
		wantContent string
	}{
		{
			name:      "enter_session",
			eventType: enums.WxWorkKFEventTypeEnterSession,
			eventFields: map[string]any{
				"scene":        "atomic-test",
				"scene_param":  "entry",
				"welcome_code": "welcome-code",
			},
			wantContent: "微信客户进入会话",
		},
		{
			name:      "session_status_change",
			eventType: enums.WxWorkKFEventTypeSessionStatusChange,
			eventFields: map[string]any{
				"change_type":         1,
				"new_servicer_userid": "servicer-atomic-test",
				"msg_code":            "msg-code",
			},
			wantContent: "微信会话状态变更",
		},
		{
			name:      "msg_send_fail",
			eventType: enums.WxWorkKFEventTypeMsgSendFail,
			eventFields: map[string]any{
				"fail_msgid": "failed-outbound-atomic-test",
				"fail_type":  10,
			},
			wantContent: "微信消息发送失败事件",
		},
		{
			name:        "orphan_event",
			eventType:   "future_event_type",
			eventFields: map[string]any{"future_field": "value"},
			wantContent: "收到未处理的企业微信事件",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := setupMessageWelcomeTestDB(t)
			openKfID := "open-kf-event-" + testCase.name
			externalUserID := "external-event-" + testCase.name
			msgID := "wx-event-atomic-" + testCase.name
			prepareWxWorkKFInboundTestChannel(t, db, openKfID)
			service := newWxWorkKFInboundService()
			item := buildWxWorkKFInboundEventItem(t, msgID, testCase.eventType, openKfID, externalUserID, testCase.eventFields)

			if err := db.Exec(fmt.Sprintf(`
				CREATE TRIGGER fail_event_log_insert
				BEFORE INSERT ON t_conversation_event_log
				WHEN NEW.request_id = '%s'
				BEGIN
					SELECT RAISE(FAIL, 'forced event log failure');
				END
			`, msgID)).Error; err != nil {
				t.Fatalf("create event log failure trigger: %v", err)
			}

			if err := service.consumeSyncMessage(item); err == nil {
				t.Fatal("event consumption must return the forced event log failure")
			}
			if ref := WxWorkKFMessageRefService.GetByWxMsgID(msgID); ref != nil {
				t.Fatalf("event ref must roll back with the failed event log: %+v", ref)
			}
			var failedLogCount int64
			if err := db.Model(&models.ConversationEventLog{}).Where("request_id = ?", msgID).Count(&failedLogCount).Error; err != nil {
				t.Fatalf("count rolled-back event logs: %v", err)
			}
			if failedLogCount != 0 {
				t.Fatalf("failed event transaction left logs behind: count=%d", failedLogCount)
			}

			if err := db.Exec("DROP TRIGGER fail_event_log_insert").Error; err != nil {
				t.Fatalf("drop event log failure trigger: %v", err)
			}
			if err := service.consumeSyncMessage(item); err != nil {
				t.Fatalf("replay event after rollback: %v", err)
			}
			if err := service.consumeSyncMessage(item); err != nil {
				t.Fatalf("repeat successfully consumed event: %v", err)
			}

			var refCount int64
			if err := db.Model(&models.WxWorkKFMessageRef{}).Where("wx_msg_id = ?", msgID).Count(&refCount).Error; err != nil {
				t.Fatalf("count replayed event refs: %v", err)
			}
			if refCount != 1 {
				t.Fatalf("event replay must persist exactly one ref: count=%d", refCount)
			}
			var logs []models.ConversationEventLog
			if err := db.Where("request_id = ?", msgID).Find(&logs).Error; err != nil {
				t.Fatalf("find replayed event logs: %v", err)
			}
			if len(logs) != 1 {
				t.Fatalf("event replay must persist exactly one log: count=%d", len(logs))
			}
			if logs[0].Content != testCase.wantContent {
				t.Fatalf("unexpected event log content: got %q want %q", logs[0].Content, testCase.wantContent)
			}
		})
	}
}

func TestWxWorkKFSyncPageFailureDoesNotAdvanceCursorAndRetryIsIdempotent(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	const (
		openKfID       = "open-kf-inbound-page"
		externalUserID = "external-inbound-page"
	)
	prepareWxWorkKFInboundTestChannel(t, db, openKfID)
	service := newWxWorkKFInboundService()
	items := []syncmsg.Message{
		buildWxWorkKFInboundTextItem(t, "wx-inbound-page-1", openKfID, externalUserID, "第一条"),
		buildWxWorkKFInboundTextItem(t, "wx-inbound-page-2", openKfID, externalUserID, "第二条"),
	}
	if err := db.Exec(`
		CREATE TRIGGER fail_second_inbound_ref
		BEFORE INSERT ON t_wx_work_kf_message_ref
		WHEN NEW.wx_msg_id = 'wx-inbound-page-2'
		BEGIN
			SELECT RAISE(FAIL, 'forced inbound ref failure');
		END
	`).Error; err != nil {
		t.Fatalf("create inbound ref failure trigger: %v", err)
	}

	saveCount := 0
	err := consumeWxWorkKFSyncPage(items, service.consumeSyncMessage, func() error {
		saveCount++
		return nil
	})
	if err == nil {
		t.Fatal("page consumption must return the second item failure")
	}
	if saveCount != 0 {
		t.Fatalf("cursor was advanced after a page item failed: saves=%d", saveCount)
	}
	if WxWorkKFMessageRefService.GetByWxMsgID(items[0].MsgID) == nil {
		t.Fatal("the successful item must remain durably consumed")
	}
	if WxWorkKFMessageRefService.GetByWxMsgID(items[1].MsgID) != nil {
		t.Fatal("the failed item must not receive a deduplication ref")
	}

	if err := db.Exec("DROP TRIGGER fail_second_inbound_ref").Error; err != nil {
		t.Fatalf("drop inbound ref failure trigger: %v", err)
	}
	if err := consumeWxWorkKFSyncPage(items, service.consumeSyncMessage, func() error {
		saveCount++
		return nil
	}); err != nil {
		t.Fatalf("retry page consumption: %v", err)
	}
	if saveCount != 1 {
		t.Fatalf("cursor must advance exactly once after the whole page succeeds: saves=%d", saveCount)
	}
	if WxWorkKFMessageRefService.GetByWxMsgID(items[1].MsgID) == nil {
		t.Fatal("the failed item was not replayed successfully")
	}
	var messageCount int64
	clientMsgIDs := []string{
		service.buildInboundClientMsgID(items[0].MsgID),
		service.buildInboundClientMsgID(items[1].MsgID),
	}
	if err := db.Model(&models.Message{}).Where("client_msg_id IN ?", clientMsgIDs).Count(&messageCount).Error; err != nil {
		t.Fatalf("count replayed inbound messages: %v", err)
	}
	if messageCount != 2 {
		t.Fatalf("retry duplicated a message instead of using wx_msg_id/client_msg_id idempotency: count=%d", messageCount)
	}
}

func TestWxWorkKFSendFailurePlaceholderErrorDoesNotWriteEventDedupRef(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	const openKfID = "open-kf-placeholder-error"
	prepareWxWorkKFInboundTestChannel(t, db, openKfID)
	service := newWxWorkKFInboundService()
	item := buildWxWorkKFSendFailItem(t, "wx-event-placeholder-error", "wx-target-placeholder-error", openKfID, "external-placeholder-error")
	if err := db.Exec(`
		CREATE TRIGGER fail_outbound_placeholder_ref
		BEFORE INSERT ON t_wx_work_kf_message_ref
		WHEN NEW.direction = 'out'
		BEGIN
			SELECT RAISE(FAIL, 'forced outbound placeholder failure');
		END
	`).Error; err != nil {
		t.Fatalf("create placeholder failure trigger: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := service.consumeSyncMessage(item); err == nil {
			t.Fatalf("attempt %d must return the placeholder persistence error", attempt)
		}
		if WxWorkKFMessageRefService.GetByWxMsgID(item.MsgID) != nil {
			t.Fatalf("attempt %d wrote the event deduplication ref despite the failure", attempt)
		}
	}
}

func TestWxWorkKFSendFailureOutboxUpdateErrorDoesNotWriteEventDedupRef(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	const (
		openKfID       = "open-kf-outbox-error"
		externalUserID = "external-outbox-error"
		failMsgID      = "wx-target-outbox-error"
	)
	aiAgent, _ := prepareWxWorkKFInboundTestChannel(t, db, openKfID)
	service := newWxWorkKFInboundService()
	conversation, err := service.ensureConversation(syncmsg.BaseMessage{
		MsgID:          "wx-seed-outbox-error",
		OpenKFID:       openKfID,
		ExternalUserID: externalUserID,
		SendTime:       uint64(time.Now().Unix()),
		Origin:         3,
	}, nil)
	if err != nil {
		t.Fatalf("create wxwork kf conversation: %v", err)
	}
	now := time.Now()
	message := &models.Message{
		ConversationID: conversation.ID,
		SessionNo:      1,
		RequestID:      "wxwork-kf-outbox-error",
		ClientMsgID:    "wxwork-kf-outbox-error",
		SenderType:     enums.IMSenderTypeAI,
		SenderID:       aiAgent.ID,
		MessageType:    enums.IMMessageTypeText,
		Content:        "待确认送达",
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create outbound message: %v", err)
	}
	outbox := &models.ChannelMessageOutbox{
		ChannelType:    enums.ChannelTypeWxWorkKF,
		ConversationID: conversation.ID,
		MessageID:      message.ID,
		SendStatus:     string(enums.ChannelMessageOutboxStatusSent),
		SentAt:         &now,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(outbox).Error; err != nil {
		t.Fatalf("create sent outbox: %v", err)
	}
	failedRef := &models.WxWorkKFMessageRef{
		ConversationID: conversation.ID,
		MessageID:      message.ID,
		WxMsgID:        failMsgID,
		Direction:      string(enums.WxWorkKFMessageDirectionOut),
		OpenKfID:       openKfID,
		ExternalUserID: externalUserID,
		SendStatus:     string(enums.WxWorkKFMessageSendStatusSent),
		RawPayload:     `{"dispatchAttempt":0,"chunkIndex":0}`,
		Status:         enums.StatusOk,
		AuditFields:    models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(failedRef).Error; err != nil {
		t.Fatalf("create outbound message ref: %v", err)
	}
	if err := db.Exec(fmt.Sprintf(`
		CREATE TRIGGER fail_outbox_callback_update
		BEFORE UPDATE ON t_channel_message_outbox
		WHEN OLD.id = %d
		BEGIN
			SELECT RAISE(FAIL, 'forced outbox callback update failure');
		END
	`, outbox.ID)).Error; err != nil {
		t.Fatalf("create outbox update failure trigger: %v", err)
	}
	item := buildWxWorkKFSendFailItem(t, "wx-event-outbox-error", failMsgID, openKfID, externalUserID)

	for attempt := 1; attempt <= 2; attempt++ {
		if err := service.consumeSyncMessage(item); err == nil {
			t.Fatalf("attempt %d must return the outbox update error", attempt)
		}
		if WxWorkKFMessageRefService.GetByWxMsgID(item.MsgID) != nil {
			t.Fatalf("attempt %d wrote the event deduplication ref despite the outbox failure", attempt)
		}
	}
	reloaded := ChannelMessageOutboxService.Get(outbox.ID)
	if reloaded == nil || reloaded.SendStatus != string(enums.ChannelMessageOutboxStatusSent) {
		t.Fatalf("failed outbox transaction changed the durable status: %+v", reloaded)
	}
}
