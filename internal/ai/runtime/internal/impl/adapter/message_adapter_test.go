package adapter

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

func TestBuildHistoryMessagesOnlyUsesMessagesBeforeCurrent(t *testing.T) {
	setupAdapterHistoryTestDB(t)
	now := time.Now()
	conversationID := int64(7001)
	items := []models.Message{
		{ID: 1, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-1", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "早餐几点", SeqNo: 1, SentAt: ptrAdapterTime(now)},
		{ID: 2, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-2", SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "早餐暂不提供", SeqNo: 2, SentAt: ptrAdapterTime(now.Add(time.Second))},
		{ID: 3, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-3", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "我要办理入住", SeqNo: 3, SentAt: ptrAdapterTime(now.Add(2 * time.Second))},
		{ID: 4, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-4", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "未来才发的定位问题", SeqNo: 4, SentAt: ptrAdapterTime(now.Add(3 * time.Second))},
	}
	for _, item := range items {
		if err := sqls.DB().Create(&item).Error; err != nil {
			t.Fatalf("create message %d: %v", item.ID, err)
		}
	}

	history := BuildHistoryMessages(conversationID, 3, 10)
	joined := historyText(history)
	if strings.Contains(joined, "我要办理入住") {
		t.Fatalf("history must not include current message, got %q", joined)
	}
	if strings.Contains(joined, "未来才发的定位问题") {
		t.Fatalf("history must not include messages after current, got %q", joined)
	}
	if !strings.Contains(joined, "早餐几点") || !strings.Contains(joined, "早餐暂不提供") {
		t.Fatalf("history should keep earlier messages, got %q", joined)
	}
}

func setupAdapterHistoryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := "adapter_history_test_" + strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite db: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&models.Message{}, &models.Conversation{}, &models.ConversationRouteState{}, &models.ConversationSessionSummary{}); err != nil {
		t.Fatalf("migrate tables: %v", err)
	}
	sqls.SetDB(db)
	return db
}

func historyText(history HistoryBuildResult) string {
	parts := make([]string, 0, len(history.Messages))
	for _, message := range history.Messages {
		if message != nil {
			parts = append(parts, message.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func ptrAdapterTime(v time.Time) *time.Time {
	return &v
}
