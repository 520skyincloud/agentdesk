package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
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

	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, nil, time.Now())
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

	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, nil, time.Now())
	if err != nil {
		t.Fatalf("pickDispatchCandidates() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].profile.ID != 2 || candidates[0].squadID != squadID {
		t.Fatalf("expected only scheduled squad member, got candidates=%+v report=%+v", candidates, report)
	}
}

func TestConversationDispatchCandidatesDoNotBroadenEmptyScheduledSquad(t *testing.T) {
	db := setupConversationDispatchSquadTestDB(t)
	createDispatchSquadTeamAndAgents(t, db)
	squadID := createDispatchSquad(t, db, nil)
	createDispatchSquadSchedule(t, db, squadID)

	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{1}, nil, time.Now())
	if err != nil {
		t.Fatalf("pickDispatchCandidates() error = %v", err)
	}
	if len(candidates) != 0 || report.Reason != "no_matched_profile" {
		t.Fatalf("expected empty scheduled squad to remain unassigned, got candidates=%+v report=%+v", candidates, report)
	}
}

func TestConversationAssignmentStoresSquadSnapshot(t *testing.T) {
	setupConversationDispatchSquadTestDB(t)
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
		&models.ConversationAssignment{},
	); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	return db
}

func createDispatchSquadTeamAndAgents(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&models.AgentTeam{ID: 1, Name: "综合客服组", Status: enums.StatusOk}).Error; err != nil {
		t.Fatalf("create team error = %v", err)
	}
	users := []models.User{
		{ID: 101, Username: "agent-a", Status: enums.StatusOk},
		{ID: 102, Username: "agent-b", Status: enums.StatusOk},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users error = %v", err)
	}
	profiles := []models.AgentProfile{
		{ID: 1, UserID: 101, TeamID: 1, AgentCode: "agent-a", DisplayName: "客服 A", Status: enums.StatusOk, ServiceStatus: enums.ServiceStatusIdle, AutoAssignEnabled: true, MaxConcurrentCount: 10},
		{ID: 2, UserID: 102, TeamID: 1, AgentCode: "agent-b", DisplayName: "客服 B", Status: enums.StatusOk, ServiceStatus: enums.ServiceStatusIdle, AutoAssignEnabled: true, MaxConcurrentCount: 10},
	}
	if err := db.Create(&profiles).Error; err != nil {
		t.Fatalf("create profiles error = %v", err)
	}
}

func createDispatchSquad(t *testing.T, db *gorm.DB, memberProfileIDs []int64) int64 {
	t.Helper()
	squad := models.AgentTeamSquad{TeamID: 1, Name: "白班小组", Status: enums.StatusOk}
	if err := db.Create(&squad).Error; err != nil {
		t.Fatalf("create squad error = %v", err)
	}
	for _, profileID := range memberProfileIDs {
		member := models.AgentTeamSquadMember{SquadID: squad.ID, AgentProfileID: profileID, Status: enums.StatusOk}
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
		TeamID:  1,
		SquadID: squadID,
		StartAt: now.Add(-time.Hour),
		EndAt:   now.Add(time.Hour),
		Status:  enums.StatusOk,
	}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatalf("create schedule error = %v", err)
	}
}
