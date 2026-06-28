package services

import (
	"encoding/json"
	"strings"
	"testing"

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
