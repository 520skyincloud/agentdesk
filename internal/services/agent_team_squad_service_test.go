package services

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupAgentTeamSquadTest(t *testing.T) (*gorm.DB, *dto.AuthPrincipal, *models.AgentTeam, *models.AgentTeam, []models.AgentProfile) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.AgentTeam{},
		&models.AgentProfile{},
		&models.AgentTeamSquad{},
		&models.AgentTeamSquadMember{},
		&models.AgentTeamSchedule{},
	); err != nil {
		t.Fatalf("migrate squad models: %v", err)
	}
	sqls.SetDB(db)
	teamA := &models.AgentTeam{Name: "综合客服组A", Status: enums.StatusOk}
	teamB := &models.AgentTeam{Name: "综合客服组B", Status: enums.StatusOk}
	if err := db.Create(teamA).Error; err != nil {
		t.Fatalf("create team A: %v", err)
	}
	if err := db.Create(teamB).Error; err != nil {
		t.Fatalf("create team B: %v", err)
	}
	profiles := make([]models.AgentProfile, 0, 3)
	for index, teamID := range []int64{teamA.ID, teamA.ID, teamB.ID} {
		user := &models.User{Username: fmt.Sprintf("squad-user-%d", index+1), Nickname: "小组客服", Status: enums.StatusOk}
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create user %d: %v", index, err)
		}
		profile := models.AgentProfile{UserID: user.ID, TeamID: teamID, AgentCode: fmt.Sprintf("squad-agent-%d", index+1), DisplayName: "小组客服", Status: enums.StatusOk}
		if err := db.Create(&profile).Error; err != nil {
			t.Fatalf("create profile %d: %v", index, err)
		}
		profiles = append(profiles, profile)
	}
	admin := &dto.AuthPrincipal{UserID: 99, Username: "admin", Roles: []string{constants.RoleCodeAdmin}}
	return db, admin, teamA, teamB, profiles
}

func TestAgentTeamSquadMembershipAndValidation(t *testing.T) {
	db, admin, teamA, teamB, profiles := setupAgentTeamSquadTest(t)
	squad, err := AgentTeamSquadService.Create(request.CreateAgentTeamSquadRequest{
		TeamID:       teamA.ID,
		Name:         "客服一组",
		LeaderUserID: profiles[0].UserID,
		MemberIDs:    []int64{profiles[1].ID},
		Status:       int(enums.StatusOk),
	}, admin)
	if err != nil {
		t.Fatalf("create squad: %v", err)
	}
	members := repositories.AgentTeamSquadMemberRepository.Find(db, sqls.NewCnd().Eq("squad_id", squad.ID).Eq("status", enums.StatusOk).Asc("agent_profile_id"))
	if len(members) != 2 {
		t.Fatalf("member count = %d, want 2", len(members))
	}

	if err := AgentTeamSquadService.ReplaceMembers(request.ReplaceAgentTeamSquadMembersRequest{SquadID: squad.ID, AgentProfileIDs: []int64{profiles[1].ID, profiles[1].ID}}, admin); err != nil {
		t.Fatalf("replace members: %v", err)
	}
	members = repositories.AgentTeamSquadMemberRepository.Find(db, sqls.NewCnd().Eq("squad_id", squad.ID).Eq("status", enums.StatusOk))
	if len(members) != 2 {
		t.Fatalf("idempotent member count = %d, want leader + member", len(members))
	}
	secondSquad, err := AgentTeamSquadService.Create(request.CreateAgentTeamSquadRequest{
		TeamID:    teamA.ID,
		Name:      "客服二组",
		MemberIDs: []int64{profiles[1].ID},
		Status:    int(enums.StatusOk),
	}, admin)
	if err != nil {
		t.Fatalf("create second squad with shared member: %v", err)
	}
	sharedMemberships := repositories.AgentTeamSquadMemberRepository.Find(db, sqls.NewCnd().Eq("agent_profile_id", profiles[1].ID).Eq("status", enums.StatusOk))
	if len(sharedMemberships) != 2 || sharedMemberships[0].SquadID == sharedMemberships[1].SquadID || secondSquad.ID == squad.ID {
		t.Fatalf("expected one agent in two squads, got %+v", sharedMemberships)
	}

	if err := AgentTeamSquadService.ReplaceMembers(request.ReplaceAgentTeamSquadMembersRequest{SquadID: squad.ID, AgentProfileIDs: []int64{profiles[2].ID}}, admin); err == nil {
		t.Fatal("expected cross-team member validation error")
	}
	if _, err := AgentTeamSquadService.Create(request.CreateAgentTeamSquadRequest{TeamID: teamA.ID, Name: "客服一组", Status: int(enums.StatusOk)}, admin); err == nil {
		t.Fatal("expected duplicate squad name error")
	}
	if err := AgentTeamSquadService.Update(request.UpdateAgentTeamSquadRequest{
		ID: squad.ID,
		CreateAgentTeamSquadRequest: request.CreateAgentTeamSquadRequest{
			TeamID: teamB.ID, Name: squad.Name, Status: int(enums.StatusOk),
		},
	}, admin); err == nil {
		t.Fatal("expected squad team change rejection")
	}
}

func TestAgentTeamSquadDeleteRequiresScheduleCleanup(t *testing.T) {
	db, admin, teamA, _, _ := setupAgentTeamSquadTest(t)
	squad, err := AgentTeamSquadService.Create(request.CreateAgentTeamSquadRequest{TeamID: teamA.ID, Name: "客服二组", Status: int(enums.StatusOk)}, admin)
	if err != nil {
		t.Fatalf("create squad: %v", err)
	}
	schedule := &models.AgentTeamSchedule{TeamID: teamA.ID, SquadID: squad.ID, StartAt: time.Now().Add(time.Hour), EndAt: time.Now().Add(2 * time.Hour), Status: enums.StatusOk}
	if err := db.Create(schedule).Error; err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if err := AgentTeamSquadService.Delete(squad.ID, admin); err == nil {
		t.Fatal("expected future schedule delete guard")
	}
	if err := AgentTeamSquadService.Update(request.UpdateAgentTeamSquadRequest{
		ID: squad.ID,
		CreateAgentTeamSquadRequest: request.CreateAgentTeamSquadRequest{
			TeamID: teamA.ID, Name: squad.Name, Status: int(enums.StatusDisabled),
		},
	}, admin); err == nil {
		t.Fatal("expected future schedule disable guard")
	}
	if err := db.Model(schedule).Update("status", enums.StatusDeleted).Error; err != nil {
		t.Fatalf("disable schedule: %v", err)
	}
	if err := AgentTeamSquadService.Delete(squad.ID, admin); err != nil {
		t.Fatalf("delete squad after schedule cleanup: %v", err)
	}
	if current := AgentTeamSquadService.Get(squad.ID); current == nil || current.Status != enums.StatusDeleted {
		t.Fatalf("deleted squad = %+v", current)
	}
}
