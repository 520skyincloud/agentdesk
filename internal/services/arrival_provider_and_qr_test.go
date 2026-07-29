package services

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	qrcode "github.com/skip2/go-qrcode"
)

func TestWeComProviderRefreshesTokensAndCreatesScene2ContactWay(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	ticketCiphertext, ticketNonce, err := fixture.security.Encrypt("suite_ticket", "suite-ticket-value")
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.ArrivalRepository.UpdateSuiteCredential(fixture.db, fixture.authorization.SuiteCredentialID, map[string]any{
		"suite_ticket_ciphertext": ticketCiphertext,
		"suite_ticket_nonce":      ticketNonce,
	}); err != nil {
		t.Fatal(err)
	}
	permanentCiphertext, permanentNonce, err := fixture.security.Encrypt("permanent_code", "permanent-code-value")
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.ArrivalRepository.UpdateTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
		map[string]any{
			"permanent_code_ciphertext": permanentCiphertext,
			"permanent_code_nonce":      permanentNonce,
		},
	); err != nil {
		t.Fatal(err)
	}

	var suiteTokenCalls int
	var corpTokenCalls int
	var addContactWayCalls int
	var contactWayBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/cgi-bin/service/get_suite_token":
			suiteTokenCalls++
			_, _ = w.Write([]byte(`{"errcode":0,"suite_access_token":"suite-access-token","expires_in":7200}`))
		case "/cgi-bin/service/get_corp_token":
			corpTokenCalls++
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"corp-access-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/add_contact_way":
			addContactWayCalls++
			if err := json.NewDecoder(req.Body).Decode(&contactWayBody); err != nil {
				t.Errorf("decode add_contact_way body: %v", err)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"config_id":"official-config-scene-2","qr_code":"https://wework.qpic.cn/official-arrival.png"}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	cfg := config.Current()
	cfg.Arrival.WeComAPIBaseURL = server.URL
	config.SetCurrent(&cfg)

	authorization := repositories.ArrivalRepository.GetTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
	)
	provider := newWeComProviderService()
	firstToken, err := provider.GetCorpAccessToken(authorization)
	if err != nil {
		t.Fatalf("refresh corp access token: %v", err)
	}
	secondToken, err := provider.GetCorpAccessToken(authorization)
	if err != nil {
		t.Fatalf("read cached corp access token: %v", err)
	}
	if firstToken != "corp-access-token" || secondToken != firstToken ||
		suiteTokenCalls != 1 || corpTokenCalls != 1 {
		t.Fatalf(
			"token results first=%q second=%q suiteCalls=%d corpCalls=%d",
			firstToken,
			secondToken,
			suiteTokenCalls,
			corpTokenCalls,
		)
	}

	contactWay, err := provider.AddContactWay(authorization, fixture.memberUserID, "opaque-arrival-state")
	if err != nil {
		t.Fatalf("create scene=2 contact way: %v", err)
	}
	if contactWay.ConfigID != "official-config-scene-2" ||
		contactWay.QRCode != "https://wework.qpic.cn/official-arrival.png" ||
		addContactWayCalls != 1 {
		t.Fatalf("unexpected contact way response: %#v calls=%d", contactWay, addContactWayCalls)
	}
	if contactWayBody["type"] != float64(1) ||
		contactWayBody["scene"] != float64(2) ||
		contactWayBody["state"] != "opaque-arrival-state" ||
		contactWayBody["skip_verify"] != true {
		t.Fatalf("unexpected add_contact_way contract: %#v", contactWayBody)
	}
	users, ok := contactWayBody["user"].([]any)
	if !ok || len(users) != 1 || users[0] != fixture.memberUserID {
		t.Fatalf("unexpected add_contact_way members: %#v", contactWayBody["user"])
	}
	if _, err := provider.AddContactWay(authorization, fixture.memberUserID, strings.Repeat("x", 31)); err == nil {
		t.Fatal("contact state longer than 30 bytes must fail")
	}
	if addContactWayCalls != 1 {
		t.Fatal("invalid contact state reached the official provider")
	}

	storedAuthorization := repositories.ArrivalRepository.GetTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
	)
	suite := repositories.ArrivalRepository.FindSuiteCredential(fixture.db, config.Current().Arrival.WeComSuiteID)
	if storedAuthorization == nil || suite == nil ||
		storedAuthorization.CorpAccessTokenCiphertext == "" ||
		storedAuthorization.CorpAccessTokenCiphertext == firstToken ||
		suite.SuiteAccessTokenCiphertext == "" ||
		suite.SuiteAccessTokenCiphertext == "suite-access-token" {
		t.Fatal("provider access tokens were not encrypted at rest")
	}
}

func TestWeComProviderPreservesSanitizedBusinessError(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	cacheCorpAccessToken(t, fixture, "cached-corp-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path != "/cgi-bin/externalcontact/add_contact_way" {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write([]byte(`{
			"errcode":48002,
			"errmsg":"api forbidden userid: member-a access_token: secret-token-value-012345678901234567890123456789 https://private.example/path?token=hidden"
		}`))
	}))
	defer server.Close()
	cfg := config.Current()
	cfg.Arrival.WeComAPIBaseURL = server.URL
	config.SetCurrent(&cfg)

	authorization := repositories.ArrivalRepository.GetTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
	)
	_, err := newWeComProviderService().AddContactWay(
		authorization,
		fixture.memberUserID,
		"opaque-error-state",
	)
	providerErr := asWeComProviderError(err)
	if providerErr == nil ||
		providerErr.Stage != weComStageAddContactWay ||
		providerErr.HTTPStatus != http.StatusOK ||
		providerErr.ErrCode != 48002 ||
		providerErr.Retryable {
		t.Fatalf("unexpected provider error: %#v", providerErr)
	}
	for _, forbidden := range []string{
		fixture.memberUserID,
		"secret-token-value",
		"private.example",
	} {
		if strings.Contains(providerErr.ErrMsg, forbidden) || strings.Contains(err.Error(), forbidden) {
			t.Fatalf("provider error leaked %q: %#v", forbidden, providerErr)
		}
	}
	if !strings.Contains(providerErr.ErrMsg, "api forbidden") {
		t.Fatalf("sanitized provider message lost diagnosis: %q", providerErr.ErrMsg)
	}
}

func TestWeComProviderRefreshesInvalidCorpTokenExactlyOnce(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	cacheCorpAccessToken(t, fixture, "expired-corp-token")
	cacheSuiteTicketAndPermanentCode(t, fixture)

	var suiteTokenCalls int
	var corpTokenCalls int
	var addContactWayCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/cgi-bin/service/get_suite_token":
			suiteTokenCalls++
			_, _ = w.Write([]byte(`{"errcode":0,"suite_access_token":"suite-token-refreshed","expires_in":7200}`))
		case "/cgi-bin/service/get_corp_token":
			corpTokenCalls++
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"corp-token-refreshed","expires_in":7200}`))
		case "/cgi-bin/externalcontact/add_contact_way":
			addContactWayCalls++
			switch req.URL.Query().Get("access_token") {
			case "expired-corp-token":
				_, _ = w.Write([]byte(`{"errcode":40014,"errmsg":"invalid access_token"}`))
			case "corp-token-refreshed":
				_, _ = w.Write([]byte(`{"errcode":0,"config_id":"refreshed-config","qr_code":"https://wework.qpic.cn/refreshed.png"}`))
			default:
				_, _ = w.Write([]byte(`{"errcode":40014,"errmsg":"unexpected access_token"}`))
			}
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	cfg := config.Current()
	cfg.Arrival.WeComAPIBaseURL = server.URL
	config.SetCurrent(&cfg)

	authorization := repositories.ArrivalRepository.GetTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
	)
	result, err := newWeComProviderService().AddContactWay(
		authorization,
		fixture.memberUserID,
		"opaque-refresh-state",
	)
	if err != nil {
		t.Fatalf("refresh invalid corp token: %v", err)
	}
	if result.ConfigID != "refreshed-config" ||
		suiteTokenCalls != 1 ||
		corpTokenCalls != 1 ||
		addContactWayCalls != 2 {
		t.Fatalf(
			"result=%#v suiteCalls=%d corpCalls=%d addCalls=%d",
			result,
			suiteTokenCalls,
			corpTokenCalls,
			addContactWayCalls,
		)
	}
}

func TestArrivalPersistsOfficialCorpTokenFailure(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	cacheSuiteTicketAndPermanentCode(t, fixture)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/cgi-bin/service/get_suite_token":
			_, _ = w.Write([]byte(`{"errcode":0,"suite_access_token":"suite-token-for-corp-failure","expires_in":7200}`))
		case "/cgi-bin/service/get_corp_token":
			_, _ = w.Write([]byte(`{
				"errcode":40084,
				"errmsg":"invalid permanent_code: secret-permanent-code-012345678901234567890123456789"
			}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	cfg := config.Current()
	cfg.Arrival.WeComAPIBaseURL = server.URL
	config.SetCurrent(&cfg)

	service := &arrivalLinkService{
		loginExchanger: &stubArrivalLoginExchanger{
			result: &weChatCodeSessionResult{OpenID: "openid-a"},
		},
		contactWayCreator: newWeComProviderService(),
		qrCodeBuilder: &stubArrivalQRCodeBuilder{
			result: successfulArrivalQRCodeArtifact(),
		},
	}
	req := arrivalContactWayTestRequest("official-corp-token-failure")
	result, err := service.BootstrapWithRequestID(req, "official-corp-token-failure-request")
	if err != nil {
		t.Fatal(err)
	}
	if result.ContactWay.Available {
		t.Fatal("corp token failure returned fake contact way success")
	}
	contactWay := findArrivalContactWayByRequest(t, fixture, req)
	if contactWay.FailureCode != "corp_token_failed" ||
		contactWay.FailureStage != weComStageCorpToken ||
		contactWay.ProviderErrorCode != 40084 ||
		contactWay.ProviderHTTPStatus != http.StatusOK ||
		contactWay.FailureRetryable {
		t.Fatalf("official corp token failure diagnostics=%#v", contactWay)
	}
	if strings.Contains(contactWay.ProviderErrorMessage, "secret-permanent-code") {
		t.Fatal("official corp token error leaked permanent code")
	}
}

func TestWeComProviderCorpTokenCacheIsAuthorizationScoped(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	cacheCorpAccessToken(t, fixture, "corp-token-a")
	corpCiphertext, corpNonce, err := fixture.security.Encrypt("corp_id", "ww-arrival-corp-b")
	if err != nil {
		t.Fatal(err)
	}
	permanentCiphertext, permanentNonce, err := fixture.security.Encrypt("permanent_code", "permanent-code-b")
	if err != nil {
		t.Fatal(err)
	}
	tokenCiphertext, tokenNonce, err := fixture.security.Encrypt("corp_access_token", "corp-token-b")
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(time.Hour)
	authorizationB := &models.WeComTenantAuthorization{
		TenantID:                  fixture.tenantID,
		SuiteCredentialID:         fixture.authorization.SuiteCredentialID,
		CorpIDCiphertext:          corpCiphertext,
		CorpIDNonce:               corpNonce,
		CorpIDFingerprint:         fixture.security.Fingerprint("corp_id", "ww-arrival-corp-b"),
		CorpName:                  "测试企微主体 B",
		PermanentCodeCiphertext:   permanentCiphertext,
		PermanentCodeNonce:        permanentNonce,
		CorpAccessTokenCiphertext: tokenCiphertext,
		CorpAccessTokenNonce:      tokenNonce,
		CorpAccessTokenExpiresAt:  &expiresAt,
		AuthorizationStatus:       enums.WeComAuthorizationStatusActive,
		AuditFields:               arrivalSystemAuditFields(time.Now()),
	}
	if err := repositories.ArrivalRepository.CreateTenantAuthorization(fixture.db, authorizationB); err != nil {
		t.Fatal(err)
	}
	authorizationA := repositories.ArrivalRepository.GetTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
	)
	provider := newWeComProviderService()
	tokenA, err := provider.GetCorpAccessToken(authorizationA)
	if err != nil {
		t.Fatal(err)
	}
	tokenB, err := provider.GetCorpAccessToken(authorizationB)
	if err != nil {
		t.Fatal(err)
	}
	if tokenA != "corp-token-a" || tokenB != "corp-token-b" {
		t.Fatalf("authorization token cache crossed subjects: A=%q B=%q", tokenA, tokenB)
	}
}

func cacheCorpAccessToken(t *testing.T, fixture arrivalLinkTestFixture, token string) {
	t.Helper()
	ciphertext, nonce, err := fixture.security.Encrypt("corp_access_token", token)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(time.Hour)
	if err := repositories.ArrivalRepository.UpdateTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
		map[string]any{
			"corp_access_token_ciphertext": ciphertext,
			"corp_access_token_nonce":      nonce,
			"corp_access_token_expires_at": expiresAt,
		},
	); err != nil {
		t.Fatal(err)
	}
}

func cacheSuiteTicketAndPermanentCode(t *testing.T, fixture arrivalLinkTestFixture) {
	t.Helper()
	ticketCiphertext, ticketNonce, err := fixture.security.Encrypt("suite_ticket", "suite-ticket-value")
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.ArrivalRepository.UpdateSuiteCredential(
		fixture.db,
		fixture.authorization.SuiteCredentialID,
		map[string]any{
			"suite_ticket_ciphertext": ticketCiphertext,
			"suite_ticket_nonce":      ticketNonce,
		},
	); err != nil {
		t.Fatal(err)
	}
	permanentCiphertext, permanentNonce, err := fixture.security.Encrypt("permanent_code", "permanent-code-value")
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.ArrivalRepository.UpdateTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
		map[string]any{
			"permanent_code_ciphertext": permanentCiphertext,
			"permanent_code_nonce":      permanentNonce,
		},
	); err != nil {
		t.Fatal(err)
	}
}

func TestArrivalQRCodeArtifactPreservesPayloadAndRejectsInvalidSources(t *testing.T) {
	payload := "https://work.weixin.qq.com/ca/cawcde-arrival-payload"
	source, err := qrcode.Encode(payload, qrcode.Highest, 512)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := buildArrivalQRCodeArtifact(source)
	if err != nil {
		t.Fatalf("build QR artifact: %v", err)
	}
	expectedHash := sha256.Sum256([]byte(payload))
	if artifact.PayloadHash != hex.EncodeToString(expectedHash[:]) {
		t.Fatalf("payload hash=%q", artifact.PayloadHash)
	}
	published, err := base64.StdEncoding.DecodeString(artifact.PublishedPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	publishedImage, _, err := image.Decode(bytes.NewReader(published))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSingleQRCodePayload(compositeArrivalQRForVerification(publishedImage))
	if err != nil {
		t.Fatalf("decode published QR: %v", err)
	}
	if string(decoded) != payload {
		t.Fatalf("published payload=%q want %q", string(decoded), payload)
	}
	if _, err := buildArrivalQRCodeArtifact([]byte("not-an-image")); err == nil {
		t.Fatal("invalid QR image must fail")
	}

	service := newArrivalQRCodeService()
	for _, rawURL := range []string{
		"http://wework.qpic.cn/test.png",
		"https://127.0.0.1/test.png",
		"https://localhost/test.png",
		"https://attacker.example/test.png",
	} {
		if err := service.validateRemoteURL(mustParseArrivalURL(t, rawURL)); err == nil {
			t.Fatalf("untrusted QR URL accepted: %s", rawURL)
		}
	}
	if err := service.validateRemoteURL(mustParseArrivalURL(t, "https://sub.wework.qpic.cn/test.png")); err != nil {
		t.Fatalf("official QR host rejected: %v", err)
	}
}

func mustParseArrivalURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
