package services

import (
	"path/filepath"
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
	"gorm.io/gorm/schema"
)

type agentOrganizationTenantFixture struct {
	db        *gorm.DB
	adminA    *dto.AuthPrincipal
	adminB    *dto.AuthPrincipal
	teamA     models.AgentTeam
	teamB     models.AgentTeam
	userA     models.User
	userB     models.User
	profileA  models.AgentProfile
	profileB  models.AgentProfile
	squadA    models.AgentTeamSquad
	squadB    models.AgentTeamSquad
	scheduleA models.AgentTeamSchedule
	scheduleB models.AgentTeamSchedule
}

func TestAgentOrganizationTenantIsolationAcrossReads(t *testing.T) {
	fixture := setupAgentOrganizationTenantFixture(t)

	teams := AgentTeamService.FindInTenant(sqls.NewCnd().Asc("id"), fixture.adminA)
	if len(teams) != 1 || teams[0].ID != fixture.teamA.ID {
		t.Fatalf("tenant A teams=%+v want only %d", teams, fixture.teamA.ID)
	}
	if AgentTeamService.GetInTenant(fixture.teamB.ID, fixture.adminA) != nil {
		t.Fatal("tenant A must not read tenant B team detail")
	}

	profileCnd := sqls.NewCnd().Asc("id")
	profileCnd.Paging = &sqls.Paging{Page: 1, Limit: 20}
	profiles, paging := AgentProfileService.FindPageInTenant(profileCnd, fixture.adminA)
	if len(profiles) != 1 || profiles[0].ID != fixture.profileA.ID || paging.Total != 1 {
		t.Fatalf("tenant A profiles=%+v paging=%+v", profiles, paging)
	}
	if AgentProfileService.GetInTenant(fixture.profileB.ID, fixture.adminA) != nil {
		t.Fatal("tenant A must not read tenant B profile detail")
	}

	squads, err := AgentTeamSquadService.ListByTeam(fixture.teamA.ID, fixture.adminA)
	if err != nil || len(squads) != 1 || squads[0].Squad.ID != fixture.squadA.ID {
		t.Fatalf("tenant A squads=%+v error=%v", squads, err)
	}
	if len(squads[0].MemberProfileIDs) != 1 || squads[0].MemberProfileIDs[0] != fixture.profileA.ID {
		t.Fatalf("tenant A squad members=%v", squads[0].MemberProfileIDs)
	}
	if _, err := AgentTeamSquadService.ListByTeam(fixture.teamB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A must not list tenant B squads")
	}

	scheduleCnd := sqls.NewCnd().Asc("id")
	scheduleCnd.Paging = &sqls.Paging{Page: 1, Limit: 20}
	schedules, paging := AgentTeamScheduleService.FindPageInTenant(scheduleCnd, fixture.adminA)
	if len(schedules) != 1 || schedules[0].ID != fixture.scheduleA.ID || paging.Total != 1 {
		t.Fatalf("tenant A schedules=%+v paging=%+v", schedules, paging)
	}
	calendar, err := AgentTeamScheduleService.FindCalendarSchedulesInTenant(request.AgentTeamScheduleCalendarRequest{
		StartAt: fixture.scheduleA.StartAt.Add(-time.Hour).Format(time.DateTime),
		EndAt:   fixture.scheduleA.EndAt.Add(time.Hour).Format(time.DateTime),
	}, fixture.adminA)
	if err != nil || len(calendar) != 1 || calendar[0].ID != fixture.scheduleA.ID {
		t.Fatalf("tenant A calendar=%+v error=%v", calendar, err)
	}

	loads, err := ConversationDispatchWorkbenchService.ListAgentLoads(0, fixture.adminA)
	if err != nil || len(loads) != 1 || loads[0].ProfileID != fixture.profileA.ID || loads[0].Username != fixture.userA.Username {
		t.Fatalf("tenant A agent loads=%+v error=%v", loads, err)
	}
}

func TestAgentOrganizationTenantIsolationRejectsIDTampering(t *testing.T) {
	fixture := setupAgentOrganizationTenantFixture(t)

	if err := AgentTeamService.UpdateAgentTeam(request.UpdateAgentTeamRequest{ID: fixture.teamB.ID, Name: "越权客服组", Status: int(enums.StatusOk)}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not update tenant B team")
	}
	if err := AgentTeamService.DeleteAgentTeam(fixture.teamB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B team")
	}
	if err := AgentProfileService.UpdateAgentProfile(request.UpdateAgentProfileRequest{ID: fixture.profileB.ID}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not update tenant B profile")
	}
	if err := AgentProfileService.DeleteAgentProfile(fixture.profileB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B profile")
	}
	if err := AgentTeamSquadService.Update(request.UpdateAgentTeamSquadRequest{ID: fixture.squadB.ID}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not update tenant B squad")
	}
	if err := AgentTeamSquadService.ReplaceMembers(request.ReplaceAgentTeamSquadMembersRequest{SquadID: fixture.squadB.ID}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not replace tenant B squad members")
	}
	if err := AgentTeamSquadService.Delete(fixture.squadB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B squad")
	}
	if err := AgentTeamScheduleService.UpdateAgentTeamSchedule(request.UpdateAgentTeamScheduleRequest{ID: fixture.scheduleB.ID}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not update tenant B schedule")
	}
	if err := AgentTeamScheduleService.DeleteAgentTeamSchedule(fixture.scheduleB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A must not delete tenant B schedule")
	}

	assertAgentOrganizationTenantBUnchanged(t, fixture)
}

func TestAgentOrganizationRejectsCrossTenantProfileAndStoreStaffUnassignment(t *testing.T) {
	fixture := setupAgentOrganizationTenantFixture(t)
	if _, err := AgentProfileService.buildProfileModel(0, request.CreateAgentProfileRequest{
		UserID: fixture.userB.ID, TeamID: fixture.teamA.ID, AgentCode: "cross-tenant", DisplayName: "越权客服",
	}); err == nil {
		t.Fatal("tenant B user must not be combined with tenant A team")
	}
	if err := AgentTeamService.BindStoreStaffUser(fixture.userB.ID, 0, fixture.adminA); err == nil {
		t.Fatal("tenant A must not unassign tenant B store staff from a team")
	}
	binding := repositories.StoreStaffBindingRepository.Take(fixture.db, "user_id = ?", fixture.userB.ID)
	if binding == nil || binding.AgentTeamID != fixture.teamB.ID {
		t.Fatalf("tenant B store staff binding changed: %+v", binding)
	}
}

func TestAgentOrganizationRepositoriesKeepTenantInFinalWritePredicate(t *testing.T) {
	fixture := setupAgentOrganizationTenantFixture(t)
	if err := repositories.AgentTeamRepository.UpdatesInTenant(fixture.db, fixture.teamB.ID, fixture.teamA.TenantID, map[string]any{"name": "越权更新"}); err != nil {
		t.Fatalf("scoped team update: %v", err)
	}
	if err := repositories.AgentProfileRepository.DeleteInTenant(fixture.db, fixture.profileB.ID, fixture.profileA.TenantID); err != nil {
		t.Fatalf("scoped profile delete: %v", err)
	}
	if err := repositories.AgentTeamSquadRepository.UpdatesInTenant(fixture.db, fixture.squadB.ID, fixture.squadA.TenantID, map[string]any{"name": "越权更新"}); err != nil {
		t.Fatalf("scoped squad update: %v", err)
	}
	if err := repositories.AgentTeamScheduleRepository.DeleteInTenant(fixture.db, fixture.scheduleB.ID, fixture.scheduleA.TenantID); err != nil {
		t.Fatalf("scoped schedule delete: %v", err)
	}
	assertAgentOrganizationTenantBUnchanged(t, fixture)
}

func TestAgentOrganizationRequiresActiveTenant(t *testing.T) {
	fixture := setupAgentOrganizationTenantFixture(t)
	withoutTenant := *fixture.adminA
	withoutTenant.ActiveTenantID = 0
	if AgentTeamScopeService.CanManageTeam(&withoutTenant, fixture.teamA.ID) {
		t.Fatal("platform admin without active tenant must not manage a team")
	}
	if list := AgentTeamService.FindInTenant(sqls.NewCnd(), &withoutTenant); len(list) != 0 {
		t.Fatalf("teams without tenant=%+v want empty", list)
	}
	if list := AgentProfileService.FindInTenant(sqls.NewCnd(), &withoutTenant); len(list) != 0 {
		t.Fatalf("profiles without tenant=%+v want empty", list)
	}
	if _, err := AgentTeamScheduleService.FindCalendarSchedulesInTenant(request.AgentTeamScheduleCalendarRequest{
		StartAt: fixture.scheduleA.StartAt.Add(-time.Hour).Format(time.DateTime),
		EndAt:   fixture.scheduleA.EndAt.Add(time.Hour).Format(time.DateTime),
	}, &withoutTenant); err == nil {
		t.Fatal("calendar without active tenant must fail")
	}
}

func setupAgentOrganizationTenantFixture(t *testing.T) agentOrganizationTenantFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "agent-organization.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Role{}, &models.UserRole{}, &models.AgentTeam{}, &models.AgentProfile{},
		&models.AgentTeamSquad{}, &models.AgentTeamSquadMember{}, &models.AgentTeamSchedule{}, &models.Conversation{},
		&models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{},
	); err != nil {
		t.Fatalf("migrate agent organization: %v", err)
	}
	sqls.SetDB(db)

	fixture := agentOrganizationTenantFixture{
		db:     db,
		adminA: &dto.AuthPrincipal{UserID: 9001, Username: "admin-a", ActiveTenantID: 101, Roles: []string{constants.RoleCodeAdmin}},
		adminB: &dto.AuthPrincipal{UserID: 9002, Username: "admin-b", ActiveTenantID: 202, Roles: []string{constants.RoleCodeAdmin}},
		teamA:  models.AgentTeam{TenantID: 101, Name: "A公司综合客服组", Status: enums.StatusOk},
		teamB:  models.AgentTeam{TenantID: 202, Name: "B公司综合客服组", Status: enums.StatusOk},
		userA:  models.User{TenantID: 101, Username: "tenant-a-agent", Nickname: "A客服", Status: enums.StatusOk},
		userB:  models.User{TenantID: 202, Username: "tenant-b-agent", Nickname: "B客服", Status: enums.StatusOk},
	}
	if err := db.Create(&fixture.teamA).Error; err != nil {
		t.Fatalf("create team A: %v", err)
	}
	if err := db.Create(&fixture.teamB).Error; err != nil {
		t.Fatalf("create team B: %v", err)
	}
	if err := db.Create(&fixture.userA).Error; err != nil {
		t.Fatalf("create user A: %v", err)
	}
	if err := db.Create(&fixture.userB).Error; err != nil {
		t.Fatalf("create user B: %v", err)
	}
	agentRole := models.Role{Name: "客服", Code: constants.RoleCodeCsUser, Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	storeStaffRole := models.Role{Name: "门店员工", Code: constants.RoleCodeStoreStaff, Scope: constants.RoleScopeTenant, Status: enums.StatusOk}
	if err := db.Create(&agentRole).Error; err != nil {
		t.Fatalf("create agent role: %v", err)
	}
	if err := db.Create(&storeStaffRole).Error; err != nil {
		t.Fatalf("create store staff role: %v", err)
	}
	userRoles := []models.UserRole{
		{UserID: fixture.userA.ID, RoleID: agentRole.ID},
		{UserID: fixture.userB.ID, RoleID: agentRole.ID},
		{UserID: fixture.userB.ID, RoleID: storeStaffRole.ID},
	}
	if err := db.Create(&userRoles).Error; err != nil {
		t.Fatalf("create user roles: %v", err)
	}
	storeStaffBinding := models.StoreStaffBinding{
		UserID: fixture.userB.ID, AgentTeamID: fixture.teamB.ID, CompanyID: 202, StoreID: 2202, Status: enums.StatusOk,
	}
	if err := db.Create(&storeStaffBinding).Error; err != nil {
		t.Fatalf("create store staff binding: %v", err)
	}
	fixture.profileA = models.AgentProfile{TenantID: 101, UserID: fixture.userA.ID, TeamID: fixture.teamA.ID, AgentCode: "tenant-a-code", DisplayName: "A客服", Status: enums.StatusOk, MaxConcurrentCount: 10, AutoAssignEnabled: true}
	fixture.profileB = models.AgentProfile{TenantID: 202, UserID: fixture.userB.ID, TeamID: fixture.teamB.ID, AgentCode: "tenant-b-code", DisplayName: "B客服", Status: enums.StatusOk, MaxConcurrentCount: 10, AutoAssignEnabled: true}
	if err := db.Create(&fixture.profileA).Error; err != nil {
		t.Fatalf("create profile A: %v", err)
	}
	if err := db.Create(&fixture.profileB).Error; err != nil {
		t.Fatalf("create profile B: %v", err)
	}
	fixture.squadA = models.AgentTeamSquad{TenantID: 101, TeamID: fixture.teamA.ID, Name: "A白班", Status: enums.StatusOk}
	fixture.squadB = models.AgentTeamSquad{TenantID: 202, TeamID: fixture.teamB.ID, Name: "B白班", Status: enums.StatusOk}
	if err := db.Create(&fixture.squadA).Error; err != nil {
		t.Fatalf("create squad A: %v", err)
	}
	if err := db.Create(&fixture.squadB).Error; err != nil {
		t.Fatalf("create squad B: %v", err)
	}
	members := []models.AgentTeamSquadMember{
		{TenantID: 101, SquadID: fixture.squadA.ID, AgentProfileID: fixture.profileA.ID, Status: enums.StatusOk},
		{TenantID: 202, SquadID: fixture.squadB.ID, AgentProfileID: fixture.profileB.ID, Status: enums.StatusOk},
		{TenantID: 202, SquadID: fixture.squadA.ID, AgentProfileID: fixture.profileB.ID, Status: enums.StatusOk},
	}
	if err := db.Create(&members).Error; err != nil {
		t.Fatalf("create squad members: %v", err)
	}
	tomorrow := time.Now().AddDate(0, 0, 1)
	startAt := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 9, 0, 0, 0, time.Local)
	fixture.scheduleA = models.AgentTeamSchedule{TenantID: 101, TeamID: fixture.teamA.ID, SquadID: fixture.squadA.ID, StartAt: startAt, EndAt: startAt.Add(8 * time.Hour), Status: enums.StatusOk, Remark: "A排班"}
	fixture.scheduleB = models.AgentTeamSchedule{TenantID: 202, TeamID: fixture.teamB.ID, SquadID: fixture.squadB.ID, StartAt: startAt, EndAt: startAt.Add(8 * time.Hour), Status: enums.StatusOk, Remark: "B排班"}
	if err := db.Create(&fixture.scheduleA).Error; err != nil {
		t.Fatalf("create schedule A: %v", err)
	}
	if err := db.Create(&fixture.scheduleB).Error; err != nil {
		t.Fatalf("create schedule B: %v", err)
	}
	return fixture
}

func assertAgentOrganizationTenantBUnchanged(t *testing.T, fixture agentOrganizationTenantFixture) {
	t.Helper()
	if team := repositories.AgentTeamRepository.Get(fixture.db, fixture.teamB.ID); team == nil || team.Name != fixture.teamB.Name || team.Status != enums.StatusOk {
		t.Fatalf("tenant B team changed: %+v", team)
	}
	if profile := repositories.AgentProfileRepository.Get(fixture.db, fixture.profileB.ID); profile == nil || profile.DisplayName != fixture.profileB.DisplayName {
		t.Fatalf("tenant B profile changed: %+v", profile)
	}
	if squad := repositories.AgentTeamSquadRepository.Get(fixture.db, fixture.squadB.ID); squad == nil || squad.Name != fixture.squadB.Name || squad.Status != enums.StatusOk {
		t.Fatalf("tenant B squad changed: %+v", squad)
	}
	if schedule := repositories.AgentTeamScheduleRepository.Get(fixture.db, fixture.scheduleB.ID); schedule == nil || schedule.Remark != fixture.scheduleB.Remark {
		t.Fatalf("tenant B schedule changed: %+v", schedule)
	}
}
