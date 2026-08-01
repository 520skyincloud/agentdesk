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
	tenantID := int64(101)
	items := []models.Message{
		{ID: 1, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-1", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "早餐几点", SeqNo: 1, SentAt: ptrAdapterTime(now)},
		{ID: 2, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-2", SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "早餐暂不提供", SeqNo: 2, SentAt: ptrAdapterTime(now.Add(time.Second))},
		{ID: 3, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-3", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "我要办理入住", SeqNo: 3, SentAt: ptrAdapterTime(now.Add(2 * time.Second))},
		{ID: 4, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-4", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "未来才发的定位问题", SeqNo: 4, SentAt: ptrAdapterTime(now.Add(3 * time.Second))},
	}
	for _, item := range items {
		if err := sqls.DB().Create(&item).Error; err != nil {
			t.Fatalf("create message %d: %v", item.ID, err)
		}
	}

	history := BuildHistoryMessages(conversationID, 3, tenantID, 10)
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

func TestBuildHistoryMessagesUsesOnlySameStoreCustomerMemory(t *testing.T) {
	db := setupAdapterHistoryTestDB(t)
	now := time.Now()
	tenantID := int64(101)
	storeID := int64(201)
	customerID := int64(301)
	current := &models.Conversation{
		TenantID: tenantID, StoreID: storeID, StoreStaffBindingID: 401,
		CustomerID: customerID, CustomerName: "当前客户", Status: enums.IMConversationStatusAIServing,
	}
	if err := db.Create(current).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ConversationRouteState{
		TenantID: tenantID, ConversationID: current.ID, StoreID: storeID,
		StoreStaffBindingID: current.StoreStaffBindingID, WxWorkInstanceID: 501, SessionNo: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreCustomerRelation{
		TenantID: tenantID, StoreID: storeID, CustomerID: customerID,
		StableNotes: "偏好高楼层", Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}
	summaries := []models.ConversationSessionSummary{
		{
			TenantID: tenantID, ConversationID: current.ID + 1, SessionNo: 1, StoreID: storeID,
			CustomerID: customerID, StableFacts: "需要安静房间", MessageCount: 6, LastMessageID: 61, Status: enums.StatusOk,
		},
		{
			TenantID: tenantID, ConversationID: current.ID + 2, SessionNo: 1, StoreID: storeID + 1,
			CustomerID: customerID, StableFacts: "其他门店专属信息", MessageCount: 4, LastMessageID: 62, Status: enums.StatusOk,
		},
		{
			TenantID: tenantID, ConversationID: current.ID + 3, SessionNo: 1, StoreID: storeID,
			CustomerID: customerID + 1, StableFacts: "其他客户专属信息", MessageCount: 5, LastMessageID: 63, Status: enums.StatusOk,
		},
	}
	if err := db.Create(&summaries).Error; err != nil {
		t.Fatal(err)
	}
	currentMessage := &models.Message{
		TenantID: tenantID, ConversationID: current.ID, SessionNo: 1,
		ClientMsgID: "store-memory-current", SenderType: enums.IMSenderTypeCustomer,
		MessageType: enums.IMMessageTypeText, Content: "这次想订房", SeqNo: 1, SentAt: ptrAdapterTime(now),
	}
	if err := db.Create(currentMessage).Error; err != nil {
		t.Fatal(err)
	}

	history := BuildHistoryMessages(current.ID, currentMessage.ID, tenantID, 10)
	if history.MemoryMessage == nil || history.MemorySource != "store_customer_memory" {
		t.Fatalf("store customer memory missing: %#v", history)
	}
	memory := history.MemoryMessage.Content
	if !strings.Contains(memory, "偏好高楼层") || !strings.Contains(memory, "需要安静房间") {
		t.Fatalf("same Store customer memory not loaded: %q", memory)
	}
	if strings.Contains(memory, "其他门店专属信息") || strings.Contains(memory, "其他客户专属信息") {
		t.Fatalf("cross-scope memory leaked: %q", memory)
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
	if err := db.AutoMigrate(
		&models.Message{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.ConversationSessionSummary{},
		&models.StoreCustomerRelation{},
	); err != nil {
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
