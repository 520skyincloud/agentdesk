package response

type MiniprogramChatMessageResponse struct {
	ID             int64  `json:"id"`
	ConversationID int64  `json:"conversationId"`
	Role           string `json:"role"`
	SenderType     string `json:"senderType"`
	MessageType    string `json:"messageType"`
	Content        string `json:"content"`
	CreatedAt      string `json:"createdAt"`
}

type MiniprogramSessionStartResponse struct {
	SessionID      string                           `json:"sessionId"`
	ConversationID int64                            `json:"conversationId"`
	WelcomeMessage string                           `json:"welcomeMessage"`
	Messages       []MiniprogramChatMessageResponse `json:"messages"`
}

type MiniprogramMessageSendResponse struct {
	SessionID        string                           `json:"sessionId"`
	ConversationID   int64                            `json:"conversationId"`
	UserMessage      *MiniprogramChatMessageResponse  `json:"userMessage,omitempty"`
	AIMessage        *MiniprogramChatMessageResponse  `json:"aiMessage,omitempty"`
	CreatedAt        string                           `json:"createdAt"`
	AnswerStatus     string                           `json:"answerStatus"`
	NeedHumanSupport bool                             `json:"needHumanSupport,omitempty"`
	Messages         []MiniprogramChatMessageResponse `json:"messages,omitempty"`
}

type MiniprogramMessageListResponse struct {
	SessionID      string                           `json:"sessionId"`
	ConversationID int64                            `json:"conversationId"`
	Messages       []MiniprogramChatMessageResponse `json:"messages"`
}
