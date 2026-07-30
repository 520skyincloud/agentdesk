package services

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type arrivalLinkTestFixture struct {
	db            *gorm.DB
	security      *arrivalSecurity
	tenantID      int64
	store         *models.Store
	identity      *models.MiniProgramIdentity
	authorization *models.WeComTenantAuthorization
	connection    *models.StoreArrivalConnection
	scanEvent     *models.ArrivalScanEvent
	sessionToken  string
	contactWay    *models.ArrivalContactWay
	contactState  string
	corpID        string
	memberUserID  string
	externalID    string
}

type retryingArrivalContactWayProvider struct {
	calls int
}

type stubArrivalLoginExchanger struct {
	result *weChatCodeSessionResult
	err    error
	calls  int
}

func (s *stubArrivalLoginExchanger) ExchangeMiniProgramLoginCode(string) (*weChatCodeSessionResult, error) {
	s.calls++
	return s.result, s.err
}

type stubArrivalContactWayCreator struct {
	result *weComContactWayResult
	err    error
	calls  int
}

func (s *stubArrivalContactWayCreator) AddContactWay(*models.WeComTenantAuthorization, string, string) (*weComContactWayResult, error) {
	s.calls++
	return s.result, s.err
}

type stubArrivalQRCodeBuilder struct {
	result *arrivalQRCodeArtifact
	err    error
	calls  int
}

func (s *stubArrivalQRCodeBuilder) BuildArtifact(string) (*arrivalQRCodeArtifact, error) {
	s.calls++
	return s.result, s.err
}

type stubArrivalCardSender struct {
	status             enums.ArrivalDeliveryStatus
	err                error
	calls              int
	lastConversationID int64
	lastInstanceID     int64
	lastClientMsgID    string
}

func (s *stubArrivalCardSender) SendArrivalCard(
	conversationID, instanceID int64,
	clientMsgID string,
) (enums.ArrivalDeliveryStatus, error) {
	s.calls++
	s.lastConversationID = conversationID
	s.lastInstanceID = instanceID
	s.lastClientMsgID = clientMsgID
	return s.status, s.err
}

func (p *retryingArrivalContactWayProvider) DeleteContactWay(*models.WeComTenantAuthorization, string) error {
	p.calls++
	if p.calls == 1 {
		return errors.New("temporary official API failure")
	}
	return nil
}

func TestArrivalLifecycleKeepsStatusReadOnlyAndStoreBindingsIsolated(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)

	provider := &retryingArrivalContactWayProvider{}
	maintenance := &arrivalMaintenanceService{provider: provider}
	if cleaned := maintenance.CleanupExpiredContactWays(10); cleaned != 0 {
		t.Fatalf("first cleanup count=%d want 0 while official deletion fails", cleaned)
	}
	failedCleanup := repositories.ArrivalRepository.GetContactWay(fixture.db, fixture.contactWay.ID, fixture.tenantID)
	if failedCleanup == nil || failedCleanup.ContactWayStatus != enums.ArrivalContactWayStatusExpired ||
		failedCleanup.FailureCode != "official_delete_failed" {
		t.Fatalf("failed cleanup state=%#v", failedCleanup)
	}
	if cleaned := maintenance.CleanupExpiredContactWays(10); cleaned != 1 {
		t.Fatalf("retry cleanup count=%d want 1", cleaned)
	}
	cleanedContactWay := repositories.ArrivalRepository.GetContactWay(fixture.db, fixture.contactWay.ID, fixture.tenantID)
	if cleanedContactWay == nil || cleanedContactWay.ContactWayStatus != enums.ArrivalContactWayStatusCleaned ||
		cleanedContactWay.OriginalQRCodeCiphertext != "" ||
		cleanedContactWay.OriginalPNGBase64 != "" ||
		cleanedContactWay.ArtworkPNGBase64 != "" ||
		cleanedContactWay.CleanedAt == nil {
		t.Fatalf("cleaned contact way retained public QR material: %#v", cleanedContactWay)
	}

	addPayload := fmt.Sprintf(
		"<xml><ToUserName>%s</ToUserName><CreateTime>%d</CreateTime><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><UserID>%s</UserID><ExternalUserID>%s</ExternalUserID><State>%s</State></xml>",
		fixture.corpID,
		time.Now().Unix(),
		fixture.memberUserID,
		fixture.externalID,
		fixture.contactState,
	)
	signature, timestamp, nonce, body := buildArrivalCallbackRequest(t, fixture.corpID, addPayload)
	if err := WeComProviderCallbackService.Handle("data", signature, timestamp, nonce, body); err != nil {
		t.Fatalf("handle late relationship callback: %v", err)
	}
	if err := WeComProviderCallbackService.Handle("data", signature, timestamp, nonce, body); err != nil {
		t.Fatalf("handle duplicate relationship callback: %v", err)
	}

	var callbackCount int64
	if err := fixture.db.Model(&models.WeComProviderCallbackEvent{}).Count(&callbackCount).Error; err != nil {
		t.Fatalf("count callback events: %v", err)
	}
	if callbackCount != 1 {
		t.Fatalf("callback idempotency rows=%d want 1", callbackCount)
	}
	binding := repositories.ArrivalRepository.FindBinding(
		fixture.db,
		fixture.tenantID,
		fixture.identity.ID,
		fixture.store.ID,
	)
	if binding == nil ||
		binding.OfficialRelationStatus != enums.ArrivalOfficialRelationStatusConfirmed ||
		binding.BindingStatus != enums.ArrivalBindingStatusLegacyUnmapped ||
		binding.ConversationID != 0 {
		t.Fatalf("stage A must remain deterministically unmapped: %#v", binding)
	}

	var auditsBeforeStatus int64
	if err := fixture.db.Model(&models.ArrivalAuditLog{}).Count(&auditsBeforeStatus).Error; err != nil {
		t.Fatalf("count audit logs before status: %v", err)
	}
	eventBeforeStatus := repositories.ArrivalRepository.GetScanEvent(fixture.db, fixture.scanEvent.ID, fixture.tenantID)
	result, err := ArrivalLinkService.Status(fixture.sessionToken)
	if err != nil {
		t.Fatalf("read arrival status: %v", err)
	}
	if result.BindingStatus != string(enums.ArrivalBindingStatusLegacyUnmapped) ||
		result.DeliveryStatus != string(enums.ArrivalDeliveryStatusNotBound) ||
		result.ContactWay.Available {
		t.Fatalf("unexpected late-callback status: %#v", result)
	}
	eventAfterStatus := repositories.ArrivalRepository.GetScanEvent(fixture.db, fixture.scanEvent.ID, fixture.tenantID)
	var auditsAfterStatus int64
	_ = fixture.db.Model(&models.ArrivalAuditLog{}).Count(&auditsAfterStatus).Error
	if eventBeforeStatus == nil || eventAfterStatus == nil ||
		!eventBeforeStatus.UpdatedAt.Equal(eventAfterStatus.UpdatedAt) ||
		auditsBeforeStatus != auditsAfterStatus {
		t.Fatal("GET /status mutated persisted arrival state")
	}

	otherBinding := createArrivalSiblingStoreBinding(t, fixture)
	missingMemberPayload := &weComProviderCallbackPayload{
		ToUserName:     fixture.corpID,
		ExternalUserID: fixture.externalID,
	}
	if err := WeComProviderCallbackService.invalidateOfficialRelationship(
		missingMemberPayload,
		&models.WeComProviderCallbackEvent{},
	); err == nil {
		t.Fatal("relationship deletion without member UserID must be rejected")
	}

	deletePayload := fmt.Sprintf(
		"<xml><ToUserName>%s</ToUserName><CreateTime>%d</CreateTime><Event>change_external_contact</Event><ChangeType>del_external_contact</ChangeType><UserID>%s</UserID><ExternalUserID>%s</ExternalUserID></xml>",
		fixture.corpID,
		time.Now().Unix(),
		fixture.memberUserID,
		fixture.externalID,
	)
	deleteSignature, deleteTimestamp, deleteNonce, deleteBody := buildArrivalCallbackRequest(t, fixture.corpID, deletePayload)
	if err := WeComProviderCallbackService.Handle("data", deleteSignature, deleteTimestamp, deleteNonce, deleteBody); err != nil {
		t.Fatalf("handle relationship deletion: %v", err)
	}
	binding = repositories.ArrivalRepository.GetBinding(fixture.db, binding.ID, fixture.tenantID)
	if binding == nil ||
		binding.OfficialRelationStatus != enums.ArrivalOfficialRelationStatusRevoked ||
		binding.BindingStatus != enums.ArrivalBindingStatusUnbound {
		t.Fatalf("deleted relationship remains active: %#v", binding)
	}
	otherBinding = repositories.ArrivalRepository.GetBinding(fixture.db, otherBinding.ID, fixture.tenantID)
	if otherBinding == nil ||
		otherBinding.OfficialRelationStatus != enums.ArrivalOfficialRelationStatusConfirmed ||
		otherBinding.BindingStatus != enums.ArrivalBindingStatusBound {
		t.Fatalf("deleting one member relationship changed a sibling store: %#v", otherBinding)
	}

	result, err = ArrivalLinkService.Status(fixture.sessionToken)
	if err != nil {
		t.Fatalf("read status after relationship deletion: %v", err)
	}
	if result.BindingStatus != string(enums.ArrivalBindingStatusUnbound) {
		t.Fatalf("status binding=%q want unbound after current relationship was revoked", result.BindingStatus)
	}
	storedEvent := repositories.ArrivalRepository.GetScanEvent(fixture.db, fixture.scanEvent.ID, fixture.tenantID)
	if storedEvent == nil || storedEvent.BindingStatus != enums.ArrivalBindingStatusLegacyUnmapped {
		t.Fatal("status read must not rewrite the original scan event snapshot")
	}
}

func TestArrivalBootstrapValidatesInputAndPreservesIdempotency(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	login := &stubArrivalLoginExchanger{
		result: &weChatCodeSessionResult{OpenID: "openid-a", UnionID: "union-a"},
	}
	contactWay := &stubArrivalContactWayCreator{
		result: &weComContactWayResult{
			ConfigID: "official-config-new",
			QRCode:   "https://wework.qpic.cn/arrival-test.png",
		},
	}
	qrBuilder := &stubArrivalQRCodeBuilder{
		result: &arrivalQRCodeArtifact{
			OriginalPNGBase64:  base64.StdEncoding.EncodeToString([]byte("official-png")),
			PublishedPNGBase64: base64.StdEncoding.EncodeToString([]byte("artwork-png")),
			PayloadHash:        strings.Repeat("a", 64),
			ArtworkVerified:    true,
		},
	}
	service := &arrivalLinkService{
		loginExchanger:    login,
		contactWayCreator: contactWay,
		qrCodeBuilder:     qrBuilder,
	}

	invalidScene := request.ArrivalBootstrapRequest{
		SchemaVersion: arrivalScanInputVersion,
		LoginCode:     "login-code-invalid-scene",
		Scene:         "missing-scene",
		ScanEventID:   "scan-invalid-scene",
	}
	if _, err := service.Bootstrap(invalidScene); err == nil {
		t.Fatal("invalid scene must fail")
	}
	if login.calls != 0 {
		t.Fatalf("invalid scene exchanged login code %d times", login.calls)
	}

	login.err = errors.New("login exchange unavailable")
	loginFailure := request.ArrivalBootstrapRequest{
		SchemaVersion: arrivalScanInputVersion,
		LoginCode:     "login-code-failure",
		Scene:         fixture.connection.StoreScene,
		ScanEventID:   "scan-login-failure",
	}
	if _, err := service.Bootstrap(loginFailure); err == nil {
		t.Fatal("login exchange failure must fail")
	}
	login.err = nil

	var scansBefore int64
	if err := fixture.db.Model(&models.ArrivalScanEvent{}).Count(&scansBefore).Error; err != nil {
		t.Fatal(err)
	}
	req := request.ArrivalBootstrapRequest{
		SchemaVersion: arrivalScanInputVersion,
		LoginCode:     "login-code-unbound",
		Scene:         fixture.connection.StoreScene,
		ScanEventID:   "scan-unbound-idempotent",
	}
	first, err := service.Bootstrap(req)
	if err != nil {
		t.Fatalf("bootstrap first unbound scan: %v", err)
	}
	if first.SchemaVersion != arrivalScanResultVersion ||
		first.IdentityStatus != string(enums.ArrivalIdentityStatusMatched) ||
		first.BindingStatus != string(enums.ArrivalBindingStatusUnbound) ||
		first.DeliveryStatus != string(enums.ArrivalDeliveryStatusNotBound) ||
		!first.ContactWay.Available ||
		first.ContactWay.Mode != string(enums.ArrivalContactWayModeQRCode) ||
		!strings.HasPrefix(first.ContactWay.QRCodeURL, "https://") {
		t.Fatalf("unexpected first unbound result: %#v", first)
	}
	if contactWay.calls != 1 || qrBuilder.calls != 1 {
		t.Fatalf("contact way provision calls=%d qr calls=%d want 1/1", contactWay.calls, qrBuilder.calls)
	}
	second, err := service.Bootstrap(req)
	if err != nil {
		t.Fatalf("repeat idempotent bootstrap: %v", err)
	}
	if second.SessionToken != first.SessionToken || second.ContactWay.QRCodeURL != first.ContactWay.QRCodeURL {
		t.Fatal("idempotent bootstrap did not return the original session and QR")
	}
	if contactWay.calls != 1 || qrBuilder.calls != 1 {
		t.Fatal("idempotent bootstrap provisioned a duplicate contact way")
	}
	var scansAfter int64
	if err := fixture.db.Model(&models.ArrivalScanEvent{}).Count(&scansAfter).Error; err != nil {
		t.Fatal(err)
	}
	if scansAfter != scansBefore+1 {
		t.Fatalf("scan rows=%d want %d", scansAfter, scansBefore+1)
	}

	reused := req
	reused.LoginCode = "different-login-code"
	if _, err := service.Bootstrap(reused); err == nil {
		t.Fatal("same scanEventId with a different request must be rejected")
	}

	resourceToken := first.ContactWay.QRCodeURL[strings.LastIndex(first.ContactWay.QRCodeURL, "/")+1:]
	raw, err := service.PublicQRCode(resourceToken)
	if err != nil {
		t.Fatalf("read public QR with .png suffix: %v", err)
	}
	if string(raw) != "artwork-png" {
		t.Fatalf("public QR bytes=%q", string(raw))
	}
}

func TestArrivalBoundDeliveryStatusesAndStatusNeverResends(t *testing.T) {
	tests := []struct {
		name                string
		health              string
		recentlySent        bool
		sendStatus          enums.ArrivalDeliveryStatus
		sendErr             error
		wantStatus          enums.ArrivalDeliveryStatus
		wantCalls           int
		callStatusAfterScan bool
	}{
		{
			name:                "sent",
			health:              "online",
			sendStatus:          enums.ArrivalDeliveryStatusSent,
			wantStatus:          enums.ArrivalDeliveryStatusSent,
			wantCalls:           1,
			callStatusAfterScan: true,
		},
		{
			name:         "rate limited",
			health:       "online",
			recentlySent: true,
			sendStatus:   enums.ArrivalDeliveryStatusSent,
			wantStatus:   enums.ArrivalDeliveryStatusRateLimited,
			wantCalls:    0,
		},
		{
			name:       "instance offline",
			health:     "offline",
			sendStatus: enums.ArrivalDeliveryStatusSent,
			wantStatus: enums.ArrivalDeliveryStatusInstanceOffline,
			wantCalls:  0,
		},
		{
			name:       "send failed",
			health:     "online",
			sendStatus: enums.ArrivalDeliveryStatusFailed,
			sendErr:    errors.New("protocol send failed"),
			wantStatus: enums.ArrivalDeliveryStatusFailed,
			wantCalls:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupArrivalLinkTestFixture(t)
			createBoundArrivalBinding(t, fixture, tt.health)
			if tt.recentlySent {
				createRecentArrivalDelivery(t, fixture)
			}
			login := &stubArrivalLoginExchanger{
				result: &weChatCodeSessionResult{OpenID: "openid-a"},
			}
			sender := &stubArrivalCardSender{status: tt.sendStatus, err: tt.sendErr}
			service := &arrivalLinkService{loginExchanger: login, cardSender: sender}
			req := request.ArrivalBootstrapRequest{
				SchemaVersion: arrivalScanInputVersion,
				LoginCode:     "bound-login-code-" + strings.ReplaceAll(tt.name, " ", "-"),
				Scene:         fixture.connection.StoreScene,
				ScanEventID:   "bound-scan-event-" + strings.ReplaceAll(tt.name, " ", "-"),
			}
			result, err := service.Bootstrap(req)
			if err != nil {
				t.Fatalf("bootstrap bound scan: %v", err)
			}
			if result.BindingStatus != string(enums.ArrivalBindingStatusBound) ||
				result.DeliveryStatus != string(tt.wantStatus) ||
				result.ContactWay.Available {
				t.Fatalf("unexpected bound delivery result: %#v", result)
			}
			if sender.calls != tt.wantCalls {
				t.Fatalf("sender calls=%d want %d", sender.calls, tt.wantCalls)
			}
			repeated, err := service.Bootstrap(req)
			if err != nil {
				t.Fatalf("repeat bound bootstrap: %v", err)
			}
			if repeated.SessionToken != result.SessionToken || sender.calls != tt.wantCalls {
				t.Fatal("repeat bootstrap resent or changed the original result")
			}
			if tt.callStatusAfterScan {
				if _, err := service.Status(result.SessionToken); err != nil {
					t.Fatalf("read bound status: %v", err)
				}
				if sender.calls != tt.wantCalls {
					t.Fatal("GET /status resent the arrival card")
				}
			}
		})
	}
}

func TestArrivalAuthorizationRevocationInvalidatesBoundStatus(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	createBoundArrivalBinding(t, fixture, "online")
	resourceToken := fixture.security.PublicResourceToken(fixture.contactWay.ID)
	futureExpiry := time.Now().Add(time.Hour)
	if err := repositories.ArrivalRepository.UpdateContactWay(
		fixture.db,
		fixture.contactWay.ID,
		fixture.tenantID,
		map[string]any{
			"public_resource_token_hash": fixture.security.Fingerprint("public_qr_token", resourceToken),
			"contact_way_status":         enums.ArrivalContactWayStatusActive,
			"expires_at":                 futureExpiry,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := ArrivalLinkService.PublicQRCode(resourceToken + ".png"); err != nil {
		t.Fatalf("public QR unavailable before authorization revocation: %v", err)
	}
	before, err := ArrivalLinkService.Status(fixture.sessionToken)
	if err != nil {
		t.Fatal(err)
	}
	if before.BindingStatus != string(enums.ArrivalBindingStatusBound) {
		t.Fatalf("binding before revocation=%q", before.BindingStatus)
	}

	payload := fmt.Sprintf(
		"<xml><SuiteId>%s</SuiteId><InfoType>cancel_auth</InfoType><AuthCorpId>%s</AuthCorpId><TimeStamp>%d</TimeStamp></xml>",
		config.Current().Arrival.WeComSuiteID,
		fixture.corpID,
		time.Now().Unix(),
	)
	signature, timestamp, nonce, body := buildArrivalCallbackRequest(t, config.Current().Arrival.WeComSuiteID, payload)
	if err := WeComProviderCallbackService.Handle("command", signature, timestamp, nonce, body); err != nil {
		t.Fatalf("handle authorization revocation: %v", err)
	}
	authorization := repositories.ArrivalRepository.GetTenantAuthorization(
		fixture.db,
		fixture.authorization.ID,
		fixture.tenantID,
	)
	connection := repositories.ArrivalRepository.GetConnection(
		fixture.db,
		fixture.connection.ID,
		fixture.tenantID,
	)
	if authorization == nil || authorization.AuthorizationStatus != enums.WeComAuthorizationStatusRevoked ||
		connection == nil || connection.ConnectionStatus != enums.ArrivalConnectionStatusInvalid {
		t.Fatalf("revocation state authorization=%#v connection=%#v", authorization, connection)
	}
	after, err := ArrivalLinkService.Status(fixture.sessionToken)
	if err != nil {
		t.Fatal(err)
	}
	if after.BindingStatus != string(enums.ArrivalBindingStatusUnbound) ||
		after.ContactWay.Available {
		t.Fatalf("revoked authorization remains usable: %#v", after)
	}
	if _, err := ArrivalLinkService.PublicQRCode(resourceToken + ".png"); err == nil {
		t.Fatal("public QR remained readable after authorization revocation")
	}
	revokedContactWay := repositories.ArrivalRepository.GetContactWay(
		fixture.db,
		fixture.contactWay.ID,
		fixture.tenantID,
	)
	if revokedContactWay == nil ||
		revokedContactWay.ContactWayStatus != enums.ArrivalContactWayStatusExpired ||
		revokedContactWay.FailureCode != "authorization_revoked" {
		t.Fatalf("revoked contact way state=%#v", revokedContactWay)
	}
	maintenance := &arrivalMaintenanceService{}
	if cleaned := maintenance.CleanupExpiredContactWays(10); cleaned != 1 {
		t.Fatalf("locally cleaned revoked contact ways=%d want 1", cleaned)
	}
	cleanedContactWay := repositories.ArrivalRepository.GetContactWay(
		fixture.db,
		fixture.contactWay.ID,
		fixture.tenantID,
	)
	if cleanedContactWay == nil ||
		cleanedContactWay.ContactWayStatus != enums.ArrivalContactWayStatusCleaned ||
		cleanedContactWay.OriginalQRCodeCiphertext != "" ||
		cleanedContactWay.OriginalPNGBase64 != "" ||
		cleanedContactWay.ArtworkPNGBase64 != "" {
		t.Fatalf("revoked QR material was not removed locally: %#v", cleanedContactWay)
	}
}

func TestArrivalBoundStatusRequiresLiveScopedConversationEvidence(t *testing.T) {
	tests := []string{
		"customer missing",
		"conversation customer mismatch",
		"route store mismatch",
		"instance store mismatch",
		"member fingerprint mismatch",
	}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := setupArrivalLinkTestFixture(t)
			createBoundArrivalBinding(t, fixture, "online")
			before, err := ArrivalLinkService.Status(fixture.sessionToken)
			if err != nil {
				t.Fatal(err)
			}
			if before.BindingStatus != string(enums.ArrivalBindingStatusBound) {
				t.Fatalf("binding before corruption=%q want bound", before.BindingStatus)
			}
			eventBefore := repositories.ArrivalRepository.GetScanEvent(
				fixture.db,
				fixture.scanEvent.ID,
				fixture.tenantID,
			)
			switch name {
			case "customer missing":
				if err := fixture.db.Delete(&models.Customer{}, int64(7001)).Error; err != nil {
					t.Fatal(err)
				}
			case "conversation customer mismatch":
				if err := fixture.db.Model(&models.Conversation{}).
					Where("id = ?", 8001).
					Update("customer_id", 9999).Error; err != nil {
					t.Fatal(err)
				}
			case "route store mismatch":
				if err := fixture.db.Model(&models.ConversationRouteState{}).
					Where("conversation_id = ?", 8001).
					Update("store_id", 0).Error; err != nil {
					t.Fatal(err)
				}
			case "instance store mismatch":
				if err := fixture.db.Model(&models.WxWorkProtocolInstance{}).
					Where("id = ?", fixture.connection.WxWorkProtocolInstanceID).
					Update("store_id", 0).Error; err != nil {
					t.Fatal(err)
				}
			case "member fingerprint mismatch":
				if err := fixture.db.Model(&models.StoreArrivalConnection{}).
					Where("id = ?", fixture.connection.ID).
					Update("contact_member_fingerprint", "mismatched-member").Error; err != nil {
					t.Fatal(err)
				}
			}
			after, err := ArrivalLinkService.Status(fixture.sessionToken)
			if err != nil {
				t.Fatal(err)
			}
			if after.BindingStatus != string(enums.ArrivalBindingStatusLegacyUnmapped) {
				t.Fatalf("binding after %s=%q want legacy_unmapped", name, after.BindingStatus)
			}
			eventAfter := repositories.ArrivalRepository.GetScanEvent(
				fixture.db,
				fixture.scanEvent.ID,
				fixture.tenantID,
			)
			if eventBefore == nil || eventAfter == nil || !eventBefore.UpdatedAt.Equal(eventAfter.UpdatedAt) {
				t.Fatal("GET /status rewrote the scan event while validating binding evidence")
			}
		})
	}
}

func TestWeComCallbackRejectsInvalidSignatureAndStaleTimestamp(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	payload := fmt.Sprintf(
		"<xml><ToUserName>%s</ToUserName><CreateTime>%d</CreateTime><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><UserID>%s</UserID><ExternalUserID>%s</ExternalUserID><State>%s</State></xml>",
		fixture.corpID,
		time.Now().Unix(),
		fixture.memberUserID,
		fixture.externalID,
		fixture.contactState,
	)
	signature, timestamp, nonce, body := buildArrivalCallbackRequest(t, fixture.corpID, payload)
	if err := WeComProviderCallbackService.Handle("data", "invalid-"+signature, timestamp, nonce, body); err == nil {
		t.Fatal("invalid callback signature must fail")
	}
	staleAt := time.Now().Add(-weComCallbackReplayWindow - time.Minute)
	staleSignature, staleTimestamp, staleNonce, staleBody := buildArrivalCallbackRequestAt(t, fixture.corpID, payload, staleAt)
	if err := WeComProviderCallbackService.Handle("data", staleSignature, staleTimestamp, staleNonce, staleBody); err == nil {
		t.Fatal("stale callback must fail replay-window validation")
	}
	var count int64
	if err := fixture.db.Model(&models.WeComProviderCallbackEvent{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid callbacks persisted %d events", count)
	}
}

func setupArrivalLinkTestFixture(t *testing.T) arrivalLinkTestFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open arrival test database: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Store{},
		&models.WxWorkProtocolInstance{},
		&models.Customer{},
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.WxWorkKFConversation{},
		&models.MiniProgramIdentity{},
		&models.WeComSuiteCredential{},
		&models.WeComTenantAuthorization{},
		&models.StoreArrivalConnection{},
		&models.StoreArrivalInvitation{},
		&models.WeComAuthorizationAttempt{},
		&models.ArrivalScanEvent{},
		&models.ArrivalSession{},
		&models.ArrivalContactWay{},
		&models.ArrivalAcquisitionLink{},
		&models.ArrivalStoreBinding{},
		&models.ArrivalBindingTicket{},
		&models.WeComProviderCallbackEvent{},
		&models.ArrivalAuditLog{},
	); err != nil {
		t.Fatalf("migrate arrival test database: %v", err)
	}
	sqls.SetDB(db)
	dataKey := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	encodingKey := strings.TrimSuffix(dataKey, "=")
	config.SetCurrent(&config.Config{Arrival: config.ArrivalConfig{
		Enabled:                     true,
		PublicBaseURL:               "https://weibao.example.test",
		MiniProgramAppID:            "wx-arrival-test",
		MiniProgramAppSecret:        "miniprogram-secret",
		SessionSecret:               "arrival-session-secret-at-least-32-bytes",
		IdentityHMACKey:             "arrival-identity-secret-at-least-32-bytes",
		DataMasterKey:               dataKey,
		DataMasterKeyID:             "arrival-test-key-v1",
		WeComSuiteID:                "suite-arrival-test",
		WeComSuiteSecret:            "suite-secret",
		WeComProviderCallbackToken:  "callback-token-at-least-32-bytes",
		WeComProviderEncodingAESKey: encodingKey,
	}})
	previousBridge := ArrivalBindingBridge
	ArrivalBindingBridge = unavailableArrivalProtocolBindingBridge{}
	t.Cleanup(func() {
		ArrivalBindingBridge = previousBridge
		config.SetCurrent(&config.Config{})
		sqls.SetDB(nil)
	})

	security, err := newArrivalSecurity()
	if err != nil {
		t.Fatalf("initialize arrival security: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	audit := arrivalSystemAuditFields(now)
	tenantID := int64(101)
	store := &models.Store{
		TenantID:    tenantID,
		StoreCode:   "arrival-a",
		Name:        "到店联动 A 店",
		BrandName:   "丽斯未来",
		Status:      enums.StatusOk,
		AuditFields: audit,
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create arrival store: %v", err)
	}
	identity := &models.MiniProgramIdentity{
		TenantID:          tenantID,
		AppID:             "wx-arrival-test",
		OpenIDCiphertext:  "encrypted-openid",
		OpenIDNonce:       "nonce",
		OpenIDFingerprint: security.Fingerprint("miniprogram_openid", "openid-a"),
		Status:            enums.StatusOk,
		AuditFields:       audit,
	}
	if err := db.Create(identity).Error; err != nil {
		t.Fatalf("create mini-program identity: %v", err)
	}
	suite := &models.WeComSuiteCredential{
		SuiteID:     "suite-arrival-test",
		Status:      enums.StatusOk,
		AuditFields: audit,
	}
	if err := db.Create(suite).Error; err != nil {
		t.Fatalf("create suite credential: %v", err)
	}
	corpID := "ww-arrival-corp"
	corpCiphertext, corpNonce, err := security.Encrypt("corp_id", corpID)
	if err != nil {
		t.Fatal(err)
	}
	authorization := &models.WeComTenantAuthorization{
		TenantID:                tenantID,
		SuiteCredentialID:       suite.ID,
		CorpIDCiphertext:        corpCiphertext,
		CorpIDNonce:             corpNonce,
		CorpIDFingerprint:       security.Fingerprint("corp_id", corpID),
		CorpName:                "测试企微主体",
		PermanentCodeCiphertext: "encrypted-permanent-code",
		AuthorizationStatus:     enums.WeComAuthorizationStatusActive,
		AuthorizedAt:            &now,
		AuditFields:             audit,
	}
	if err := db.Create(authorization).Error; err != nil {
		t.Fatalf("create tenant authorization: %v", err)
	}
	memberUserID := "member-a"
	memberCiphertext, memberNonce, err := security.Encrypt("contact_member", memberUserID)
	if err != nil {
		t.Fatal(err)
	}
	connection := &models.StoreArrivalConnection{
		TenantID:                 tenantID,
		StoreID:                  store.ID,
		StoreScene:               "arr-test-a",
		TenantAuthorizationID:    authorization.ID,
		ContactMemberCiphertext:  memberCiphertext,
		ContactMemberNonce:       memberNonce,
		ContactMemberFingerprint: security.Fingerprint("contact_member", memberUserID),
		WxWorkProtocolInstanceID: 9001,
		ConnectionStatus:         enums.ArrivalConnectionStatusActive,
		Status:                   enums.StatusOk,
		AuditFields:              audit,
	}
	if err := db.Create(connection).Error; err != nil {
		t.Fatalf("create arrival connection: %v", err)
	}
	scanEvent := &models.ArrivalScanEvent{
		TenantID:              tenantID,
		StoreID:               store.ID,
		MiniProgramIdentityID: identity.ID,
		ScanEventHash:         security.Fingerprint("scan_event", "scan-event-a"),
		SchemaVersion:         arrivalScanInputVersion,
		IdentityStatus:        enums.ArrivalIdentityStatusMatched,
		BindingStatus:         enums.ArrivalBindingStatusUnbound,
		DeliveryStatus:        enums.ArrivalDeliveryStatusNotBound,
		Status:                enums.StatusOk,
		AuditFields:           audit,
	}
	if err := db.Create(scanEvent).Error; err != nil {
		t.Fatalf("create scan event: %v", err)
	}
	expiresAt := now.Add(time.Hour)
	session := &models.ArrivalSession{
		TenantID:    tenantID,
		StoreID:     store.ID,
		ScanEventID: scanEvent.ID,
		TokenHash:   security.Fingerprint("session_placeholder", "placeholder"),
		ExpiresAt:   expiresAt,
		AuditFields: audit,
	}
	if err := db.Create(session).Error; err != nil {
		t.Fatalf("create arrival session: %v", err)
	}
	sessionToken := security.SessionToken(session.ID, session.ExpiresAt)
	if err := db.Model(session).Update("token_hash", security.Fingerprint("session_token", sessionToken)).Error; err != nil {
		t.Fatalf("finalize arrival session: %v", err)
	}
	contactExpiry := now.Add(-time.Minute)
	contactWay := &models.ArrivalContactWay{
		TenantID:                 tenantID,
		StoreID:                  store.ID,
		ScanEventID:              scanEvent.ID,
		TenantAuthorizationID:    authorization.ID,
		ContactStateHash:         security.Fingerprint("contact_state_placeholder", "state"),
		ConfigID:                 "official-config-a",
		OriginalQRCodeCiphertext: "encrypted-qr-url",
		OriginalQRCodeNonce:      "qr-nonce",
		OriginalPNGBase64:        "b3JpZ2luYWw=",
		PublicResourceTokenHash:  security.Fingerprint("public_qr_placeholder", "resource"),
		ArtworkPNGBase64:         "YXJ0d29yaw==",
		Mode:                     enums.ArrivalContactWayModeQRCode,
		ContactWayStatus:         enums.ArrivalContactWayStatusActive,
		ExpiresAt:                &contactExpiry,
		Status:                   enums.StatusOk,
		AuditFields:              audit,
	}
	if err := db.Create(contactWay).Error; err != nil {
		t.Fatalf("create contact way: %v", err)
	}
	contactState := security.ContactState(contactWay.ID)
	if err := repositories.ArrivalRepository.UpdateContactWay(db, contactWay.ID, tenantID, map[string]any{
		"contact_state_hash": security.Fingerprint("contact_state", contactState),
	}); err != nil {
		t.Fatalf("finalize contact state: %v", err)
	}
	return arrivalLinkTestFixture{
		db:            db,
		security:      security,
		tenantID:      tenantID,
		store:         store,
		identity:      identity,
		authorization: authorization,
		connection:    connection,
		scanEvent:     scanEvent,
		sessionToken:  sessionToken,
		contactWay:    contactWay,
		contactState:  contactState,
		corpID:        corpID,
		memberUserID:  memberUserID,
		externalID:    "external-customer-a",
	}
}

func createArrivalSiblingStoreBinding(t *testing.T, fixture arrivalLinkTestFixture) *models.ArrivalStoreBinding {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	store := &models.Store{
		TenantID:    fixture.tenantID,
		StoreCode:   "arrival-b",
		Name:        "到店联动 B 店",
		BrandName:   "丽斯未来",
		Status:      enums.StatusOk,
		AuditFields: arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(store).Error; err != nil {
		t.Fatalf("create sibling store: %v", err)
	}
	memberCiphertext, memberNonce, err := fixture.security.Encrypt("contact_member", "member-b")
	if err != nil {
		t.Fatal(err)
	}
	externalCiphertext, externalNonce, err := fixture.security.Encrypt("external_user_id", fixture.externalID)
	if err != nil {
		t.Fatal(err)
	}
	binding := &models.ArrivalStoreBinding{
		TenantID:                  fixture.tenantID,
		StoreID:                   store.ID,
		MiniProgramIdentityID:     fixture.identity.ID,
		TenantAuthorizationID:     fixture.authorization.ID,
		ExternalUserIDCiphertext:  externalCiphertext,
		ExternalUserIDNonce:       externalNonce,
		ExternalUserIDFingerprint: fixture.security.Fingerprint("external_user_id", fixture.externalID),
		ContactMemberCiphertext:   memberCiphertext,
		ContactMemberNonce:        memberNonce,
		ContactMemberFingerprint:  fixture.security.Fingerprint("contact_member", "member-b"),
		WxWorkProtocolInstanceID:  9002,
		CustomerID:                7002,
		ConversationID:            8002,
		OfficialRelationStatus:    enums.ArrivalOfficialRelationStatusConfirmed,
		BindingStatus:             enums.ArrivalBindingStatusBound,
		EvidenceHash:              "sibling-evidence",
		OfficialRelationshipAt:    &now,
		ProtocolMappedAt:          &now,
		Status:                    enums.StatusOk,
		AuditFields:               arrivalSystemAuditFields(now),
	}
	protocolCiphertext, protocolNonce, err := fixture.security.Encrypt("protocol_conversation_id", "S:sibling-contact")
	if err != nil {
		t.Fatal(err)
	}
	binding.ProtocolConversationCiphertext = protocolCiphertext
	binding.ProtocolConversationNonce = protocolNonce
	binding.ProtocolConversationFingerprint = fixture.security.Fingerprint("protocol_conversation_id", "S:sibling-contact")
	if err := fixture.db.Create(binding).Error; err != nil {
		t.Fatalf("create sibling store binding: %v", err)
	}
	return binding
}

func createBoundArrivalBinding(t *testing.T, fixture arrivalLinkTestFixture, health string) *models.ArrivalStoreBinding {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	instance := &models.WxWorkProtocolInstance{
		ID:             fixture.connection.WxWorkProtocolInstanceID,
		TenantID:       fixture.tenantID,
		Guid:           "arrival-instance-guid",
		EmployeeUserID: fixture.memberUserID,
		EmployeeName:   "到店测试员工",
		StoreID:        fixture.store.ID,
		HealthStatus:   health,
		Status:         enums.StatusOk,
		AuditFields:    arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(instance).Error; err != nil {
		t.Fatalf("create arrival protocol instance: %v", err)
	}
	customer := &models.Customer{
		ID:          7001,
		TenantID:    fixture.tenantID,
		Name:        "到店测试客户",
		Status:      enums.StatusOk,
		AuditFields: arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(customer).Error; err != nil {
		t.Fatalf("create arrival customer: %v", err)
	}
	conversation := &models.Conversation{
		ID:           8001,
		TenantID:     fixture.tenantID,
		CustomerID:   customer.ID,
		CustomerName: customer.Name,
		Status:       enums.IMConversationStatusAIServing,
		ServiceMode:  enums.IMConversationServiceModeAIOnly,
		AuditFields:  arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(conversation).Error; err != nil {
		t.Fatalf("create arrival conversation: %v", err)
	}
	route := &models.ConversationRouteState{
		TenantID:         fixture.tenantID,
		ConversationID:   conversation.ID,
		StoreID:          fixture.store.ID,
		WxWorkInstanceID: instance.ID,
		AuditFields:      arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(route).Error; err != nil {
		t.Fatalf("create arrival conversation route: %v", err)
	}
	externalCiphertext, externalNonce, err := fixture.security.Encrypt("external_user_id", fixture.externalID)
	if err != nil {
		t.Fatal(err)
	}
	memberCiphertext, memberNonce, err := fixture.security.Encrypt("contact_member", fixture.memberUserID)
	if err != nil {
		t.Fatal(err)
	}
	protocolConversationID := "S:arrival-protocol-contact"
	protocolCiphertext, protocolNonce, err := fixture.security.Encrypt("protocol_conversation_id", protocolConversationID)
	if err != nil {
		t.Fatal(err)
	}
	binding := &models.ArrivalStoreBinding{
		TenantID:                       fixture.tenantID,
		StoreID:                        fixture.store.ID,
		MiniProgramIdentityID:          fixture.identity.ID,
		TenantAuthorizationID:          fixture.authorization.ID,
		ExternalUserIDCiphertext:       externalCiphertext,
		ExternalUserIDNonce:            externalNonce,
		ExternalUserIDFingerprint:      fixture.security.Fingerprint("external_user_id", fixture.externalID),
		ContactMemberCiphertext:        memberCiphertext,
		ContactMemberNonce:             memberNonce,
		ContactMemberFingerprint:       fixture.security.Fingerprint("contact_member", fixture.memberUserID),
		WxWorkProtocolInstanceID:       fixture.connection.WxWorkProtocolInstanceID,
		CustomerID:                     7001,
		ConversationID:                 8001,
		ProtocolConversationCiphertext: protocolCiphertext,
		ProtocolConversationNonce:      protocolNonce,
		ProtocolConversationFingerprint: fixture.security.Fingerprint(
			"protocol_conversation_id",
			protocolConversationID,
		),
		OfficialRelationStatus: enums.ArrivalOfficialRelationStatusConfirmed,
		BindingStatus:          enums.ArrivalBindingStatusBound,
		EvidenceHash:           "verified-binding-evidence",
		OfficialRelationshipAt: &now,
		ProtocolMappedAt:       &now,
		Status:                 enums.StatusOk,
		AuditFields:            arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(binding).Error; err != nil {
		t.Fatalf("create bound arrival relationship: %v", err)
	}
	return binding
}

func createRecentArrivalDelivery(t *testing.T, fixture arrivalLinkTestFixture) {
	t.Helper()
	now := time.Now()
	event := &models.ArrivalScanEvent{
		TenantID:              fixture.tenantID,
		StoreID:               fixture.store.ID,
		MiniProgramIdentityID: fixture.identity.ID,
		ScanEventHash:         fixture.security.Fingerprint("scan_event", "recent-sent-scan"),
		RequestFingerprint:    fixture.security.Fingerprint("scan_request", "recent-sent-request"),
		SchemaVersion:         arrivalScanInputVersion,
		IdentityStatus:        enums.ArrivalIdentityStatusMatched,
		BindingStatus:         enums.ArrivalBindingStatusBound,
		DeliveryStatus:        enums.ArrivalDeliveryStatusSent,
		DeliveryAttemptedAt:   &now,
		DeliveryCompletedAt:   &now,
		Status:                enums.StatusOk,
		AuditFields:           arrivalSystemAuditFields(now),
	}
	if err := fixture.db.Create(event).Error; err != nil {
		t.Fatalf("create recent arrival delivery: %v", err)
	}
}

func buildArrivalCallbackRequest(t *testing.T, receiveID, payload string) (string, string, string, []byte) {
	return buildArrivalCallbackRequestAt(t, receiveID, payload, time.Now())
}

func buildArrivalCallbackRequestAt(t *testing.T, receiveID, payload string, at time.Time) (string, string, string, []byte) {
	t.Helper()
	cfg := config.Current().Arrival
	encrypted, err := encryptWeComCallbackForTest(
		cfg.WeComProviderEncodingAESKey,
		receiveID,
		[]byte(payload),
	)
	if err != nil {
		t.Fatalf("encrypt callback fixture: %v", err)
	}
	body, err := xml.Marshal(weComEncryptedEnvelope{Encrypt: encrypted})
	if err != nil {
		t.Fatalf("marshal callback envelope: %v", err)
	}
	timestamp := strconv.FormatInt(at.Unix(), 10)
	nonce := "arrival-test-nonce-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	values := []string{cfg.WeComProviderCallbackToken, timestamp, nonce, encrypted}
	sort.Strings(values)
	sum := sha1.Sum([]byte(strings.Join(values, "")))
	return hex.EncodeToString(sum[:]), timestamp, nonce, body
}
