package runtime

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestReplyRunLogStoresRequestID(t *testing.T) {
	dbName := "reply_runlog_trace_test_" + strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite db: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close sqlite db: %v", err)
		}
	})
	if err := db.AutoMigrate(&models.Conversation{}, &models.Message{}, &models.AIAgent{}, &models.AgentRunLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)
	now := time.Now()
	conversation := models.Conversation{ID: 11, TenantID: 101, CustomerName: "runlog customer", Status: enums.IMConversationStatusAIServing, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	message := models.Message{ID: 22, TenantID: 101, ConversationID: conversation.ID, RequestID: "trace-123", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "hello", SentAt: &now, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	agent := models.AIAgent{ID: 33, TenantID: 101, Name: "runlog agent", AIConfigID: 44, Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}}
	for _, item := range []any{&conversation, &message, &agent} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create run log parent %T: %v", item, err)
		}
	}

	newReplyRunLogService().Write(replyRunLogInput{
		StartedAt:    now,
		Message:      message,
		Conversation: conversation,
		AIAgent:      agent,
		Question:     "hello",
	})

	var item models.AgentRunLog
	if err := db.First(&item).Error; err != nil {
		t.Fatalf("find run log: %v", err)
	}
	if item.RequestID != "trace-123" {
		t.Fatalf("RequestID=%q want %q", item.RequestID, "trace-123")
	}
	if item.TenantID != 101 {
		t.Fatalf("TenantID=%d want 101", item.TenantID)
	}
}
