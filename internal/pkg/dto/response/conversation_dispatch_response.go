package response

import "agent-desk/internal/pkg/enums"

type ConversationDispatchTaskResponse struct {
	ConversationID          int64                           `json:"conversationId"`
	CustomerID              int64                           `json:"customerId"`
	CustomerName            string                          `json:"customerName"`
	Status                  string                          `json:"status"`
	StatusLabel             string                          `json:"statusLabel"`
	ConversationStatus      enums.IMConversationStatus      `json:"conversationStatus"`
	RouteStatus             enums.ConversationRouteStatus   `json:"routeStatus,omitempty"`
	RouteStatusLabel        string                          `json:"routeStatusLabel,omitempty"`
	ServiceMode             enums.IMConversationServiceMode `json:"serviceMode"`
	TeamID                  int64                           `json:"teamId"`
	TeamName                string                          `json:"teamName,omitempty"`
	Manageable              bool                            `json:"manageable"`
	CurrentAssigneeID       int64                           `json:"currentAssigneeId"`
	CurrentAssigneeName     string                          `json:"currentAssigneeName,omitempty"`
	StoreID                 int64                           `json:"storeId,omitempty"`
	StoreName               string                          `json:"storeName,omitempty"`
	WxWorkInstanceID        int64                           `json:"wxWorkInstanceId,omitempty"`
	WxWorkEmployeeName      string                          `json:"wxWorkEmployeeName,omitempty"`
	WxWorkEmployeeUserID    string                          `json:"wxWorkEmployeeUserId,omitempty"`
	HandoffReason           string                          `json:"handoffReason,omitempty"`
	LastMessageSummary      string                          `json:"lastMessageSummary,omitempty"`
	LastMessageAt           string                          `json:"lastMessageAt,omitempty"`
	WaitingSeconds          int64                           `json:"waitingSeconds"`
	SLAType                 string                          `json:"slaType,omitempty"`
	SLAStatus               string                          `json:"slaStatus,omitempty"`
	SLADeadlineAt           string                          `json:"slaDeadlineAt,omitempty"`
	SLARemainingSeconds     int64                           `json:"slaRemainingSeconds"`
	AssignedAt              string                          `json:"assignedAt,omitempty"`
	FirstAgentReplyAt       string                          `json:"firstAgentReplyAt,omitempty"`
	RecommendedAssigneeID   int64                           `json:"recommendedAssigneeId,omitempty"`
	RecommendedAssigneeName string                          `json:"recommendedAssigneeName,omitempty"`
	RecommendationReason    string                          `json:"recommendationReason,omitempty"`
	DispatchMode            enums.AgentTeamDispatchMode     `json:"dispatchMode,omitempty"`
	DispatchModeLabel       string                          `json:"dispatchModeLabel,omitempty"`
	WorkloadWeight          int                             `json:"workloadWeight"`
	Priority                int                             `json:"priority"`
	AssignmentReason        string                          `json:"assignmentReason,omitempty"`
}

type ConversationDispatchStatsResponse struct {
	Total              int `json:"total"`
	Pending            int `json:"pending"`
	Assigned           int `json:"assigned"`
	Processing         int `json:"processing"`
	Warning            int `json:"warning"`
	Timeout            int `json:"timeout"`
	Closed             int `json:"closed"`
	ManageablePending  int `json:"manageablePending"`
	ManageableAssigned int `json:"manageableAssigned"`
	AvailableAgents    int `json:"availableAgents"`
}

type ConversationDispatchAgentLoadResponse struct {
	UserID              int64                     `json:"userId"`
	ProfileID           int64                     `json:"profileId"`
	TeamID              int64                     `json:"teamId"`
	TeamName            string                    `json:"teamName,omitempty"`
	Username            string                    `json:"username,omitempty"`
	Nickname            string                    `json:"nickname,omitempty"`
	DisplayName         string                    `json:"displayName"`
	MaxConcurrentCount  int                       `json:"maxConcurrentCount"`
	ActiveCount         int                       `json:"activeCount"`
	PendingFirstReply   int                       `json:"pendingFirstReply"`
	PendingReplyCount   int                       `json:"pendingReplyCount"`
	ProcessingCount     int                       `json:"processingCount"`
	AutoAssignEnabled   bool                      `json:"autoAssignEnabled"`
	Available           bool                      `json:"available"`
	ManuallyAssignable  bool                      `json:"manuallyAssignable"`
	AvailabilityCode    string                    `json:"availabilityCode"`
	AvailabilityReason  string                    `json:"availabilityReason"`
	PresenceStatus      enums.AgentPresenceStatus `json:"presenceStatus,omitempty"`
	PresenceLastSeenAt  string                    `json:"presenceLastSeenAt,omitempty"`
	PriorityLevel       int                       `json:"priorityLevel"`
	LastOnlineAt        string                    `json:"lastOnlineAt,omitempty"`
	WeightedOpenLoad    int                       `json:"weightedOpenLoad"`
	ShiftWorkloadWeight int                       `json:"shiftWorkloadWeight"`
	NormalizedLoad      float64                   `json:"normalizedLoad"`
}

type ConversationDispatchListResponse struct {
	Tasks  []ConversationDispatchTaskResponse      `json:"tasks"`
	Stats  ConversationDispatchStatsResponse       `json:"stats"`
	Agents []ConversationDispatchAgentLoadResponse `json:"agents"`
}
