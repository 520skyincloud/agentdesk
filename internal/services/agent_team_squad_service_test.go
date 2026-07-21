package services

import (
	"fmt"
	"slices"
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
		&models.Role{},
		&models.UserRole{},
		&models.AgentTeam{},
		&models.AgentProfile{},
		&models.AgentTeamSquad{},
		&models.AgentTeamSquadMember{},
		&models.AgentTeamSchedule{},
	); err != nil {
		t.Fatalf("migrate squad models: %v", err)
	}
	sqls.SetDB(db)
	teamA := &models.AgentTeam{TenantID: 101, Name: "综合客服组A", Status: enums.StatusOk}
	teamB := &models.AgentTeam{TenantID: 202, Name: "综合客服组B", Status: enums.StatusOk}
	if err := db.Create(teamA).Error; err != nil {
		t.Fatalf("create team A: %v", err)
	}
	if err := db.Create(teamB).Error; err != nil {
		t.Fatalf("create team B: %v", err)
	}
	agentRole := &models.Role{Name: "客服", Code: constants.RoleCodeCsUser, Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	if err := db.Create(agentRole).Error; err != nil {
		t.Fatalf("create agent role: %v", err)
	}
	profiles := make([]models.AgentProfile, 0, 3)
	for index, teamID := range []int64{teamA.ID, teamA.ID, teamB.ID} {
		team := teamA
		if teamID == teamB.ID {
			team = teamB
		}
		user := &models.User{TenantID: team.TenantID, Username: fmt.Sprintf("squad-user-%d", index+1), Nickname: "小组客服", Status: enums.StatusOk}
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create user %d: %v", index, err)
		}
		if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: agentRole.ID}).Error; err != nil {
			t.Fatalf("assign agent role %d: %v", index, err)
		}
		profile := models.AgentProfile{TenantID: team.TenantID, UserID: user.ID, TeamID: teamID, AgentCode: fmt.Sprintf("squad-agent-%d", index+1), DisplayName: "小组客服", Status: enums.StatusOk}
		if err := db.Create(&profile).Error; err != nil {
			t.Fatalf("create profile %d: %v", index, err)
		}
		profiles = append(profiles, profile)
	}
	admin := &dto.AuthPrincipal{UserID: 99, Username: "admin", ActiveTenantID: teamA.TenantID, Roles: []string{constants.RoleCodeAdmin}}
	return db, admin, teamA, teamB, profiles
}

func TestBuildAgentProfileModelInheritsTenantAndRejectsCrossTenant(t *testing.T) {
	db, _, teamA, teamB, _ := setupAgentTeamSquadTest(t)
	role := repositories.RoleRepository.GetByCode(db, constants.RoleCodeCsUser)
	user := &models.User{TenantID: teamA.TenantID, Username: "profile-tenant-user", Nickname: "租户客服", Status: enums.StatusOk}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create profile user: %v", err)
	}
	if err := db.Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		t.Fatalf("assign profile role: %v", err)
	}
	req := request.CreateAgentProfileRequest{
		UserID: user.ID, TeamID: teamA.ID, AgentCode: "tenant-profile", DisplayName: "租户客服",
		MaxConcurrentCount: 5,
	}
	profile, err := AgentProfileService.buildProfileModel(0, req)
	if err != nil {
		t.Fatalf("build profile: %v", err)
	}
	if profile.TenantID != teamA.TenantID {
		t.Fatalf("profile tenant = %d, want %d", profile.TenantID, teamA.TenantID)
	}
	req.TeamID = teamB.ID
	if _, err := AgentProfileService.buildProfileModel(0, req); err == nil {
		t.Fatal("expected cross-tenant profile to be rejected")
	}
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
	if squad.TenantID != teamA.TenantID {
		t.Fatalf("squad tenant = %d, want %d", squad.TenantID, teamA.TenantID)
	}
	members := repositories.AgentTeamSquadMemberRepository.Find(db, sqls.NewCnd().Eq("squad_id", squad.ID).Eq("status", enums.StatusOk).Asc("agent_profile_id"))
	if len(members) != 2 {
		t.Fatalf("member count = %d, want 2", len(members))
	}
	for i := range members {
		if members[i].TenantID != teamA.TenantID {
			t.Fatalf("member tenant = %d, want %d", members[i].TenantID, teamA.TenantID)
		}
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
	members = repositories.AgentTeamSquadMemberRepository.Find(db, sqls.NewCnd().Eq("squad_id", squad.ID).Eq("status", enums.StatusOk).Asc("agent_profile_id"))
	if len(members) != 2 || members[0].AgentProfileID != profiles[0].ID || members[1].AgentProfileID != profiles[1].ID {
		t.Fatalf("rejected member replacement changed active members: %+v", members)
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
	schedule := &models.AgentTeamSchedule{TenantID: teamA.TenantID, TeamID: teamA.ID, SquadID: squad.ID, StartAt: time.Now().Add(time.Hour), EndAt: time.Now().Add(2 * time.Hour), Status: enums.StatusOk}
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
	if current := AgentTeamSquadService.Get(squad.ID); current == nil || current.Status != enums.StatusOk || current.Name != squad.Name {
		t.Fatalf("rejected schedule-dependent mutation changed squad: %+v", current)
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

func TestAgentTeamSquadMutationsLockSquadAndParentTeam(t *testing.T) {
	tests := []struct {
		name          string
		wantSquadLock bool
		action        func(t *testing.T, db *gorm.DB, admin *dto.AuthPrincipal, team *models.AgentTeam) error
	}{
		{
			name: "create",
			action: func(t *testing.T, db *gorm.DB, admin *dto.AuthPrincipal, team *models.AgentTeam) error {
				_, err := AgentTeamSquadService.Create(request.CreateAgentTeamSquadRequest{TeamID: team.ID, Name: "新建小组", Status: int(enums.StatusOk)}, admin)
				return err
			},
		},
		{
			name:          "update",
			wantSquadLock: true,
			action: func(t *testing.T, db *gorm.DB, admin *dto.AuthPrincipal, team *models.AgentTeam) error {
				squad := createAgentTeamSquadForLockTest(t, db, team, "待编辑小组")
				return AgentTeamSquadService.Update(request.UpdateAgentTeamSquadRequest{
					ID: squad.ID,
					CreateAgentTeamSquadRequest: request.CreateAgentTeamSquadRequest{
						TeamID: team.ID, Name: "已编辑小组", Status: int(enums.StatusOk),
					},
				}, admin)
			},
		},
		{
			name:          "replace members",
			wantSquadLock: true,
			action: func(t *testing.T, db *gorm.DB, admin *dto.AuthPrincipal, team *models.AgentTeam) error {
				squad := createAgentTeamSquadForLockTest(t, db, team, "待调整成员小组")
				return AgentTeamSquadService.ReplaceMembers(request.ReplaceAgentTeamSquadMembersRequest{SquadID: squad.ID}, admin)
			},
		},
		{
			name:          "delete",
			wantSquadLock: true,
			action: func(t *testing.T, db *gorm.DB, admin *dto.AuthPrincipal, team *models.AgentTeam) error {
				squad := createAgentTeamSquadForLockTest(t, db, team, "待删除小组")
				return AgentTeamSquadService.Delete(squad.ID, admin)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, admin, team, _, _ := setupAgentTeamSquadTest(t)
			teamIDs := make([]int64, 0, 1)
			squadLocked := false
			callbackName := "test:agent-team-squad-locks-" + tt.name
			if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if _, locked := tx.Statement.Clauses["FOR"]; !locked || tx.Statement.Schema == nil {
					return
				}
				switch tx.Statement.Schema.Name {
				case "AgentTeam":
					if item, ok := tx.Statement.Dest.(*models.AgentTeam); ok {
						teamIDs = append(teamIDs, item.ID)
					}
				case "AgentTeamSquad":
					squadLocked = true
				}
			}); err != nil {
				t.Fatalf("register lock callback: %v", err)
			}
			t.Cleanup(func() {
				if err := db.Callback().Query().Remove(callbackName); err != nil {
					t.Errorf("remove lock callback: %v", err)
				}
			})

			if err := tt.action(t, db, admin, team); err != nil {
				t.Fatalf("%s squad: %v", tt.name, err)
			}
			if !slices.Equal(teamIDs, []int64{team.ID}) {
				t.Fatalf("%s team locks = %v, want [%d]", tt.name, teamIDs, team.ID)
			}
			if squadLocked != tt.wantSquadLock {
				t.Fatalf("%s squad lock = %v, want %v", tt.name, squadLocked, tt.wantSquadLock)
			}
		})
	}
}

func createAgentTeamSquadForLockTest(t *testing.T, db *gorm.DB, team *models.AgentTeam, name string) *models.AgentTeamSquad {
	t.Helper()
	squad := &models.AgentTeamSquad{TenantID: team.TenantID, TeamID: team.ID, Name: name, Status: enums.StatusOk}
	if err := db.Create(squad).Error; err != nil {
		t.Fatalf("create squad %s: %v", name, err)
	}
	return squad
}
