package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ConversationDispatchService = newConversationDispatchService()

func newConversationDispatchService() *conversationDispatchService {
	return &conversationDispatchService{
		dispatchTimers: make(map[string]*time.Timer),
	}
}

type conversationDispatchService struct {
	dispatchMu                sync.Mutex
	dispatchTimers            map[string]*time.Timer
	dispatching               sync.Map
	teamDispatching           sync.Map
	realtimeSchedulingEnabled atomic.Bool
	pendingTenantCursor       atomic.Int64
	pendingTeamCursor         atomic.Int64
}

type dispatchCandidate struct {
	profile             models.AgentProfile
	squadID             int64
	activeCount         int
	weightedOpenLoad    int
	pendingFirstReply   int
	pendingReplyCount   int
	shiftWorkloadWeight int
	normalizedLoad      float64
	lastAssignedAt      time.Time
}

type agentActiveConversationCount struct {
	CurrentAssigneeID int64 `gorm:"column:current_assignee_id"`
	ActiveCount       int   `gorm:"column:active_count"`
}

type dispatchPoolReport struct {
	RequestedTeamIDs    []int64
	ActiveScheduleTeams []int64
	MatchedProfiles     int
	EligibleProfiles    int
	OnlineProfiles      int
	CandidateCount      int
	Reason              string
}

var (
	errConversationDispatchConflict     = errors.New("conversation dispatch conflict")
	errConversationDispatchTeamMismatch = errors.New("conversation dispatch team mismatch")
)

const (
	dispatchTriggerAutomatic    = "auto_dispatch"
	dispatchTriggerOperatorRule = "operator_rule_dispatch"
)

type ruleDispatchExecutionContext struct {
	operator       *dto.AuthPrincipal
	expectedTeamID int64
	trigger        string
	interactive    bool
}

func normalizeRuleDispatchExecutionContext(execution ruleDispatchExecutionContext) ruleDispatchExecutionContext {
	if execution.operator == nil {
		execution.operator = systemDispatchPrincipal()
	}
	execution.trigger = strings.TrimSpace(execution.trigger)
	if execution.trigger == "" {
		execution.trigger = dispatchTriggerAutomatic
	}
	return execution
}

const (
	pendingDispatchBatchLimit     = 50
	pendingDispatchTeamScanLimit  = 1000
	pendingDispatchTeamScanFactor = 10
	pendingCompensationTeamLimit  = 100
	pendingUnresolvedScanLimit    = 20
)

const dispatchDebounceDelay = 800 * time.Millisecond

var pendingDispatchRunning atomic.Bool

type activeScheduleSelection struct {
	ScheduleID              int64
	SquadID                 int64
	IncludedAgentProfileIDs []int64
	ExcludedAgentProfileIDs []int64
	StartAt                 time.Time
	EndAt                   time.Time
	Windows                 []activeScheduleWindow
}

type activeScheduleWindow struct {
	ScheduleID              int64
	SquadID                 int64
	IncludedAgentProfileIDs []int64
	ExcludedAgentProfileIDs []int64
	StartAt                 time.Time
	EndAt                   time.Time
}

type dispatchPresenceSnapshot struct {
	Status     enums.AgentPresenceStatus
	LastSeenAt time.Time
}

const dispatchPresenceFreshness = 3 * time.Minute

func (s *conversationDispatchService) ScheduleDispatch(conversationID int64) {
	if conversationID <= 0 || !s.realtimeSchedulingEnabled.Load() {
		return
	}
	conversation := ConversationService.Get(conversationID)
	if conversation != nil && conversation.TenantID > 0 {
		route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
		if teamIDs := s.resolveDispatchTeamIDs(conversation, route); len(teamIDs) == 1 {
			s.ScheduleTeamDispatch(conversation.TenantID, teamIDs[0])
			return
		}
	}
	s.scheduleDispatchTimer(fmt.Sprintf("conversation:%d", conversationID), func() error {
		_, err := s.DispatchPendingTeamForConversation(conversationID, pendingDispatchBatchLimit)
		return err
	})
}

// ScheduleTeamDispatch coalesces bursty conversation, presence and capacity events
// into one queue drain for the affected team.
func (s *conversationDispatchService) ScheduleTeamDispatch(tenantID, teamID int64) {
	if tenantID <= 0 || teamID <= 0 || !s.realtimeSchedulingEnabled.Load() {
		return
	}
	key := fmt.Sprintf("team:%d:%d", tenantID, teamID)
	s.scheduleDispatchTimer(key, func() error {
		count, err := s.DispatchPendingTeam(tenantID, teamID, pendingDispatchBatchLimit)
		if err == nil && count >= pendingDispatchBatchLimit {
			s.ScheduleTeamDispatch(tenantID, teamID)
		}
		return err
	})
}

func (s *conversationDispatchService) scheduleDispatchTimer(key string, run func() error) {
	if strings.TrimSpace(key) == "" || run == nil || !s.realtimeSchedulingEnabled.Load() {
		return
	}
	db := sqls.DB()
	if db == nil {
		return
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if timer := s.dispatchTimers[key]; timer != nil {
		timer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(dispatchDebounceDelay, func() {
		s.dispatchMu.Lock()
		if s.dispatchTimers[key] != timer {
			s.dispatchMu.Unlock()
			return
		}
		delete(s.dispatchTimers, key)
		s.dispatchMu.Unlock()
		if sqls.DB() != db || !dispatchDatabaseReady(db) {
			return
		}
		if err := run(); err != nil {
			slog.Warn("scheduled conversation dispatch failed", "dispatch_key", key, "error", err)
		}
	})
	s.dispatchTimers[key] = timer
}

func (s *conversationDispatchService) DispatchPendingTeamForConversation(conversationID int64, limit int) (int, error) {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil || conversation.TenantID <= 0 {
		return 0, nil
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	teamIDs := s.resolveDispatchTeamIDs(conversation, route)
	if len(teamIDs) != 1 {
		if conversation.Status == enums.IMConversationStatusPending && conversation.CurrentAssigneeID == 0 {
			_, err := s.DispatchConversation(conversation.ID)
			return 0, err
		}
		return 0, nil
	}
	return s.DispatchPendingTeam(conversation.TenantID, teamIDs[0], limit)
}

func (s *conversationDispatchService) DispatchPendingTeam(tenantID, teamID int64, limit int) (int, error) {
	if tenantID <= 0 || teamID <= 0 {
		return 0, nil
	}
	key := fmt.Sprintf("%d:%d", tenantID, teamID)
	if _, loaded := s.teamDispatching.LoadOrStore(key, struct{}{}); loaded {
		return 0, nil
	}
	defer s.teamDispatching.Delete(key)
	if limit <= 0 || limit > pendingDispatchBatchLimit {
		limit = pendingDispatchBatchLimit
	}

	team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, tenantID)
	if team == nil || team.Status != enums.StatusOk || normalizedDispatchMode(team.DispatchMode) != enums.AgentTeamDispatchModeRule {
		return 0, nil
	}
	scanLimit := limit * pendingDispatchTeamScanFactor
	if scanLimit < pendingDispatchBatchLimit {
		scanLimit = pendingDispatchBatchLimit
	}
	if scanLimit > pendingDispatchTeamScanLimit {
		scanLimit = pendingDispatchTeamScanLimit
	}
	conversations, err := repositories.ConversationRepository.FindPendingUnassignedForTeam(sqls.DB(), tenantID, teamID, scanLimit)
	if err != nil {
		return 0, err
	}
	conversations = s.prioritizePendingConversationWindow(conversations, time.Now(), limit)
	dispatchedCount := 0
	var firstErr error
	for i := range conversations {
		if dispatchedCount >= limit {
			break
		}
		conversation := &conversations[i]
		route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, tenantID)
		resolvedTeamIDs := s.resolveDispatchTeamIDs(conversation, route)
		if len(resolvedTeamIDs) != 1 || resolvedTeamIDs[0] != teamID {
			continue
		}
		dispatched, err := s.DispatchPendingConversation(conversation)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			slog.Warn("team pending conversation dispatch failed", "tenant_id", tenantID, "team_id", teamID, "conversation_id", conversation.ID, "error", err)
			continue
		}
		if dispatched != nil {
			dispatchedCount++
		}
	}
	return dispatchedCount, firstErr
}

func (s *conversationDispatchService) EnableRealtimeScheduling() {
	s.realtimeSchedulingEnabled.Store(true)
}

func dispatchDatabaseReady(db *gorm.DB) bool {
	sqlDB, err := db.DB()
	return err == nil && sqlDB.Ping() == nil
}

func (s *conversationDispatchService) DispatchConversation(conversationID int64) (*models.Conversation, error) {
	if conversationID <= 0 {
		return nil, nil
	}
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return nil, nil
	}
	if conversation.TenantID <= 0 || conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
		return nil, nil
	}
	return s.DispatchPendingConversation(conversation)
}

func (s *conversationDispatchService) DispatchPendingConversation(conversation *models.Conversation) (*models.Conversation, error) {
	return s.dispatchPendingConversationWithContext(conversation, ruleDispatchExecutionContext{})
}

func (s *conversationDispatchService) dispatchPendingConversationWithContext(conversation *models.Conversation, execution ruleDispatchExecutionContext) (*models.Conversation, error) {
	if conversation == nil {
		return nil, nil
	}
	if conversation.TenantID <= 0 || conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
		return nil, nil
	}
	execution = normalizeRuleDispatchExecutionContext(execution)
	decisionStartedAt := time.Now()
	if _, loaded := s.dispatching.LoadOrStore(conversation.ID, struct{}{}); loaded {
		s.ScheduleDispatch(conversation.ID)
		if execution.interactive {
			return nil, errConversationDispatchConflict
		}
		return nil, nil
	}
	defer s.dispatching.Delete(conversation.ID)

	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	teamIDs := s.resolveDispatchTeamIDs(conversation, route)
	if execution.expectedTeamID > 0 && (len(teamIDs) != 1 || teamIDs[0] != execution.expectedTeamID) {
		s.recordAutomaticDispatchEvidence(conversation, teamIDs, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusStale, "会话归属客服组已变化", "team_mismatch", execution, decisionStartedAt)
		return nil, errConversationDispatchTeamMismatch
	}
	recoveryLimitReached, err := s.ruleDispatchRecoveryLimitReached(conversation)
	if err != nil {
		return nil, err
	}
	if recoveryLimitReached {
		s.notifyDispatchAttentionOnce(conversation, singleDispatchEvidenceTeamID(teamIDs), "dispatch_recovery_exhausted", "人工会话需要编排", "已达到规则自动重派上限", pendingConversationAt(*conversation))
		return nil, nil
	}
	if len(teamIDs) == 0 {
		slog.Debug("skip auto dispatch due to unresolved team",
			"conversation_id", conversation.ID,
			"ai_agent_id", conversation.AIAgentID,
		)
		s.recordAutomaticDispatchEvidence(conversation, nil, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "未找到可承接该会话的客服组", "unresolved_team", execution, decisionStartedAt)
		s.notifyUnassignedConversationIfOverdue(conversation, 0, "unresolved_team", "未找到可承接该会话的客服组", time.Now())
		return nil, nil
	}
	teamIDs = s.filterAutomaticTeamIDs(teamIDs, conversation.TenantID)
	if len(teamIDs) == 0 {
		slog.Debug("skip automatic dispatch for manual teams", "conversation_id", conversation.ID)
		return nil, nil
	}

	candidates, report, err := s.pickDispatchCandidates(teamIDs, conversation.TenantID, route, time.Now())
	if err != nil {
		s.recordAutomaticDispatchEvidence(conversation, teamIDs, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "自动派单候选计算失败", err.Error(), execution, decisionStartedAt)
		return nil, err
	}
	candidates, err = s.filterRuleRetryCooldownCandidates(conversation, candidates, time.Now())
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 && report.CandidateCount > 0 {
		report.CandidateCount = 0
		report.Reason = "recent_assignment_cooldown"
	}
	if len(candidates) == 0 {
		slog.Debug("no dispatch candidate available",
			"conversation_id", conversation.ID,
			"ai_agent_id", conversation.AIAgentID,
			"requested_team_ids", report.RequestedTeamIDs,
			"active_schedule_team_ids", report.ActiveScheduleTeams,
			"matched_profiles", report.MatchedProfiles,
			"eligible_profiles", report.EligibleProfiles,
			"reason", report.Reason,
		)
		s.recordAutomaticDispatchEvidence(conversation, teamIDs, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "当前值班范围内没有可接待客服", report.Reason, execution, decisionStartedAt)
		s.notifyUnassignedConversationIfOverdue(conversation, singleDispatchEvidenceTeamID(teamIDs), report.Reason, "当前值班范围内没有可接待客服", time.Now())
		return nil, nil
	}

	decision := s.selectDispatchDecision(conversation, route, candidates)
	orderedCandidates := append([]dispatchCandidate{decision.candidate}, candidates...)
	tried := make(map[int64]struct{}, len(orderedCandidates))
	for _, candidate := range orderedCandidates {
		if _, exists := tried[candidate.profile.UserID]; exists {
			continue
		}
		tried[candidate.profile.UserID] = struct{}{}
		candidateDecision := decision
		candidateDecision.candidate = candidate
		if candidate.profile.UserID != decision.candidate.profile.UserID {
			candidateDecision.reason = "规则首选候选失效，按公平队列顺延"
		}
		dispatched, err := s.tryAssignWithDecisionContext(conversation.ID, candidateDecision, execution)
		if err != nil {
			if errors.Is(err, errConversationDispatchConflict) || errors.Is(err, errConversationDispatchTeamMismatch) {
				s.recordAutomaticDispatchEvidence(conversation, teamIDs, candidates, &candidate, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusStale, "派单候选或会话状态已变化", err.Error(), execution, decisionStartedAt)
				if latest := ConversationService.Get(conversation.ID); latest != nil && latest.Status == enums.IMConversationStatusPending && latest.CurrentAssigneeID == 0 {
					s.ScheduleDispatch(conversation.ID)
				}
				if execution.interactive {
					return nil, err
				}
				return nil, nil
			}
			s.recordAutomaticDispatchEvidence(conversation, teamIDs, candidates, &candidate, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "自动派单执行失败", err.Error(), execution, decisionStartedAt)
			return nil, err
		}
		if dispatched != nil {
			assignmentID := int64(0)
			if assignment := repositories.ConversationAssignmentRepository.FindOne(sqls.DB(), sqls.NewCnd().
				Eq("tenant_id", conversation.TenantID).
				Eq("conversation_id", conversation.ID).
				Eq("to_user_id", candidate.profile.UserID).
				Desc("id")); assignment != nil {
				assignmentID = assignment.ID
			}
			s.recordAutomaticDispatchEvidence(conversation, teamIDs, candidates, &candidate, assignmentID, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusSelected, candidateDecision.reason, "", execution, decisionStartedAt)
			slog.Info("conversation auto dispatched",
				"conversation_id", dispatched.ID,
				"ai_agent_id", conversation.AIAgentID,
				"assignee_id", dispatched.CurrentAssigneeID,
				"team_id", dispatched.CurrentTeamID,
				"candidate_count", report.CandidateCount,
				"requested_team_ids", report.RequestedTeamIDs,
				"dispatch_mode", enums.AgentTeamDispatchModeRule,
				"workload_weight", candidateDecision.workloadWeight,
			)
			WsService.PublishConversationChanged(dispatched, enums.IMRealtimeEventConversationAssigned)
			eventbus.PublishAsync(context.Background(), events.ConversationAssignedEvent{
				ConversationID: dispatched.ID,
				ToUserID:       dispatched.CurrentAssigneeID,
				OperatorID:     execution.operator.UserID,
				Reason:         candidateDecision.reason,
				AssignType:     events.ConversationAssignTypeAutoAssign,
			})
			return dispatched, nil
		}
	}
	slog.Debug("auto dispatch candidate list exhausted without assignment",
		"conversation_id", conversation.ID,
		"ai_agent_id", conversation.AIAgentID,
		"candidate_count", report.CandidateCount,
	)
	s.recordAutomaticDispatchEvidence(conversation, teamIDs, candidates, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "自动派单候选已全部失效", "candidate_exhausted", execution, decisionStartedAt)
	s.notifyUnassignedConversationIfOverdue(conversation, singleDispatchEvidenceTeamID(teamIDs), "candidate_exhausted", "自动派单候选已全部失效", time.Now())
	return nil, nil
}

func (s *conversationDispatchService) recordAutomaticDispatchEvidence(
	conversation *models.Conversation,
	requestedTeamIDs []int64,
	candidates []dispatchCandidate,
	selected *dispatchCandidate,
	assignmentID int64,
	mode string,
	status enums.DispatchDecisionStatus,
	reason string,
	fallbackReason string,
	execution ruleDispatchExecutionContext,
	startedAt time.Time,
) {
	if conversation == nil || conversation.TenantID <= 0 {
		return
	}
	evidenceCandidates := make([]DispatchDecisionCandidateEvidence, 0, len(candidates))
	for _, candidate := range candidates {
		evidenceCandidates = append(evidenceCandidates, DispatchDecisionCandidateEvidence{
			UserID: candidate.profile.UserID, TeamID: candidate.profile.TeamID, SquadID: candidate.squadID,
			ActiveCount: candidate.activeCount, WeightedOpenLoad: candidate.weightedOpenLoad,
			PendingFirstReply: candidate.pendingFirstReply, PendingReplyCount: candidate.pendingReplyCount,
			ShiftWorkloadWeight: candidate.shiftWorkloadWeight, NormalizedLoad: candidate.normalizedLoad,
		})
	}
	execution = normalizeRuleDispatchExecutionContext(execution)
	evidence := DispatchDecisionEvidence{
		ConversationID: conversation.ID, Trigger: execution.trigger, DecisionMode: mode, Status: status,
		Candidates: evidenceCandidates, InputLastMessageID: conversation.LastMessageID,
		DecisionLatencyMillis: time.Since(startedAt).Milliseconds(), Reason: reason, FallbackReason: fallbackReason,
		OperatorID: execution.operator.UserID, DecidedAt: time.Now(), AssignmentID: assignmentID,
	}
	if selected != nil {
		evidence.SelectedUserID = selected.profile.UserID
		evidence.SelectedTeamID = selected.profile.TeamID
		evidence.SelectedSquadID = selected.squadID
	} else {
		evidence.SelectedTeamID = singleDispatchEvidenceTeamID(requestedTeamIDs)
	}
	if err := ServiceAnalyticsCaptureService.RecordDispatchEvidence(evidence); err != nil {
		slog.Warn("record automatic dispatch evidence failed", "conversation_id", conversation.ID, "status", status, "error", err)
	}
}

func singleDispatchEvidenceTeamID(teamIDs []int64) int64 {
	var selected int64
	for _, teamID := range teamIDs {
		if teamID <= 0 || teamID == selected {
			continue
		}
		if selected > 0 {
			return 0
		}
		selected = teamID
	}
	return selected
}

func (s *conversationDispatchService) DispatchPendingConversations(limit int) (int, error) {
	if !pendingDispatchRunning.CompareAndSwap(false, true) {
		return 0, nil
	}
	defer pendingDispatchRunning.Store(false)

	if limit <= 0 || limit > pendingDispatchBatchLimit {
		limit = pendingDispatchBatchLimit
	}
	tenantIDs, err := repositories.ConversationRepository.FindPendingUnassignedTenantIDs(sqls.DB(), s.pendingTenantCursor.Load(), limit)
	if err != nil {
		return 0, err
	}
	if len(tenantIDs) == 0 {
		return 0, nil
	}
	s.pendingTenantCursor.Store(tenantIDs[len(tenantIDs)-1])
	perTenantLimit := (limit + len(tenantIDs) - 1) / len(tenantIDs)
	dispatchedCount := 0
	scannedCount := 0
	var firstErr error
	for _, tenantID := range tenantIDs {
		if dispatchedCount >= limit {
			break
		}
		budget := min(perTenantLimit, limit-dispatchedCount)
		count, scanned, err := s.dispatchPendingRuleTeamsForTenant(tenantID, budget)
		dispatchedCount += count
		scannedCount += scanned
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err := s.inspectUnresolvedPendingConversations(tenantID, 1); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if scannedCount > 0 {
		slog.Info("pending conversation dispatch scan completed",
			"scanned_count", scannedCount,
			"dispatched_count", dispatchedCount,
			"limit", limit,
		)
	}
	return dispatchedCount, firstErr
}

func (s *conversationDispatchService) dispatchPendingRuleTeamsForTenant(tenantID int64, budget int) (dispatchedCount, scannedCount int, firstErr error) {
	if tenantID <= 0 || budget <= 0 {
		return 0, 0, nil
	}
	teams := repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("status", enums.StatusOk).
		Asc("id").
		Limit(pendingCompensationTeamLimit))
	ruleTeams := make([]models.AgentTeam, 0, len(teams))
	for _, team := range teams {
		if normalizedDispatchMode(team.DispatchMode) == enums.AgentTeamDispatchModeRule {
			ruleTeams = append(ruleTeams, team)
		}
	}
	if len(ruleTeams) == 0 {
		return 0, 0, nil
	}
	ruleTeams = rotateDispatchTeams(ruleTeams, s.pendingTeamCursor.Load())
	for i, team := range ruleTeams {
		if dispatchedCount >= budget {
			break
		}
		remainingTeams := len(ruleTeams) - i
		teamBudget := (budget - dispatchedCount + remainingTeams - 1) / remainingTeams
		if teamBudget < 1 {
			teamBudget = 1
		}
		count, err := s.DispatchPendingTeam(tenantID, team.ID, teamBudget)
		scannedCount++
		s.pendingTeamCursor.Store(team.ID)
		dispatchedCount += count
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			slog.Warn("rule team compensation dispatch failed", "tenant_id", tenantID, "team_id", team.ID, "error", err)
		}
	}
	return dispatchedCount, scannedCount, firstErr
}

func rotateDispatchTeams(teams []models.AgentTeam, afterTeamID int64) []models.AgentTeam {
	if len(teams) < 2 || afterTeamID <= 0 {
		return teams
	}
	start := 0
	for i := range teams {
		if teams[i].ID > afterTeamID {
			start = i
			break
		}
		if i == len(teams)-1 {
			start = 0
		}
	}
	if start == 0 {
		return teams
	}
	ret := make([]models.AgentTeam, 0, len(teams))
	ret = append(ret, teams[start:]...)
	ret = append(ret, teams[:start]...)
	return ret
}

func (s *conversationDispatchService) inspectUnresolvedPendingConversations(tenantID int64, limit int) error {
	if tenantID <= 0 || limit <= 0 {
		return nil
	}
	items, err := repositories.ConversationRepository.FindPendingUnassignedByTenant(sqls.DB(), tenantID, pendingUnresolvedScanLimit)
	if err != nil {
		return err
	}
	inspected := 0
	for i := range items {
		conversation := &items[i]
		route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, tenantID)
		if len(s.resolveDispatchTeamIDs(conversation, route)) != 0 {
			continue
		}
		if _, err := s.DispatchPendingConversation(conversation); err != nil {
			return err
		}
		inspected++
		if inspected >= limit {
			break
		}
	}
	return nil
}

// pickDispatchCandidates returns the eligible dispatch candidates for the given teamIDs at the given time, along with a report for debugging and analysis.
func (s *conversationDispatchService) pickDispatchCandidates(teamIDs []int64, tenantID int64, route *models.ConversationRouteState, now time.Time) ([]dispatchCandidate, dispatchPoolReport, error) {
	report := dispatchPoolReport{
		RequestedTeamIDs: append([]int64(nil), teamIDs...),
	}

	// 1. filter teams with active schedule
	if tenantID <= 0 || (route != nil && route.TenantID != tenantID) {
		report.Reason = "invalid_tenant_scope"
		return nil, report, nil
	}
	activeScheduleDetails := s.findActiveScheduleDetails(teamIDs, tenantID, now)
	activeTeamIDs := make([]int64, 0, len(activeScheduleDetails))
	for _, teamID := range teamIDs {
		if _, ok := activeScheduleDetails[teamID]; ok {
			activeTeamIDs = append(activeTeamIDs, teamID)
		}
	}
	report.ActiveScheduleTeams = activeTeamIDs
	if len(activeTeamIDs) == 0 {
		report.Reason = "no_active_schedule_team"
		return nil, report, nil
	}

	// 2. find agent profiles for the active teams
	profiles := AgentProfileService.Find(sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("team_id", activeTeamIDs).
		Eq("status", enums.StatusOk).
		Eq("auto_assign_enabled", true).
		Where("max_concurrent_count > ?", 0))
	profiles, scheduleWindowByProfileID := s.filterProfilesByActiveSchedules(profiles, activeScheduleDetails, tenantID)
	report.MatchedProfiles = len(profiles)
	if len(profiles) == 0 {
		report.Reason = "no_matched_profile"
		return nil, report, nil
	}

	enabledProfiles, enabledUserIDs, reason := s.filterEnabledDispatchProfiles(profiles, tenantID)
	if reason != "" {
		report.Reason = reason
		return nil, report, nil
	}
	report.EligibleProfiles = len(enabledProfiles)
	permittedUserIDs, err := repositories.PermissionRepository.FindUserIDsWithAllCodes(sqls.DB(), enabledUserIDs, []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
	})
	if err != nil {
		return nil, report, err
	}
	permittedUserSet := int64Set(permittedUserIDs)
	permittedProfiles := make([]models.AgentProfile, 0, len(enabledProfiles))
	for _, profile := range enabledProfiles {
		if _, ok := permittedUserSet[profile.UserID]; ok {
			permittedProfiles = append(permittedProfiles, profile)
		}
	}
	enabledProfiles = permittedProfiles
	if len(enabledProfiles) == 0 {
		report.Reason = "no_agent_with_reply_permission"
		return nil, report, nil
	}
	if route != nil {
		scopedProfiles := make([]models.AgentProfile, 0, len(enabledProfiles))
		for _, profile := range enabledProfiles {
			if AgentProfileService.ProfileCanServeRoute(&profile, route) {
				scopedProfiles = append(scopedProfiles, profile)
			}
		}
		enabledProfiles = scopedProfiles
		if len(enabledProfiles) == 0 {
			report.Reason = "no_profile_in_store_scope"
			return nil, report, nil
		}
	}
	enabledUserIDs = enabledUserIDs[:0]
	for _, profile := range enabledProfiles {
		enabledUserIDs = append(enabledUserIDs, profile.UserID)
	}
	presenceByUserID := s.loadDispatchPresenceMapDB(sqls.DB(), tenantID, enabledUserIDs, now)
	onlineProfiles := make([]models.AgentProfile, 0, len(enabledProfiles))
	for _, profile := range enabledProfiles {
		if isDispatchPresenceEligible(presenceByUserID[profile.UserID], now) {
			onlineProfiles = append(onlineProfiles, profile)
		}
	}
	report.OnlineProfiles = len(onlineProfiles)
	if len(onlineProfiles) == 0 {
		report.Reason = "no_online_agent"
		return nil, report, nil
	}
	enabledProfiles = onlineProfiles

	loads, err := s.buildDispatchLoadMap(enabledProfiles, activeScheduleDetails, tenantID)
	if err != nil {
		return nil, report, err
	}
	candidates := make([]dispatchCandidate, 0, len(enabledProfiles))
	for _, profile := range enabledProfiles {
		load := loads[profile.UserID]
		if profile.MaxConcurrentCount <= 0 || load.activeCount >= profile.MaxConcurrentCount {
			continue
		}
		normalizedLoad := normalizedDispatchPressure(load, profile.MaxConcurrentCount)
		candidates = append(candidates, dispatchCandidate{
			profile:             profile,
			squadID:             scheduleWindowByProfileID[profile.ID].SquadID,
			activeCount:         load.activeCount,
			weightedOpenLoad:    load.weightedOpenLoad,
			pendingFirstReply:   load.pendingFirstReply,
			pendingReplyCount:   load.pendingReplyCount,
			shiftWorkloadWeight: load.shiftWorkloadWeight,
			normalizedLoad:      normalizedLoad,
			lastAssignedAt:      load.lastAssignedAt,
		})
	}
	report.CandidateCount = len(candidates)
	if len(candidates) == 0 {
		report.Reason = "all_candidates_at_capacity"
		return nil, report, nil
	}

	slices.SortFunc(candidates, func(a, b dispatchCandidate) int {
		switch {
		case a.normalizedLoad < b.normalizedLoad:
			return -1
		case a.normalizedLoad > b.normalizedLoad:
			return 1
		}
		aDebt := dispatchShiftDebt(a)
		bDebt := dispatchShiftDebt(b)
		switch {
		case aDebt < bDebt:
			return -1
		case aDebt > bDebt:
			return 1
		}
		switch {
		case a.activeCount < b.activeCount:
			return -1
		case a.activeCount > b.activeCount:
			return 1
		}
		switch {
		case a.lastAssignedAt.Before(b.lastAssignedAt):
			return -1
		case a.lastAssignedAt.After(b.lastAssignedAt):
			return 1
		}
		switch {
		case a.profile.PriorityLevel > b.profile.PriorityLevel:
			return -1
		case a.profile.PriorityLevel < b.profile.PriorityLevel:
			return 1
		}
		switch {
		case a.profile.UserID < b.profile.UserID:
			return -1
		case a.profile.UserID > b.profile.UserID:
			return 1
		default:
			return 0
		}
	})
	report.Reason = "ok"
	return candidates, report, nil
}

func (s *conversationDispatchService) filterEnabledDispatchProfiles(profiles []models.AgentProfile, tenantID int64) ([]models.AgentProfile, []int64, string) {
	userIDs := make([]int64, 0, len(profiles))
	for _, profile := range profiles {
		if profile.UserID > 0 {
			userIDs = append(userIDs, profile.UserID)
		}
	}
	if len(userIDs) == 0 {
		return nil, nil, "no_profile_with_capacity_config"
	}

	enabledUsers := UserService.Find(sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("id", userIDs).
		Eq("status", enums.StatusOk))
	if len(enabledUsers) == 0 {
		return nil, nil, "no_enabled_user"
	}

	enabledUserSet := make(map[int64]struct{}, len(enabledUsers))
	for _, user := range enabledUsers {
		enabledUserSet[user.ID] = struct{}{}
	}

	enabledProfiles := make([]models.AgentProfile, 0, len(profiles))
	enabledUserIDs := make([]int64, 0, len(profiles))
	for _, profile := range profiles {
		if _, exists := enabledUserSet[profile.UserID]; !exists {
			continue
		}
		enabledProfiles = append(enabledProfiles, profile)
		enabledUserIDs = append(enabledUserIDs, profile.UserID)
	}
	if len(enabledProfiles) == 0 {
		return nil, nil, "no_profile_for_enabled_user"
	}
	return enabledProfiles, enabledUserIDs, ""
}

// findActiveScheduleTeamIDs returns the subset of teamIDs that have active schedule at the given time.
func (s *conversationDispatchService) findActiveScheduleSelections(teamIDs []int64, tenantID int64, now time.Time) map[int64]int64 {
	details := s.findActiveScheduleDetails(teamIDs, tenantID, now)
	ret := make(map[int64]int64, len(details))
	for teamID, selection := range details {
		ret[teamID] = selection.SquadID
	}
	return ret
}

func (s *conversationDispatchService) findActiveScheduleDetails(teamIDs []int64, tenantID int64, now time.Time) map[int64]activeScheduleSelection {
	return s.findActiveScheduleDetailsDB(sqls.DB(), teamIDs, tenantID, now)
}

func (s *conversationDispatchService) findActiveScheduleDetailsDB(db *gorm.DB, teamIDs []int64, tenantID int64, now time.Time) map[int64]activeScheduleSelection {
	if db == nil || len(teamIDs) == 0 || tenantID <= 0 {
		return map[int64]activeScheduleSelection{}
	}

	teams := repositories.AgentTeamRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("id", teamIDs).
		Eq("status", enums.StatusOk))
	if len(teams) == 0 {
		return map[int64]activeScheduleSelection{}
	}

	enabledTeamIDs := make([]int64, 0, len(teams))
	for _, team := range teams {
		enabledTeamIDs = append(enabledTeamIDs, team.ID)
	}

	schedules := repositories.AgentTeamScheduleRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("team_id", enabledTeamIDs).
		Eq("status", enums.StatusOk).
		Lte("start_at", now).
		Gt("end_at", now).
		Desc("start_at").
		Desc("id"))

	activeSet := make(map[int64]activeScheduleSelection, len(schedules))
	for _, schedule := range schedules {
		window := activeScheduleWindow{
			ScheduleID:              schedule.ID,
			SquadID:                 schedule.SquadID,
			IncludedAgentProfileIDs: utils.SplitInt64s(schedule.IncludedAgentProfileIDs),
			ExcludedAgentProfileIDs: utils.SplitInt64s(schedule.ExcludedAgentProfileIDs),
			StartAt:                 schedule.StartAt,
			EndAt:                   schedule.EndAt,
		}
		selection, exists := activeSet[schedule.TeamID]
		if !exists {
			selection = activeScheduleSelection{
				ScheduleID:              window.ScheduleID,
				SquadID:                 window.SquadID,
				IncludedAgentProfileIDs: append([]int64(nil), window.IncludedAgentProfileIDs...),
				ExcludedAgentProfileIDs: append([]int64(nil), window.ExcludedAgentProfileIDs...),
				StartAt:                 window.StartAt,
				EndAt:                   window.EndAt,
			}
		} else {
			selection.ScheduleID = 0
			selection.SquadID = 0
			selection.IncludedAgentProfileIDs = nil
			selection.ExcludedAgentProfileIDs = nil
			if window.StartAt.Before(selection.StartAt) {
				selection.StartAt = window.StartAt
			}
			if window.EndAt.After(selection.EndAt) {
				selection.EndAt = window.EndAt
			}
		}
		selection.Windows = append(selection.Windows, window)
		activeSet[schedule.TeamID] = selection
	}
	return activeSet
}

func (s *conversationDispatchService) findActiveScheduleTeamIDs(teamIDs []int64, tenantID int64, now time.Time) []int64 {
	activeSchedules := s.findActiveScheduleSelections(teamIDs, tenantID, now)
	ret := make([]int64, 0, len(activeSchedules))
	for _, teamID := range teamIDs {
		if _, ok := activeSchedules[teamID]; ok && !slices.Contains(ret, teamID) {
			ret = append(ret, teamID)
		}
	}
	return ret
}

func (s *conversationDispatchService) filterProfilesByActiveSchedules(profiles []models.AgentProfile, activeSchedules map[int64]activeScheduleSelection, tenantID int64) ([]models.AgentProfile, map[int64]activeScheduleWindow) {
	squadIDs := activeScheduleSquadIDs(activeSchedules)
	membersBySquad, teamBySquad := AgentTeamSquadService.ActiveMemberProfileSet(squadIDs, tenantID)
	ret := make([]models.AgentProfile, 0, len(profiles))
	windowByProfileID := make(map[int64]activeScheduleWindow, len(profiles))
	for i := range profiles {
		selection, scheduled := activeSchedules[profiles[i].TeamID]
		if !scheduled {
			continue
		}
		window, matched := matchingActiveScheduleSnapshot(&profiles[i], selection, membersBySquad, teamBySquad)
		if !matched {
			continue
		}
		ret = append(ret, profiles[i])
		windowByProfileID[profiles[i].ID] = window
	}
	return ret, windowByProfileID
}

func profileMatchesActiveScheduleSnapshot(profile *models.AgentProfile, selection activeScheduleSelection, membersBySquad map[int64]map[int64]struct{}, teamBySquad map[int64]int64) bool {
	_, matched := matchingActiveScheduleSnapshot(profile, selection, membersBySquad, teamBySquad)
	return matched
}

func matchingActiveScheduleSnapshot(profile *models.AgentProfile, selection activeScheduleSelection, membersBySquad map[int64]map[int64]struct{}, teamBySquad map[int64]int64) (activeScheduleWindow, bool) {
	if profile == nil || profile.ID <= 0 || profile.TeamID <= 0 {
		return activeScheduleWindow{}, false
	}
	for _, window := range activeScheduleWindows(selection) {
		if slices.Contains(window.ExcludedAgentProfileIDs, profile.ID) {
			continue
		}
		if window.SquadID <= 0 {
			return window, true
		}
		if teamBySquad[window.SquadID] != profile.TeamID {
			continue
		}
		if slices.Contains(window.IncludedAgentProfileIDs, profile.ID) {
			return window, true
		}
		if _, member := membersBySquad[window.SquadID][profile.ID]; member {
			return window, true
		}
	}
	return activeScheduleWindow{}, false
}

func activeScheduleWindows(selection activeScheduleSelection) []activeScheduleWindow {
	if len(selection.Windows) > 0 {
		return selection.Windows
	}
	if selection.ScheduleID <= 0 && selection.StartAt.IsZero() && selection.EndAt.IsZero() {
		return nil
	}
	return []activeScheduleWindow{{
		ScheduleID:              selection.ScheduleID,
		SquadID:                 selection.SquadID,
		IncludedAgentProfileIDs: selection.IncludedAgentProfileIDs,
		ExcludedAgentProfileIDs: selection.ExcludedAgentProfileIDs,
		StartAt:                 selection.StartAt,
		EndAt:                   selection.EndAt,
	}}
}

func activeScheduleSquadIDs(selections map[int64]activeScheduleSelection) []int64 {
	ret := make([]int64, 0, len(selections))
	for _, selection := range selections {
		for _, window := range activeScheduleWindows(selection) {
			if window.SquadID > 0 {
				ret = append(ret, window.SquadID)
			}
		}
	}
	return uniquePositiveInt64s(ret)
}

func (s *conversationDispatchService) loadDispatchPresenceMapDB(db *gorm.DB, tenantID int64, userIDs []int64, now time.Time) map[int64]dispatchPresenceSnapshot {
	ret := make(map[int64]dispatchPresenceSnapshot, len(userIDs))
	userIDs = uniquePositiveInt64s(userIDs)
	if db == nil || tenantID <= 0 || len(userIDs) == 0 {
		return ret
	}
	sessions := repositories.AgentPresenceSessionRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("user_id", userIDs).
		Where("ended_at IS NULL").
		Gte("last_seen_at", now.Add(-dispatchPresenceFreshness)).
		Desc("id"))
	for _, session := range sessions {
		if _, exists := ret[session.UserID]; exists {
			continue
		}
		ret[session.UserID] = dispatchPresenceSnapshot{Status: session.Status, LastSeenAt: session.LastSeenAt}
	}
	return ret
}

func isDispatchPresenceEligible(snapshot dispatchPresenceSnapshot, now time.Time) bool {
	if snapshot.LastSeenAt.IsZero() || now.Sub(snapshot.LastSeenAt) > dispatchPresenceFreshness {
		return false
	}
	return snapshot.Status == enums.AgentPresenceStatusOnline || snapshot.Status == enums.AgentPresenceStatusIdle
}

func (s *conversationDispatchService) tryAssignWithDecision(conversationID int64, decision dispatchDecision) (*models.Conversation, error) {
	return s.tryAssignWithDecisionContext(conversationID, decision, ruleDispatchExecutionContext{})
}

func (s *conversationDispatchService) tryAssignWithDecisionContext(conversationID int64, decision dispatchDecision, execution ruleDispatchExecutionContext) (*models.Conversation, error) {
	now := time.Now()
	execution = normalizeRuleDispatchExecutionContext(execution)
	operator := execution.operator
	candidate := decision.candidate

	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, conversationID, candidate.profile.TenantID)
		if err != nil {
			return err
		}
		if conversation == nil {
			return errConversationDispatchConflict
		}
		if conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
			return errConversationDispatchConflict
		}
		if conversation.TenantID <= 0 || candidate.profile.TenantID != conversation.TenantID {
			return errConversationDispatchConflict
		}
		if conversation.LastMessageID != decision.expectedLastMessageID {
			return errConversationDispatchConflict
		}
		if execution.expectedTeamID > 0 {
			route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
			teamIDs := s.resolveDispatchTeamIDsDB(ctx.Tx, conversation, route)
			if len(teamIDs) != 1 || teamIDs[0] != execution.expectedTeamID || candidate.profile.TeamID != execution.expectedTeamID {
				return errConversationDispatchTeamMismatch
			}
		}
		coolingDown, err := s.ruleDispatchCandidateCoolingDownDB(ctx.Tx, conversation, candidate.profile.UserID, now)
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

		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversationID, now); err != nil {
			return err
		}
		if err := ConversationAssignmentService.CreateAssignmentWithOptions(ctx, conversationID, conversation.CurrentAssigneeID, profile.UserID, enums.IMAssignmentTypeAssign, decision.reason, operator, now, ConversationAssignmentOptions{
			SquadID:        activeSelection.SquadID,
			DispatchMode:   enums.AgentTeamDispatchModeRule,
			WorkloadWeight: decision.workloadWeight,
		}); err != nil {
			return err
		}

		result := ctx.Tx.Model(&models.Conversation{}).
			Where("id = ? AND tenant_id = ? AND status = ? AND current_assignee_id = ? AND last_message_id = ?", conversationID, conversation.TenantID, enums.IMConversationStatusPending, 0, conversation.LastMessageID).
			Updates(map[string]any{
				"current_assignee_id": profile.UserID,
				"current_team_id":     profile.TeamID,
				"status":              enums.IMConversationStatusActive,
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

		if err := ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeAssign, enums.IMSenderTypeSystem, operator.UserID, "会话已按规则分配", buildDispatchEventPayload(conversation.CurrentAssigneeID, profile.UserID, profile.TeamID, decision, execution.trigger)); err != nil {
			return err
		}
		_, err = ConversationRouteService.enterHQAgentDeskServingWithDB(ctx.Tx, conversationID, "网页端总部客服自动分配:"+strings.TrimSpace(decision.reason), now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return ConversationService.Get(conversationID), nil
}

func (s *conversationDispatchService) validateDispatchDecisionCandidateDB(db *gorm.DB, conversation *models.Conversation, decision dispatchDecision, now time.Time) (*models.AgentProfile, activeScheduleSelection, error) {
	candidate := decision.candidate
	if db == nil || conversation == nil || conversation.TenantID <= 0 {
		return nil, activeScheduleSelection{}, errConversationDispatchConflict
	}
	profile, err := repositories.AgentProfileRepository.GetForUpdateInTenant(db, candidate.profile.ID, conversation.TenantID)
	if err != nil {
		return nil, activeScheduleSelection{}, err
	}
	if profile == nil || profile.UserID != candidate.profile.UserID || profile.Status != enums.StatusOk || !profile.AutoAssignEnabled || profile.MaxConcurrentCount <= 0 {
		return nil, activeScheduleSelection{}, errConversationDispatchConflict
	}
	user := repositories.UserRepository.GetInTenant(db, profile.UserID, conversation.TenantID)
	if user == nil || user.Status != enums.StatusOk || user.DeletedAt != nil {
		return nil, activeScheduleSelection{}, errConversationDispatchConflict
	}
	permittedUserIDs, err := repositories.PermissionRepository.FindUserIDsWithAllCodes(db, []int64{profile.UserID}, []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
	})
	if err != nil {
		return nil, activeScheduleSelection{}, err
	}
	if len(permittedUserIDs) != 1 || permittedUserIDs[0] != profile.UserID {
		return nil, activeScheduleSelection{}, errConversationDispatchConflict
	}
	presence := s.loadDispatchPresenceMapDB(db, conversation.TenantID, []int64{profile.UserID}, now)[profile.UserID]
	if !isDispatchPresenceEligible(presence, now) {
		return nil, activeScheduleSelection{}, errConversationDispatchConflict
	}
	team, err := repositories.AgentTeamRepository.GetForUpdateInTenant(db, profile.TeamID, conversation.TenantID)
	if err != nil {
		return nil, activeScheduleSelection{}, err
	}
	if team == nil || team.Status != enums.StatusOk || normalizedDispatchMode(team.DispatchMode) != enums.AgentTeamDispatchModeRule {
		return nil, activeScheduleSelection{}, errConversationDispatchConflict
	}
	activeScheduleDetails := s.findActiveScheduleDetailsDB(db, []int64{profile.TeamID}, conversation.TenantID, now)
	activeSelection, scheduled := activeScheduleDetails[profile.TeamID]
	matchedWindow, matched := matchingActiveScheduleDB(db, profile, activeSelection)
	if !scheduled || !matched || matchedWindow.SquadID != candidate.squadID {
		return nil, activeScheduleSelection{}, errConversationDispatchConflict
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)
	if route != nil && !teamCanServeRoute(team, route) {
		return nil, activeScheduleSelection{}, errConversationDispatchConflict
	}
	activeCounts, err := s.findActiveConversationCountMapDB(db, []int64{profile.UserID}, conversation.TenantID)
	if err != nil {
		return nil, activeScheduleSelection{}, err
	}
	if activeCounts[profile.UserID] >= profile.MaxConcurrentCount {
		return nil, activeScheduleSelection{}, errConversationDispatchConflict
	}
	currentLoads, err := s.buildDispatchLoadMapDB(db, []models.AgentProfile{*profile}, activeScheduleDetails, conversation.TenantID)
	if err != nil {
		return nil, activeScheduleSelection{}, err
	}
	currentLoad := currentLoads[profile.UserID]
	if currentLoad.activeCount != candidate.activeCount ||
		currentLoad.weightedOpenLoad != candidate.weightedOpenLoad ||
		currentLoad.pendingFirstReply != candidate.pendingFirstReply ||
		currentLoad.pendingReplyCount != candidate.pendingReplyCount ||
		currentLoad.shiftWorkloadWeight != candidate.shiftWorkloadWeight ||
		!currentLoad.lastAssignedAt.Equal(candidate.lastAssignedAt) {
		return nil, activeScheduleSelection{}, errConversationDispatchConflict
	}
	return profile, activeScheduleSelectionFromWindow(matchedWindow), nil
}

func profileMatchesActiveScheduleDB(db *gorm.DB, profile *models.AgentProfile, selection activeScheduleSelection) bool {
	_, matched := matchingActiveScheduleDB(db, profile, selection)
	return matched
}

func matchingActiveScheduleDB(db *gorm.DB, profile *models.AgentProfile, selection activeScheduleSelection) (activeScheduleWindow, bool) {
	if db == nil || profile == nil || profile.TenantID <= 0 || profile.TeamID <= 0 {
		return activeScheduleWindow{}, false
	}
	for _, window := range activeScheduleWindows(selection) {
		if slices.Contains(window.ExcludedAgentProfileIDs, profile.ID) {
			continue
		}
		if window.SquadID <= 0 {
			return window, true
		}
		squad := repositories.AgentTeamSquadRepository.GetInTenant(db, window.SquadID, profile.TenantID)
		if squad == nil || squad.Status != enums.StatusOk || squad.TeamID != profile.TeamID {
			continue
		}
		if slices.Contains(window.IncludedAgentProfileIDs, profile.ID) {
			return window, true
		}
		if repositories.AgentTeamSquadMemberRepository.Take(db,
			"tenant_id = ? AND squad_id = ? AND agent_profile_id = ? AND status = ?",
			profile.TenantID, window.SquadID, profile.ID, enums.StatusOk,
		) != nil {
			return window, true
		}
	}
	return activeScheduleWindow{}, false
}

func activeScheduleSelectionFromWindow(window activeScheduleWindow) activeScheduleSelection {
	return activeScheduleSelection{
		ScheduleID:              window.ScheduleID,
		SquadID:                 window.SquadID,
		IncludedAgentProfileIDs: append([]int64(nil), window.IncludedAgentProfileIDs...),
		ExcludedAgentProfileIDs: append([]int64(nil), window.ExcludedAgentProfileIDs...),
		StartAt:                 window.StartAt,
		EndAt:                   window.EndAt,
		Windows:                 []activeScheduleWindow{window},
	}
}

func buildDispatchEventPayload(fromAssigneeID, toAssigneeID, toTeamID int64, decision dispatchDecision, trigger string) string {
	return ConversationService.buildEventPayload(map[string]any{
		"fromStatus":     enums.IMConversationStatusPending,
		"toStatus":       enums.IMConversationStatusActive,
		"fromAssigneeId": fromAssigneeID,
		"toAssigneeId":   toAssigneeID,
		"toTeamId":       toTeamID,
		"reason":         strings.TrimSpace(decision.reason),
		"dispatchMode":   enums.AgentTeamDispatchModeRule,
		"workloadWeight": decision.workloadWeight,
		"priority":       decision.priority,
		"trigger":        strings.TrimSpace(trigger),
	})
}

func systemDispatchPrincipal() *dto.AuthPrincipal {
	return &dto.AuthPrincipal{
		UserID:   0,
		Username: "system",
		Nickname: "system",
	}
}
