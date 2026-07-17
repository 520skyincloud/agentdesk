package services

import (
	"slices"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var ServiceAnalyticsService = &serviceAnalyticsService{}

type serviceAnalyticsService struct{}

type ServiceAnalyticsQuery struct {
	StartAt                   time.Time
	EndAt                     time.Time
	TeamID                    int64
	SquadID                   int64
	AgentID                   int64
	StoreID                   int64
	WxWorkInstanceID          int64
	DataQuality               string
	IncludeCurrentAgentRoster bool
}

type ServiceAnalyticsSummary struct {
	SessionCount                int64
	UniqueCustomerCount         int64
	ClosedSessionCount          int64
	HumanQueueCount             int64
	AssignedCount               int64
	HumanRepliedCount           int64
	UnansweredCount             int64
	QueueFailureCount           int64
	TransferSessionCount        int64
	RepeatConsultationCount     int64
	TotalMessageCount           int64
	CustomerMessageCount        int64
	AIMessageCount              int64
	HumanMessageCount           int64
	AssignmentAccessRate        float64
	EffectiveAccessRate         float64
	TransferRate                float64
	RepeatConsultationRate      float64
	AverageQueueSeconds         float64
	P50QueueSeconds             float64
	P90QueueSeconds             float64
	AverageFirstReplySeconds    float64
	P50FirstReplySeconds        float64
	P90FirstReplySeconds        float64
	AverageResponseSeconds      float64
	P50ResponseSeconds          float64
	P90ResponseSeconds          float64
	AverageHumanWaitSeconds     float64
	P50HumanWaitSeconds         float64
	P90HumanWaitSeconds         float64
	AverageSessionSeconds       float64
	P50SessionSeconds           float64
	P90SessionSeconds           float64
	AverageMessagesPerSession   float64
	QueueSLARate                float64
	FirstReplySLARate           float64
	ResponseSLARate             float64
	QualityInspectableCount     int64
	QualityInspectionCount      int64
	QualityPendingCount         int64
	QualityPassedCount          int64
	QualityFailedCount          int64
	QualityCoverageRate         float64
	QualityPassRate             float64
	AverageQualityScore         float64
	EvaluationInviteCount       int64
	EvaluationSubmittedCount    int64
	SatisfiedCount              int64
	EvaluationParticipationRate float64
	SatisfactionRate            float64
	AverageSatisfaction         float64
	ExactSessionCount           int64
	EstimatedSessionCount       int64
	IncompleteSessionCount      int64
}

type ServiceAnalyticsTrend struct {
	Date              string
	Sessions          int64
	HumanQueues       int64
	HumanReplies      int64
	Messages          int64
	AverageQueue      float64
	AverageFirstReply float64
	AverageResponse   float64
	AverageSession    float64
	queueTotal        int64
	queueSamples      int64
	firstReplyTotal   int64
	firstReplySamples int64
	responseTotal     int64
	responseSamples   int64
	sessionTotal      int64
	sessionSamples    int64
}

type ServiceAnalyticsAgent struct {
	AgentID                     int64
	AgentName                   string
	TeamID                      int64
	TeamName                    string
	SquadNames                  []string
	CurrentStatus               string
	CurrentActiveCount          int64
	MaxConcurrentCount          int
	AssignedCount               int64
	RepliedCount                int64
	UnansweredCount             int64
	HumanMessageCount           int64
	ResponseCount               int64
	ServiceSeconds              int64
	AverageFirstReplySeconds    float64
	P50FirstReplySeconds        float64
	P90FirstReplySeconds        float64
	AverageResponseSeconds      float64
	P50ResponseSeconds          float64
	P90ResponseSeconds          float64
	ResponseSLARate             float64
	OnlineSeconds               int64
	IdleSeconds                 int64
	BusySeconds                 int64
	BreakSeconds                int64
	FirstOnlineAt               *time.Time
	LastOnlineAt                *time.Time
	UtilizationRate             float64
	QualityInspectableCount     int64
	QualityInspectionCount      int64
	QualityPendingCount         int64
	QualityPassedCount          int64
	QualityFailedCount          int64
	QualityPassRate             float64
	AverageQualityScore         float64
	EvaluationInviteCount       int64
	EvaluationSubmittedCount    int64
	SatisfiedCount              int64
	EvaluationParticipationRate float64
	SatisfactionRate            float64
	AverageSatisfaction         float64
	firstReplyTotal             int64
	firstReplySamples           int64
	firstReplyValues            []int64
	responseTotal               int64
	responseSamples             int64
	responseValues              []int64
	responseSLAMet              int64
	qualityScoreTotal           float64
	qualitySamples              int64
	evaluationRatingTotal       int64
	repliedAssignments          map[int64]struct{}
}

type ServiceAnalyticsSource struct {
	StoreID                     int64
	StoreName                   string
	WxWorkInstanceID            int64
	WxWorkEmployeeName          string
	SessionCount                int64
	HumanQueueCount             int64
	HumanRepliedCount           int64
	MessageCount                int64
	AverageFirstReply           float64
	EffectiveAccessRate         float64
	QualityInspectableCount     int64
	QualityInspectionCount      int64
	QualityPassedCount          int64
	QualityCoverageRate         float64
	QualityPassRate             float64
	AverageQualityScore         float64
	EvaluationInviteCount       int64
	EvaluationSubmittedCount    int64
	SatisfiedCount              int64
	EvaluationParticipationRate float64
	SatisfactionRate            float64
	AverageSatisfaction         float64
	firstReplyTotal             int64
	firstReplySamples           int64
	qualityScoreTotal           float64
	evaluationRatingTotal       int64
}

type ServiceAnalyticsRealtime struct {
	OpenSessionCount              int64
	AIActiveCount                 int64
	QueueingCount                 int64
	AssignedActiveCount           int64
	WaitingReplyCount             int64
	LongestQueueSeconds           int64
	QueueSLAAlertCount            int64
	OnlineAgentCount              int64
	IdleAgentCount                int64
	BusyAgentCount                int64
	BreakAgentCount               int64
	OfflineAgentCount             int64
	AvailableCapacity             int64
	TodaySessionCount             int64
	TodayQueueCount               int64
	TodayAssignedCount            int64
	TodayHumanRepliedCount        int64
	TodayTransferCount            int64
	TodayQueueFailureCount        int64
	TodayMessageCount             int64
	TodayAverageQueueSeconds      float64
	TodayAverageFirstReplySeconds float64
}

type ServiceAnalyticsDistribution struct {
	Key   string
	Label string
	Count int64
	Rate  float64
}

type ServiceAnalyticsDispatch struct {
	DecisionCount                int64
	SelectedCount                int64
	AutoCount                    int64
	ManualCount                  int64
	RuleCount                    int64
	ModelCount                   int64
	HybridCount                  int64
	FallbackCount                int64
	FailedCount                  int64
	StaleCount                   int64
	OverrideCount                int64
	TransferCount                int64
	AutoRate                     float64
	AverageDecisionLatencyMillis float64
}

type ServiceAnalyticsOverview struct {
	StartAt                     time.Time
	EndAt                       time.Time
	GeneratedAt                 time.Time
	Summary                     ServiceAnalyticsSummary
	Realtime                    ServiceAnalyticsRealtime
	Trend                       []ServiceAnalyticsTrend
	FirstReplyDistribution      []ServiceAnalyticsDistribution
	ResponseDistribution        []ServiceAnalyticsDistribution
	SessionDurationDistribution []ServiceAnalyticsDistribution
	Agents                      []ServiceAnalyticsAgent
	Sources                     []ServiceAnalyticsSource
	Dispatch                    ServiceAnalyticsDispatch
}

func (s *serviceAnalyticsService) GetOverview(query ServiceAnalyticsQuery, operator *dto.AuthPrincipal) (*ServiceAnalyticsOverview, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要查看统计的接入公司")
	}
	query = normalizeAnalyticsQuery(query)
	cnd := sqls.NewCnd().Eq("tenant_id", tenantID).Gte("started_at", query.StartAt).Lte("started_at", query.EndAt).Asc("started_at")
	cnd = s.applySessionScope(cnd, query, operator)
	sessions := repositories.ConversationServiceSessionRepository.Find(sqls.DB(), cnd)
	policy := s.GetPolicy(tenantID)
	result := &ServiceAnalyticsOverview{StartAt: query.StartAt, EndAt: query.EndAt, GeneratedAt: time.Now()}
	agents := map[int64]*ServiceAnalyticsAgent{}
	sources := map[[2]int64]*ServiceAnalyticsSource{}
	trends := map[string]*ServiceAnalyticsTrend{}
	customers := map[int64]struct{}{}
	firstReplyValues := make([]int64, 0)
	queueValues := make([]int64, 0)
	humanWaitValues := make([]int64, 0)
	sessionDurationValues := make([]int64, 0)
	sourceKeysBySession := make(map[analyticsAssignmentSessionKey][2]int64, len(sessions))
	for i := range sessions {
		session := &sessions[i]
		date := session.StartedAt.Format(time.DateOnly)
		trend := trends[date]
		if trend == nil {
			trend = &ServiceAnalyticsTrend{Date: date}
			trends[date] = trend
		}
		trend.Sessions++
		result.Summary.SessionCount++
		switch session.DataQuality {
		case enums.AnalyticsDataQualityExact:
			result.Summary.ExactSessionCount++
		case enums.AnalyticsDataQualityEstimated:
			result.Summary.EstimatedSessionCount++
		case enums.AnalyticsDataQualityIncomplete:
			result.Summary.IncompleteSessionCount++
		}
		if session.CustomerID > 0 {
			customers[session.CustomerID] = struct{}{}
		}
		messageCount := int64(session.CustomerMessageCount + session.AIMessageCount + session.HumanMessageCount + session.SystemMessageCount)
		result.Summary.TotalMessageCount += messageCount
		result.Summary.CustomerMessageCount += int64(session.CustomerMessageCount)
		result.Summary.AIMessageCount += int64(session.AIMessageCount)
		result.Summary.HumanMessageCount += int64(session.HumanMessageCount)
		trend.Messages += messageCount
		if session.Status == enums.ServiceSessionStatusClosed {
			result.Summary.ClosedSessionCount++
			if session.EndedAt != nil {
				duration := nonNegativeSeconds(session.StartedAt, *session.EndedAt)
				result.Summary.AverageSessionSeconds += float64(duration)
				trend.sessionTotal += duration
				trend.sessionSamples++
				sessionDurationValues = append(sessionDurationValues, duration)
			}
		}
		if session.QueueEnteredAt != nil {
			result.Summary.HumanQueueCount++
			trend.HumanQueues++
		}
		if session.AssignedAt != nil {
			result.Summary.AssignedCount++
			if session.QueueEnteredAt != nil {
				result.Summary.AverageQueueSeconds += float64(session.QueueSeconds)
				queueValues = append(queueValues, session.QueueSeconds)
				trend.queueTotal += session.QueueSeconds
				trend.queueSamples++
			}
		}
		if session.FirstHumanReplyAt != nil {
			result.Summary.HumanRepliedCount++
			result.Summary.AverageFirstReplySeconds += float64(session.FirstResponseSeconds)
			result.Summary.AverageHumanWaitSeconds += float64(session.TotalHumanWaitSeconds)
			humanWaitValues = append(humanWaitValues, session.TotalHumanWaitSeconds)
			trend.HumanReplies++
			trend.firstReplyTotal += session.FirstResponseSeconds
			trend.firstReplySamples++
			firstReplyValues = append(firstReplyValues, session.FirstResponseSeconds)
		} else if session.QueueEnteredAt != nil {
			result.Summary.UnansweredCount++
			if session.Status == enums.ServiceSessionStatusClosed && session.AssignedAt == nil {
				result.Summary.QueueFailureCount++
			}
		}
		if session.TransferCount > 0 {
			result.Summary.TransferSessionCount++
		}
		key := [2]int64{session.StoreID, session.WxWorkInstanceID}
		sourceKeysBySession[analyticsAssignmentSessionKey{ConversationID: session.ConversationID, SessionNo: normalizedSessionNo(session.SessionNo)}] = key
		source := sources[key]
		if source == nil {
			source = &ServiceAnalyticsSource{StoreID: session.StoreID, WxWorkInstanceID: session.WxWorkInstanceID}
			if store := StoreService.GetInTenant(session.StoreID, tenantID); store != nil {
				source.StoreName = store.Name
			}
			if instance := WxWorkProtocolInstanceService.GetByTenantID(session.WxWorkInstanceID, tenantID); instance != nil {
				source.WxWorkEmployeeName = instance.EmployeeName
			}
			sources[key] = source
		}
		source.SessionCount++
		source.MessageCount += messageCount
		if session.QueueEnteredAt != nil {
			source.HumanQueueCount++
		}
		if session.FirstHumanReplyAt != nil {
			source.HumanRepliedCount++
			source.firstReplyTotal += session.FirstResponseSeconds
			source.firstReplySamples++
		}
	}
	result.Summary.UniqueCustomerCount = int64(len(customers))
	result.Summary.RepeatConsultationCount = countRepeatConsultations(sessions, time.Duration(policy.RepeatConsultationHours)*time.Hour)
	s.finalizeSummary(&result.Summary, sessions, policy)
	s.applyAssignmentMetrics(agents, tenantID, query, operator)
	responseValues := s.applyResponseMetrics(&result.Summary, agents, trends, tenantID, query, operator, policy)
	result.Summary.P50QueueSeconds, result.Summary.P90QueueSeconds = percentilePair(queueValues)
	result.Summary.P50FirstReplySeconds, result.Summary.P90FirstReplySeconds = percentilePair(firstReplyValues)
	result.Summary.P50ResponseSeconds, result.Summary.P90ResponseSeconds = percentilePair(responseValues)
	result.Summary.P50HumanWaitSeconds, result.Summary.P90HumanWaitSeconds = percentilePair(humanWaitValues)
	result.Summary.P50SessionSeconds, result.Summary.P90SessionSeconds = percentilePair(sessionDurationValues)
	s.applyPresenceMetrics(agents, tenantID, query, operator)
	s.applyCurrentAgentMetrics(agents, tenantID, query, operator)
	s.applyQualityMetrics(&result.Summary, agents, sources, sourceKeysBySession, tenantID, query, operator)
	s.applyEvaluationMetrics(&result.Summary, agents, sources, sourceKeysBySession, tenantID, query, operator, policy)
	result.Dispatch = s.dispatchMetrics(tenantID, query, operator, sessions)
	result.Realtime = s.realtimeMetrics(tenantID, query, operator, policy)
	result.FirstReplyDistribution = buildDurationDistribution(firstReplyValues, firstReplyBuckets())
	result.ResponseDistribution = buildDurationDistribution(responseValues, responseBuckets())
	result.SessionDurationDistribution = buildDurationDistribution(sessionDurationValues, sessionDurationBuckets())
	fillAnalyticsTrendDays(trends, query.StartAt, query.EndAt)
	for _, trend := range trends {
		if trend.queueSamples > 0 {
			trend.AverageQueue = float64(trend.queueTotal) / float64(trend.queueSamples)
		}
		if trend.firstReplySamples > 0 {
			trend.AverageFirstReply = float64(trend.firstReplyTotal) / float64(trend.firstReplySamples)
		}
		if trend.responseSamples > 0 {
			trend.AverageResponse = float64(trend.responseTotal) / float64(trend.responseSamples)
		}
		if trend.sessionSamples > 0 {
			trend.AverageSession = float64(trend.sessionTotal) / float64(trend.sessionSamples)
		}
		result.Trend = append(result.Trend, *trend)
	}
	sort.Slice(result.Trend, func(i, j int) bool { return result.Trend[i].Date < result.Trend[j].Date })
	for _, agent := range agents {
		if query.SquadID > 0 {
			if squad := repositories.AgentTeamSquadRepository.GetInTenant(sqls.DB(), query.SquadID, tenantID); squad != nil {
				agent.SquadNames = []string{squad.Name}
			}
		}
		agent.RepliedCount = int64(len(agent.repliedAssignments))
		if agent.AssignedCount > agent.RepliedCount {
			agent.UnansweredCount = agent.AssignedCount - agent.RepliedCount
		}
		if agent.firstReplySamples > 0 {
			agent.AverageFirstReplySeconds = float64(agent.firstReplyTotal) / float64(agent.firstReplySamples)
			agent.P50FirstReplySeconds, agent.P90FirstReplySeconds = percentilePair(agent.firstReplyValues)
		}
		if agent.responseSamples > 0 {
			agent.AverageResponseSeconds = float64(agent.responseTotal) / float64(agent.responseSamples)
			agent.P50ResponseSeconds, agent.P90ResponseSeconds = percentilePair(agent.responseValues)
			agent.ResponseSLARate = percentage(agent.responseSLAMet, agent.responseSamples)
		}
		if agent.MaxConcurrentCount > 0 {
			agent.UtilizationRate = float64(agent.CurrentActiveCount) * 100 / float64(agent.MaxConcurrentCount)
		}
		if agent.qualitySamples > 0 {
			agent.AverageQualityScore = agent.qualityScoreTotal / float64(agent.qualitySamples)
		}
		if agent.QualityInspectionCount > 0 {
			agent.QualityPassRate = percentage(agent.QualityPassedCount, agent.QualityInspectionCount)
		}
		if agent.EvaluationInviteCount > 0 {
			agent.EvaluationParticipationRate = percentage(agent.EvaluationSubmittedCount, agent.EvaluationInviteCount)
		}
		if agent.EvaluationSubmittedCount > 0 {
			agent.SatisfactionRate = percentage(agent.SatisfiedCount, agent.EvaluationSubmittedCount)
			agent.AverageSatisfaction = float64(agent.evaluationRatingTotal) / float64(agent.EvaluationSubmittedCount)
		}
		result.Agents = append(result.Agents, *agent)
	}
	sort.Slice(result.Agents, func(i, j int) bool {
		if result.Agents[i].RepliedCount == result.Agents[j].RepliedCount {
			return result.Agents[i].AgentID < result.Agents[j].AgentID
		}
		return result.Agents[i].RepliedCount > result.Agents[j].RepliedCount
	})
	for _, source := range sources {
		if source.firstReplySamples > 0 {
			source.AverageFirstReply = float64(source.firstReplyTotal) / float64(source.firstReplySamples)
		}
		if source.HumanQueueCount > 0 {
			source.EffectiveAccessRate = percentage(source.HumanRepliedCount, source.HumanQueueCount)
		}
		if source.QualityInspectableCount > 0 {
			source.QualityCoverageRate = percentage(source.QualityInspectionCount, source.QualityInspectableCount)
		}
		if source.QualityInspectionCount > 0 {
			source.QualityPassRate = percentage(source.QualityPassedCount, source.QualityInspectionCount)
			source.AverageQualityScore = source.qualityScoreTotal / float64(source.QualityInspectionCount)
		}
		if source.EvaluationInviteCount > 0 {
			source.EvaluationParticipationRate = percentage(source.EvaluationSubmittedCount, source.EvaluationInviteCount)
		}
		if source.EvaluationSubmittedCount > 0 {
			source.SatisfactionRate = percentage(source.SatisfiedCount, source.EvaluationSubmittedCount)
			source.AverageSatisfaction = float64(source.evaluationRatingTotal) / float64(source.EvaluationSubmittedCount)
		}
		result.Sources = append(result.Sources, *source)
	}
	sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].SessionCount > result.Sources[j].SessionCount })
	return result, nil
}

func fillAnalyticsTrendDays(trends map[string]*ServiceAnalyticsTrend, startAt, endAt time.Time) {
	if trends == nil || startAt.IsZero() || endAt.IsZero() {
		return
	}
	if endAt.Before(startAt) {
		startAt, endAt = endAt, startAt
	}
	location := startAt.Location()
	start := time.Date(startAt.Year(), startAt.Month(), startAt.Day(), 0, 0, 0, 0, location)
	endInLocation := endAt.In(location)
	end := time.Date(endInLocation.Year(), endInLocation.Month(), endInLocation.Day(), 0, 0, 0, 0, location)
	for current := start; !current.After(end); current = current.AddDate(0, 0, 1) {
		date := current.Format(time.DateOnly)
		if trends[date] == nil {
			trends[date] = &ServiceAnalyticsTrend{Date: date}
		}
	}
}

func (s *serviceAnalyticsService) GetSession(id int64, operator *dto.AuthPrincipal) (*models.ConversationServiceSession, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	item := repositories.ConversationServiceSessionRepository.GetInTenant(sqls.DB(), id, tenantID)
	if item == nil || !AgentTeamScopeService.CanViewConversation(operator, item.ConversationID) {
		return nil, errorsx.InvalidParam("会话记录不存在")
	}
	if !s.canViewAgent(operator, item.AssignedAgentID, item.AssignedTeamID) {
		return nil, errorsx.Forbidden("无权查看该会话记录")
	}
	return item, nil
}

func (s *serviceAnalyticsService) GetPolicy(tenantID int64) models.ServiceAnalyticsPolicy {
	if item := repositories.ServiceAnalyticsPolicyRepository.TakeByTenant(sqls.DB(), tenantID); item != nil {
		return *item
	}
	return models.ServiceAnalyticsPolicy{
		TenantID: tenantID, QueueTargetSeconds: 60, FirstResponseTargetSeconds: 180,
		ResponseTargetSeconds: 300, RepeatConsultationHours: 24,
		SatisfactionThreshold: 4, EvaluationExpiryHours: 72, DefaultSampleSize: 20,
	}
}

func (s *serviceAnalyticsService) UpdatePolicy(req request.SaveServiceAnalyticsPolicyRequest, operator *dto.AuthPrincipal) (*models.ServiceAnalyticsPolicy, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要配置统计口径的接入公司")
	}
	now := time.Now()
	item := repositories.ServiceAnalyticsPolicyRepository.TakeByTenant(sqls.DB(), tenantID)
	values := map[string]any{
		"queue_target_seconds":          req.QueueTargetSeconds,
		"first_response_target_seconds": req.FirstResponseTargetSeconds,
		"response_target_seconds":       req.ResponseTargetSeconds,
		"repeat_consultation_hours":     req.RepeatConsultationHours,
		"satisfaction_threshold":        req.SatisfactionThreshold,
		"evaluation_expiry_hours":       req.EvaluationExpiryHours,
		"default_sample_size":           req.DefaultSampleSize,
		"updated_at":                    now,
		"update_user_id":                operator.UserID,
		"update_user_name":              operator.Username,
	}
	if item == nil {
		item = &models.ServiceAnalyticsPolicy{
			TenantID: tenantID, QueueTargetSeconds: req.QueueTargetSeconds, FirstResponseTargetSeconds: req.FirstResponseTargetSeconds,
			ResponseTargetSeconds: req.ResponseTargetSeconds, RepeatConsultationHours: req.RepeatConsultationHours,
			SatisfactionThreshold: req.SatisfactionThreshold, EvaluationExpiryHours: req.EvaluationExpiryHours,
			DefaultSampleSize: req.DefaultSampleSize,
			AuditFields:       utils.BuildAuditFields(operator),
		}
		if err := repositories.ServiceAnalyticsPolicyRepository.Create(sqls.DB(), item); err != nil {
			return nil, err
		}
		return item, nil
	}
	if err := repositories.ServiceAnalyticsPolicyRepository.UpdatesInTenant(sqls.DB(), item.ID, tenantID, values); err != nil {
		return nil, err
	}
	return repositories.ServiceAnalyticsPolicyRepository.TakeByTenant(sqls.DB(), tenantID), nil
}
func (s *serviceAnalyticsService) applySessionScope(cnd *sqls.Cnd, query ServiceAnalyticsQuery, operator *dto.AuthPrincipal) *sqls.Cnd {
	cnd = s.applySessionRoleScope(cnd, operator)
	if query.TeamID > 0 {
		cnd.Eq("assigned_team_id", query.TeamID)
	}
	if query.SquadID > 0 {
		cnd.Eq("assigned_squad_id", query.SquadID)
	}
	if query.AgentID > 0 {
		cnd.Eq("assigned_agent_id", query.AgentID)
	}
	if query.StoreID > 0 {
		cnd.Eq("store_id", query.StoreID)
	}
	if query.WxWorkInstanceID > 0 {
		cnd.Eq("wx_work_instance_id", query.WxWorkInstanceID)
	}
	switch enums.AnalyticsDataQuality(strings.TrimSpace(query.DataQuality)) {
	case enums.AnalyticsDataQualityExact, enums.AnalyticsDataQualityEstimated, enums.AnalyticsDataQualityIncomplete:
		cnd.Eq("data_quality", strings.TrimSpace(query.DataQuality))
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if !scope.Unrestricted {
		if len(scope.WxWorkInstanceIDs) > 0 {
			cnd.In("wx_work_instance_id", scope.WxWorkInstanceIDs)
		} else if len(scope.StoreIDs) > 0 {
			cnd.In("store_id", scope.StoreIDs)
		}
	}
	return cnd
}

func (s *serviceAnalyticsService) applySessionRoleScope(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	if AgentTeamScopeService.IsAdmin(operator) {
		return cnd
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsUser) && !slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		return cnd.Eq("assigned_agent_id", operator.UserID)
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		teams := repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", operator.ActiveTenantID).Eq("leader_user_id", operator.UserID).Where("status <> ?", enums.StatusDeleted))
		ids := make([]int64, 0, len(teams))
		for _, team := range teams {
			ids = append(ids, team.ID)
		}
		if len(ids) > 0 {
			return cnd.In("assigned_team_id", ids)
		}
	}
	return cnd.Eq("id", -1)
}

func (s *serviceAnalyticsService) canViewAgent(operator *dto.AuthPrincipal, agentID, teamID int64) bool {
	if AgentTeamScopeService.IsAdmin(operator) {
		return true
	}
	if teamID <= 0 && agentID > 0 {
		if profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", operator.ActiveTenantID, agentID); profile != nil {
			teamID = profile.TeamID
		}
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsUser) && !slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		return agentID == operator.UserID
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, operator.ActiveTenantID)
		return team != nil && team.LeaderUserID == operator.UserID
	}
	return false
}

func (s *serviceAnalyticsService) ensureAgentMetric(metrics map[int64]*ServiceAnalyticsAgent, agentID, teamID, tenantID int64) *ServiceAnalyticsAgent {
	if existing := metrics[agentID]; existing != nil {
		if existing.TeamID <= 0 && teamID > 0 {
			existing.TeamID = teamID
		}
		return existing
	}
	item := &ServiceAnalyticsAgent{AgentID: agentID, TeamID: teamID, CurrentStatus: "offline", repliedAssignments: map[int64]struct{}{}}
	if user := UserService.GetInTenant(agentID, tenantID); user != nil {
		item.AgentName = strings.TrimSpace(user.Nickname)
		if item.AgentName == "" {
			item.AgentName = user.Username
		}
	}
	if profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", tenantID, agentID); profile != nil {
		if item.TeamID <= 0 {
			item.TeamID = profile.TeamID
		}
		item.MaxConcurrentCount = profile.MaxConcurrentCount
		item.LastOnlineAt = profile.LastOnlineAt
		for _, membership := range repositories.AgentTeamSquadMemberRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			Eq("agent_profile_id", profile.ID).
			Eq("status", enums.StatusOk)) {
			if squad := repositories.AgentTeamSquadRepository.GetInTenant(sqls.DB(), membership.SquadID, tenantID); squad != nil {
				item.SquadNames = append(item.SquadNames, squad.Name)
			}
		}
		sort.Strings(item.SquadNames)
	}
	if team := AgentTeamService.GetByTenantID(item.TeamID, tenantID); team != nil {
		item.TeamName = team.Name
	}
	metrics[agentID] = item
	return item
}

func (s *serviceAnalyticsService) finalizeSummary(summary *ServiceAnalyticsSummary, sessions []models.ConversationServiceSession, policy models.ServiceAnalyticsPolicy) {
	if summary.HumanQueueCount > 0 {
		summary.AssignmentAccessRate = percentage(summary.AssignedCount, summary.HumanQueueCount)
		summary.EffectiveAccessRate = percentage(summary.HumanRepliedCount, summary.HumanQueueCount)
	}
	queueSamples := int64(0)
	queueMet := int64(0)
	firstReplySamples := int64(0)
	firstReplyMet := int64(0)
	for _, session := range sessions {
		if session.QueueEnteredAt != nil && session.AssignedAt != nil {
			queueSamples++
			if session.QueueSeconds <= int64(policy.QueueTargetSeconds) {
				queueMet++
			}
		}
		if session.FirstHumanReplyAt != nil {
			firstReplySamples++
			if session.FirstResponseSeconds <= int64(policy.FirstResponseTargetSeconds) {
				firstReplyMet++
			}
		}
	}
	if queueSamples > 0 {
		summary.AverageQueueSeconds /= float64(queueSamples)
		summary.QueueSLARate = percentage(queueMet, queueSamples)
	}
	if firstReplySamples > 0 {
		summary.AverageFirstReplySeconds /= float64(firstReplySamples)
		summary.AverageHumanWaitSeconds /= float64(firstReplySamples)
		summary.FirstReplySLARate = percentage(firstReplyMet, firstReplySamples)
	}
	durationSamples := int64(0)
	for _, session := range sessions {
		if session.Status == enums.ServiceSessionStatusClosed && session.EndedAt != nil {
			durationSamples++
		}
	}
	if durationSamples > 0 {
		summary.AverageSessionSeconds /= float64(durationSamples)
	}
	if summary.SessionCount > 0 {
		summary.AverageMessagesPerSession = float64(summary.TotalMessageCount) / float64(summary.SessionCount)
		summary.TransferRate = percentage(summary.TransferSessionCount, summary.SessionCount)
		summary.RepeatConsultationRate = percentage(summary.RepeatConsultationCount, summary.SessionCount)
	}
}

func (s *serviceAnalyticsService) applyAssignmentMetrics(agents map[int64]*ServiceAnalyticsAgent, tenantID int64, query ServiceAnalyticsQuery, operator *dto.AuthPrincipal) {
	assignments := repositories.ConversationAssignmentRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Gte("created_at", query.StartAt).
		Lte("created_at", query.EndAt).
		Asc("created_at").Asc("id"))
	for i := range assignments {
		assignment := &assignments[i]
		teamID := s.assignmentTeamID(assignment, tenantID)
		if !s.assignmentMatchesQuery(assignment, teamID, query, operator) {
			continue
		}
		agent := s.ensureAgentMetric(agents, assignment.ToUserID, teamID, tenantID)
		agent.AssignedCount++
	}

	assignmentSegments := repositories.ConversationAssignmentRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Lte("created_at", query.EndAt).
		Where("finished_at IS NULL OR finished_at >= ?", query.StartAt).
		Asc("created_at").Asc("id"))
	segmentsBySession := map[analyticsAssignmentSessionKey][]models.ConversationAssignment{}
	for i := range assignmentSegments {
		assignment := assignmentSegments[i]
		key := analyticsAssignmentSessionKey{ConversationID: assignment.ConversationID, SessionNo: normalizedSessionNo(assignment.SessionNo)}
		segmentsBySession[key] = append(segmentsBySession[key], assignment)
		teamID := s.assignmentTeamID(&assignment, tenantID)
		if !s.assignmentMatchesQuery(&assignment, teamID, query, operator) {
			continue
		}
		from := assignment.CreatedAt
		if from.Before(query.StartAt) {
			from = query.StartAt
		}
		to := query.EndAt
		if assignment.FinishedAt != nil && assignment.FinishedAt.Before(to) {
			to = *assignment.FinishedAt
		}
		s.ensureAgentMetric(agents, assignment.ToUserID, teamID, tenantID).ServiceSeconds += nonNegativeSeconds(from, to)
	}

	messages := repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("sender_type", enums.IMSenderTypeAgent).
		Gte("created_at", query.StartAt).
		Lte("created_at", query.EndAt).
		Asc("created_at").Asc("id"))
	for i := range messages {
		message := &messages[i]
		assignment := assignmentForAnalyticsMessage(segmentsBySession[analyticsAssignmentSessionKey{ConversationID: message.ConversationID, SessionNo: normalizedSessionNo(message.SessionNo)}], message)
		teamID := int64(0)
		if assignment != nil {
			teamID = s.assignmentTeamID(assignment, tenantID)
			if !s.assignmentMatchesQuery(assignment, teamID, query, operator) {
				continue
			}
		} else {
			profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", tenantID, message.SenderID)
			if profile == nil || query.SquadID > 0 || !s.canViewAgent(operator, message.SenderID, profile.TeamID) || !s.agentMatchesQuery(profile, query) || !s.sessionMatchesSourceQuery(tenantID, message.ConversationID, message.SessionNo, query) {
				continue
			}
			teamID = profile.TeamID
		}
		agent := s.ensureAgentMetric(agents, message.SenderID, teamID, tenantID)
		agent.HumanMessageCount++
	}
}

func (s *serviceAnalyticsService) applyResponseMetrics(
	summary *ServiceAnalyticsSummary,
	agents map[int64]*ServiceAnalyticsAgent,
	trends map[string]*ServiceAnalyticsTrend,
	tenantID int64,
	query ServiceAnalyticsQuery,
	operator *dto.AuthPrincipal,
	policy models.ServiceAnalyticsPolicy,
) []int64 {
	cnd := sqls.NewCnd().Eq("tenant_id", tenantID).Eq("status", enums.ResponseSpanStatusReplied).
		Gte("replied_at", query.StartAt).Lte("replied_at", query.EndAt).Asc("replied_at").Asc("id")
	values := make([]int64, 0)
	firstByAssignment := map[int64]struct{}{}
	for _, span := range repositories.ConversationResponseSpanRepository.Find(sqls.DB(), cnd) {
		if span.AgentID <= 0 || span.RepliedAt == nil || !s.canViewAgent(operator, span.AgentID, span.TeamID) || !s.responseSpanMatchesQuery(&span, query) {
			continue
		}
		summary.AverageResponseSeconds += float64(span.WaitSeconds)
		values = append(values, span.WaitSeconds)
		trend := trends[span.RepliedAt.Format(time.DateOnly)]
		if trend == nil {
			trend = &ServiceAnalyticsTrend{Date: span.RepliedAt.Format(time.DateOnly)}
			trends[trend.Date] = trend
		}
		trend.responseTotal += span.WaitSeconds
		trend.responseSamples++
		agent := s.ensureAgentMetric(agents, span.AgentID, span.TeamID, tenantID)
		agent.responseTotal += span.WaitSeconds
		agent.responseSamples++
		agent.responseValues = append(agent.responseValues, span.WaitSeconds)
		agent.ResponseCount++
		agent.repliedAssignments[span.AssignmentID] = struct{}{}
		if span.WaitSeconds <= int64(policy.ResponseTargetSeconds) {
			agent.responseSLAMet++
		}
		if _, exists := firstByAssignment[span.AssignmentID]; exists || span.AssignmentID <= 0 {
			continue
		}
		firstByAssignment[span.AssignmentID] = struct{}{}
		assignment := repositories.ConversationAssignmentRepository.Get(sqls.DB(), span.AssignmentID)
		if assignment == nil || assignment.TenantID != tenantID {
			continue
		}
		firstReplySeconds := nonNegativeSeconds(assignment.CreatedAt, *span.RepliedAt)
		agent.firstReplyTotal += firstReplySeconds
		agent.firstReplySamples++
		agent.firstReplyValues = append(agent.firstReplyValues, firstReplySeconds)
	}
	responseSamples := int64(len(values))
	if responseSamples > 0 {
		summary.AverageResponseSeconds /= float64(responseSamples)
		met := int64(0)
		for _, value := range values {
			if value <= int64(policy.ResponseTargetSeconds) {
				met++
			}
		}
		summary.ResponseSLARate = percentage(met, responseSamples)
	}
	return values
}

func (s *serviceAnalyticsService) applyPresenceMetrics(agents map[int64]*ServiceAnalyticsAgent, tenantID int64, query ServiceAnalyticsQuery, operator *dto.AuthPrincipal) {
	// Presence has a team snapshot but no store, WeCom account, or squad snapshot.
	// Under those filters, only enrich agents already selected by service facts.
	requireExistingAgent := query.SquadID > 0 || query.StoreID > 0 || query.WxWorkInstanceID > 0
	cnd := sqls.NewCnd().Eq("tenant_id", tenantID).Lte("started_at", query.EndAt).Where("ended_at IS NULL OR ended_at >= ?", query.StartAt)
	for _, presence := range repositories.AgentPresenceSessionRepository.Find(sqls.DB(), cnd) {
		profile := repositories.AgentProfileRepository.GetInTenant(sqls.DB(), presence.AgentProfileID, tenantID)
		if profile == nil || !s.canViewAgent(operator, presence.UserID, presence.TeamID) {
			continue
		}
		if query.TeamID > 0 && presence.TeamID != query.TeamID {
			continue
		}
		if query.AgentID > 0 && presence.UserID != query.AgentID {
			continue
		}
		agent := agents[presence.UserID]
		if agent == nil && requireExistingAgent {
			continue
		}
		from := presence.StartedAt
		if from.Before(query.StartAt) {
			from = query.StartAt
		}
		to := query.EndAt
		if presence.EndedAt != nil && presence.EndedAt.Before(to) {
			to = *presence.EndedAt
		}
		if agent == nil {
			agent = s.ensureAgentMetric(agents, presence.UserID, presence.TeamID, tenantID)
		}
		duration := nonNegativeSeconds(from, to)
		agent.OnlineSeconds += duration
		switch presence.Status {
		case enums.AgentPresenceStatusIdle:
			agent.IdleSeconds += duration
		case enums.AgentPresenceStatusBusy:
			agent.BusySeconds += duration
		case enums.AgentPresenceStatusBreak:
			agent.BreakSeconds += duration
		}
		if agent.FirstOnlineAt == nil || presence.StartedAt.Before(*agent.FirstOnlineAt) {
			at := presence.StartedAt
			agent.FirstOnlineAt = &at
		}
		if agent.LastOnlineAt == nil || presence.LastSeenAt.After(*agent.LastOnlineAt) {
			at := presence.LastSeenAt
			agent.LastOnlineAt = &at
		}
	}
}

func (s *serviceAnalyticsService) applyCurrentAgentMetrics(agents map[int64]*ServiceAnalyticsAgent, tenantID int64, query ServiceAnalyticsQuery, operator *dto.AuthPrincipal) {
	now := time.Now()
	if query.IncludeCurrentAgentRoster {
		for _, profile := range repositories.AgentProfileRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			Eq("status", enums.StatusOk).
			Asc("id")) {
			if s.canViewAgent(operator, profile.UserID, profile.TeamID) && s.agentMatchesQuery(&profile, query) {
				s.ensureAgentMetric(agents, profile.UserID, profile.TeamID, tenantID)
			}
		}
	}
	assignments := repositories.ConversationAssignmentRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("status", enums.IMAssignmentStatusActive).
		Asc("id"))
	for i := range assignments {
		assignment := &assignments[i]
		profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", tenantID, assignment.ToUserID)
		if profile == nil || !s.assignmentMatchesQuery(assignment, profile.TeamID, query, operator) {
			continue
		}
		agent := agents[assignment.ToUserID]
		if agent == nil && query.IncludeCurrentAgentRoster {
			agent = s.ensureAgentMetric(agents, assignment.ToUserID, profile.TeamID, tenantID)
		}
		if agent != nil {
			agent.CurrentActiveCount++
		}
	}
	for _, presence := range repositories.AgentPresenceSessionRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Where("ended_at IS NULL").
		Gte("last_seen_at", now.Add(-2*presenceHeartbeatInterval)).
		Asc("id")) {
		profile := repositories.AgentProfileRepository.GetInTenant(sqls.DB(), presence.AgentProfileID, tenantID)
		if profile == nil || !s.canViewAgent(operator, presence.UserID, presence.TeamID) || !s.agentMatchesQuery(profile, query) {
			continue
		}
		agent := agents[presence.UserID]
		if agent == nil && query.IncludeCurrentAgentRoster {
			agent = s.ensureAgentMetric(agents, presence.UserID, presence.TeamID, tenantID)
		}
		if agent == nil {
			continue
		}
		agent.CurrentStatus = string(presence.Status)
		if presence.Status != enums.AgentPresenceStatusBreak && agent.CurrentActiveCount > 0 {
			agent.CurrentStatus = "busy"
		}
		at := presence.LastSeenAt
		agent.LastOnlineAt = &at
	}
}

func (s *serviceAnalyticsService) realtimeMetrics(tenantID int64, query ServiceAnalyticsQuery, operator *dto.AuthPrincipal, policy models.ServiceAnalyticsPolicy) ServiceAnalyticsRealtime {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	todayQuery := query
	todayQuery.StartAt = todayStart
	todayQuery.EndAt = now
	ret := ServiceAnalyticsRealtime{}

	openCnd := sqls.NewCnd().Eq("tenant_id", tenantID).Eq("status", enums.ServiceSessionStatusOpen)
	for _, session := range repositories.ConversationServiceSessionRepository.Find(sqls.DB(), s.applySessionScope(openCnd, query, operator)) {
		ret.OpenSessionCount++
		if route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), session.ConversationID, tenantID); route != nil && route.SessionNo == session.SessionNo {
			switch route.RouteStatus {
			case enums.ConversationRouteStatusAIServing, enums.ConversationRouteStatusAIFallback:
				ret.AIActiveCount++
			case enums.ConversationRouteStatusHQAgentDeskPending:
				ret.QueueingCount++
				if session.QueueEnteredAt != nil {
					waitSeconds := nonNegativeSeconds(*session.QueueEnteredAt, now)
					if waitSeconds > ret.LongestQueueSeconds {
						ret.LongestQueueSeconds = waitSeconds
					}
					if waitSeconds >= int64(policy.QueueTargetSeconds) {
						ret.QueueSLAAlertCount++
					}
				}
			case enums.ConversationRouteStatusStoreWecomManual, enums.ConversationRouteStatusHQAgentDeskServing:
				ret.AssignedActiveCount++
			}
		}
	}

	todayCnd := sqls.NewCnd().Eq("tenant_id", tenantID).Gte("started_at", todayStart).Lte("started_at", now)
	ret.TodaySessionCount = int64(len(repositories.ConversationServiceSessionRepository.Find(sqls.DB(), s.applySessionScope(todayCnd, todayQuery, operator))))
	queueCnd := sqls.NewCnd().Eq("tenant_id", tenantID).Gte("queue_entered_at", todayStart).Lte("queue_entered_at", now)
	todayQueues := repositories.ConversationServiceSessionRepository.Find(sqls.DB(), s.applySessionScope(queueCnd, todayQuery, operator))
	queueSamples := int64(0)
	firstReplySamples := int64(0)
	for _, session := range todayQueues {
		if session.QueueEnteredAt != nil {
			ret.TodayQueueCount++
		}
		if session.QueueEnteredAt != nil && session.AssignedAt != nil {
			ret.TodayAssignedCount++
			queueSamples++
			ret.TodayAverageQueueSeconds += float64(session.QueueSeconds)
		}
		if session.FirstHumanReplyAt != nil {
			ret.TodayHumanRepliedCount++
			firstReplySamples++
			ret.TodayAverageFirstReplySeconds += float64(session.FirstResponseSeconds)
		} else if session.QueueEnteredAt != nil && session.AssignedAt == nil && session.Status == enums.ServiceSessionStatusClosed {
			ret.TodayQueueFailureCount++
		}
		if session.TransferCount > 0 {
			ret.TodayTransferCount++
		}
	}
	if queueSamples > 0 {
		ret.TodayAverageQueueSeconds /= float64(queueSamples)
	}
	if firstReplySamples > 0 {
		ret.TodayAverageFirstReplySeconds /= float64(firstReplySamples)
	}
	messageSessions := map[analyticsAssignmentSessionKey]*models.ConversationServiceSession{}
	for _, message := range repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Where("sent_at >= ? OR (sent_at IS NULL AND created_at >= ?)", todayStart, todayStart).
		Asc("id")) {
		key := analyticsAssignmentSessionKey{ConversationID: message.ConversationID, SessionNo: normalizedSessionNo(message.SessionNo)}
		session, exists := messageSessions[key]
		if !exists {
			session = repositories.ConversationServiceSessionRepository.TakeByKey(sqls.DB(), tenantID, key.ConversationID, key.SessionNo)
			messageSessions[key] = session
		}
		if session != nil && s.sessionMatchesAnalyticsScope(session, query, operator) {
			ret.TodayMessageCount++
		}
	}

	activeAssignments := map[int64]int64{}
	for _, assignment := range repositories.ConversationAssignmentRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("status", enums.IMAssignmentStatusActive).
		Asc("id")) {
		profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", tenantID, assignment.ToUserID)
		if profile == nil || !s.assignmentMatchesQuery(&assignment, profile.TeamID, query, operator) {
			continue
		}
		activeAssignments[assignment.ToUserID]++
	}

	activePresence := map[int64]models.AgentPresenceSession{}
	for _, presence := range repositories.AgentPresenceSessionRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Where("ended_at IS NULL").
		Gte("last_seen_at", now.Add(-2*presenceHeartbeatInterval))) {
		profile := repositories.AgentProfileRepository.GetInTenant(sqls.DB(), presence.AgentProfileID, tenantID)
		if profile == nil || !s.canViewAgent(operator, presence.UserID, presence.TeamID) || !s.agentMatchesQuery(profile, query) {
			continue
		}
		activePresence[presence.UserID] = presence
	}
	for _, profile := range repositories.AgentProfileRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("status", enums.StatusOk).
		Asc("id")) {
		if !s.canViewAgent(operator, profile.UserID, profile.TeamID) || !s.agentMatchesQuery(&profile, query) {
			continue
		}
		presence, online := activePresence[profile.UserID]
		if !online {
			ret.OfflineAgentCount++
			continue
		}
		ret.OnlineAgentCount++
		activeCount := activeAssignments[profile.UserID]
		switch {
		case presence.Status == enums.AgentPresenceStatusBreak:
			ret.BreakAgentCount++
		case activeCount > 0 || presence.Status == enums.AgentPresenceStatusBusy:
			ret.BusyAgentCount++
		default:
			ret.IdleAgentCount++
		}
		if presence.Status != enums.AgentPresenceStatusBreak && int64(profile.MaxConcurrentCount) > activeCount {
			ret.AvailableCapacity += int64(profile.MaxConcurrentCount) - activeCount
		}
	}

	for _, span := range repositories.ConversationResponseSpanRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("status", enums.ResponseSpanStatusWaiting).
		Asc("id")) {
		session := repositories.ConversationServiceSessionRepository.TakeByKey(sqls.DB(), tenantID, span.ConversationID, normalizedSessionNo(span.SessionNo))
		if session != nil && session.Status == enums.ServiceSessionStatusOpen && s.sessionMatchesAnalyticsScope(session, query, operator) {
			ret.WaitingReplyCount++
		}
	}
	return ret
}

func (s *serviceAnalyticsService) sessionMatchesAnalyticsScope(session *models.ConversationServiceSession, query ServiceAnalyticsQuery, operator *dto.AuthPrincipal) bool {
	if session == nil || session.TenantID != AgentTeamScopeService.ActiveTenantID(operator) {
		return false
	}
	if query.TeamID > 0 && session.AssignedTeamID != query.TeamID {
		return false
	}
	if query.SquadID > 0 && session.AssignedSquadID != query.SquadID {
		return false
	}
	if query.AgentID > 0 && session.AssignedAgentID != query.AgentID {
		return false
	}
	if query.StoreID > 0 && session.StoreID != query.StoreID {
		return false
	}
	if query.WxWorkInstanceID > 0 && session.WxWorkInstanceID != query.WxWorkInstanceID {
		return false
	}
	if !s.canViewAgent(operator, session.AssignedAgentID, session.AssignedTeamID) {
		return false
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if scope.Unrestricted {
		return true
	}
	if len(scope.WxWorkInstanceIDs) > 0 {
		return slices.Contains(scope.WxWorkInstanceIDs, session.WxWorkInstanceID)
	}
	return len(scope.StoreIDs) == 0 || slices.Contains(scope.StoreIDs, session.StoreID)
}

func (s *serviceAnalyticsService) assignmentMatchesQuery(assignment *models.ConversationAssignment, teamID int64, query ServiceAnalyticsQuery, operator *dto.AuthPrincipal) bool {
	if assignment == nil || assignment.ToUserID <= 0 || !s.canViewAgent(operator, assignment.ToUserID, teamID) {
		return false
	}
	if query.TeamID > 0 && teamID != query.TeamID {
		return false
	}
	if query.SquadID > 0 && assignment.SquadID != query.SquadID {
		return false
	}
	if query.AgentID > 0 && assignment.ToUserID != query.AgentID {
		return false
	}
	return s.sessionMatchesSourceQuery(assignment.TenantID, assignment.ConversationID, assignment.SessionNo, query)
}

func (s *serviceAnalyticsService) responseSpanMatchesQuery(span *models.ConversationResponseSpan, query ServiceAnalyticsQuery) bool {
	if span == nil {
		return false
	}
	if query.TeamID > 0 && span.TeamID != query.TeamID {
		return false
	}
	if query.SquadID > 0 && span.SquadID != query.SquadID {
		return false
	}
	if query.AgentID > 0 && span.AgentID != query.AgentID {
		return false
	}
	return s.sessionMatchesSourceQuery(span.TenantID, span.ConversationID, span.SessionNo, query)
}

func (s *serviceAnalyticsService) agentMatchesQuery(profile *models.AgentProfile, query ServiceAnalyticsQuery) bool {
	if profile == nil {
		return false
	}
	if query.TeamID > 0 && profile.TeamID != query.TeamID {
		return false
	}
	if query.AgentID > 0 && profile.UserID != query.AgentID {
		return false
	}
	if query.SquadID <= 0 {
		return true
	}
	return repositories.AgentTeamSquadMemberRepository.Take(sqls.DB(),
		"tenant_id = ? AND squad_id = ? AND agent_profile_id = ? AND status = ?",
		profile.TenantID, query.SquadID, profile.ID, enums.StatusOk) != nil
}

func (s *serviceAnalyticsService) sessionMatchesSourceQuery(tenantID, conversationID int64, sessionNo int, query ServiceAnalyticsQuery) bool {
	if query.StoreID <= 0 && query.WxWorkInstanceID <= 0 {
		return true
	}
	session := repositories.ConversationServiceSessionRepository.TakeByKey(sqls.DB(), tenantID, conversationID, normalizedSessionNo(sessionNo))
	if session == nil {
		return false
	}
	if query.StoreID > 0 && session.StoreID != query.StoreID {
		return false
	}
	return query.WxWorkInstanceID <= 0 || session.WxWorkInstanceID == query.WxWorkInstanceID
}

func (s *serviceAnalyticsService) applyQualityMetrics(
	summary *ServiceAnalyticsSummary,
	agents map[int64]*ServiceAnalyticsAgent,
	sources map[[2]int64]*ServiceAnalyticsSource,
	sourceKeysBySession map[analyticsAssignmentSessionKey][2]int64,
	tenantID int64,
	query ServiceAnalyticsQuery,
	operator *dto.AuthPrincipal,
) {
	assignments := repositories.ConversationAssignmentRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Gte("created_at", query.StartAt).
		Lte("created_at", query.EndAt).
		Where(`EXISTS (
			SELECT 1 FROM t_message m
			WHERE m.tenant_id = t_conversation_assignment.tenant_id
			  AND m.conversation_id = t_conversation_assignment.conversation_id
			  AND m.session_no = t_conversation_assignment.session_no
			  AND m.sender_type = ?
			  AND m.sender_id = t_conversation_assignment.to_user_id
			  AND m.recalled_at IS NULL
			  AND m.created_at >= t_conversation_assignment.created_at
			  AND (t_conversation_assignment.finished_at IS NULL OR m.created_at <= t_conversation_assignment.finished_at)
		)`, enums.IMSenderTypeAgent).
		Asc("created_at").Asc("id"))
	eligible := make(map[int64]*models.ConversationAssignment, len(assignments))
	for i := range assignments {
		assignment := &assignments[i]
		teamID := s.assignmentTeamID(assignment, tenantID)
		if !s.assignmentMatchesQuery(assignment, teamID, query, operator) {
			continue
		}
		eligible[assignment.ID] = assignment
		summary.QualityInspectableCount++
		s.ensureAgentMetric(agents, assignment.ToUserID, teamID, tenantID).QualityInspectableCount++
		if source := sourceForSession(sources, sourceKeysBySession, assignment.ConversationID, assignment.SessionNo); source != nil {
			source.QualityInspectableCount++
		}
	}
	if len(eligible) == 0 {
		return
	}
	assignmentIDs := make([]int64, 0, len(eligible))
	for assignmentID := range eligible {
		assignmentIDs = append(assignmentIDs, assignmentID)
	}
	inspectionsByAssignment := make(map[int64]models.QualityInspection, len(eligible))
	for _, inspection := range repositories.QualityInspectionRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("assignment_id", assignmentIDs).
		Asc("id")) {
		current, exists := inspectionsByAssignment[inspection.AssignmentID]
		if !exists || (current.Status != enums.QualityInspectionStatusCompleted && inspection.Status == enums.QualityInspectionStatusCompleted) ||
			(current.Status == inspection.Status && inspection.ID > current.ID) {
			inspectionsByAssignment[inspection.AssignmentID] = inspection
		}
	}
	var total float64
	for assignmentID, assignment := range eligible {
		teamID := s.assignmentTeamID(assignment, tenantID)
		agent := s.ensureAgentMetric(agents, assignment.ToUserID, teamID, tenantID)
		inspection, exists := inspectionsByAssignment[assignmentID]
		if !exists || inspection.Status != enums.QualityInspectionStatusCompleted {
			summary.QualityPendingCount++
			agent.QualityPendingCount++
			continue
		}
		summary.QualityInspectionCount++
		agent.QualityInspectionCount++
		source := sourceForSession(sources, sourceKeysBySession, assignment.ConversationID, assignment.SessionNo)
		if source != nil {
			source.QualityInspectionCount++
		}
		switch inspection.Result {
		case enums.QualityInspectionResultExcellent, enums.QualityInspectionResultPassed:
			summary.QualityPassedCount++
			agent.QualityPassedCount++
			if source != nil {
				source.QualityPassedCount++
			}
		case enums.QualityInspectionResultFailed:
			summary.QualityFailedCount++
			agent.QualityFailedCount++
		}
		if inspection.MaxScore <= 0 {
			continue
		}
		normalized := float64(inspection.TotalScore) * 100 / float64(inspection.MaxScore)
		total += normalized
		agent.qualityScoreTotal += normalized
		agent.qualitySamples++
		if source != nil {
			source.qualityScoreTotal += normalized
		}
	}
	if summary.QualityInspectableCount > 0 {
		summary.QualityCoverageRate = percentage(summary.QualityInspectionCount, summary.QualityInspectableCount)
	}
	if summary.QualityInspectionCount > 0 {
		summary.QualityPassRate = percentage(summary.QualityPassedCount, summary.QualityInspectionCount)
		summary.AverageQualityScore = total / float64(summary.QualityInspectionCount)
	}
}

func (s *serviceAnalyticsService) applyEvaluationMetrics(
	summary *ServiceAnalyticsSummary,
	agents map[int64]*ServiceAnalyticsAgent,
	sources map[[2]int64]*ServiceAnalyticsSource,
	sourceKeysBySession map[analyticsAssignmentSessionKey][2]int64,
	tenantID int64,
	query ServiceAnalyticsQuery,
	operator *dto.AuthPrincipal,
	policy models.ServiceAnalyticsPolicy,
) {
	evaluations := repositories.ConversationEvaluationRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).Gte("invited_at", query.StartAt).Lte("invited_at", query.EndAt).Asc("id"))
	var ratingTotal int64
	for i := range evaluations {
		item := &evaluations[i]
		session := repositories.ConversationServiceSessionRepository.TakeByKey(sqls.DB(), tenantID, item.ConversationID, normalizedSessionNo(item.SessionNo))
		if session == nil || !s.sessionMatchesAnalyticsScope(session, query, operator) {
			continue
		}
		agentID := session.AssignedAgentID
		teamID := session.AssignedTeamID
		if item.AssignmentID > 0 {
			if assignment := repositories.ConversationAssignmentRepository.Get(sqls.DB(), item.AssignmentID); assignment != nil && assignment.TenantID == tenantID {
				agentID = assignment.ToUserID
				teamID = s.assignmentTeamID(assignment, tenantID)
			}
		}
		var agent *ServiceAnalyticsAgent
		if agentID > 0 && s.canViewAgent(operator, agentID, teamID) {
			agent = s.ensureAgentMetric(agents, agentID, teamID, tenantID)
			agent.EvaluationInviteCount++
		}
		source := sourceForSession(sources, sourceKeysBySession, item.ConversationID, item.SessionNo)
		if source != nil {
			source.EvaluationInviteCount++
		}
		summary.EvaluationInviteCount++
		if item.Status != enums.ConversationEvaluationStatusSubmitted || item.SubmittedAt == nil {
			continue
		}
		summary.EvaluationSubmittedCount++
		ratingTotal += int64(item.Rating)
		if agent != nil {
			agent.EvaluationSubmittedCount++
			agent.evaluationRatingTotal += int64(item.Rating)
		}
		if source != nil {
			source.EvaluationSubmittedCount++
			source.evaluationRatingTotal += int64(item.Rating)
		}
		if item.Rating >= policy.SatisfactionThreshold {
			summary.SatisfiedCount++
			if agent != nil {
				agent.SatisfiedCount++
			}
			if source != nil {
				source.SatisfiedCount++
			}
		}
	}
	if summary.EvaluationInviteCount > 0 {
		summary.EvaluationParticipationRate = percentage(summary.EvaluationSubmittedCount, summary.EvaluationInviteCount)
	}
	if summary.EvaluationSubmittedCount > 0 {
		summary.SatisfactionRate = percentage(summary.SatisfiedCount, summary.EvaluationSubmittedCount)
		summary.AverageSatisfaction = float64(ratingTotal) / float64(summary.EvaluationSubmittedCount)
	}
}

type analyticsAssignmentSessionKey struct {
	ConversationID int64
	SessionNo      int
}

func sourceForSession(
	sources map[[2]int64]*ServiceAnalyticsSource,
	keys map[analyticsAssignmentSessionKey][2]int64,
	conversationID int64,
	sessionNo int,
) *ServiceAnalyticsSource {
	key, exists := keys[analyticsAssignmentSessionKey{ConversationID: conversationID, SessionNo: normalizedSessionNo(sessionNo)}]
	if !exists {
		return nil
	}
	return sources[key]
}

func assignmentForAnalyticsMessage(assignments []models.ConversationAssignment, message *models.Message) *models.ConversationAssignment {
	if message == nil || message.SenderID <= 0 {
		return nil
	}
	at := message.CreatedAt
	if message.SentAt != nil {
		at = *message.SentAt
	}
	var matched *models.ConversationAssignment
	for i := range assignments {
		assignment := &assignments[i]
		if assignment.ToUserID != message.SenderID || assignment.CreatedAt.After(at) || (assignment.FinishedAt != nil && at.After(*assignment.FinishedAt)) {
			continue
		}
		if matched == nil || assignment.CreatedAt.After(matched.CreatedAt) || (assignment.CreatedAt.Equal(matched.CreatedAt) && assignment.ID > matched.ID) {
			matched = assignment
		}
	}
	return matched
}

func (s *serviceAnalyticsService) assignmentTeamID(assignment *models.ConversationAssignment, tenantID int64) int64 {
	if assignment == nil || assignment.TenantID != tenantID {
		return 0
	}
	if assignment.SquadID > 0 {
		if squad := repositories.AgentTeamSquadRepository.GetInTenant(sqls.DB(), assignment.SquadID, tenantID); squad != nil {
			return squad.TeamID
		}
	}
	if decision := repositories.DispatchDecisionLogRepository.TakeByAssignment(sqls.DB(), tenantID, assignment.ID); decision != nil && decision.SelectedTeamID > 0 {
		return decision.SelectedTeamID
	}
	if session := repositories.ConversationServiceSessionRepository.TakeByKey(sqls.DB(), tenantID, assignment.ConversationID, normalizedSessionNo(assignment.SessionNo)); session != nil && session.LastAssignmentID == assignment.ID && session.AssignedTeamID > 0 {
		return session.AssignedTeamID
	}
	if profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", tenantID, assignment.ToUserID); profile != nil {
		return profile.TeamID
	}
	return 0
}

func (s *serviceAnalyticsService) dispatchMetrics(tenantID int64, query ServiceAnalyticsQuery, operator *dto.AuthPrincipal, sessions []models.ConversationServiceSession) ServiceAnalyticsDispatch {
	ret := ServiceAnalyticsDispatch{}
	logs := repositories.DispatchDecisionLogRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Gte("decided_at", query.StartAt).Lte("decided_at", query.EndAt))
	for _, item := range logs {
		if !AgentTeamScopeService.IsAdmin(operator) {
			if item.SelectedTeamID <= 0 || !s.canViewAgent(operator, item.SelectedUserID, item.SelectedTeamID) {
				continue
			}
		}
		if query.AgentID > 0 && item.SelectedUserID != query.AgentID {
			continue
		}
		if query.TeamID > 0 && item.SelectedTeamID != query.TeamID {
			continue
		}
		if query.SquadID > 0 && item.SelectedSquadID != query.SquadID {
			continue
		}
		if !s.sessionMatchesSourceQuery(tenantID, item.ConversationID, item.SessionNo, query) {
			continue
		}
		ret.DecisionCount++
		if item.AssignmentID > 0 && item.SelectedUserID > 0 {
			ret.SelectedCount++
		}
		switch strings.TrimSpace(item.DecisionMode) {
		case "rule":
			ret.AutoCount++
			ret.RuleCount++
		case "model", "intelligent":
			ret.AutoCount++
			ret.ModelCount++
		case "hybrid", "auto":
			ret.AutoCount++
			ret.HybridCount++
		default:
			ret.ManualCount++
		}
		switch item.Status {
		case enums.DispatchDecisionStatusFallback:
			ret.FallbackCount++
		case enums.DispatchDecisionStatusFailed:
			ret.FailedCount++
		case enums.DispatchDecisionStatusOverride:
			ret.OverrideCount++
		case enums.DispatchDecisionStatusStale:
			ret.StaleCount++
		}
		ret.AverageDecisionLatencyMillis += float64(item.DecisionLatencyMillis)
	}
	for _, session := range sessions {
		ret.TransferCount += int64(session.TransferCount)
	}
	if ret.DecisionCount > 0 {
		ret.AutoRate = percentage(ret.AutoCount, ret.DecisionCount)
		ret.AverageDecisionLatencyMillis /= float64(ret.DecisionCount)
	}
	return ret
}

func normalizeAnalyticsQuery(query ServiceAnalyticsQuery) ServiceAnalyticsQuery {
	now := time.Now()
	if query.EndAt.IsZero() {
		query.EndAt = now
	}
	if query.StartAt.IsZero() {
		query.StartAt = query.EndAt.AddDate(0, 0, -6)
	}
	if query.EndAt.Before(query.StartAt) {
		query.StartAt, query.EndAt = query.EndAt, query.StartAt
	}
	return query
}

func percentilePair(values []int64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return nearestRankPercentile(ordered, 0.5), nearestRankPercentile(ordered, 0.9)
}

func nearestRankPercentile(sortedValues []int64, percentile float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	if percentile <= 0 {
		return float64(sortedValues[0])
	}
	if percentile >= 1 {
		return float64(sortedValues[len(sortedValues)-1])
	}
	index := int(float64(len(sortedValues))*percentile+0.999999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sortedValues) {
		index = len(sortedValues) - 1
	}
	return float64(sortedValues[index])
}

func countRepeatConsultations(sessions []models.ConversationServiceSession, window time.Duration) int64 {
	if window <= 0 {
		return 0
	}
	ordered := append([]models.ConversationServiceSession(nil), sessions...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].CustomerID == ordered[j].CustomerID {
			return ordered[i].StartedAt.Before(ordered[j].StartedAt)
		}
		return ordered[i].CustomerID < ordered[j].CustomerID
	})
	lastByCustomer := map[int64]time.Time{}
	count := int64(0)
	for _, session := range ordered {
		if session.CustomerID <= 0 {
			continue
		}
		if previous, ok := lastByCustomer[session.CustomerID]; ok {
			delta := session.StartedAt.Sub(previous)
			if delta >= 0 && delta <= window {
				count++
			}
		}
		lastByCustomer[session.CustomerID] = session.StartedAt
	}
	return count
}

type durationBucket struct {
	key   string
	label string
	min   int64
	max   int64
}

func firstReplyBuckets() []durationBucket {
	return []durationBucket{
		{key: "lt15", label: "15秒内", min: 0, max: 15},
		{key: "15to30", label: "15-30秒", min: 15, max: 30},
		{key: "30to60", label: "30-60秒", min: 30, max: 60},
		{key: "1to3m", label: "1-3分钟", min: 60, max: 180},
		{key: "3to5m", label: "3-5分钟", min: 180, max: 300},
		{key: "gte5m", label: "5分钟以上", min: 300, max: -1},
	}
}

func responseBuckets() []durationBucket {
	return []durationBucket{
		{key: "lt30", label: "30秒内", min: 0, max: 30},
		{key: "30to60", label: "30-60秒", min: 30, max: 60},
		{key: "1to3m", label: "1-3分钟", min: 60, max: 180},
		{key: "3to5m", label: "3-5分钟", min: 180, max: 300},
		{key: "5to10m", label: "5-10分钟", min: 300, max: 600},
		{key: "gte10m", label: "10分钟以上", min: 600, max: -1},
	}
}

func sessionDurationBuckets() []durationBucket {
	return []durationBucket{
		{key: "lt2m", label: "2分钟内", min: 0, max: 120},
		{key: "2to5m", label: "2-5分钟", min: 120, max: 300},
		{key: "5to10m", label: "5-10分钟", min: 300, max: 600},
		{key: "10to30m", label: "10-30分钟", min: 600, max: 1800},
		{key: "30to60m", label: "30-60分钟", min: 1800, max: 3600},
		{key: "gte60m", label: "60分钟以上", min: 3600, max: -1},
	}
}

func buildDurationDistribution(values []int64, buckets []durationBucket) []ServiceAnalyticsDistribution {
	results := make([]ServiceAnalyticsDistribution, len(buckets))
	for i, bucket := range buckets {
		results[i] = ServiceAnalyticsDistribution{Key: bucket.key, Label: bucket.label}
	}
	for _, value := range values {
		if value < 0 {
			continue
		}
		for i, bucket := range buckets {
			if value >= bucket.min && (bucket.max < 0 || value < bucket.max) {
				results[i].Count++
				break
			}
		}
	}
	for i := range results {
		results[i].Rate = percentage(results[i].Count, int64(len(values)))
	}
	return results
}

func percentage(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) * 100 / float64(denominator)
}
