package services

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type stubArrivalAcquisitionProvider struct {
	mu sync.Mutex

	quota     *weComCustomerAcquisitionQuota
	quotaErr  error
	created   *weComCustomerAcquisitionLink
	createErr error
	details   *weComCustomerAcquisitionLink
	detailErr error
	customers []weComCustomerAcquisitionCustomer
	listErr   error
	delay     time.Duration

	quotaCalls  int
	createCalls int
	getCalls    int
	listCalls   int
	members     []string
}

func (s *stubArrivalAcquisitionProvider) GetCustomerAcquisitionQuota(
	*models.WeComTenantAuthorization,
) (*weComCustomerAcquisitionQuota, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.quotaCalls++
	if s.quota == nil {
		return nil, s.quotaErr
	}
	value := *s.quota
	return &value, s.quotaErr
}

func (s *stubArrivalAcquisitionProvider) CreateCustomerAcquisitionLink(
	_ *models.WeComTenantAuthorization,
	memberUserID string,
) (*weComCustomerAcquisitionLink, error) {
	s.mu.Lock()
	s.createCalls++
	s.members = append(s.members, memberUserID)
	delay := s.delay
	err := s.createErr
	var value *weComCustomerAcquisitionLink
	if s.created != nil {
		copyValue := *s.created
		copyValue.UserList = append([]string(nil), s.created.UserList...)
		value = &copyValue
	}
	s.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	return value, err
}

func (s *stubArrivalAcquisitionProvider) GetCustomerAcquisitionLink(
	_ *models.WeComTenantAuthorization,
	_ string,
) (*weComCustomerAcquisitionLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	if s.details == nil {
		return nil, s.detailErr
	}
	value := *s.details
	value.UserList = append([]string(nil), s.details.UserList...)
	return &value, s.detailErr
}

func (s *stubArrivalAcquisitionProvider) ListCustomerAcquisitionCustomers(
	_ *models.WeComTenantAuthorization,
	_ string,
) ([]weComCustomerAcquisitionCustomer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	return append([]weComCustomerAcquisitionCustomer(nil), s.customers...), s.listErr
}

func (s *stubArrivalAcquisitionProvider) counts() (quota, create, get, list int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quotaCalls, s.createCalls, s.getCalls, s.listCalls
}

type capturingArrivalPayloadQRBuilder struct {
	mu       sync.Mutex
	payloads []string
}

func (s *capturingArrivalPayloadQRBuilder) BuildPayloadArtifact(
	payload string,
) (*arrivalQRCodeArtifact, error) {
	s.mu.Lock()
	s.payloads = append(s.payloads, payload)
	s.mu.Unlock()
	sum := sha256.Sum256([]byte(payload))
	encoded := base64.StdEncoding.EncodeToString([]byte("test-png"))
	return &arrivalQRCodeArtifact{
		OriginalPNGBase64:  encoded,
		PublishedPNGBase64: encoded,
		PayloadHash:        hex.EncodeToString(sum[:]),
		ArtworkVerified:    true,
	}, nil
}

func (s *capturingArrivalPayloadQRBuilder) values() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.payloads...)
}

func successfulArrivalAcquisitionProvider(memberUserID string) *stubArrivalAcquisitionProvider {
	return &stubArrivalAcquisitionProvider{
		quota: &weComCustomerAcquisitionQuota{Total: 1000, Balance: 500},
		created: &weComCustomerAcquisitionLink{
			LinkID:     "acquisition-link-a",
			URL:        "https://work.weixin.qq.com/ca/arrival-link-a",
			CreateTime: 1_700_000_000,
			UserList:   []string{memberUserID},
		},
		details: &weComCustomerAcquisitionLink{
			LinkID:     "acquisition-link-a",
			LinkName:   "门店到店管家",
			URL:        "https://work.weixin.qq.com/ca/arrival-link-a",
			CreateTime: 1_700_000_000,
			UserList:   []string{memberUserID},
		},
	}
}

func TestArrivalAcquisitionPreflightUsesRealQuotaOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		provider    *stubArrivalAcquisitionProvider
		wantCode    string
		wantBalance int64
	}{
		{
			name: "available",
			provider: &stubArrivalAcquisitionProvider{
				quota: &weComCustomerAcquisitionQuota{Total: 1000, Balance: 500},
			},
			wantBalance: 500,
		},
		{
			name: "permission denied",
			provider: &stubArrivalAcquisitionProvider{
				quotaErr: newWeComProviderError(
					weComStageAcquisitionQuota,
					http.StatusOK,
					48002,
					"api forbidden",
					false,
				),
			},
			wantCode: "acquisition_permission_denied",
		},
		{
			name: "quota exhausted",
			provider: &stubArrivalAcquisitionProvider{
				quota: &weComCustomerAcquisitionQuota{Total: 1000, Balance: 0},
			},
			wantCode: "acquisition_quota_exhausted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &arrivalAcquisitionService{provider: tt.provider}
			quota, err := service.Preflight(&models.WeComTenantAuthorization{ID: 1})
			if tt.wantCode == "" {
				if err != nil || quota == nil || quota.Balance != tt.wantBalance {
					t.Fatalf("Preflight() quota=%#v error=%v", quota, err)
				}
				return
			}
			if err == nil || acquisitionFailureCode(err, "") != tt.wantCode {
				t.Fatalf("Preflight() error=%v want code %q", err, tt.wantCode)
			}
		})
	}
}

func TestWeComCustomerAcquisitionProviderContract(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	cacheCorpAccessToken(t, fixture, "cached-corp-token")
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
	)

	var createBody map[string]any
	var getBody map[string]any
	customerBodies := make([]map[string]any, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Query().Get("access_token") != "cached-corp-token" {
			t.Error("provider request did not use the authorization-scoped corp token")
		}
		switch req.URL.Path {
		case "/cgi-bin/externalcontact/customer_acquisition_quota":
			if req.Method != http.MethodGet {
				t.Errorf("quota method=%s want GET", req.Method)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","total":1000,"balance":500}`))
		case "/cgi-bin/externalcontact/customer_acquisition/create_link":
			if req.Method != http.MethodPost {
				t.Errorf("create method=%s want POST", req.Method)
			}
			if err := json.NewDecoder(req.Body).Decode(&createBody); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","link":{"link_id":"link-a","url":"https://work.weixin.qq.com/ca/link-a","create_time":1700000000}}`))
		case "/cgi-bin/externalcontact/customer_acquisition/get":
			if err := json.NewDecoder(req.Body).Decode(&getBody); err != nil {
				t.Errorf("decode get body: %v", err)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","link":{"link_name":"门店到店管家","url":"https://work.weixin.qq.com/ca/link-a","create_time":1700000000},"range":{"user_list":["member-a"]}}`))
		case "/cgi-bin/externalcontact/customer_acquisition/customer":
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode customer body: %v", err)
			}
			customerBodies = append(customerBodies, body)
			if len(customerBodies) == 1 {
				_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","customer_list":[{"external_userid":"external-a","userid":"member-a","chat_status":1,"state":"state-a"}],"next_cursor":"next-a"}`))
			} else {
				_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","customer_list":[{"external_userid":"external-b","userid":"member-a","chat_status":1,"state":"state-b"}]}`))
			}
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	cfg := config.Current()
	cfg.Arrival.WeComAPIBaseURL = server.URL
	config.SetCurrent(&cfg)
	provider := newWeComProviderService()

	quota, err := provider.GetCustomerAcquisitionQuota(authorization)
	if err != nil || quota == nil || quota.Total != 1000 || quota.Balance != 500 {
		t.Fatalf("quota=%#v error=%v", quota, err)
	}
	created, err := provider.CreateCustomerAcquisitionLink(authorization, fixture.memberUserID)
	if err != nil || created == nil || created.LinkID != "link-a" {
		t.Fatalf("created=%#v error=%v", created, err)
	}
	details, err := provider.GetCustomerAcquisitionLink(authorization, created.LinkID)
	if err != nil || details == nil || len(details.UserList) != 1 ||
		details.UserList[0] != fixture.memberUserID {
		t.Fatalf("details=%#v error=%v", details, err)
	}
	customers, err := provider.ListCustomerAcquisitionCustomers(authorization, created.LinkID)
	if err != nil || len(customers) != 2 {
		t.Fatalf("customers=%#v error=%v", customers, err)
	}

	if len(createBody) != 3 ||
		createBody["link_name"] != "门店到店管家" ||
		createBody["skip_verify"] != true {
		t.Fatalf("unexpected create request: %#v", createBody)
	}
	rangeValue, ok := createBody["range"].(map[string]any)
	if !ok || len(rangeValue) != 1 {
		t.Fatalf("unexpected acquisition range: %#v", createBody["range"])
	}
	users, ok := rangeValue["user_list"].([]any)
	if !ok || len(users) != 1 || users[0] != fixture.memberUserID {
		t.Fatalf("unexpected acquisition users: %#v", rangeValue["user_list"])
	}
	if len(getBody) != 1 || getBody["link_id"] != "link-a" {
		t.Fatalf("unexpected get request: %#v", getBody)
	}
	if len(customerBodies) != 2 ||
		customerBodies[0]["link_id"] != "link-a" ||
		customerBodies[0]["limit"] != float64(1000) ||
		customerBodies[0]["cursor"] != nil ||
		customerBodies[1]["cursor"] != "next-a" {
		t.Fatalf("unexpected customer pagination: %#v", customerBodies)
	}
}

func TestWeComCustomerAcquisitionProviderRejectsEmptyLinkFields(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	cacheCorpAccessToken(t, fixture, "cached-corp-token")
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
	)

	for _, response := range []string{
		`{"errcode":0,"link":{"url":"https://work.weixin.qq.com/ca/missing-id"}}`,
		`{"errcode":0,"link":{"link_id":"missing-url"}}`,
	} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
			}))
			defer server.Close()
			cfg := config.Current()
			cfg.Arrival.WeComAPIBaseURL = server.URL
			config.SetCurrent(&cfg)
			if result, err := newWeComProviderService().CreateCustomerAcquisitionLink(
				authorization,
				fixture.memberUserID,
			); err == nil || result != nil {
				t.Fatalf("empty provider link accepted: %#v error=%v", result, err)
			}
		})
	}
}

func TestArrivalAcquisitionLinkReuseIsolationAndConcurrency(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	provider := successfulArrivalAcquisitionProvider(fixture.memberUserID)
	provider.delay = 50 * time.Millisecond
	service := &arrivalAcquisitionService{provider: provider}

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_, _, _ = service.EnsureLink(
				fixture.connection,
				fixture.authorization,
				fixture.memberUserID,
				"concurrent-acquisition",
			)
		}()
	}
	wg.Wait()

	_, createCalls, _, _ := provider.counts()
	if createCalls != 1 {
		t.Fatalf("concurrent provider create calls=%d want 1", createCalls)
	}
	var count int64
	if err := fixture.db.Model(&models.ArrivalAcquisitionLink{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("acquisition link rows=%d want 1", count)
	}

	first, firstURL, err := service.EnsureLink(
		fixture.connection,
		fixture.authorization,
		fixture.memberUserID,
		"reuse-acquisition",
	)
	if err != nil || first == nil || firstURL == "" {
		t.Fatalf("reuse link=%#v url=%q error=%v", first, firstURL, err)
	}
	_, createCalls, _, _ = provider.counts()
	if createCalls != 1 {
		t.Fatalf("reuse created another official link: %d", createCalls)
	}

	siblingStore := &models.Store{
		TenantID:    fixture.tenantID,
		StoreCode:   "arrival-acquisition-b",
		Name:        "到店联动获客 B 店",
		Status:      enums.StatusOk,
		AuditFields: arrivalSystemAuditFields(time.Now()),
	}
	if err := fixture.db.Create(siblingStore).Error; err != nil {
		t.Fatal(err)
	}
	siblingConnection := &models.StoreArrivalConnection{
		TenantID:                 fixture.tenantID,
		StoreID:                  siblingStore.ID,
		StoreScene:               "arr-acquisition-b",
		TenantAuthorizationID:    fixture.authorization.ID,
		ContactMemberCiphertext:  fixture.connection.ContactMemberCiphertext,
		ContactMemberNonce:       fixture.connection.ContactMemberNonce,
		ContactMemberFingerprint: fixture.connection.ContactMemberFingerprint,
		WxWorkProtocolInstanceID: 9002,
		ConnectionStatus:         enums.ArrivalConnectionStatusActive,
		Status:                   enums.StatusOk,
		AuditFields:              arrivalSystemAuditFields(time.Now()),
	}
	if err := fixture.db.Create(siblingConnection).Error; err != nil {
		t.Fatal(err)
	}
	second, _, err := service.EnsureLink(
		siblingConnection,
		fixture.authorization,
		fixture.memberUserID,
		"isolated-acquisition",
	)
	if err != nil || second == nil || second.ID == first.ID || second.StoreID == first.StoreID {
		t.Fatalf("cross-store link isolation failed: first=%#v second=%#v error=%v", first, second, err)
	}
	_, createCalls, _, _ = provider.counts()
	if createCalls != 2 {
		t.Fatalf("cross-store official create calls=%d want 2", createCalls)
	}
}

func TestArrivalAcquisitionRetryAfterVerificationFailureReusesCreatedLink(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	provider := successfulArrivalAcquisitionProvider(fixture.memberUserID)
	provider.detailErr = newWeComProviderError(
		weComStageAcquisitionGet,
		http.StatusServiceUnavailable,
		-1,
		"temporary unavailable",
		true,
	)
	service := &arrivalAcquisitionService{provider: provider}

	first, _, err := service.EnsureLink(
		fixture.connection,
		fixture.authorization,
		fixture.memberUserID,
		"acquisition-verify-failure",
	)
	if err == nil || first != nil {
		t.Fatalf("verification failure result=%#v error=%v", first, err)
	}
	link := repositories.ArrivalRepository.FindAcquisitionLink(
		fixture.db,
		fixture.tenantID,
		fixture.authorization.ID,
		fixture.store.ID,
		fixture.connection.ContactMemberFingerprint,
	)
	if link == nil || link.ProviderLinkID != provider.created.LinkID ||
		link.LinkStatus != enums.ArrivalAcquisitionLinkStatusFailed ||
		!link.FailureRetryable {
		t.Fatalf("created provider link was not retained for recovery: %#v", link)
	}
	if err := repositories.ArrivalRepository.UpdateAcquisitionLink(
		fixture.db,
		link.ID,
		link.TenantID,
		map[string]any{"next_provision_retry_at": time.Now().Add(-time.Second)},
	); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.detailErr = nil
	provider.mu.Unlock()

	recovered, recoveredURL, err := service.EnsureLink(
		fixture.connection,
		fixture.authorization,
		fixture.memberUserID,
		"acquisition-verify-recovery",
	)
	if err != nil || recovered == nil ||
		recovered.LinkStatus != enums.ArrivalAcquisitionLinkStatusActive ||
		recoveredURL != provider.details.URL {
		t.Fatalf("recovered link=%#v url=%q error=%v", recovered, recoveredURL, err)
	}
	quotaCalls, createCalls, getCalls, _ := provider.counts()
	if quotaCalls != 1 || createCalls != 1 || getCalls != 2 {
		t.Fatalf(
			"provider calls quota=%d create=%d get=%d, want 1/1/2",
			quotaCalls,
			createCalls,
			getCalls,
		)
	}
}

func TestArrivalBootstrapCustomerAcquisitionIsIdempotentPerScan(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	cfg := config.Current()
	cfg.Arrival.ContactProvider = string(enums.ArrivalContactProviderModeCustomerAcquisition)
	config.SetCurrent(&cfg)

	provider := successfulArrivalAcquisitionProvider(fixture.memberUserID)
	payloadBuilder := &capturingArrivalPayloadQRBuilder{}
	service := &arrivalLinkService{
		loginExchanger: &stubArrivalLoginExchanger{
			result: &weChatCodeSessionResult{OpenID: "openid-a", UnionID: "union-a"},
		},
		payloadQRBuilder: payloadBuilder,
		acquisition:      &arrivalAcquisitionService{provider: provider},
	}
	firstRequest := request.ArrivalBootstrapRequest{
		SchemaVersion: arrivalScanInputVersion,
		LoginCode:     "login-code-acquisition-a",
		Scene:         fixture.connection.StoreScene,
		ScanEventID:   "scan-acquisition-a",
	}
	first, err := service.BootstrapWithRequestID(firstRequest, "acquisition-bootstrap-a")
	if err != nil || first == nil || !first.ContactWay.Available {
		t.Fatalf("first acquisition bootstrap=%#v error=%v", first, err)
	}
	repeated, err := service.BootstrapWithRequestID(firstRequest, "acquisition-bootstrap-a-retry")
	if err != nil || repeated.ContactWay.QRCodeURL != first.ContactWay.QRCodeURL ||
		repeated.SessionToken != first.SessionToken {
		t.Fatalf("idempotent acquisition retry=%#v error=%v", repeated, err)
	}
	secondRequest := firstRequest
	secondRequest.LoginCode = "login-code-acquisition-b"
	secondRequest.ScanEventID = "scan-acquisition-b"
	second, err := service.BootstrapWithRequestID(secondRequest, "acquisition-bootstrap-b")
	if err != nil || second == nil || !second.ContactWay.Available ||
		second.ContactWay.QRCodeURL == first.ContactWay.QRCodeURL {
		t.Fatalf("second acquisition bootstrap=%#v error=%v", second, err)
	}

	payloads := payloadBuilder.values()
	if len(payloads) != 2 {
		t.Fatalf("QR artifact calls=%d want 2", len(payloads))
	}
	firstURL, err := url.Parse(payloads[0])
	if err != nil {
		t.Fatal(err)
	}
	secondURL, err := url.Parse(payloads[1])
	if err != nil {
		t.Fatal(err)
	}
	firstChannel := firstURL.Query().Get("customer_channel")
	secondChannel := secondURL.Query().Get("customer_channel")
	if firstChannel == "" || secondChannel == "" ||
		firstChannel == secondChannel ||
		len([]byte(firstChannel)) > 64 ||
		len([]byte(secondChannel)) > 64 {
		t.Fatalf("customer channels first=%q second=%q", firstChannel, secondChannel)
	}
	channelPattern := regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
	for _, channel := range []string{firstChannel, secondChannel} {
		if !channelPattern.MatchString(channel) || len(channel) != 51 {
			t.Fatalf("customer channel is not opaque alphanumeric data: %q", channel)
		}
	}
	distantIDChannel := fixture.security.AcquisitionContactState(123456789)
	if !channelPattern.MatchString(distantIDChannel) ||
		len(distantIDChannel) != len(firstChannel) ||
		distantIDChannel == firstChannel {
		t.Fatalf("customer channel shape depends on a sequential record ID: %q", distantIDChannel)
	}
	_, createCalls, _, _ := provider.counts()
	if createCalls != 1 {
		t.Fatalf("two scans for one store created %d official links", createCalls)
	}
}

func TestArrivalPayloadQRCodePreservesExactLinkAndFallsBack(t *testing.T) {
	payload := "https://work.weixin.qq.com/ca/arrival-link?customer_channel=opaque123"
	service := newArrivalQRCodeService()
	artifact, err := service.BuildPayloadArtifact(payload)
	if err != nil {
		t.Fatalf("build acquisition QR: %v", err)
	}
	assertArrivalArtifactPayload(t, artifact, payload)

	service.payloadArtworkRenderer = func(string) (image.Image, error) {
		return image.NewNRGBA(image.Rect(0, 0, 8, 8)), nil
	}
	fallback, err := service.BuildPayloadArtifact(payload)
	if err != nil {
		t.Fatalf("build fallback QR: %v", err)
	}
	if fallback.ArtworkVerified {
		t.Fatal("invalid artwork did not fall back to the standard QR")
	}
	if fallback.PublishedPNGBase64 != fallback.OriginalPNGBase64 {
		t.Fatal("fallback did not publish the verified standard QR")
	}
	assertArrivalArtifactPayload(t, fallback, payload)
}

func TestArrivalAcquisitionCustomerReconciliationCreatesScopedLegacyBinding(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	provider := successfulArrivalAcquisitionProvider(fixture.memberUserID)
	service := &arrivalAcquisitionService{provider: provider}
	link, _, err := service.EnsureLink(
		fixture.connection,
		fixture.authorization,
		fixture.memberUserID,
		"acquisition-reconcile-link",
	)
	if err != nil {
		t.Fatal(err)
	}
	state := fixture.security.AcquisitionContactState(fixture.contactWay.ID)
	if err := repositories.ArrivalRepository.UpdateContactWay(
		fixture.db,
		fixture.contactWay.ID,
		fixture.tenantID,
		map[string]any{
			"provider_mode":       enums.ArrivalContactProviderModeCustomerAcquisition,
			"acquisition_link_id": link.ID,
			"contact_state_hash": fixture.security.Fingerprint(
				"contact_state",
				state,
			),
			"contact_way_status": enums.ArrivalContactWayStatusActive,
			"expires_at":         time.Now().Add(time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	provider.customers = []weComCustomerAcquisitionCustomer{
		{
			ExternalUserID: fixture.externalID,
			UserID:         fixture.memberUserID,
			ChatStatus:     1,
			State:          state,
		},
		{
			ExternalUserID: "must-not-map",
			UserID:         fixture.memberUserID,
			ChatStatus:     1,
			State:          "unknown-state",
		},
	}
	reconciled, err := service.reconcileLinkCustomers(link)
	if err != nil || reconciled != 1 {
		t.Fatalf("reconciled=%d error=%v", reconciled, err)
	}
	binding := repositories.ArrivalRepository.FindBinding(
		fixture.db,
		fixture.tenantID,
		fixture.identity.ID,
		fixture.store.ID,
	)
	if binding == nil ||
		binding.BindingStatus != enums.ArrivalBindingStatusLegacyUnmapped ||
		binding.OfficialRelationStatus != enums.ArrivalOfficialRelationStatusConfirmed ||
		binding.ExternalUserIDFingerprint != fixture.security.Fingerprint(
			"external_user_id",
			fixture.externalID,
		) ||
		binding.ContactMemberFingerprint != fixture.connection.ContactMemberFingerprint ||
		binding.ExternalUserIDCiphertext == fixture.externalID ||
		binding.ContactMemberCiphertext == fixture.memberUserID {
		t.Fatalf("unexpected reconciled binding: %#v", binding)
	}
	var count int64
	if err := fixture.db.Model(&models.ArrivalStoreBinding{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("out-of-scope customer created a binding: rows=%d", count)
	}
}

func TestArrivalAcquisitionFailureDiagnosticsDoNotLeakSecrets(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	rawMember := fixture.memberUserID
	rawToken := "secret-token-value-012345678901234567890123456789"
	provider := successfulArrivalAcquisitionProvider(rawMember)
	provider.quota = nil
	provider.quotaErr = newWeComProviderError(
		weComStageAcquisitionQuota,
		http.StatusOK,
		48002,
		"api forbidden userid="+rawMember+" access_token="+rawToken,
		false,
	)
	service := &arrivalAcquisitionService{provider: provider}

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	_, _, err := service.EnsureLink(
		fixture.connection,
		fixture.authorization,
		rawMember,
		"acquisition-secret-scan",
	)
	if err == nil || acquisitionFailureCode(err, "") != "acquisition_permission_denied" {
		t.Fatalf("unexpected acquisition error: %v", err)
	}
	link := repositories.ArrivalRepository.FindAcquisitionLink(
		sqls.DB(),
		fixture.tenantID,
		fixture.authorization.ID,
		fixture.store.ID,
		fixture.connection.ContactMemberFingerprint,
	)
	if link == nil || link.FailureCode != "acquisition_permission_denied" {
		t.Fatalf("missing persisted acquisition diagnostics: %#v", link)
	}
	serialized, marshalErr := json.Marshal(link)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	surfaces := strings.Join([]string{
		string(serialized),
		logs.String(),
		err.Error(),
	}, "\n")
	for _, forbidden := range []string{rawMember, rawToken, "access_token=secret"} {
		if strings.Contains(surfaces, forbidden) {
			t.Fatalf("acquisition diagnostics leaked %q", forbidden)
		}
	}
}

func assertArrivalArtifactPayload(
	t *testing.T,
	artifact *arrivalQRCodeArtifact,
	want string,
) {
	t.Helper()
	if artifact == nil {
		t.Fatal("QR artifact is nil")
	}
	raw, err := base64.StdEncoding.DecodeString(artifact.PublishedPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := decodeSingleQRCodePayload(compositeArrivalQRForVerification(img))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != want {
		t.Fatalf("QR payload=%q want %q", string(payload), want)
	}
}
