package services

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
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

func TestPendingDispatchCompensationRotatesAcrossTenantBatches(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	now := time.Now()
	conversations := []models.Conversation{
		{ID: 1011, TenantID: 101, Status: enums.IMConversationStatusPending, AuditFields: models.AuditFields{CreatedAt: now}},
		{ID: 2021, TenantID: 202, Status: enums.IMConversationStatusPending, AuditFields: models.AuditFields{CreatedAt: now}},
		{ID: 3031, TenantID: 303, Status: enums.IMConversationStatusPending, AuditFields: models.AuditFields{CreatedAt: now}},
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatalf("create cross-tenant pending conversations: %v", err)
	}
	service := newConversationDispatchService()
	if _, err := service.DispatchPendingConversations(2); err != nil {
		t.Fatalf("first compensation batch: %v", err)
	}
	var firstTenantCount int64
	if err := db.Model(&models.DispatchDecisionLog{}).Where("conversation_id IN ?", []int64{1011, 2021}).Count(&firstTenantCount).Error; err != nil {
		t.Fatalf("count first tenant decisions: %v", err)
	}
	var thirdTenantCount int64
	if err := db.Model(&models.DispatchDecisionLog{}).Where("conversation_id = ?", 3031).Count(&thirdTenantCount).Error; err != nil {
		t.Fatalf("count third tenant decision: %v", err)
	}
	if firstTenantCount != 2 || thirdTenantCount != 0 {
		t.Fatalf("first batch decisions first=%d third=%d", firstTenantCount, thirdTenantCount)
	}
	if _, err := service.DispatchPendingConversations(2); err != nil {
		t.Fatalf("second compensation batch: %v", err)
	}
	if err := db.Model(&models.DispatchDecisionLog{}).Where("conversation_id = ?", 3031).Count(&thirdTenantCount).Error; err != nil {
		t.Fatalf("count rotated tenant decision: %v", err)
	}
	if thirdTenantCount != 1 {
		t.Fatalf("rotating cursor did not reach third tenant, decisions=%d cursor=%d", thirdTenantCount, service.pendingTenantCursor.Load())
	}
}

func TestPendingDispatchCompensationDoesNotLetManualQueuesCrowdRuleTeams(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.AgentTeam{ID: 2, TenantID: 101, Name: "人工编排组", DispatchMode: enums.AgentTeamDispatchModeManual, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create manual team: %v", err)
	}
	now := time.Now()
	manual := make([]models.Conversation, 0, 20)
	for i := int64(1); i <= 20; i++ {
		handoffAt := now.Add(-time.Duration(i) * time.Second)
		manual = append(manual, models.Conversation{
			ID: 6400 + i, TenantID: 101, CurrentTeamID: 2, Status: enums.IMConversationStatusPending, Priority: 100, HandoffAt: &handoffAt,
			AuditFields: models.AuditFields{CreatedAt: handoffAt},
		})
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("create manual backlog: %v", err)
	}
	ruleHandoffAt := now.Add(-time.Minute)
	ruleConversation := &models.Conversation{
		ID: 6499, TenantID: 101, CurrentTeamID: 1, Status: enums.IMConversationStatusPending, Priority: 1, HandoffAt: &ruleHandoffAt,
		AuditFields: models.AuditFields{CreatedAt: ruleHandoffAt},
	}
	if err := db.Create(ruleConversation).Error; err != nil {
		t.Fatalf("create rule task: %v", err)
	}

	service := newConversationDispatchService()
	dispatched, err := service.DispatchPendingConversations(1)
	if err != nil {
		t.Fatalf("run compensation: %v", err)
	}
	current := ConversationService.Get(ruleConversation.ID)
	if dispatched != 1 || current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID == 0 {
		t.Fatalf("rule task must dispatch despite manual backlog, dispatched=%d current=%+v", dispatched, current)
	}
}

func TestPendingTeamRepositoryScanIsBoundedAndIncludesExplicitRoute(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	if err := db.Create(&models.AgentTeam{ID: 7, TenantID: 101, Name: "有界扫描组", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create bounded scan team: %v", err)
	}
	conversations := make([]models.Conversation, 0, 40)
	for i := int64(1); i <= 40; i++ {
		conversations = append(conversations, models.Conversation{ID: 4000 + i, TenantID: 101, CurrentTeamID: 7, Status: enums.IMConversationStatusPending, Priority: int(i)})
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatalf("create bounded scan conversations: %v", err)
	}
	routed := models.Conversation{ID: 4999, TenantID: 101, Status: enums.IMConversationStatusPending, Priority: 100}
	if err := db.Create(&routed).Error; err != nil {
		t.Fatalf("create routed conversation: %v", err)
	}
	instance := models.WxWorkProtocolInstance{ID: 88, TenantID: 101, AgentTeamID: 7, Guid: "bounded-route", Status: enums.StatusOk}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatalf("create routed instance: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: routed.ID, WxWorkInstanceID: instance.ID}).Error; err != nil {
		t.Fatalf("create routed state: %v", err)
	}

	got, err := repositories.ConversationRepository.FindPendingUnassignedForTeam(sqls.DB(), 101, 7, 10)
	if err != nil {
		t.Fatalf("bounded team scan: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("bounded team scan returned %d rows, want 10", len(got))
	}
	if got[0].ID != routed.ID {
		t.Fatalf("explicitly routed high-priority conversation missing from bounded scan: %+v", got)
	}
}

func TestPendingRepositoryWindowsAlwaysIncludeOldestTask(t *testing.T) {
	t.Run("tenant", func(t *testing.T) {
		db := setupConversationDispatchSquadTestDB(t)
		now := time.Now()
		oldestAt := now.Add(-2 * time.Hour)
		conversations := []models.Conversation{{
			ID: 6100, TenantID: 101, Status: enums.IMConversationStatusPending, Priority: 0, HandoffAt: &oldestAt,
			AuditFields: models.AuditFields{CreatedAt: oldestAt},
		}}
		for i := int64(1); i <= 10; i++ {
			handoffAt := now.Add(-time.Duration(i) * time.Second)
			conversations = append(conversations, models.Conversation{
				ID: 6100 + i, TenantID: 101, Status: enums.IMConversationStatusPending, Priority: 100, HandoffAt: &handoffAt,
				AuditFields: models.AuditFields{CreatedAt: handoffAt},
			})
		}
		if err := db.Create(&conversations).Error; err != nil {
			t.Fatalf("create tenant pending window: %v", err)
		}
		got, err := repositories.ConversationRepository.FindPendingUnassignedByTenant(db, 101, 5)
		if err != nil {
			t.Fatalf("load tenant pending window: %v", err)
		}
		if !conversationListContains(got, 6100) {
			t.Fatalf("oldest tenant task missing from bounded window: %+v", got)
		}
	})

	t.Run("team", func(t *testing.T) {
		db := setupConversationDispatchSquadTestDB(t)
		if err := db.Create(&models.AgentTeam{ID: 7, TenantID: 101, Name: "防饥饿组", Status: enums.StatusOk}).Error; err != nil {
			t.Fatalf("create team: %v", err)
		}
		now := time.Now()
		oldestAt := now.Add(-2 * time.Hour)
		conversations := []models.Conversation{{
			ID: 6200, TenantID: 101, CurrentTeamID: 7, Status: enums.IMConversationStatusPending, Priority: 0, HandoffAt: &oldestAt,
			AuditFields: models.AuditFields{CreatedAt: oldestAt},
		}}
		for i := int64(1); i <= 10; i++ {
			handoffAt := now.Add(-time.Duration(i) * time.Second)
			conversations = append(conversations, models.Conversation{
				ID: 6200 + i, TenantID: 101, CurrentTeamID: 7, Status: enums.IMConversationStatusPending, Priority: 100, HandoffAt: &handoffAt,
				AuditFields: models.AuditFields{CreatedAt: handoffAt},
			})
		}
		if err := db.Create(&conversations).Error; err != nil {
			t.Fatalf("create team pending window: %v", err)
		}
		got, err := repositories.ConversationRepository.FindPendingUnassignedForTeam(db, 101, 7, 5)
		if err != nil {
			t.Fatalf("load team pending window: %v", err)
		}
		if !conversationListContains(got, 6200) {
			t.Fatalf("oldest team task missing from bounded window: %+v", got)
		}
	})
}

func TestPendingProcessingWindowReservesOldestTask(t *testing.T) {
	setupConversationDispatchSquadTestDB(t)
	now := time.Now()
	oldestAt := now.Add(-2 * time.Hour)
	conversations := []models.Conversation{{ID: 6300, TenantID: 101, Status: enums.IMConversationStatusPending, Priority: 0, HandoffAt: &oldestAt}}
	for i := int64(1); i <= 10; i++ {
		handoffAt := now.Add(-time.Duration(i) * time.Second)
		conversations = append(conversations, models.Conversation{ID: 6300 + i, TenantID: 101, Status: enums.IMConversationStatusPending, Priority: 100, HandoffAt: &handoffAt})
	}
	got := ConversationDispatchService.prioritizePendingConversationWindow(conversations, now, 5)
	if len(got) < 5 || !conversationListContains(got[:5], 6300) {
		t.Fatalf("oldest task must be reserved inside processing budget: %+v", got[:min(len(got), 5)])
	}
}

func conversationListContains(conversations []models.Conversation, id int64) bool {
	for _, conversation := range conversations {
		if conversation.ID == id {
			return true
		}
	}
	return false
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
	var timer *time.Timer
	for _, scheduled := range service.dispatchTimers {
		timer = scheduled
		break
	}
	service.dispatchMu.Unlock()
	if timer == nil {
		t.Fatal("runtime enablement must activate debounced dispatch")
	}
	timer.Stop()
}

func TestPendingConversationPriorityEventuallyAgesRoutineTaskAheadOfNewUrgentTask(t *testing.T) {
	setupConversationDispatchSquadTestDB(t)
	now := time.Now()
	routineAt := now.Add(-10 * time.Minute)
	urgentAt := now.Add(-time.Minute)
	got := ConversationDispatchService.prioritizePendingConversations([]models.Conversation{
		{ID: 1, TenantID: 101, HandoffAt: &routineAt, HandoffReason: "普通咨询"},
		{ID: 2, TenantID: 101, HandoffAt: &urgentAt, HandoffReason: "客户投诉无法入住"},
	}, now)
	if len(got) != 2 || got[0].ID != 1 {
		t.Fatalf("long-waiting task should eventually outrank a newer non-critical urgent task, got %+v", got)
	}
}

func TestAutomaticDispatchRecordsUnresolvedTeamFailure(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	conversation := &models.Conversation{ID: 9, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create unresolved-team conversation: %v", err)
	}
	service := newConversationDispatchService()
	if dispatched, err := service.DispatchPendingConversation(conversation); err != nil || dispatched != nil {
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

func TestAutomaticDispatchEvidenceDeduplicatesUnchangedAttempts(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	conversation := &models.Conversation{ID: 90, TenantID: 101, Status: enums.IMConversationStatusPending, LastMessageID: 100}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create pending conversation: %v", err)
	}
	service := newConversationDispatchService()
	for range 2 {
		service.recordAutomaticDispatchEvidence(conversation, nil, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "未找到归属客服组", "unresolved_team", ruleDispatchExecutionContext{}, time.Now())
	}
	var count int64
	if err := db.Model(&models.DispatchDecisionLog{}).Where("tenant_id = ? AND conversation_id = ?", conversation.TenantID, conversation.ID).Count(&count).Error; err != nil {
		t.Fatalf("count dispatch evidence: %v", err)
	}
	if count != 1 {
		t.Fatalf("unchanged dispatch attempts created %d evidence rows, want 1", count)
	}

	conversation.LastMessageID = 101
	service.recordAutomaticDispatchEvidence(conversation, nil, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "未找到归属客服组", "unresolved_team", ruleDispatchExecutionContext{}, time.Now())
	if err := db.Model(&models.DispatchDecisionLog{}).Where("tenant_id = ? AND conversation_id = ?", conversation.TenantID, conversation.ID).Count(&count).Error; err != nil {
		t.Fatalf("count changed dispatch evidence: %v", err)
	}
	if count != 2 {
		t.Fatalf("new message version created %d evidence rows, want 2", count)
	}
}

func TestAutomaticDispatchEvidenceAttributesSingleRequestedTeam(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	conversation := &models.Conversation{ID: 91, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	service := newConversationDispatchService()
	service.recordAutomaticDispatchEvidence(conversation, []int64{77}, nil, nil, 0, string(enums.AgentTeamDispatchModeRule), enums.DispatchDecisionStatusFailed, "无可用客服", "no_candidate", ruleDispatchExecutionContext{}, time.Now())
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

func TestWorkbenchRuleDispatchUsesCoreTransactionAndPreservesOperatorAudit(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	conversation := &models.Conversation{
		ID:            92,
		TenantID:      101,
		CurrentTeamID: 1,
		Status:        enums.IMConversationStatusPending,
		LastMessageID: 700,
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create workbench pending conversation: %v", err)
	}
	operator := &dto.AuthPrincipal{
		UserID:         900,
		TenantID:       101,
		ActiveTenantID: 101,
		Username:       "dispatch-manager",
		Roles:          []string{constants.RoleCodeAdmin},
	}
	if err := ConversationDispatchWorkbenchService.AutoAssign(request.ConversationDispatchAutoAssignRequest{
		ConversationID: conversation.ID,
		TeamID:         1,
	}, operator); err != nil {
		t.Fatalf("workbench rule dispatch: %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID == 0 || current.CurrentTeamID != 1 {
		t.Fatalf("workbench dispatch did not activate conversation: %+v", current)
	}
	assignment := repositories.ConversationAssignmentRepository.FindOne(db, sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("conversation_id", conversation.ID).
		Desc("id"))
	if assignment == nil || assignment.OperatorID != operator.UserID || assignment.DispatchMode != enums.AgentTeamDispatchModeRule {
		t.Fatalf("workbench assignment audit=%+v", assignment)
	}
	var evidence models.DispatchDecisionLog
	if err := db.Where("tenant_id = ? AND assignment_id = ?", conversation.TenantID, assignment.ID).Take(&evidence).Error; err != nil {
		t.Fatalf("find workbench dispatch evidence: %v", err)
	}
	if evidence.Trigger != dispatchTriggerOperatorRule || evidence.OperatorID != operator.UserID || evidence.Status != enums.DispatchDecisionStatusSelected {
		t.Fatalf("workbench dispatch evidence=%+v", evidence)
	}
}

func TestWorkbenchRuleDispatchRejectsStaleRequestedTeam(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	conversation := &models.Conversation{ID: 93, TenantID: 101, CurrentTeamID: 1, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create stale-team conversation: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 900, TenantID: 101, ActiveTenantID: 101, Username: "dispatch-manager", Roles: []string{constants.RoleCodeAdmin}}
	err := ConversationDispatchWorkbenchService.AutoAssign(request.ConversationDispatchAutoAssignRequest{
		ConversationID: conversation.ID,
		TeamID:         2,
	}, operator)
	if err == nil || !strings.Contains(err.Error(), "归属客服组已变化") {
		t.Fatalf("expected stale team error, got %v", err)
	}
	if got := ConversationService.Get(conversation.ID); got == nil || got.CurrentAssigneeID != 0 || got.Status != enums.IMConversationStatusPending {
		t.Fatalf("stale team request changed conversation: %+v", got)
	}
}

func TestRuleDispatchUsesUniqueDefaultTeamWithoutAIAgent(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Update("is_default", true).Error; err != nil {
		t.Fatalf("mark default team: %v", err)
	}
	conversation := &models.Conversation{TenantID: 101, AIAgentID: 999, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create pending web conversation: %v", err)
	}

	service := newConversationDispatchService()
	dispatched, err := service.DispatchConversation(conversation.ID)
	if err != nil {
		t.Fatalf("dispatch without AI Agent: %v", err)
	}
	if dispatched == nil || dispatched.CurrentTeamID != 1 || dispatched.CurrentAssigneeID == 0 {
		t.Fatalf("unique default team should dispatch missing-agent conversation, got %+v", dispatched)
	}
}

func TestRuleDispatchDoesNotRequireEnabledAIAgent(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Update("is_default", true).Error; err != nil {
		t.Fatalf("mark default team: %v", err)
	}
	aiAgent := &models.AIAgent{TenantID: 101, Name: "已停用运行策略", Status: enums.StatusDisabled}
	if err := db.Create(aiAgent).Error; err != nil {
		t.Fatalf("create disabled AI Agent: %v", err)
	}
	conversation := &models.Conversation{TenantID: 101, AIAgentID: aiAgent.ID, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create pending conversation: %v", err)
	}

	service := newConversationDispatchService()
	dispatched, err := service.DispatchConversation(conversation.ID)
	if err != nil {
		t.Fatalf("dispatch with disabled AI Agent: %v", err)
	}
	if dispatched == nil || dispatched.CurrentTeamID != 1 || dispatched.CurrentAssigneeID == 0 {
		t.Fatalf("disabled AI Agent must not block rule dispatch, got %+v", dispatched)
	}
}

func TestRuleDispatchPrefersWxWorkOwningTeamOverLegacyAIAgentTeams(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.AgentTeam{ID: 2, TenantID: 101, Name: "历史策略客服组", DispatchMode: enums.AgentTeamDispatchModeRule, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create legacy team: %v", err)
	}
	aiAgent := &models.AIAgent{TenantID: 101, Name: "历史运行策略", TeamIDs: "2", Status: enums.StatusOk}
	if err := db.Create(aiAgent).Error; err != nil {
		t.Fatalf("create legacy AI Agent: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{TenantID: 101, AgentTeamID: 1, Guid: "dispatch-owner", Status: enums.StatusOk}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create wxwork owner: %v", err)
	}
	if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Update("wx_work_instance_scope_ids", fmt.Sprint(instance.ID)).Error; err != nil {
		t.Fatalf("sync wxwork owner team scope: %v", err)
	}
	conversation := &models.Conversation{TenantID: 101, AIAgentID: aiAgent.ID, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create pending wxwork conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: conversation.ID, WxWorkInstanceID: instance.ID}).Error; err != nil {
		t.Fatalf("create wxwork route: %v", err)
	}

	service := newConversationDispatchService()
	dispatched, err := service.DispatchConversation(conversation.ID)
	if err != nil {
		t.Fatalf("dispatch wxwork-owned conversation: %v", err)
	}
	if dispatched == nil || dispatched.CurrentTeamID != 1 || dispatched.CurrentAssigneeID == 0 {
		t.Fatalf("wxwork owning team must override legacy AI Agent teams, got %+v", dispatched)
	}
}

func TestRuleDispatchLeavesAmbiguousRouteOwnershipPending(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.AgentTeam{ID: 2, TenantID: 101, Name: "冲突客服组", DispatchMode: enums.AgentTeamDispatchModeRule, Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create conflicting team: %v", err)
	}
	binding := &models.StoreStaffBinding{TenantID: 101, StoreID: 88, AgentTeamID: 2, Status: enums.StatusOk}
	if err := db.Create(binding).Error; err != nil {
		t.Fatalf("create conflicting store binding: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{TenantID: 101, AgentTeamID: 1, StoreStaffBindingID: binding.ID, Guid: "ambiguous-owner", Status: enums.StatusOk}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create ambiguous wxwork instance: %v", err)
	}
	conversation := &models.Conversation{TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create ambiguous pending conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: conversation.ID, StoreID: 88, WxWorkInstanceID: instance.ID}).Error; err != nil {
		t.Fatalf("create ambiguous route: %v", err)
	}

	service := newConversationDispatchService()
	dispatched, err := service.DispatchConversation(conversation.ID)
	if err != nil {
		t.Fatalf("scan ambiguous conversation: %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if dispatched != nil || current == nil || current.Status != enums.IMConversationStatusPending || current.CurrentAssigneeID != 0 || current.CurrentTeamID != 0 {
		t.Fatalf("ambiguous ownership must stay pending and unassigned, dispatched=%+v current=%+v", dispatched, current)
	}
}

func TestFairRuleCandidateBandExcludesOverloadedCandidate(t *testing.T) {
	candidates := ruleDispatchTestCandidates()
	candidates[1].weightedOpenLoad = 5
	candidates[1].normalizedLoad = 0.8
	shortlist := fairRuleCandidateBand(candidates)
	if len(shortlist) != 1 || shortlist[0].profile.UserID != 101 {
		t.Fatalf("overloaded candidate must not enter the fairness band, got %+v", shortlist)
	}
}

func TestRuleDispatchPrefersLowerCapacityAdjustedShiftDebtInsideFairBand(t *testing.T) {
	candidates := ruleDispatchTestCandidates()
	candidates[0].shiftWorkloadWeight = 6
	candidates[1].shiftWorkloadWeight = 2
	decision := ConversationDispatchService.selectDispatchDecision(&models.Conversation{TenantID: 101}, nil, candidates)
	if decision.candidate.profile.UserID != 102 {
		t.Fatalf("lower shift debt candidate must be selected, got %+v", decision)
	}
}

func TestRuleDispatchContinuityOnlyBreaksEqualFairnessTie(t *testing.T) {
	candidates := ruleDispatchTestCandidates()
	candidates[0].shiftWorkloadWeight = 2
	candidates[1].shiftWorkloadWeight = 2
	continuity := map[int64]bool{102: true}
	if got := compareRuleCandidates(candidates[0], candidates[1], continuity); got <= 0 {
		t.Fatalf("recent handler should win an otherwise equal fairness tie, compare=%d", got)
	}
	candidates[1].normalizedLoad = 0.8
	band := fairRuleCandidateBand(candidates)
	if len(band) != 1 || band[0].profile.UserID != 101 {
		t.Fatalf("continuity must not bypass the fairness band, got %+v", band)
	}
}

func TestDispatchRejectsStaleRuleDecision(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	conversation := &models.Conversation{ID: 20, TenantID: 101, Status: enums.IMConversationStatusPending, LastMessageID: 10}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation error = %v", err)
	}
	decision := dispatchDecision{
		candidate:             dispatchCandidate{profile: *AgentProfileService.Get(1)},
		reason:                "stale rule result",
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
		candidate:      dispatchCandidate{profile: *AgentProfileService.Get(1)},
		reason:         "capacity recheck",
		workloadWeight: 1,
	}
	if _, err := ConversationDispatchService.tryAssignWithDecision(pending.ID, decision); !errors.Is(err, errConversationDispatchConflict) {
		t.Fatalf("expected capacity conflict, got %v", err)
	}
}

func TestDispatchCandidatesRequireFreshAvailablePresence(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	now := time.Now()
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id = ? AND ended_at IS NULL", 101, 101).
		Updates(map[string]any{"status": enums.AgentPresenceStatusIdle, "last_seen_at": now.Add(-4 * time.Minute)}).Error; err != nil {
		t.Fatalf("make agent 101 presence stale: %v", err)
	}
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id = ? AND ended_at IS NULL", 101, 102).
		Updates(map[string]any{"status": enums.AgentPresenceStatusBreak, "last_seen_at": now}).Error; err != nil {
		t.Fatalf("put agent 102 on break: %v", err)
	}
	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, now)
	if err != nil {
		t.Fatalf("pick unavailable presence candidates: %v", err)
	}
	if len(candidates) != 0 || report.OnlineProfiles != 0 || report.Reason != "no_online_agent" {
		t.Fatalf("stale and break agents must be excluded, candidates=%+v report=%+v", candidates, report)
	}
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id = ? AND ended_at IS NULL", 101, 101).
		Updates(map[string]any{"status": enums.AgentPresenceStatusOnline, "last_seen_at": now}).Error; err != nil {
		t.Fatalf("restore agent 101 presence: %v", err)
	}
	candidates, report, err = ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, now)
	if err != nil || len(candidates) != 1 || candidates[0].profile.UserID != 101 || report.OnlineProfiles != 1 {
		t.Fatalf("fresh online agent should be eligible, candidates=%+v report=%+v err=%v", candidates, report, err)
	}
}

func TestDispatchWorkbenchAvailabilityUsesRuleEligibility(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	operator := &dto.AuthPrincipal{UserID: 900, TenantID: 101, ActiveTenantID: 101, Username: "tenant-admin", Roles: []string{constants.RoleCodeTenantAdmin}}

	loads, err := ConversationDispatchWorkbenchService.ListAgentLoads(1, operator)
	if err != nil || len(loads) != 2 || !loads[0].Available || !loads[1].Available {
		t.Fatalf("fresh scheduled agents should be available, loads=%+v err=%v", loads, err)
	}
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id = ? AND ended_at IS NULL", 101, 101).
		Updates(map[string]any{"status": enums.AgentPresenceStatusBreak, "last_seen_at": time.Now()}).Error; err != nil {
		t.Fatalf("put agent 101 on break: %v", err)
	}
	if err := db.Model(&models.AgentTeamSchedule{}).Where("tenant_id = ? AND team_id = ?", 101, 1).Update("excluded_agent_profile_ids", "2").Error; err != nil {
		t.Fatalf("exclude agent 102 from active shift: %v", err)
	}

	loads, err = ConversationDispatchWorkbenchService.ListAgentLoads(1, operator)
	if err != nil || len(loads) != 2 {
		t.Fatalf("load ineligible agent states: loads=%+v err=%v", loads, err)
	}
	loadByUserID := make(map[int64]response.ConversationDispatchAgentLoadResponse, len(loads))
	for _, load := range loads {
		loadByUserID[load.UserID] = load
	}
	if loadByUserID[101].Available || loadByUserID[101].AvailabilityCode != dispatchAvailabilityBreak {
		t.Fatalf("break agent availability=%+v", loadByUserID[101])
	}
	if loadByUserID[102].Available || loadByUserID[102].AvailabilityCode != dispatchAvailabilityOutOfShift {
		t.Fatalf("excluded agent availability=%+v", loadByUserID[102])
	}
}

func TestDispatchRechecksPresenceInsideTransaction(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	pending := &models.Conversation{ID: 310, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(pending).Error; err != nil {
		t.Fatalf("create pending conversation: %v", err)
	}
	candidates, _, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, time.Now())
	if err != nil || len(candidates) == 0 {
		t.Fatalf("pick initial candidates=%+v err=%v", candidates, err)
	}
	selected := candidates[0]
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id = ? AND ended_at IS NULL", 101, selected.profile.UserID).
		Updates(map[string]any{"status": enums.AgentPresenceStatusBreak, "last_seen_at": time.Now()}).Error; err != nil {
		t.Fatalf("change selected presence: %v", err)
	}
	decision := dispatchDecision{
		candidate:             selected,
		reason:                "presence changed after selection",
		workloadWeight:        1,
		expectedLastMessageID: pending.LastMessageID,
	}
	if _, err := ConversationDispatchService.tryAssignWithDecision(pending.ID, decision); !errors.Is(err, errConversationDispatchConflict) {
		t.Fatalf("expected changed presence conflict, got %v", err)
	}
	if got := ConversationService.Get(pending.ID); got == nil || got.Status != enums.IMConversationStatusPending || got.CurrentAssigneeID != 0 {
		t.Fatalf("presence conflict must leave conversation pending, got %+v", got)
	}
}

func TestDispatchScheduleOverridesIncludeReliefAndExcludeSquadMember(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	squadID := createDispatchSquad(t, db, []int64{1})
	now := time.Now()
	if err := db.Create(&models.AgentTeamSchedule{
		TenantID:                101,
		TeamID:                  1,
		SquadID:                 squadID,
		IncludedAgentProfileIDs: "2",
		ExcludedAgentProfileIDs: "1",
		StartAt:                 now.Add(-time.Hour),
		EndAt:                   now.Add(time.Hour),
		Status:                  enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create override schedule: %v", err)
	}
	candidates, _, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, now)
	if err != nil {
		t.Fatalf("pick override candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].profile.ID != 2 || candidates[0].squadID != squadID {
		t.Fatalf("temporary relief must replace excluded squad member, got %+v", candidates)
	}
}

func TestDispatchUnionsAgentsAcrossOverlappingSquadSchedules(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	squads := []models.AgentTeamSquad{
		{TenantID: 101, TeamID: 1, Name: "交班白班", Status: enums.StatusOk},
		{TenantID: 101, TeamID: 1, Name: "交班晚班", Status: enums.StatusOk},
	}
	if err := db.Create(&squads).Error; err != nil {
		t.Fatalf("create overlapping squads: %v", err)
	}
	members := []models.AgentTeamSquadMember{
		{TenantID: 101, SquadID: squads[0].ID, AgentProfileID: 1, Status: enums.StatusOk},
		{TenantID: 101, SquadID: squads[1].ID, AgentProfileID: 2, Status: enums.StatusOk},
	}
	if err := db.Create(&members).Error; err != nil {
		t.Fatalf("create overlapping squad members: %v", err)
	}
	now := time.Now()
	schedules := []models.AgentTeamSchedule{
		{TenantID: 101, TeamID: 1, SquadID: squads[0].ID, StartAt: now.Add(-2 * time.Hour), EndAt: now.Add(30 * time.Minute), Status: enums.StatusOk},
		{TenantID: 101, TeamID: 1, SquadID: squads[1].ID, StartAt: now.Add(-30 * time.Minute), EndAt: now.Add(8 * time.Hour), Status: enums.StatusOk},
	}
	if err := db.Create(&schedules).Error; err != nil {
		t.Fatalf("create overlapping schedules: %v", err)
	}
	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, now)
	if err != nil {
		t.Fatalf("pick candidates from overlapping squads: %v", err)
	}
	if len(candidates) != 2 || report.CandidateCount != 2 {
		t.Fatalf("overlapping schedules must union both squads, candidates=%+v report=%+v", candidates, report)
	}
	squadByUserID := map[int64]int64{}
	for _, candidate := range candidates {
		squadByUserID[candidate.profile.UserID] = candidate.squadID
	}
	if squadByUserID[101] != squads[0].ID || squadByUserID[102] != squads[1].ID {
		t.Fatalf("candidate squad snapshots=%+v want agent 101->%d agent 102->%d", squadByUserID, squads[0].ID, squads[1].ID)
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
			activeCount:      0,
			weightedOpenLoad: 0,
		},
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
	if err := db.Create(&models.Conversation{ID: 88, TenantID: 101, Status: enums.IMConversationStatusClosed}).Error; err != nil {
		t.Fatalf("create completed shift conversation error = %v", err)
	}
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
	if err := db.Create(&models.Message{
		TenantID: 101, ConversationID: 88, SessionNo: 1, SenderType: enums.IMSenderTypeAgent, SenderID: 101,
		ClientMsgID: "completed-shift-reply", SeqNo: 1, SendStatus: enums.IMMessageStatusSent,
		AuditFields: models.AuditFields{CreatedAt: now.Add(-25 * time.Minute)},
	}).Error; err != nil {
		t.Fatalf("create completed shift reply error = %v", err)
	}
	pending := &models.Conversation{ID: 34, TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(pending).Error; err != nil {
		t.Fatalf("create pending conversation error = %v", err)
	}
	decision := dispatchDecision{
		candidate: dispatchCandidate{
			profile: *AgentProfileService.Get(1),
		},
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
			profile: *AgentProfileService.Get(1),
		},
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
	dispatchTimerKey := fmt.Sprintf("team:%d:%d", conversation.TenantID, conversation.CurrentTeamID)
	t.Cleanup(func() {
		ConversationDispatchService.realtimeSchedulingEnabled.Store(previousRealtime)
		ConversationDispatchService.dispatchMu.Lock()
		if timer := ConversationDispatchService.dispatchTimers[dispatchTimerKey]; timer != nil {
			timer.Stop()
			delete(ConversationDispatchService.dispatchTimers, dispatchTimerKey)
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
	timer := ConversationDispatchService.dispatchTimers[dispatchTimerKey]
	ConversationDispatchService.dispatchMu.Unlock()
	if timer == nil {
		t.Fatal("released automatic-team conversation must enter the debounced dispatch queue")
	}
	if got := ConversationService.Get(conversation.ID); got == nil || got.Status != enums.IMConversationStatusPending || got.CurrentAssigneeID != 0 {
		t.Fatalf("released conversation must return to pending pool, got %+v", got)
	}
}

func TestManualDispatchReasonIsRequired(t *testing.T) {
	if _, err := requiredManualDispatchReason("  "); err == nil {
		t.Fatal("blank manual dispatch reason must be rejected")
	}
	if reason, err := requiredManualDispatchReason("  客户投诉需专人处理  "); err != nil || reason != "客户投诉需专人处理" {
		t.Fatalf("manual dispatch reason=%q err=%v", reason, err)
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
		DispatchMode:   enums.AgentTeamDispatchModeRule,
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
	if candidates[1].weightedOpenLoad != 4 || candidates[1].shiftWorkloadWeight != 4 {
		t.Fatalf("expected weighted load snapshot for agent 101, got %+v", candidates[1])
	}
}

func TestDispatchShiftWorkloadCountsHandledManualWorkAndDropsUnrepliedRecovery(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	now := time.Now()
	finishedAt := now.Add(-10 * time.Minute)
	conversations := []models.Conversation{
		{ID: 41, TenantID: 101, Status: enums.IMConversationStatusClosed},
		{ID: 42, TenantID: 101, Status: enums.IMConversationStatusClosed},
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatalf("create shift workload conversations error = %v", err)
	}
	assignments := []models.ConversationAssignment{
		{TenantID: 101, ConversationID: 41, SessionNo: 1, ToUserID: 101, DispatchMode: enums.AgentTeamDispatchModeRule, WorkloadWeight: 5, Status: enums.IMAssignmentStatusInactive, CreatedAt: now.Add(-30 * time.Minute), FinishedAt: &finishedAt},
		{TenantID: 101, ConversationID: 42, SessionNo: 1, ToUserID: 101, DispatchMode: enums.AgentTeamDispatchModeManual, WorkloadWeight: 2, Status: enums.IMAssignmentStatusInactive, CreatedAt: now.Add(-25 * time.Minute), FinishedAt: &finishedAt},
	}
	if err := db.Create(&assignments).Error; err != nil {
		t.Fatalf("create shift workload assignments error = %v", err)
	}
	if err := db.Create(&models.Message{
		TenantID: 101, ConversationID: 42, SessionNo: 1, SenderType: enums.IMSenderTypeAgent, SenderID: 101,
		ClientMsgID: "handled-manual-reply", SeqNo: 1, SendStatus: enums.IMMessageStatusSent,
		AuditFields: models.AuditFields{CreatedAt: now.Add(-20 * time.Minute)},
	}).Error; err != nil {
		t.Fatalf("create handled manual reply error = %v", err)
	}

	candidates, _, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, now)
	if err != nil {
		t.Fatalf("pickDispatchCandidates() error = %v", err)
	}
	for _, candidate := range candidates {
		if candidate.profile.UserID == 101 {
			if candidate.shiftWorkloadWeight != 2 {
				t.Fatalf("shift workload weight = %d, want handled manual weight 2 only", candidate.shiftWorkloadWeight)
			}
			return
		}
	}
	t.Fatal("expected agent 101 in dispatch candidates")
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
		DispatchMode:   enums.AgentTeamDispatchModeRule,
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

func TestDispatchReplyFactsRequireAssignedAgent(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	now := time.Now()
	conversation := &models.Conversation{ID: 55, TenantID: 101, Status: enums.IMConversationStatusActive, CurrentAssigneeID: 101, DispatchWeight: 1}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create active conversation: %v", err)
	}
	assignment := &models.ConversationAssignment{
		TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, ToUserID: 101,
		DispatchMode: enums.AgentTeamDispatchModeRule, WorkloadWeight: 1, Status: enums.IMAssignmentStatusActive, CreatedAt: now.Add(-10 * time.Minute),
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	messages := []models.Message{
		{TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, SenderType: enums.IMSenderTypeAgent, SenderID: 102, ClientMsgID: "wrong-agent-reply", SeqNo: 1, SendStatus: enums.IMMessageStatusSent, AuditFields: models.AuditFields{CreatedAt: now.Add(-8 * time.Minute)}},
		{TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, SenderType: enums.IMSenderTypeCustomer, ClientMsgID: "customer-followup", SeqNo: 2, SendStatus: enums.IMMessageStatusSent, AuditFields: models.AuditFields{CreatedAt: now.Add(-7 * time.Minute)}},
	}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatalf("create ownership messages: %v", err)
	}
	replied, err := repositories.ConversationAssignmentRepository.HasHumanReplySince(db, assignment)
	if err != nil || replied {
		t.Fatalf("different agent must not satisfy assignment reply, replied=%v err=%v", replied, err)
	}
	unreplied := repositories.ConversationAssignmentRepository.FindActiveRuleWithoutHumanReply(db, 101, 101, 10)
	if len(unreplied) != 1 || unreplied[0].ID != assignment.ID {
		t.Fatalf("different agent reply must keep assignment recoverable: %+v", unreplied)
	}
	candidates, _, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, now)
	if err != nil {
		t.Fatalf("pick candidates: %v", err)
	}
	for _, candidate := range candidates {
		if candidate.profile.UserID == 101 {
			if candidate.pendingFirstReply != 1 || candidate.pendingReplyCount != 1 {
				t.Fatalf("reply ownership load mismatch: %+v", candidate)
			}
			return
		}
	}
	t.Fatal("assigned agent missing from candidates")
}

func TestDispatchContinuityUsesSuccessfulReplyFromOlderSession(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	now := time.Now()
	conversation := &models.Conversation{ID: 56, TenantID: 101, CustomerID: 7001, ChannelID: 9, Status: enums.IMConversationStatusPending, LastActiveAt: now}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create reused conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: conversation.ID, StoreID: 88, SessionNo: 2}).Error; err != nil {
		t.Fatalf("create current route session: %v", err)
	}
	finishedAt := now.Add(-time.Hour)
	assignments := []models.ConversationAssignment{
		{TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, ToUserID: 101, Status: enums.IMAssignmentStatusInactive, CreatedAt: now.Add(-2 * time.Hour), FinishedAt: &finishedAt},
		{TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, ToUserID: 102, Status: enums.IMAssignmentStatusInactive, CreatedAt: now.Add(-2 * time.Hour), FinishedAt: &finishedAt},
	}
	if err := db.Create(&assignments).Error; err != nil {
		t.Fatalf("create historical assignments: %v", err)
	}
	if err := db.Create(&models.Message{
		TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, SenderType: enums.IMSenderTypeAgent, SenderID: 101,
		ClientMsgID: "older-session-reply", SeqNo: 1, SendStatus: enums.IMMessageStatusSent,
		AuditFields: models.AuditFields{CreatedAt: now.Add(-90 * time.Minute)},
	}).Error; err != nil {
		t.Fatalf("create historical reply: %v", err)
	}
	continuity := ConversationDispatchService.findContinuityUsers(conversation, []dispatchCandidate{
		{profile: models.AgentProfile{UserID: 101}},
		{profile: models.AgentProfile{UserID: 102}},
	})
	if !continuity[101] || continuity[102] {
		t.Fatalf("continuity must credit only actual older-session replier: %+v", continuity)
	}
}

func ruleDispatchTestCandidates() []dispatchCandidate {
	return []dispatchCandidate{
		{
			profile:             models.AgentProfile{ID: 1, TenantID: 101, UserID: 101, TeamID: 1, MaxConcurrentCount: 10},
			weightedOpenLoad:    1,
			shiftWorkloadWeight: 2,
			normalizedLoad:      0.15,
		},
		{
			profile:             models.AgentProfile{ID: 2, TenantID: 101, UserID: 102, TeamID: 1, MaxConcurrentCount: 10},
			weightedOpenLoad:    2,
			shiftWorkloadWeight: 1,
			normalizedLoad:      0.25,
		},
	}
}

func TestRuleDispatchAssessmentAgesWaitingTaskWithoutGuessingItsWeight(t *testing.T) {
	handoffAt := time.Now().Add(-20 * time.Minute)
	conversation := &models.Conversation{Priority: 10, DispatchWeight: 1, HandoffReason: "客户投诉无法入住", HandoffAt: &handoffAt}
	weight, priority := ConversationDispatchService.ruleDispatchAssessment(conversation, nil)
	if weight != 1 || priority <= 40 {
		t.Fatalf("waiting should raise priority without text-based weight guessing, weight=%d priority=%d", weight, priority)
	}
}

func TestRuleDispatchAssessmentUsesExistingSafetyClassification(t *testing.T) {
	conversation := &models.Conversation{Priority: 10, DispatchWeight: 1, HandoffReason: "emergency_safety: 客户人身安全风险"}
	weight, priority := ConversationDispatchService.ruleDispatchAssessment(conversation, nil)
	if weight != 5 || priority != 100 {
		t.Fatalf("safety handoff weight=%d priority=%d", weight, priority)
	}
}
