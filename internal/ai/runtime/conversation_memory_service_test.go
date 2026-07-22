package runtime

import (
	"fmt"
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

func TestSummarizeMemoryItemsDoesNotPersistRoomNumberAsStableFact(t *testing.T) {
	stable, openIssues, _, _ := summarizeMemoryItems([]models.Message{
		{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "我在109，厕所太滑摔倒了"},
		{SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "我的车牌皖A12345，开发票需要抬头"},
	})
	if strings.Contains(stable, "109") || strings.Contains(stable, "房号") || strings.Contains(stable, "我在") {
		t.Fatalf("room number should not be persisted as stable fact: %q", stable)
	}
	if !strings.Contains(stable, "车牌") || !strings.Contains(stable, "发票") {
		t.Fatalf("expected non-room stable facts to remain, got %q", stable)
	}
	if !strings.Contains(openIssues, "摔倒") {
		t.Fatalf("expected safety issue to remain in open issues, got %q", openIssues)
	}
}

func TestConversationMemoryUpdatePersistsAndReadsWithinTenant(t *testing.T) {
	db := setupConversationMemoryTestDB(t)
	now := time.Now()
	conversation := models.Conversation{ID: 71, TenantID: 101, CustomerID: 81, Status: enums.IMConversationStatusAIServing}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := db.Create(&models.ConversationRouteState{
		TenantID: 101, ConversationID: conversation.ID, StoreID: 91, WxWorkInstanceID: 92,
	}).Error; err != nil {
		t.Fatalf("create route state: %v", err)
	}
	trigger := models.Message{
		ID: 101, TenantID: 101, ConversationID: conversation.ID, SessionNo: 1, SeqNo: 1, ClientMsgID: "tenant-101-message",
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "我的发票还没开", SentAt: &now,
	}
	foreign := models.Message{
		ID: 102, TenantID: 202, ConversationID: conversation.ID, SessionNo: 1, SeqNo: 2, ClientMsgID: "tenant-202-message",
		SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText,
		Content: "另一家公司消息", SentAt: &now,
	}
	if err := db.Create(&trigger).Error; err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatalf("create foreign message: %v", err)
	}

	service := &conversationMemoryService{}
	if err := service.Update(conversation, trigger); err != nil {
		t.Fatalf("update memory: %v", err)
	}

	var summary models.ConversationSessionSummary
	if err := db.Where("conversation_id = ?", conversation.ID).Take(&summary).Error; err != nil {
		t.Fatalf("load summary: %v", err)
	}
	if summary.TenantID != conversation.TenantID {
		t.Fatalf("summary tenant=%d want %d", summary.TenantID, conversation.TenantID)
	}
	if summary.MessageCount != 1 || summary.LastMessageID != trigger.ID {
		t.Fatalf("summary crossed tenant boundary: %#v", summary)
	}
	if summary.StoreID != 91 || summary.WxWorkInstanceID != 92 {
		t.Fatalf("summary route scope mismatch: %#v", summary)
	}
}

func setupConversationMemoryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Conversation{}, &models.ConversationRouteState{}, &models.Message{}, &models.ConversationSessionSummary{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return db
}
