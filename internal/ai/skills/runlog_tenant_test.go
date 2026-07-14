package skills

import (
	"path/filepath"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestSkillRunLogWriteEnforcesTenantParents(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "skill-run-log-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Conversation{}, &models.AIAgent{}, &models.SkillRunLog{}); err != nil {
		t.Fatalf("auto migrate skill run log: %v", err)
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
	conversationA := models.Conversation{TenantID: 101, CustomerName: "A customer", Status: enums.IMConversationStatusAIServing, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	conversationB := models.Conversation{TenantID: 202, CustomerName: "B customer", Status: enums.IMConversationStatusAIServing, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	agentA := models.AIAgent{TenantID: 101, Name: "A agent", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	agentB := models.AIAgent{TenantID: 202, Name: "B agent", Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	for _, item := range []any{&conversationA, &conversationB, &agentA, &agentB} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create skill run log parent %T: %v", item, err)
		}
	}

	service := newRunLogService()
	valid := service.Build(RuntimeContext{AIAgent: agentA, ConversationID: conversationA.ID, UserMessage: "test"}, nil, nil, nil)
	if err := service.Write(valid); err != nil {
		t.Fatalf("write valid skill run log: %v", err)
	}
	if valid.TenantID != 101 {
		t.Fatalf("skill run log tenant=%d want=101", valid.TenantID)
	}

	crossConversation := *valid
	crossConversation.ID = 0
	crossConversation.ConversationID = conversationB.ID
	if err := service.Write(&crossConversation); err == nil {
		t.Fatal("skill run log accepted a cross-tenant conversation")
	}
	crossAgent := *valid
	crossAgent.ID = 0
	crossAgent.AIAgentID = agentB.ID
	if err := service.Write(&crossAgent); err == nil {
		t.Fatal("skill run log accepted a cross-tenant AI Agent")
	}
}
