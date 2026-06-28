package request

import (
	"encoding/json"
	"strings"
)

type WxWorkProtocolCallbackRequest struct {
	Guid       string          `json:"guid"`
	NotifyType int             `json:"notify_type"`
	Data       json.RawMessage `json:"data"`
}

type WxProtocolChatMsg struct {
	FromUsername   string                 `json:"from_username"`
	ToUsername     string                 `json:"to_username"`
	ChatroomSender string                 `json:"chatroom_sender"`
	CreateTime     int64                  `json:"create_time"`
	Desc           string                 `json:"desc"`
	MsgID          string                 `json:"msg_id"`
	MsgType        int                    `json:"msg_type"`
	ContentType    int                    `json:"content_type"`
	Chatroom       string                 `json:"chatroom"`
	Source         string                 `json:"source"`
	Content        string                 `json:"content"`
	Sender         string                 `json:"sender"`
	Receiver       string                 `json:"receiver"`
	RoomID         string                 `json:"roomid"`
	SendTime       int64                  `json:"sendtime"`
	ID             string                 `json:"id"`
	SenderName     string                 `json:"sender_name"`
	FileName       string                 `json:"file_name"`
	VoiceTime      int64                  `json:"voice_time"`
	URL            string                 `json:"url"`
	Type           int                    `json:"type"`
	SourceType     int                    `json:"source_type"`
	Width          int                    `json:"width"`
	Height         int                    `json:"height"`
	CDN            WxProtocolMediaPayload `json:"cdn"`
}

func (m *WxProtocolChatMsg) Normalize() {
	if m.FromUsername == "" {
		m.FromUsername = m.Sender
	}
	if m.ToUsername == "" {
		m.ToUsername = m.Receiver
	}
	if m.MsgID == "" {
		m.MsgID = m.ID
	}
	if m.CreateTime == 0 {
		m.CreateTime = m.SendTime
	}
	if m.Chatroom == "" && m.RoomID != "" && m.RoomID != "0" {
		m.Chatroom = m.RoomID
	}
	if m.Desc == "" {
		m.Desc = m.SenderName
	}
	m.normalizeMediaSize()
	inferredMsgType := m.InferMsgType()
	if inferredMsgType != 0 && (m.MsgType == 0 || m.shouldPreferInferredMsgType(inferredMsgType)) {
		m.MsgType = inferredMsgType
	}
}

func (m *WxProtocolChatMsg) shouldPreferInferredMsgType(inferredMsgType int) bool {
	switch inferredMsgType {
	case 2, 3, 4, 5, 6, 7, 8, 10, 11, 12, 13, 14, 16:
		return true
	default:
		return false
	}
}

func (m *WxProtocolChatMsg) normalizeMediaSize() {
	if m.CDN.FileSize <= 0 && m.CDN.Size > 0 {
		m.CDN.FileSize = m.CDN.Size
	}
	if m.CDN.Size <= 0 && m.CDN.FileSize > 0 {
		m.CDN.Size = m.CDN.FileSize
	}
}
func (m *WxProtocolChatMsg) InferMsgType() int {
	if m.ContentType == 2 && m.Content != "" {
		return 2
	}
	if m.VoiceTime > 0 {
		return 6
	}
	if m.ContentType == 43 || strings.HasPrefix(strings.ToLower(strings.TrimSpace(m.CDN.MimeType)), "video/") {
		return 7
	}
	if m.ContentType == 48 {
		return 3
	}
	if m.ContentType == 104 || m.SourceType == 101 {
		return 10
	}
	if m.CDN.ImageWidth > 0 || m.CDN.ImageHeight > 0 || m.ContentType == 101 {
		return 5
	}
	if m.CDN.FileID != "" || m.CDN.Size > 0 || m.CDN.FileSize > 0 || m.FileName != "" {
		return 8
	}
	return m.ContentType
}

type WxWorkProtocolSendTextResponse struct {
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	Data         struct {
		MsgData WxProtocolSendMsgData `json:"msg_data"`
	} `json:"data"`
}

type WxProtocolSendMsgData struct {
	ID             string `json:"id"`
	Seq            string `json:"seq"`
	MsgID          string `json:"msg_id"`
	ConversationID string `json:"conversation_id"`
	Content        string `json:"content"`
}

type WxProtocolMediaPayload struct {
	URL         string `json:"url,omitempty"`
	FileID      string `json:"file_id,omitempty"`
	AesKey      string `json:"aes_key,omitempty"`
	Size        int64  `json:"size,omitempty"`
	FileSize    int64  `json:"file_size,omitempty"`
	ImageWidth  int    `json:"image_width,omitempty"`
	ImageHeight int    `json:"image_height,omitempty"`
	MD5         string `json:"md5,omitempty"`
	FileMD5     string `json:"file_md5,omitempty"`
	IsHD        bool   `json:"is_hd,omitempty"`
	Length      int64  `json:"length,omitempty"`
	Filename    string `json:"filename,omitempty"`
	FileName    string `json:"file_name,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	AuthKey     string `json:"auth_key,omitempty"`
}
