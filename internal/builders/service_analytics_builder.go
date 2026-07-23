package builders

import (
	"encoding/json"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"
)

func BuildServiceAnalyticsOverview(aggregate *services.ServiceAnalyticsOverview) *response.ServiceAnalyticsOverviewResponse {
	if aggregate == nil {
		return nil
	}
	ret := &response.ServiceAnalyticsOverviewResponse{
		StartAt: utils.FormatTime(aggregate.StartAt), EndAt: utils.FormatTime(aggregate.EndAt), GeneratedAt: utils.FormatTime(aggregate.GeneratedAt),
		Trend:                       make([]response.ServiceAnalyticsTrendResponse, 0, len(aggregate.Trend)),
		FirstReplyDistribution:      make([]response.ServiceAnalyticsDistributionResponse, 0, len(aggregate.FirstReplyDistribution)),
		ResponseDistribution:        make([]response.ServiceAnalyticsDistributionResponse, 0, len(aggregate.ResponseDistribution)),
		SessionDurationDistribution: make([]response.ServiceAnalyticsDistributionResponse, 0, len(aggregate.SessionDurationDistribution)),
		Agents:                      make([]response.ServiceAnalyticsAgentResponse, 0, len(aggregate.Agents)),
		Sources:                     make([]response.ServiceAnalyticsSourceResponse, 0, len(aggregate.Sources)),
		Summary: response.ServiceAnalyticsSummaryResponse{
			SessionCount: aggregate.Summary.SessionCount, UniqueCustomerCount: aggregate.Summary.UniqueCustomerCount, ClosedSessionCount: aggregate.Summary.ClosedSessionCount,
			HumanQueueCount: aggregate.Summary.HumanQueueCount, AssignedCount: aggregate.Summary.AssignedCount,
			HumanRepliedCount: aggregate.Summary.HumanRepliedCount, UnansweredCount: aggregate.Summary.UnansweredCount,
			QueueFailureCount: aggregate.Summary.QueueFailureCount, TransferSessionCount: aggregate.Summary.TransferSessionCount,
			RepeatConsultationCount: aggregate.Summary.RepeatConsultationCount, TotalMessageCount: aggregate.Summary.TotalMessageCount,
			CustomerMessageCount: aggregate.Summary.CustomerMessageCount, AIMessageCount: aggregate.Summary.AIMessageCount,
			HumanMessageCount:    aggregate.Summary.HumanMessageCount,
			AssignmentAccessRate: aggregate.Summary.AssignmentAccessRate, EffectiveAccessRate: aggregate.Summary.EffectiveAccessRate,
			TransferRate: aggregate.Summary.TransferRate, RepeatConsultationRate: aggregate.Summary.RepeatConsultationRate,
			AverageQueueSeconds: aggregate.Summary.AverageQueueSeconds, AverageFirstReplySeconds: aggregate.Summary.AverageFirstReplySeconds,
			P50QueueSeconds: aggregate.Summary.P50QueueSeconds, P90QueueSeconds: aggregate.Summary.P90QueueSeconds,
			P50FirstReplySeconds: aggregate.Summary.P50FirstReplySeconds, P90FirstReplySeconds: aggregate.Summary.P90FirstReplySeconds,
			AverageResponseSeconds: aggregate.Summary.AverageResponseSeconds, AverageHumanWaitSeconds: aggregate.Summary.AverageHumanWaitSeconds,
			P50ResponseSeconds: aggregate.Summary.P50ResponseSeconds, P90ResponseSeconds: aggregate.Summary.P90ResponseSeconds,
			P50HumanWaitSeconds: aggregate.Summary.P50HumanWaitSeconds, P90HumanWaitSeconds: aggregate.Summary.P90HumanWaitSeconds,
			AverageSessionSeconds: aggregate.Summary.AverageSessionSeconds, AverageMessagesPerSession: aggregate.Summary.AverageMessagesPerSession,
			P50SessionSeconds: aggregate.Summary.P50SessionSeconds, P90SessionSeconds: aggregate.Summary.P90SessionSeconds,
			QueueSLARate: aggregate.Summary.QueueSLARate, FirstReplySLARate: aggregate.Summary.FirstReplySLARate,
			ResponseSLARate: aggregate.Summary.ResponseSLARate, QualityInspectableCount: aggregate.Summary.QualityInspectableCount,
			QualityInspectionCount: aggregate.Summary.QualityInspectionCount, QualityPendingCount: aggregate.Summary.QualityPendingCount,
			QualityPassedCount: aggregate.Summary.QualityPassedCount, QualityFailedCount: aggregate.Summary.QualityFailedCount,
			QualityCoverageRate: aggregate.Summary.QualityCoverageRate, QualityPassRate: aggregate.Summary.QualityPassRate,
			AverageQualityScore:         aggregate.Summary.AverageQualityScore,
			EvaluationInviteCount:       aggregate.Summary.EvaluationInviteCount,
			EvaluationSubmittedCount:    aggregate.Summary.EvaluationSubmittedCount,
			SatisfiedCount:              aggregate.Summary.SatisfiedCount,
			EvaluationParticipationRate: aggregate.Summary.EvaluationParticipationRate,
			SatisfactionRate:            aggregate.Summary.SatisfactionRate,
			AverageSatisfaction:         aggregate.Summary.AverageSatisfaction,
			ExactSessionCount:           aggregate.Summary.ExactSessionCount,
			EstimatedSessionCount:       aggregate.Summary.EstimatedSessionCount,
			IncompleteSessionCount:      aggregate.Summary.IncompleteSessionCount,
		},
		Realtime: response.ServiceAnalyticsRealtimeResponse{
			OpenSessionCount: aggregate.Realtime.OpenSessionCount, AIActiveCount: aggregate.Realtime.AIActiveCount,
			QueueingCount: aggregate.Realtime.QueueingCount, AssignedActiveCount: aggregate.Realtime.AssignedActiveCount,
			WaitingReplyCount: aggregate.Realtime.WaitingReplyCount, LongestQueueSeconds: aggregate.Realtime.LongestQueueSeconds,
			QueueSLAAlertCount: aggregate.Realtime.QueueSLAAlertCount, OnlineAgentCount: aggregate.Realtime.OnlineAgentCount,
			IdleAgentCount: aggregate.Realtime.IdleAgentCount, BusyAgentCount: aggregate.Realtime.BusyAgentCount,
			BreakAgentCount: aggregate.Realtime.BreakAgentCount, OfflineAgentCount: aggregate.Realtime.OfflineAgentCount,
			AvailableCapacity: aggregate.Realtime.AvailableCapacity,
			TodaySessionCount: aggregate.Realtime.TodaySessionCount, TodayQueueCount: aggregate.Realtime.TodayQueueCount,
			TodayAssignedCount: aggregate.Realtime.TodayAssignedCount, TodayHumanRepliedCount: aggregate.Realtime.TodayHumanRepliedCount,
			TodayTransferCount:     aggregate.Realtime.TodayTransferCount,
			TodayQueueFailureCount: aggregate.Realtime.TodayQueueFailureCount, TodayMessageCount: aggregate.Realtime.TodayMessageCount,
			TodayAverageQueueSeconds:      aggregate.Realtime.TodayAverageQueueSeconds,
			TodayAverageFirstReplySeconds: aggregate.Realtime.TodayAverageFirstReplySeconds,
		},
		Dispatch: response.ServiceAnalyticsDispatchResponse{
			DecisionCount: aggregate.Dispatch.DecisionCount, AutoCount: aggregate.Dispatch.AutoCount, ManualCount: aggregate.Dispatch.ManualCount,
			SelectedCount: aggregate.Dispatch.SelectedCount, RuleCount: aggregate.Dispatch.RuleCount, ModelCount: aggregate.Dispatch.ModelCount,
			HybridCount: aggregate.Dispatch.HybridCount, FallbackCount: aggregate.Dispatch.FallbackCount, FailedCount: aggregate.Dispatch.FailedCount,
			StaleCount: aggregate.Dispatch.StaleCount, OverrideCount: aggregate.Dispatch.OverrideCount, TransferCount: aggregate.Dispatch.TransferCount,
			AutoRate: aggregate.Dispatch.AutoRate, AverageDecisionLatencyMillis: aggregate.Dispatch.AverageDecisionLatencyMillis,
		},
	}
	for _, item := range aggregate.Trend {
		ret.Trend = append(ret.Trend, response.ServiceAnalyticsTrendResponse{
			Date: item.Date, Sessions: item.Sessions, HumanQueues: item.HumanQueues, HumanReplies: item.HumanReplies,
			Messages: item.Messages, AverageQueue: item.AverageQueue, AverageFirstReply: item.AverageFirstReply,
			AverageResponse: item.AverageResponse, AverageSession: item.AverageSession,
		})
	}
	for _, item := range aggregate.FirstReplyDistribution {
		ret.FirstReplyDistribution = append(ret.FirstReplyDistribution, response.ServiceAnalyticsDistributionResponse{Key: item.Key, Label: item.Label, Count: item.Count, Rate: item.Rate})
	}
	for _, item := range aggregate.ResponseDistribution {
		ret.ResponseDistribution = append(ret.ResponseDistribution, response.ServiceAnalyticsDistributionResponse{Key: item.Key, Label: item.Label, Count: item.Count, Rate: item.Rate})
	}
	for _, item := range aggregate.SessionDurationDistribution {
		ret.SessionDurationDistribution = append(ret.SessionDurationDistribution, response.ServiceAnalyticsDistributionResponse{Key: item.Key, Label: item.Label, Count: item.Count, Rate: item.Rate})
	}
	for _, item := range aggregate.Agents {
		ret.Agents = append(ret.Agents, response.ServiceAnalyticsAgentResponse{
			AgentID: item.AgentID, AgentName: item.AgentName, TeamID: item.TeamID, TeamName: item.TeamName, AssignedCount: item.AssignedCount,
			SquadNames: item.SquadNames, CurrentStatus: item.CurrentStatus, CurrentActiveCount: item.CurrentActiveCount,
			MaxConcurrentCount: item.MaxConcurrentCount, RepliedCount: item.RepliedCount, UnansweredCount: item.UnansweredCount,
			HumanMessageCount: item.HumanMessageCount, ResponseCount: item.ResponseCount, ServiceSeconds: item.ServiceSeconds,
			AverageFirstReplySeconds: item.AverageFirstReplySeconds, AverageResponseSeconds: item.AverageResponseSeconds,
			P50FirstReplySeconds: item.P50FirstReplySeconds, P90FirstReplySeconds: item.P90FirstReplySeconds,
			P50ResponseSeconds: item.P50ResponseSeconds, P90ResponseSeconds: item.P90ResponseSeconds,
			ResponseSLARate: item.ResponseSLARate, OnlineSeconds: item.OnlineSeconds, IdleSeconds: item.IdleSeconds,
			BusySeconds: item.BusySeconds, BreakSeconds: item.BreakSeconds,
			FirstOnlineAt: utils.FormatTimePtr(item.FirstOnlineAt), LastOnlineAt: utils.FormatTimePtr(item.LastOnlineAt),
			UtilizationRate: item.UtilizationRate, QualityInspectableCount: item.QualityInspectableCount,
			QualityInspectionCount: item.QualityInspectionCount, QualityPendingCount: item.QualityPendingCount,
			QualityPassedCount: item.QualityPassedCount, QualityFailedCount: item.QualityFailedCount,
			QualityPassRate: item.QualityPassRate, AverageQualityScore: item.AverageQualityScore,
			EvaluationInviteCount: item.EvaluationInviteCount, EvaluationSubmittedCount: item.EvaluationSubmittedCount,
			SatisfiedCount: item.SatisfiedCount, EvaluationParticipationRate: item.EvaluationParticipationRate,
			SatisfactionRate: item.SatisfactionRate, AverageSatisfaction: item.AverageSatisfaction,
		})
	}
	for _, item := range aggregate.Sources {
		ret.Sources = append(ret.Sources, response.ServiceAnalyticsSourceResponse{
			StoreID: item.StoreID, StoreName: item.StoreName, WxWorkInstanceID: item.WxWorkInstanceID,
			WxWorkEmployeeName: item.WxWorkEmployeeName, SessionCount: item.SessionCount, HumanQueueCount: item.HumanQueueCount,
			HumanRepliedCount: item.HumanRepliedCount, MessageCount: item.MessageCount, AverageFirstReply: item.AverageFirstReply,
			EffectiveAccessRate: item.EffectiveAccessRate, QualityInspectableCount: item.QualityInspectableCount,
			QualityInspectionCount: item.QualityInspectionCount, QualityPassedCount: item.QualityPassedCount,
			QualityCoverageRate: item.QualityCoverageRate, QualityPassRate: item.QualityPassRate,
			AverageQualityScore: item.AverageQualityScore, EvaluationInviteCount: item.EvaluationInviteCount,
			EvaluationSubmittedCount: item.EvaluationSubmittedCount, SatisfiedCount: item.SatisfiedCount,
			EvaluationParticipationRate: item.EvaluationParticipationRate, SatisfactionRate: item.SatisfactionRate,
			AverageSatisfaction: item.AverageSatisfaction,
		})
	}
	return ret
}

func BuildServiceAnalyticsDimensions(aggregate *services.ServiceAnalyticsDimensions) *response.ServiceAnalyticsDimensionsResponse {
	if aggregate == nil {
		return nil
	}
	ret := &response.ServiceAnalyticsDimensionsResponse{}
	build := func(items []services.ServiceAnalyticsDimensionItem) []response.ServiceAnalyticsDimensionItemResponse {
		results := make([]response.ServiceAnalyticsDimensionItemResponse, 0, len(items))
		for _, item := range items {
			results = append(results, response.ServiceAnalyticsDimensionItemResponse{ID: item.ID, Name: item.Name, ParentID: item.ParentID})
		}
		return results
	}
	ret.Teams = build(aggregate.Teams)
	ret.Squads = build(aggregate.Squads)
	ret.Agents = build(aggregate.Agents)
	ret.Channels = build(aggregate.Channels)
	ret.Stores = build(aggregate.Stores)
	ret.WxWorkInstances = build(aggregate.WxWorkInstances)
	return ret
}

func BuildQualityTemplate(aggregate services.QualityTemplateAggregate) response.QualityTemplateResponse {
	items := make([]response.QualityTemplateItemResponse, 0, len(aggregate.Items))
	for _, item := range aggregate.Items {
		items = append(items, response.QualityTemplateItemResponse{
			ID: item.ID, Code: item.Code, Name: item.Name, Description: item.Description,
			RuleType: string(item.RuleType), MetricCode: item.MetricCode, MaxScore: item.MaxScore,
			Required: item.Required, HardFail: item.HardFail, SortNo: item.SortNo,
		})
	}
	return response.QualityTemplateResponse{
		ID: aggregate.Template.ID, Name: aggregate.Template.Name, Description: aggregate.Template.Description, TotalScore: aggregate.Template.TotalScore,
		PassScore: aggregate.Template.PassScore, Version: aggregate.Template.Version, IsDefault: aggregate.Template.IsDefault, Items: items,
	}
}

func BuildQualityInspection(aggregate *services.QualityInspectionAggregate) *response.QualityInspectionResponse {
	if aggregate == nil {
		return nil
	}
	items := make([]response.QualityInspectionItemResponse, 0, len(aggregate.Items))
	for _, item := range aggregate.Items {
		messageIDs := []int64{}
		_ = json.Unmarshal([]byte(item.MessageIDsJSON), &messageIDs)
		items = append(items, response.QualityInspectionItemResponse{
			TemplateItemID: item.TemplateItemID, ItemCode: item.ItemCode, ItemName: item.ItemName,
			RuleType: string(item.RuleType), MaxScore: item.MaxScore, Score: item.Score,
			Passed: item.Passed, HardFailed: item.HardFailed, MetricValue: item.MetricValue,
			Evidence: item.Evidence, MessageIDs: messageIDs, Comment: item.Comment,
		})
	}
	return &response.QualityInspectionResponse{
		ID: aggregate.Inspection.ID, ConversationID: aggregate.Inspection.ConversationID, SessionNo: aggregate.Inspection.SessionNo,
		AssignmentID: aggregate.Inspection.AssignmentID, AgentID: aggregate.Inspection.AgentID, AgentName: userDisplayName(aggregate.Agent),
		TeamID: aggregate.Inspection.TeamID, TeamName: teamDisplayName(aggregate.Team), TemplateID: aggregate.Inspection.TemplateID,
		Status: string(aggregate.Inspection.Status), TotalScore: aggregate.Inspection.TotalScore, MaxScore: aggregate.Inspection.MaxScore,
		Result: string(aggregate.Inspection.Result), HardFailed: aggregate.Inspection.HardFailed,
		Summary: aggregate.Inspection.Summary, InspectedBy: aggregate.Inspection.InspectedBy,
		InspectedAt: utils.FormatTimePtr(aggregate.Inspection.InspectedAt), Items: items,
	}
}

func BuildQualityPoolEntry(aggregate services.QualityPoolAggregate) response.QualityPoolEntryResponse {
	ret := response.QualityPoolEntryResponse{
		AssignmentID: aggregate.Assignment.ID, ConversationID: aggregate.Assignment.ConversationID, SessionNo: aggregate.Assignment.SessionNo,
		AgentID: aggregate.Assignment.ToUserID, AgentName: userDisplayName(aggregate.Agent), TeamName: teamDisplayName(aggregate.Team),
		AssignedAt: utils.FormatTime(aggregate.Assignment.CreatedAt), FinishedAt: utils.FormatTimePtr(aggregate.Assignment.FinishedAt), HumanReplyCount: aggregate.HumanReplies,
		Inspection: BuildQualityInspection(aggregate.Inspection),
	}
	if aggregate.Conversation != nil {
		ret.CustomerName = aggregate.Conversation.CustomerName
	}
	if aggregate.Team != nil {
		ret.TeamID = aggregate.Team.ID
	}
	if aggregate.Route != nil {
		if store := services.StoreService.GetInTenant(aggregate.Route.StoreID, aggregate.Route.TenantID); store != nil {
			ret.StoreName = store.Name
		}
		if instance := services.WxWorkProtocolInstanceService.GetByTenantID(aggregate.Route.WxWorkInstanceID, aggregate.Route.TenantID); instance != nil {
			ret.WxWorkEmployeeName = instance.EmployeeName
		}
	}
	return ret
}

func BuildServiceSession(item *models.ConversationServiceSession) response.ServiceSessionResponse {
	if item == nil {
		return response.ServiceSessionResponse{}
	}
	ret := response.ServiceSessionResponse{
		ID: item.ID, ConversationID: item.ConversationID, SessionNo: item.SessionNo, CustomerID: item.CustomerID, ChannelID: item.ChannelID, StoreID: item.StoreID,
		WxWorkInstanceID: item.WxWorkInstanceID, Status: string(item.Status), StartedAt: utils.FormatTime(item.StartedAt), QueueEnteredAt: utils.FormatTimePtr(item.QueueEnteredAt),
		AssignedAt: utils.FormatTimePtr(item.AssignedAt), FirstHumanReplyAt: utils.FormatTimePtr(item.FirstHumanReplyAt), EndedAt: utils.FormatTimePtr(item.EndedAt),
		AssignedTeamID: item.AssignedTeamID, AssignedAgentID: item.AssignedAgentID, CustomerMessageCount: item.CustomerMessageCount,
		AIMessageCount: item.AIMessageCount, HumanMessageCount: item.HumanMessageCount, AssignmentCount: item.AssignmentCount, TransferCount: item.TransferCount,
		QueueSeconds: item.QueueSeconds, FirstResponseSeconds: item.FirstResponseSeconds, TotalHumanWaitSeconds: item.TotalHumanWaitSeconds,
		CloseReason: item.CloseReason, LastMessageAt: utils.FormatTimePtr(item.LastMessageAt),
		ResolutionCode: item.ResolutionCode, CategoryCode: item.CategoryCode, SessionSummary: item.SessionSummary,
		FactOrigin: string(item.FactOrigin), DataQuality: string(item.DataQuality),
	}
	_ = json.Unmarshal([]byte(item.EstimatedFieldsJSON), &ret.EstimatedFields)
	if ret.EstimatedFields == nil {
		ret.EstimatedFields = []string{}
	}
	if conversation := services.ConversationService.GetByTenantID(item.ConversationID, item.TenantID); conversation != nil {
		ret.CustomerName = conversation.CustomerName
	}
	if channel := services.ChannelService.GetByTenantID(item.ChannelID, item.TenantID); channel != nil {
		ret.ChannelName = channel.Name
	}
	if store := services.StoreService.GetInTenant(item.StoreID, item.TenantID); store != nil {
		ret.StoreName = store.Name
	}
	if instance := services.WxWorkProtocolInstanceService.GetByTenantID(item.WxWorkInstanceID, item.TenantID); instance != nil {
		ret.WxWorkEmployeeName = instance.EmployeeName
	}
	if team := services.AgentTeamService.GetByTenantID(item.AssignedTeamID, item.TenantID); team != nil {
		ret.AssignedTeamName = team.Name
	}
	if user := services.UserService.GetInTenant(item.AssignedAgentID, item.TenantID); user != nil {
		ret.AssignedAgentName = userDisplayName(user)
	}
	return ret
}

func BuildServiceAnalyticsPolicy(item models.ServiceAnalyticsPolicy) response.ServiceAnalyticsPolicyResponse {
	return response.ServiceAnalyticsPolicyResponse{
		QueueTargetSeconds: item.QueueTargetSeconds, FirstResponseTargetSeconds: item.FirstResponseTargetSeconds,
		ResponseTargetSeconds: item.ResponseTargetSeconds, RepeatConsultationHours: item.RepeatConsultationHours,
		SatisfactionThreshold: item.SatisfactionThreshold, EvaluationExpiryHours: item.EvaluationExpiryHours,
		DefaultSampleSize: item.DefaultSampleSize,
	}
}

func userDisplayName(user *models.User) string {
	if user == nil {
		return ""
	}
	if name := strings.TrimSpace(user.Nickname); name != "" {
		return name
	}
	return strings.TrimSpace(user.Username)
}

func teamDisplayName(team *models.AgentTeam) string {
	if team == nil {
		return ""
	}
	return strings.TrimSpace(team.Name)
}
