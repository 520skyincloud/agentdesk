package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
)

type scriptedArrivalContactWayCreator struct {
	mu     sync.Mutex
	errors []error
	result *weComContactWayResult
	delay  time.Duration
	calls  int
}

func (s *scriptedArrivalContactWayCreator) AddContactWay(
	*models.WeComTenantAuthorization,
	string,
	string,
) (*weComContactWayResult, error) {
	s.mu.Lock()
	s.calls++
	callIndex := s.calls - 1
	delay := s.delay
	var err error
	if callIndex < len(s.errors) {
		err = s.errors[callIndex]
	}
	result := s.result
	s.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *scriptedArrivalContactWayCreator) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type scriptedArrivalQRCodeBuilder struct {
	mu      sync.Mutex
	errors  []error
	results []*arrivalQRCodeArtifact
	calls   int
}

func (s *scriptedArrivalQRCodeBuilder) BuildArtifact(string) (*arrivalQRCodeArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.calls
	s.calls++
	if index < len(s.errors) && s.errors[index] != nil {
		return nil, s.errors[index]
	}
	if index < len(s.results) && s.results[index] != nil {
		return s.results[index], nil
	}
	if len(s.results) > 0 {
		return s.results[len(s.results)-1], nil
	}
	return nil, errors.New("missing scripted QR artifact")
}

func (s *scriptedArrivalQRCodeBuilder) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestArrivalContactWayProvisionFailuresArePersistedAndHidden(t *testing.T) {
	longSecret := "secret-value-012345678901234567890123456789"
	tests := []struct {
		name              string
		prepare           func(t *testing.T, fixture arrivalLinkTestFixture)
		providerErr       error
		wantFailureCode   string
		wantStage         string
		wantProviderCode  int
		wantProviderCalls int
	}{
		{
			name: "corp access token failure",
			providerErr: newWeComProviderError(
				weComStageCorpToken,
				200,
				40084,
				"invalid permanent_code: "+longSecret,
				false,
			),
			wantFailureCode:   "corp_token_failed",
			wantStage:         weComStageCorpToken,
			wantProviderCode:  40084,
			wantProviderCalls: 1,
		},
		{
			name: "application permission denied",
			providerErr: newWeComProviderError(
				weComStageAddContactWay,
				200,
				48002,
				"api forbidden userid: member-a",
				false,
			),
			wantFailureCode:   "contact_way_permission_denied",
			wantStage:         weComStageAddContactWay,
			wantProviderCode:  48002,
			wantProviderCalls: 1,
		},
		{
			name: "member outside authorized corporation",
			providerErr: newWeComProviderError(
				weComStageAddContactWay,
				200,
				40003,
				"invalid userid: member-a",
				false,
			),
			wantFailureCode:   "contact_way_api_failed",
			wantStage:         weComStageAddContactWay,
			wantProviderCode:  40003,
			wantProviderCalls: 1,
		},
		{
			name: "member lacks customer contact capability",
			providerErr: newWeComProviderError(
				weComStageAddContactWay,
				200,
				41054,
				"customer contact member not activated",
				false,
			),
			wantFailureCode:   "contact_way_api_failed",
			wantStage:         weComStageAddContactWay,
			wantProviderCode:  41054,
			wantProviderCalls: 1,
		},
		{
			name: "authorization revoked",
			prepare: func(t *testing.T, fixture arrivalLinkTestFixture) {
				t.Helper()
				if err := repositories.ArrivalRepository.UpdateTenantAuthorization(
					fixture.db,
					fixture.authorization.ID,
					fixture.tenantID,
					map[string]any{"authorization_status": enums.WeComAuthorizationStatusRevoked},
				); err != nil {
					t.Fatal(err)
				}
			},
			wantFailureCode:   "authorization_unavailable",
			wantStage:         weComStageAuthorizationValidate,
			wantProviderCalls: 0,
		},
		{
			name: "contact member missing",
			prepare: func(t *testing.T, fixture arrivalLinkTestFixture) {
				t.Helper()
				if err := repositories.ArrivalRepository.UpdateConnection(
					fixture.db,
					fixture.connection.ID,
					fixture.tenantID,
					map[string]any{
						"contact_member_ciphertext": "",
						"contact_member_nonce":      "",
					},
				); err != nil {
					t.Fatal(err)
				}
			},
			wantFailureCode:   "contact_member_invalid",
			wantStage:         weComStageContactMemberValidate,
			wantProviderCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := setupArrivalLinkTestFixture(t)
			if tt.prepare != nil {
				tt.prepare(t, fixture)
			}
			creator := &scriptedArrivalContactWayCreator{
				errors: []error{tt.providerErr},
				result: &weComContactWayResult{
					ConfigID: "must-not-be-used",
					QRCode:   "https://wework.qpic.cn/must-not-be-used.png",
				},
			}
			service := &arrivalLinkService{
				loginExchanger: &stubArrivalLoginExchanger{
					result: &weChatCodeSessionResult{OpenID: "openid-a"},
				},
				contactWayCreator: creator,
				qrCodeBuilder: &stubArrivalQRCodeBuilder{
					result: successfulArrivalQRCodeArtifact(),
				},
			}
			req := arrivalContactWayTestRequest("failure-" + strings.ReplaceAll(tt.name, " ", "-"))
			result, err := service.BootstrapWithRequestID(req, "arrival-contact-way-failure-test")
			if err != nil {
				t.Fatalf("bootstrap failure result: %v", err)
			}
			if result.ContactWay.Available ||
				result.ContactWay.Mode != string(enums.ArrivalContactWayModeNone) {
				t.Fatalf("failed contact way returned fake success: %#v", result.ContactWay)
			}
			if creator.callCount() != tt.wantProviderCalls {
				t.Fatalf("provider calls=%d want %d", creator.callCount(), tt.wantProviderCalls)
			}
			contactWay := findArrivalContactWayByRequest(t, fixture, req)
			if contactWay.ContactWayStatus != enums.ArrivalContactWayStatusFailed ||
				contactWay.FailureCode != tt.wantFailureCode ||
				contactWay.FailureStage != tt.wantStage ||
				contactWay.ProviderErrorCode != tt.wantProviderCode ||
				contactWay.FailureRetryable ||
				contactWay.ProvisionAttemptCount != 1 ||
				contactWay.LastProvisionRequestID != "arrival-contact-way-failure-test" {
				t.Fatalf("unexpected persisted diagnostics: %#v", contactWay)
			}
			serialized, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{
				"member-a",
				longSecret,
				"providerError",
				"failureStage",
			} {
				if strings.Contains(string(serialized), forbidden) ||
					strings.Contains(contactWay.ProviderErrorMessage, forbidden) {
					t.Fatalf("failure surface leaked %q", forbidden)
				}
			}
		})
	}
}

func TestSanitizeWeComProviderMessageRedactsProviderDiagnostics(t *testing.T) {
	message := sanitizeWeComProviderMessage(
		"api forbidden, hint: [987654321012345678], from ip: 203.0.113.42, " +
			"more info at https://developer.example.test/query?e=48002",
	)
	if message != "api forbidden" {
		t.Fatalf("sanitized provider message=%q want %q", message, "api forbidden")
	}

	message = sanitizeWeComProviderMessage(
		"upstream failed from ip: [2001:db8::1], trace " +
			"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdef " +
			"access_token=test-sensitive-value-012345678901234567890123456789",
	)
	for _, forbidden := range []string{
		"2001:db8::1",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdef",
		"test-sensitive-value",
	} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("sanitized provider message leaked %q: %q", forbidden, message)
		}
	}
}

func TestArrivalContactWayRetryReusesScanAndOfficialConfig(t *testing.T) {
	t.Run("provider retry reuses contact way row", func(t *testing.T) {
		fixture := setupArrivalLinkTestFixture(t)
		creator := &scriptedArrivalContactWayCreator{
			errors: []error{
				newWeComProviderError(weComStageAddContactWay, 200, -1, "system busy", true),
				nil,
			},
			result: &weComContactWayResult{
				ConfigID: "official-retry-config",
				QRCode:   "https://wework.qpic.cn/retry.png",
			},
		}
		qrBuilder := &scriptedArrivalQRCodeBuilder{
			results: []*arrivalQRCodeArtifact{successfulArrivalQRCodeArtifact()},
		}
		service := &arrivalLinkService{
			loginExchanger: &stubArrivalLoginExchanger{
				result: &weChatCodeSessionResult{OpenID: "openid-a"},
			},
			contactWayCreator: creator,
			qrCodeBuilder:     qrBuilder,
		}
		req := arrivalContactWayTestRequest("provider-retry")
		first, err := service.BootstrapWithRequestID(req, "arrival-provider-first")
		if err != nil {
			t.Fatal(err)
		}
		if first.ContactWay.Available {
			t.Fatal("temporary provider failure returned a contact way")
		}
		failed := findArrivalContactWayByRequest(t, fixture, req)
		if !failed.FailureRetryable || failed.NextProvisionRetryAt == nil {
			t.Fatalf("temporary failure was not scheduled: %#v", failed)
		}
		makeArrivalContactWayRetryDue(t, fixture, failed)

		second, err := service.BootstrapWithRequestID(req, "arrival-provider-second")
		if err != nil {
			t.Fatal(err)
		}
		if !second.ContactWay.Available ||
			second.ContactWay.Mode != string(enums.ArrivalContactWayModeQRCode) {
			t.Fatalf("retry did not recover contact way: %#v", second)
		}
		recovered := repositories.ArrivalRepository.GetContactWay(fixture.db, failed.ID, fixture.tenantID)
		if recovered == nil ||
			recovered.ContactWayStatus != enums.ArrivalContactWayStatusActive ||
			recovered.ConfigID != "official-retry-config" ||
			recovered.ProvisionAttemptCount != 2 ||
			recovered.LastProvisionRequestID != "arrival-provider-second" ||
			recovered.FailureCode != "" ||
			recovered.ProviderErrorMessage != "" {
			t.Fatalf("unexpected recovered contact way: %#v", recovered)
		}
		if creator.callCount() != 2 || qrBuilder.callCount() != 1 {
			t.Fatalf("provider calls=%d qr calls=%d", creator.callCount(), qrBuilder.callCount())
		}
		var count int64
		if err := fixture.db.Model(&models.ArrivalContactWay{}).
			Where("scan_event_id = ?", recovered.ScanEventID).
			Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("retry created %d contact way rows", count)
		}
	})

	t.Run("QR retry does not recreate official configuration", func(t *testing.T) {
		fixture := setupArrivalLinkTestFixture(t)
		creator := &scriptedArrivalContactWayCreator{
			result: &weComContactWayResult{
				ConfigID: "official-single-config",
				QRCode:   "https://wework.qpic.cn/official-single.png",
			},
		}
		qrBuilder := &scriptedArrivalQRCodeBuilder{
			errors: []error{errors.New("企业微信二维码下载失败"), nil},
			results: []*arrivalQRCodeArtifact{
				nil,
				successfulArrivalQRCodeArtifact(),
			},
		}
		service := &arrivalLinkService{
			loginExchanger: &stubArrivalLoginExchanger{
				result: &weChatCodeSessionResult{OpenID: "openid-a"},
			},
			contactWayCreator: creator,
			qrCodeBuilder:     qrBuilder,
		}
		req := arrivalContactWayTestRequest("qr-retry")
		first, err := service.Bootstrap(req)
		if err != nil {
			t.Fatal(err)
		}
		if first.ContactWay.Available {
			t.Fatal("failed QR artifact returned fake success")
		}
		failed := findArrivalContactWayByRequest(t, fixture, req)
		if failed.ConfigID != "official-single-config" ||
			failed.OriginalQRCodeCiphertext == "" ||
			!failed.FailureRetryable {
			t.Fatalf("official result was not retained for QR retry: %#v", failed)
		}
		makeArrivalContactWayRetryDue(t, fixture, failed)
		second, err := service.Bootstrap(req)
		if err != nil {
			t.Fatal(err)
		}
		if !second.ContactWay.Available {
			t.Fatalf("QR retry did not recover: %#v", second)
		}
		if creator.callCount() != 1 || qrBuilder.callCount() != 2 {
			t.Fatalf(
				"QR retry recreated official config: provider=%d qr=%d",
				creator.callCount(),
				qrBuilder.callCount(),
			)
		}
	})
}

func TestArrivalConcurrentBootstrapCreatesOneOfficialContactWay(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	creator := &scriptedArrivalContactWayCreator{
		result: &weComContactWayResult{
			ConfigID: "official-concurrent-config",
			QRCode:   "https://wework.qpic.cn/concurrent.png",
		},
		delay: 20 * time.Millisecond,
	}
	qrBuilder := &scriptedArrivalQRCodeBuilder{
		results: []*arrivalQRCodeArtifact{successfulArrivalQRCodeArtifact()},
	}
	login := &stubArrivalLoginExchanger{
		result: &weChatCodeSessionResult{OpenID: "openid-a"},
	}
	service := &arrivalLinkService{
		loginExchanger:    login,
		contactWayCreator: creator,
		qrCodeBuilder:     qrBuilder,
	}
	req := arrivalContactWayTestRequest("concurrent")
	const workers = 8
	results := make(chan error, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := service.Bootstrap(req)
			if err == nil && !result.ContactWay.Available {
				err = errors.New("concurrent bootstrap did not return active contact way")
			}
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if creator.callCount() != 1 || qrBuilder.callCount() != 1 || login.calls != 1 {
		t.Fatalf(
			"login=%d provider=%d qr=%d want 1/1/1",
			login.calls,
			creator.callCount(),
			qrBuilder.callCount(),
		)
	}
	contactWay := findArrivalContactWayByRequest(t, fixture, req)
	var count int64
	if err := fixture.db.Model(&models.ArrivalContactWay{}).
		Where("scan_event_id = ?", contactWay.ScanEventID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent bootstrap created %d contact way rows", count)
	}
}

func TestArrivalMaintenanceRecoversLegacyGenericFailure(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	future := time.Now().Add(time.Hour)
	if err := repositories.ArrivalRepository.UpdateContactWay(
		fixture.db,
		fixture.contactWay.ID,
		fixture.tenantID,
		map[string]any{
			"config_id":                   "",
			"original_qr_code_ciphertext": "",
			"original_qr_code_nonce":      "",
			"original_png_base64":         "",
			"artwork_png_base64":          "",
			"mode":                        enums.ArrivalContactWayModeNone,
			"contact_way_status":          enums.ArrivalContactWayStatusFailed,
			"failure_code":                "contact_way_api_failed",
			"failure_stage":               "",
			"failure_retryable":           false,
			"provision_attempt_count":     0,
			"last_provision_attempt_at":   nil,
			"next_provision_retry_at":     nil,
			"expires_at":                  future,
		},
	); err != nil {
		t.Fatal(err)
	}
	creator := &scriptedArrivalContactWayCreator{
		result: &weComContactWayResult{
			ConfigID: "official-legacy-recovered",
			QRCode:   "https://wework.qpic.cn/legacy-recovered.png",
		},
	}
	linkService := &arrivalLinkService{
		contactWayCreator: creator,
		qrCodeBuilder: &stubArrivalQRCodeBuilder{
			result: successfulArrivalQRCodeArtifact(),
		},
	}
	maintenance := &arrivalMaintenanceService{linkService: linkService}
	if retried := maintenance.RetryFailedContactWays(10); retried != 1 {
		t.Fatalf("legacy retry count=%d want 1", retried)
	}
	recovered := repositories.ArrivalRepository.GetContactWay(
		fixture.db,
		fixture.contactWay.ID,
		fixture.tenantID,
	)
	if recovered == nil ||
		recovered.ContactWayStatus != enums.ArrivalContactWayStatusActive ||
		recovered.ConfigID != "official-legacy-recovered" ||
		recovered.ProvisionAttemptCount != 2 {
		t.Fatalf("legacy failure was not recovered: %#v", recovered)
	}
	if retried := maintenance.RetryFailedContactWays(10); retried != 0 {
		t.Fatalf("active contact way retried again: %d", retried)
	}
	if creator.callCount() != 1 {
		t.Fatalf("legacy provider calls=%d want 1", creator.callCount())
	}
}

func TestArrivalContactWayLogsAndDiagnosticsRedactSensitiveValues(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
	})
	creator := &scriptedArrivalContactWayCreator{
		errors: []error{
			newWeComProviderError(
				weComStageAddContactWay,
				200,
				40003,
				"invalid userid: member-a access_token: sensitive-access-token-012345678901234567890123456789",
				false,
			),
		},
	}
	service := &arrivalLinkService{
		loginExchanger: &stubArrivalLoginExchanger{
			result: &weChatCodeSessionResult{OpenID: "openid-a"},
		},
		contactWayCreator: creator,
		qrCodeBuilder:     &stubArrivalQRCodeBuilder{},
	}
	req := arrivalContactWayTestRequest("sensitive-log")
	if _, err := service.BootstrapWithRequestID(req, "safe-internal-request-id"); err != nil {
		t.Fatal(err)
	}
	logOutput := logs.String()
	for _, forbidden := range []string{
		"member-a",
		"sensitive-access-token",
		fixture.corpID,
	} {
		if strings.Contains(logOutput, forbidden) {
			t.Fatalf("structured log leaked %q: %s", forbidden, logOutput)
		}
	}
	for _, required := range []string{
		"safe-internal-request-id",
		"stage=add_contact_way",
		"provider_error_code=40003",
	} {
		if !strings.Contains(logOutput, required) {
			t.Fatalf("structured log missing %q: %s", required, logOutput)
		}
	}
}

func arrivalContactWayTestRequest(suffix string) request.ArrivalBootstrapRequest {
	return request.ArrivalBootstrapRequest{
		SchemaVersion: arrivalScanInputVersion,
		LoginCode:     "arrival-login-" + suffix,
		Scene:         "arr-test-a",
		ScanEventID:   "arrival-scan-" + suffix,
	}
}

func successfulArrivalQRCodeArtifact() *arrivalQRCodeArtifact {
	return &arrivalQRCodeArtifact{
		OriginalPNGBase64:  "b3JpZ2luYWwtcG5n",
		PublishedPNGBase64: "cHVibGlzaGVkLXBuZw==",
		PayloadHash:        strings.Repeat("a", 64),
		ArtworkVerified:    true,
	}
}

func findArrivalContactWayByRequest(
	t *testing.T,
	fixture arrivalLinkTestFixture,
	req request.ArrivalBootstrapRequest,
) *models.ArrivalContactWay {
	t.Helper()
	scanHash := fixture.security.Fingerprint("scan_event", req.ScanEventID)
	event := repositories.ArrivalRepository.FindScanEventByHash(fixture.db, scanHash)
	if event == nil {
		t.Fatal("arrival scan event was not persisted")
	}
	contactWay := repositories.ArrivalRepository.FindContactWayByScanEvent(fixture.db, event.ID)
	if contactWay == nil {
		t.Fatal("arrival contact way was not persisted")
	}
	return contactWay
}

func makeArrivalContactWayRetryDue(
	t *testing.T,
	fixture arrivalLinkTestFixture,
	contactWay *models.ArrivalContactWay,
) {
	t.Helper()
	if err := repositories.ArrivalRepository.UpdateContactWay(
		fixture.db,
		contactWay.ID,
		fixture.tenantID,
		map[string]any{"next_provision_retry_at": time.Now().Add(-time.Second)},
	); err != nil {
		t.Fatal(err)
	}
}
