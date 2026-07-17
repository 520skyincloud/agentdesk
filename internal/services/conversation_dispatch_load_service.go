package services

import (
	"slices"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

type agentDispatchLoadSnapshot struct {
	activeCount         int
	weightedOpenLoad    int
	pendingFirstReply   int
	pendingReplyCount   int
	shiftAssignedWeight int
	lastAssignedAt      time.Time
}

func normalizedDispatchMode(mode enums.AgentTeamDispatchMode) enums.AgentTeamDispatchMode {
	if !enums.IsValidAgentTeamDispatchMode(mode) {
		return enums.AgentTeamDispatchModeRule
	}
	return mode
}

func normalizedWorkloadWeight(conversation *models.Conversation) int {
	if conversation == nil || conversation.DispatchWeight <= 0 {
		return 1
	}
	if conversation.DispatchWeight > 5 {
		return 5
	}
	return conversation.DispatchWeight
}

func normalizedConversationPriority(conversation *models.Conversation) int {
	if conversation == nil || conversation.Priority < 0 {
		return 0
	}
	if conversation.Priority > 100 {
		return 100
	}
	return conversation.Priority
}

func fairPendingConversationQueue(conversations []models.Conversation) []models.Conversation {
	if len(conversations) < 2 {
		return conversations
	}
	tenantOrder := make([]int64, 0)
	queues := make(map[int64][]models.Conversation)
	for _, conversation := range conversations {
		if _, exists := queues[conversation.TenantID]; !exists {
			tenantOrder = append(tenantOrder, conversation.TenantID)
		}
		queues[conversation.TenantID] = append(queues[conversation.TenantID], conversation)
	}
	ret := make([]models.Conversation, 0, len(conversations))
	for len(ret) < len(conversations) {
		for _, tenantID := range tenantOrder {
			queue := queues[tenantID]
			if len(queue) == 0 {
				continue
			}
			ret = append(ret, queue[0])
			queues[tenantID] = queue[1:]
		}
	}
	return ret
}

func (s *conversationDispatchService) prioritizePendingConversations(conversations []models.Conversation, now time.Time) []models.Conversation {
	ret := append([]models.Conversation(nil), conversations...)
	priorities := make(map[int64]int, len(ret))
	for i := range ret {
		route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), ret[i].ID, ret[i].TenantID)
		_, priorities[ret[i].ID] = s.ruleDispatchAssessmentAt(&ret[i], route, now)
	}
	slices.SortStableFunc(ret, func(a, b models.Conversation) int {
		if priorities[a.ID] != priorities[b.ID] {
			return priorities[b.ID] - priorities[a.ID]
		}
		aPendingAt := pendingConversationAt(a)
		bPendingAt := pendingConversationAt(b)
		if aPendingAt.Before(bPendingAt) {
			return -1
		}
		if aPendingAt.After(bPendingAt) {
			return 1
		}
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return ret
}

func pendingConversationAt(conversation models.Conversation) time.Time {
	if conversation.HandoffAt != nil {
		return *conversation.HandoffAt
	}
	return conversation.CreatedAt
}

func (s *conversationDispatchService) resolveDispatchTeamIDs(conversation *models.Conversation, aiAgent *models.AIAgent, route *models.ConversationRouteState) []int64 {
	if conversation == nil || conversation.TenantID <= 0 {
		return nil
	}
	teamIDs := make([]int64, 0, 4)
	appendTeam := func(teamID int64) {
		if teamID <= 0 || slices.Contains(teamIDs, teamID) {
			return
		}
		team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, conversation.TenantID)
		if team != nil && team.Status == enums.StatusOk {
			teamIDs = append(teamIDs, teamID)
		}
	}
	appendTeam(conversation.CurrentTeamID)
	if route != nil && route.TenantID == conversation.TenantID {
		if route.WxWorkInstanceID > 0 {
			if instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(sqls.DB(), route.WxWorkInstanceID, conversation.TenantID); instance != nil {
				appendTeam(instance.AgentTeamID)
			}
		}
		if route.StoreID > 0 {
			if binding := repositories.StoreStaffBindingRepository.TakeInTenant(sqls.DB(), conversation.TenantID, "store_id = ? AND status = ?", route.StoreID, enums.StatusOk); binding != nil {
				appendTeam(binding.AgentTeamID)
			}
		}
		if len(teamIDs) == 0 {
			for _, team := range AgentTeamService.Find(sqls.NewCnd().Eq("tenant_id", conversation.TenantID).Eq("status", enums.StatusOk).Asc("id")) {
				if (route.StoreID > 0 && slices.Contains(utils.SplitInt64s(team.StoreScopeIDs), route.StoreID)) ||
					(route.WxWorkInstanceID > 0 && slices.Contains(utils.SplitInt64s(team.WxWorkInstanceScopeIDs), route.WxWorkInstanceID)) {
					appendTeam(team.ID)
					break
				}
			}
		}
	}
	if len(teamIDs) == 0 && aiAgent != nil && aiAgent.TenantID == conversation.TenantID {
		for _, teamID := range utils.SplitInt64s(aiAgent.TeamIDs) {
			appendTeam(teamID)
		}
	}
	return teamIDs
}

func (s *conversationDispatchService) filterAutomaticTeamIDs(teamIDs []int64, tenantID int64) []int64 {
	ret := make([]int64, 0, len(teamIDs))
	for _, teamID := range teamIDs {
		team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, tenantID)
		if team == nil || team.Status != enums.StatusOk || normalizedDispatchMode(team.DispatchMode) == enums.AgentTeamDispatchModeManual {
			continue
		}
		ret = append(ret, teamID)
	}
	return ret
}

func (s *conversationDispatchService) buildDispatchLoadMap(profiles []models.AgentProfile, schedules map[int64]activeScheduleSelection, tenantID int64) (map[int64]agentDispatchLoadSnapshot, error) {
	return s.buildDispatchLoadMapDB(sqls.DB(), profiles, schedules, tenantID)
}

func (s *conversationDispatchService) buildDispatchLoadMapDB(db *gorm.DB, profiles []models.AgentProfile, schedules map[int64]activeScheduleSelection, tenantID int64) (map[int64]agentDispatchLoadSnapshot, error) {
	loads := make(map[int64]agentDispatchLoadSnapshot, len(profiles))
	if db == nil || len(profiles) == 0 || tenantID <= 0 {
		return loads, nil
	}
	userIDs := make([]int64, 0, len(profiles))
	profileTeamByUser := make(map[int64]int64, len(profiles))
	for _, profile := range profiles {
		userIDs = append(userIDs, profile.UserID)
		profileTeamByUser[profile.UserID] = profile.TeamID
		loads[profile.UserID] = agentDispatchLoadSnapshot{}
	}
	conversations := repositories.ConversationRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("status", enums.IMConversationStatusActive).
		In("current_assignee_id", userIDs))
	conversationIDs := make([]int64, 0, len(conversations))
	for _, conversation := range conversations {
		conversationIDs = append(conversationIDs, conversation.ID)
		load := loads[conversation.CurrentAssigneeID]
		load.activeCount++
		load.weightedOpenLoad += normalizedWorkloadWeight(&conversation)
		loads[conversation.CurrentAssigneeID] = load
	}

	activeAssignmentByConversation := make(map[int64]models.ConversationAssignment, len(conversationIDs))
	if len(conversationIDs) > 0 {
		for _, assignment := range repositories.ConversationAssignmentRepository.Find(db, sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("conversation_id", conversationIDs).
			Eq("status", enums.IMAssignmentStatusActive).
			Desc("created_at")) {
			if _, exists := activeAssignmentByConversation[assignment.ConversationID]; !exists {
				activeAssignmentByConversation[assignment.ConversationID] = assignment
			}
		}
		repliedAfterAssignment := make(map[int64]bool, len(conversationIDs))
		unansweredCustomerMessages := make(map[int64]int, len(conversationIDs))
		oldestUnansweredAt := make(map[int64]time.Time, len(conversationIDs))
		for _, message := range repositories.MessageRepository.Find(db, sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("conversation_id", conversationIDs).
			In("sender_type", []enums.IMSenderType{enums.IMSenderTypeAgent, enums.IMSenderTypeCustomer}).
			Asc("created_at").
			Asc("id")) {
			assignment, exists := activeAssignmentByConversation[message.ConversationID]
			if !exists || message.CreatedAt.Before(assignment.CreatedAt) {
				continue
			}
			switch message.SenderType {
			case enums.IMSenderTypeAgent:
				repliedAfterAssignment[message.ConversationID] = true
				unansweredCustomerMessages[message.ConversationID] = 0
				delete(oldestUnansweredAt, message.ConversationID)
			case enums.IMSenderTypeCustomer:
				if unansweredCustomerMessages[message.ConversationID] == 0 {
					oldestUnansweredAt[message.ConversationID] = message.CreatedAt
				}
				unansweredCustomerMessages[message.ConversationID]++
			}
		}
		now := time.Now()
		for _, conversation := range conversations {
			load := loads[conversation.CurrentAssigneeID]
			assignment, exists := activeAssignmentByConversation[conversation.ID]
			if !exists || !repliedAfterAssignment[conversation.ID] {
				load.pendingFirstReply++
			}
			if exists && assignment.CreatedAt.After(load.lastAssignedAt) {
				load.lastAssignedAt = assignment.CreatedAt
			}
			unansweredCount := unansweredCustomerMessages[conversation.ID]
			route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, tenantID)
			if unansweredCount > 0 || (route != nil && route.NeedHumanFollowUp) {
				load.pendingReplyCount++
				load.weightedOpenLoad += dispatchBacklogPressure(unansweredCount, oldestUnansweredAt[conversation.ID], now)
			}
			loads[conversation.CurrentAssigneeID] = load
		}
	}

	minimumShiftStart := time.Time{}
	for _, selection := range schedules {
		if minimumShiftStart.IsZero() || selection.StartAt.Before(minimumShiftStart) {
			minimumShiftStart = selection.StartAt
		}
	}
	if !minimumShiftStart.IsZero() {
		for _, assignment := range repositories.ConversationAssignmentRepository.Find(db, sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("to_user_id", userIDs).
			Gte("created_at", minimumShiftStart).
			Asc("created_at")) {
			selection, exists := schedules[profileTeamByUser[assignment.ToUserID]]
			if !exists || assignment.CreatedAt.Before(selection.StartAt) {
				continue
			}
			weight := assignment.WorkloadWeight
			if weight <= 0 {
				weight = 1
			}
			load := loads[assignment.ToUserID]
			load.shiftAssignedWeight += weight
			if assignment.CreatedAt.After(load.lastAssignedAt) {
				load.lastAssignedAt = assignment.CreatedAt
			}
			loads[assignment.ToUserID] = load
		}
	}
	return loads, nil
}

func dispatchBacklogPressure(unansweredCount int, oldestUnansweredAt, now time.Time) int {
	pressure := min(max(unansweredCount-1, 0), 2)
	if oldestUnansweredAt.IsZero() || !now.After(oldestUnansweredAt) {
		return pressure
	}
	waiting := now.Sub(oldestUnansweredAt)
	if waiting >= 10*time.Minute {
		pressure++
	}
	if waiting >= 30*time.Minute {
		pressure++
	}
	return pressure
}

func (s *conversationDispatchService) findActiveConversationCountMapDB(db *gorm.DB, userIDs []int64, tenantID int64) (map[int64]int, error) {
	ret := make(map[int64]int, len(userIDs))
	if len(userIDs) == 0 || tenantID <= 0 {
		return ret, nil
	}
	rows := make([]agentActiveConversationCount, 0)
	if err := db.Model(&models.Conversation{}).
		Select("current_assignee_id, COUNT(1) AS active_count").
		Where("tenant_id = ? AND status = ? AND current_assignee_id IN ?", tenantID, enums.IMConversationStatusActive, userIDs).
		Group("current_assignee_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		ret[row.CurrentAssigneeID] = row.ActiveCount
	}
	return ret, nil
}
