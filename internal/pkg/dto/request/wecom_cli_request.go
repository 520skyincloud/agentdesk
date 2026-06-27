package request

type WxWorkCLIInboundRequest struct {
	ChannelID    string `json:"channelId"`
	BridgeToken  string `json:"bridgeToken"`
	ChatID       string `json:"chatId"`
	ChatType     int    `json:"chatType"`
	ChatName     string `json:"chatName"`
	MsgID        string `json:"msgId"`
	SenderUserID string `json:"senderUserId"`
	SenderName   string `json:"senderName"`
	SendTime     string `json:"sendTime"`
	MsgType      string `json:"msgType"`
	Content      string `json:"content"`
	RawPayload   string `json:"rawPayload"`
}

type WxWorkCLIOutboxPollRequest struct {
	ChannelID   string `json:"channelId"`
	BridgeToken string `json:"bridgeToken"`
	Limit       int    `json:"limit"`
}

type WxWorkCLIOutboxSentRequest struct {
	ChannelID      string `json:"channelId"`
	BridgeToken    string `json:"bridgeToken"`
	OutboxID       int64  `json:"outboxId"`
	ExternalMsgID  string `json:"externalMsgId"`
	ExternalResult string `json:"externalResult"`
}

type WxWorkCLIOutboxFailedRequest struct {
	ChannelID   string `json:"channelId"`
	BridgeToken string `json:"bridgeToken"`
	OutboxID    int64  `json:"outboxId"`
	Error       string `json:"error"`
}
