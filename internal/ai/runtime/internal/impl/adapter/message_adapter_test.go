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

func TestBuildHistoryMessagesExcludesStandaloneOneExchange(t *testing.T) {
	setupAdapterHistoryTestDB(t)
	now := time.Now()
	conversationID := int64(7002)
	tenantID := int64(101)
	items := []models.Message{
		{ID: 1, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-normal-question", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "早餐几点", SeqNo: 1, SentAt: ptrAdapterTime(now)},
		{ID: 2, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-normal-answer", SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "早餐七点开始", SeqNo: 2, SentAt: ptrAdapterTime(now.Add(time.Second))},
		{ID: 3, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-standalone-one", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "1", SeqNo: 3, SentAt: ptrAdapterTime(now.Add(2 * time.Second))},
		{ID: 4, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "ai_reply_faq_one_3_text", SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "入住流程介绍", SeqNo: 4, SentAt: ptrAdapterTime(now.Add(3 * time.Second))},
		{ID: 5, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "ai_reply_faq_one_3_mini_program", SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeMiniProgram, Content: "e秒安心住", SeqNo: 5, SentAt: ptrAdapterTime(now.Add(4 * time.Second))},
		{ID: 6, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-near-one-punctuation", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "1。", SeqNo: 6, SentAt: ptrAdapterTime(now.Add(5 * time.Second))},
		{ID: 7, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-near-one-fullwidth", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "１", SeqNo: 7, SentAt: ptrAdapterTime(now.Add(6 * time.Second))},
		{ID: 8, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-next-question", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "停车场在哪里", SeqNo: 8, SentAt: ptrAdapterTime(now.Add(7 * time.Second))},
		{ID: 9, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "history-current", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "现在能开发票吗", SeqNo: 9, SentAt: ptrAdapterTime(now.Add(8 * time.Second))},
	}
	for _, item := range items {
		if err := sqls.DB().Create(&item).Error; err != nil {
			t.Fatalf("create message %d: %v", item.ID, err)
		}
	}

	history := BuildHistoryMessages(conversationID, 9, tenantID, 20)
	keptIDs := make([]int64, 0, len(history.RawItems))
	for _, item := range history.RawItems {
		keptIDs = append(keptIDs, item.ID)
	}
	wantIDs := []int64{1, 2, 6, 7, 8}
	if len(keptIDs) != len(wantIDs) {
		t.Fatalf("unexpected history ids: got %v want %v", keptIDs, wantIDs)
	}
	for index := range wantIDs {
		if keptIDs[index] != wantIDs[index] {
			t.Fatalf("unexpected history ids: got %v want %v", keptIDs, wantIDs)
		}
	}
	joined := historyText(history)
	for _, expected := range []string{"早餐几点", "早餐七点开始", "1。", "１", "停车场在哪里"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("history should keep %q, got %q", expected, joined)
		}
	}
	for _, excluded := range []string{"入住流程介绍", "e秒安心住"} {
		if strings.Contains(joined, excluded) {
			t.Fatalf("standalone one exchange should exclude %q, got %q", excluded, joined)
		}
	}
}

func TestBuildHistoryMessagesBackfillsLimitAfterStandaloneOneExchange(t *testing.T) {
	setupAdapterHistoryTestDB(t)
	now := time.Now()
	conversationID := int64(7003)
	tenantID := int64(101)
	items := []models.Message{
		{ID: 1, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "normal-1", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "早餐几点", SeqNo: 1, SentAt: ptrAdapterTime(now)},
		{ID: 2, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "normal-2", SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "七点开始", SeqNo: 2, SentAt: ptrAdapterTime(now.Add(time.Second))},
		{ID: 3, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "normal-3", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "停车场在哪", SeqNo: 3, SentAt: ptrAdapterTime(now.Add(2 * time.Second))},
		{ID: 4, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "standalone-one", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "1", SeqNo: 4, SentAt: ptrAdapterTime(now.Add(3 * time.Second))},
		{ID: 5, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "ai_reply_faq_one_4_text", SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "入住流程", SeqNo: 5, SentAt: ptrAdapterTime(now.Add(4 * time.Second))},
		{ID: 6, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "ai_reply_faq_one_4_mini_program", SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeMiniProgram, Content: "入住小程序", SeqNo: 6, SentAt: ptrAdapterTime(now.Add(5 * time.Second))},
		{ID: 7, TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, ClientMsgID: "current", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "能开发票吗", SeqNo: 7, SentAt: ptrAdapterTime(now.Add(6 * time.Second))},
	}
	for _, item := range items {
		if err := sqls.DB().Create(&item).Error; err != nil {
			t.Fatalf("create message %d: %v", item.ID, err)
		}
	}

	history := BuildHistoryMessages(conversationID, 7, tenantID, 2)
	keptIDs := make([]int64, 0, len(history.RawItems))
	for _, item := range history.RawItems {
		keptIDs = append(keptIDs, item.ID)
	}
	wantIDs := []int64{1, 2, 3}
	if len(keptIDs) != len(wantIDs) {
		t.Fatalf("standalone exchange consumed history limit: got %v want %v", keptIDs, wantIDs)
	}
	for index := range wantIDs {
		if keptIDs[index] != wantIDs[index] {
			t.Fatalf("standalone exchange consumed history limit: got %v want %v", keptIDs, wantIDs)
		}
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
