package request

type UpdateHQQiyuRouteRequest struct {
	DefaultGroupID  string `json:"defaultGroupId"`
	HighRiskGroupID string `json:"highRiskGroupId"`
	ServiceTime     string `json:"serviceTime"`
	FallbackMode    string `json:"fallbackMode"`
	TimeoutMinutes  int    `json:"timeoutMinutes"`
	Status          int    `json:"status"`
	Remark          string `json:"remark"`
}

type QiyuCallbackRequest struct {
	EventType      string `json:"eventType" form:"eventType"`
	ConversationID int64  `json:"conversationId" form:"conversationId"`
	QiyuUID        string `json:"qiyuUid" form:"qiyuUid"`
	SessionID      string `json:"sessionId" form:"sessionId"`
	StaffID        string `json:"staffId" form:"staffId"`
	StaffName      string `json:"staffName" form:"staffName"`
	StaffType      string `json:"staffType" form:"staffType"`
	MsgID          string `json:"msgId" form:"msgId"`
	Content        string `json:"content" form:"content"`
	MsgType        string `json:"msgType" form:"msgType"`
	CloseReason    string `json:"closeReason" form:"closeReason"`
	TransferTo     string `json:"transferTo" form:"transferTo"`
	RawPayload     string `json:"rawPayload" form:"rawPayload"`
}
