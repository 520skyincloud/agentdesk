package event_handlers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestTicketAssignedInAppNotification(t *testing.T) {
	db := setupNotificationEventHandlerTestDB(t)
	createNotificationEventUser(t, db, 11, 101)

	ticket := &models.Ticket{
		TenantID:          101,
		TicketNo:          "TK202604280001",
		Title:             "退款处理",
		Source:            enums.TicketSourceManual,
		Status:            enums.TicketStatusPending,
		CurrentAssigneeID: 11,
		AuditFields: models.AuditFields{
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	if err := repositories.TicketRepository.Create(sqls.DB(), ticket); err != nil {
		t.Fatalf("create ticket error = %v", err)
	}

	if err := handleTicketAssignedInAppNotification(context.Background(), events.TicketAssignedEvent{
		TicketID:   ticket.ID,
		FromUserID: 0,
		ToUserID:   11,
		OperatorID: 1,
		Reason:     "需要人工跟进",
	}); err != nil {
		t.Fatalf("handler error = %v", err)
	}

	list := repositories.NotificationRepository.Find(sqls.DB(), sqls.NewCnd().Eq("recipient_user_id", 11))
	if len(list) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(list))
	}
	got := list[0]
	if got.TenantID != 101 || got.NotificationType != "ticket_assigned" || got.BizType != "ticket" || got.BizID != ticket.ID {
		t.Fatalf("unexpected notification: %+v", got)
	}
	if got.ActionURL != "/dashboard/tickets?ticketId=1" {
		t.Fatalf("unexpected action url: %q", got.ActionURL)
	}
}

func TestConversationAssignedInAppNotification(t *testing.T) {
	db := setupNotificationEventHandlerTestDB(t)
	createNotificationEventUser(t, db, 22, 202)

	conversation := &models.Conversation{
		TenantID:          202,
		CustomerName:      "张三",
		Status:            enums.IMConversationStatusActive,
		CurrentAssigneeID: 22,
		AuditFields: models.AuditFields{
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	if err := repositories.ConversationRepository.Create(sqls.DB(), conversation); err != nil {
		t.Fatalf("create conversation error = %v", err)
	}

	if err := handleConversationAssignedInAppNotification(context.Background(), events.ConversationAssignedEvent{
		ConversationID: conversation.ID,
		FromUserID:     0,
		ToUserID:       22,
		OperatorID:     1,
		Reason:         "自动分配",
		AssignType:     events.ConversationAssignTypeAutoAssign,
	}); err != nil {
		t.Fatalf("handler error = %v", err)
	}

	list := repositories.NotificationRepository.Find(sqls.DB(), sqls.NewCnd().Eq("recipient_user_id", 22))
	if len(list) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(list))
	}
	got := list[0]
	if got.TenantID != 202 || got.NotificationType != "conversation_assigned" || got.BizType != "conversation" || got.BizID != conversation.ID {
		t.Fatalf("unexpected notification: %+v", got)
	}
	if got.ActionURL != "/dashboard/conversations?conversationId=1" {
		t.Fatalf("unexpected action url: %q", got.ActionURL)
	}
}

func TestAssignmentInAppNotificationsRejectCrossTenantRecipients(t *testing.T) {
	db := setupNotificationEventHandlerTestDB(t)
	createNotificationEventUser(t, db, 31, 301)
	createNotificationEventUser(t, db, 32, 302)
	now := time.Now()
	ticket := &models.Ticket{
		TenantID: 301, TicketNo: "TK-CROSS-TENANT", Title: "租户 A 工单",
		Status: enums.TicketStatusPending, CurrentAssigneeID: 31,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := repositories.TicketRepository.Create(sqls.DB(), ticket); err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	conversation := &models.Conversation{
		TenantID: 301, CustomerName: "租户 A 客户", Status: enums.IMConversationStatusActive,
		CurrentAssigneeID: 31, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := repositories.ConversationRepository.Create(sqls.DB(), conversation); err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	if err := handleTicketAssignedInAppNotification(context.Background(), events.TicketAssignedEvent{
		TicketID: ticket.ID, ToUserID: 32, Reason: "错误跨租户事件",
	}); err != nil {
		t.Fatalf("ticket handler error: %v", err)
	}
	if err := handleConversationAssignedInAppNotification(context.Background(), events.ConversationAssignedEvent{
		ConversationID: conversation.ID, ToUserID: 32, Reason: "错误跨租户事件",
	}); err != nil {
		t.Fatalf("conversation handler error: %v", err)
	}

	if count := repositories.NotificationRepository.Count(sqls.DB(), sqls.NewCnd()); count != 0 {
		t.Fatalf("cross-tenant events created %d notifications", count)
	}
}

func setupNotificationEventHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open sqlite error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&models.User{}, &models.Notification{}, &models.Ticket{}, &models.Conversation{}); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	return db
}

func createNotificationEventUser(t *testing.T, db *gorm.DB, id, tenantID int64) {
	t.Helper()
	if err := db.Create(&models.User{
		ID: id, TenantID: tenantID, Username: fmt.Sprintf("notification-event-%d", id), Nickname: "通知用户", Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create notification event user %d: %v", id, err)
	}
}
