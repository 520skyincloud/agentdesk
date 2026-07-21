package services

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestRuleAssignmentRecoveryTransfersUnavailableUnrepliedAssignment(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, time.Now().Add(-time.Minute))
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id = ? AND ended_at IS NULL", 101, 101).
		Updates(map[string]any{"status": enums.AgentPresenceStatusBreak, "last_seen_at": time.Now()}).Error; err != nil {
		t.Fatalf("put assigned agent on break: %v", err)
	}

	service := newConversationDispatchService()
	recovered, err := service.RecoverStaleAssignments(10)
	if err != nil {
		t.Fatalf("recover stale assignments: %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if recovered != 1 || current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != 102 || current.CurrentTeamID != 1 {
		t.Fatalf("expected transfer to available agent, recovered=%d current=%+v", recovered, current)
	}
	oldAssignment := repositories.ConversationAssignmentRepository.Get(db, assignment.ID)
	latest := repositories.ConversationAssignmentRepository.FindOne(db, sqls.NewCnd().Eq("conversation_id", conversation.ID).Desc("id"))
	if oldAssignment == nil || oldAssignment.Status != enums.IMAssignmentStatusInactive || latest == nil || latest.ID == oldAssignment.ID || latest.Status != enums.IMAssignmentStatusActive || latest.ToUserID != 102 || latest.AssignType != string(enums.IMAssignmentTypeTransfer) || latest.DispatchMode != enums.AgentTeamDispatchModeRule {
		t.Fatalf("unexpected recovery assignment history old=%+v latest=%+v", oldAssignment, latest)
	}
}

func TestAgentBreakImmediatelyRecoversRuleAssignment(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	conversation, _ := createActiveRuleRecoveryFixture(t, db, 101, time.Now().Add(-time.Minute))
	operator := &dto.AuthPrincipal{UserID: 101, TenantID: 101, ActiveTenantID: 101, Username: "agent-a"}

	if _, err := AgentPresenceService.SetStatus(operator, enums.AgentPresenceStatusBreak, "临时离席", time.Now()); err != nil {
		t.Fatalf("set assigned agent break status: %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != 102 {
		t.Fatalf("break status should immediately transfer unreplied rule assignment, current=%+v", current)
	}
}

func TestRuleAssignmentRecoveryLeavesManualAndRepliedAssignmentsUntouched(t *testing.T) {
	t.Run("manual", func(t *testing.T) {
		db := setupConversationDispatchSquadTestDB(t)
		createDispatchSquadTeamAndAgents(t, db)
		createDispatchSquadSchedule(t, db, 0)
		conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, time.Now().Add(-10*time.Minute))
		if err := db.Model(&models.ConversationAssignment{}).Where("id = ?", assignment.ID).Update("dispatch_mode", enums.AgentTeamDispatchModeManual).Error; err != nil {
			t.Fatalf("mark assignment manual: %v", err)
		}
		service := newConversationDispatchService()
		if recovered, err := service.RecoverStaleAssignments(10); err != nil || recovered != 0 {
			t.Fatalf("manual assignment recovery=%d err=%v", recovered, err)
		}
		assertRecoveryConversationAssignee(t, conversation.ID, 101)
	})

	t.Run("replied", func(t *testing.T) {
		db := setupConversationDispatchSquadTestDB(t)
		createDispatchSquadTeamAndAgents(t, db)
		createDispatchSquadSchedule(t, db, 0)
		conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, time.Now().Add(-10*time.Minute))
		createRecoveryAgentReply(t, db, assignment, assignment.CreatedAt.Add(time.Minute))
		service := newConversationDispatchService()
		if recovered, err := service.RecoverStaleAssignments(10); err != nil || recovered != 0 {
			t.Fatalf("replied assignment recovery=%d err=%v", recovered, err)
		}
		assertRecoveryConversationAssignee(t, conversation.ID, 101)
	})
}

func TestRuleAssignmentRecoveryTransfersFirstResponseSLABreach(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.ServiceAnalyticsPolicy{TenantID: 101, FirstResponseTargetSeconds: 30}).Error; err != nil {
		t.Fatalf("create SLA policy: %v", err)
	}
	conversation, _ := createActiveRuleRecoveryFixture(t, db, 101, time.Now().Add(-time.Minute))

	service := newConversationDispatchService()
	recovered, err := service.RecoverStaleAssignments(10)
	if err != nil {
		t.Fatalf("recover SLA breach: %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if recovered != 1 || current == nil || current.CurrentAssigneeID != 102 {
		t.Fatalf("expected SLA breach transfer, recovered=%d current=%+v", recovered, current)
	}
}

func TestRuleAssignmentRecoveryTransfersOverdueFollowUpWhenAgentUnavailable(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.ServiceAnalyticsPolicy{TenantID: 101, ResponseTargetSeconds: 30}).Error; err != nil {
		t.Fatalf("create response SLA policy: %v", err)
	}
	now := time.Now()
	conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, now.Add(-10*time.Minute))
	createRecoveryAgentReply(t, db, assignment, now.Add(-2*time.Minute))
	createRecoveryCustomerMessage(t, db, assignment, now.Add(-time.Minute), 2)
	setRecoveryPresenceStatus(t, db, []int64{101}, enums.AgentPresenceStatusBreak, now)

	service := newConversationDispatchService()
	recovered, err := service.RecoverStaleAssignments(10)
	if err != nil {
		t.Fatalf("recover overdue follow-up: %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if recovered != 1 || current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != 102 {
		t.Fatalf("expected overdue follow-up transfer, recovered=%d current=%+v", recovered, current)
	}
}

func TestRuleAssignmentRecoveryKeepsFollowUpBeforeResponseSLA(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.ServiceAnalyticsPolicy{TenantID: 101, ResponseTargetSeconds: 300}).Error; err != nil {
		t.Fatalf("create response SLA policy: %v", err)
	}
	now := time.Now()
	conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, now.Add(-10*time.Minute))
	createRecoveryAgentReply(t, db, assignment, now.Add(-time.Minute))
	createRecoveryCustomerMessage(t, db, assignment, now.Add(-30*time.Second), 2)
	setRecoveryPresenceStatus(t, db, []int64{101}, enums.AgentPresenceStatusBreak, now)

	service := newConversationDispatchService()
	if recovered, err := service.RecoverStaleAssignments(10); err != nil || recovered != 0 {
		t.Fatalf("follow-up before SLA recovery=%d err=%v", recovered, err)
	}
	assertRecoveryConversationAssignee(t, conversation.ID, 101)
}

func TestRuleAssignmentRecoveryDoesNotTransferBusyAgentFollowUp(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.ServiceAnalyticsPolicy{TenantID: 101, ResponseTargetSeconds: 30}).Error; err != nil {
		t.Fatalf("create response SLA policy: %v", err)
	}
	now := time.Now()
	conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, now.Add(-10*time.Minute))
	createRecoveryAgentReply(t, db, assignment, now.Add(-2*time.Minute))
	createRecoveryCustomerMessage(t, db, assignment, now.Add(-time.Minute), 2)
	setRecoveryPresenceStatus(t, db, []int64{101}, enums.AgentPresenceStatusBusy, now)

	service := newConversationDispatchService()
	if recovered, err := service.RecoverStaleAssignments(10); err != nil || recovered != 0 {
		t.Fatalf("busy agent follow-up recovery=%d err=%v", recovered, err)
	}
	assertRecoveryConversationAssignee(t, conversation.ID, 101)
}

func TestRuleAssignmentRecoveryDoesNotInterruptServedConversationForSoftProfileChanges(t *testing.T) {
	tests := []struct {
		name   string
		column string
		value  any
	}{
		{name: "automatic assignment disabled", column: "auto_assign_enabled", value: false},
		{name: "automatic capacity disabled", column: "max_concurrent_count", value: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupConversationDispatchSquadTestDB(t)
			createDispatchSquadTeamAndAgents(t, db)
			createDispatchSquadSchedule(t, db, 0)
			if err := db.Create(&models.ServiceAnalyticsPolicy{TenantID: 101, ResponseTargetSeconds: 30}).Error; err != nil {
				t.Fatalf("create response SLA policy: %v", err)
			}
			now := time.Now()
			conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, now.Add(-10*time.Minute))
			createRecoveryAgentReply(t, db, assignment, now.Add(-2*time.Minute))
			createRecoveryCustomerMessage(t, db, assignment, now.Add(-time.Minute), 2)
			if err := db.Model(&models.AgentProfile{}).Where("tenant_id = ? AND user_id = ?", 101, 101).Update(tt.column, tt.value).Error; err != nil {
				t.Fatalf("update soft profile setting: %v", err)
			}

			service := newConversationDispatchService()
			if recovered, err := service.RecoverStaleAssignments(10); err != nil || recovered != 0 {
				t.Fatalf("soft profile change recovery=%d err=%v", recovered, err)
			}
			assertRecoveryConversationAssignee(t, conversation.ID, 101)
		})
	}
}

func TestRuleAssignmentRecoveryTransfersOverdueFollowUpAfterShift(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.ServiceAnalyticsPolicy{TenantID: 101, ResponseTargetSeconds: 30}).Error; err != nil {
		t.Fatalf("create response SLA policy: %v", err)
	}
	now := time.Now()
	conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, now.Add(-10*time.Minute))
	createRecoveryAgentReply(t, db, assignment, now.Add(-2*time.Minute))
	createRecoveryCustomerMessage(t, db, assignment, now.Add(-time.Minute), 2)
	if err := db.Model(&models.AgentTeamSchedule{}).Where("team_id = ?", 1).Update("excluded_agent_profile_ids", "1").Error; err != nil {
		t.Fatalf("exclude assigned profile from shift: %v", err)
	}

	service := newConversationDispatchService()
	recovered, err := service.RecoverStaleAssignments(10)
	if err != nil {
		t.Fatalf("recover follow-up after shift: %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if recovered != 1 || current == nil || current.CurrentAssigneeID != 102 {
		t.Fatalf("expected follow-up transfer to active shift, recovered=%d current=%+v", recovered, current)
	}
}

func TestRuleAssignmentRecoveryReleasesOverdueFollowUpWithoutAlternative(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.ServiceAnalyticsPolicy{TenantID: 101, ResponseTargetSeconds: 30}).Error; err != nil {
		t.Fatalf("create response SLA policy: %v", err)
	}
	now := time.Now()
	conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, now.Add(-10*time.Minute))
	createRecoveryAgentReply(t, db, assignment, now.Add(-2*time.Minute))
	createRecoveryCustomerMessage(t, db, assignment, now.Add(-time.Minute), 2)
	setRecoveryPresenceStatus(t, db, []int64{101, 102}, enums.AgentPresenceStatusBreak, now)

	service := newConversationDispatchService()
	recovered, err := service.RecoverStaleAssignments(10)
	if err != nil {
		t.Fatalf("release overdue follow-up: %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if recovered != 1 || current == nil || current.Status != enums.IMConversationStatusPending || current.CurrentAssigneeID != 0 || current.CurrentTeamID != 1 {
		t.Fatalf("expected overdue follow-up in team pool, recovered=%d current=%+v", recovered, current)
	}
}

func TestRuleAssignmentRecoveryRechecksFollowUpReplyInsideTransaction(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	if err := db.Create(&models.ServiceAnalyticsPolicy{TenantID: 101, ResponseTargetSeconds: 30}).Error; err != nil {
		t.Fatalf("create response SLA policy: %v", err)
	}
	now := time.Now()
	conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, now.Add(-10*time.Minute))
	createRecoveryAgentReply(t, db, assignment, now.Add(-2*time.Minute))
	customerMessage := createRecoveryCustomerMessage(t, db, assignment, now.Add(-time.Minute), 2)
	setRecoveryPresenceStatus(t, db, []int64{101}, enums.AgentPresenceStatusBreak, now)

	service := newConversationDispatchService()
	candidates, _, err := service.pickDispatchCandidates([]int64{1}, 101, nil, now)
	if err != nil || len(candidates) != 1 || candidates[0].profile.UserID != 102 {
		t.Fatalf("pick alternate candidate=%+v err=%v", candidates, err)
	}
	decision := service.selectDispatchDecision(ConversationService.Get(conversation.ID), nil, candidates)
	recovery := &ruleAssignmentRecoveryCandidate{
		assignment: *assignment, stage: ruleAssignmentRecoveryStageFollowUp,
		waitingSince: serviceAnalyticsMessageTime(customerMessage), oldestUnansweredMessageID: customerMessage.ID,
	}
	createRecoveryMessage(t, db, assignment, enums.IMSenderTypeAgent, assignment.ToUserID, "已继续处理", now, 3)

	if _, err := service.tryRecoverWithDecisionContext(recovery, decision, now); !errors.Is(err, errConversationDispatchConflict) {
		t.Fatalf("expected concurrent follow-up reply conflict, got %v", err)
	}
	assertRecoveryConversationAssignee(t, conversation.ID, 101)
}

func TestRuleAssignmentRecoveryRechecksReplyInsideTransaction(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, time.Now().Add(-time.Minute))
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id = ? AND ended_at IS NULL", 101, 101).
		Updates(map[string]any{"status": enums.AgentPresenceStatusBreak, "last_seen_at": time.Now()}).Error; err != nil {
		t.Fatalf("put assigned agent on break: %v", err)
	}
	service := newConversationDispatchService()
	candidates, _, err := service.pickDispatchCandidates([]int64{1}, 101, nil, time.Now())
	if err != nil || len(candidates) != 1 || candidates[0].profile.UserID != 102 {
		t.Fatalf("pick alternate candidate=%+v err=%v", candidates, err)
	}
	decision := service.selectDispatchDecision(conversation, nil, candidates)
	createRecoveryAgentReply(t, db, assignment, time.Now())

	if _, err := service.tryRecoverWithDecision(assignment, decision, time.Now()); !errors.Is(err, errConversationDispatchConflict) {
		t.Fatalf("expected replied assignment conflict, got %v", err)
	}
	assertRecoveryConversationAssignee(t, conversation.ID, 101)
}

func TestRuleAssignmentRecoveryReleasesUnavailableAssignmentWithoutAlternative(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	conversation, _ := createActiveRuleRecoveryFixture(t, db, 101, time.Now().Add(-time.Minute))
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND ended_at IS NULL", 101).
		Updates(map[string]any{"status": enums.AgentPresenceStatusBreak, "last_seen_at": time.Now()}).Error; err != nil {
		t.Fatalf("put all agents on break: %v", err)
	}

	service := newConversationDispatchService()
	recovered, err := service.RecoverStaleAssignments(10)
	if err != nil {
		t.Fatalf("release unavailable assignment: %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if recovered != 1 || current == nil || current.Status != enums.IMConversationStatusPending || current.CurrentAssigneeID != 0 || current.CurrentTeamID != 1 {
		t.Fatalf("expected pending team pool fallback, recovered=%d current=%+v", recovered, current)
	}
}

func TestRuleAssignmentRecoveryStopsAfterThreeSuccessfulAttempts(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)
	now := time.Now()
	conversation, active := createActiveRuleRecoveryFixture(t, db, 101, now.Add(-time.Minute))
	for i := 0; i < maxRuleAssignmentRecoveryAttempts; i++ {
		finishedAt := now.Add(time.Duration(i-maxRuleAssignmentRecoveryAttempts) * time.Minute)
		if err := db.Create(&models.ConversationAssignment{
			TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, ToUserID: 102,
			AssignType: string(enums.IMAssignmentTypeTransfer), DispatchMode: enums.AgentTeamDispatchModeRule,
			WorkloadWeight: 1, Status: enums.IMAssignmentStatusInactive, CreatedAt: finishedAt.Add(-time.Minute), FinishedAt: &finishedAt,
		}).Error; err != nil {
			t.Fatalf("create recovery history: %v", err)
		}
	}
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id = ? AND ended_at IS NULL", 101, 101).
		Updates(map[string]any{"status": enums.AgentPresenceStatusBreak, "last_seen_at": now}).Error; err != nil {
		t.Fatalf("put assigned agent on break: %v", err)
	}

	service := newConversationDispatchService()
	recovered, err := service.RecoverStaleAssignments(10)
	if err != nil {
		t.Fatalf("recover exhausted assignment: %v", err)
	}
	current := ConversationService.Get(conversation.ID)
	if recovered != 1 || current == nil || current.Status != enums.IMConversationStatusPending || current.CurrentAssigneeID != 0 {
		t.Fatalf("exhausted recovery must enter manual pool, recovered=%d current=%+v", recovered, current)
	}
	if activeCurrent := repositories.ConversationAssignmentRepository.Get(db, active.ID); activeCurrent == nil || activeCurrent.Status != enums.IMAssignmentStatusInactive {
		t.Fatalf("active assignment should be closed at recovery limit, got %+v", activeCurrent)
	}
	if dispatched, err := service.DispatchPendingConversation(current); err != nil || dispatched != nil {
		t.Fatalf("recovery limit must block another automatic assignment, dispatched=%+v err=%v", dispatched, err)
	}
}

func TestRuleDispatchRetryCooldownExcludesRecentlyTriedAgent(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	now := time.Now()
	conversation := &models.Conversation{TenantID: 101, Status: enums.IMConversationStatusPending}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create pending conversation: %v", err)
	}
	if err := db.Create(&models.ConversationAssignment{
		TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, ToUserID: 102,
		AssignType: string(enums.IMAssignmentTypeTransfer), DispatchMode: enums.AgentTeamDispatchModeRule,
		WorkloadWeight: 1, Status: enums.IMAssignmentStatusInactive, CreatedAt: now.Add(-30 * time.Second),
	}).Error; err != nil {
		t.Fatalf("create recent assignment: %v", err)
	}
	candidates := []dispatchCandidate{
		{profile: models.AgentProfile{UserID: 101}},
		{profile: models.AgentProfile{UserID: 102}},
	}
	filtered, err := newConversationDispatchService().filterRuleRetryCooldownCandidates(conversation, candidates, now)
	if err != nil || len(filtered) != 1 || filtered[0].profile.UserID != 101 {
		t.Fatalf("filtered candidates=%+v err=%v", filtered, err)
	}
}

func TestRuleAssignmentRecoveryDetectsEligibilityLoss(t *testing.T) {
	tests := []struct {
		name string
		want string
		edit func(*testing.T, *gorm.DB, *models.ConversationAssignment)
	}{
		{name: "offline", want: "agent_unavailable", edit: func(t *testing.T, db *gorm.DB, _ *models.ConversationAssignment) {
			t.Helper()
			if err := db.Model(&models.AgentPresenceSession{}).Where("tenant_id = ? AND user_id = ?", 101, 101).Update("last_seen_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
				t.Fatalf("stale presence: %v", err)
			}
		}},
		{name: "disabled_account", want: "agent_account_unavailable", edit: func(t *testing.T, db *gorm.DB, _ *models.ConversationAssignment) {
			t.Helper()
			if err := db.Model(&models.User{}).Where("id = ?", 101).Update("status", enums.StatusDisabled).Error; err != nil {
				t.Fatalf("disable user: %v", err)
			}
		}},
		{name: "out_of_shift", want: "out_of_shift", edit: func(t *testing.T, db *gorm.DB, _ *models.ConversationAssignment) {
			t.Helper()
			if err := db.Model(&models.AgentTeamSchedule{}).Where("team_id = ?", 1).Update("excluded_agent_profile_ids", "1").Error; err != nil {
				t.Fatalf("exclude profile from shift: %v", err)
			}
		}},
		{name: "out_of_scope", want: "route_scope_changed", edit: func(t *testing.T, db *gorm.DB, assignment *models.ConversationAssignment) {
			t.Helper()
			if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Update("store_scope_ids", "99").Error; err != nil {
				t.Fatalf("set team scope: %v", err)
			}
			if err := db.Create(&models.ConversationRouteState{TenantID: 101, ConversationID: assignment.ConversationID, StoreID: 88, SessionNo: 1}).Error; err != nil {
				t.Fatalf("create out-of-scope route: %v", err)
			}
		}},
		{name: "permission_loss", want: "reply_permission_lost", edit: func(t *testing.T, db *gorm.DB, _ *models.ConversationAssignment) {
			t.Helper()
			permission := repositories.PermissionRepository.FindOne(db, sqls.NewCnd().Eq("code", constants.PermissionConversationSend.Code))
			if permission == nil {
				t.Fatal("send permission not found")
			}
			if err := db.Where("permission_id = ?", permission.ID).Delete(&models.RolePermission{}).Error; err != nil {
				t.Fatalf("remove reply permission: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupConversationDispatchSquadTestDB(t)
			createDispatchSquadTeamAndAgents(t, db)
			createDispatchSquadSchedule(t, db, 0)
			conversation, assignment := createActiveRuleRecoveryFixture(t, db, 101, time.Now())
			tt.edit(t, db, assignment)
			cause, err := newConversationDispatchService().detectRuleAssignmentRecoveryCause(assignment, conversation, time.Now())
			if err != nil || cause.code != tt.want || !cause.hard {
				t.Fatalf("cause=%+v want=%s err=%v", cause, tt.want, err)
			}
		})
	}
}

func createActiveRuleRecoveryFixture(t *testing.T, db *gorm.DB, userID int64, assignedAt time.Time) (*models.Conversation, *models.ConversationAssignment) {
	t.Helper()
	conversation := &models.Conversation{
		TenantID: 101, Status: enums.IMConversationStatusActive, CurrentTeamID: 1, CurrentAssigneeID: userID,
		DispatchWeight: 1, LastActiveAt: assignedAt, LastMessageAt: assignedAt,
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create active recovery conversation: %v", err)
	}
	assignment := &models.ConversationAssignment{
		TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, ToUserID: userID,
		AssignType: string(enums.IMAssignmentTypeAssign), DispatchMode: enums.AgentTeamDispatchModeRule,
		WorkloadWeight: 1, Status: enums.IMAssignmentStatusActive, CreatedAt: assignedAt,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create active rule assignment: %v", err)
	}
	return conversation, assignment
}

func createRecoveryAgentReply(t *testing.T, db *gorm.DB, assignment *models.ConversationAssignment, at time.Time) {
	t.Helper()
	createRecoveryMessage(t, db, assignment, enums.IMSenderTypeAgent, assignment.ToUserID, "已处理", at, 1)
}

func createRecoveryCustomerMessage(t *testing.T, db *gorm.DB, assignment *models.ConversationAssignment, at time.Time, seqNo int64) *models.Message {
	t.Helper()
	return createRecoveryMessage(t, db, assignment, enums.IMSenderTypeCustomer, 0, "客户继续追问", at, seqNo)
}

func createRecoveryMessage(t *testing.T, db *gorm.DB, assignment *models.ConversationAssignment, senderType enums.IMSenderType, senderID int64, content string, at time.Time, seqNo int64) *models.Message {
	t.Helper()
	message := &models.Message{
		TenantID: assignment.TenantID, ConversationID: assignment.ConversationID, SessionNo: assignment.SessionNo,
		ClientMsgID: fmt.Sprintf("recovery-%s-%d-%d", senderType, assignment.ID, seqNo), SenderType: senderType, SenderID: senderID,
		MessageType: enums.IMMessageTypeText, Content: content, SeqNo: seqNo, SendStatus: enums.IMMessageStatusSent,
		SentAt: &at, AuditFields: models.AuditFields{CreatedAt: at, UpdatedAt: at},
	}
	if err := db.Create(message).Error; err != nil {
		t.Fatalf("create recovery message: %v", err)
	}
	if err := db.Model(&models.Conversation{}).Where("id = ? AND tenant_id = ?", assignment.ConversationID, assignment.TenantID).Updates(map[string]any{
		"last_message_id": message.ID, "last_message_at": at, "last_active_at": at,
	}).Error; err != nil {
		t.Fatalf("update recovery conversation message pointer: %v", err)
	}
	return message
}

func setRecoveryPresenceStatus(t *testing.T, db *gorm.DB, userIDs []int64, status enums.AgentPresenceStatus, at time.Time) {
	t.Helper()
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id IN ? AND ended_at IS NULL", 101, userIDs).
		Updates(map[string]any{"status": status, "last_seen_at": at}).Error; err != nil {
		t.Fatalf("set recovery presence status: %v", err)
	}
}

func assertRecoveryConversationAssignee(t *testing.T, conversationID, userID int64) {
	t.Helper()
	current := ConversationService.Get(conversationID)
	if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != userID {
		t.Fatalf("conversation %d assignee=%d current=%+v", conversationID, userID, current)
	}
}
