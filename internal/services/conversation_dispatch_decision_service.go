package services

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	dispatchFairNormalizedLoadTolerance = 0.20
	dispatchFairWeightedLoadTolerance   = 1
	dispatchContinuityLookback          = 30 * 24 * time.Hour
)

type dispatchDecision struct {
	candidate             dispatchCandidate
	reason                string
	workloadWeight        int
	priority              int
	expectedLastMessageID int64
}

func (s *conversationDispatchService) selectDispatchDecision(conversation *models.Conversation, route *models.ConversationRouteState, candidates []dispatchCandidate) dispatchDecision {
	weight, priority := s.ruleDispatchAssessment(conversation, route)
	shortlist := fairRuleCandidateBand(candidates)
	continuityUsers := s.findContinuityUsers(conversation, shortlist)
	slices.SortStableFunc(shortlist, func(a, b dispatchCandidate) int {
		return compareRuleCandidates(a, b, continuityUsers)
	})
	selected := candidates[0]
	if len(shortlist) > 0 {
		selected = shortlist[0]
	}
	decision := dispatchDecision{
		candidate:             selected,
		reason:                buildRuleDispatchReason(selected, continuityUsers[selected.profile.UserID]),
		workloadWeight:        weight,
		priority:              priority,
		expectedLastMessageID: conversation.LastMessageID,
	}
	return decision
}

func (s *conversationDispatchService) findContinuityUsers(conversation *models.Conversation, candidates []dispatchCandidate) map[int64]bool {
	ret := make(map[int64]bool, len(candidates))
	if conversation == nil || conversation.CustomerID <= 0 || len(candidates) == 0 {
		return ret
	}
	userIDs := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		userIDs = append(userIDs, candidate.profile.UserID)
	}
	conversations := ConversationService.Find(sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("customer_id", conversation.CustomerID).
		Where("id <> ?", conversation.ID).
		Gte("last_active_at", time.Now().Add(-dispatchContinuityLookback)).
		Desc("last_active_at").
		Limit(20))
	conversations = append(conversations, *conversation)
	conversationIDs := make([]int64, 0, len(conversations))
	conversationByID := make(map[int64]models.Conversation, len(conversations))
	for _, item := range conversations {
		conversationIDs = append(conversationIDs, item.ID)
		conversationByID[item.ID] = item
	}
	if len(conversationIDs) == 0 {
		return ret
	}
	currentRoute := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	currentSessionNo := 1
	if currentRoute != nil && currentRoute.SessionNo > 0 {
		currentSessionNo = currentRoute.SessionNo
	}
	routes := repositories.ConversationRouteStateRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		In("conversation_id", uniquePositiveInt64s(conversationIDs)))
	routeByConversationID := make(map[int64]*models.ConversationRouteState, len(routes))
	for i := range routes {
		routeByConversationID[routes[i].ConversationID] = &routes[i]
	}
	sameSourceConversationIDs := make([]int64, 0, len(conversationIDs))
	for _, conversationID := range conversationIDs {
		item := conversationByID[conversationID]
		if sameDispatchContinuitySource(conversation, currentRoute, &item, routeByConversationID[conversationID]) {
			sameSourceConversationIDs = append(sameSourceConversationIDs, conversationID)
		}
	}
	loadRepliedUsers := func(ids []int64) []int64 {
		if len(ids) == 0 {
			return nil
		}
		repliedUserIDs, err := repositories.ConversationAssignmentRepository.FindSuccessfulRepliedAssigneeIDs(
			sqls.DB(), conversation.TenantID, uniquePositiveInt64s(ids), uniquePositiveInt64s(userIDs), conversation.ID, currentSessionNo,
		)
		if err != nil {
			slog.Warn("load dispatch continuity failed", "tenant_id", conversation.TenantID, "conversation_id", conversation.ID, "error", err)
			return nil
		}
		return repliedUserIDs
	}
	repliedUserIDs := loadRepliedUsers(sameSourceConversationIDs)
	if len(repliedUserIDs) == 0 {
		repliedUserIDs = loadRepliedUsers(conversationIDs)
	}
	for _, userID := range repliedUserIDs {
		ret[userID] = true
	}
	return ret
}

func sameDispatchContinuitySource(
	currentConversation *models.Conversation,
	currentRoute *models.ConversationRouteState,
	historicalConversation *models.Conversation,
	historicalRoute *models.ConversationRouteState,
) bool {
	if currentConversation == nil || historicalConversation == nil {
		return false
	}
	if currentRoute != nil && historicalRoute != nil {
		if currentRoute.WxWorkInstanceID > 0 {
			return currentRoute.WxWorkInstanceID == historicalRoute.WxWorkInstanceID
		}
		if currentRoute.StoreID > 0 {
			return currentRoute.StoreID == historicalRoute.StoreID
		}
	}
	return currentConversation.ChannelID > 0 && currentConversation.ChannelID == historicalConversation.ChannelID
}

func (s *conversationDispatchService) ruleDispatchAssessment(conversation *models.Conversation, route *models.ConversationRouteState) (int, int) {
	return s.ruleDispatchAssessmentAt(conversation, route, time.Now())
}

func (s *conversationDispatchService) ruleDispatchAssessmentAt(conversation *models.Conversation, route *models.ConversationRouteState, now time.Time) (int, int) {
	if conversation == nil {
		return 1, 0
	}
	weight := normalizedWorkloadWeight(conversation)
	priority := normalizedConversationPriority(conversation)
	reason := conversation.HandoffReason + " " + conversation.LastMessageSummary
	if route != nil {
		reason += " " + route.HandoffReason
	}
	if isSafetyHandoffReason(reason) {
		priority = max(priority, 100)
		weight = max(weight, 5)
	}
	if conversation.HandoffAt != nil {
		waitSeconds := int(now.Sub(*conversation.HandoffAt).Seconds())
		queueTargetSeconds := ServiceAnalyticsService.GetPolicy(conversation.TenantID).QueueTargetSeconds
		if queueTargetSeconds <= 0 {
			queueTargetSeconds = 60
		}
		switch {
		case waitSeconds >= queueTargetSeconds*4:
			priority = max(priority, 95)
		case waitSeconds >= queueTargetSeconds*2:
			priority = max(priority, 80)
		case waitSeconds >= queueTargetSeconds:
			priority = max(priority, 60)
		case waitSeconds*100 >= queueTargetSeconds*80:
			priority = max(priority, 40)
		}
	}
	if weight > 5 {
		weight = 5
	}
	if priority > 100 {
		priority = 100
	}
	return weight, priority
}

func violatesDispatchFairnessGuard(selected, fairest dispatchCandidate) bool {
	return selected.normalizedLoad > fairest.normalizedLoad+dispatchFairNormalizedLoadTolerance ||
		selected.weightedOpenLoad > fairest.weightedOpenLoad+dispatchFairWeightedLoadTolerance
}

func fairRuleCandidateBand(candidates []dispatchCandidate) []dispatchCandidate {
	if len(candidates) == 0 {
		return nil
	}
	fairest := candidates[0]
	shortlist := make([]dispatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if violatesDispatchFairnessGuard(candidate, fairest) {
			continue
		}
		shortlist = append(shortlist, candidate)
	}
	return shortlist
}

func compareRuleCandidates(a, b dispatchCandidate, continuityUsers map[int64]bool) int {
	aDebt := dispatchShiftDebt(a)
	bDebt := dispatchShiftDebt(b)
	switch {
	case aDebt < bDebt:
		return -1
	case aDebt > bDebt:
		return 1
	}
	aContinuity := continuityUsers[a.profile.UserID]
	bContinuity := continuityUsers[b.profile.UserID]
	if aContinuity != bContinuity {
		if aContinuity {
			return -1
		}
		return 1
	}
	if a.pendingFirstReply != b.pendingFirstReply {
		return a.pendingFirstReply - b.pendingFirstReply
	}
	if a.pendingReplyCount != b.pendingReplyCount {
		return a.pendingReplyCount - b.pendingReplyCount
	}
	if a.lastAssignedAt.Before(b.lastAssignedAt) {
		return -1
	}
	if a.lastAssignedAt.After(b.lastAssignedAt) {
		return 1
	}
	if a.profile.PriorityLevel != b.profile.PriorityLevel {
		return b.profile.PriorityLevel - a.profile.PriorityLevel
	}
	switch {
	case a.profile.UserID < b.profile.UserID:
		return -1
	case a.profile.UserID > b.profile.UserID:
		return 1
	default:
		return 0
	}
}

func dispatchShiftDebt(candidate dispatchCandidate) float64 {
	capacity := candidate.profile.MaxConcurrentCount
	if capacity <= 0 {
		capacity = 1
	}
	return float64(candidate.shiftWorkloadWeight) / float64(capacity)
}

func buildRuleDispatchReason(candidate dispatchCandidate, continuity bool) string {
	reason := fmt.Sprintf(
		"规则均衡：实时压力 %.2f，加权负载 %d，待首响 %d，待回复 %d，本班累计权重 %d",
		candidate.normalizedLoad,
		candidate.weightedOpenLoad,
		candidate.pendingFirstReply,
		candidate.pendingReplyCount,
		candidate.shiftWorkloadWeight,
	)
	if continuity {
		reason += "，且近期接待过该客户"
	}
	return compactDispatchReason(reason)
}

func compactDispatchReason(value string) string {
	return compactDispatchText(value, 255)
}

func compactDispatchText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}
