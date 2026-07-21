package services

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

type ServiceAnalyticsBackfillResult struct {
	Sessions          int
	ResponseSpans     int
	DispatchDecisions int
	Policies          int
	QualityTemplates  int
}

func (s *serviceAnalyticsCaptureService) BackfillMissingFacts() (ServiceAnalyticsBackfillResult, error) {
	result := ServiceAnalyticsBackfillResult{}
	for _, tenant := range repositories.TenantRepository.Find(sqls.DB(), sqls.NewCnd().Where("status <> ?", enums.StatusDeleted).Asc("id")) {
		if repositories.ServiceAnalyticsPolicyRepository.TakeByTenant(sqls.DB(), tenant.ID) == nil {
			policy := ServiceAnalyticsService.GetPolicy(tenant.ID)
			policy.AuditFields = utils.BuildAuditFields(nil)
			if err := repositories.ServiceAnalyticsPolicyRepository.Create(sqls.DB(), &policy); err != nil {
				return result, err
			}
			result.Policies++
		}
		if repositories.QualityTemplateRepository.FindDefault(sqls.DB(), tenant.ID) == nil {
			if _, err := QualityInspectionService.EnsureDefaultTemplate(tenant.ID); err != nil {
				return result, err
			}
			result.QualityTemplates++
		}
	}
	lastID := int64(0)
	for {
		conversations := repositories.ConversationRepository.Find(sqls.DB(), sqls.NewCnd().
			Where("tenant_id > ?", 0).
			Gt("id", lastID).
			Asc("id").
			Page(1, 200))
		if len(conversations) == 0 {
			break
		}
		for i := range conversations {
			conversation := conversations[i]
			if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
				created, err := s.backfillConversationFactsDB(ctx.Tx, &conversation)
				if err == nil {
					result.Sessions += created.Sessions
					result.ResponseSpans += created.ResponseSpans
					result.DispatchDecisions += created.DispatchDecisions
				}
				return err
			}); err != nil {
				return result, err
			}
			lastID = conversation.ID
		}
		if len(conversations) < 200 {
			break
		}
	}
	return result, nil
}

func (s *serviceAnalyticsCaptureService) backfillConversationFactsDB(db *gorm.DB, conversation *models.Conversation) (ServiceAnalyticsBackfillResult, error) {
	result := ServiceAnalyticsBackfillResult{}
	messages := repositories.MessageRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("conversation_id", conversation.ID).
		Asc("session_no").Asc("created_at").Asc("id"))
	assignments := repositories.ConversationAssignmentRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("conversation_id", conversation.ID).
		Asc("session_no").Asc("created_at").Asc("id"))
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)

	messagesBySession := make(map[int][]models.Message)
	assignmentsBySession := make(map[int][]models.ConversationAssignment)
	sessionNos := make(map[int]struct{})
	for _, message := range messages {
		sessionNo := normalizedSessionNo(message.SessionNo)
		messagesBySession[sessionNo] = append(messagesBySession[sessionNo], message)
		sessionNos[sessionNo] = struct{}{}
	}
	for _, assignment := range assignments {
		sessionNo := normalizedSessionNo(assignment.SessionNo)
		assignmentsBySession[sessionNo] = append(assignmentsBySession[sessionNo], assignment)
		sessionNos[sessionNo] = struct{}{}
	}
	if route != nil {
		sessionNos[normalizedSessionNo(route.SessionNo)] = struct{}{}
	}
	if len(sessionNos) == 0 {
		sessionNos[1] = struct{}{}
	}

	orderedSessionNos := make([]int, 0, len(sessionNos))
	for sessionNo := range sessionNos {
		orderedSessionNos = append(orderedSessionNos, sessionNo)
	}
	sort.Ints(orderedSessionNos)
	for _, sessionNo := range orderedSessionNos {
		sessionMessages := messagesBySession[sessionNo]
		sessionAssignments := assignmentsBySession[sessionNo]
		sort.SliceStable(sessionMessages, func(i, j int) bool {
			left, right := serviceAnalyticsMessageTime(&sessionMessages[i]), serviceAnalyticsMessageTime(&sessionMessages[j])
			if left.Equal(right) {
				return sessionMessages[i].ID < sessionMessages[j].ID
			}
			return left.Before(right)
		})
		sort.SliceStable(sessionAssignments, func(i, j int) bool {
			if sessionAssignments[i].CreatedAt.Equal(sessionAssignments[j].CreatedAt) {
				return sessionAssignments[i].ID < sessionAssignments[j].ID
			}
			return sessionAssignments[i].CreatedAt.Before(sessionAssignments[j].CreatedAt)
		})

		session := repositories.ConversationServiceSessionRepository.TakeByKey(db, conversation.TenantID, conversation.ID, sessionNo)
		if session == nil {
			built := s.buildHistoricalSessionDB(db, conversation, route, sessionNo, sessionMessages, sessionAssignments)
			if err := repositories.ConversationServiceSessionRepository.Create(db, built); err != nil {
				return result, err
			}
			session = built
			result.Sessions++
		}
		spanCount := repositories.ConversationResponseSpanRepository.Count(db, sqls.NewCnd().
			Eq("tenant_id", conversation.TenantID).
			Eq("conversation_id", conversation.ID).
			Eq("session_no", sessionNo))
		if spanCount == 0 {
			created, err := s.backfillResponseSpansDB(db, session, sessionMessages, sessionAssignments)
			if err != nil {
				return result, err
			}
			result.ResponseSpans += created
		}
	}
	for i := range assignments {
		created, err := s.backfillDispatchDecisionDB(db, conversation, &assignments[i])
		if err != nil {
			return result, err
		}
		if created {
			result.DispatchDecisions++
		}
	}
	return result, nil
}

func (s *serviceAnalyticsCaptureService) buildHistoricalSessionDB(
	db *gorm.DB,
	conversation *models.Conversation,
	route *models.ConversationRouteState,
	sessionNo int,
	messages []models.Message,
	assignments []models.ConversationAssignment,
) *models.ConversationServiceSession {
	startedAt := conversation.CreatedAt
	if len(messages) > 0 {
		startedAt = serviceAnalyticsMessageTime(&messages[0])
	} else if len(assignments) > 0 {
		startedAt = assignments[0].CreatedAt
	} else if route != nil && route.SessionNo == sessionNo && route.SessionStartedAt != nil {
		startedAt = *route.SessionStartedAt
	}
	if startedAt.IsZero() {
		startedAt = conversation.UpdatedAt
	}
	item := &models.ConversationServiceSession{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: sessionNo,
		CustomerID: conversation.CustomerID, ChannelID: conversation.ChannelID, ServiceMode: conversation.ServiceMode,
		Status: enums.ServiceSessionStatusOpen, StartedAt: startedAt,
		FactOrigin: enums.AnalyticsFactOriginBackfill, DataQuality: enums.AnalyticsDataQualityEstimated,
		EstimatedFieldsJSON: `["startedAt","queueEnteredAt","assignmentSessionNo","endedAt"]`,
		AuditFields:         utils.BuildAuditFields(nil),
	}
	if len(messages) == 0 {
		item.DataQuality = enums.AnalyticsDataQualityIncomplete
		item.EstimatedFieldsJSON = `["startedAt","queueEnteredAt","assignmentSessionNo","messageCounts","endedAt"]`
	}
	item.CreatedAt = startedAt
	item.UpdatedAt = startedAt
	if route != nil {
		item.StoreID = route.StoreID
		item.WxWorkInstanceID = route.WxWorkInstanceID
	}
	for i := range messages {
		message := &messages[i]
		switch message.SenderType {
		case enums.IMSenderTypeCustomer:
			item.CustomerMessageCount++
		case enums.IMSenderTypeAI:
			item.AIMessageCount++
		case enums.IMSenderTypeAgent:
			item.HumanMessageCount++
		case enums.IMSenderTypeSystem:
			item.SystemMessageCount++
		}
		at := serviceAnalyticsMessageTime(message)
		if item.LastMessageAt == nil || at.After(*item.LastMessageAt) || (at.Equal(*item.LastMessageAt) && message.ID > item.LastMessageID) {
			item.LastMessageID = message.ID
			item.LastMessageAt = timePointer(at)
		}
	}
	item.AIHandled = item.AIMessageCount > 0
	item.HumanHandled = item.HumanMessageCount > 0
	item.AssignmentCount = len(assignments)
	for _, assignment := range assignments {
		if assignment.AssignType == string(enums.IMAssignmentTypeTransfer) {
			item.TransferCount++
		}
	}
	if len(assignments) > 0 {
		first, last := assignments[0], assignments[len(assignments)-1]
		item.FirstAssignmentID = first.ID
		item.LastAssignmentID = last.ID
		item.AssignedAt = timePointer(first.CreatedAt)
		item.AssignedSquadID = last.SquadID
		item.AssignedAgentID = last.ToUserID
		if profile := repositories.AgentProfileRepository.Take(db, "tenant_id = ? AND user_id = ?", conversation.TenantID, last.ToUserID); profile != nil {
			item.AssignedTeamID = profile.TeamID
		} else {
			item.AssignedTeamID = conversation.CurrentTeamID
		}
	}
	item.QueueEnteredAt = historicalQueueEntry(conversation, route, sessionNo, startedAt, item.AssignedAt)
	if item.QueueEnteredAt != nil && item.AssignedAt != nil {
		item.QueueSeconds = nonNegativeSeconds(*item.QueueEnteredAt, *item.AssignedAt)
	}
	if item.AssignedAt != nil {
		for i := range messages {
			message := &messages[i]
			at := serviceAnalyticsMessageTime(message)
			if message.SenderType != enums.IMSenderTypeAgent || at.Before(*item.AssignedAt) {
				continue
			}
			item.FirstHumanReplyAt = timePointer(at)
			item.FirstResponseSeconds = nonNegativeSeconds(*item.AssignedAt, at)
			if item.QueueEnteredAt != nil {
				item.TotalHumanWaitSeconds = nonNegativeSeconds(*item.QueueEnteredAt, at)
			}
			break
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].SenderType == enums.IMSenderTypeAgent {
			item.LastHumanReplyAt = timePointer(serviceAnalyticsMessageTime(&messages[i]))
			break
		}
	}
	closed := conversation.Status == enums.IMConversationStatusClosed || (route != nil && normalizedSessionNo(route.SessionNo) > sessionNo)
	if closed {
		item.Status = enums.ServiceSessionStatusClosed
		endedAt := conversation.ClosedAt
		if endedAt == nil || (item.LastMessageAt != nil && item.LastMessageAt.Before(*endedAt) && route != nil && route.SessionNo > sessionNo) {
			endedAt = item.LastMessageAt
		}
		if endedAt == nil && len(assignments) > 0 {
			endedAt = assignments[len(assignments)-1].FinishedAt
		}
		item.EndedAt = endedAt
		item.CloseReason = conversation.CloseReason
	}
	return item
}

type historicalWaitingSpan struct {
	startMessageID int64
	endMessageID   int64
	messageCount   int
	startedAt      time.Time
}

func (s *serviceAnalyticsCaptureService) backfillResponseSpansDB(
	db *gorm.DB,
	session *models.ConversationServiceSession,
	messages []models.Message,
	assignments []models.ConversationAssignment,
) (int, error) {
	if session == nil || session.QueueEnteredAt == nil || len(assignments) == 0 {
		return 0, nil
	}
	queueAt := *session.QueueEnteredAt
	var waiting *historicalWaitingSpan
	for i := range messages {
		message := &messages[i]
		at := serviceAnalyticsMessageTime(message)
		if at.After(queueAt) {
			break
		}
		if message.SenderType == enums.IMSenderTypeCustomer {
			waiting = &historicalWaitingSpan{startMessageID: message.ID, endMessageID: message.ID, messageCount: 1, startedAt: queueAt}
		}
	}
	created := 0
	for i := range messages {
		message := &messages[i]
		at := serviceAnalyticsMessageTime(message)
		if at.Before(queueAt) || (at.Equal(queueAt) && waiting != nil && message.ID == waiting.startMessageID) {
			continue
		}
		switch message.SenderType {
		case enums.IMSenderTypeCustomer:
			if waiting == nil {
				waiting = &historicalWaitingSpan{startMessageID: message.ID, endMessageID: message.ID, messageCount: 1, startedAt: at}
			} else if message.ID > waiting.endMessageID {
				waiting.endMessageID = message.ID
				waiting.messageCount++
			}
		case enums.IMSenderTypeAgent:
			if waiting == nil {
				continue
			}
			assignment := historicalAssignmentAt(assignments, message.SenderID, at)
			if assignment == nil {
				continue
			}
			span := s.buildHistoricalResponseSpanDB(db, session, waiting, assignment)
			span.RepliedAt = timePointer(at)
			span.ReplyMessageID = message.ID
			span.WaitSeconds = nonNegativeSeconds(waiting.startedAt, at)
			span.Status = enums.ResponseSpanStatusReplied
			if err := repositories.ConversationResponseSpanRepository.Create(db, span); err != nil {
				return created, err
			}
			created++
			waiting = nil
		}
	}
	if waiting != nil {
		at := time.Now()
		status := enums.ResponseSpanStatusWaiting
		if session.Status == enums.ServiceSessionStatusClosed {
			status = enums.ResponseSpanStatusAbandoned
			if session.EndedAt != nil {
				at = *session.EndedAt
			} else if session.LastMessageAt != nil {
				at = *session.LastMessageAt
			}
		}
		assignment := historicalAssignmentAt(assignments, 0, at)
		if assignment != nil {
			span := s.buildHistoricalResponseSpanDB(db, session, waiting, assignment)
			span.Status = status
			if status == enums.ResponseSpanStatusAbandoned {
				span.WaitSeconds = nonNegativeSeconds(waiting.startedAt, at)
			}
			if err := repositories.ConversationResponseSpanRepository.Create(db, span); err != nil {
				return created, err
			}
			created++
		}
	}
	return created, nil
}

func (s *serviceAnalyticsCaptureService) buildHistoricalResponseSpanDB(
	db *gorm.DB,
	session *models.ConversationServiceSession,
	waiting *historicalWaitingSpan,
	assignment *models.ConversationAssignment,
) *models.ConversationResponseSpan {
	span := &models.ConversationResponseSpan{
		TenantID: session.TenantID, ConversationID: session.ConversationID, SessionNo: session.SessionNo,
		AssignmentID: assignment.ID, SquadID: assignment.SquadID, AgentID: assignment.ToUserID,
		CustomerStartMessageID: waiting.startMessageID, CustomerEndMessageID: waiting.endMessageID,
		CustomerMessageCount: waiting.messageCount, StartedAt: waiting.startedAt,
		Status:     enums.ResponseSpanStatusWaiting,
		FactOrigin: enums.AnalyticsFactOriginBackfill, DataQuality: enums.AnalyticsDataQualityEstimated,
		EstimatedFieldsJSON: `["startedAt","assignmentSessionNo"]`,
		AuditFields:         utils.BuildAuditFields(nil),
	}
	span.CreatedAt = waiting.startedAt
	span.UpdatedAt = waiting.startedAt
	if profile := repositories.AgentProfileRepository.Take(db, "tenant_id = ? AND user_id = ?", session.TenantID, assignment.ToUserID); profile != nil {
		span.TeamID = profile.TeamID
	}
	return span
}

func (s *serviceAnalyticsCaptureService) backfillDispatchDecisionDB(db *gorm.DB, conversation *models.Conversation, assignment *models.ConversationAssignment) (bool, error) {
	if assignment == nil || assignment.ID <= 0 || repositories.DispatchDecisionLogRepository.TakeByAssignment(db, conversation.TenantID, assignment.ID) != nil {
		return false, nil
	}
	mode := string(assignment.DispatchMode)
	if mode == "" {
		mode = string(enums.AgentTeamDispatchModeManual)
		if assignment.OperatorID == 0 && strings.Contains(assignment.Reason, "自动") {
			mode = string(enums.AgentTeamDispatchModeRule)
		}
	}
	status := enums.DispatchDecisionStatusSelected
	if assignment.AssignType == string(enums.IMAssignmentTypeTransfer) {
		status = enums.DispatchDecisionStatusOverride
	}
	teamID := int64(0)
	if profile := repositories.AgentProfileRepository.Take(db, "tenant_id = ? AND user_id = ?", conversation.TenantID, assignment.ToUserID); profile != nil {
		teamID = profile.TeamID
	}
	candidateSnapshot := fmt.Sprintf(`[{"userId":%d,"workloadWeight":%d}]`, assignment.ToUserID, assignment.WorkloadWeight)
	if assignment.DecisionConfidence > 0 {
		candidateSnapshot = fmt.Sprintf(`[{"userId":%d,"decisionConfidence":%d,"workloadWeight":%d}]`, assignment.ToUserID, assignment.DecisionConfidence, assignment.WorkloadWeight)
	}
	item := &models.DispatchDecisionLog{
		TenantID: conversation.TenantID, ConversationID: conversation.ID, SessionNo: normalizedSessionNo(assignment.SessionNo),
		DecisionKey: fmt.Sprintf("assignment:%d", assignment.ID), AssignmentID: assignment.ID,
		Trigger: "backfill", DecisionMode: mode, Status: status,
		CandidateUserIDsJSON:  fmt.Sprintf("[%d]", assignment.ToUserID),
		CandidateSnapshotJSON: candidateSnapshot,
		InputLastMessageID:    conversation.LastMessageID,
		SelectedUserID:        assignment.ToUserID, SelectedTeamID: teamID, SelectedSquadID: assignment.SquadID,
		Reason: strings.TrimSpace(assignment.Reason), OperatorID: assignment.OperatorID, DecidedAt: assignment.CreatedAt,
		AuditFields: utils.BuildAuditFields(nil),
	}
	return true, repositories.DispatchDecisionLogRepository.Create(db, item)
}

func historicalQueueEntry(conversation *models.Conversation, route *models.ConversationRouteState, sessionNo int, startedAt time.Time, assignedAt *time.Time) *time.Time {
	if route != nil && normalizedSessionNo(route.SessionNo) == sessionNo && route.LastManualHandoffAt != nil {
		return timePointer(*route.LastManualHandoffAt)
	}
	if conversation.HandoffAt != nil && !conversation.HandoffAt.Before(startedAt) && (assignedAt == nil || !conversation.HandoffAt.After(*assignedAt)) {
		return timePointer(*conversation.HandoffAt)
	}
	if assignedAt != nil {
		return timePointer(*assignedAt)
	}
	return nil
}

func historicalAssignmentAt(assignments []models.ConversationAssignment, agentID int64, at time.Time) *models.ConversationAssignment {
	var selected *models.ConversationAssignment
	for i := range assignments {
		assignment := &assignments[i]
		if assignment.CreatedAt.After(at) || (assignment.FinishedAt != nil && assignment.FinishedAt.Before(at)) {
			continue
		}
		if agentID > 0 && assignment.ToUserID != agentID {
			continue
		}
		selected = assignment
	}
	return selected
}

func serviceAnalyticsMessageTime(message *models.Message) time.Time {
	if message != nil && message.SentAt != nil {
		return *message.SentAt
	}
	if message != nil {
		return message.CreatedAt
	}
	return time.Time{}
}

func normalizedSessionNo(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func LogServiceAnalyticsBackfill(result ServiceAnalyticsBackfillResult) {
	slog.Info("service analytics facts backfilled",
		"sessions", result.Sessions,
		"responseSpans", result.ResponseSpans,
		"dispatchDecisions", result.DispatchDecisions,
		"policies", result.Policies,
		"qualityTemplates", result.QualityTemplates,
	)
}
