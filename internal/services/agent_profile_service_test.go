package services

import (
	"slices"
	"strings"
	"testing"

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
	"gorm.io/gorm/schema"
)

func TestTeamCanServeRouteUsesTeamScopeOnly(t *testing.T) {
	team := &models.AgentTeam{
		ID:                     1,
		Name:                   "测试客服组",
		StoreScopeIDs:          "11",
		WxWorkInstanceScopeIDs: "22",
		Status:                 enums.StatusOk,
	}
	tests := []struct {
		name  string
		route *models.ConversationRouteState
		want  bool
	}{
		{name: "team store", route: &models.ConversationRouteState{StoreID: 11}, want: true},
		{name: "team instance", route: &models.ConversationRouteState{WxWorkInstanceID: 22}, want: true},
		{name: "store outside team", route: &models.ConversationRouteState{StoreID: 99}, want: false},
		{name: "instance outside team", route: &models.ConversationRouteState{WxWorkInstanceID: 88}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := teamCanServeRoute(team, tt.route); got != tt.want {
				t.Fatalf("teamCanServeRoute() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTeamCanServeRouteRequiresEnabledTeam(t *testing.T) {
	team := &models.AgentTeam{ID: 1, Name: "停用客服组", Status: enums.StatusDisabled}
	if teamCanServeRoute(team, &models.ConversationRouteState{}) {
		t.Fatal("disabled team must not grant conversation scope")
	}
}

func TestTeamCanServeRouteRejectsEmptyTeamScope(t *testing.T) {
	team := &models.AgentTeam{ID: 1, Name: "未配置范围客服组", Status: enums.StatusOk}
	if teamCanServeRoute(team, &models.ConversationRouteState{StoreID: 99, WxWorkInstanceID: 88}) {
		t.Fatal("team without configured accounts must not grant unrestricted conversation scope")
	}
}

func TestAgentProfileMutationsLockProfileAndParentTeams(t *testing.T) {
	tests := []struct {
		name            string
		wantTeamIDs     []int64
		wantProfileLock bool
		action          func(fixture agentProfileMutationFixture) error
	}{
		{
			name:        "create",
			wantTeamIDs: []int64{2},
			action: func(fixture agentProfileMutationFixture) error {
				_, err := AgentProfileService.CreateAgentProfile(request.CreateAgentProfileRequest{
					UserID: fixture.availableUser.ID, TeamID: fixture.teamB.ID, AgentCode: "CREATE-002", DisplayName: "新增客服",
					MaxConcurrentCount: 5,
				}, fixture.operator)
				return err
			},
		},
		{
			name:            "cross team update",
			wantTeamIDs:     []int64{1, 2},
			wantProfileLock: true,
			action: func(fixture agentProfileMutationFixture) error {
				return AgentProfileService.UpdateAgentProfile(request.UpdateAgentProfileRequest{
					ID: fixture.profile.ID,
					CreateAgentProfileRequest: request.CreateAgentProfileRequest{
						UserID: fixture.profile.UserID, TeamID: fixture.teamA.ID, AgentCode: fixture.profile.AgentCode, DisplayName: "跨组客服",
						MaxConcurrentCount: 5,
					},
				}, fixture.operator)
			},
		},
		{
			name:            "delete",
			wantTeamIDs:     []int64{2},
			wantProfileLock: true,
			action: func(fixture agentProfileMutationFixture) error {
				return AgentProfileService.DeleteAgentProfile(fixture.profile.ID, fixture.operator)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupAgentProfileMutationFixture(t)
			teamIDs := make([]int64, 0, len(tt.wantTeamIDs))
			profileLocked := false
			callbackName := "test:agent-profile-locks-" + strings.ReplaceAll(tt.name, " ", "-")
			if err := fixture.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if _, locked := tx.Statement.Clauses["FOR"]; !locked || tx.Statement.Schema == nil {
					return
				}
				switch tx.Statement.Schema.Name {
				case "AgentProfile":
					profileLocked = true
				case "AgentTeam":
					if item, ok := tx.Statement.Dest.(*models.AgentTeam); ok {
						teamIDs = append(teamIDs, item.ID)
					}
				}
			}); err != nil {
				t.Fatalf("register lock callback: %v", err)
			}
			t.Cleanup(func() {
				if err := fixture.db.Callback().Query().Remove(callbackName); err != nil {
					t.Errorf("remove lock callback: %v", err)
				}
			})

			if err := tt.action(fixture); err != nil {
				t.Fatalf("%s profile: %v", tt.name, err)
			}
			if !slices.Equal(teamIDs, tt.wantTeamIDs) {
				t.Fatalf("%s team lock order = %v, want %v", tt.name, teamIDs, tt.wantTeamIDs)
			}
			if profileLocked != tt.wantProfileLock {
				t.Fatalf("%s profile lock = %v, want %v", tt.name, profileLocked, tt.wantProfileLock)
			}
		})
	}
}

func TestAgentProfileSquadRejectionLeavesProfileUnchanged(t *testing.T) {
	fixture := setupAgentProfileMutationFixture(t)
	squad := &models.AgentTeamSquad{TenantID: fixture.teamB.TenantID, TeamID: fixture.teamB.ID, Name: "白班", Status: enums.StatusOk}
	if err := fixture.db.Create(squad).Error; err != nil {
		t.Fatalf("create squad: %v", err)
	}
	member := &models.AgentTeamSquadMember{
		TenantID: fixture.teamB.TenantID, SquadID: squad.ID, AgentProfileID: fixture.profile.ID, Status: enums.StatusOk,
	}
	if err := fixture.db.Create(member).Error; err != nil {
		t.Fatalf("create squad member: %v", err)
	}

	err := AgentProfileService.UpdateAgentProfile(request.UpdateAgentProfileRequest{
		ID: fixture.profile.ID,
		CreateAgentProfileRequest: request.CreateAgentProfileRequest{
			UserID: fixture.profile.UserID, TeamID: fixture.teamA.ID, AgentCode: fixture.profile.AgentCode, DisplayName: "不应跨组",
			MaxConcurrentCount: 5,
		},
	}, fixture.operator)
	if err == nil {
		t.Fatal("profile with active squad membership must not move teams")
	}
	if err := AgentProfileService.DeleteAgentProfile(fixture.profile.ID, fixture.operator); err == nil {
		t.Fatal("profile with active squad membership must not be deleted")
	}

	current := repositories.AgentProfileRepository.Get(fixture.db, fixture.profile.ID)
	if current == nil || current.TeamID != fixture.teamB.ID || current.DisplayName != fixture.profile.DisplayName {
		t.Fatalf("rejected mutation changed profile: %+v", current)
	}
}

type agentProfileMutationFixture struct {
	db            *gorm.DB
	operator      *dto.AuthPrincipal
	teamA         models.AgentTeam
	teamB         models.AgentTeam
	profile       models.AgentProfile
	availableUser models.User
}

func setupAgentProfileMutationFixture(t *testing.T) agentProfileMutationFixture {
	t.Helper()
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(
		&models.User{}, &models.Role{}, &models.UserRole{}, &models.AgentTeam{}, &models.AgentProfile{},
		&models.AgentTeamSquad{}, &models.AgentTeamSquadMember{},
	); err != nil {
		t.Fatalf("migrate profile mutation fixture: %v", err)
	}
	sqls.SetDB(db)

	fixture := agentProfileMutationFixture{
		db:       db,
		operator: &dto.AuthPrincipal{UserID: 9001, Username: "tenant-admin", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}},
		teamA:    models.AgentTeam{TenantID: 101, Name: "一组", Status: enums.StatusOk},
		teamB:    models.AgentTeam{TenantID: 101, Name: "二组", Status: enums.StatusOk},
	}
	if err := db.Create(&fixture.teamA).Error; err != nil {
		t.Fatalf("create team A: %v", err)
	}
	if err := db.Create(&fixture.teamB).Error; err != nil {
		t.Fatalf("create team B: %v", err)
	}
	assignedUser := models.User{TenantID: 101, Username: "assigned-agent", Status: enums.StatusOk}
	fixture.availableUser = models.User{TenantID: 101, Username: "available-agent", Status: enums.StatusOk}
	if err := db.Create(&assignedUser).Error; err != nil {
		t.Fatalf("create assigned user: %v", err)
	}
	if err := db.Create(&fixture.availableUser).Error; err != nil {
		t.Fatalf("create available user: %v", err)
	}
	agentRole := models.Role{Name: "客服", Code: constants.RoleCodeCsUser, Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	if err := db.Create(&agentRole).Error; err != nil {
		t.Fatalf("create agent role: %v", err)
	}
	userRoles := []models.UserRole{{UserID: assignedUser.ID, RoleID: agentRole.ID}, {UserID: fixture.availableUser.ID, RoleID: agentRole.ID}}
	if err := db.Create(&userRoles).Error; err != nil {
		t.Fatalf("create user roles: %v", err)
	}
	fixture.profile = models.AgentProfile{
		TenantID: 101, UserID: assignedUser.ID, TeamID: fixture.teamB.ID, AgentCode: "ASSIGNED-001", DisplayName: "原客服", Status: enums.StatusOk,
		MaxConcurrentCount: 5,
	}
	if err := db.Create(&fixture.profile).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	return fixture
}
