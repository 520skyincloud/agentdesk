package services

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ConversationDispatchService = newConversationDispatchService()

func newConversationDispatchService() *conversationDispatchService {
	return &conversationDispatchService{
		dispatchTimers: make(map[int64]*time.Timer),
		llmChat:        defaultDispatchLLMChat,
		resolveModel:   defaultDispatchModelResolver,
	}
}

type conversationDispatchService struct {
	dispatchMu                sync.Mutex
	dispatchTimers            map[int64]*time.Timer
	dispatching               sync.Map
	realtimeSchedulingEnabled atomic.Bool
	llmChat                   dispatchLLMChatFunc
	resolveModel              dispatchModelResolverFunc
}

type dispatchCandidate struct {
	profile             models.AgentProfile
	squadID             int64
	dispatchMode        enums.AgentTeamDispatchMode
	activeCount         int
	weightedOpenLoad    int
	pendingFirstReply   int
	pendingReplyCount   int
	shiftAssignedWeight int
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
	CandidateCount      int
	Reason              string
}

var errConversationDispatchConflict = errors.New("conversation dispatch conflict")

const pendingDispatchBatchLimit = 50

const dispatchDebounceDelay = 800 * time.Millisecond

var pendingDispatchRunning atomic.Bool

type activeScheduleSelection struct {
	SquadID int64
	StartAt time.Time
	EndAt   time.Time
}

func (s *conversationDispatchService) ScheduleDispatch(conversationID int64) {
	if conversationID <= 0 || !s.realtimeSchedulingEnabled.Load() {
		return
	}
	db := sqls.DB()
	if db == nil {
		return
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if timer := s.dispatchTimers[conversationID]; timer != nil {
		timer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(dispatchDebounceDelay, func() {
		s.dispatchMu.Lock()
		if s.dispatchTimers[conversationID] != timer {
			s.dispatchMu.Unlock()
			return
		}
		delete(s.dispatchTimers, conversationID)
		s.dispatchMu.Unlock()
		if sqls.DB() != db || !dispatchDatabaseReady(db) {
			return
		}
		if _, err := s.DispatchConversation(conversationID); err != nil {
			slog.Warn("scheduled conversation dispatch failed", "conversation_id", conversationID, "error", err)
		}
	})
	s.dispatchTimers[conversationID] = timer
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
	aiAgent := AIAgentService.GetByTenantID(conversation.AIAgentID, conversation.TenantID)
	if aiAgent == nil || aiAgent.Status != enums.StatusOk {
		return nil, nil
	}
	return s.DispatchPendingConversation(conversation, aiAgent)
}

func (s *conversationDispatchService) DispatchPendingConversation(conversation *models.Conversation, aiAgent *models.AIAgent) (*models.Conversation, error) {
	if conversation == nil || aiAgent == nil {
		return nil, nil
	}
	if conversation.TenantID <= 0 || conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
		return nil, nil
	}
	if aiAgent.TenantID != conversation.TenantID {
		return nil, nil
	}
	decisionStartedAt := time.Now()
	if _, loaded := s.dispatching.LoadOrStore(conversation.ID, struct{}{}); loaded {
		s.ScheduleDispatch(conversation.ID)
		return nil, nil
	}
	defer s.dispatching.Delete(conversation.ID)

	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	teamIDs := s.resolveDispatchTeamIDs(conversation, aiAgent, route)
	if len(teamIDs) == 0 {
		slog.Debug("skip auto dispatch due to unresolved team",
			"conversation_id", conversation.ID,
			"ai_agent_id", aiAgent.ID,
		)
		s.recordAutomaticDispatchEvidence(conversation, nil, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "未找到可承接该会话的客服组", "unresolved_team", decisionStartedAt)
		return nil, nil
	}
	teamIDs = s.filterAutomaticTeamIDs(teamIDs, conversation.TenantID)
	if len(teamIDs) == 0 {
		slog.Debug("skip automatic dispatch for manual teams", "conversation_id", conversation.ID)
		return nil, nil
	}

	candidates, report, err := s.pickDispatchCandidates(teamIDs, conversation.TenantID, route, time.Now())
	if err != nil {
		s.recordAutomaticDispatchEvidence(conversation, teamIDs, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "自动派单候选计算失败", err.Error(), decisionStartedAt)
		return nil, err
	}
	if len(candidates) == 0 {
		slog.Debug("no dispatch candidate available",
			"conversation_id", conversation.ID,
			"ai_agent_id", aiAgent.ID,
			"requested_team_ids", report.RequestedTeamIDs,
			"active_schedule_team_ids", report.ActiveScheduleTeams,
			"matched_profiles", report.MatchedProfiles,
			"eligible_profiles", report.EligibleProfiles,
			"reason", report.Reason,
		)
		s.recordAutomaticDispatchEvidence(conversation, teamIDs, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "当前值班范围内没有可接待客服", report.Reason, decisionStartedAt)
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
			candidateDecision.mode = enums.AgentTeamDispatchModeRule
			candidateDecision.confidence = 0
			candidateDecision.reason = "智能候选失效，按公平队列顺延"
		}
		dispatched, err := s.tryAssignWithDecision(conversation.ID, candidateDecision)
		if err != nil {
			if errors.Is(err, errConversationDispatchConflict) {
				s.recordAutomaticDispatchEvidence(conversation, teamIDs, candidates, &candidate, 0, string(candidateDecision.mode), enums.DispatchDecisionStatusStale, "派单候选或会话状态已变化", err.Error(), decisionStartedAt)
				if latest := ConversationService.Get(conversation.ID); latest != nil && latest.Status == enums.IMConversationStatusPending && latest.CurrentAssigneeID == 0 {
					s.ScheduleDispatch(conversation.ID)
				}
				return nil, nil
			}
			s.recordAutomaticDispatchEvidence(conversation, teamIDs, candidates, &candidate, 0, string(candidateDecision.mode), enums.DispatchDecisionStatusFailed, "自动派单执行失败", err.Error(), decisionStartedAt)
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
			decisionStatus := enums.DispatchDecisionStatusSelected
			fallbackReason := ""
			if strings.Contains(candidateDecision.reason, "降级") || strings.Contains(candidateDecision.reason, "候选失效") {
				decisionStatus = enums.DispatchDecisionStatusFallback
				fallbackReason = candidateDecision.reason
			}
			s.recordAutomaticDispatchEvidence(conversation, teamIDs, candidates, &candidate, assignmentID, string(candidateDecision.mode), decisionStatus, candidateDecision.reason, fallbackReason, decisionStartedAt)
			slog.Info("conversation auto dispatched",
				"conversation_id", dispatched.ID,
				"ai_agent_id", aiAgent.ID,
				"assignee_id", dispatched.CurrentAssigneeID,
				"team_id", dispatched.CurrentTeamID,
				"candidate_count", report.CandidateCount,
				"requested_team_ids", report.RequestedTeamIDs,
				"dispatch_mode", candidateDecision.mode,
				"workload_weight", candidateDecision.workloadWeight,
			)
			WsService.PublishConversationChanged(dispatched, enums.IMRealtimeEventConversationAssigned)
			eventbus.PublishAsync(context.Background(), events.ConversationAssignedEvent{
				ConversationID: dispatched.ID,
				ToUserID:       dispatched.CurrentAssigneeID,
				OperatorID:     systemDispatchPrincipal().UserID,
				Reason:         candidateDecision.reason,
				AssignType:     events.ConversationAssignTypeAutoAssign,
			})
			return dispatched, nil
		}
	}
	slog.Debug("auto dispatch candidate list exhausted without assignment",
		"conversation_id", conversation.ID,
		"ai_agent_id", aiAgent.ID,
		"candidate_count", report.CandidateCount,
	)
	s.recordAutomaticDispatchEvidence(conversation, teamIDs, candidates, nil, 0, string(decision.mode), enums.DispatchDecisionStatusFailed, "自动派单候选已全部失效", "candidate_exhausted", decisionStartedAt)
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
			ShiftAssignedWeight: candidate.shiftAssignedWeight, NormalizedLoad: candidate.normalizedLoad,
		})
	}
	evidence := DispatchDecisionEvidence{
		ConversationID: conversation.ID, Trigger: "auto_dispatch", DecisionMode: mode, Status: status,
		Candidates: evidenceCandidates, InputLastMessageID: conversation.LastMessageID,
		DecisionLatencyMillis: time.Since(startedAt).Milliseconds(), Reason: reason, FallbackReason: fallbackReason,
		OperatorID: systemDispatchPrincipal().UserID, DecidedAt: time.Now(), AssignmentID: assignmentID,
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

	if limit <= 0 {
		limit = pendingDispatchBatchLimit
	}
	conversations := ConversationService.Find(sqls.NewCnd().
		Where("tenant_id > ?", 0).
		Eq("status", enums.IMConversationStatusPending).
		Eq("current_assignee_id", 0).
		Desc("priority").
		Asc("handoff_at").
		Asc("id"))
	if len(conversations) == 0 {
		return 0, nil
	}

	dispatchedCount := 0
	scannedCount := 0
	conversations = s.prioritizePendingConversations(conversations, time.Now())
	for i, conversation := range fairPendingConversationQueue(conversations) {
		if i >= limit {
			break
		}
		scannedCount++
		dispatched, err := s.DispatchConversation(conversation.ID)
		if err != nil {
			return dispatchedCount, err
		}
		if dispatched != nil {
			dispatchedCount++
		}
	}
	if scannedCount > 0 {
		slog.Info("pending conversation dispatch scan completed",
			"scanned_count", scannedCount,
			"dispatched_count", dispatchedCount,
			"limit", limit,
		)
	}
	return dispatchedCount, nil
}

func (s *conversationDispatchService) RunPendingDispatchLoop(interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		slog.Info("pending conversation dispatch loop started",
			"interval_seconds", int(interval/time.Second),
		)

		for {
			if _, err := s.DispatchPendingConversations(0); err != nil {
				slog.Warn("dispatch pending conversations loop failed", "error", err)
			}
			<-ticker.C
		}
	}()
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
		Eq("service_status", enums.ServiceStatusIdle))
	activeSchedules := make(map[int64]int64, len(activeScheduleDetails))
	for teamID, selection := range activeScheduleDetails {
		activeSchedules[teamID] = selection.SquadID
	}
	profiles = s.filterProfilesByActiveSquads(profiles, activeSchedules, tenantID)
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
	if route != nil {
		scopedProfiles := make([]models.AgentProfile, 0, len(enabledProfiles))
		scopedUserIDs := make([]int64, 0, len(enabledUserIDs))
		for _, profile := range enabledProfiles {
			if AgentProfileService.ProfileCanServeRoute(&profile, route) {
				scopedProfiles = append(scopedProfiles, profile)
				scopedUserIDs = append(scopedUserIDs, profile.UserID)
			}
		}
		enabledProfiles = scopedProfiles
		enabledUserIDs = scopedUserIDs
		if len(enabledProfiles) == 0 {
			report.Reason = "no_profile_in_store_scope"
			return nil, report, nil
		}
	}

	loads, err := s.buildDispatchLoadMap(enabledProfiles, activeScheduleDetails, tenantID)
	if err != nil {
		return nil, report, err
	}
	teamModes := make(map[int64]enums.AgentTeamDispatchMode, len(activeTeamIDs))
	for _, team := range AgentTeamService.Find(sqls.NewCnd().Eq("tenant_id", tenantID).In("id", activeTeamIDs).Eq("status", enums.StatusOk)) {
		teamModes[team.ID] = normalizedDispatchMode(team.DispatchMode)
	}

	candidates := make([]dispatchCandidate, 0, len(enabledProfiles))
	for _, profile := range enabledProfiles {
		load := loads[profile.UserID]
		if profile.MaxConcurrentCount > 0 && load.activeCount >= profile.MaxConcurrentCount {
			continue
		}
		if !profile.ReceiveOfflineMessage && profile.LastOnlineAt != nil && now.Sub(*profile.LastOnlineAt) > 15*time.Minute {
			continue
		}
		capacity := math.Max(float64(profile.MaxConcurrentCount), 1)
		normalizedLoad := (float64(load.weightedOpenLoad)+float64(load.pendingFirstReply)*0.75+float64(load.pendingReplyCount)*0.5)/capacity + float64(load.shiftAssignedWeight)*0.03
		candidates = append(candidates, dispatchCandidate{
			profile:             profile,
			squadID:             activeSchedules[profile.TeamID],
			dispatchMode:        teamModes[profile.TeamID],
			activeCount:         load.activeCount,
			weightedOpenLoad:    load.weightedOpenLoad,
			pendingFirstReply:   load.pendingFirstReply,
			pendingReplyCount:   load.pendingReplyCount,
			shiftAssignedWeight: load.shiftAssignedWeight,
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
		switch {
		case a.shiftAssignedWeight < b.shiftAssignedWeight:
			return -1
		case a.shiftAssignedWeight > b.shiftAssignedWeight:
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
		aLastStatusAt := zeroTime(a.profile.LastStatusAt)
		bLastStatusAt := zeroTime(b.profile.LastStatusAt)
		switch {
		case aLastStatusAt.Before(bLastStatusAt):
			return -1
		case aLastStatusAt.After(bLastStatusAt):
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
		Gt("end_at", now))

	activeSet := make(map[int64]activeScheduleSelection, len(schedules))
	for _, schedule := range schedules {
		if _, exists := activeSet[schedule.TeamID]; !exists {
			activeSet[schedule.TeamID] = activeScheduleSelection{SquadID: schedule.SquadID, StartAt: schedule.StartAt, EndAt: schedule.EndAt}
		}
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

func (s *conversationDispatchService) filterProfilesByActiveSquads(profiles []models.AgentProfile, activeSchedules map[int64]int64, tenantID int64) []models.AgentProfile {
	squadIDs := make([]int64, 0, len(activeSchedules))
	for _, squadID := range activeSchedules {
		if squadID > 0 {
			squadIDs = append(squadIDs, squadID)
		}
	}
	membersBySquad, teamBySquad := AgentTeamSquadService.ActiveMemberProfileSet(squadIDs, tenantID)
	ret := make([]models.AgentProfile, 0, len(profiles))
	for i := range profiles {
		squadID, scheduled := activeSchedules[profiles[i].TeamID]
		if !scheduled {
			continue
		}
		if squadID > 0 {
			if teamBySquad[squadID] != profiles[i].TeamID {
				continue
			}
			if _, member := membersBySquad[squadID][profiles[i].ID]; !member {
				continue
			}
		}
		ret = append(ret, profiles[i])
	}
	return ret
}

func (s *conversationDispatchService) findActiveConversationCountMap(userIDs []int64, tenantID int64) (map[int64]int, error) {
	return s.findActiveConversationCountMapDB(sqls.DB(), userIDs, tenantID)
}

func (s *conversationDispatchService) tryAssignConversation(conversationID int64, candidate dispatchCandidate, reason string) (*models.Conversation, error) {
	conversation := ConversationService.Get(conversationID)
	expectedLastMessageID := int64(0)
	if conversation != nil {
		expectedLastMessageID = conversation.LastMessageID
	}
	return s.tryAssignWithDecision(conversationID, dispatchDecision{
		candidate:             candidate,
		mode:                  enums.AgentTeamDispatchModeRule,
		reason:                reason,
		workloadWeight:        normalizedWorkloadWeight(conversation),
		priority:              normalizedConversationPriority(conversation),
		expectedLastMessageID: expectedLastMessageID,
	})
}

func (s *conversationDispatchService) tryAssignWithDecision(conversationID int64, decision dispatchDecision) (*models.Conversation, error) {
	now := time.Now()
	operator := systemDispatchPrincipal()
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
		profile, err := repositories.AgentProfileRepository.GetForUpdateInTenant(ctx.Tx, candidate.profile.ID, conversation.TenantID)
		if err != nil {
			return err
		}
		if profile == nil || profile.UserID != candidate.profile.UserID || profile.Status != enums.StatusOk || !profile.AutoAssignEnabled || profile.ServiceStatus != enums.ServiceStatusIdle {
			return errConversationDispatchConflict
		}
		user := repositories.UserRepository.GetInTenant(ctx.Tx, profile.UserID, conversation.TenantID)
		if user == nil || user.Status != enums.StatusOk || user.DeletedAt != nil {
			return errConversationDispatchConflict
		}
		team, err := repositories.AgentTeamRepository.GetForUpdateInTenant(ctx.Tx, profile.TeamID, conversation.TenantID)
		if err != nil {
			return err
		}
		if team == nil || team.Status != enums.StatusOk {
			return errConversationDispatchConflict
		}
		teamMode := normalizedDispatchMode(team.DispatchMode)
		if teamMode == enums.AgentTeamDispatchModeManual || (decision.mode == enums.AgentTeamDispatchModeIntelligent && teamMode != enums.AgentTeamDispatchModeIntelligent) {
			return errConversationDispatchConflict
		}
		activeScheduleDetails := s.findActiveScheduleDetailsDB(ctx.Tx, []int64{profile.TeamID}, conversation.TenantID, now)
		activeSelection, scheduled := activeScheduleDetails[profile.TeamID]
		if !scheduled || activeSelection.SquadID != candidate.squadID || !profileMatchesActiveScheduleDB(ctx.Tx, profile, activeSelection) {
			return errConversationDispatchConflict
		}
		route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if route != nil && !teamCanServeRoute(team, route) {
			return errConversationDispatchConflict
		}
		activeCounts, err := s.findActiveConversationCountMapDB(ctx.Tx, []int64{profile.UserID}, conversation.TenantID)
		if err != nil {
			return err
		}
		if profile.MaxConcurrentCount > 0 && activeCounts[profile.UserID] >= profile.MaxConcurrentCount {
			return errConversationDispatchConflict
		}
		currentLoads, err := s.buildDispatchLoadMapDB(ctx.Tx, []models.AgentProfile{*profile}, activeScheduleDetails, conversation.TenantID)
		if err != nil {
			return err
		}
		currentLoad := currentLoads[profile.UserID]
		if currentLoad.activeCount != candidate.activeCount ||
			currentLoad.weightedOpenLoad != candidate.weightedOpenLoad ||
			currentLoad.pendingFirstReply != candidate.pendingFirstReply ||
			currentLoad.pendingReplyCount != candidate.pendingReplyCount ||
			currentLoad.shiftAssignedWeight != candidate.shiftAssignedWeight ||
			!currentLoad.lastAssignedAt.Equal(candidate.lastAssignedAt) {
			return errConversationDispatchConflict
		}

		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, conversationID, now); err != nil {
			return err
		}
		if err := ConversationAssignmentService.CreateAssignmentWithOptions(ctx, conversationID, conversation.CurrentAssigneeID, profile.UserID, enums.IMAssignmentTypeAssign, decision.reason, operator, now, ConversationAssignmentOptions{
			SquadID:            activeSelection.SquadID,
			DispatchMode:       decision.mode,
			DecisionConfidence: decision.confidence,
			WorkloadWeight:     decision.workloadWeight,
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

		if err := ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeAssign, enums.IMSenderTypeSystem, operator.UserID, "会话已自动分配", buildDispatchEventPayload(conversation.CurrentAssigneeID, profile.UserID, profile.TeamID, decision)); err != nil {
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

func profileMatchesActiveScheduleDB(db *gorm.DB, profile *models.AgentProfile, selection activeScheduleSelection) bool {
	if db == nil || profile == nil || profile.TenantID <= 0 || profile.TeamID <= 0 {
		return false
	}
	if selection.SquadID <= 0 {
		return true
	}
	squad := repositories.AgentTeamSquadRepository.GetInTenant(db, selection.SquadID, profile.TenantID)
	if squad == nil || squad.Status != enums.StatusOk || squad.TeamID != profile.TeamID {
		return false
	}
	return repositories.AgentTeamSquadMemberRepository.Take(db,
		"tenant_id = ? AND squad_id = ? AND agent_profile_id = ? AND status = ?",
		profile.TenantID, selection.SquadID, profile.ID, enums.StatusOk,
	) != nil
}

func buildDispatchEventPayload(fromAssigneeID, toAssigneeID, toTeamID int64, decision dispatchDecision) string {
	return ConversationService.buildEventPayload(map[string]any{
		"fromStatus":     enums.IMConversationStatusPending,
		"toStatus":       enums.IMConversationStatusActive,
		"fromAssigneeId": fromAssigneeID,
		"toAssigneeId":   toAssigneeID,
		"toTeamId":       toTeamID,
		"reason":         strings.TrimSpace(decision.reason),
		"dispatchMode":   decision.mode,
		"confidence":     decision.confidence,
		"workloadWeight": decision.workloadWeight,
		"priority":       decision.priority,
	})
}

func systemDispatchPrincipal() *dto.AuthPrincipal {
	return &dto.AuthPrincipal{
		UserID:   0,
		Username: "system",
		Nickname: "system",
	}
}

func zeroTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
