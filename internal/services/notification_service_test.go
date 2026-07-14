package services_test

import (
	"strconv"
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestNotificationServiceCreateAndUnreadCount(t *testing.T) {
	db := setupNotificationTestDB(t)
	createNotificationTestUser(t, db, 101, 11)
	createNotificationTestUser(t, db, 102, 22)
	operator101 := notificationTestPrincipal(101, 11)
	operator102 := notificationTestPrincipal(102, 22)

	item, err := services.NotificationService.Create(request.CreateNotificationRequest{
		RecipientUserID:  101,
		Title:            "工单指派提醒",
		Content:          "工单 TK-1 已指派给你",
		NotificationType: "ticket_assigned",
		BizType:          "ticket",
		BizID:            1,
		ActionURL:        "/dashboard/tickets/1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if item.ID == 0 {
		t.Fatalf("expected notification id to be assigned")
	}
	if item.TenantID != 11 || item.RecipientUserID != 101 || item.ReadAt != nil {
		t.Fatalf("unexpected notification: %+v", item)
	}
	if got := services.NotificationService.CountUnread(operator101); got != 1 {
		t.Fatalf("expected unread count 1, got %d", got)
	}
	if got := services.NotificationService.CountUnread(operator102); got != 0 {
		t.Fatalf("expected unread count 0 for another user, got %d", got)
	}
	list, _ := services.NotificationService.FindPageForPrincipal(sqls.NewCnd(), operator101)
	if len(list) != 1 || list[0].ID != item.ID {
		t.Fatalf("tenant user list=%+v want own notification", list)
	}
}

func TestNotificationServiceMarkReadRequiresOwner(t *testing.T) {
	db := setupNotificationTestDB(t)
	createNotificationTestUser(t, db, 201, 31)
	createNotificationTestUser(t, db, 202, 32)
	operator201 := notificationTestPrincipal(201, 31)
	operator202 := notificationTestPrincipal(202, 32)

	item, err := services.NotificationService.Create(request.CreateNotificationRequest{
		RecipientUserID:  201,
		Title:            "会话分配提醒",
		Content:          "会话 #9 已分配给你",
		NotificationType: "conversation_assigned",
		BizType:          "conversation",
		BizID:            9,
		ActionURL:        "/dashboard/conversations?conversationId=9",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := services.NotificationService.MarkRead(item.ID, operator202); err == nil {
		t.Fatalf("expected foreign user mark read to fail")
	}
	if err := services.NotificationService.MarkRead(item.ID, notificationTestPrincipal(201, 999)); err == nil {
		t.Fatalf("expected mismatched tenant context to fail")
	}
	if got := services.NotificationService.CountUnread(operator201); got != 1 {
		t.Fatalf("expected notification to remain unread, got %d", got)
	}
	if err := services.NotificationService.MarkRead(item.ID, operator201); err != nil {
		t.Fatalf("MarkRead() owner error = %v", err)
	}
	if got := services.NotificationService.CountUnread(operator201); got != 0 {
		t.Fatalf("expected unread count 0 after mark read, got %d", got)
	}
}

func TestNotificationServiceMarkAllReadOnlyCurrentUser(t *testing.T) {
	db := setupNotificationTestDB(t)
	createNotificationTestUser(t, db, 301, 41)
	createNotificationTestUser(t, db, 302, 42)

	for _, userID := range []int64{301, 301, 302} {
		if _, err := services.NotificationService.Create(request.CreateNotificationRequest{
			RecipientUserID:  userID,
			Title:            "工单指派提醒",
			Content:          "工单已指派给你",
			NotificationType: "ticket_assigned",
			BizType:          "ticket",
			BizID:            userID,
			ActionURL:        "/dashboard/tickets/1",
		}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	if err := services.NotificationService.MarkAllRead(notificationTestPrincipal(301, 41)); err != nil {
		t.Fatalf("MarkAllRead() error = %v", err)
	}
	if got := services.NotificationService.CountUnread(notificationTestPrincipal(301, 41)); got != 0 {
		t.Fatalf("expected user 301 unread count 0, got %d", got)
	}
	if got := services.NotificationService.CountUnread(notificationTestPrincipal(302, 42)); got != 1 {
		t.Fatalf("expected user 302 unread count 1, got %d", got)
	}
}

func TestNotificationServiceCreateInTenantRejectsForeignRecipient(t *testing.T) {
	db := setupNotificationTestDB(t)
	createNotificationTestUser(t, db, 501, 51)
	createNotificationTestUser(t, db, 502, 52)
	req := request.CreateNotificationRequest{
		RecipientUserID: 502, Title: "跨租户通知", Content: "不应创建",
		NotificationType: "conversation_assigned", BizType: "conversation", BizID: 9,
	}
	if _, err := services.NotificationService.CreateInTenant(req, 51); err == nil {
		t.Fatal("expected foreign recipient to be rejected")
	}
	if count := services.NotificationService.CountUnread(notificationTestPrincipal(502, 52)); count != 0 {
		t.Fatalf("foreign recipient received %d notifications", count)
	}
	req.RecipientUserID = 501
	item, err := services.NotificationService.CreateInTenant(req, 51)
	if err != nil {
		t.Fatalf("same-tenant CreateInTenant() error = %v", err)
	}
	if item.TenantID != 51 || item.RecipientUserID != 501 {
		t.Fatalf("unexpected same-tenant notification: %+v", item)
	}
}

func setupNotificationTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbName := strings.NewReplacer("/", "_", " ", "_", "-", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
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
	if err := db.AutoMigrate(&models.User{}, &models.Notification{}); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	return db
}

func createNotificationTestUser(t *testing.T, db *gorm.DB, id, tenantID int64) {
	t.Helper()
	if err := db.Create(&models.User{
		ID: id, TenantID: tenantID, Username: "notification-user-" + strconv.FormatInt(id, 10), Nickname: "通知用户", Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create notification user %d: %v", id, err)
	}
}

func notificationTestPrincipal(userID, tenantID int64) *dto.AuthPrincipal {
	return &dto.AuthPrincipal{UserID: userID, TenantID: tenantID, ActiveTenantID: tenantID}
}
