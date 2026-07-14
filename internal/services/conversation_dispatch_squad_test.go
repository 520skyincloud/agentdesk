package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestConversationDispatchCandidatesUseWholeTeamSchedule(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	createDispatchSquadSchedule(t, db, 0)

	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, time.Now())
	if err != nil {
		t.Fatalf("pickDispatchCandidates() error = %v", err)
	}
	if len(candidates) != 2 || report.Reason != "ok" {
		t.Fatalf("expected both team agents, got candidates=%+v report=%+v", candidates, report)
	}
	for i := range candidates {
		if candidates[i].squadID != 0 {
			t.Fatalf("expected legacy whole-team squad snapshot 0, got %d", candidates[i].squadID)
		}
	}
}

func TestConversationDispatchCandidatesFilterScheduledSquad(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	squadID := createDispatchSquad(t, db, []int64{2})
	createDispatchSquadSchedule(t, db, squadID)

	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, time.Now())
	if err != nil {
		t.Fatalf("pickDispatchCandidates() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].profile.ID != 2 || candidates[0].squadID != squadID {
		t.Fatalf("expected only scheduled squad member, got candidates=%+v report=%+v", candidates, report)
	}
}

func TestConversationDispatchCandidatesRejectCrossTenantSquadMembership(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	squadID := createDispatchSquad(t, db, nil)
	if err := db.Create(&models.AgentTeamSquadMember{
		TenantID:       202,
		SquadID:        squadID,
		AgentProfileID: 2,
		Status:         enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create cross-tenant squad member error = %v", err)
	}
	createDispatchSquadSchedule(t, db, squadID)

	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, time.Now())
	if err != nil {
		t.Fatalf("pickDispatchCandidates() error = %v", err)
	}
	if len(candidates) != 0 || report.Reason != "no_matched_profile" {
		t.Fatalf("cross-tenant membership must not enter dispatch pool, got candidates=%+v report=%+v", candidates, report)
	}
}

func TestConversationDispatchCandidatesDoNotBroadenEmptyScheduledSquad(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	squadID := createDispatchSquad(t, db, nil)
	createDispatchSquadSchedule(t, db, squadID)

	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, time.Now())
	if err != nil {
		t.Fatalf("pickDispatchCandidates() error = %v", err)
	}
	if len(candidates) != 0 || report.Reason != "no_matched_profile" {
		t.Fatalf("expected empty scheduled squad to remain unassigned, got candidates=%+v report=%+v", candidates, report)
	}
}

func TestConversationDispatchCandidatesDoNotUseDisabledScheduledSquad(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	squadID := createDispatchSquad(t, db, []int64{1, 2})
	createDispatchSquadSchedule(t, db, squadID)
	if err := db.Model(&models.AgentTeamSquad{}).Where("id = ?", squadID).Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable scheduled squad error = %v", err)
	}

	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, 101, nil, time.Now())
	if err != nil {
		t.Fatalf("pickDispatchCandidates() error = %v", err)
	}
	if len(candidates) != 0 || report.Reason != "no_matched_profile" {
		t.Fatalf("expected disabled scheduled squad to remain unassigned, got candidates=%+v report=%+v", candidates, report)
	}
}

func TestConversationAssignmentStoresSquadSnapshot(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	if err := db.Create(&models.Conversation{ID: 10, TenantID: 101, Status: enums.IMConversationStatusPending}).Error; err != nil {
		t.Fatalf("create conversation error = %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 9, Username: "leader"}
	now := time.Now()
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return ConversationAssignmentService.CreateAssignmentWithSquad(ctx, 10, 23, 0, 101, enums.IMAssignmentTypeAssign, "小组自动派发", operator, now)
	})
	if err != nil {
		t.Fatalf("CreateAssignmentWithSquad() error = %v", err)
	}
	assignment := ConversationAssignmentService.Take("conversation_id = ?", 10)
	if assignment == nil || assignment.SquadID != 23 {
		t.Fatalf("expected assignment squad snapshot 23, got %+v", assignment)
	}
}

func TestConversationDispatchManualAssignmentStaysInOwningTeam(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	if err := db.Create(&models.AgentTeam{ID: 2, TenantID: 101, Name: "其他综合客服组", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create other team error = %v", err)
	}
	if err := db.Create(&models.User{ID: 103, TenantID: 101, Username: "agent-c", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create other team user error = %v", err)
	}
	if err := db.Create(&models.AgentProfile{ID: 3, TenantID: 101, UserID: 103, TeamID: 2, AgentCode: "agent-c", DisplayName: "客服 C", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create other team profile error = %v", err)
	}

	conversation := &models.Conversation{TenantID: 101, CurrentTeamID: 1, Status: enums.IMConversationStatusPending}
	operator := &dto.AuthPrincipal{UserID: 9, Username: "admin", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}}
	if _, err := ConversationDispatchWorkbenchService.requireManageableTargetProfile(103, conversation, operator); err == nil || !strings.Contains(err.Error(), "不属于当前会话综合客服组") {
		t.Fatalf("expected cross-team manual assignment rejection, got %v", err)
	}
	if profile, err := ConversationDispatchWorkbenchService.requireManageableTargetProfile(102, conversation, operator); err != nil || profile == nil || profile.TeamID != 1 {
		t.Fatalf("expected same-team manual assignment to remain allowed, profile=%+v err=%v", profile, err)
	}
}

func TestConversationDispatchManualAssignmentRejectsDisabledAccount(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	if err := db.Model(&models.User{}).Where("id = ?", 102).Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatalf("disable target user error = %v", err)
	}

	conversation := &models.Conversation{TenantID: 101, CurrentTeamID: 1, Status: enums.IMConversationStatusPending}
	operator := &dto.AuthPrincipal{UserID: 9, Username: "admin", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}}
	if _, err := ConversationDispatchWorkbenchService.requireManageableTargetProfile(102, conversation, operator); err == nil || !strings.Contains(err.Error(), "账号已停用") {
		t.Fatalf("expected disabled account rejection, got %v", err)
	}

	profile := AgentProfileService.Get(2)
	if profile == nil {
		t.Fatal("expected target profile")
	}
	loads, err := ConversationDispatchWorkbenchService.buildAgentLoads([]models.AgentProfile{*profile}, operator)
	if err != nil {
		t.Fatalf("buildAgentLoads() error = %v", err)
	}
	if len(loads) != 1 || loads[0].available {
		t.Fatalf("disabled account should remain visible but unavailable, got %+v", loads)
	}
}

func setupConversationDispatchSquadTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite error = %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(
		&models.User{},
		&models.AgentTeam{},
		&models.AgentProfile{},
		&models.AgentTeamSquad{},
		&models.AgentTeamSquadMember{},
		&models.AgentTeamSchedule{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.ConversationAssignment{},
	); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	return db
}

func createDispatchSquadTeamAndAgents(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&models.AgentTeam{ID: 1, TenantID: 101, Name: "综合客服组", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create team error = %v", err)
	}
	users := []models.User{
		{ID: 101, TenantID: 101, Username: "agent-a", Status: enums.StatusOk},
		{ID: 102, TenantID: 101, Username: "agent-b", Status: enums.StatusOk},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users error = %v", err)
	}
	profiles := []models.AgentProfile{
		{ID: 1, TenantID: 101, UserID: 101, TeamID: 1, AgentCode: "agent-a", DisplayName: "客服 A", Status: enums.StatusOk, ServiceStatus: enums.ServiceStatusIdle, AutoAssignEnabled: true, MaxConcurrentCount: 10},
		{ID: 2, TenantID: 101, UserID: 102, TeamID: 1, AgentCode: "agent-b", DisplayName: "客服 B", Status: enums.StatusOk, ServiceStatus: enums.ServiceStatusIdle, AutoAssignEnabled: true, MaxConcurrentCount: 10},
	}
	if err := db.Create(&profiles).Error; err != nil {
		t.Fatalf("create profiles error = %v", err)
	}
}

func createDispatchSquad(t *testing.T, db *gorm.DB, memberProfileIDs []int64) int64 {
	t.Helper()
	squad := models.AgentTeamSquad{TenantID: 101, TeamID: 1, Name: "白班小组", Status: enums.StatusOk}
	if err := db.Create(&squad).Error; err != nil {
		t.Fatalf("create squad error = %v", err)
	}
	for _, profileID := range memberProfileIDs {
		member := models.AgentTeamSquadMember{TenantID: 101, SquadID: squad.ID, AgentProfileID: profileID, Status: enums.StatusOk}
		if err := db.Create(&member).Error; err != nil {
			t.Fatalf("create squad member error = %v", err)
		}
	}
	return squad.ID
}

func createDispatchSquadSchedule(t *testing.T, db *gorm.DB, squadID int64) {
	t.Helper()
	now := time.Now()
	schedule := models.AgentTeamSchedule{
		TenantID: 101,
		TeamID:   1,
		SquadID:  squadID,
		StartAt:  now.Add(-time.Hour),
		EndAt:    now.Add(time.Hour),
		Status:   enums.StatusOk,
	}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatalf("create schedule error = %v", err)
	}
}
