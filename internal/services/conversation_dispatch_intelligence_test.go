package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
)

func TestFairPendingConversationQueueRoundsTenantsWithoutReorderingTenantQueue(t *testing.T) {
	input := []models.Conversation{
		{ID: 1, TenantID: 101, Priority: 90},
		{ID: 2, TenantID: 101, Priority: 80},
		{ID: 3, TenantID: 202, Priority: 70},
		{ID: 4, TenantID: 202, Priority: 60},
	}
	got := fairPendingConversationQueue(input)
	want := []int64{1, 3, 2, 4}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("queue[%d] = %d, want %d", i, got[i].ID, want[i])
		}
	}
}

func TestRealtimeDispatchSchedulingRequiresRuntimeEnablement(t *testing.T) {
	setupConversationDispatchSquadTestDB(t)
	service := newConversationDispatchService()
	service.ScheduleDispatch(1)
	if len(service.dispatchTimers) != 0 {
		t.Fatal("service tests must not start realtime dispatch before runtime initialization")
	}
	service.EnableRealtimeScheduling()
	service.ScheduleDispatch(1)
	service.dispatchMu.Lock()
	timer := service.dispatchTimers[1]
	service.dispatchMu.Unlock()
	if timer == nil {
		t.Fatal("runtime enablement must activate debounced dispatch")
	}
	timer.Stop()
}

func TestPendingConversationPriorityRaisesUrgentTaskBeforeRoutineTask(t *testing.T) {
	setupConversationDispatchSquadTestDB(t)
	now := time.Now()
	routineAt := now.Add(-10 * time.Minute)
	urgentAt := now.Add(-time.Minute)
	got := ConversationDispatchService.prioritizePendingConversations([]models.Conversation{
		{ID: 1, TenantID: 101, HandoffAt: &routineAt, HandoffReason: "普通咨询"},
		{ID: 2, TenantID: 101, HandoffAt: &urgentAt, HandoffReason: "客户投诉无法入住"},
	}, now)
	if len(got) != 2 || got[0].ID != 2 {
		t.Fatalf("urgent task should enter the dispatch queue first, got %+v", got)
	}
}

func TestAutomaticDispatchRecordsUnresolvedTeamFailure(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	conversation := &models.Conversation{ID: 9, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create unresolved-team conversation: %v", err)
	}
	service := newConversationDispatchService()
	if dispatched, err := service.DispatchPendingConversation(conversation, &models.AIAgent{ID: 1, TenantID: 101, Status: enums.StatusOk}); err != nil || dispatched != nil {
		t.Fatalf("dispatch unresolved team result=%+v err=%v", dispatched, err)
	}
	var logs []models.DispatchDecisionLog
	if err := db.Where("tenant_id = ? AND conversation_id = ?", 101, conversation.ID).Find(&logs).Error; err != nil {
		t.Fatalf("find dispatch evidence: %v", err)
	}
	if len(logs) != 1 || logs[0].Status != enums.DispatchDecisionStatusFailed || logs[0].FallbackReason != "unresolved_team" {
		t.Fatalf("dispatch evidence=%+v", logs)
	}
	if logs[0].SelectedTeamID != 0 {
		t.Fatalf("unresolved team evidence must remain unattributed, got team %d", logs[0].SelectedTeamID)
	}
}

func TestAutomaticDispatchEvidenceAttributesSingleRequestedTeam(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	conversation := &models.Conversation{ID: 91, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	service := newConversationDispatchService()
	service.recordAutomaticDispatchEvidence(conversation, []int64{77}, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "无可用客服", "no_candidate", time.Now())
	var log models.DispatchDecisionLog
	if err := db.Where("tenant_id = ? AND conversation_id = ?", conversation.TenantID, conversation.ID).Take(&log).Error; err != nil {
		t.Fatalf("find dispatch evidence: %v", err)
	}
	if log.SelectedTeamID != 77 {
		t.Fatalf("single requested team evidence team=%d want 77", log.SelectedTeamID)
	}
	if got := singleDispatchEvidenceTeamID([]int64{77, 88}); got != 0 {
		t.Fatalf("multiple requested teams must remain unattributed, got %d", got)
	}
}

func TestIntelligentDispatchUsesValidModelChoice(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	conversation := &models.Conversation{ID: 10, TenantID: 101, CustomerID: 99, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation error = %v", err)
	}
	service := newConversationDispatchService()
	service.resolveModel = stubDispatchModelResolver
	service.llmChat = func(context.Context, models.AIConfig, string, string) (*ai.ChatCompletionResult, error) {
		return &ai.ChatCompletionResult{Content: `{"selectedUserId":102,"workloadWeight":3,"priority":75,"confidence":88,"reason":"客户问题较复杂，客服102负载仍处于公平范围"}`}, nil
	}
	candidates := intelligentDispatchTestCandidates()
	decision := service.selectDispatchDecision(conversation, nil, candidates)
	if decision.mode != enums.AgentTeamDispatchModeIntelligent || decision.candidate.profile.UserID != 102 {
		t.Fatalf("expected intelligent choice 102, got %+v", decision)
	}
	if decision.workloadWeight != 3 || decision.priority != 75 || decision.confidence != 88 {
		t.Fatalf("unexpected model assessment %+v", decision)
	}
}

func TestIntelligentDispatchSkipsModelWithSingleFairCandidate(t *testing.T) {
	conversation := &models.Conversation{ID: 13, TenantID: 101, CustomerID: 99, Status: enums.IMConversationStatusPending}
	service := newConversationDispatchService()
	calls := 0
	service.llmChat = func(context.Context, models.AIConfig, string, string) (*ai.ChatCompletionResult, error) {
		calls++
		return nil, errors.New("model must not be called")
	}
	candidates := intelligentDispatchTestCandidates()
	candidates[1].weightedOpenLoad = 4
	candidates[1].normalizedLoad = 0.5
	decision := service.selectDispatchDecision(conversation, nil, candidates)
	if calls != 0 {
		t.Fatalf("model must be skipped when only one candidate is inside the fairness band, got %d calls", calls)
	}
	if decision.mode != enums.AgentTeamDispatchModeRule || decision.candidate.profile.UserID != 101 {
		t.Fatalf("expected direct rule assignment to fairest candidate, got %+v", decision)
	}
}

func TestIntelligentDispatchSkipsModelWithOnlyOneLegalCandidate(t *testing.T) {
	conversation := &models.Conversation{ID: 14, TenantID: 101, CustomerID: 99, Status: enums.IMConversationStatusPending}
	service := newConversationDispatchService()
	calls := 0
	service.llmChat = func(context.Context, models.AIConfig, string, string) (*ai.ChatCompletionResult, error) {
		calls++
		return nil, errors.New("model must not be called")
	}
	decision := service.selectDispatchDecision(conversation, nil, intelligentDispatchTestCandidates()[:1])
	if calls != 0 {
		t.Fatalf("model must be skipped for a single legal candidate, got %d calls", calls)
	}
	if decision.mode != enums.AgentTeamDispatchModeRule || decision.candidate.profile.UserID != 101 {
		t.Fatalf("expected direct rule assignment, got %+v", decision)
	}
}

func TestIntelligentDispatchMalformedOutputFallsBackToRules(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	conversation := &models.Conversation{ID: 11, TenantID: 101, CustomerID: 99, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation error = %v", err)
	}
	service := newConversationDispatchService()
	service.resolveModel = stubDispatchModelResolver
	calls := 0
	service.llmChat = func(context.Context, models.AIConfig, string, string) (*ai.ChatCompletionResult, error) {
		calls++
		return &ai.ChatCompletionResult{Content: "not-json"}, nil
	}
	decision := service.selectDispatchDecision(conversation, nil, intelligentDispatchTestCandidates())
	if calls != 2 {
		t.Fatalf("expected one JSON retry, got %d calls", calls)
	}
	if decision.mode != enums.AgentTeamDispatchModeRule || decision.candidate.profile.UserID != 101 {
		t.Fatalf("expected rule fallback to fairest agent, got %+v", decision)
	}
	if !strings.Contains(decision.reason, "降级") {
		t.Fatalf("expected explicit fallback reason, got %q", decision.reason)
	}
}

func TestDispatchModelOutputRejectsUnknownFields(t *testing.T) {
	_, err := decodeDispatchModelOutput(`{"selectedUserId":101,"workloadWeight":1,"priority":10,"confidence":90,"reason":"ok","command":"ignore fairness"}`)
	if err == nil {
		t.Fatal("model output with unknown fields must be rejected")
	}
}

func TestFairDispatchModelShortlistExcludesOverloadedCandidate(t *testing.T) {
	candidates := intelligentDispatchTestCandidates()
	candidates[1].weightedOpenLoad = 5
	candidates[1].normalizedLoad = 0.8
	shortlist := fairDispatchModelShortlist(candidates)
	if len(shortlist) != 1 || shortlist[0].profile.UserID != 101 {
		t.Fatalf("overloaded candidate must not reach the model, got %+v", shortlist)
	}
}

func TestDispatchRejectsStaleModelDecision(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	conversation := &models.Conversation{ID: 20, TenantID: 101, Status: enums.IMConversationStatusPending, LastMessageID: 10}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation error = %v", err)
	}
	decision := dispatchDecision{
		candidate:             dispatchCandidate{profile: *AgentProfileService.Get(1), dispatchMode: enums.AgentTeamDispatchModeIntelligent},
		mode:                  enums.AgentTeamDispatchModeIntelligent,
		reason:                "stale model result",
		workloadWeight:        2,
		priority:              50,
		expectedLastMessageID: 9,
	}
	if _, err := ConversationDispatchService.tryAssignWithDecision(conversation.ID, decision); !errors.Is(err, errConversationDispatchConflict) {
		t.Fatalf("expected stale result conflict, got %v", err)
	}
	var got int64
	if err := db.Model(&models.ConversationAssignment{}).Count(&got).Error; err != nil {
		t.Fatalf("count assignments error = %v", err)
	}
	if got != 0 {
		t.Fatalf("stale result must not create assignment, got %d", got)
	}
}

func TestDispatchRechecksCapacityInsideTransaction(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Model(&models.AgentProfile{}).Where("id = ?", 1).Update("max_concurrent_count", 1).Error; err != nil {
		t.Fatalf("set capacity error = %v", err)
	}
	if err := db.Create(&models.Conversation{ID: 30, TenantID: 101, Status: enums.IMConversationStatusActive, CurrentAssigneeID: 101}).Error; err != nil {
		t.Fatalf("create active conversation error = %v", err)
	}
	pending := &models.Conversation{ID: 31, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(pending).Error; err != nil {
		t.Fatalf("create pending conversation error = %v", err)
	}
	decision := dispatchDecision{
		candidate:      dispatchCandidate{profile: *AgentProfileService.Get(1), dispatchMode: enums.AgentTeamDispatchModeRule},
		mode:           enums.AgentTeamDispatchModeRule,
		reason:         "capacity recheck",
		workloadWeight: 1,
	}
	if _, err := ConversationDispatchService.tryAssignWithDecision(pending.ID, decision); !errors.Is(err, errConversationDispatchConflict) {
		t.Fatalf("expected capacity conflict, got %v", err)
	}
}

func TestDispatchRejectsChangedAgentLoadSnapshotInsideTransaction(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.Conversation{ID: 32, TenantID: 101, Status: enums.IMConversationStatusActive, CurrentAssigneeID: 101, DispatchWeight: 2}).Error; err != nil {
		t.Fatalf("create concurrent active conversation error = %v", err)
	}
	pending := &models.Conversation{ID: 33, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(pending).Error; err != nil {
		t.Fatalf("create pending conversation error = %v", err)
	}
	decision := dispatchDecision{
		candidate: dispatchCandidate{
			profile:          *AgentProfileService.Get(1),
			dispatchMode:     enums.AgentTeamDispatchModeRule,
			activeCount:      0,
			weightedOpenLoad: 0,
		},
		mode:           enums.AgentTeamDispatchModeRule,
		reason:         "stale load snapshot",
		workloadWeight: 1,
	}
	if _, err := ConversationDispatchService.tryAssignWithDecision(pending.ID, decision); !errors.Is(err, errConversationDispatchConflict) {
		t.Fatalf("expected changed load snapshot conflict, got %v", err)
	}
	if got := ConversationService.Get(pending.ID); got == nil || got.CurrentAssigneeID != 0 || got.Status != enums.IMConversationStatusPending {
		t.Fatalf("stale load snapshot must not assign conversation, got %+v", got)
	}
}

func TestDispatchRejectsChangedShiftLoadSnapshotInsideTransaction(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	now := time.Now()
	finishedAt := now.Add(-20 * time.Minute)
	if err := db.Create(&models.ConversationAssignment{
		TenantID:       101,
		ConversationID: 88,
		ToUserID:       101,
		DispatchMode:   enums.AgentTeamDispatchModeRule,
		WorkloadWeight: 3,
		Status:         enums.IMAssignmentStatusInactive,
		CreatedAt:      now.Add(-30 * time.Minute),
		FinishedAt:     &finishedAt,
	}).Error; err != nil {
		t.Fatalf("create completed shift assignment error = %v", err)
	}
	pending := &models.Conversation{ID: 34, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(pending).Error; err != nil {
		t.Fatalf("create pending conversation error = %v", err)
	}
	decision := dispatchDecision{
		candidate: dispatchCandidate{
			profile:      *AgentProfileService.Get(1),
			dispatchMode: enums.AgentTeamDispatchModeRule,
		},
		mode:           enums.AgentTeamDispatchModeRule,
		reason:         "stale shift load snapshot",
		workloadWeight: 1,
	}
	if _, err := ConversationDispatchService.tryAssignWithDecision(pending.ID, decision); !errors.Is(err, errConversationDispatchConflict) {
		t.Fatalf("expected changed shift load snapshot conflict, got %v", err)
	}
	if got := ConversationService.Get(pending.ID); got == nil || got.CurrentAssigneeID != 0 || got.Status != enums.IMConversationStatusPending {
		t.Fatalf("stale shift snapshot must not assign conversation, got %+v", got)
	}
}

func TestDispatchRechecksScheduledSquadMembershipInsideTransaction(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	squadID := createDispatchSquad(t, db, []int64{1})
	createDispatchSquadSchedule(t, db, squadID)
	pending := &models.Conversation{ID: 37, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(pending).Error; err != nil {
		t.Fatalf("create pending conversation error = %v", err)
	}
	candidates, _, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, time.Now())
	if err != nil || len(candidates) != 1 {
		t.Fatalf("pick scheduled squad candidate error=%v candidates=%+v", err, candidates)
	}
	if err := db.Model(&models.AgentTeamSquadMember{}).
		Where("tenant_id = ? AND squad_id = ? AND agent_profile_id = ?", 101, squadID, candidates[0].profile.ID).
		Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable scheduled squad membership error = %v", err)
	}
	decision := dispatchDecision{
		candidate:             candidates[0],
		mode:                  enums.AgentTeamDispatchModeRule,
		reason:                "stale scheduled squad membership",
		workloadWeight:        1,
		expectedLastMessageID: pending.LastMessageID,
	}
	if _, err := ConversationDispatchService.tryAssignWithDecision(pending.ID, decision); !errors.Is(err, errConversationDispatchConflict) {
		t.Fatalf("expected changed scheduled squad membership conflict, got %v", err)
	}
}

func TestAutomaticDispatchRollsBackWhenRouteTransitionFails(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Update("store_scope_ids", "1").Error; err != nil {
		t.Fatalf("set team store scope error = %v", err)
	}
	pending := &models.Conversation{ID: 35, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(pending).Error; err != nil {
		t.Fatalf("create pending conversation error = %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		TenantID:       101,
		ConversationID: pending.ID,
		StoreID:        1,
		RouteStatus:    enums.ConversationRouteStatusHQAgentDeskPending,
		RouteTarget:    "agentdesk_hq",
		SessionNo:      1,
	}).Error; err != nil {
		t.Fatalf("create route state error = %v", err)
	}
	if err := db.Exec(`
		CREATE TRIGGER reject_dispatch_route_update
		BEFORE UPDATE OF route_status ON t_conversation_route_state
		WHEN NEW.route_status = 'HQ_AGENTDESK_SERVING'
		BEGIN
			SELECT RAISE(ABORT, 'route update rejected');
		END;
	`).Error; err != nil {
		t.Fatalf("create route rejection trigger error = %v", err)
	}
	decision := dispatchDecision{
		candidate: dispatchCandidate{
			profile:      *AgentProfileService.Get(1),
			dispatchMode: enums.AgentTeamDispatchModeRule,
		},
		mode:                  enums.AgentTeamDispatchModeRule,
		reason:                "route rollback test",
		workloadWeight:        1,
		expectedLastMessageID: pending.LastMessageID,
	}
	if _, err := ConversationDispatchService.tryAssignWithDecision(pending.ID, decision); err == nil || !strings.Contains(err.Error(), "route update rejected") {
		t.Fatalf("expected route transition failure, got %v", err)
	}
	if got := ConversationService.Get(pending.ID); got == nil || got.CurrentAssigneeID != 0 || got.Status != enums.IMConversationStatusPending {
		t.Fatalf("route failure must roll back conversation assignment, got %+v", got)
	}
	var assignmentCount int64
	if err := db.Model(&models.ConversationAssignment{}).Where("conversation_id = ?", pending.ID).Count(&assignmentCount).Error; err != nil {
		t.Fatalf("count rolled back assignments error = %v", err)
	}
	if assignmentCount != 0 {
		t.Fatalf("route failure must roll back assignment rows, got %d", assignmentCount)
	}
	var eventCount int64
	if err := db.Model(&models.ConversationEventLog{}).Where("conversation_id = ?", pending.ID).Count(&eventCount).Error; err != nil {
		t.Fatalf("count rolled back events error = %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("route failure must roll back event rows, got %d", eventCount)
	}
}

func TestReleaseSchedulesAutomaticRedispatch(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	now := time.Now()
	conversation := &models.Conversation{
		ID:                36,
		TenantID:          101,
		Status:            enums.IMConversationStatusActive,
		CurrentTeamID:     1,
		CurrentAssigneeID: 101,
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create active conversation error = %v", err)
	}
	if err := db.Create(&models.ConversationAssignment{
		TenantID:       101,
		ConversationID: conversation.ID,
		ToUserID:       101,
		AssignType:     string(enums.IMAssignmentTypeAssign),
		DispatchMode:   enums.AgentTeamDispatchModeRule,
		WorkloadWeight: 1,
		Status:         enums.IMAssignmentStatusActive,
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create active assignment error = %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		TenantID:       101,
		ConversationID: conversation.ID,
		RouteStatus:    enums.ConversationRouteStatusHQAgentDeskServing,
		RouteTarget:    "agentdesk_hq",
		SessionNo:      1,
	}).Error; err != nil {
		t.Fatalf("create serving route error = %v", err)
	}

	previousRealtime := ConversationDispatchService.realtimeSchedulingEnabled.Load()
	ConversationDispatchService.realtimeSchedulingEnabled.Store(true)
	t.Cleanup(func() {
		ConversationDispatchService.realtimeSchedulingEnabled.Store(previousRealtime)
		ConversationDispatchService.dispatchMu.Lock()
		if timer := ConversationDispatchService.dispatchTimers[conversation.ID]; timer != nil {
			timer.Stop()
			delete(ConversationDispatchService.dispatchTimers, conversation.ID)
		}
		ConversationDispatchService.dispatchMu.Unlock()
	})
	operator := &dto.AuthPrincipal{
		UserID:         9001,
		Username:       "dispatch-admin",
		TenantID:       101,
		ActiveTenantID: 101,
		Roles:          []string{constants.RoleCodeAdmin},
	}
	if err := ConversationDispatchWorkbenchService.Release(request.ConversationDispatchActionRequest{
		ConversationID: conversation.ID,
		Reason:         "释放后重新均衡",
	}, operator); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	ConversationDispatchService.dispatchMu.Lock()
	timer := ConversationDispatchService.dispatchTimers[conversation.ID]
	ConversationDispatchService.dispatchMu.Unlock()
	if timer == nil {
		t.Fatal("released automatic-team conversation must enter the debounced dispatch queue")
	}
	if got := ConversationService.Get(conversation.ID); got == nil || got.Status != enums.IMConversationStatusPending || got.CurrentAssigneeID != 0 {
		t.Fatalf("released conversation must return to pending pool, got %+v", got)
	}
}

func TestDispatchCandidatesBalanceWeightedOpenAndShiftLoad(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	conversation := &models.Conversation{ID: 40, TenantID: 101, Status: enums.IMConversationStatusActive, CurrentAssigneeID: 101, DispatchWeight: 4}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create weighted conversation error = %v", err)
	}
	if err := db.Create(&models.ConversationAssignment{
		TenantID:       101,
		ConversationID: conversation.ID,
		ToUserID:       101,
		DispatchMode:   enums.AgentTeamDispatchModeIntelligent,
		WorkloadWeight: 4,
		Status:         enums.IMAssignmentStatusActive,
		CreatedAt:      time.Now(),
	}).Error; err != nil {
		t.Fatalf("create weighted assignment error = %v", err)
	}
	candidates, _, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, time.Now())
	if err != nil {
		t.Fatalf("pickDispatchCandidates() error = %v", err)
	}
	if len(candidates) != 2 || candidates[0].profile.UserID != 102 {
		t.Fatalf("expected lower-load agent 102 first, got %+v", candidates)
	}
	if candidates[1].weightedOpenLoad != 4 || candidates[1].shiftAssignedWeight != 4 {
		t.Fatalf("expected weighted load snapshot for agent 101, got %+v", candidates[1])
	}
}

func TestDispatchLoadIncludesContinuousUnansweredMessagePressure(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	now := time.Now()
	conversation := &models.Conversation{ID: 50, TenantID: 101, Status: enums.IMConversationStatusActive, CurrentAssigneeID: 101, DispatchWeight: 1}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create active conversation error = %v", err)
	}
	if err := db.Create(&models.ConversationAssignment{
		TenantID:       101,
		ConversationID: conversation.ID,
		ToUserID:       101,
		DispatchMode:   enums.AgentTeamDispatchModeIntelligent,
		WorkloadWeight: 1,
		Status:         enums.IMAssignmentStatusActive,
		CreatedAt:      now.Add(-40 * time.Minute),
	}).Error; err != nil {
		t.Fatalf("create assignment error = %v", err)
	}
	messages := []models.Message{
		{TenantID: 101, ConversationID: conversation.ID, SenderType: enums.IMSenderTypeCustomer, ClientMsgID: "backlog-1", SeqNo: 1, AuditFields: models.AuditFields{CreatedAt: now.Add(-35 * time.Minute)}},
		{TenantID: 101, ConversationID: conversation.ID, SenderType: enums.IMSenderTypeCustomer, ClientMsgID: "backlog-2", SeqNo: 2, AuditFields: models.AuditFields{CreatedAt: now.Add(-34 * time.Minute)}},
		{TenantID: 101, ConversationID: conversation.ID, SenderType: enums.IMSenderTypeCustomer, ClientMsgID: "backlog-3", SeqNo: 3, AuditFields: models.AuditFields{CreatedAt: now.Add(-33 * time.Minute)}},
	}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatalf("create customer backlog error = %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: conversation.ID, NeedHumanFollowUp: true}).Error; err != nil {
		t.Fatalf("create route state error = %v", err)
	}

	candidates, _, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, now)
	if err != nil {
		t.Fatalf("pickDispatchCandidates() error = %v", err)
	}
	var loaded *dispatchCandidate
	for i := range candidates {
		if candidates[i].profile.UserID == 101 {
			loaded = &candidates[i]
			break
		}
	}
	if loaded == nil {
		t.Fatal("expected assigned agent in candidate load list")
	}
	if loaded.pendingReplyCount != 1 || loaded.weightedOpenLoad < 5 {
		t.Fatalf("continuous unanswered messages must increase live load, got %+v", *loaded)
	}
}

func intelligentDispatchTestCandidates() []dispatchCandidate {
	return []dispatchCandidate{
		{
			profile:             models.AgentProfile{ID: 1, TenantID: 101, UserID: 101, TeamID: 1, MaxConcurrentCount: 10},
			dispatchMode:        enums.AgentTeamDispatchModeIntelligent,
			weightedOpenLoad:    1,
			shiftAssignedWeight: 2,
			normalizedLoad:      0.15,
		},
		{
			profile:             models.AgentProfile{ID: 2, TenantID: 101, UserID: 102, TeamID: 1, MaxConcurrentCount: 10},
			dispatchMode:        enums.AgentTeamDispatchModeIntelligent,
			weightedOpenLoad:    2,
			shiftAssignedWeight: 1,
			normalizedLoad:      0.25,
		},
	}
}

func stubDispatchModelResolver(int64) (*ResolvedAIConfig, error) {
	return &ResolvedAIConfig{Config: models.AIConfig{ID: 1, Provider: enums.AIProviderOpenAI, ModelName: "dispatch-test"}, Source: "test"}, nil
}

func TestRuleDispatchAssessmentRaisesUrgentWaitingTask(t *testing.T) {
	handoffAt := time.Now().Add(-20 * time.Minute)
	conversation := &models.Conversation{Priority: 10, DispatchWeight: 1, HandoffReason: "客户投诉无法入住", HandoffAt: &handoffAt}
	weight, priority := ConversationDispatchService.ruleDispatchAssessment(conversation, nil)
	if weight <= 1 || priority <= 40 {
		t.Fatalf("urgent waiting task should be raised, weight=%d priority=%d", weight, priority)
	}
}
