package response

import "agent-desk/internal/pkg/enums"

type AgentProfileResponse struct {
	ID                     int64                     `json:"id"`
	UserID                 int64                     `json:"userId"`
	TeamID                 int64                     `json:"teamId"`
	StoreScopeIDs          []int64                   `json:"storeScopeIds"`
	WxWorkInstanceScopeIDs []int64                   `json:"wxWorkInstanceScopeIds"`
	TeamName               string                    `json:"teamName,omitempty"`
	Username               string                    `json:"username,omitempty"`
	Nickname               string                    `json:"nickname,omitempty"`
	AgentCode              string                    `json:"agentCode"`
	DisplayName            string                    `json:"displayName"`
	Avatar                 string                    `json:"avatar"`
	MaxConcurrentCount     int                       `json:"maxConcurrentCount"`
	PriorityLevel          int                       `json:"priorityLevel"`
	AutoAssignEnabled      bool                      `json:"autoAssignEnabled"`
	LastOnlineAt           string                    `json:"lastOnlineAt,omitempty"`
	PresenceStatus         enums.AgentPresenceStatus `json:"presenceStatus,omitempty"`
	PresenceLastSeenAt     string                    `json:"presenceLastSeenAt,omitempty"`
	Available              bool                      `json:"available"`
	AvailabilityCode       string                    `json:"availabilityCode,omitempty"`
	AvailabilityReason     string                    `json:"availabilityReason,omitempty"`
	Remark                 string                    `json:"remark"`
	ActiveTaskCount        int                       `json:"activeTaskCount"`
	PendingReplyCount      int                       `json:"pendingReplyCount"`
	ProcessingTaskCount    int                       `json:"processingTaskCount"`
}

type AgentTeamResponse struct {
	ID                     int64                       `json:"id"`
	Name                   string                      `json:"name"`
	LeaderUserID           int64                       `json:"leaderUserId"`
	StoreStaffUserIDs      []int64                     `json:"storeStaffUserIds"`
	StoreScopeIDs          []int64                     `json:"storeScopeIds"`
	WxWorkInstanceScopeIDs []int64                     `json:"wxWorkInstanceScopeIds"`
	DispatchMode           enums.AgentTeamDispatchMode `json:"dispatchMode"`
	DispatchModeLabel      string                      `json:"dispatchModeLabel"`
	LeaderUsername         string                      `json:"leaderUsername,omitempty"`
	LeaderNickname         string                      `json:"leaderNickname,omitempty"`
	Status                 enums.Status                `json:"status"`
	Description            string                      `json:"description"`
	Remark                 string                      `json:"remark"`
	Manageable             bool                        `json:"manageable"`
	PendingReplyCount      int                         `json:"pendingReplyCount"`
	SquadCount             int                         `json:"squadCount"`
}

type AgentTeamSquadResponse struct {
	ID                    int64        `json:"id"`
	TeamID                int64        `json:"teamId"`
	Name                  string       `json:"name"`
	LeaderUserID          int64        `json:"leaderUserId"`
	LeaderName            string       `json:"leaderName,omitempty"`
	MemberProfileIDs      []int64      `json:"memberProfileIds"`
	Status                enums.Status `json:"status"`
	Remark                string       `json:"remark"`
	Manageable            bool         `json:"manageable"`
	ActiveScheduleID      int64        `json:"activeScheduleId"`
	ActiveScheduleStartAt string       `json:"activeScheduleStartAt,omitempty"`
	ActiveScheduleEndAt   string       `json:"activeScheduleEndAt,omitempty"`
	NextScheduleStartAt   string       `json:"nextScheduleStartAt,omitempty"`
	NextScheduleEndAt     string       `json:"nextScheduleEndAt,omitempty"`
}

type AgentTeamScheduleResponse struct {
	ID                      int64   `json:"id"`
	TeamID                  int64   `json:"teamId"`
	TeamName                string  `json:"teamName,omitempty"`
	SquadID                 int64   `json:"squadId"`
	SquadName               string  `json:"squadName,omitempty"`
	IncludedAgentProfileIDs []int64 `json:"includedAgentProfileIds"`
	ExcludedAgentProfileIDs []int64 `json:"excludedAgentProfileIds"`
	StartAt                 string  `json:"startAt"`
	EndAt                   string  `json:"endAt"`
	Remark                  string  `json:"remark"`
}

type AgentTeamScheduleBatchPreviewResponse struct {
	Total    int                                 `json:"total"`
	Conflict bool                                `json:"conflict"`
	Items    []AgentTeamScheduleBatchPreviewItem `json:"items"`
}

type AgentTeamScheduleBatchPreviewItem struct {
	TeamID             int64  `json:"teamId"`
	TeamName           string `json:"teamName"`
	SquadID            int64  `json:"squadId"`
	SquadName          string `json:"squadName,omitempty"`
	Date               string `json:"date"`
	Weekday            int    `json:"weekday"`
	StartAt            string `json:"startAt"`
	EndAt              string `json:"endAt"`
	Remark             string `json:"remark"`
	EligibleAgentCount int    `json:"eligibleAgentCount"`
	TotalCapacity      int    `json:"totalCapacity"`
	CoverageWarning    string `json:"coverageWarning,omitempty"`
	Conflict           bool   `json:"conflict"`
	ConflictReason     string `json:"conflictReason"`
}

type AgentTeamScheduleBatchGenerateResponse struct {
	Created int `json:"created"`
}
