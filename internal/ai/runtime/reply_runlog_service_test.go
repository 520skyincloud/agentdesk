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
	if err := db.AutoMigrate(&models.AgentRunLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)

	newReplyRunLogService().Write(replyRunLogInput{
		StartedAt:    time.Now(),
		Message:      models.Message{ID: 22, RequestID: "trace-123", SenderType: enums.IMSenderTypeCustomer, Content: "hello"},
		Conversation: models.Conversation{ID: 11},
		AIAgent:      models.AIAgent{ID: 33, AIConfigID: 44},
		Question:     "hello",
	})

	var item models.AgentRunLog
	if err := db.First(&item).Error; err != nil {
		t.Fatalf("find run log: %v", err)
	}
	if item.RequestID != "trace-123" {
		t.Fatalf("RequestID=%q want %q", item.RequestID, "trace-123")
	}
}
