package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	maxRuleAssignmentRecoveryAttempts = 3
	ruleAssignmentRetryCooldown       = 90 * time.Second
)

type ruleAssignmentRecoveryStage string

const (
	ruleAssignmentRecoveryStageFirstResponse ruleAssignmentRecoveryStage = "first_response"
	ruleAssignmentRecoveryStageFollowUp      ruleAssignmentRecoveryStage = "follow_up"
)

type ruleAssignmentRecoveryCandidate struct {
	assignment                models.ConversationAssignment
	stage                     ruleAssignmentRecoveryStage
	waitingSince              time.Time
	oldestUnansweredMessageID int64
}

type ruleAssignmentRecoveryCause struct {
	code    string
	message string
	hard    bool
}

func (s *conversationDispatchService) RecoverStaleAssignments(limit int) (int, error) {
	return s.recoverStaleAssignments(0, 0, limit)
}

func (s *conversationDispatchService) RecoverAssignmentsForAgent(tenantID, userID int64, limit int) (int, error) {
	if tenantID <= 0 || userID <= 0 {
		return 0, nil
	}
	return s.recoverStaleAssignments(tenantID, userID, limit)
}

func (s *conversationDispatchService) RecoverAssignmentsForTenant(tenantID int64, limit int) (int, error) {
	if tenantID <= 0 {
		return 0, nil
	}
	return s.recoverStaleAssignments(tenantID, 0, limit)
}

// ReconcileConfigurationChange applies committed organization, schedule and
// authorization changes to current rule assignments, then drains affected
// team queues through the existing debounced dispatcher.
func (s *conversationDispatchService) ReconcileConfigurationChange(tenantID int64, teamIDs ...int64) {
	if tenantID <= 0 {
		return
	}
	if _, err := s.RecoverAssignmentsForTenant(tenantID, 500); err != nil {
		slog.Warn("reconcile rule assignments after configuration change failed", "tenant_id", tenantID, "error", err)
	}
	for _, teamID := range uniquePositiveInt64s(teamIDs) {
		s.ScheduleTeamDispatch(tenantID, teamID)
	}
}

func (s *conversationDispatchService) ReconcileUserAuthorizationChanges(userIDs ...int64) {
	userIDs = uniquePositiveInt64s(userIDs)
	if len(userIDs) == 0 {
		return
	}
	profiles := repositories.AgentProfileRepository.Find(sqls.DB(), sqls.NewCnd().
		In("user_id", userIDs).
		Where("status <> ?", enums.StatusDeleted))
	teamIDsByTenant := make(map[int64][]int64)
	for i := range profiles {
		if profiles[i].TenantID <= 0 || profiles[i].TeamID <= 0 {
			continue
		}
		teamIDsByTenant[profiles[i].TenantID] = append(teamIDsByTenant[profiles[i].TenantID], profiles[i].TeamID)
	}
	for tenantID, teamIDs := range teamIDsByTenant {
		s.ReconcileConfigurationChange(tenantID, teamIDs...)
	}
}

func (s *conversationDispatchService) recoverStaleAssignments(tenantID, userID int64, limit int) (int, error) {
	if limit <= 0 {
		limit = pendingDispatchBatchLimit
	}
	candidates, err := s.findRuleAssignmentRecoveryCandidates(tenantID, userID, limit, time.Now())
	if err != nil {
		return 0, err
	}
	recovered := 0
	for i := range candidates {
		candidate := candidates[i]
		assignment := &candidate.assignment
		if _, loaded := s.dispatching.LoadOrStore(assignment.ConversationID, struct{}{}); loaded {
			continue
		}
		changed, err := s.recoverRuleAssignmentCandidate(&candidate, time.Now())
		s.dispatching.Delete(assignment.ConversationID)
		if err != nil {
			if errors.Is(err, errConversationDispatchConflict) {
				continue
			}
			return recovered, err
		}
		if changed {
			recovered++
		}
	}
	return recovered, nil
}

func (s *conversationDispatchService) findRuleAssignmentRecoveryCandidates(tenantID, userID int64, limit int, now time.Time) ([]ruleAssignmentRecoveryCandidate, error) {
	if limit <= 0 {
		limit = pendingDispatchBatchLimit
	}
	scanLimit := limit * 5
	if scanLimit < pendingDispatchBatchLimit {
		scanLimit = pendingDispatchBatchLimit
	}
	if scanLimit > 500 {
		scanLimit = 500
	}
	unreplied := repositories.ConversationAssignmentRepository.FindActiveRuleWithoutHumanReply(sqls.DB(), tenantID, userID, scanLimit)
	followUps := repositories.ConversationAssignmentRepository.FindActiveRuleWithHumanReplyAndCustomerWaiting(sqls.DB(), tenantID, userID, scanLimit)

	ret := make([]ruleAssignmentRecoveryCandidate, 0, len(unreplied)+len(followUps))
	for i := range unreplied {
		ret = append(ret, ruleAssignmentRecoveryCandidate{
			assignment:   unreplied[i],
			stage:        ruleAssignmentRecoveryStageFirstResponse,
			waitingSince: unreplied[i].CreatedAt,
		})
	}

	followUpsByTenant := make(map[int64][]models.ConversationAssignment)
	for i := range followUps {
		followUpsByTenant[followUps[i].TenantID] = append(followUpsByTenant[followUps[i].TenantID], followUps[i])
	}
	for candidateTenantID, assignments := range followUpsByTenant {
		assignmentIDs := make([]int64, 0, len(assignments))
		for i := range assignments {
			assignmentIDs = append(assignmentIDs, assignments[i].ID)
		}
		states, err := repositories.MessageRepository.FindActiveAssignmentMessageStatesByAssignmentIDs(sqls.DB(), candidateTenantID, assignmentIDs)
		if err != nil {
			return nil, err
		}
		stateByAssignmentID := make(map[int64]repositories.ActiveAssignmentMessageStateRow, len(states))
		messageIDs := make([]int64, 0, len(states))
		for _, state := range states {
			stateByAssignmentID[state.AssignmentID] = state
			if state.OldestUnansweredMessageID > 0 {
				messageIDs = append(messageIDs, state.OldestUnansweredMessageID)
			}
		}
		messageByID := make(map[int64]models.Message, len(messageIDs))
		if len(messageIDs) > 0 {
			for _, message := range repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
				Eq("tenant_id", candidateTenantID).
				In("id", uniquePositiveInt64s(messageIDs))) {
				messageByID[message.ID] = message
			}
		}
		responseTarget := ruleDispatchResponseTargetSecondsDB(sqls.DB(), candidateTenantID)
		for i := range assignments {
			state, exists := stateByAssignmentID[assignments[i].ID]
			message, messageExists := messageByID[state.OldestUnansweredMessageID]
			if !exists || state.LastAssignedReplySeq <= 0 || state.UnansweredCustomerCount <= 0 || !messageExists {
				continue
			}
			waitingSince := serviceAnalyticsMessageTime(&message)
			if waitingSince.IsZero() || now.Before(waitingSince.Add(time.Duration(responseTarget)*time.Second)) {
				continue
			}
			ret = append(ret, ruleAssignmentRecoveryCandidate{
				assignment:                assignments[i],
				stage:                     ruleAssignmentRecoveryStageFollowUp,
				waitingSince:              waitingSince,
				oldestUnansweredMessageID: state.OldestUnansweredMessageID,
			})
		}
	}

	slices.SortStableFunc(ret, func(a, b ruleAssignmentRecoveryCandidate) int {
		if a.waitingSince.Before(b.waitingSince) {
			return -1
		}
		if a.waitingSince.After(b.waitingSince) {
			return 1
		}
		switch {
		case a.assignment.ID < b.assignment.ID:
			return -1
		case a.assignment.ID > b.assignment.ID:
			return 1
		default:
			return 0
		}
	})
	if len(ret) > limit {
		ret = ret[:limit]
	}
	return ret, nil
}

func (s *conversationDispatchService) recoverRuleAssignment(assignment *models.ConversationAssignment, now time.Time) (bool, error) {
	if assignment == nil {
		return false, nil
	}
	return s.recoverRuleAssignmentCandidate(&ruleAssignmentRecoveryCandidate{
		assignment:   *assignment,
		stage:        ruleAssignmentRecoveryStageFirstResponse,
		waitingSince: assignment.CreatedAt,
	}, now)
}

func (s *conversationDispatchService) recoverRuleAssignmentCandidate(recovery *ruleAssignmentRecoveryCandidate, now time.Time) (bool, error) {
	if recovery == nil {
		return false, nil
	}
	assignment := &recovery.assignment
	if assignment == nil || assignment.TenantID <= 0 || assignment.ConversationID <= 0 || assignment.ToUserID <= 0 {
		return false, nil
	}
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), assignment.ConversationID, assignment.TenantID)
	if conversation == nil || conversation.Status != enums.IMConversationStatusActive || conversation.CurrentAssigneeID != assignment.ToUserID {
		return false, nil
	}
	cause, err := s.detectRuleAssignmentRecoveryCauseDB(sqls.DB(), assignment, conversation, recovery.stage, now)
	if err != nil || cause.code == "" {
		return false, err
	}
	if recovery.stage == ruleAssignmentRecoveryStageFollowUp && !cause.hard {
		return false, nil
	}
	recoveryAttempts, err := s.ruleAssignmentRecoveryAttempts(sqls.DB(), assignment)
	if err != nil {
		return false, err
	}
	if recoveryAttempts >= maxRuleAssignmentRecoveryAttempts {
		if !cause.hard {
			s.notifyRecoveredConversation(conversation, assignment, "已超过人工首响时限，且达到自动重派上限")
			return false, nil
		}
		return s.releaseUnavailableRuleAssignment(recovery, conversation, cause, true, now)
	}

	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	teamIDs := s.resolveDispatchTeamIDs(conversation, route)
	teamIDs = s.filterAutomaticTeamIDs(teamIDs, conversation.TenantID)
	candidates, _, err := s.pickDispatchCandidates(teamIDs, conversation.TenantID, route, now)
	if err != nil {
		return false, err
	}
	alternatives := make([]dispatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.profile.UserID != assignment.ToUserID {
			alternatives = append(alternatives, candidate)
		}
	}
	alternatives, err = s.filterRuleRetryCooldownCandidates(conversation, alternatives, now)
	if err != nil {
		return false, err
	}
	if len(alternatives) == 0 {
		if !cause.hard {
			s.notifyRecoveredConversation(conversation, assignment, cause.message+"，当前暂无其他可接待客服")
			return false, nil
		}
		return s.releaseUnavailableRuleAssignment(recovery, conversation, cause, false, now)
	}

	decision := s.selectDispatchDecision(conversation, route, alternatives)
	decision.reason = compactDispatchReason(fmt.Sprintf("规则自动重派（%s）：%s", cause.message, decision.reason))
	updated, err := s.tryRecoverWithDecisionContext(recovery, decision, now)
	if err != nil {
		return false, err
	}
	if updated == nil {
		return false, nil
	}
	latestAssignment := repositories.ConversationAssignmentRepository.FindOne(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", assignment.TenantID).
		Eq("conversation_id", assignment.ConversationID).
		Eq("session_no", assignment.SessionNo).
		Eq("to_user_id", updated.CurrentAssigneeID).
		Eq("dispatch_mode", enums.AgentTeamDispatchModeRule).
		Desc("id"))
	assignmentID := int64(0)
	if latestAssignment != nil {
		assignmentID = latestAssignment.ID
	}
	_ = ServiceAnalyticsCaptureService.RecordDispatchEvidence(DispatchDecisionEvidence{
		ConversationID:     assignment.ConversationID,
		SessionNo:          assignment.SessionNo,
		AssignmentID:       assignmentID,
		Trigger:            "auto_recovery",
		DecisionMode:       string(enums.AgentTeamDispatchModeRule),
		Status:             enums.DispatchDecisionStatusSelected,
		InputLastMessageID: conversation.LastMessageID,
		SelectedUserID:     updated.CurrentAssigneeID,
		SelectedTeamID:     updated.CurrentTeamID,
		SelectedSquadID:    decision.candidate.squadID,
		Reason:             decision.reason,
		FallbackReason:     cause.code,
		DecidedAt:          now,
	})
	WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationTransferred)
	eventbus.PublishAsync(context.Background(), events.ConversationAssignedEvent{
		ConversationID: updated.ID,
		FromUserID:     assignment.ToUserID,
		ToUserID:       updated.CurrentAssigneeID,
		OperatorID:     systemDispatchPrincipal().UserID,
		Reason:         decision.reason,
		AssignType:     events.ConversationAssignTypeTransfer,
	})
	slog.Info("rule assignment recovered",
		"conversation_id", updated.ID,
		"from_user_id", assignment.ToUserID,
		"to_user_id", updated.CurrentAssigneeID,
		"stage", recovery.stage,
		"cause", cause.code,
		"recovery_attempt", recoveryAttempts+1,
	)
	return true, nil
}

func (s *conversationDispatchService) filterRuleRetryCooldownCandidates(conversation *models.Conversation, candidates []dispatchCandidate, now time.Time) ([]dispatchCandidate, error) {
	if conversation == nil || conversation.TenantID <= 0 || conversation.ID <= 0 || len(candidates) == 0 {
		return candidates, nil
	}
	sessionNo := currentSessionNoDB(sqls.DB(), conversation.ID, conversation.TenantID)
	assignments, err := repositories.ConversationAssignmentRepository.FindRecentRuleAssignmentsForSession(
		sqls.DB(),
		conversation.TenantID,
		conversation.ID,
		sessionNo,
		now.Add(-ruleAssignmentRetryCooldown),
	)
	if err != nil || len(assignments) == 0 {
		return candidates, err
	}
	recentUsers := make(map[int64]struct{}, len(assignments))
	for _, assignment := range assignments {
		if assignment.ToUserID > 0 {
			recentUsers[assignment.ToUserID] = struct{}{}
		}
	}
	ret := make([]dispatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, coolingDown := recentUsers[candidate.profile.UserID]; coolingDown {
			continue
		}
		ret = append(ret, candidate)
	}
	return ret, nil
}

func (s *conversationDispatchService) ruleDispatchCandidateCoolingDownDB(db *gorm.DB, conversation *models.Conversation, userID int64, now time.Time) (bool, error) {
	if db == nil || conversation == nil || conversation.TenantID <= 0 || conversation.ID <= 0 || userID <= 0 {
		return false, nil
	}
	sessionNo := currentSessionNoDB(db, conversation.ID, conversation.TenantID)
	assignments, err := repositories.ConversationAssignmentRepository.FindRecentRuleAssignmentsForSession(
		db,
		conversation.TenantID,
		conversation.ID,
		sessionNo,
		now.Add(-ruleAssignmentRetryCooldown),
	)
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		if assignment.ToUserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (s *conversationDispatchService) detectRuleAssignmentRecoveryCause(assignment *models.ConversationAssignment, conversation *models.Conversation, now time.Time) (ruleAssignmentRecoveryCause, error) {
	return s.detectRuleAssignmentRecoveryCauseDB(sqls.DB(), assignment, conversation, ruleAssignmentRecoveryStageFirstResponse, now)
}

func (s *conversationDispatchService) detectRuleAssignmentRecoveryCauseDB(db *gorm.DB, assignment *models.ConversationAssignment, conversation *models.Conversation, stage ruleAssignmentRecoveryStage, now time.Time) (ruleAssignmentRecoveryCause, error) {
	if db == nil || assignment == nil || conversation == nil {
		return ruleAssignmentRecoveryCause{}, nil
	}
	team := repositories.AgentTeamRepository.GetInTenant(db, conversation.CurrentTeamID, conversation.TenantID)
	if team != nil && normalizedDispatchMode(team.DispatchMode) == enums.AgentTeamDispatchModeManual {
		return ruleAssignmentRecoveryCause{}, nil
	}
	if team == nil || team.Status != enums.StatusOk {
		return ruleAssignmentRecoveryCause{code: "team_unavailable", message: "原客服组已停用", hard: true}, nil
	}
	profile := repositories.AgentProfileRepository.Take(db, "tenant_id = ? AND user_id = ?", conversation.TenantID, assignment.ToUserID)
	if profile == nil || profile.Status != enums.StatusOk || profile.TeamID != team.ID {
		return ruleAssignmentRecoveryCause{code: "agent_profile_unavailable", message: "原客服档案已停用或已离开客服组", hard: true}, nil
	}
	if stage == ruleAssignmentRecoveryStageFirstResponse {
		if !profile.AutoAssignEnabled {
			return ruleAssignmentRecoveryCause{code: "agent_auto_assign_unavailable", message: "原客服已退出自动接待", hard: true}, nil
		}
		if profile.MaxConcurrentCount <= 0 {
			return ruleAssignmentRecoveryCause{code: "agent_capacity_unavailable", message: "原客服自动接待容量已关闭", hard: true}, nil
		}
		activeCounts, err := s.findActiveConversationCountMapDB(db, []int64{assignment.ToUserID}, conversation.TenantID)
		if err != nil {
			return ruleAssignmentRecoveryCause{}, err
		}
		if activeCounts[assignment.ToUserID] > profile.MaxConcurrentCount {
			return ruleAssignmentRecoveryCause{code: "agent_capacity_reduced", message: "原客服当前任务已超过调整后的并发上限", hard: true}, nil
		}
	}
	user := repositories.UserRepository.GetInTenant(db, assignment.ToUserID, conversation.TenantID)
	if user == nil || user.Status != enums.StatusOk || user.DeletedAt != nil {
		return ruleAssignmentRecoveryCause{code: "agent_account_unavailable", message: "原客服账号已停用", hard: true}, nil
	}
	permittedUserIDs, err := repositories.PermissionRepository.FindUserIDsWithAllCodes(db, []int64{assignment.ToUserID}, []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
	})
	if err != nil {
		return ruleAssignmentRecoveryCause{}, err
	}
	if len(permittedUserIDs) != 1 || permittedUserIDs[0] != assignment.ToUserID {
		return ruleAssignmentRecoveryCause{code: "reply_permission_lost", message: "原客服已无会话回复权限", hard: true}, nil
	}
	activeSchedules := s.findActiveScheduleDetailsDB(db, []int64{team.ID}, conversation.TenantID, now)
	selection, scheduled := activeSchedules[team.ID]
	if !scheduled || !profileMatchesActiveScheduleDB(db, profile, selection) {
		return ruleAssignmentRecoveryCause{code: "out_of_shift", message: "原客服已不在当前班次", hard: true}, nil
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)
	if route != nil && !teamCanServeRoute(team, route) {
		return ruleAssignmentRecoveryCause{code: "route_scope_changed", message: "会话来源已超出原客服组范围", hard: true}, nil
	}
	if stage == ruleAssignmentRecoveryStageFollowUp {
		return ruleAssignmentRecoveryCause{}, nil
	}
	targetSeconds := ruleDispatchFirstResponseTargetSecondsDB(db, conversation.TenantID)
	if !assignment.CreatedAt.IsZero() && !now.Before(assignment.CreatedAt.Add(time.Duration(targetSeconds)*time.Second)) {
		return ruleAssignmentRecoveryCause{code: "first_response_sla_breached", message: "已超过人工首响时限", hard: false}, nil
	}
	return ruleAssignmentRecoveryCause{}, nil
}

func ruleDispatchFirstResponseTargetSecondsDB(db *gorm.DB, tenantID int64) int {
	if db != nil {
		if policy := repositories.ServiceAnalyticsPolicyRepository.TakeByTenant(db, tenantID); policy != nil && policy.FirstResponseTargetSeconds > 0 {
			return policy.FirstResponseTargetSeconds
		}
	}
	return 180
}

func ruleDispatchResponseTargetSecondsDB(db *gorm.DB, tenantID int64) int {
	if db != nil {
		if policy := repositories.ServiceAnalyticsPolicyRepository.TakeByTenant(db, tenantID); policy != nil && policy.ResponseTargetSeconds > 0 {
			return policy.ResponseTargetSeconds
		}
	}
	return 300
}

func (s *conversationDispatchService) ruleAssignmentRecoveryAttempts(db *gorm.DB, assignment *models.ConversationAssignment) (int, error) {
	if assignment == nil {
		return 0, nil
	}
	count, err := repositories.ConversationAssignmentRepository.CountRuleAssignmentsForSession(db, assignment.TenantID, assignment.ConversationID, assignment.SessionNo)
	if err != nil || count <= 1 {
		return 0, err
	}
	return int(count - 1), nil
}

func (s *conversationDispatchService) ruleDispatchRecoveryLimitReached(conversation *models.Conversation) (bool, error) {
	if conversation == nil || conversation.TenantID <= 0 || conversation.ID <= 0 {
		return false, nil
	}
	sessionNo := currentSessionNoDB(sqls.DB(), conversation.ID, conversation.TenantID)
	count, err := repositories.ConversationAssignmentRepository.CountRuleAssignmentsForSession(sqls.DB(), conversation.TenantID, conversation.ID, sessionNo)
	if err != nil {
		return false, err
	}
	return count >= int64(maxRuleAssignmentRecoveryAttempts+1), nil
}

func (s *conversationDispatchService) validateRuleAssignmentRecoveryStateDB(db *gorm.DB, recovery *ruleAssignmentRecoveryCandidate, assignment *models.ConversationAssignment, conversation *models.Conversation, now time.Time) (ruleAssignmentRecoveryCause, error) {
	if db == nil || recovery == nil || assignment == nil || conversation == nil {
		return ruleAssignmentRecoveryCause{}, errConversationDispatchConflict
	}
	switch recovery.stage {
	case ruleAssignmentRecoveryStageFirstResponse:
		replied, err := repositories.ConversationAssignmentRepository.HasHumanReplySince(db, assignment)
		if err != nil {
			return ruleAssignmentRecoveryCause{}, err
		}
		if replied {
			return ruleAssignmentRecoveryCause{}, errConversationDispatchConflict
		}
	case ruleAssignmentRecoveryStageFollowUp:
		states, err := repositories.MessageRepository.FindActiveAssignmentMessageStatesByAssignmentIDs(db, assignment.TenantID, []int64{assignment.ID})
		if err != nil {
			return ruleAssignmentRecoveryCause{}, err
		}
		if len(states) != 1 || states[0].LastAssignedReplySeq <= 0 || states[0].UnansweredCustomerCount <= 0 || states[0].OldestUnansweredMessageID <= 0 {
			return ruleAssignmentRecoveryCause{}, errConversationDispatchConflict
		}
		if recovery.oldestUnansweredMessageID > 0 && states[0].OldestUnansweredMessageID != recovery.oldestUnansweredMessageID {
			return ruleAssignmentRecoveryCause{}, errConversationDispatchConflict
		}
		message := repositories.MessageRepository.GetInTenant(db, states[0].OldestUnansweredMessageID, assignment.TenantID)
		if message == nil || message.ConversationID != assignment.ConversationID || message.SessionNo != assignment.SessionNo || message.SenderType != enums.IMSenderTypeCustomer {
			return ruleAssignmentRecoveryCause{}, errConversationDispatchConflict
		}
		waitingSince := serviceAnalyticsMessageTime(message)
		responseTarget := ruleDispatchResponseTargetSecondsDB(db, assignment.TenantID)
		if waitingSince.IsZero() || now.Before(waitingSince.Add(time.Duration(responseTarget)*time.Second)) {
			return ruleAssignmentRecoveryCause{}, errConversationDispatchConflict
		}
	default:
		return ruleAssignmentRecoveryCause{}, errConversationDispatchConflict
	}

	cause, err := s.detectRuleAssignmentRecoveryCauseDB(db, assignment, conversation, recovery.stage, now)
	if err != nil {
		return ruleAssignmentRecoveryCause{}, err
	}
	if cause.code == "" || (recovery.stage == ruleAssignmentRecoveryStageFollowUp && !cause.hard) {
		return ruleAssignmentRecoveryCause{}, errConversationDispatchConflict
	}
	return cause, nil
}

func (s *conversationDispatchService) tryRecoverWithDecision(assignment *models.ConversationAssignment, decision dispatchDecision, now time.Time) (*models.Conversation, error) {
	if assignment == nil {
		return nil, errConversationDispatchConflict
	}
	return s.tryRecoverWithDecisionContext(&ruleAssignmentRecoveryCandidate{
		assignment:   *assignment,
		stage:        ruleAssignmentRecoveryStageFirstResponse,
		waitingSince: assignment.CreatedAt,
	}, decision, now)
}

func (s *conversationDispatchService) tryRecoverWithDecisionContext(recovery *ruleAssignmentRecoveryCandidate, decision dispatchDecision, now time.Time) (*models.Conversation, error) {
	if recovery == nil {
		return nil, errConversationDispatchConflict
	}
	assignment := &recovery.assignment
	if decision.candidate.profile.UserID <= 0 || decision.candidate.profile.UserID == assignment.ToUserID {
		return nil, errConversationDispatchConflict
	}
	operator := systemDispatchPrincipal()
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, assignment.ConversationID, assignment.TenantID)
		if err != nil {
			return err
		}
		if conversation == nil || conversation.Status != enums.IMConversationStatusActive || conversation.CurrentAssigneeID != assignment.ToUserID || conversation.LastMessageID != decision.expectedLastMessageID {
			return errConversationDispatchConflict
		}
		lockedAssignment, err := repositories.ConversationAssignmentRepository.GetForUpdateInTenant(ctx.Tx, assignment.ID, assignment.TenantID)
		if err != nil {
			return err
		}
		if lockedAssignment == nil || lockedAssignment.ConversationID != conversation.ID || lockedAssignment.SessionNo != assignment.SessionNo || lockedAssignment.ToUserID != assignment.ToUserID || lockedAssignment.Status != enums.IMAssignmentStatusActive || lockedAssignment.DispatchMode != enums.AgentTeamDispatchModeRule {
			return errConversationDispatchConflict
		}
		if currentSessionNoDB(ctx.Tx, conversation.ID, conversation.TenantID) != lockedAssignment.SessionNo {
			return errConversationDispatchConflict
		}
		if _, err := s.validateRuleAssignmentRecoveryStateDB(ctx.Tx, recovery, lockedAssignment, conversation, now); err != nil {
			return err
		}
		attempts, err := s.ruleAssignmentRecoveryAttempts(ctx.Tx, lockedAssignment)
		if err != nil {
			return err
		}
		if attempts >= maxRuleAssignmentRecoveryAttempts {
			return errConversationDispatchConflict
		}
		coolingDown, err := s.ruleDispatchCandidateCoolingDownDB(ctx.Tx, conversation, decision.candidate.profile.UserID, now)
		if err != nil {
			return err
		}
		if coolingDown {
			return errConversationDispatchConflict
		}
		profile, activeSelection, err := s.validateDispatchDecisionCandidateDB(ctx.Tx, conversation, decision, now)
		if err != nil {
			return err
		}
		if profile.UserID == lockedAssignment.ToUserID {
			return errConversationDispatchConflict
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversation.ID, now); err != nil {
			return err
		}
		if err := ConversationAssignmentService.CreateAssignmentWithOptions(ctx, conversation.ID, lockedAssignment.ToUserID, profile.UserID, enums.IMAssignmentTypeTransfer, decision.reason, operator, now, ConversationAssignmentOptions{
			SquadID:        activeSelection.SquadID,
			DispatchMode:   enums.AgentTeamDispatchModeRule,
			WorkloadWeight: decision.workloadWeight,
		}); err != nil {
			return err
		}
		result := ctx.Tx.Model(&models.Conversation{}).
			Where("id = ? AND tenant_id = ? AND status = ? AND current_assignee_id = ? AND last_message_id = ?", conversation.ID, conversation.TenantID, enums.IMConversationStatusActive, lockedAssignment.ToUserID, conversation.LastMessageID).
			Updates(map[string]any{
				"current_assignee_id": profile.UserID,
				"current_team_id":     profile.TeamID,
				"priority":            decision.priority,
				"dispatch_weight":     decision.workloadWeight,
				"update_user_id":      operator.UserID,
				"update_user_name":    operator.Username,
				"updated_at":          now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errConversationDispatchConflict
		}
		if err := ConversationEventLogService.CreateEvent(ctx, conversation.ID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, operator.UserID, "会话已自动重派", ConversationService.buildEventPayload(map[string]any{
			"fromStatus":     enums.IMConversationStatusActive,
			"toStatus":       enums.IMConversationStatusActive,
			"fromAssigneeId": lockedAssignment.ToUserID,
			"toAssigneeId":   profile.UserID,
			"toTeamId":       profile.TeamID,
			"reason":         strings.TrimSpace(decision.reason),
			"dispatchMode":   enums.AgentTeamDispatchModeRule,
			"workloadWeight": decision.workloadWeight,
			"priority":       decision.priority,
		})); err != nil {
			return err
		}
		_, err = ConversationRouteService.enterHQAgentDeskServingWithDB(ctx.Tx, conversation.ID, "规则自动重派:"+strings.TrimSpace(decision.reason), now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return ConversationService.Get(assignment.ConversationID), nil
}

func (s *conversationDispatchService) releaseUnavailableRuleAssignment(recovery *ruleAssignmentRecoveryCandidate, conversation *models.Conversation, cause ruleAssignmentRecoveryCause, exhausted bool, now time.Time) (bool, error) {
	if recovery == nil || conversation == nil {
		return false, nil
	}
	assignment := &recovery.assignment
	reason := "规则自动回收：" + cause.message
	if exhausted {
		reason += "，已达到自动重派上限，等待组长处理"
	} else {
		reason += "，当前暂无其他可接待客服"
	}
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if err != nil {
			return err
		}
		if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != assignment.ToUserID || current.LastMessageID != conversation.LastMessageID {
			return errConversationDispatchConflict
		}
		lockedAssignment, err := repositories.ConversationAssignmentRepository.GetForUpdateInTenant(ctx.Tx, assignment.ID, assignment.TenantID)
		if err != nil {
			return err
		}
		if lockedAssignment == nil || lockedAssignment.Status != enums.IMAssignmentStatusActive || lockedAssignment.DispatchMode != enums.AgentTeamDispatchModeRule || lockedAssignment.ConversationID != current.ID || lockedAssignment.ToUserID != current.CurrentAssigneeID || lockedAssignment.SessionNo != currentSessionNoDB(ctx.Tx, current.ID, current.TenantID) {
			return errConversationDispatchConflict
		}
		if _, err := s.validateRuleAssignmentRecoveryStateDB(ctx.Tx, recovery, lockedAssignment, current, now); err != nil {
			return err
		}
		attempts, err := s.ruleAssignmentRecoveryAttempts(ctx.Tx, lockedAssignment)
		if err != nil {
			return err
		}
		if (exhausted && attempts < maxRuleAssignmentRecoveryAttempts) || (!exhausted && attempts >= maxRuleAssignmentRecoveryAttempts) {
			return errConversationDispatchConflict
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, current.ID, now); err != nil {
			return err
		}
		result := ctx.Tx.Model(&models.Conversation{}).
			Where("id = ? AND tenant_id = ? AND status = ? AND current_assignee_id = ? AND last_message_id = ?", current.ID, current.TenantID, enums.IMConversationStatusActive, lockedAssignment.ToUserID, current.LastMessageID).
			Updates(map[string]any{
				"status":              enums.IMConversationStatusPending,
				"current_assignee_id": int64(0),
				"current_team_id":     current.CurrentTeamID,
				"update_user_id":      int64(0),
				"update_user_name":    "system",
				"updated_at":          now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errConversationDispatchConflict
		}
		if err := ConversationEventLogService.CreateEvent(ctx, current.ID, enums.IMEventTypeTransfer, enums.IMSenderTypeSystem, 0, "会话已自动释放回待派发池", ConversationService.buildEventPayload(map[string]any{
			"fromStatus":     enums.IMConversationStatusActive,
			"toStatus":       enums.IMConversationStatusPending,
			"fromAssigneeId": lockedAssignment.ToUserID,
			"toAssigneeId":   int64(0),
			"toTeamId":       current.CurrentTeamID,
			"reason":         reason,
			"dispatchMode":   enums.AgentTeamDispatchModeRule,
			"recoveryLimit":  maxRuleAssignmentRecoveryAttempts,
		})); err != nil {
			return err
		}
		_, err = ConversationRouteService.enterHQAgentDeskPendingWithDB(ctx.Tx, current.ID, reason, now)
		return err
	})
	if err != nil {
		return false, err
	}
	_ = ServiceAnalyticsCaptureService.RecordDispatchEvidence(DispatchDecisionEvidence{
		ConversationID:     conversation.ID,
		SessionNo:          assignment.SessionNo,
		Trigger:            "auto_recovery",
		DecisionMode:       string(enums.AgentTeamDispatchModeRule),
		Status:             enums.DispatchDecisionStatusFallback,
		InputLastMessageID: conversation.LastMessageID,
		SelectedTeamID:     conversation.CurrentTeamID,
		Reason:             reason,
		FallbackReason:     cause.code,
		DecidedAt:          now,
	})
	s.notifyRecoveredConversation(conversation, assignment, reason)
	if !exhausted {
		s.ScheduleDispatch(conversation.ID)
	}
	if updated := ConversationService.Get(conversation.ID); updated != nil {
		WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationUpdated)
	}
	return true, nil
}
