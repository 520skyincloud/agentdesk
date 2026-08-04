package services

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
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

func TestWsConfigurationTopicsRespectTenantAndPlatformScope(t *testing.T) {
	svc := newWsService()
	tenantPrincipal := &dto.AuthPrincipal{
		UserID: 1, ActiveTenantID: 101,
		Permissions: []string{constants.PermissionAIConfigView.Code},
	}
	tenantSession := &ClientSession{
		TenantID: 101, Principal: tenantPrincipal, Role: realtimeRoleConfiguration,
	}
	tenantTopics := sliceToSet(svc.defaultTopics(tenantSession))
	if _, ok := tenantTopics[svc.configurationTenantTopic(101)]; !ok {
		t.Fatalf("tenant configuration topic missing: %+v", tenantTopics)
	}
	if _, ok := tenantTopics[realtimeTopicConfigPlatform]; ok {
		t.Fatalf("tenant account received platform configuration topic: %+v", tenantTopics)
	}

	platformPrincipal := &dto.AuthPrincipal{
		UserID: 2, ActiveTenantID: 101, IsPlatformAccount: true,
		Permissions: []string{constants.PermissionAIConfigView.Code},
	}
	platformSession := &ClientSession{
		TenantID: 101, Principal: platformPrincipal, Role: realtimeRoleConfiguration,
	}
	platformTopics := sliceToSet(svc.defaultTopics(platformSession))
	if _, ok := platformTopics[svc.configurationTenantTopic(101)]; ok {
		t.Fatalf("platform account must not subscribe twice through the tenant topic: %+v", platformTopics)
	}
	if _, ok := platformTopics[realtimeTopicConfigPlatform]; !ok {
		t.Fatalf("platform configuration topic missing: %+v", platformTopics)
	}
}

func TestWsConfigurationPermissionDoesNotDependOnConversationAccess(t *testing.T) {
	for _, permission := range []string{
		constants.PermissionAIConfigView.Code,
		constants.PermissionStoreWorkbenchView.Code,
		constants.PermissionKnowledgeBaseView.Code,
		constants.PermissionBillingView.Code,
	} {
		principal := &dto.AuthPrincipal{UserID: 1, Permissions: []string{permission}}
		if !hasAnyRealtimeConfigurationPermission(principal) {
			t.Fatalf("permission %q should allow configuration realtime access", permission)
		}
	}
	if hasAnyRealtimeConfigurationPermission(&dto.AuthPrincipal{
		UserID: 1, Permissions: []string{constants.PermissionConversationView.Code},
	}) {
		t.Fatal("conversation-only permission must not allow configuration realtime access")
	}
}

func TestWsAIConfigurationPayloadContainsNoSensitiveFields(t *testing.T) {
	event := newWsService().newEvent(realtimeTopicConfigPlatform, RealtimeAIConfigurationChangedEvent{
		Type: enums.IMRealtimeEventStoreCredentialChanged,
		Payload: RealtimeAIConfigurationChangedPayload{
			TenantID: 101, StoreID: 202, ProfileID: 303, Revision: 4,
			Status: "active", UpdatedAt: time.Now().Format(time.RFC3339Nano),
		},
	})
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal configuration event: %v", err)
	}
	body := string(raw)
	for _, expected := range []string{
		`"type":"store_model_credential.changed"`,
		`"tenantId":101`,
		`"storeId":202`,
		`"profileId":303`,
		`"revision":4`,
		`"status":"active"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("configuration event missing %s: %s", expected, body)
		}
	}
	lower := strings.ToLower(body)
	for _, forbidden := range []string{"apikey", "prompt", "schema", "cipher", "nonce", "fingerprint"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("configuration event leaked forbidden field %q: %s", forbidden, body)
		}
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
	if _, ok := defaultTopics[svc.dispatchTenantTopic(101)]; ok {
		t.Fatalf("user without handover permission must not receive dispatch topic: %+v", defaultTopics)
	}
	managerPrincipal := *fixture.adminA
	managerPrincipal.Permissions = append(managerPrincipal.Permissions, constants.PermissionConversationHandover.Code)
	managerSession := &ClientSession{TenantID: 101, Principal: &managerPrincipal, Role: realtimeRoleAdmin, Topics: map[string]struct{}{}}
	managerTopics := sliceToSet(svc.defaultTopics(managerSession))
	if _, ok := managerTopics[svc.dispatchTenantTopic(101)]; !ok {
		t.Fatalf("handover manager dispatch topic missing: %+v", managerTopics)
	}
	if _, ok := managerTopics[svc.dispatchTenantTopic(202)]; ok {
		t.Fatalf("foreign tenant dispatch topic leaked: %+v", managerTopics)
	}
	routeTopics := sliceToSet(svc.routeConversationTopics(conversationA))
	if _, ok := routeTopics[svc.adminTenantTopic(101)]; !ok {
		t.Fatalf("unassigned conversation missing tenant route topic: %+v", routeTopics)
	}
	if _, ok := routeTopics[svc.adminTenantTopic(202)]; ok {
		t.Fatalf("unassigned conversation routed to foreign tenant: %+v", routeTopics)
	}
	if _, ok := routeTopics[svc.dispatchTenantTopic(101)]; !ok {
		t.Fatalf("conversation missing tenant dispatch topic: %+v", routeTopics)
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

func TestWsStoreStaffTopicsAreBindingScoped(t *testing.T) {
	db := setupStoreStaffTenantDB(t)
	if err := db.AutoMigrate(&models.Conversation{}, &models.ConversationRouteState{}); err != nil {
		t.Fatalf("migrate conversation fixtures: %v", err)
	}
	store := createStoreStaffTenantStore(t, db, 101, "ws-binding-store")
	staffA := createStoreStaffTenantUser(t, db, 101, "ws-store-staff-a")
	staffB := createStoreStaffTenantUser(t, db, 101, "ws-store-staff-b")
	bindingA := createStoreStaffTenantBinding(t, db, 101, staffA.ID, 0, store.ID)
	bindingB := createStoreStaffTenantBinding(t, db, 101, staffB.ID, 0, store.ID)
	conversationA := &models.Conversation{
		TenantID: 101, StoreID: store.ID, StoreStaffBindingID: bindingA.ID,
		Status: enums.IMConversationStatusAIServing, LastActiveAt: time.Now(), LastMessageAt: time.Now(),
	}
	conversationB := &models.Conversation{
		TenantID: 101, StoreID: store.ID, StoreStaffBindingID: bindingB.ID,
		Status: enums.IMConversationStatusAIServing, LastActiveAt: time.Now(), LastMessageAt: time.Now(),
	}
	if err := db.Create(conversationA).Error; err != nil {
		t.Fatalf("create binding A conversation: %v", err)
	}
	if err := db.Create(conversationB).Error; err != nil {
		t.Fatalf("create binding B conversation: %v", err)
	}
	for _, route := range []*models.ConversationRouteState{
		{TenantID: 101, ConversationID: conversationA.ID, StoreID: store.ID, StoreStaffBindingID: bindingA.ID},
		{TenantID: 101, ConversationID: conversationB.ID, StoreID: store.ID, StoreStaffBindingID: bindingB.ID},
	} {
		if err := db.Create(route).Error; err != nil {
			t.Fatalf("create conversation route: %v", err)
		}
	}

	principal := &dto.AuthPrincipal{
		UserID: staffA.ID, ActiveTenantID: 101,
		Roles:       []string{constants.RoleCodeStoreStaff},
		Permissions: []string{constants.PermissionConversationView.Code},
	}
	svc := newWsService()
	session := &ClientSession{TenantID: 101, Principal: principal, Role: realtimeRoleAdmin}
	defaultTopics := sliceToSet(svc.defaultTopics(session))
	if _, ok := defaultTopics[svc.storeStaffBindingTopic(bindingA.ID)]; !ok {
		t.Fatalf("own binding topic missing: %+v", defaultTopics)
	}
	for _, forbidden := range []string{
		svc.storeStaffBindingTopic(bindingB.ID),
		svc.adminTenantTopic(101),
		svc.adminTopic(staffA.ID),
	} {
		if _, ok := defaultTopics[forbidden]; ok {
			t.Fatalf("store staff received forbidden topic %q: %+v", forbidden, defaultTopics)
		}
	}
	if !svc.canSubscribeConversation(session, conversationA.ID) {
		t.Fatal("store staff should subscribe to its binding conversation")
	}
	if svc.canSubscribeConversation(session, conversationB.ID) {
		t.Fatal("store staff must not subscribe to another binding conversation")
	}
	ownedRouteTopics := sliceToSet(svc.routeConversationTopics(conversationA))
	if _, ok := ownedRouteTopics[svc.storeStaffBindingTopic(bindingA.ID)]; !ok {
		t.Fatalf("own conversation route missing binding topic: %+v", ownedRouteTopics)
	}
	otherRouteTopics := sliceToSet(svc.routeConversationTopics(conversationB))
	if _, ok := otherRouteTopics[svc.storeStaffBindingTopic(bindingA.ID)]; ok {
		t.Fatalf("other conversation routed to binding A: %+v", otherRouteTopics)
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
