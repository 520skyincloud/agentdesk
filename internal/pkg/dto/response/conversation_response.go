package response

import (
	"agent-desk/internal/pkg/enums"
	"time"
)

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

type ConversationTakeoverStateResponse struct {
	RequestID            int64  `json:"requestId,omitempty"`
	RequestStatus        string `json:"requestStatus,omitempty"`
	RequesterUserID      int64  `json:"requesterUserId,omitempty"`
	RequesterName        string `json:"requesterName,omitempty"`
	TeamID               int64  `json:"teamId,omitempty"`
	TeamName             string `json:"teamName,omitempty"`
	Reason               string `json:"reason,omitempty"`
	ReviewRemark         string `json:"reviewRemark,omitempty"`
	RequestedAt          string `json:"requestedAt,omitempty"`
	ReviewedAt           string `json:"reviewedAt,omitempty"`
	CanReply             bool   `json:"canReply"`
	CanRequest           bool   `json:"canRequest"`
	CanDirectTakeover    bool   `json:"canDirectTakeover"`
	CanReview            bool   `json:"canReview"`
	CanResumeAI          bool   `json:"canResumeAi"`
	CanActivateTakeover  bool   `json:"canActivateTakeover"`
	IsCurrentAssignee    bool   `json:"isCurrentAssignee"`
	PendingForMe         bool   `json:"pendingForMe"`
	PendingForAnother    bool   `json:"pendingForAnother"`
	AuthorizedForMe      bool   `json:"authorizedForMe"`
	AuthorizedForAnother bool   `json:"authorizedForAnother"`
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
	StoreStaffBindingID       int64                               `json:"storeStaffBindingId,omitempty"`
	WxWorkInstanceID          int64                               `json:"wxWorkInstanceId,omitempty"`
	CurrentSessionNo          int                                 `json:"currentSessionNo,omitempty"`
	WxWorkReplyReady          bool                                `json:"wxWorkReplyReady"`
	WxWorkReplyStatus         string                              `json:"wxWorkReplyStatus,omitempty"`
	WxWorkExternalUserID      string                              `json:"wxWorkExternalUserId,omitempty"`
	WxWorkEmployeeName        string                              `json:"wxWorkEmployeeName,omitempty"`
	WxWorkEmployeeUserID      string                              `json:"wxWorkEmployeeUserId,omitempty"`
	CustomerTags              []CustomerTagResponse               `json:"customerTags,omitempty"`
}

type ConversationDetailResponse struct {
	ConversationResponse
	TakeoverState        ConversationTakeoverStateResponse    `json:"takeoverState"`
	Participants         []ConversationParticipantResponse    `json:"participants,omitempty"`
	ChannelSessions      []ConversationChannelSessionResponse `json:"channelSessions,omitempty"`
	HistorySegments      []ConversationHistorySegmentResponse `json:"historySegments,omitempty"`
	RelatedConversations []ConversationResponse               `json:"relatedConversations,omitempty"`
	ContinuityLinks      []ConversationContinuityLinkResponse `json:"continuityLinks,omitempty"`
}

type ConversationHistorySegmentResponse struct {
	Index                     int          `json:"index"`
	ConversationID            int64        `json:"conversationId"`
	SessionNo                 int          `json:"sessionNo"`
	StoreID                   int64        `json:"storeId"`
	StoreStaffBindingID       int64        `json:"storeStaffBindingId"`
	WxWorkInstanceID          int64        `json:"wxWorkInstanceId"`
	ChannelID                 int64        `json:"channelId"`
	StartReason               string       `json:"startReason"`
	StoreStaffDisplayName     string       `json:"storeStaffDisplayName"`
	WxWorkEmployeeDisplayName string       `json:"wxWorkEmployeeDisplayName"`
	StartedAt                 time.Time    `json:"startedAt"`
	EndedAt                   *time.Time   `json:"endedAt,omitempty"`
	Status                    enums.Status `json:"status"`
	InheritedHistory          bool         `json:"inheritedHistory"`
	CurrentConversation       bool         `json:"currentConversation"`
}

type ConversationContinuityLinkResponse struct {
	PredecessorConversationID int64  `json:"predecessorConversationId"`
	SuccessorConversationID   int64  `json:"successorConversationId"`
	Reason                    string `json:"reason"`
	CreatedAt                 string `json:"createdAt"`
}

type ConversationChannelSessionResponse struct {
	SessionNo                 int          `json:"sessionNo"`
	StoreID                   int64        `json:"storeId"`
	StoreStaffBindingID       int64        `json:"storeStaffBindingId"`
	WxWorkInstanceID          int64        `json:"wxWorkInstanceId"`
	ChannelID                 int64        `json:"channelId"`
	StartReason               string       `json:"startReason"`
	StoreStaffDisplayName     string       `json:"storeStaffDisplayName"`
	WxWorkEmployeeDisplayName string       `json:"wxWorkEmployeeDisplayName"`
	StartedAt                 time.Time    `json:"startedAt"`
	EndedAt                   *time.Time   `json:"endedAt,omitempty"`
	Status                    enums.Status `json:"status"`
}

type StoreConversationInheritancePreviewResponse struct {
	SourceStoreStaffBindingID int64                                             `json:"sourceStoreStaffBindingId"`
	TargetStoreStaffBindingID int64                                             `json:"targetStoreStaffBindingId"`
	TargetWxWorkInstanceID    int64                                             `json:"targetWxWorkInstanceId"`
	StoreID                   int64                                             `json:"storeId"`
	StoreName                 string                                            `json:"storeName"`
	PreviewVersion            string                                            `json:"previewVersion"`
	EligibleCount             int                                               `json:"eligibleCount"`
	LinkedExistingCount       int                                               `json:"linkedExistingCount"`
	ConflictCount             int                                               `json:"conflictCount"`
	Items                     []StoreConversationInheritancePreviewItemResponse `json:"items"`
}

type StoreConversationInheritancePreviewItemResponse struct {
	ConversationID       int64  `json:"conversationId"`
	CustomerID           int64  `json:"customerId"`
	CustomerName         string `json:"customerName"`
	LastMessageAt        string `json:"lastMessageAt,omitempty"`
	CurrentSessionNo     int    `json:"currentSessionNo"`
	Eligible             bool   `json:"eligible"`
	ResolutionMode       string `json:"resolutionMode"`
	TargetConversationID int64  `json:"targetConversationId,omitempty"`
	ConflictReason       string `json:"conflictReason,omitempty"`
}

type BatchStoreConversationInheritanceResponse struct {
	InheritedCount  int64   `json:"inheritedCount"`
	CreatedCount    int64   `json:"createdCount"`
	LinkedCount     int64   `json:"linkedCount"`
	ConversationIDs []int64 `json:"conversationIds"`
}
