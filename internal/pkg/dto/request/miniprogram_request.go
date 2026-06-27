package request

type MiniprogramChatContextRequest struct {
	SessionID      string `json:"sessionId"`
	ConversationID int64  `json:"conversationId"`
	StoreID        string `json:"storeId"`
	BrandCode      string `json:"brandCode"`
	OrderNo        string `json:"orderNo"`
	Source         string `json:"source"`
	HotelName      string `json:"hotelName"`
	ChannelID      string `json:"channelId"`
}

type MiniprogramSessionStartRequest struct {
	MiniprogramChatContextRequest
}

type MiniprogramMessageSendRequest struct {
	MiniprogramChatContextRequest
	Content string `json:"content"`
}
