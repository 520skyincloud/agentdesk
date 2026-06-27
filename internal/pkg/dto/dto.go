package dto

import "agent-desk/internal/pkg/enums"

type AuthPrincipal struct {
	UserID      int64
	Username    string
	Nickname    string
	Avatar      string
	Status      enums.Status
	Roles       []string
	Permissions []string
}

type WxWorkKFChannelConfig struct {
	OpenKfID string `json:"openKfId"`
}

type WxWorkCLIChannelConfig struct {
	BridgeToken     string `json:"bridgeToken,omitempty"`
	DefaultChatType int    `json:"defaultChatType,omitempty"`
}

type WxWorkProtocolChannelConfig struct {
	AppKey        string `json:"appKey,omitempty"`
	AppSecret     string `json:"appSecret,omitempty"`
	BaseURL       string `json:"baseUrl,omitempty"`
	CallbackToken string `json:"callbackToken,omitempty"`
}

type WebChannelConfig struct {
	Title           string `json:"title"`
	Subtitle        string `json:"subtitle"`
	ThemeColor      string `json:"themeColor"`
	Position        string `json:"position"`
	Width           string `json:"width"`
	UserTokenSecret string `json:"userTokenSecret,omitempty"`
}

type WechatMPChannelConfig struct {
	Title           string `json:"title"`
	Subtitle        string `json:"subtitle"`
	ThemeColor      string `json:"themeColor"`
	UserTokenSecret string `json:"userTokenSecret,omitempty"`
}
