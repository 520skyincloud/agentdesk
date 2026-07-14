package services

import (
	"net/http/httptest"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"

	"github.com/gin-gonic/gin"
)

func TestWsNotificationTopic(t *testing.T) {
	svc := newWsService()
	if got := svc.notificationTopic(123); got != "notification:123" {
		t.Fatalf("expected notification:123, got %q", got)
	}
}

func TestWsDashboardRequiresPermissionAndActiveTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		principal *dto.AuthPrincipal
	}{
		{
			name: "missing permission",
			principal: &dto.AuthPrincipal{
				UserID: 1, ActiveTenantID: 101,
			},
		},
		{
			name: "missing active tenant",
			principal: &dto.AuthPrincipal{
				UserID: 2, Permissions: []string{constants.PermissionConversationView.Code},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest("GET", "/api/ws/dashboard", nil)
			ctx.Set("authPrincipal", tt.principal)

			newWsService().HandleDashboardWS(ctx)

			if recorder.Code != 403 {
				t.Fatalf("status=%d want 403; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestWsTenantTopicsAndConversationSubscriptionIsolation(t *testing.T) {
	fixture := setupConversationRuntimeTenantFixture(t)
	now := time.Now()
	customerA := &models.Customer{TenantID: 101, Name: "tenant-a-customer", Status: enums.StatusOk}
	customerB := &models.Customer{TenantID: 202, Name: "tenant-b-customer", Status: enums.StatusOk}
	if err := fixture.db.Create(customerA).Error; err != nil {
		t.Fatalf("create tenant A customer: %v", err)
	}
	if err := fixture.db.Create(customerB).Error; err != nil {
		t.Fatalf("create tenant B customer: %v", err)
	}
	identityA := &models.CustomerIdentity{TenantID: 101, CustomerID: customerA.ID, ExternalSource: enums.ExternalSourceGuest, ExternalID: "S:shared-user", Status: enums.StatusOk}
	identityB := &models.CustomerIdentity{TenantID: 202, CustomerID: customerB.ID, ExternalSource: enums.ExternalSourceGuest, ExternalID: "S:shared-user", Status: enums.StatusOk}
	if err := fixture.db.Create(identityA).Error; err != nil {
		t.Fatalf("create tenant A identity: %v", err)
	}
	if err := fixture.db.Create(identityB).Error; err != nil {
		t.Fatalf("create tenant B identity: %v", err)
	}
	conversationA := &models.Conversation{TenantID: 101, ChannelID: fixture.channelA.ID, CustomerID: customerA.ID, Status: enums.IMConversationStatusPending, LastActiveAt: now, LastMessageAt: now}
	conversationB := &models.Conversation{TenantID: 202, ChannelID: fixture.channelB.ID, CustomerID: customerB.ID, Status: enums.IMConversationStatusPending, LastActiveAt: now, LastMessageAt: now}
	if err := fixture.db.Create(conversationA).Error; err != nil {
		t.Fatalf("create tenant A conversation: %v", err)
	}
	if err := fixture.db.Create(conversationB).Error; err != nil {
		t.Fatalf("create tenant B conversation: %v", err)
	}

	svc := newWsService()
	sessionA := &ClientSession{TenantID: 101, Principal: fixture.adminA, Role: realtimeRoleAdmin, Topics: map[string]struct{}{}}
	if !svc.canSubscribeConversation(sessionA, conversationA.ID) {
		t.Fatal("tenant A admin should subscribe to tenant A conversation")
	}
	if svc.canSubscribeConversation(sessionA, conversationB.ID) {
		t.Fatal("tenant A admin must not subscribe to tenant B conversation")
	}

	defaultTopics := sliceToSet(svc.defaultTopics(sessionA))
	if _, ok := defaultTopics[svc.adminTenantTopic(101)]; !ok {
		t.Fatalf("tenant admin topic missing: %+v", defaultTopics)
	}
	if _, ok := defaultTopics[svc.adminTenantTopic(202)]; ok {
		t.Fatalf("foreign tenant admin topic leaked: %+v", defaultTopics)
	}
	routeTopics := sliceToSet(svc.routeConversationTopics(conversationA))
	if _, ok := routeTopics[svc.adminTenantTopic(101)]; !ok {
		t.Fatalf("unassigned conversation missing tenant route topic: %+v", routeTopics)
	}
	if _, ok := routeTopics[svc.adminTenantTopic(202)]; ok {
		t.Fatalf("unassigned conversation routed to foreign tenant: %+v", routeTopics)
	}

	guestA := &ClientSession{
		ID: "guest-a", TenantID: 101, External: &openidentity.ExternalUser{ExternalSource: enums.ExternalSourceGuest, ExternalID: "S:shared-user"},
		Role: realtimeRoleUser, Topics: map[string]struct{}{}, Send: make(chan []byte, 1),
	}
	if !svc.canSubscribeConversation(guestA, conversationA.ID) {
		t.Fatal("tenant A guest should subscribe to its own conversation")
	}
	if svc.canSubscribeConversation(guestA, conversationB.ID) {
		t.Fatal("same external id must not subscribe to another tenant conversation")
	}
	svc.manager.Register(guestA, svc.defaultTopics(guestA))
	if !svc.IsGuestOnline(101, "S:shared-user") {
		t.Fatal("tenant A guest should be online")
	}
	if svc.IsGuestOnline(202, "S:shared-user") {
		t.Fatal("same external id must not appear online in tenant B")
	}
}

func TestWsNotificationCreatedEventType(t *testing.T) {
	event := RealtimeNotificationCreatedEvent{
		Payload: RealtimeNotificationCreatedPayload{
			Notification: response.NotificationResponse{ID: 1},
		},
	}
	if got := event.EventType(); got != "notification.created" {
		t.Fatalf("expected notification.created, got %q", got)
	}
	if payload := event.EventPayload(); payload == nil {
		t.Fatalf("expected payload")
	}
}
