package response

type WxWorkCLIInboundResponse struct {
	ConversationID int64 `json:"conversationId"`
	MessageID      int64 `json:"messageId"`
}

type WxWorkCLIOutboxItemResponse struct {
	OutboxID       int64  `json:"outboxId"`
	ConversationID int64  `json:"conversationId"`
	MessageID      int64  `json:"messageId"`
	ChatID         string `json:"chatId"`
	ChatName       string `json:"chatName"`
	ChatType       int    `json:"chatType"`
	Content        string `json:"content"`
}

type WxWorkCLIOutboxPollResponse struct {
	Items []WxWorkCLIOutboxItemResponse `json:"items"`
}
