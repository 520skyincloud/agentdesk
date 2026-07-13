package response

import "agent-desk/internal/pkg/enums"

type AgentProfileResponse struct {
	ID                     int64               `json:"id"`
	UserID                 int64               `json:"userId"`
	TeamID                 int64               `json:"teamId"`
	StoreScopeIDs          []int64             `json:"storeScopeIds"`
	WxWorkInstanceScopeIDs []int64             `json:"wxWorkInstanceScopeIds"`
	TeamName               string              `json:"teamName,omitempty"`
	Username               string              `json:"username,omitempty"`
	Nickname               string              `json:"nickname,omitempty"`
	AgentCode              string              `json:"agentCode"`
	DisplayName            string              `json:"displayName"`
	Avatar                 string              `json:"avatar"`
	ServiceStatus          enums.ServiceStatus `json:"serviceStatus"`
	MaxConcurrentCount     int                 `json:"maxConcurrentCount"`
	PriorityLevel          int                 `json:"priorityLevel"`
	AutoAssignEnabled      bool                `json:"autoAssignEnabled"`
	ReceiveOfflineMessage  bool                `json:"receiveOfflineMessage"`
	LastOnlineAt           string              `json:"lastOnlineAt,omitempty"`
	LastStatusAt           string              `json:"lastStatusAt,omitempty"`
	Remark                 string              `json:"remark"`
	ActiveTaskCount        int                 `json:"activeTaskCount"`
	PendingReplyCount      int                 `json:"pendingReplyCount"`
	ProcessingTaskCount    int                 `json:"processingTaskCount"`
}

type AgentTeamResponse struct {
	ID                     int64        `json:"id"`
	Name                   string       `json:"name"`
	LeaderUserID           int64        `json:"leaderUserId"`
	StoreStaffUserIDs      []int64      `json:"storeStaffUserIds"`
	CompanyScopeIDs        []int64      `json:"companyScopeIds"`
	StoreScopeIDs          []int64      `json:"storeScopeIds"`
	WxWorkInstanceScopeIDs []int64      `json:"wxWorkInstanceScopeIds"`
	LeaderUsername         string       `json:"leaderUsername,omitempty"`
	LeaderNickname         string       `json:"leaderNickname,omitempty"`
	Status                 enums.Status `json:"status"`
	Description            string       `json:"description"`
	Remark                 string       `json:"remark"`
	Manageable             bool         `json:"manageable"`
	PendingReplyCount      int          `json:"pendingReplyCount"`
	SquadCount             int          `json:"squadCount"`
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
	ID        int64  `json:"id"`
	TeamID    int64  `json:"teamId"`
	TeamName  string `json:"teamName,omitempty"`
	SquadID   int64  `json:"squadId"`
	SquadName string `json:"squadName,omitempty"`
	StartAt   string `json:"startAt"`
	EndAt     string `json:"endAt"`
	Remark    string `json:"remark"`
}

type AgentTeamScheduleBatchPreviewResponse struct {
	Total    int                                 `json:"total"`
	Conflict bool                                `json:"conflict"`
	Items    []AgentTeamScheduleBatchPreviewItem `json:"items"`
}

type AgentTeamScheduleBatchPreviewItem struct {
	TeamID         int64  `json:"teamId"`
	TeamName       string `json:"teamName"`
	SquadID        int64  `json:"squadId"`
	SquadName      string `json:"squadName,omitempty"`
	Date           string `json:"date"`
	Weekday        int    `json:"weekday"`
	StartAt        string `json:"startAt"`
	EndAt          string `json:"endAt"`
	Remark         string `json:"remark"`
	Conflict       bool   `json:"conflict"`
	ConflictReason string `json:"conflictReason"`
}

type AgentTeamScheduleBatchGenerateResponse struct {
	Created int `json:"created"`
}
