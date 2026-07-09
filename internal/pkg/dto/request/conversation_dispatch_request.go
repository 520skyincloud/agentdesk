package request

type ConversationDispatchListRequest struct {
	Status         string `json:"status"`
	TeamID         int64  `json:"teamId"`
	AssigneeID     int64  `json:"assigneeId"`
	Keyword        string `json:"keyword"`
	OnlyManageable bool   `json:"onlyManageable"`
}

type ConversationDispatchActionRequest struct {
	ConversationID int64  `json:"conversationId"`
	AssigneeID     int64  `json:"assigneeId"`
	Reason         string `json:"reason"`
}

type ConversationDispatchAutoAssignRequest struct {
	ConversationID int64 `json:"conversationId"`
	TeamID         int64 `json:"teamId"`
}
