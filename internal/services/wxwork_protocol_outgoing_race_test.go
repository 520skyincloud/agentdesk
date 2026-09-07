package services

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
)

type outgoingRaceAdapter struct {
	send func() (string, error)
}

func (a outgoingRaceAdapter) SendMessage(_ *dto.WxWorkProtocolChannelConfig, _ *models.WxWorkProtocolInstance, _ string, _ *models.Message) (string, error) {
	return a.send()
}

func (a outgoingRaceAdapter) CallDocumented(_ *dto.WxWorkProtocolChannelConfig, _ string, _ map[string]any) (string, error) {
	return `{"error_code":0}`, nil
}

func TestWxProtocolSuccessfulSendSurvivesCancellationAndEarlyEcho(t *testing.T) {
	db := setupMessageWelcomeTestDB(t)
	channel := models.Channel{ChannelID: "echo-race", ChannelType: enums.ChannelTypeWxWorkProtocol, Status: enums.StatusOk}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	instance := models.WxWorkProtocolInstance{Guid: "echo-race-guid", ChannelID: channel.ID, EmployeeUserID: "employee", Status: enums.StatusOk}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	conversation := models.Conversation{ChannelID: channel.ID, Status: enums.IMConversationStatusAIServing, ServiceMode: enums.IMConversationServiceModeAIFirst}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	state := models.ConversationRouteState{ConversationID: conversation.ID, WxWorkInstanceID: instance.ID, RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai"}
	mapping := models.WxWorkKFConversation{ConversationID: conversation.ID, ChannelID: channel.ID, OpenKfID: "wx_protocol:" + instance.Guid, ExternalUserID: "customer", Status: enums.StatusOk}
	message := models.Message{ConversationID: conversation.ID, SeqNo: 1, SenderType: enums.IMSenderTypeAI, ClientMsgID: "ai_reply_race", MessageType: enums.IMMessageTypeText, Content: "毛巾可自行取用。"}
	for _, value := range []any{&state, &mapping, &message} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	box := models.ChannelMessageOutbox{ConversationID: conversation.ID, MessageID: message.ID, ChannelType: enums.ChannelTypeWxWorkProtocol, SendStatus: "pending", Payload: `{}`}
	if err := db.Create(&box).Error; err != nil {
		t.Fatal(err)
	}
	svc := &wxWorkProtocolService{}
	echoDone := make(chan error, 1)
	svc.adapter = outgoingRaceAdapter{send: func() (string, error) {
		// Reproduce route cancellation while the network send is already in flight.
		if _, err := ConversationRouteService.EnterStoreWecomManual(conversation.ID, "deferred handoff", time.Now()); err != nil {
			return "", err
		}
		go func() {
			echoDone <- svc.handleChatMessage(&instance, request.WxProtocolChatMsg{
				ID: "5001", Sender: "employee", Receiver: "customer", RoomID: "0",
				ContentType: 2, MsgType: 2, Content: message.Content, SendTime: time.Now().Unix(),
			}, `{"id":"5001","content":"毛巾可自行取用。"}`)
		}()
		select {
		case err := <-echoDone:
			return "", fmt.Errorf("early echo must wait for send ID, returned %v", err)
		case <-time.After(20 * time.Millisecond):
		}
		return `{"error_code":0,"data":{"msg_data":{"id":"5001"}}}`, nil
	}}
	if err := svc.dispatchOutbox(box); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-echoDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("echo did not resume after send receipt")
	}
	if err := db.First(&box, box.ID).Error; err != nil {
		t.Fatal(err)
	}
	if box.SendStatus != "sent" || box.SentAt == nil {
		t.Fatalf("successful delivery must not remain cancelled: %+v", box)
	}
	ref := WxWorkKFMessageRefService.GetByWxMsgID("wx_protocol:echo-race-guid:5001")
	if ref == nil || ref.MessageID != message.ID {
		t.Fatalf("successful send lost exact external ID association: %+v", ref)
	}
	var count int64
	db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeAgent).Count(&count)
	if count != 0 {
		t.Fatalf("AI echo was counted as employee speech: %d", count)
	}
	// Identical text from a different external message ID is genuine employee speech.
	manual := request.WxProtocolChatMsg{ID: "5002", Sender: "employee", Receiver: "customer", ContentType: 2, MsgType: 2, Content: message.Content, SendTime: time.Now().Unix()}
	if err := svc.handleChatMessage(&instance, manual, `{"id":"5002"}`); err != nil {
		t.Fatal(err)
	}
	if err := svc.handleChatMessage(&instance, manual, `{"id":"5002"}`); err != nil {
		t.Fatal(err)
	}
	db.Model(&models.Message{}).Where("conversation_id = ? AND sender_type = ?", conversation.ID, enums.IMSenderTypeAgent).Count(&count)
	if count != 1 {
		t.Fatalf("real employee must be recorded exactly once: %d", count)
	}
	if route := ConversationRouteService.GetByConversationID(conversation.ID); route == nil || route.RouteStatus != enums.ConversationRouteStatusStoreWecomManual {
		t.Fatalf("recording delivery must not restore AI over employee: %+v", route)
	}
}
