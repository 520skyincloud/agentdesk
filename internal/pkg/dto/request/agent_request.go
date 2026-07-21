package request

import "agent-desk/internal/pkg/enums"

type CreateAgentProfileRequest struct {
	UserID                 int64   `json:"userId"`
	TeamID                 int64   `json:"teamId"`
	StoreScopeIDs          []int64 `json:"storeScopeIds"`
	WxWorkInstanceScopeIDs []int64 `json:"wxWorkInstanceScopeIds"`
	AgentCode              string  `json:"agentCode"`
	DisplayName            string  `json:"displayName"`
	Avatar                 string  `json:"avatar"`
	MaxConcurrentCount     int     `json:"maxConcurrentCount"`
	PriorityLevel          int     `json:"priorityLevel"`
	AutoAssignEnabled      bool    `json:"autoAssignEnabled"`
	Remark                 string  `json:"remark"`
}

type UpdateAgentProfileRequest struct {
	ID int64 `json:"id"`
	CreateAgentProfileRequest
}

type DeleteAgentProfileRequest struct {
	ID int64 `json:"id"`
}

type CreateAgentTeamRequest struct {
	Name                   string                      `json:"name"`
	LeaderUserID           int64                       `json:"leaderUserId"`
	StoreStaffUserIDs      []int64                     `json:"storeStaffUserIds"`
	StoreScopeIDs          []int64                     `json:"storeScopeIds"`
	WxWorkInstanceScopeIDs []int64                     `json:"wxWorkInstanceScopeIds"`
	DispatchMode           enums.AgentTeamDispatchMode `json:"dispatchMode"`
	Status                 int                         `json:"status"`
	Description            string                      `json:"description"`
	Remark                 string                      `json:"remark"`
}

type UpdateAgentTeamRequest struct {
	ID                     int64                       `json:"id"`
	Name                   string                      `json:"name"`
	LeaderUserID           int64                       `json:"leaderUserId"`
	StoreStaffUserIDs      []int64                     `json:"storeStaffUserIds"`
	StoreScopeIDs          []int64                     `json:"storeScopeIds"`
	WxWorkInstanceScopeIDs []int64                     `json:"wxWorkInstanceScopeIds"`
	DispatchMode           enums.AgentTeamDispatchMode `json:"dispatchMode"`
	Status                 int                         `json:"status"`
	Description            string                      `json:"description"`
	Remark                 string                      `json:"remark"`
}

type DeleteAgentTeamRequest struct {
	ID int64 `json:"id"`
}

type CreateAgentTeamSquadRequest struct {
	TeamID       int64   `json:"teamId"`
	Name         string  `json:"name"`
	LeaderUserID int64   `json:"leaderUserId"`
	MemberIDs    []int64 `json:"memberIds"`
	Status       int     `json:"status"`
	Remark       string  `json:"remark"`
}

type UpdateAgentTeamSquadRequest struct {
	ID int64 `json:"id"`
	CreateAgentTeamSquadRequest
}

type DeleteAgentTeamSquadRequest struct {
	ID int64 `json:"id"`
}

type ReplaceAgentTeamSquadMembersRequest struct {
	SquadID         int64   `json:"squadId"`
	AgentProfileIDs []int64 `json:"agentProfileIds"`
}

type CreateAgentTeamScheduleRequest struct {
	TeamID                  int64   `json:"teamId"`
	SquadID                 int64   `json:"squadId"`
	IncludedAgentProfileIDs []int64 `json:"includedAgentProfileIds"`
	ExcludedAgentProfileIDs []int64 `json:"excludedAgentProfileIds"`
	StartAt                 string  `json:"startAt"`
	EndAt                   string  `json:"endAt"`
	Remark                  string  `json:"remark"`
}

type UpdateAgentTeamScheduleRequest struct {
	ID int64 `json:"id"`
	CreateAgentTeamScheduleRequest
}

type DeleteAgentTeamScheduleRequest struct {
	ID int64 `json:"id"`
}

type AgentTeamScheduleCalendarRequest struct {
	StartAt string `json:"startAt"`
	EndAt   string `json:"endAt"`
	TeamID  int64  `json:"teamId"`
	SquadID int64  `json:"squadId"`
}

type AgentTeamScheduleBatchRequest struct {
	TeamIDs                 []int64                      `json:"teamIds"`
	SquadID                 int64                        `json:"squadId"`
	IncludedAgentProfileIDs []int64                      `json:"includedAgentProfileIds"`
	ExcludedAgentProfileIDs []int64                      `json:"excludedAgentProfileIds"`
	StartDate               string                       `json:"startDate"`
	EndDate                 string                       `json:"endDate"`
	Weekdays                []int                        `json:"weekdays"`
	StartTime               string                       `json:"startTime"`
	EndTime                 string                       `json:"endTime"`
	TimeRanges              []AgentTeamScheduleTimeRange `json:"timeRanges"`
	Remark                  string                       `json:"remark"`
}

type AgentTeamScheduleTimeRange struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}
