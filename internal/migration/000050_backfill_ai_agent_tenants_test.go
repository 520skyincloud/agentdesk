package migration

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestBackfillAIAgentTenantsUsesCurrentRuntimeEvidenceAndIsIdempotent(t *testing.T) {
	db := setupAIAgentTenantBackfillDB(t)
	legacy := createAIAgentTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createAIAgentTenant(t, db, "ai-agent-a")
	userA := &models.User{TenantID: tenantA.ID, Username: "ai-agent-user-a", Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()}
	if err := db.Create(userA).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	team := &models.AgentTeam{TenantID: tenantA.ID, Name: "A team", Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()}
	knowledge := &models.KnowledgeBase{TenantID: tenantA.ID, Name: "A knowledge", Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()}
	for _, item := range []any{team, knowledge} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create tenant evidence %T: %v", item, err)
		}
	}
	agent := &models.AIAgent{
		Name: "A agent", Status: enums.StatusOk, TeamIDs: " " + stringID(team.ID) + " ", KnowledgeIDs: stringID(knowledge.ID),
		AuditFields: models.AuditFields{CreatedAt: time.Now(), CreateUserID: userA.ID, UpdatedAt: time.Now(), UpdateUserID: userA.ID},
	}
	legacyAgent := &models.AIAgent{Name: "Legacy agent", Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()}
	for _, item := range []*models.AIAgent{agent, legacyAgent} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create ai agent: %v", err)
		}
	}
	channel := &models.Channel{TenantID: tenantA.ID, Name: "A channel", ChannelType: enums.ChannelTypeWeb, ChannelID: "ai-agent-a-channel", AIAgentID: agent.ID, Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()}
	conversation := &models.Conversation{TenantID: tenantA.ID, AIAgentID: agent.ID, CustomerName: "A customer", Status: enums.IMConversationStatusAIServing, AuditFields: aiAgentTenantAuditFields()}
	for _, item := range []any{channel, conversation} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create runtime evidence %T: %v", item, err)
		}
	}

	if err := db.Transaction(backfillAIAgentTenants); err != nil {
		t.Fatalf("backfill ai agent tenants: %v", err)
	}
	if err := db.Transaction(backfillAIAgentTenants); err != nil {
		t.Fatalf("repeat ai agent tenant backfill: %v", err)
	}
	assertAIAgentTenant(t, db, agent.ID, tenantA.ID)
	assertAIAgentTenant(t, db, legacyAgent.ID, legacy.ID)
}

func TestBackfillAIAgentTenantsRejectsSharedAgentAndRollsBack(t *testing.T) {
	db := setupAIAgentTenantBackfillDB(t)
	createAIAgentTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createAIAgentTenant(t, db, "ai-agent-conflict-a")
	tenantB := createAIAgentTenant(t, db, "ai-agent-conflict-b")
	agent := &models.AIAgent{Name: "Shared agent", Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create ai agent: %v", err)
	}
	for _, channel := range []*models.Channel{
		{TenantID: tenantA.ID, Name: "A", ChannelType: enums.ChannelTypeWeb, ChannelID: "ai-agent-conflict-a", AIAgentID: agent.ID, Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()},
		{TenantID: tenantB.ID, Name: "B", ChannelType: enums.ChannelTypeWeb, ChannelID: "ai-agent-conflict-b", AIAgentID: agent.ID, Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()},
	} {
		if err := db.Create(channel).Error; err != nil {
			t.Fatalf("create channel: %v", err)
		}
	}

	err := db.Transaction(backfillAIAgentTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with channel") {
		t.Fatalf("backfill error=%v want shared agent conflict", err)
	}
	assertAIAgentTenant(t, db, agent.ID, 0)
}

func TestBackfillAIAgentTenantsRejectsMissingReferences(t *testing.T) {
	db := setupAIAgentTenantBackfillDB(t)
	createAIAgentTenant(t, db, constants.LegacyDefaultTenantCode)
	agent := &models.AIAgent{Name: "Broken agent", TeamIDs: "999999", Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create ai agent: %v", err)
	}

	err := db.Transaction(backfillAIAgentTenants)
	if err == nil || !strings.Contains(err.Error(), "references missing agent team") {
		t.Fatalf("backfill error=%v want missing team rejection", err)
	}
	assertAIAgentTenant(t, db, agent.ID, 0)
}

func TestBackfillAIAgentTenantsRejectsMissingAgentReference(t *testing.T) {
	db := setupAIAgentTenantBackfillDB(t)
	createAIAgentTenant(t, db, constants.LegacyDefaultTenantCode)
	tenant := createAIAgentTenant(t, db, "ai-agent-missing")
	channel := &models.Channel{TenantID: tenant.ID, Name: "Broken channel", ChannelType: enums.ChannelTypeWeb, ChannelID: "ai-agent-missing-channel", AIAgentID: 999999, Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	err := db.Transaction(backfillAIAgentTenants)
	if err == nil || !strings.Contains(err.Error(), "references missing ai agent") {
		t.Fatalf("backfill error=%v want missing ai agent rejection", err)
	}
}

func setupAIAgentTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "ai-agent-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Tenant{}, &models.User{}, &models.AgentTeam{}, &models.KnowledgeBase{}, &models.AIAgent{}, &models.Channel{}, &models.Conversation{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func createAIAgentTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	item := &models.Tenant{TenantCode: code, LegalName: code, ShortName: code, RegistrationType: "test", RegistrationNo: code, Status: enums.StatusOk, AuditFields: aiAgentTenantAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return item
}

func assertAIAgentTenant(t *testing.T, db *gorm.DB, id, want int64) {
	t.Helper()
	var item models.AIAgent
	if err := db.Take(&item, id).Error; err != nil {
		t.Fatalf("read ai agent %d: %v", id, err)
	}
	if item.TenantID != want {
		t.Fatalf("ai agent %d tenant=%d want=%d", id, item.TenantID, want)
	}
}

func aiAgentTenantAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, UpdatedAt: now}
}

func stringID(id int64) string {
	return fmt.Sprintf("%d", id)
}
