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
	shiftWorkloadWeight int
	lastAssignedAt      time.Time
}

const pendingDispatchOldestQuotaDivisor = 5

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
	return s.prioritizePendingConversationWindow(conversations, now, len(conversations))
}

func (s *conversationDispatchService) prioritizePendingConversationWindow(conversations []models.Conversation, now time.Time, selectionLimit int) []models.Conversation {
	ret := append([]models.Conversation(nil), conversations...)
	if len(ret) < 2 {
		return ret
	}
	priorities := make(map[int64]int, len(ret))
	conversationIDs := make([]int64, 0, len(ret))
	for i := range ret {
		conversationIDs = append(conversationIDs, ret[i].ID)
	}
	routes := repositories.ConversationRouteStateRepository.Find(sqls.DB(), sqls.NewCnd().In("conversation_id", uniquePositiveInt64s(conversationIDs)))
	routeByConversationID := make(map[int64]*models.ConversationRouteState, len(routes))
	for i := range routes {
		routeByConversationID[routes[i].ConversationID] = &routes[i]
	}
	for i := range ret {
		route := routeByConversationID[ret[i].ID]
		if route != nil && route.TenantID != ret[i].TenantID {
			route = nil
		}
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
	if selectionLimit <= 0 || selectionLimit > len(ret) {
		selectionLimit = len(ret)
	}
	oldestQuota := selectionLimit / pendingDispatchOldestQuotaDivisor
	if oldestQuota < 1 {
		oldestQuota = 1
	}
	prioritySlots := selectionLimit - oldestQuota
	selected := make(map[int64]struct{}, selectionLimit)
	ordered := make([]models.Conversation, 0, len(ret))
	for _, conversation := range ret {
		if len(ordered) >= prioritySlots {
			break
		}
		ordered = append(ordered, conversation)
		selected[conversation.ID] = struct{}{}
	}

	oldest := append([]models.Conversation(nil), ret...)
	slices.SortStableFunc(oldest, func(a, b models.Conversation) int {
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
	for _, conversation := range oldest {
		if len(ordered) >= selectionLimit {
			break
		}
		if _, exists := selected[conversation.ID]; exists {
			continue
		}
		ordered = append(ordered, conversation)
		selected[conversation.ID] = struct{}{}
	}
	for _, conversation := range ret {
		if _, exists := selected[conversation.ID]; exists {
			continue
		}
		ordered = append(ordered, conversation)
	}
	return ordered
}

func pendingConversationAt(conversation models.Conversation) time.Time {
	if conversation.HandoffAt != nil {
		return *conversation.HandoffAt
	}
	return conversation.CreatedAt
}

func (s *conversationDispatchService) resolveDispatchTeamIDs(conversation *models.Conversation, route *models.ConversationRouteState) []int64 {
	return s.resolveDispatchTeamIDsDB(sqls.DB(), conversation, route)
}

func (s *conversationDispatchService) resolveDispatchTeamIDsDB(db *gorm.DB, conversation *models.Conversation, route *models.ConversationRouteState) []int64 {
	if db == nil || conversation == nil || conversation.TenantID <= 0 {
		return nil
	}
	teamIDs := make([]int64, 0, 2)
	appendTeam := func(teamID int64) {
		if teamID <= 0 || slices.Contains(teamIDs, teamID) {
			return
		}
		team := repositories.AgentTeamRepository.GetInTenant(db, teamID, conversation.TenantID)
		if team != nil && team.Status == enums.StatusOk {
			teamIDs = append(teamIDs, teamID)
		}
	}
	if route != nil && route.TenantID == conversation.TenantID {
		if route.WxWorkInstanceID > 0 {
			if instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, route.WxWorkInstanceID, conversation.TenantID); instance != nil {
				storeMatches := route.StoreID <= 0 || instance.StoreID == route.StoreID
				bindingMatches := route.StoreStaffBindingID <= 0 || instance.StoreStaffBindingID == route.StoreStaffBindingID
				if storeMatches && bindingMatches {
					appendTeam(instance.AgentTeamID)
				}
			}
		}
		if route.StoreStaffBindingID > 0 {
			if binding := repositories.StoreStaffBindingRepository.GetInTenant(db, route.StoreStaffBindingID, conversation.TenantID); binding != nil &&
				binding.Status == enums.StatusOk && (route.StoreID <= 0 || binding.StoreID == route.StoreID) {
				appendTeam(binding.AgentTeamID)
			}
		}
		if len(teamIDs) == 0 {
			matchedTeamIDs := make([]int64, 0, 2)
			for _, team := range repositories.AgentTeamRepository.Find(db, sqls.NewCnd().Eq("tenant_id", conversation.TenantID).Eq("status", enums.StatusOk).Asc("id")) {
				if (route.StoreID > 0 && slices.Contains(utils.SplitInt64s(team.StoreScopeIDs), route.StoreID)) ||
					(route.WxWorkInstanceID > 0 && slices.Contains(utils.SplitInt64s(team.WxWorkInstanceScopeIDs), route.WxWorkInstanceID)) {
					matchedTeamIDs = append(matchedTeamIDs, team.ID)
				}
			}
			if len(matchedTeamIDs) == 1 {
				appendTeam(matchedTeamIDs[0])
			}
		}
	}
	if len(teamIDs) > 1 {
		return nil
	}
	if len(teamIDs) == 0 {
		appendTeam(conversation.CurrentTeamID)
	}
	if len(teamIDs) == 0 {
		defaultTeams := repositories.AgentTeamRepository.Find(db, sqls.NewCnd().
			Eq("tenant_id", conversation.TenantID).
			Eq("is_default", true).
			Eq("status", enums.StatusOk).
			Asc("id"))
		if len(defaultTeams) == 1 {
			appendTeam(defaultTeams[0].ID)
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
	for _, profile := range profiles {
		userIDs = append(userIDs, profile.UserID)
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
		messageStates, err := repositories.MessageRepository.FindActiveAssignmentMessageStates(db, tenantID, userIDs)
		if err != nil {
			return nil, err
		}
		messageStateByConversation := make(map[int64]repositories.ActiveAssignmentMessageStateRow, len(messageStates))
		oldestUnansweredMessageIDs := make([]int64, 0, len(messageStates))
		for _, state := range messageStates {
			messageStateByConversation[state.ConversationID] = state
			if state.OldestUnansweredMessageID > 0 {
				oldestUnansweredMessageIDs = append(oldestUnansweredMessageIDs, state.OldestUnansweredMessageID)
			}
		}
		oldestUnansweredAtByMessageID := make(map[int64]time.Time, len(oldestUnansweredMessageIDs))
		if len(oldestUnansweredMessageIDs) > 0 {
			for _, message := range repositories.MessageRepository.Find(db, sqls.NewCnd().
				Eq("tenant_id", tenantID).
				In("id", uniquePositiveInt64s(oldestUnansweredMessageIDs))) {
				oldestUnansweredAtByMessageID[message.ID] = message.CreatedAt
			}
		}
		followupByConversation := make(map[int64]bool, len(conversationIDs))
		for _, route := range repositories.ConversationRouteStateRepository.Find(db, sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("conversation_id", conversationIDs).
			Eq("need_human_follow_up", true)) {
			followupByConversation[route.ConversationID] = true
		}
		now := time.Now()
		for _, conversation := range conversations {
			load := loads[conversation.CurrentAssigneeID]
			assignment, exists := activeAssignmentByConversation[conversation.ID]
			state := messageStateByConversation[conversation.ID]
			if !exists || state.LastAssignedReplySeq <= 0 {
				load.pendingFirstReply++
			}
			if exists && assignment.CreatedAt.After(load.lastAssignedAt) {
				load.lastAssignedAt = assignment.CreatedAt
			}
			unansweredCount := state.UnansweredCustomerCount
			if unansweredCount > 0 || followupByConversation[conversation.ID] {
				load.pendingReplyCount++
				oldestUnansweredAt := oldestUnansweredAtByMessageID[state.OldestUnansweredMessageID]
				load.weightedOpenLoad += dispatchBacklogPressure(unansweredCount, oldestUnansweredAt, now)
			}
			loads[conversation.CurrentAssigneeID] = load
		}
	}

	minimumShiftStart := time.Time{}
	shiftStartByUserID := make(map[int64]time.Time, len(profiles))
	membersBySquad, teamBySquad := AgentTeamSquadService.ActiveMemberProfileSetDB(db, activeScheduleSquadIDs(schedules), tenantID)
	for i := range profiles {
		selection, exists := schedules[profiles[i].TeamID]
		if !exists {
			continue
		}
		window, matched := matchingActiveScheduleSnapshot(&profiles[i], selection, membersBySquad, teamBySquad)
		if !matched || window.StartAt.IsZero() {
			continue
		}
		shiftStartByUserID[profiles[i].UserID] = window.StartAt
		if minimumShiftStart.IsZero() || window.StartAt.Before(minimumShiftStart) {
			minimumShiftStart = window.StartAt
		}
	}
	if !minimumShiftStart.IsZero() {
		assignments, err := repositories.ConversationAssignmentRepository.FindShiftWorkAssignments(db, tenantID, userIDs, minimumShiftStart)
		if err != nil {
			return nil, err
		}
		for _, assignment := range assignments {
			shiftStart, exists := shiftStartByUserID[assignment.ToUserID]
			if !exists || assignment.CreatedAt.Before(shiftStart) {
				continue
			}
			weight := assignment.WorkloadWeight
			if weight <= 0 {
				weight = 1
			}
			load := loads[assignment.ToUserID]
			load.shiftWorkloadWeight += weight
			if assignment.CreatedAt.After(load.lastAssignedAt) {
				load.lastAssignedAt = assignment.CreatedAt
			}
			loads[assignment.ToUserID] = load
		}
	}
	return loads, nil
}

func normalizedDispatchPressure(load agentDispatchLoadSnapshot, maxConcurrentCount int) float64 {
	capacity := float64(maxConcurrentCount)
	if capacity <= 0 {
		capacity = 1
	}
	return (float64(load.weightedOpenLoad) + float64(load.pendingFirstReply)*0.75 + float64(load.pendingReplyCount)*0.5) / capacity
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
