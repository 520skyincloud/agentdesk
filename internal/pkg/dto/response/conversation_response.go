package response

import "agent-desk/internal/pkg/enums"

type ConversationTagResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type CustomerTagResponse struct {
	ID              int64   `json:"id"`
	TagID           int64   `json:"tagId"`
	Name            string  `json:"name"`
	StandardName    string  `json:"standardName"`
	SemanticKey     string  `json:"semanticKey"`
	ConflictGroup   string  `json:"conflictGroup,omitempty"`
	Source          string  `json:"source"`
	Confidence      float64 `json:"confidence"`
	EvidenceCount   int     `json:"evidenceCount"`
	ManualProtected bool    `json:"manualProtected"`
	UpdatedAt       string  `json:"updatedAt,omitempty"`
}

type CustomerTagChangeLogResponse struct {
	ID                 int64   `json:"id"`
	Action             string  `json:"action"`
	OldTagID           int64   `json:"oldTagId"`
	OldTagName         string  `json:"oldTagName,omitempty"`
	NewTagID           int64   `json:"newTagId"`
	NewTagName         string  `json:"newTagName,omitempty"`
	EvidenceMessageIDs []int64 `json:"evidenceMessageIds"`
	Source             string  `json:"source"`
	Confidence         float64 `json:"confidence"`
	OperatorType       string  `json:"operatorType"`
	OperatorID         int64   `json:"operatorId"`
	OperatorName       string  `json:"operatorName"`
	CreatedAt          string  `json:"createdAt"`
}

type ConversationParticipantResponse struct {
	ID                    int64        `json:"id"`
	ParticipantType       string       `json:"participantType"`
	ParticipantID         int64        `json:"participantId"`
	ExternalParticipantID string       `json:"externalParticipantId,omitempty"`
	JoinedAt              string       `json:"joinedAt,omitempty"`
	LeftAt                string       `json:"leftAt,omitempty"`
	Status                enums.Status `json:"status"`
}

type ConversationManualAttentionResponse struct {
	Dot       bool   `json:"dot"`
	Level     string `json:"level"`
	Label     string `json:"label"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

type ConversationResponse struct {
	ID                        int64                               `json:"id"`
	AIAgentID                 int64                               `json:"aiAgentId"`
	ChannelID                 int64                               `json:"channelId"`
	CustomerID                int64                               `json:"customerId"`
	CustomerName              string                              `json:"customerName"`
	CustomerAvatar            string                              `json:"customerAvatar,omitempty"`
	Status                    enums.IMConversationStatus          `json:"status"`
	ServiceMode               enums.IMConversationServiceMode     `json:"serviceMode"`
	Priority                  int                                 `json:"priority"`
	CurrentAssigneeID         int64                               `json:"currentAssigneeId"`
	CurrentAssigneeName       string                              `json:"currentAssigneeName,omitempty"`
	CurrentTeamID             int64                               `json:"currentTeamId"`
	CurrentTeamName           string                              `json:"currentTeamName,omitempty"`
	LastMessageID             int64                               `json:"lastMessageId"`
	LastMessageAt             string                              `json:"lastMessageAt,omitempty"`
	LastActiveAt              string                              `json:"lastActiveAt,omitempty"`
	LastMessageSummary        string                              `json:"lastMessageSummary,omitempty"`
	CustomerUnreadCount       int                                 `json:"customerUnreadCount"`
	AgentUnreadCount          int                                 `json:"agentUnreadCount"`
	CustomerLastReadMessageID int64                               `json:"customerLastReadMessageId"`
	CustomerLastReadSeqNo     int64                               `json:"customerLastReadSeqNo"`
	CustomerLastReadAt        string                              `json:"customerLastReadAt,omitempty"`
	AgentLastReadMessageID    int64                               `json:"agentLastReadMessageId"`
	AgentLastReadSeqNo        int64                               `json:"agentLastReadSeqNo"`
	AgentLastReadAt           string                              `json:"agentLastReadAt,omitempty"`
	CustomerOnline            bool                                `json:"customerOnline"`
	ClosedAt                  string                              `json:"closedAt,omitempty"`
	ClosedBy                  int64                               `json:"closedBy"`
	ClosedByName              string                              `json:"closedByName,omitempty"`
	CloseReason               string                              `json:"closeReason,omitempty"`
	RouteStatus               enums.ConversationRouteStatus       `json:"routeStatus,omitempty"`
	RouteStatusLabel          string                              `json:"routeStatusLabel,omitempty"`
	RouteTarget               string                              `json:"routeTarget,omitempty"`
	HandoffReason             string                              `json:"handoffReason,omitempty"`
	NeedHumanFollowUp         bool                                `json:"needHumanFollowUp"`
	AutoHandoffEnabled        bool                                `json:"autoHandoffEnabled"`
	ManualExpireAt            string                              `json:"manualExpireAt,omitempty"`
	ManualAttention           ConversationManualAttentionResponse `json:"manualAttention"`
	StoreID                   int64                               `json:"storeId,omitempty"`
	StoreName                 string                              `json:"storeName,omitempty"`
	WxWorkInstanceID          int64                               `json:"wxWorkInstanceId,omitempty"`
	WxWorkExternalUserID      string                              `json:"wxWorkExternalUserId,omitempty"`
	WxWorkEmployeeName        string                              `json:"wxWorkEmployeeName,omitempty"`
	WxWorkEmployeeUserID      string                              `json:"wxWorkEmployeeUserId,omitempty"`
	CustomerTags              []CustomerTagResponse               `json:"customerTags,omitempty"`
}

type ConversationDetailResponse struct {
	ConversationResponse
	Participants []ConversationParticipantResponse `json:"participants,omitempty"`
}
