package services

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func (s *serviceAnalyticsCaptureService) RecordMessage(message *models.Message) error {
	if message == nil || message.ID <= 0 || message.TenantID <= 0 || message.ConversationID <= 0 {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation := repositories.ConversationRepository.GetInTenant(ctx.Tx, message.ConversationID, message.TenantID)
		if conversation == nil {
			return nil
		}
		at := message.CreatedAt
		if message.SentAt != nil {
			at = *message.SentAt
		}
		session, err := s.ensureSessionDB(ctx.Tx, conversation, message.SessionNo, at)
		if err != nil {
			return err
		}
		if err := s.refreshMessageFactsDB(ctx.Tx, session, message, at); err != nil {
			return err
		}
		switch message.SenderType {
		case enums.IMSenderTypeCustomer:
			return s.recordCustomerWaitingSpanDB(ctx.Tx, session, message, at)
		case enums.IMSenderTypeAgent:
			return s.recordHumanReplyDB(ctx.Tx, session, message, at)
		default:
			return nil
		}
	})
}

func (s *serviceAnalyticsCaptureService) RecordQueueEntry(conversationID int64, at time.Time) error {
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	if conversation == nil || conversation.TenantID <= 0 {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return s.recordQueueEntryDB(ctx.Tx, conversation, at)
	})
}

func (s *serviceAnalyticsCaptureService) RecordQueueEntryWithDB(db *gorm.DB, conversationID, tenantID int64, at time.Time) error {
	if db == nil || conversationID <= 0 || tenantID <= 0 {
		return nil
	}
	conversation := repositories.ConversationRepository.GetInTenant(db, conversationID, tenantID)
	if conversation == nil {
		return nil
	}
	return s.recordQueueEntryDB(db, conversation, at)
}

func (s *serviceAnalyticsCaptureService) recordQueueEntryDB(db *gorm.DB, conversation *models.Conversation, at time.Time) error {
	sessionNo := currentSessionNoDB(db, conversation.ID, conversation.TenantID)
	session, err := s.ensureSessionDB(db, conversation, sessionNo, at)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"assigned_team_id": conversation.CurrentTeamID,
		"updated_at":       at,
		"update_user_name": "system",
	}
	session.AssignedTeamID = conversation.CurrentTeamID
	if session.QueueEnteredAt == nil {
		updates["queue_entered_at"] = at
		session.QueueEnteredAt = timePointer(at)
	}
	if err := repositories.ConversationServiceSessionRepository.UpdatesInTenant(db, session.ID, session.TenantID, updates); err != nil {
		return err
	}
	lastCustomer := repositories.MessageRepository.FindOne(db, sqls.NewCnd().
		Eq("tenant_id", session.TenantID).
		Eq("conversation_id", session.ConversationID).
		Eq("session_no", session.SessionNo).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Desc("id"))
	if lastCustomer == nil {
		return nil
	}
	return s.ensureWaitingSpanDB(db, session, lastCustomer, at)
}

func (s *serviceAnalyticsCaptureService) RecordCurrentAssignment(conversationID int64) error {
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	if conversation == nil || conversation.TenantID <= 0 {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		assignment := repositories.ConversationAssignmentRepository.FindOne(ctx.Tx, sqls.NewCnd().
			Eq("tenant_id", conversation.TenantID).
			Eq("conversation_id", conversation.ID).
			Eq("status", enums.IMAssignmentStatusActive).
			Desc("id"))
		if assignment == nil {
			return nil
		}
		sessionNo := assignment.SessionNo
		if sessionNo <= 0 {
			sessionNo = currentSessionNoDB(ctx.Tx, conversation.ID, conversation.TenantID)
		}
		session, err := s.ensureSessionDB(ctx.Tx, conversation, sessionNo, assignment.CreatedAt)
		if err != nil {
			return err
		}
		profile := repositories.AgentProfileRepository.Take(ctx.Tx, "tenant_id = ? AND user_id = ?", conversation.TenantID, assignment.ToUserID)
		teamID := conversation.CurrentTeamID
		if profile != nil && profile.TeamID > 0 {
			teamID = profile.TeamID
		}
		assignmentCount := repositories.ConversationAssignmentRepository.Count(ctx.Tx, sqls.NewCnd().
			Eq("tenant_id", conversation.TenantID).
			Eq("conversation_id", conversation.ID).
			Eq("session_no", sessionNo))
		transferCount := repositories.ConversationAssignmentRepository.Count(ctx.Tx, sqls.NewCnd().
			Eq("tenant_id", conversation.TenantID).
			Eq("conversation_id", conversation.ID).
			Eq("session_no", sessionNo).
			Eq("assign_type", string(enums.IMAssignmentTypeTransfer)))
		updates := map[string]any{
			"last_assignment_id": assignment.ID,
			"assigned_team_id":   teamID,
			"assigned_squad_id":  assignment.SquadID,
			"assigned_agent_id":  assignment.ToUserID,
			"assignment_count":   assignmentCount,
			"transfer_count":     transferCount,
			"updated_at":         assignment.CreatedAt,
			"update_user_name":   "system",
		}
		if session.FirstAssignmentID <= 0 || session.AssignedAt == nil {
			firstAssignment := repositories.ConversationAssignmentRepository.FindOne(ctx.Tx, sqls.NewCnd().
				Eq("tenant_id", conversation.TenantID).
				Eq("conversation_id", conversation.ID).
				Eq("session_no", sessionNo).
				Asc("created_at").Asc("id"))
			if firstAssignment != nil {
				updates["first_assignment_id"] = firstAssignment.ID
				updates["assigned_at"] = firstAssignment.CreatedAt
				if session.QueueEnteredAt != nil {
					updates["queue_seconds"] = nonNegativeSeconds(*session.QueueEnteredAt, firstAssignment.CreatedAt)
				}
			}
		}
		if err := repositories.ConversationServiceSessionRepository.UpdatesInTenant(ctx.Tx, session.ID, session.TenantID, updates); err != nil {
			return err
		}
		for _, span := range repositories.ConversationResponseSpanRepository.FindWaiting(ctx.Tx, session.TenantID, session.ConversationID, session.SessionNo) {
			if err := repositories.ConversationResponseSpanRepository.UpdatesInTenant(ctx.Tx, span.ID, span.TenantID, map[string]any{
				"assignment_id": assignment.ID,
				"team_id":       teamID,
				"squad_id":      assignment.SquadID,
				"agent_id":      assignment.ToUserID,
				"updated_at":    assignment.CreatedAt,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *serviceAnalyticsCaptureService) RecordDispatchDecision(conversationID, toUserID, operatorID int64, assignType, reason string) error {
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	if conversation == nil || conversation.TenantID <= 0 || toUserID <= 0 {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		assignmentType := enums.IMAssignmentTypeAssign
		if strings.TrimSpace(assignType) == "transfer" {
			assignmentType = enums.IMAssignmentTypeTransfer
		}
		assignment := repositories.ConversationAssignmentRepository.FindOne(ctx.Tx, sqls.NewCnd().
			Eq("tenant_id", conversation.TenantID).
			Eq("conversation_id", conversation.ID).
			Eq("to_user_id", toUserID).
			Eq("assign_type", string(assignmentType)).
			Desc("id"))
		if assignment == nil || repositories.DispatchDecisionLogRepository.TakeByAssignment(ctx.Tx, conversation.TenantID, assignment.ID) != nil {
			return nil
		}
		decisionMode := string(assignment.DispatchMode)
		if decisionMode == "" || (strings.TrimSpace(assignType) == "auto_assign" && assignment.DispatchMode == enums.AgentTeamDispatchModeManual) {
			if strings.TrimSpace(assignType) == "auto_assign" {
				decisionMode = string(enums.AgentTeamDispatchModeRule)
			} else {
				decisionMode = string(enums.AgentTeamDispatchModeManual)
			}
		}
		status := enums.DispatchDecisionStatusSelected
		if strings.TrimSpace(assignType) == "transfer" {
			status = enums.DispatchDecisionStatusOverride
		} else if strings.Contains(assignment.Reason, "降级") {
			status = enums.DispatchDecisionStatusFallback
		} else if strings.TrimSpace(assignType) != "auto_assign" {
			prior := repositories.DispatchDecisionLogRepository.Find(ctx.Tx, sqls.NewCnd().
				Eq("tenant_id", conversation.TenantID).
				Eq("conversation_id", conversation.ID).
				Eq("session_no", normalizedSessionNo(assignment.SessionNo)).
				Eq("assignment_id", 0).
				In("status", []enums.DispatchDecisionStatus{enums.DispatchDecisionStatusFallback, enums.DispatchDecisionStatusFailed, enums.DispatchDecisionStatusStale}).
				Desc("id").Limit(1))
			if len(prior) > 0 {
				status = enums.DispatchDecisionStatusOverride
			}
		}
		teamID := conversation.CurrentTeamID
		if profile := repositories.AgentProfileRepository.Take(ctx.Tx, "tenant_id = ? AND user_id = ?", conversation.TenantID, toUserID); profile != nil {
			teamID = profile.TeamID
		}
		return repositories.DispatchDecisionLogRepository.Create(ctx.Tx, &models.DispatchDecisionLog{
			TenantID:             conversation.TenantID,
			DecisionKey:          fmt.Sprintf("assignment:%d", assignment.ID),
			ConversationID:       conversation.ID,
			SessionNo:            assignment.SessionNo,
			AssignmentID:         assignment.ID,
			DecisionMode:         decisionMode,
			Status:               status,
			Trigger:              strings.TrimSpace(assignType),
			CandidateUserIDsJSON: fmt.Sprintf("[%d]", toUserID),
			CandidateSnapshotJSON: fmt.Sprintf(
				`[{"userId":%d,"decisionConfidence":%d,"workloadWeight":%d}]`,
				toUserID, assignment.DecisionConfidence, assignment.WorkloadWeight,
			),
			InputLastMessageID: conversation.LastMessageID,
			SelectedUserID:     toUserID,
			SelectedTeamID:     teamID,
			SelectedSquadID:    assignment.SquadID,
			Reason:             strings.TrimSpace(reason),
			OperatorID:         operatorID,
			DecidedAt:          assignment.CreatedAt,
			AuditFields:        utils.BuildAuditFields(nil),
		})
	})
}

func (s *serviceAnalyticsCaptureService) RecordClose(conversationID int64, at time.Time, reason string) error {
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	if conversation == nil || conversation.TenantID <= 0 {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		sessionNo := currentSessionNoDB(ctx.Tx, conversation.ID, conversation.TenantID)
		session, err := s.ensureSessionDB(ctx.Tx, conversation, sessionNo, at)
		if err != nil {
			return err
		}
		if err := repositories.ConversationServiceSessionRepository.UpdatesInTenant(ctx.Tx, session.ID, session.TenantID, map[string]any{
			"status":           enums.ServiceSessionStatusClosed,
			"ended_at":         at,
			"close_reason":     reason,
			"updated_at":       at,
			"update_user_name": "system",
		}); err != nil {
			return err
		}
		return s.abandonWaitingSpansDB(ctx.Tx, session, at)
	})
}

func (s *serviceAnalyticsCaptureService) ensureSessionDB(db *gorm.DB, conversation *models.Conversation, sessionNo int, at time.Time) (*models.ConversationServiceSession, error) {
	if sessionNo <= 0 {
		sessionNo = 1
	}
	if existing := repositories.ConversationServiceSessionRepository.TakeByKey(db, conversation.TenantID, conversation.ID, sessionNo); existing != nil {
		return existing, nil
	}
	for _, stale := range repositories.ConversationServiceSessionRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("conversation_id", conversation.ID).
		Eq("status", enums.ServiceSessionStatusOpen).
		Where("session_no <> ?", sessionNo)) {
		_ = repositories.ConversationServiceSessionRepository.UpdatesInTenant(db, stale.ID, stale.TenantID, map[string]any{
			"status":           enums.ServiceSessionStatusClosed,
			"ended_at":         at,
			"updated_at":       at,
			"update_user_name": "system",
		})
		_ = s.abandonWaitingSpansDB(db, &stale, at)
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)
	startedAt := at
	item := &models.ConversationServiceSession{
		TenantID:       conversation.TenantID,
		ConversationID: conversation.ID,
		SessionNo:      sessionNo,
		CustomerID:     conversation.CustomerID,
		ChannelID:      conversation.ChannelID,
		ServiceMode:    conversation.ServiceMode,
		Status:         enums.ServiceSessionStatusOpen,
		AssignedTeamID: conversation.CurrentTeamID,
		StartedAt:      startedAt,
		FactOrigin:     enums.AnalyticsFactOriginRuntime,
		DataQuality:    enums.AnalyticsDataQualityExact,
		AuditFields:    utils.BuildAuditFields(nil),
	}
	item.CreatedAt = at
	item.UpdatedAt = at
	if route != nil {
		item.StoreID = route.StoreID
		item.WxWorkInstanceID = route.WxWorkInstanceID
		if route.SessionNo == sessionNo && route.SessionStartedAt != nil {
			item.StartedAt = *route.SessionStartedAt
		}
	}
	if err := repositories.ConversationServiceSessionRepository.Create(db, item); err != nil {
		if existing := repositories.ConversationServiceSessionRepository.TakeByKey(db, conversation.TenantID, conversation.ID, sessionNo); existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return item, nil
}

func (s *serviceAnalyticsCaptureService) refreshMessageFactsDB(db *gorm.DB, session *models.ConversationServiceSession, message *models.Message, at time.Time) error {
	counts := map[enums.IMSenderType]int64{}
	for _, senderType := range []enums.IMSenderType{enums.IMSenderTypeCustomer, enums.IMSenderTypeAI, enums.IMSenderTypeAgent, enums.IMSenderTypeSystem} {
		var count int64
		if err := db.Model(&models.Message{}).Where("tenant_id = ? AND conversation_id = ? AND session_no = ? AND sender_type = ?", session.TenantID, session.ConversationID, session.SessionNo, senderType).Count(&count).Error; err != nil {
			return err
		}
		counts[senderType] = count
	}
	updates := map[string]any{
		"customer_message_count": counts[enums.IMSenderTypeCustomer],
		"ai_message_count":       counts[enums.IMSenderTypeAI],
		"human_message_count":    counts[enums.IMSenderTypeAgent],
		"system_message_count":   counts[enums.IMSenderTypeSystem],
		"ai_handled":             counts[enums.IMSenderTypeAI] > 0,
		"human_handled":          counts[enums.IMSenderTypeAgent] > 0,
		"updated_at":             at,
		"update_user_name":       "system",
	}
	if message.ID >= session.LastMessageID {
		updates["last_message_id"] = message.ID
		updates["last_message_at"] = at
	}
	return repositories.ConversationServiceSessionRepository.UpdatesInTenant(db, session.ID, session.TenantID, updates)
}

func (s *serviceAnalyticsCaptureService) recordCustomerWaitingSpanDB(db *gorm.DB, session *models.ConversationServiceSession, message *models.Message, at time.Time) error {
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, session.ConversationID, session.TenantID)
	if route == nil || !isHumanAnalyticsRoute(route.RouteStatus) {
		return nil
	}
	return s.ensureWaitingSpanDB(db, session, message, at)
}

func (s *serviceAnalyticsCaptureService) ensureWaitingSpanDB(db *gorm.DB, session *models.ConversationServiceSession, message *models.Message, startedAt time.Time) error {
	if waiting := repositories.ConversationResponseSpanRepository.FindLastWaiting(db, session.TenantID, session.ConversationID, session.SessionNo); waiting != nil {
		if message.ID <= waiting.CustomerEndMessageID {
			return nil
		}
		var count int64
		if err := db.Model(&models.Message{}).Where("tenant_id = ? AND conversation_id = ? AND session_no = ? AND sender_type = ? AND id >= ? AND id <= ?", session.TenantID, session.ConversationID, session.SessionNo, enums.IMSenderTypeCustomer, waiting.CustomerStartMessageID, message.ID).Count(&count).Error; err != nil {
			return err
		}
		return repositories.ConversationResponseSpanRepository.UpdatesInTenant(db, waiting.ID, waiting.TenantID, map[string]any{
			"customer_end_message_id": message.ID,
			"customer_message_count":  count,
			"updated_at":              startedAt,
		})
	}
	assignment := repositories.ConversationAssignmentRepository.FindOne(db, sqls.NewCnd().
		Eq("tenant_id", session.TenantID).
		Eq("conversation_id", session.ConversationID).
		Eq("session_no", session.SessionNo).
		Eq("status", enums.IMAssignmentStatusActive).
		Desc("id"))
	span := &models.ConversationResponseSpan{
		TenantID:               session.TenantID,
		ConversationID:         session.ConversationID,
		SessionNo:              session.SessionNo,
		CustomerStartMessageID: message.ID,
		CustomerEndMessageID:   message.ID,
		CustomerMessageCount:   1,
		StartedAt:              startedAt,
		Status:                 enums.ResponseSpanStatusWaiting,
		FactOrigin:             enums.AnalyticsFactOriginRuntime,
		DataQuality:            enums.AnalyticsDataQualityExact,
		AuditFields:            utils.BuildAuditFields(nil),
	}
	span.CreatedAt = startedAt
	span.UpdatedAt = startedAt
	if assignment != nil {
		span.AssignmentID = assignment.ID
		span.SquadID = assignment.SquadID
		span.AgentID = assignment.ToUserID
		if profile := repositories.AgentProfileRepository.Take(db, "tenant_id = ? AND user_id = ?", session.TenantID, assignment.ToUserID); profile != nil {
			span.TeamID = profile.TeamID
		}
	}
	return repositories.ConversationResponseSpanRepository.Create(db, span)
}

func (s *serviceAnalyticsCaptureService) recordHumanReplyDB(db *gorm.DB, session *models.ConversationServiceSession, message *models.Message, at time.Time) error {
	assignment := repositories.ConversationAssignmentRepository.FindOne(db, sqls.NewCnd().
		Eq("tenant_id", session.TenantID).
		Eq("conversation_id", session.ConversationID).
		Eq("session_no", session.SessionNo).
		Eq("status", enums.IMAssignmentStatusActive).
		Desc("id"))
	updates := map[string]any{
		"human_handled":       true,
		"last_human_reply_at": at,
		"updated_at":          at,
		"update_user_name":    "system",
	}
	if session.FirstHumanReplyAt == nil {
		updates["first_human_reply_at"] = at
		if session.AssignedAt != nil {
			updates["first_response_seconds"] = nonNegativeSeconds(*session.AssignedAt, at)
		}
		if session.QueueEnteredAt != nil {
			updates["total_human_wait_seconds"] = nonNegativeSeconds(*session.QueueEnteredAt, at)
		}
	}
	if err := repositories.ConversationServiceSessionRepository.UpdatesInTenant(db, session.ID, session.TenantID, updates); err != nil {
		return err
	}
	for _, span := range repositories.ConversationResponseSpanRepository.FindWaiting(db, session.TenantID, session.ConversationID, session.SessionNo) {
		if assignment != nil && span.AssignmentID > 0 && span.AssignmentID != assignment.ID {
			continue
		}
		values := map[string]any{
			"replied_at":       at,
			"reply_message_id": message.ID,
			"wait_seconds":     nonNegativeSeconds(span.StartedAt, at),
			"status":           enums.ResponseSpanStatusReplied,
			"updated_at":       at,
		}
		if assignment != nil {
			values["assignment_id"] = assignment.ID
			values["squad_id"] = assignment.SquadID
			values["agent_id"] = assignment.ToUserID
			if profile := repositories.AgentProfileRepository.Take(db, "tenant_id = ? AND user_id = ?", session.TenantID, assignment.ToUserID); profile != nil {
				values["team_id"] = profile.TeamID
			}
		}
		if err := repositories.ConversationResponseSpanRepository.UpdatesInTenant(db, span.ID, span.TenantID, values); err != nil {
			return err
		}
	}
	return nil
}

func (s *serviceAnalyticsCaptureService) abandonWaitingSpansDB(db *gorm.DB, session *models.ConversationServiceSession, at time.Time) error {
	for _, span := range repositories.ConversationResponseSpanRepository.FindWaiting(db, session.TenantID, session.ConversationID, session.SessionNo) {
		if err := repositories.ConversationResponseSpanRepository.UpdatesInTenant(db, span.ID, span.TenantID, map[string]any{
			"status":       enums.ResponseSpanStatusAbandoned,
			"wait_seconds": nonNegativeSeconds(span.StartedAt, at),
			"updated_at":   at,
		}); err != nil {
			return err
		}
	}
	return nil
}

func isHumanAnalyticsRoute(status enums.ConversationRouteStatus) bool {
	switch status {
	case enums.ConversationRouteStatusStoreWecomManual, enums.ConversationRouteStatusHQAgentDeskPending, enums.ConversationRouteStatusHQAgentDeskServing:
		return true
	default:
		return false
	}
}

func nonNegativeSeconds(from, to time.Time) int64 {
	if to.Before(from) {
		return 0
	}
	return int64(to.Sub(from).Seconds())
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func logServiceAnalyticsCaptureError(action string, conversationID int64, err error) {
	if err != nil {
		slog.Warn("capture service analytics fact failed", "action", action, "conversationId", conversationID, "error", err)
	}
}

var AgentPresenceService = &agentPresenceService{}

type agentPresenceService struct{}

const presenceHeartbeatInterval = 2 * time.Minute

func (s *agentPresenceService) Touch(operator *dto.AuthPrincipal, source string, at time.Time) error {
	if operator == nil || operator.UserID <= 0 || operator.ActiveTenantID <= 0 {
		return nil
	}
	profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", operator.ActiveTenantID, operator.UserID)
	if profile == nil {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedProfile, err := repositories.AgentProfileRepository.GetForUpdateInTenant(ctx.Tx, profile.ID, operator.ActiveTenantID)
		if err != nil || lockedProfile == nil || lockedProfile.UserID != operator.UserID {
			return err
		}
		active := repositories.AgentPresenceSessionRepository.FindActive(ctx.Tx, operator.ActiveTenantID, operator.UserID)
		if active != nil {
			at = monotonicPresenceTime(at, active.LastSeenAt)
			if active.Status == enums.AgentPresenceStatusBreak {
				return repositories.AgentPresenceSessionRepository.UpdatesInTenant(ctx.Tx, active.ID, active.TenantID, map[string]any{
					"last_seen_at": at, "duration_seconds": nonNegativeSeconds(active.StartedAt, at),
					"updated_at": at, "update_user_name": operator.Username,
				})
			}
			if at.Sub(active.LastSeenAt) > presenceHeartbeatInterval {
				if err := repositories.AgentPresenceSessionRepository.UpdatesInTenant(ctx.Tx, active.ID, active.TenantID, map[string]any{
					"ended_at":         active.LastSeenAt,
					"duration_seconds": nonNegativeSeconds(active.StartedAt, active.LastSeenAt),
					"updated_at":       at,
					"update_user_name": "system",
				}); err != nil {
					return err
				}
			} else {
				return repositories.AgentPresenceSessionRepository.UpdatesInTenant(ctx.Tx, active.ID, active.TenantID, map[string]any{
					"last_seen_at":     at,
					"duration_seconds": nonNegativeSeconds(active.StartedAt, at),
					"updated_at":       at,
					"update_user_name": operator.Username,
				})
			}
		}
		return repositories.AgentPresenceSessionRepository.Create(ctx.Tx, &models.AgentPresenceSession{
			TenantID:       operator.ActiveTenantID,
			UserID:         operator.UserID,
			AgentProfileID: lockedProfile.ID,
			TeamID:         lockedProfile.TeamID,
			Status:         enums.AgentPresenceStatusOnline,
			Source:         source,
			StartedAt:      at,
			LastSeenAt:     at,
			AuditFields:    utils.BuildAuditFields(operator),
		})
	})
}

func (s *agentPresenceService) GetCurrent(operator *dto.AuthPrincipal) (*models.AgentPresenceSession, error) {
	if operator == nil || operator.UserID <= 0 || operator.ActiveTenantID <= 0 {
		return nil, nil
	}
	return repositories.AgentPresenceSessionRepository.FindActive(sqls.DB(), operator.ActiveTenantID, operator.UserID), nil
}

func (s *agentPresenceService) SetStatus(operator *dto.AuthPrincipal, status enums.AgentPresenceStatus, breakReason string, at time.Time) (*models.AgentPresenceSession, error) {
	if operator == nil || operator.UserID <= 0 || operator.ActiveTenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入所属接入公司")
	}
	switch status {
	case enums.AgentPresenceStatusOnline, enums.AgentPresenceStatusIdle, enums.AgentPresenceStatusBusy, enums.AgentPresenceStatusBreak:
	default:
		return nil, errorsx.InvalidParam("客服在线状态无效")
	}
	breakReason = strings.TrimSpace(breakReason)
	if status == enums.AgentPresenceStatusBreak && breakReason == "" {
		return nil, errorsx.InvalidParam("休息状态必须选择原因")
	}
	if status != enums.AgentPresenceStatusBreak {
		breakReason = ""
	}
	profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", operator.ActiveTenantID, operator.UserID)
	if profile == nil {
		return nil, errorsx.Forbidden("当前账号没有客服档案")
	}
	var created *models.AgentPresenceSession
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedProfile, err := repositories.AgentProfileRepository.GetForUpdateInTenant(ctx.Tx, profile.ID, operator.ActiveTenantID)
		if err != nil {
			return err
		}
		if lockedProfile == nil || lockedProfile.UserID != operator.UserID {
			return errorsx.Forbidden("当前账号没有客服档案")
		}
		if active := repositories.AgentPresenceSessionRepository.FindActive(ctx.Tx, operator.ActiveTenantID, operator.UserID); active != nil {
			at = monotonicPresenceTime(at, active.LastSeenAt)
			if active.Status == status && active.BreakReason == breakReason {
				created = active
				return repositories.AgentPresenceSessionRepository.UpdatesInTenant(ctx.Tx, active.ID, active.TenantID, map[string]any{
					"last_seen_at": at, "duration_seconds": nonNegativeSeconds(active.StartedAt, at),
					"updated_at": at, "update_user_id": operator.UserID, "update_user_name": operator.Username,
				})
			}
			if err := repositories.AgentPresenceSessionRepository.UpdatesInTenant(ctx.Tx, active.ID, active.TenantID, map[string]any{
				"ended_at": at, "last_seen_at": at, "duration_seconds": nonNegativeSeconds(active.StartedAt, at),
				"updated_at": at, "update_user_id": operator.UserID, "update_user_name": operator.Username,
			}); err != nil {
				return err
			}
		}
		created = &models.AgentPresenceSession{
			TenantID: operator.ActiveTenantID, UserID: operator.UserID, AgentProfileID: lockedProfile.ID, TeamID: lockedProfile.TeamID,
			Status: status, Source: "manual", BreakReason: breakReason, ChangedBy: operator.UserID,
			StartedAt: at, LastSeenAt: at, AuditFields: utils.BuildAuditFields(operator),
		}
		return repositories.AgentPresenceSessionRepository.Create(ctx.Tx, created)
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *agentPresenceService) End(tenantID, userID int64, at time.Time) error {
	if tenantID <= 0 || userID <= 0 {
		return nil
	}
	profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", tenantID, userID)
	if profile == nil {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		lockedProfile, err := repositories.AgentProfileRepository.GetForUpdateInTenant(ctx.Tx, profile.ID, tenantID)
		if err != nil || lockedProfile == nil || lockedProfile.UserID != userID {
			return err
		}
		active := repositories.AgentPresenceSessionRepository.FindActive(ctx.Tx, tenantID, userID)
		if active == nil {
			return nil
		}
		at = monotonicPresenceTime(at, active.LastSeenAt)
		return repositories.AgentPresenceSessionRepository.UpdatesInTenant(ctx.Tx, active.ID, active.TenantID, map[string]any{
			"ended_at":         at,
			"last_seen_at":     at,
			"duration_seconds": nonNegativeSeconds(active.StartedAt, at),
			"updated_at":       at,
			"update_user_name": "system",
		})
	})
}

func monotonicPresenceTime(at, lastSeenAt time.Time) time.Time {
	if at.Before(lastSeenAt) {
		return lastSeenAt
	}
	return at
}
