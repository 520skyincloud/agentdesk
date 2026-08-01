package services

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/web"
)

type stubArrivalBindingMessageSender struct {
	calls        int
	tenantID     int64
	messageID    int64
	clientMsgID  string
	clientMsgIDs []string
	content      string
	payload      string
	err          error
}

func (s *stubArrivalBindingMessageSender) SendSystemOutboundMessage(
	conversationID int64,
	clientMsgID string,
	messageType enums.IMMessageType,
	content, payload, requestID string,
) (*models.Message, error) {
	s.calls++
	s.clientMsgID = clientMsgID
	s.clientMsgIDs = append(s.clientMsgIDs, clientMsgID)
	s.content = content
	s.payload = payload
	if s.err != nil {
		return nil, s.err
	}
	return &models.Message{
		ID:             s.messageID,
		TenantID:       s.tenantID,
		ConversationID: conversationID,
		SenderType:     enums.IMSenderTypeSystem,
		MessageType:    messageType,
		Content:        content,
		Payload:        payload,
	}, nil
}

type staticArrivalBindingFixture struct {
	arrivalLinkTestFixture
	instance     *models.WxWorkProtocolInstance
	customer     *models.Customer
	conversation *models.Conversation
	sender       *stubArrivalBindingMessageSender
	service      *arrivalBindingTicketService
}

func TestArrivalStaticProviderConfigurationDoesNotRequireSuite(t *testing.T) {
	dataKey := "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="
	cfg := config.ArrivalConfig{
		Enabled:              true,
		PublicBaseURL:        "https://weibao.example.test",
		MiniProgramAppID:     "wx-arrival-test",
		MiniProgramAppSecret: "mini-program-secret",
		SessionSecret:        "arrival-session-secret-at-least-32-bytes",
		IdentityHMACKey:      "arrival-identity-secret-at-least-32-bytes",
		DataMasterKey:        dataKey,
		DataMasterKeyID:      "arrival-test-key-v1",
		ContactProvider:      string(enums.ArrivalContactProviderModeStaticPluginTicket),
	}
	config.SetCurrent(&config.Config{Arrival: cfg})
	t.Cleanup(func() { config.SetCurrent(&config.Config{}) })

	if err := ArrivalLinkService.ValidateConfiguration(); err != nil {
		t.Fatalf("static provider rejected without Suite configuration: %v", err)
	}
	cfg.ContactProvider = string(enums.ArrivalContactProviderModeContactWay)
	config.SetCurrent(&config.Config{Arrival: cfg})
	if err := ArrivalLinkService.ValidateConfiguration(); errorCode(err) != 2061 {
		t.Fatalf("legacy provider without Suite error=%v code=%d want=2061", err, errorCode(err))
	}
}

func TestArrivalStaticBootstrapReturnsPlugIDWithoutCallingLegacyProviders(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	instance := configureStaticArrivalConnection(t, fixture)
	cfg := config.Current().Arrival
	cfg.ContactProvider = string(enums.ArrivalContactProviderModeStaticPluginTicket)
	cfg.WeComSuiteID = ""
	cfg.WeComSuiteSecret = ""
	cfg.WeComProviderCallbackToken = ""
	cfg.WeComProviderEncodingAESKey = ""
	config.SetCurrent(&config.Config{Arrival: cfg})

	login := &stubArrivalLoginExchanger{
		result: &weChatCodeSessionResult{OpenID: "openid-a", UnionID: "union-a"},
	}
	legacyProvider := &stubArrivalContactWayCreator{}
	qrBuilder := &stubArrivalQRCodeBuilder{}
	service := &arrivalLinkService{
		loginExchanger:    login,
		contactWayCreator: legacyProvider,
		qrCodeBuilder:     qrBuilder,
	}
	req := request.ArrivalBootstrapRequest{
		SchemaVersion: arrivalScanInputVersion,
		LoginCode:     "static-login-code",
		Scene:         fixture.connection.StoreScene,
		ScanEventID:   "static-scan-idempotent",
	}
	first, err := service.Bootstrap(req)
	if err != nil {
		t.Fatalf("static bootstrap: %v", err)
	}
	if first.SchemaVersion != arrivalScanResultVersion ||
		first.ContactWay.Mode != string(enums.ArrivalContactWayModePluginButton) ||
		!first.ContactWay.Available ||
		first.ContactWay.PlugID != fixture.connection.StaticContactPlugID ||
		first.ContactWay.QRCodeURL != "" {
		t.Fatalf("unexpected static bootstrap result: %#v", first)
	}
	if legacyProvider.calls != 0 || qrBuilder.calls != 0 {
		t.Fatalf("static provider called legacy providers: contact=%d qr=%d", legacyProvider.calls, qrBuilder.calls)
	}
	if instance.ID != fixture.connection.WxWorkProtocolInstanceID {
		t.Fatal("static connection did not retain the configured protocol instance")
	}
	second, err := service.Bootstrap(req)
	if err != nil {
		t.Fatalf("repeat static bootstrap: %v", err)
	}
	if second.SessionToken != first.SessionToken || second.ContactWay.PlugID != first.ContactWay.PlugID {
		t.Fatal("static bootstrap retry was not idempotent")
	}
	var count int64
	if err := fixture.db.Model(&models.ArrivalContactWay{}).
		Where("scan_event_id = ?", findScanEventIDByHash(t, fixture, req.ScanEventID)).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("static contact snapshots=%d want=1", count)
	}
}

func TestArrivalBindingTicketLifecycleAndSecurity(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	if err := fixture.service.ensureAndSendBindingCard(
		fixture.conversation,
		fixture.instance,
		fixture.connection,
		"request-static-card",
	); err != nil {
		t.Fatalf("send binding card: %v", err)
	}
	if fixture.sender.calls != 1 {
		t.Fatalf("binding card sends=%d want=1", fixture.sender.calls)
	}
	ticket := repositories.ArrivalRepository.FindPendingBindingTicketByConversation(
		fixture.db,
		fixture.tenantID,
		fixture.conversation.ID,
		time.Now(),
	)
	if ticket == nil || ticket.TicketHash == "" || ticket.TokenEntropyHash == "" {
		t.Fatalf("pending ticket not persisted securely: %#v", ticket)
	}
	if strings.Contains(fixture.sender.payload, "bindTicket") ||
		strings.Contains(fixture.sender.payload, "conversation_id") ||
		strings.Contains(fixture.sender.payload, "customerId") {
		t.Fatalf("stored message payload leaked binding context: %s", fixture.sender.payload)
	}
	storedMessage := &models.Message{
		ID:             fixture.sender.messageID,
		TenantID:       fixture.tenantID,
		ConversationID: fixture.conversation.ID,
		MessageType:    enums.IMMessageTypeMiniProgram,
		Payload:        fixture.sender.payload,
	}
	dispatchMessage, transient, err := fixture.service.MaterializeOutboundMessage(storedMessage)
	if err != nil {
		t.Fatalf("materialize binding card: %v", err)
	}
	if !transient || dispatchMessage == storedMessage {
		t.Fatal("binding card must be materialized on an in-memory message copy")
	}
	body := map[string]any{}
	if err := json.Unmarshal([]byte(dispatchMessage.Payload), &body); err != nil {
		t.Fatal(err)
	}
	pagePath := strings.TrimSpace(body["page_path"].(string))
	const prefix = arrivalBindingCardPagePath + "?bindTicket="
	if !strings.HasPrefix(pagePath, prefix) {
		t.Fatalf("materialized page_path=%q", pagePath)
	}
	rawTicket := strings.TrimPrefix(pagePath, prefix)
	if rawTicket == "" ||
		rawTicket == ticket.TicketHash ||
		rawTicket == ticket.TokenEntropyHash ||
		strings.Contains(fixture.sender.payload, rawTicket) {
		t.Fatal("opaque ticket was persisted or reduced to a database digest")
	}
	if storedMessage.Payload != fixture.sender.payload {
		t.Fatal("materialization mutated the persisted message")
	}

	result, err := fixture.service.Bind(request.ArrivalBindRequest{
		SchemaVersion: arrivalBindInputVersion,
		LoginCode:     "valid-login-code",
		BindTicket:    rawTicket,
	}, "request-bind")
	if err != nil {
		t.Fatalf("consume binding ticket: %v", err)
	}
	if result.BindingStatus != string(enums.ArrivalBindingStatusBound) ||
		result.Store.Name != fixture.store.Name {
		t.Fatalf("unexpected bind result: %#v", result)
	}
	binding := repositories.ArrivalRepository.FindBinding(
		fixture.db,
		fixture.tenantID,
		fixture.identity.ID,
		fixture.store.ID,
	)
	if binding == nil ||
		binding.BindingProofType != enums.ArrivalBindingProofTypeCardTicket ||
		binding.BindingTicketID != ticket.ID ||
		binding.ConversationID != fixture.conversation.ID ||
		binding.WxWorkProtocolInstanceID != fixture.instance.ID ||
		binding.OfficialRelationStatus != enums.ArrivalOfficialRelationStatusUnconfirmed {
		t.Fatalf("unexpected card-ticket binding: %#v", binding)
	}
	consumed := repositories.ArrivalRepository.GetBindingTicket(
		fixture.db,
		ticket.ID,
		fixture.tenantID,
	)
	if consumed == nil ||
		consumed.TicketStatus != enums.ArrivalBindingTicketStatusConsumed ||
		consumed.ConsumedMiniProgramIdentityID != fixture.identity.ID {
		t.Fatalf("ticket not consumed: %#v", consumed)
	}
	var audit models.ArrivalAuditLog
	if err := fixture.db.Where(
		"entity_type = ? AND entity_id = ?",
		"ArrivalBindingTicket",
		ticket.ID,
	).Take(&audit).Error; err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{
		rawTicket,
		"openid-a",
		fixture.instance.Guid,
		"S:arrival-contact-a",
	} {
		if sensitive != "" && strings.Contains(audit.DetailJSON, sensitive) {
			t.Fatalf("binding audit leaked sensitive value: %s", audit.DetailJSON)
		}
	}
	if _, err := fixture.service.Bind(request.ArrivalBindRequest{
		SchemaVersion: arrivalBindInputVersion,
		LoginCode:     "valid-login-code-repeat",
		BindTicket:    rawTicket,
	}, "request-bind-repeat"); err != nil {
		t.Fatalf("same identity repeat must be idempotent: %v", err)
	}

	fixture.service.loginExchanger = &stubArrivalLoginExchanger{
		result: &weChatCodeSessionResult{OpenID: "openid-other", UnionID: "union-other"},
	}
	if _, err := fixture.service.Bind(request.ArrivalBindRequest{
		SchemaVersion: arrivalBindInputVersion,
		LoginCode:     "other-login-code",
		BindTicket:    rawTicket,
	}, "request-bind-other"); errorCode(err) != 2069 {
		t.Fatalf("cross-identity consume error=%v code=%d want=2069", err, errorCode(err))
	}
}

func TestArrivalBindingTicketsAreDistinctPerCustomer(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	firstRaw, firstTicket := createStaticArrivalBindingTicket(t, fixture)
	secondConversation := createStaticProtocolConversation(
		t,
		fixture,
		"arrival-contact-distinct",
		"客户 B",
	)
	secondTicket, err := fixture.service.ensurePendingTicket(
		secondConversation,
		fixture.instance,
		fixture.connection,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := fixture.security.BindingTicket(
		secondTicket.ID,
		secondTicket.TokenEntropyHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstTicket.ID == secondTicket.ID ||
		firstTicket.CustomerID == secondTicket.CustomerID ||
		firstTicket.ConversationID == secondTicket.ConversationID ||
		firstTicket.TicketHash == secondTicket.TicketHash ||
		firstTicket.TokenEntropyHash == secondTicket.TokenEntropyHash ||
		firstRaw == secondRaw {
		t.Fatalf(
			"customer tickets are not isolated: first=%#v second=%#v",
			firstTicket,
			secondTicket,
		)
	}
}

func TestArrivalBindingTicketUpdatesExistingUnboundBinding(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	rawTicket, ticket := createStaticArrivalBindingTicket(t, fixture)
	now := time.Now()
	existing := &models.ArrivalStoreBinding{
		TenantID:              fixture.tenantID,
		StoreID:               fixture.store.ID,
		MiniProgramIdentityID: fixture.identity.ID,
		BindingProofType:      enums.ArrivalBindingProofTypeProviderCallback,
		BindingStatus:         enums.ArrivalBindingStatusUnbound,
		Status:                enums.StatusOk,
		AuditFields:           arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(existing).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Bind(request.ArrivalBindRequest{
		SchemaVersion: arrivalBindInputVersion,
		LoginCode:     "valid-existing-binding-login",
		BindTicket:    rawTicket,
	}, "request-existing-binding"); err != nil {
		t.Fatal(err)
	}
	updated := repositories.ArrivalRepository.FindBinding(
		fixture.db,
		fixture.tenantID,
		fixture.identity.ID,
		fixture.store.ID,
	)
	if updated == nil ||
		updated.ID != existing.ID ||
		updated.BindingProofType != enums.ArrivalBindingProofTypeCardTicket ||
		updated.BindingTicketID != ticket.ID ||
		updated.BindingStatus != enums.ArrivalBindingStatusBound ||
		updated.ConversationID != fixture.conversation.ID {
		t.Fatalf("existing binding was not upgraded in place: %#v", updated)
	}
}

func TestWxWorkNewContactAutomationUsesStaticBindingTicketWithoutWelcome(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	fixture.instance.WelcomeEnabled = false
	if err := fixture.db.Model(fixture.instance).Update("welcome_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if !WxWorkProtocolContactAutomationService.ShouldProcessNewContacts(
		fixture.instance,
	) {
		t.Fatal("static connection must activate new-contact processing independently of welcome text")
	}
	previousSender := ArrivalBindingTicketService.messageSender
	ArrivalBindingTicketService.messageSender = fixture.sender
	t.Cleanup(func() {
		ArrivalBindingTicketService.messageSender = previousSender
	})
	for i := 0; i < 2; i++ {
		if err := ArrivalBindingTicketService.SendBindingCardForNewContact(
			fixture.conversation,
			fixture.instance,
			"request-new-contact",
		); err != nil {
			t.Fatal(err)
		}
	}
	var tickets []models.ArrivalBindingTicket
	if err := fixture.db.Where(
		"tenant_id = ? AND conversation_id = ?",
		fixture.tenantID,
		fixture.conversation.ID,
	).Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 1 ||
		len(fixture.sender.clientMsgIDs) != 2 ||
		fixture.sender.clientMsgIDs[0] != fixture.sender.clientMsgIDs[1] {
		t.Fatalf(
			"duplicate new-contact event was not idempotent: tickets=%d clientMsgIDs=%v",
			len(tickets),
			fixture.sender.clientMsgIDs,
		)
	}
}

func TestArrivalCardTicketBindingDeliversSecondScanToBoundConversation(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	rawTicket, _ := createStaticArrivalBindingTicket(t, fixture)
	if _, err := fixture.service.Bind(request.ArrivalBindRequest{
		SchemaVersion: arrivalBindInputVersion,
		LoginCode:     "valid-login-code",
		BindTicket:    rawTicket,
	}, "request-bind-before-second-scan"); err != nil {
		t.Fatal(err)
	}
	cardSender := &stubArrivalCardSender{
		status: enums.ArrivalDeliveryStatusSent,
	}
	linkService := &arrivalLinkService{
		loginExchanger: &stubArrivalLoginExchanger{
			result: &weChatCodeSessionResult{OpenID: "openid-a", UnionID: "union-a"},
		},
		cardSender: cardSender,
	}
	result, err := linkService.Bootstrap(request.ArrivalBootstrapRequest{
		SchemaVersion: arrivalScanInputVersion,
		LoginCode:     "valid-second-scan-login",
		Scene:         fixture.connection.StoreScene,
		ScanEventID:   "static-second-scan-event",
	})
	if err != nil {
		t.Fatalf("second bound scan: %v", err)
	}
	if result.BindingStatus != string(enums.ArrivalBindingStatusBound) ||
		result.DeliveryStatus != string(enums.ArrivalDeliveryStatusSent) ||
		result.ContactWay.Available {
		t.Fatalf("unexpected second scan result: %#v", result)
	}
	if cardSender.calls != 1 ||
		cardSender.lastConversationID != fixture.conversation.ID ||
		cardSender.lastInstanceID != fixture.instance.ID ||
		!strings.HasPrefix(cardSender.lastClientMsgID, "arrival_") {
		t.Fatalf("second scan delivery target mismatch: %#v", cardSender)
	}
}

func TestArrivalBindingTicketFixedErrorCodes(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(t *testing.T, fixture staticArrivalBindingFixture, ticket *models.ArrivalBindingTicket)
		request  func(rawTicket string) request.ArrivalBindRequest
		wantCode int
	}{
		{
			name: "invalid format",
			request: func(string) request.ArrivalBindRequest {
				return request.ArrivalBindRequest{
					SchemaVersion: arrivalBindInputVersion,
					LoginCode:     "valid-login-code",
					BindTicket:    "not+urlsafe",
				}
			},
			wantCode: 1000,
		},
		{
			name: "invalid login code",
			prepare: func(t *testing.T, fixture staticArrivalBindingFixture, ticket *models.ArrivalBindingTicket) {
				fixture.service.loginExchanger = &stubArrivalLoginExchanger{err: errors.New("expired code")}
			},
			wantCode: 2062,
		},
		{
			name: "invalid ticket signature",
			request: func(rawTicket string) request.ArrivalBindRequest {
				last := byte('A')
				if rawTicket[len(rawTicket)-1] == last {
					last = 'B'
				}
				return request.ArrivalBindRequest{
					SchemaVersion: arrivalBindInputVersion,
					LoginCode:     "valid-login-code",
					BindTicket:    rawTicket[:len(rawTicket)-1] + string(last),
				}
			},
			wantCode: 2066,
		},
		{
			name: "revoked",
			prepare: func(t *testing.T, fixture staticArrivalBindingFixture, ticket *models.ArrivalBindingTicket) {
				if err := fixture.db.Model(ticket).Updates(map[string]any{
					"ticket_status": enums.ArrivalBindingTicketStatusRevoked,
					"revoked_at":    time.Now(),
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 2068,
		},
		{
			name: "expired",
			prepare: func(t *testing.T, fixture staticArrivalBindingFixture, ticket *models.ArrivalBindingTicket) {
				if err := fixture.db.Model(ticket).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 2067,
		},
		{
			name: "missing recent scan",
			prepare: func(t *testing.T, fixture staticArrivalBindingFixture, ticket *models.ArrivalBindingTicket) {
				if err := fixture.db.Where(
					"tenant_id = ? AND store_id = ?",
					fixture.tenantID,
					fixture.store.ID,
				).Delete(&models.ArrivalScanEvent{}).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 2070,
		},
		{
			name: "instance unavailable",
			prepare: func(t *testing.T, fixture staticArrivalBindingFixture, ticket *models.ArrivalBindingTicket) {
				if err := fixture.db.Model(fixture.instance).Update("status", enums.StatusDisabled).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 2071,
		},
		{
			name: "store unavailable",
			prepare: func(t *testing.T, fixture staticArrivalBindingFixture, ticket *models.ArrivalBindingTicket) {
				if err := fixture.db.Model(fixture.store).Update("status", enums.StatusDisabled).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 2071,
		},
		{
			name: "conversation unavailable",
			prepare: func(t *testing.T, fixture staticArrivalBindingFixture, ticket *models.ArrivalBindingTicket) {
				if err := fixture.db.Model(fixture.conversation).Update(
					"status",
					enums.IMConversationStatusClosed,
				).Error; err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 2071,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupStaticArrivalBindingFixture(t)
			rawTicket, ticket := createStaticArrivalBindingTicket(t, fixture)
			if tt.prepare != nil {
				tt.prepare(t, fixture, ticket)
			}
			req := request.ArrivalBindRequest{
				SchemaVersion: arrivalBindInputVersion,
				LoginCode:     "valid-login-code",
				BindTicket:    rawTicket,
			}
			if tt.request != nil {
				req = tt.request(rawTicket)
			}
			_, err := fixture.service.Bind(req, "request-fixed-error")
			if got := errorCode(err); got != tt.wantCode {
				t.Fatalf("Bind() error=%v code=%d want=%d", err, got, tt.wantCode)
			}
		})
	}
}

func TestArrivalBindingTicketRejectsConversationConflictAndAmbiguousStore(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	firstRaw, _ := createStaticArrivalBindingTicket(t, fixture)
	if _, err := fixture.service.Bind(request.ArrivalBindRequest{
		SchemaVersion: arrivalBindInputVersion,
		LoginCode:     "valid-login-first",
		BindTicket:    firstRaw,
	}, "request-first-bind"); err != nil {
		t.Fatal(err)
	}

	secondConversation := createStaticProtocolConversation(
		t,
		fixture,
		"arrival-contact-b",
		"客户 B",
	)
	secondTicket, err := fixture.service.ensurePendingTicket(
		secondConversation,
		fixture.instance,
		fixture.connection,
	)
	if err != nil {
		t.Fatal(err)
	}
	rawSecond, err := fixture.security.BindingTicket(
		secondTicket.ID,
		secondTicket.TokenEntropyHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Bind(request.ArrivalBindRequest{
		SchemaVersion: arrivalBindInputVersion,
		LoginCode:     "valid-login-conflict",
		BindTicket:    rawSecond,
	}, "request-conflict"); errorCode(err) != 2069 {
		t.Fatalf("same store different conversation error=%v code=%d want=2069", err, errorCode(err))
	}

	secondStore := &models.Store{
		TenantID:    fixture.tenantID,
		StoreCode:   "arrival-ambiguous",
		Name:        "歧义门店",
		Status:      enums.StatusOk,
		AuditFields: arrivalSystemAuditFields(time.Now()),
	}
	if err := fixture.db.Create(secondStore).Error; err != nil {
		t.Fatal(err)
	}
	secondConnection := &models.StoreArrivalConnection{
		TenantID:                 fixture.tenantID,
		StoreID:                  secondStore.ID,
		StoreStaffBindingID:      fixture.storeStaffBinding.ID,
		StoreScene:               "arr-ambiguous",
		ContactProviderMode:      enums.ArrivalContactProviderModeStaticPluginTicket,
		StaticContactPlugID:      "plug-ambiguous",
		WxWorkProtocolInstanceID: fixture.instance.ID,
		ConnectionStatus:         enums.ArrivalConnectionStatusActive,
		Status:                   enums.StatusOk,
		AuditFields:              arrivalSystemAuditFields(time.Now()),
	}
	if err := fixture.db.Create(secondConnection).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.uniqueStaticConnectionForInstance(fixture.instance); errorCode(err) != 2071 {
		t.Fatalf("ambiguous static mapping error=%v code=%d want=2071", err, errorCode(err))
	}
	if _, err := ArrivalConnectionService.UpdateProvider(
		request.UpdateArrivalConnectionProviderRequest{
			StoreID:                  fixture.store.ID,
			ContactProvider:          string(enums.ArrivalContactProviderModeStaticPluginTicket),
			StaticContactPlugID:      fixture.connection.StaticContactPlugID,
			WxWorkProtocolInstanceID: fixture.instance.ID,
		},
		staticArrivalOperator(fixture.tenantID),
	); errorCode(err) != 1000 {
		t.Fatalf("provider update ambiguous mapping error=%v code=%d want=1000", err, errorCode(err))
	}
}

func TestArrivalCardTicketBindingsRemainIsolatedAcrossStores(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	firstRaw, _ := createStaticArrivalBindingTicket(t, fixture)
	if _, err := fixture.service.Bind(request.ArrivalBindRequest{
		SchemaVersion: arrivalBindInputVersion,
		LoginCode:     "valid-store-a-login",
		BindTicket:    firstRaw,
	}, "request-store-a"); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	storeB := &models.Store{
		TenantID:    fixture.tenantID,
		StoreCode:   "arrival-store-b",
		Name:        "到店联动 B 店",
		BrandName:   "丽斯未来",
		Status:      enums.StatusOk,
		AuditFields: arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(storeB).Error; err != nil {
		t.Fatal(err)
	}
	storeStaffUserB := &models.User{
		TenantID:    fixture.tenantID,
		Username:    "arrival-store-staff-b",
		Nickname:    "到店测试门店员工 B",
		Status:      enums.StatusOk,
		AuditFields: arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(storeStaffUserB).Error; err != nil {
		t.Fatal(err)
	}
	storeStaffRole := repositories.RoleRepository.GetByCode(fixture.db, constants.RoleCodeStoreStaff)
	if storeStaffRole == nil {
		t.Fatal("Store staff role missing")
	}
	if err := fixture.db.Create(&models.UserRole{
		UserID:      storeStaffUserB.ID,
		RoleID:      storeStaffRole.ID,
		AuditFields: arrivalSystemAuditFields(now),
	}).Error; err != nil {
		t.Fatal(err)
	}
	storeStaffBindingB := &models.StoreStaffBinding{
		TenantID:             fixture.tenantID,
		UserID:               storeStaffUserB.ID,
		ActiveUserID:         positiveInt64Pointer(storeStaffUserB.ID),
		StoreID:              storeB.ID,
		ManagedMode:          constants.StoreManagedModeSemi,
		FallbackToHQ:         true,
		ManualTimeoutMinutes: DefaultManualTimeoutMinutes,
		Status:               enums.StatusOk,
		AuditFields:          arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(storeStaffBindingB).Error; err != nil {
		t.Fatal(err)
	}
	instanceB := &models.WxWorkProtocolInstance{
		TenantID:                  fixture.tenantID,
		Guid:                      "static-arrival-guid-b",
		ChannelID:                 fixture.instance.ChannelID,
		EmployeeName:              "静态到店员工 B",
		StoreID:                   storeB.ID,
		StoreStaffBindingID:       storeStaffBindingB.ID,
		DefaultMiniProgramPayload: fixture.instance.DefaultMiniProgramPayload,
		HealthStatus:              "online",
		Status:                    enums.StatusOk,
		AuditFields:               arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(instanceB).Error; err != nil {
		t.Fatal(err)
	}
	connectionB := &models.StoreArrivalConnection{
		TenantID:                 fixture.tenantID,
		StoreID:                  storeB.ID,
		StoreStaffBindingID:      storeStaffBindingB.ID,
		StoreScene:               "arr-test-b",
		ContactProviderMode:      enums.ArrivalContactProviderModeStaticPluginTicket,
		StaticContactPlugID:      "plug-real-arrival-b",
		WxWorkProtocolInstanceID: instanceB.ID,
		ConnectionStatus:         enums.ArrivalConnectionStatusActive,
		Status:                   enums.StatusOk,
		AuditFields:              arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(connectionB).Error; err != nil {
		t.Fatal(err)
	}
	scanB := &models.ArrivalScanEvent{
		TenantID:              fixture.tenantID,
		StoreID:               storeB.ID,
		MiniProgramIdentityID: fixture.identity.ID,
		ScanEventHash:         fixture.security.Fingerprint("scan_event", "store-b-scan"),
		SchemaVersion:         arrivalScanInputVersion,
		IdentityStatus:        enums.ArrivalIdentityStatusMatched,
		BindingStatus:         enums.ArrivalBindingStatusUnbound,
		DeliveryStatus:        enums.ArrivalDeliveryStatusNotBound,
		Status:                enums.StatusOk,
		AuditFields:           arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(scanB).Error; err != nil {
		t.Fatal(err)
	}
	baseB := fixture.arrivalLinkTestFixture
	baseB.store = storeB
	baseB.storeStaffUser = storeStaffUserB
	baseB.storeStaffBinding = storeStaffBindingB
	baseB.instance = instanceB
	baseB.connection = connectionB
	fixtureB := fixture
	fixtureB.arrivalLinkTestFixture = baseB
	fixtureB.instance = instanceB
	fixtureB.conversation = createStaticProtocolConversation(
		t,
		fixtureB,
		"arrival-contact-store-b",
		"客户 B",
	)
	ticketB, err := fixture.service.ensurePendingTicket(
		fixtureB.conversation,
		instanceB,
		connectionB,
	)
	if err != nil {
		t.Fatal(err)
	}
	rawB, err := fixture.security.BindingTicket(
		ticketB.ID,
		ticketB.TokenEntropyHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Bind(request.ArrivalBindRequest{
		SchemaVersion: arrivalBindInputVersion,
		LoginCode:     "valid-store-b-login",
		BindTicket:    rawB,
	}, "request-store-b"); err != nil {
		t.Fatal(err)
	}

	bindingA := repositories.ArrivalRepository.FindBinding(
		fixture.db,
		fixture.tenantID,
		fixture.identity.ID,
		fixture.store.ID,
	)
	bindingB := repositories.ArrivalRepository.FindBinding(
		fixture.db,
		fixture.tenantID,
		fixture.identity.ID,
		storeB.ID,
	)
	if bindingA == nil ||
		bindingB == nil ||
		bindingA.ConversationID != fixture.conversation.ID ||
		bindingB.ConversationID != fixtureB.conversation.ID ||
		bindingA.ConversationID == bindingB.ConversationID ||
		bindingA.WxWorkProtocolInstanceID == bindingB.WxWorkProtocolInstanceID {
		t.Fatalf("cross-store bindings were mixed: A=%#v B=%#v", bindingA, bindingB)
	}
}

func TestArrivalProviderChangeAndDisableRevokePendingTickets(t *testing.T) {
	t.Run("provider change", func(t *testing.T) {
		fixture := setupStaticArrivalBindingFixture(t)
		_, ticket := createStaticArrivalBindingTicket(t, fixture)
		operator := staticArrivalOperator(fixture.tenantID)

		result, err := ArrivalConnectionService.UpdateProvider(
			request.UpdateArrivalConnectionProviderRequest{
				StoreID:         fixture.store.ID,
				ContactProvider: string(enums.ArrivalContactProviderModeContactWay),
			},
			operator,
		)
		if err != nil {
			t.Fatalf("change provider: %v", err)
		}
		if result.ContactProvider != string(enums.ArrivalContactProviderModeContactWay) ||
			result.StaticContactPlugID != "" {
			t.Fatalf("unexpected provider result: %#v", result)
		}
		storedTicket := repositories.ArrivalRepository.GetBindingTicket(
			fixture.db,
			ticket.ID,
			fixture.tenantID,
		)
		if storedTicket == nil ||
			storedTicket.TicketStatus != enums.ArrivalBindingTicketStatusRevoked ||
			storedTicket.RevokedAt == nil {
			t.Fatalf("provider change did not revoke ticket: %#v", storedTicket)
		}
	})

	t.Run("connection disable", func(t *testing.T) {
		fixture := setupStaticArrivalBindingFixture(t)
		_, ticket := createStaticArrivalBindingTicket(t, fixture)
		if err := fixture.db.Where(
			"tenant_id = ? AND store_id = ?",
			fixture.tenantID,
			fixture.store.ID,
		).Delete(&models.ArrivalContactWay{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := ArrivalConnectionService.DisableConnection(
			request.DisableArrivalConnectionRequest{
				ConnectionID: fixture.connection.ID,
				Reason:       "test disable",
			},
			staticArrivalOperator(fixture.tenantID),
		); err != nil {
			t.Fatalf("disable connection: %v", err)
		}
		storedTicket := repositories.ArrivalRepository.GetBindingTicket(
			fixture.db,
			ticket.ID,
			fixture.tenantID,
		)
		if storedTicket == nil ||
			storedTicket.TicketStatus != enums.ArrivalBindingTicketStatusRevoked {
			t.Fatalf("connection disable did not revoke ticket: %#v", storedTicket)
		}
		connection := repositories.ArrivalRepository.GetConnection(
			fixture.db,
			fixture.connection.ID,
			fixture.tenantID,
		)
		if connection == nil ||
			connection.ConnectionStatus != enums.ArrivalConnectionStatusDisabled ||
			connection.Status != enums.StatusDisabled {
			t.Fatalf("connection was not disabled: %#v", connection)
		}
	})
}

func TestArrivalMaintenanceExpiresPendingBindingTickets(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	_, ticket := createStaticArrivalBindingTicket(t, fixture)
	if err := fixture.db.Model(ticket).Update(
		"expires_at",
		time.Now().Add(-time.Minute),
	).Error; err != nil {
		t.Fatal(err)
	}
	expired, err := repositories.ArrivalRepository.ExpirePendingBindingTickets(
		fixture.db,
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	stored := repositories.ArrivalRepository.GetBindingTicket(
		fixture.db,
		ticket.ID,
		fixture.tenantID,
	)
	if expired != 1 ||
		stored == nil ||
		stored.TicketStatus != enums.ArrivalBindingTicketStatusExpired {
		t.Fatalf("expired=%d ticket=%#v", expired, stored)
	}
}

func TestArrivalBindingCardRequiresExplicitSystemOutboundMarker(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	if err := fixture.db.AutoMigrate(&models.Channel{}); err != nil {
		t.Fatal(err)
	}
	channel := &models.Channel{
		ID:          fixture.instance.ChannelID,
		TenantID:    fixture.tenantID,
		Name:        "企微员工号测试渠道",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "arrival-static-channel",
		Status:      enums.StatusOk,
		AuditFields: arrivalSystemAuditFields(time.Now()),
	}
	if err := fixture.db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	rawTicket, ticket := createStaticArrivalBindingTicket(t, fixture)
	_, payload, err := buildStoredArrivalBindingCardPayload(
		fixture.instance,
		ticket.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	message := &models.Message{
		ID:             9901,
		TenantID:       fixture.tenantID,
		ConversationID: fixture.conversation.ID,
		SenderType:     enums.IMSenderTypeSystem,
		MessageType:    enums.IMMessageTypeMiniProgram,
		Content:        "连接门店服务",
		Payload:        payload,
	}
	ChannelMessageOutboxService.PrepareOutboundMessage(
		fixture.db,
		fixture.conversation,
		message,
	)
	if message.OutboundChannelType != "" {
		t.Fatal("ordinary outbound preparation marked a system message")
	}
	if created, err := ChannelMessageOutboxService.EnsureMarkedOutboundMessage(
		fixture.conversation,
		message,
	); err != nil || created {
		t.Fatalf("unmarked system message entered outbox: created=%v err=%v", created, err)
	}
	ChannelMessageOutboxService.PrepareSystemOutboundMessage(
		fixture.db,
		fixture.conversation,
		message,
	)
	if message.OutboundChannelType != enums.ChannelTypeWxWorkProtocol {
		t.Fatalf("system outbound marker=%q", message.OutboundChannelType)
	}
	if created, err := ChannelMessageOutboxService.EnsureMarkedOutboundMessage(
		fixture.conversation,
		message,
	); err != nil || !created {
		t.Fatalf("marked system message was not queued: created=%v err=%v", created, err)
	}
	outbox := ChannelMessageOutboxService.GetByMessageIDInTenant(
		enums.ChannelTypeWxWorkProtocol,
		message.ID,
		fixture.tenantID,
	)
	if outbox == nil ||
		strings.Contains(outbox.Payload, rawTicket) ||
		strings.Contains(outbox.Payload, "bindTicket") {
		t.Fatalf("outbox missing or leaked raw binding ticket: %#v", outbox)
	}
}

func TestArrivalBindingCardSystemMessageOutboxRepair(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	if err := fixture.db.AutoMigrate(
		&models.Channel{},
		&models.Message{},
		&models.ChannelMessageOutbox{},
	); err != nil {
		t.Fatal(err)
	}
	channel := &models.Channel{
		ID:          fixture.instance.ChannelID,
		TenantID:    fixture.tenantID,
		Name:        "企微员工号测试渠道",
		ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID:   "arrival-static-repair-channel",
		Status:      enums.StatusOk,
		AuditFields: arrivalSystemAuditFields(time.Now()),
	}
	if err := fixture.db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	rawTicket, ticket := createStaticArrivalBindingTicket(t, fixture)
	content, payload, err := buildStoredArrivalBindingCardPayload(fixture.instance, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	message := &models.Message{
		TenantID:       fixture.tenantID,
		ConversationID: fixture.conversation.ID,
		SessionNo:      1,
		ClientMsgID:    "arrival-binding-repair-test",
		SenderType:     enums.IMSenderTypeSystem,
		MessageType:    enums.IMMessageTypeMiniProgram,
		Content:        content,
		Payload:        payload,
		SeqNo:          1,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    arrivalSystemAuditFields(now),
	}
	ChannelMessageOutboxService.PrepareSystemOutboundMessage(
		fixture.db,
		fixture.conversation,
		message,
	)
	if err := fixture.db.Create(message).Error; err != nil {
		t.Fatal(err)
	}

	repaired, err := ChannelMessageOutboxService.RepairMissingOutboundMessages(10)
	if err != nil {
		t.Fatalf("repair missing system outbox: %v", err)
	}
	outbox := ChannelMessageOutboxService.GetByMessageIDInTenant(
		enums.ChannelTypeWxWorkProtocol,
		message.ID,
		fixture.tenantID,
	)
	if repaired != 1 || outbox == nil {
		t.Fatalf("repaired=%d outbox=%#v", repaired, outbox)
	}
	if strings.Contains(outbox.Payload, rawTicket) || strings.Contains(outbox.Payload, "bindTicket") {
		t.Fatalf("repaired outbox leaked raw binding ticket: %#v", outbox)
	}
}

func TestArrivalBindingTicketErrorRedaction(t *testing.T) {
	const rawTicket = "AbCdEf0123456789_-ticket"
	message := &models.Message{
		Payload: `{"page_path":"pages/arrival/index?bindTicket=` + rawTicket + `"}`,
	}
	reason := `upstream rejected {"page_path":"pages/arrival/index?bindTicket=` + rawTicket + `"} token=` + rawTicket
	redacted := redactArrivalBindingTicketError(message, reason)
	if strings.Contains(redacted, rawTicket) || !strings.Contains(redacted, "bindTicket=[REDACTED]") {
		t.Fatalf("binding ticket error was not redacted: %q", redacted)
	}
}

func TestArrivalBindingCardMaterializationRejectsExpiredPendingTicket(t *testing.T) {
	fixture := setupStaticArrivalBindingFixture(t)
	_, ticket := createStaticArrivalBindingTicket(t, fixture)
	_, payload, err := buildStoredArrivalBindingCardPayload(fixture.instance, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(ticket).Update(
		"expires_at",
		time.Now().Add(-time.Minute),
	).Error; err != nil {
		t.Fatal(err)
	}
	message := &models.Message{
		TenantID:       fixture.tenantID,
		ConversationID: fixture.conversation.ID,
		SenderType:     enums.IMSenderTypeSystem,
		MessageType:    enums.IMMessageTypeMiniProgram,
		Payload:        payload,
	}
	materialized, transient, err := fixture.service.MaterializeOutboundMessage(message)
	if err == nil || !transient || materialized != nil {
		t.Fatalf(
			"expired ticket materialization message=%#v transient=%v err=%v",
			materialized,
			transient,
			err,
		)
	}
}

func setupStaticArrivalBindingFixture(t *testing.T) staticArrivalBindingFixture {
	t.Helper()
	base := setupArrivalLinkTestFixture(t)
	instance := configureStaticArrivalConnection(t, base)
	cfg := config.Current().Arrival
	cfg.ContactProvider = string(enums.ArrivalContactProviderModeStaticPluginTicket)
	cfg.WeComSuiteID = ""
	cfg.WeComSuiteSecret = ""
	cfg.WeComProviderCallbackToken = ""
	cfg.WeComProviderEncodingAESKey = ""
	config.SetCurrent(&config.Config{Arrival: cfg})
	if err := base.db.Model(base.scanEvent).Updates(map[string]any{
		"created_at": time.Now(),
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := base.db.AutoMigrate(&models.ChannelMessageOutbox{}); err != nil {
		t.Fatal(err)
	}
	conversation := createStaticProtocolConversation(t, staticArrivalBindingFixture{
		arrivalLinkTestFixture: base,
		instance:               instance,
	}, "arrival-contact-a", "客户 A")
	sender := &stubArrivalBindingMessageSender{
		tenantID:  base.tenantID,
		messageID: 8001,
	}
	service := &arrivalBindingTicketService{
		loginExchanger: &stubArrivalLoginExchanger{
			result: &weChatCodeSessionResult{OpenID: "openid-a", UnionID: "union-a"},
		},
		messageSender: sender,
	}
	identity, _, err := ArrivalLinkService.ensureIdentity(
		base.tenantID,
		"openid-a",
		"union-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID != base.identity.ID {
		t.Fatalf(
			"fixture identity mismatch: ensured=%d expected=%d",
			identity.ID,
			base.identity.ID,
		)
	}
	if recent := repositories.ArrivalRepository.FindRecentScanEvent(
		base.db,
		base.tenantID,
		base.store.ID,
		base.identity.ID,
		time.Now().Add(-time.Hour),
	); recent == nil {
		t.Fatal("fixture recent scan event missing")
	}
	customer := repositories.CustomerRepository.GetInTenant(
		base.db,
		conversation.CustomerID,
		base.tenantID,
	)
	return staticArrivalBindingFixture{
		arrivalLinkTestFixture: base,
		instance:               instance,
		customer:               customer,
		conversation:           conversation,
		sender:                 sender,
		service:                service,
	}
}

func configureStaticArrivalConnection(
	t *testing.T,
	fixture arrivalLinkTestFixture,
) *models.WxWorkProtocolInstance {
	t.Helper()
	defaultPayload := `{
			"username":"gh_arrival_test",
			"appid":"wx-old-template",
			"appname":"知悉微宝",
			"appicon":"https://example.test/appicon.png",
			"thumb_url":"https://example.test/cover.png",
			"title":"连接门店服务",
			"page_path":"pages/index/index"
		}`
	if err := repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(
		fixture.db,
		fixture.instance.ID,
		fixture.tenantID,
		map[string]any{
			"guid":                         "static-arrival-guid",
			"channel_id":                   701,
			"employee_name":                "静态到店员工",
			"default_mini_program_payload": defaultPayload,
			"health_status":                "online",
			"status":                       enums.StatusOk,
			"updated_at":                   time.Now(),
		},
	); err != nil {
		t.Fatalf("update static protocol instance: %v", err)
	}
	instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(fixture.db, fixture.instance.ID, fixture.tenantID)
	if instance == nil {
		t.Fatal("static protocol instance missing")
	}
	if err := repositories.ArrivalRepository.UpdateConnection(
		fixture.db,
		fixture.connection.ID,
		fixture.tenantID,
		map[string]any{
			"contact_provider_mode":        enums.ArrivalContactProviderModeStaticPluginTicket,
			"static_contact_plug_id":       "plug-real-arrival-test",
			"tenant_authorization_id":      0,
			"contact_member_ciphertext":    "",
			"contact_member_nonce":         "",
			"contact_member_fingerprint":   "",
			"wx_work_protocol_instance_id": instance.ID,
			"connection_status":            enums.ArrivalConnectionStatusActive,
			"status":                       enums.StatusOk,
		},
	); err != nil {
		t.Fatal(err)
	}
	fixture.connection.ContactProviderMode = enums.ArrivalContactProviderModeStaticPluginTicket
	fixture.connection.StaticContactPlugID = "plug-real-arrival-test"
	fixture.connection.TenantAuthorizationID = 0
	fixture.connection.ContactMemberCiphertext = ""
	fixture.connection.ContactMemberNonce = ""
	fixture.connection.ContactMemberFingerprint = ""
	fixture.connection.WxWorkProtocolInstanceID = instance.ID
	return instance
}

func createStaticProtocolConversation(
	t *testing.T,
	fixture staticArrivalBindingFixture,
	externalID, customerName string,
) *models.Conversation {
	t.Helper()
	now := time.Now()
	customer := &models.Customer{
		TenantID:    fixture.tenantID,
		Name:        customerName,
		Status:      enums.StatusOk,
		AuditFields: arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(customer).Error; err != nil {
		t.Fatal(err)
	}
	conversation := &models.Conversation{
		TenantID:            fixture.tenantID,
		StoreID:             fixture.store.ID,
		StoreStaffBindingID: fixture.storeStaffBinding.ID,
		ChannelID:           fixture.instance.ChannelID,
		CustomerID:          customer.ID,
		CustomerName:        customerName,
		Status:              enums.IMConversationStatusActive,
		ServiceMode:         enums.IMConversationServiceModeAIFirst,
		LastMessageAt:       now,
		LastActiveAt:        now,
		AuditFields:         arrivalSystemAuditFields(now),
	}
	threadKey := buildStoreConversationThreadKey(fixture.tenantID, fixture.store.ID, customer.ID, fixture.storeStaffBinding.ID)
	conversation.ThreadKey = &threadKey
	if err := fixture.db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	route := &models.ConversationRouteState{
		TenantID:            fixture.tenantID,
		ConversationID:      conversation.ID,
		StoreID:             fixture.store.ID,
		StoreStaffBindingID: fixture.storeStaffBinding.ID,
		WxWorkInstanceID:    fixture.instance.ID,
		RouteStatus:         enums.ConversationRouteStatusAIServing,
		SessionNo:           1,
		AuditFields:         arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(route).Error; err != nil {
		t.Fatal(err)
	}
	mapping := &models.WxWorkKFConversation{
		TenantID:       fixture.tenantID,
		ConversationID: conversation.ID,
		ChannelID:      fixture.instance.ChannelID,
		OpenKfID:       "wx_protocol:" + fixture.instance.Guid,
		ExternalUserID: "S:" + externalID,
		SessionStatus:  string(enums.WxWorkKFSessionStatusActive),
		Status:         enums.StatusOk,
		AuditFields:    arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(mapping).Error; err != nil {
		t.Fatal(err)
	}
	return conversation
}

func createStaticArrivalBindingTicket(
	t *testing.T,
	fixture staticArrivalBindingFixture,
) (string, *models.ArrivalBindingTicket) {
	t.Helper()
	ticket, err := fixture.service.ensurePendingTicket(
		fixture.conversation,
		fixture.instance,
		fixture.connection,
	)
	if err != nil {
		t.Fatal(err)
	}
	rawTicket, err := fixture.security.BindingTicket(
		ticket.ID,
		ticket.TokenEntropyHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	return rawTicket, ticket
}

func findScanEventIDByHash(
	t *testing.T,
	fixture arrivalLinkTestFixture,
	scanEventID string,
) int64 {
	t.Helper()
	event := repositories.ArrivalRepository.FindScanEventByHash(
		fixture.db,
		fixture.security.Fingerprint("scan_event", scanEventID),
	)
	if event == nil {
		t.Fatal("scan event not found")
	}
	return event.ID
}

func errorCode(err error) int {
	if err == nil {
		return 0
	}
	var codeErr *web.CodeError
	if errors.As(err, &codeErr) {
		return codeErr.Code
	}
	return -1
}

func staticArrivalOperator(tenantID int64) *dto.AuthPrincipal {
	return &dto.AuthPrincipal{
		UserID:         901,
		TenantID:       tenantID,
		ActiveTenantID: tenantID,
		Username:       "arrival-test-admin",
	}
}
