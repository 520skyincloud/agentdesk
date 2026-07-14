package migration

import (
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

func TestBackfillAIRunLogTenantsUsesRuntimeEvidenceAndIsIdempotent(t *testing.T) {
	db := setupAIRunLogTenantBackfillDB(t)
	legacy := createAIRunLogTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createAIRunLogTenant(t, db, "run-log-a")
	conversation := createAIRunLogConversation(t, db, tenantA.ID, "A conversation")
	agent := createAIRunLogAgent(t, db, tenantA.ID, "A agent")
	message := createAIRunLogMessage(t, db, tenantA.ID, conversation.ID)

	skillByConversation := &models.SkillRunLog{ConversationID: conversation.ID, CreatedAt: time.Now()}
	skillByAgent := &models.SkillRunLog{AIAgentID: agent.ID, CreatedAt: time.Now()}
	legacySkill := &models.SkillRunLog{CreatedAt: time.Now()}
	agentLog := &models.AgentRunLog{ConversationID: conversation.ID, MessageID: message.ID, AIAgentID: agent.ID, CreatedAt: time.Now()}
	for _, item := range []any{skillByConversation, skillByAgent, legacySkill, agentLog} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create run log %T: %v", item, err)
		}
	}

	if err := db.Transaction(backfillAIRunLogTenants); err != nil {
		t.Fatalf("backfill ai run log tenants: %v", err)
	}
	if err := db.Transaction(backfillAIRunLogTenants); err != nil {
		t.Fatalf("repeat ai run log tenant backfill: %v", err)
	}
	assertAIRunLogTenant(t, db, &models.SkillRunLog{}, skillByConversation.ID, tenantA.ID)
	assertAIRunLogTenant(t, db, &models.SkillRunLog{}, skillByAgent.ID, tenantA.ID)
	assertAIRunLogTenant(t, db, &models.SkillRunLog{}, legacySkill.ID, legacy.ID)
	assertAIRunLogTenant(t, db, &models.AgentRunLog{}, agentLog.ID, tenantA.ID)
}

func TestBackfillAIRunLogTenantsRejectsConflictingEvidenceAndRollsBack(t *testing.T) {
	db := setupAIRunLogTenantBackfillDB(t)
	createAIRunLogTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createAIRunLogTenant(t, db, "run-log-conflict-a")
	tenantB := createAIRunLogTenant(t, db, "run-log-conflict-b")
	conversation := createAIRunLogConversation(t, db, tenantA.ID, "A conversation")
	agentB := createAIRunLogAgent(t, db, tenantB.ID, "B agent")
	validLog := &models.SkillRunLog{ConversationID: conversation.ID, CreatedAt: time.Now()}
	conflictingLog := &models.SkillRunLog{ConversationID: conversation.ID, AIAgentID: agentB.ID, CreatedAt: time.Now()}
	for _, item := range []*models.SkillRunLog{validLog, conflictingLog} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create skill run log: %v", err)
		}
	}

	err := db.Transaction(backfillAIRunLogTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with ai agent") {
		t.Fatalf("backfill error=%v want tenant conflict", err)
	}
	assertAIRunLogTenant(t, db, &models.SkillRunLog{}, validLog.ID, 0)
	assertAIRunLogTenant(t, db, &models.SkillRunLog{}, conflictingLog.ID, 0)
}

func TestBackfillAIRunLogTenantsRejectsMissingReferences(t *testing.T) {
	db := setupAIRunLogTenantBackfillDB(t)
	createAIRunLogTenant(t, db, constants.LegacyDefaultTenantCode)
	item := &models.AgentRunLog{MessageID: 999999, CreatedAt: time.Now()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create agent run log: %v", err)
	}

	err := db.Transaction(backfillAIRunLogTenants)
	if err == nil || !strings.Contains(err.Error(), "references missing message") {
		t.Fatalf("backfill error=%v want missing message", err)
	}
	assertAIRunLogTenant(t, db, &models.AgentRunLog{}, item.ID, 0)
}

func TestBackfillAIRunLogTenantsRejectsMessageConversationMismatch(t *testing.T) {
	db := setupAIRunLogTenantBackfillDB(t)
	createAIRunLogTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createAIRunLogTenant(t, db, "run-log-message-mismatch")
	conversationA := createAIRunLogConversation(t, db, tenantA.ID, "Conversation A")
	conversationB := createAIRunLogConversation(t, db, tenantA.ID, "Conversation B")
	message := createAIRunLogMessage(t, db, tenantA.ID, conversationB.ID)
	item := &models.AgentRunLog{ConversationID: conversationA.ID, MessageID: message.ID, CreatedAt: time.Now()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create agent run log: %v", err)
	}

	err := db.Transaction(backfillAIRunLogTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with message") {
		t.Fatalf("backfill error=%v want message conversation conflict", err)
	}
	assertAIRunLogTenant(t, db, &models.AgentRunLog{}, item.ID, 0)
}

func setupAIRunLogTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "ai-run-log-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{},
		&models.Conversation{},
		&models.Message{},
		&models.AIAgent{},
		&models.SkillRunLog{},
		&models.AgentRunLog{},
	); err != nil {
		t.Fatalf("auto migrate ai run log tenant backfill: %v", err)
	}
	return db
}

func createAIRunLogTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	item := &models.Tenant{
		TenantCode: code, LegalName: code, ShortName: code,
		RegistrationType: "test", RegistrationNo: code, Status: enums.StatusOk,
		AuditFields: aiRunLogAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return item
}

func createAIRunLogConversation(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.Conversation {
	t.Helper()
	item := &models.Conversation{TenantID: tenantID, CustomerName: name, Status: enums.IMConversationStatusAIServing, AuditFields: aiRunLogAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	return item
}

func createAIRunLogAgent(t *testing.T, db *gorm.DB, tenantID int64, name string) *models.AIAgent {
	t.Helper()
	item := &models.AIAgent{TenantID: tenantID, Name: name, Status: enums.StatusOk, AuditFields: aiRunLogAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create ai agent: %v", err)
	}
	return item
}

func createAIRunLogMessage(t *testing.T, db *gorm.DB, tenantID, conversationID int64) *models.Message {
	t.Helper()
	now := time.Now()
	item := &models.Message{
		TenantID: tenantID, ConversationID: conversationID,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "test", SentAt: &now, AuditFields: aiRunLogAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}
	return item
}

func assertAIRunLogTenant(t *testing.T, db *gorm.DB, model any, id, want int64) {
	t.Helper()
	var row struct {
		TenantID int64
	}
	if err := db.Model(model).Select("tenant_id").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("read run log %d tenant: %v", id, err)
	}
	if row.TenantID != want {
		t.Fatalf("run log %d tenant=%d want=%d", id, row.TenantID, want)
	}
}

func aiRunLogAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, UpdatedAt: now}
}
