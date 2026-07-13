package builders

import (
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"
)

func BuildAgentTeamSquadList(items []services.AgentTeamSquadOverview) []response.AgentTeamSquadResponse {
	ret := make([]response.AgentTeamSquadResponse, 0, len(items))
	for i := range items {
		item := items[i]
		memberProfileIDs := make([]int64, len(item.MemberProfileIDs))
		copy(memberProfileIDs, item.MemberProfileIDs)
		result := response.AgentTeamSquadResponse{
			ID:               item.Squad.ID,
			TeamID:           item.Squad.TeamID,
			Name:             item.Squad.Name,
			LeaderUserID:     item.Squad.LeaderUserID,
			LeaderName:       item.LeaderName,
			MemberProfileIDs: memberProfileIDs,
			Status:           item.Squad.Status,
			Remark:           item.Squad.Remark,
			Manageable:       item.Manageable,
		}
		if item.ActiveSchedule != nil {
			result.ActiveScheduleID = item.ActiveSchedule.ID
			result.ActiveScheduleStartAt = utils.FormatTime(item.ActiveSchedule.StartAt)
			result.ActiveScheduleEndAt = utils.FormatTime(item.ActiveSchedule.EndAt)
		}
		if item.NextSchedule != nil {
			result.NextScheduleStartAt = utils.FormatTime(item.NextSchedule.StartAt)
			result.NextScheduleEndAt = utils.FormatTime(item.NextSchedule.EndAt)
		}
		ret = append(ret, result)
	}
	return ret
}
