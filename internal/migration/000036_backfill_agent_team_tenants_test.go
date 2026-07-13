package migration

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestBackfillAgentTeamTenantsPreservesExplicitTenant(t *testing.T) {
	db := setupAgentTeamTenantBackfillDB(t)
	legacy := createAgentTeamBackfillTenant(t, db, constants.LegacyDefaultTenantCode)
	other := createAgentTeamBackfillTenant(t, db, "explicit-tenant")
	historical := &models.AgentTeam{Name: "Historical", Status: enums.StatusOk, AuditFields: agentTeamBackfillAuditFields()}
	explicit := &models.AgentTeam{TenantID: other.ID, Name: "Explicit", Status: enums.StatusOk, AuditFields: agentTeamBackfillAuditFields()}
	if err := db.Create(historical).Error; err != nil {
		t.Fatalf("create historical team: %v", err)
	}
	if err := db.Create(explicit).Error; err != nil {
		t.Fatalf("create explicit team: %v", err)
	}

	if err := backfillAgentTeamTenants(db); err != nil {
		t.Fatalf("backfill agent teams: %v", err)
	}
	if err := backfillAgentTeamTenants(db); err != nil {
		t.Fatalf("repeat agent team backfill: %v", err)
	}

	var gotHistorical, gotExplicit models.AgentTeam
	if err := db.Take(&gotHistorical, historical.ID).Error; err != nil {
		t.Fatalf("read historical team: %v", err)
	}
	if err := db.Take(&gotExplicit, explicit.ID).Error; err != nil {
		t.Fatalf("read explicit team: %v", err)
	}
	if gotHistorical.TenantID != legacy.ID {
		t.Fatalf("historical team tenant = %d, want %d", gotHistorical.TenantID, legacy.ID)
	}
	if gotExplicit.TenantID != other.ID {
		t.Fatalf("explicit team tenant was overwritten: %d", gotExplicit.TenantID)
	}
}

func TestBackfillAgentTeamTenantsRequiresLegacyTenant(t *testing.T) {
	db := setupAgentTeamTenantBackfillDB(t)
	if err := backfillAgentTeamTenants(db); err == nil {
		t.Fatal("expected missing legacy tenant to stop agent team backfill")
	}
}

func setupAgentTeamTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Tenant{}, &models.AgentTeam{}); err != nil {
		t.Fatalf("migrate tenant and team: %v", err)
	}
	return db
}

func createAgentTeamBackfillTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	tenant := &models.Tenant{
		TenantCode:         code,
		LegalName:          code,
		ShortName:          code,
		RegistrationType:   "test",
		RegistrationNo:     "REG-" + code,
		VerificationStatus: enums.TenantVerificationStatusVerified,
		Status:             enums.StatusOk,
		AuditFields:        agentTeamBackfillAuditFields(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	return tenant
}

func agentTeamBackfillAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, UpdatedAt: now}
}
