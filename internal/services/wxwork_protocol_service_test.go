package services

import (
	"encoding/json"
	"testing"

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
