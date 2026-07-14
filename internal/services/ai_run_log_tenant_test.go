package services

import (
	"path/filepath"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	paramsx "agent-desk/internal/pkg/httpx/params"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestAgentRunLogServiceEnforcesTenantReadsAndWrites(t *testing.T) {
	fixture := setupAIRunLogTenantFixture(t)
	logA := &models.AgentRunLog{
		TenantID: fixture.conversationA.TenantID, ConversationID: fixture.conversationA.ID,
		MessageID: fixture.messageA.ID, AIAgentID: fixture.agentA.ID, RequestID: "run-log-a", CreatedAt: time.Now(),
	}
	logB := &models.AgentRunLog{
		TenantID: fixture.conversationB.TenantID, ConversationID: fixture.conversationB.ID,
		MessageID: fixture.messageB.ID, AIAgentID: fixture.agentB.ID, RequestID: "run-log-b", CreatedAt: time.Now(),
	}
	for _, item := range []*models.AgentRunLog{logA, logB} {
		if err := AgentRunLogService.Create(item); err != nil {
			t.Fatalf("create tenant run log: %v", err)
		}
	}

	query := aiRunLogTenantPage()
	list, paging := AgentRunLogService.FindPageInTenant(query, fixture.conversationA.TenantID)
	if len(list) != 1 || list[0].ID != logA.ID || paging.Total != 1 {
		t.Fatalf("tenant A logs=%+v paging=%+v", list, paging)
	}
	if AgentRunLogService.GetInTenant(logB.ID, fixture.conversationA.TenantID) != nil {
		t.Fatal("tenant A can read tenant B agent run log")
	}
	if AgentRunLogService.GetInTenant(logB.ID, fixture.conversationB.TenantID) == nil {
		t.Fatal("tenant B cannot read its own agent run log")
	}

	crossTenantConversation := &models.AgentRunLog{
		TenantID: fixture.conversationA.TenantID, ConversationID: fixture.conversationB.ID,
		MessageID: fixture.messageB.ID, AIAgentID: fixture.agentA.ID, CreatedAt: time.Now(),
	}
	if err := AgentRunLogService.Create(crossTenantConversation); err == nil {
		t.Fatal("agent run log accepted a cross-tenant conversation")
	}
	mismatchedMessage := &models.AgentRunLog{
		TenantID: fixture.conversationA.TenantID, ConversationID: fixture.conversationA.ID,
		MessageID: fixture.messageB.ID, AIAgentID: fixture.agentA.ID, CreatedAt: time.Now(),
	}
	if err := AgentRunLogService.Create(mismatchedMessage); err == nil {
		t.Fatal("agent run log accepted a cross-tenant message")
	}
	crossTenantAgent := &models.AgentRunLog{
		TenantID: fixture.conversationA.TenantID, ConversationID: fixture.conversationA.ID,
		MessageID: fixture.messageA.ID, AIAgentID: fixture.agentB.ID, CreatedAt: time.Now(),
	}
	if err := AgentRunLogService.Create(crossTenantAgent); err == nil {
		t.Fatal("agent run log accepted a cross-tenant AI Agent")
	}
}

type aiRunLogTenantFixture struct {
	db            *gorm.DB
	conversationA models.Conversation
	conversationB models.Conversation
	messageA      models.Message
	messageB      models.Message
	agentA        models.AIAgent
	agentB        models.AIAgent
}

func setupAIRunLogTenantFixture(t *testing.T) aiRunLogTenantFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "ai-run-log-runtime.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Conversation{}, &models.Message{}, &models.AIAgent{}, &models.AgentRunLog{}); err != nil {
		t.Fatalf("auto migrate ai run log runtime: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	now := time.Now()
	fixture := aiRunLogTenantFixture{
		db:            db,
		conversationA: models.Conversation{TenantID: 101, CustomerName: "A customer", Status: enums.IMConversationStatusAIServing, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		conversationB: models.Conversation{TenantID: 202, CustomerName: "B customer", Status: enums.IMConversationStatusAIServing, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		agentA:        models.AIAgent{TenantID: 101, Name: "A agent", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		agentB:        models.AIAgent{TenantID: 202, Name: "B agent", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
	}
	for _, item := range []any{&fixture.conversationA, &fixture.conversationB, &fixture.agentA, &fixture.agentB} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create run log parent %T: %v", item, err)
		}
	}
	fixture.messageA = aiRunLogRuntimeMessage(101, fixture.conversationA.ID, now)
	fixture.messageB = aiRunLogRuntimeMessage(202, fixture.conversationB.ID, now)
	for _, item := range []*models.Message{&fixture.messageA, &fixture.messageB} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create run log message: %v", err)
		}
	}
	return fixture
}

func aiRunLogRuntimeMessage(tenantID, conversationID int64, now time.Time) models.Message {
	return models.Message{
		TenantID: tenantID, ConversationID: conversationID,
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "test", SentAt: &now, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
}

func aiRunLogTenantPage() *paramsx.QueryParams {
	cnd := sqls.NewCnd().Asc("id")
	cnd.Paging = &sqls.Paging{Page: 1, Limit: 20}
	return &paramsx.QueryParams{Cnd: *cnd}
}
