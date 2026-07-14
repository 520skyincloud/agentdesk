package migration

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestBackfillAgentOrganizationTenantsIsIdempotentAndPreservesExplicitValues(t *testing.T) {
	db := setupAgentOrganizationTenantBackfillDB(t)
	tenantA := createAgentOrganizationTenant(t, db, "org-a")
	tenantB := createAgentOrganizationTenant(t, db, "org-b")
	userA := createAgentOrganizationUser(t, db, tenantA.ID, "agent-a")
	userB := createAgentOrganizationUser(t, db, tenantB.ID, "agent-b")
	teamA := createAgentOrganizationTeam(t, db, tenantA.ID, "team-a")
	teamB := createAgentOrganizationTeam(t, db, tenantB.ID, "team-b")

	profileA := &models.AgentProfile{UserID: userA.ID, TeamID: teamA.ID, AgentCode: "profile-a", DisplayName: "Profile A", Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	profileB := &models.AgentProfile{TenantID: tenantB.ID, UserID: userB.ID, TeamID: teamB.ID, AgentCode: "profile-b", DisplayName: "Profile B", Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(profileA).Error; err != nil {
		t.Fatalf("create historical profile: %v", err)
	}
	if err := db.Create(profileB).Error; err != nil {
		t.Fatalf("create explicit profile: %v", err)
	}
	squadA := &models.AgentTeamSquad{TeamID: teamA.ID, Name: "squad-a", Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	squadB := &models.AgentTeamSquad{TenantID: tenantB.ID, TeamID: teamB.ID, Name: "squad-b", Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(squadA).Error; err != nil {
		t.Fatalf("create historical squad: %v", err)
	}
	if err := db.Create(squadB).Error; err != nil {
		t.Fatalf("create explicit squad: %v", err)
	}
	memberA := &models.AgentTeamSquadMember{SquadID: squadA.ID, AgentProfileID: profileA.ID, Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	memberB := &models.AgentTeamSquadMember{TenantID: tenantB.ID, SquadID: squadB.ID, AgentProfileID: profileB.ID, Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(memberA).Error; err != nil {
		t.Fatalf("create historical member: %v", err)
	}
	if err := db.Create(memberB).Error; err != nil {
		t.Fatalf("create explicit member: %v", err)
	}
	scheduleA := &models.AgentTeamSchedule{TeamID: teamA.ID, SquadID: squadA.ID, StartAt: time.Now(), EndAt: time.Now().Add(time.Hour), Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	scheduleB := &models.AgentTeamSchedule{TenantID: tenantB.ID, TeamID: teamB.ID, SquadID: squadB.ID, StartAt: time.Now(), EndAt: time.Now().Add(time.Hour), Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(scheduleA).Error; err != nil {
		t.Fatalf("create historical schedule: %v", err)
	}
	if err := db.Create(scheduleB).Error; err != nil {
		t.Fatalf("create explicit schedule: %v", err)
	}

	if err := db.Transaction(backfillAgentOrganizationTenants); err != nil {
		t.Fatalf("backfill agent organization tenants: %v", err)
	}
	if err := db.Transaction(backfillAgentOrganizationTenants); err != nil {
		t.Fatalf("repeat agent organization backfill: %v", err)
	}

	assertAgentOrganizationTenant(t, db, &models.AgentProfile{}, profileA.ID, tenantA.ID)
	assertAgentOrganizationTenant(t, db, &models.AgentProfile{}, profileB.ID, tenantB.ID)
	assertAgentOrganizationTenant(t, db, &models.AgentTeamSquad{}, squadA.ID, tenantA.ID)
	assertAgentOrganizationTenant(t, db, &models.AgentTeamSquad{}, squadB.ID, tenantB.ID)
	assertAgentOrganizationTenant(t, db, &models.AgentTeamSquadMember{}, memberA.ID, tenantA.ID)
	assertAgentOrganizationTenant(t, db, &models.AgentTeamSquadMember{}, memberB.ID, tenantB.ID)
	assertAgentOrganizationTenant(t, db, &models.AgentTeamSchedule{}, scheduleA.ID, tenantA.ID)
	assertAgentOrganizationTenant(t, db, &models.AgentTeamSchedule{}, scheduleB.ID, tenantB.ID)
}

func TestBackfillAgentOrganizationTenantsRejectsProfileParentConflict(t *testing.T) {
	db := setupAgentOrganizationTenantBackfillDB(t)
	tenantA := createAgentOrganizationTenant(t, db, "profile-a")
	tenantB := createAgentOrganizationTenant(t, db, "profile-b")
	user := createAgentOrganizationUser(t, db, tenantA.ID, "profile-user")
	team := createAgentOrganizationTeam(t, db, tenantB.ID, "profile-team")
	profile := &models.AgentProfile{UserID: user.ID, TeamID: team.ID, AgentCode: "conflict-profile", DisplayName: "Conflict", Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create conflicting profile: %v", err)
	}

	err := db.Transaction(backfillAgentOrganizationTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with team tenant") {
		t.Fatalf("backfill error=%v want profile tenant conflict", err)
	}
	assertAgentOrganizationTenant(t, db, &models.AgentProfile{}, profile.ID, 0)
}

func TestBackfillAgentOrganizationTenantsRejectsTeamLeaderConflict(t *testing.T) {
	db := setupAgentOrganizationTenantBackfillDB(t)
	tenantA := createAgentOrganizationTenant(t, db, "leader-a")
	tenantB := createAgentOrganizationTenant(t, db, "leader-b")
	leader := createAgentOrganizationUser(t, db, tenantB.ID, "foreign-leader")
	team := createAgentOrganizationTeam(t, db, tenantA.ID, "leader-team")
	if err := db.Model(team).Update("leader_user_id", leader.ID).Error; err != nil {
		t.Fatalf("assign conflicting leader: %v", err)
	}

	err := db.Transaction(backfillAgentOrganizationTenants)
	if err == nil || !strings.Contains(err.Error(), "leader tenant") {
		t.Fatalf("backfill error=%v want team leader tenant conflict", err)
	}
}

func TestBackfillAgentOrganizationTenantsRejectsCrossTenantMembership(t *testing.T) {
	db := setupAgentOrganizationTenantBackfillDB(t)
	tenantA := createAgentOrganizationTenant(t, db, "member-a")
	tenantB := createAgentOrganizationTenant(t, db, "member-b")
	userA := createAgentOrganizationUser(t, db, tenantA.ID, "member-user-a")
	userB := createAgentOrganizationUser(t, db, tenantB.ID, "member-user-b")
	teamA := createAgentOrganizationTeam(t, db, tenantA.ID, "member-team-a")
	teamB := createAgentOrganizationTeam(t, db, tenantB.ID, "member-team-b")
	profileA := &models.AgentProfile{UserID: userA.ID, TeamID: teamA.ID, AgentCode: "member-profile-a", DisplayName: "A", Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	profileB := &models.AgentProfile{UserID: userB.ID, TeamID: teamB.ID, AgentCode: "member-profile-b", DisplayName: "B", Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(profileA).Error; err != nil {
		t.Fatalf("create profile A: %v", err)
	}
	if err := db.Create(profileB).Error; err != nil {
		t.Fatalf("create profile B: %v", err)
	}
	squad := &models.AgentTeamSquad{TeamID: teamA.ID, Name: "member-squad", Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(squad).Error; err != nil {
		t.Fatalf("create squad: %v", err)
	}
	member := &models.AgentTeamSquadMember{SquadID: squad.ID, AgentProfileID: profileB.ID, Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(member).Error; err != nil {
		t.Fatalf("create cross-tenant member: %v", err)
	}

	err := db.Transaction(backfillAgentOrganizationTenants)
	if err == nil || !strings.Contains(err.Error(), "crosses team or tenant boundary") {
		t.Fatalf("backfill error=%v want member tenant conflict", err)
	}
	assertAgentOrganizationTenant(t, db, &models.AgentProfile{}, profileA.ID, 0)
	assertAgentOrganizationTenant(t, db, &models.AgentTeamSquad{}, squad.ID, 0)
}

func TestBackfillAgentOrganizationTenantsRejectsScheduleSquadConflict(t *testing.T) {
	db := setupAgentOrganizationTenantBackfillDB(t)
	tenant := createAgentOrganizationTenant(t, db, "schedule")
	teamA := createAgentOrganizationTeam(t, db, tenant.ID, "schedule-team-a")
	teamB := createAgentOrganizationTeam(t, db, tenant.ID, "schedule-team-b")
	squad := &models.AgentTeamSquad{TeamID: teamB.ID, Name: "schedule-squad", Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(squad).Error; err != nil {
		t.Fatalf("create schedule squad: %v", err)
	}
	schedule := &models.AgentTeamSchedule{TeamID: teamA.ID, SquadID: squad.ID, StartAt: time.Now(), EndAt: time.Now().Add(time.Hour), Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(schedule).Error; err != nil {
		t.Fatalf("create conflicting schedule: %v", err)
	}

	err := db.Transaction(backfillAgentOrganizationTenants)
	if err == nil || !strings.Contains(err.Error(), "squad crosses team or tenant boundary") {
		t.Fatalf("backfill error=%v want schedule squad conflict", err)
	}
	assertAgentOrganizationTenant(t, db, &models.AgentTeamSchedule{}, schedule.ID, 0)
}

func setupAgentOrganizationTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "agent-organization.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{}, &models.User{}, &models.AgentTeam{}, &models.AgentProfile{},
		&models.AgentTeamSquad{}, &models.AgentTeamSquadMember{}, &models.AgentTeamSchedule{},
	); err != nil {
		t.Fatalf("migrate agent organization tables: %v", err)
	}
	return db
}

func createAgentOrganizationTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	tenant := &models.Tenant{
		TenantCode: code, LegalName: code, ShortName: code, RegistrationType: "test", RegistrationNo: "REG-" + code,
		VerificationStatus: enums.TenantVerificationStatusVerified, Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return tenant
}

func createAgentOrganizationUser(t *testing.T, db *gorm.DB, tenantID int64, username string) *models.User {
	t.Helper()
	user := &models.User{TenantID: tenantID, Username: username, Nickname: username, Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return user
}

func createAgentOrganizationTeam(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.AgentTeam {
	t.Helper()
	team := &models.AgentTeam{TenantID: tenantID, Name: name, Status: enums.StatusOk, AuditFields: agentOrganizationAuditFields()}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create team %s: %v", name, err)
	}
	return team
}

func assertAgentOrganizationTenant(t *testing.T, db *gorm.DB, model any, id, wantTenantID int64) {
	t.Helper()
	var row struct {
		TenantID int64
	}
	if err := db.Model(model).Select("tenant_id").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("read tenant for %T %d: %v", model, id, err)
	}
	if row.TenantID != wantTenantID {
		t.Fatalf("%T %d tenant = %d, want %d", model, id, row.TenantID, wantTenantID)
	}
}

func agentOrganizationAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, UpdatedAt: now}
}
