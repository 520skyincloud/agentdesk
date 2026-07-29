package services

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
)

func TestWeComProviderCommandURLVerificationAcceptsProviderCorpID(t *testing.T) {
	cfg := setupWeComCallbackConfig(t)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "command-get-nonce"
	echo := "command-url-verification"
	encrypted := mustEncryptWeComCallback(t, cfg.Arrival.WeComProviderEncodingAESKey, "provider-enterprise-corp", []byte(echo))
	signature := weComCallbackSignatureForTest(cfg.Arrival.WeComProviderCallbackToken, timestamp, nonce, encrypted)

	got, err := WeComProviderCallbackService.VerifyURL("command", signature, timestamp, nonce, encrypted)
	if err != nil {
		t.Fatalf("VerifyURL(command) error = %v", err)
	}
	if got != echo {
		t.Fatalf("VerifyURL(command) = %q, want %q", got, echo)
	}
}

func TestWeComProviderCommandURLVerificationRejectsInvalidInputs(t *testing.T) {
	cfg := setupWeComCallbackConfig(t)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "command-get-invalid-nonce"
	validEncrypted := mustEncryptWeComCallback(t, cfg.Arrival.WeComProviderEncodingAESKey, "provider-enterprise-corp", []byte("echo"))

	tests := []struct {
		name      string
		encrypted string
		receiveID string
		signature func(string) string
		wantStage string
	}{
		{
			name:      "bad signature",
			encrypted: validEncrypted,
			signature: func(string) string { return "invalid-signature" },
			wantStage: "signature",
		},
		{
			name:      "bad ciphertext",
			encrypted: "not-valid-base64",
			signature: func(encrypted string) string {
				return weComCallbackSignatureForTest(cfg.Arrival.WeComProviderCallbackToken, timestamp, nonce, encrypted)
			},
			wantStage: "decrypt",
		},
		{
			name:      "empty receive id",
			receiveID: "",
			signature: func(encrypted string) string {
				return weComCallbackSignatureForTest(cfg.Arrival.WeComProviderCallbackToken, timestamp, nonce, encrypted)
			},
			wantStage: "receive_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted := tt.encrypted
			if encrypted == "" {
				encrypted = mustEncryptWeComCallback(t, cfg.Arrival.WeComProviderEncodingAESKey, tt.receiveID, []byte("echo"))
			}
			_, err := WeComProviderCallbackService.VerifyURL(
				"command",
				tt.signature(encrypted),
				timestamp,
				nonce,
				encrypted,
			)
			if err == nil {
				t.Fatal("VerifyURL(command) unexpectedly succeeded")
			}
			if stage := WeComCallbackFailureStage(err); stage != tt.wantStage {
				t.Fatalf("failure stage = %q, want %q", stage, tt.wantStage)
			}
		})
	}
}

func TestWeComProviderDataURLVerificationStillSucceeds(t *testing.T) {
	cfg := setupWeComCallbackConfig(t)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "data-get-nonce"
	echo := "data-url-verification"
	encrypted := mustEncryptWeComCallback(t, cfg.Arrival.WeComProviderEncodingAESKey, "authorized-enterprise-corp", []byte(echo))
	signature := weComCallbackSignatureForTest(cfg.Arrival.WeComProviderCallbackToken, timestamp, nonce, encrypted)

	got, err := WeComProviderCallbackService.VerifyURL("data", signature, timestamp, nonce, encrypted)
	if err != nil {
		t.Fatalf("VerifyURL(data) error = %v", err)
	}
	if got != echo {
		t.Fatalf("VerifyURL(data) = %q, want %q", got, echo)
	}
}

func TestWeComProviderCommandPOSTKeepsSuiteIDValidation(t *testing.T) {
	fixture := setupArrivalLinkTestFixture(t)
	cfg := config.Current()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "command-post-nonce"
	payload := weComProviderCallbackPayload{
		SuiteID:     cfg.Arrival.WeComSuiteID,
		InfoType:    "suite_ticket",
		SuiteTicket: "rotating-suite-ticket",
		TimeStamp:   time.Now().Unix(),
	}
	plaintext, err := xml.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("suite id accepted", func(t *testing.T) {
		encrypted := mustEncryptWeComCallback(t, cfg.Arrival.WeComProviderEncodingAESKey, cfg.Arrival.WeComSuiteID, plaintext)
		signature := weComCallbackSignatureForTest(cfg.Arrival.WeComProviderCallbackToken, timestamp, nonce, encrypted)
		body := []byte("<xml><Encrypt><![CDATA[" + encrypted + "]]></Encrypt></xml>")
		if err := WeComProviderCallbackService.Handle("command", signature, timestamp, nonce, body); err != nil {
			t.Fatalf("Handle(command) error = %v", err)
		}
		suite := repositories.ArrivalRepository.FindSuiteCredential(fixture.db, cfg.Arrival.WeComSuiteID)
		if suite == nil || strings.TrimSpace(suite.SuiteTicketCiphertext) == "" {
			t.Fatal("suite ticket was not persisted")
		}
		event := repositories.ArrivalRepository.FindCallbackEventByHash(
			fixture.db,
			fixture.security.Fingerprint("wecom_callback_event", strings.Join([]string{"command", timestamp, nonce, encrypted}, "\x00")),
		)
		if event == nil || event.CallbackStatus != enums.ArrivalCallbackStatusProcessed {
			t.Fatalf("callback event = %#v, want processed", event)
		}
	})

	t.Run("non suite id rejected", func(t *testing.T) {
		encrypted := mustEncryptWeComCallback(t, cfg.Arrival.WeComProviderEncodingAESKey, "provider-enterprise-corp", plaintext)
		signature := weComCallbackSignatureForTest(cfg.Arrival.WeComProviderCallbackToken, timestamp, nonce+"-wrong", encrypted)
		body := []byte("<xml><Encrypt><![CDATA[" + encrypted + "]]></Encrypt></xml>")
		err := WeComProviderCallbackService.Handle("command", signature, timestamp, nonce+"-wrong", body)
		if err == nil {
			t.Fatal("Handle(command) accepted non-SuiteID receiveId")
		}
		if stage := WeComCallbackFailureStage(err); stage != "receive_id" {
			t.Fatalf("failure stage = %q, want receive_id", stage)
		}
	})
}

func setupWeComCallbackConfig(t *testing.T) config.Config {
	t.Helper()
	previous, hadPrevious := config.LookupCurrent()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Arrival: config.ArrivalConfig{
		WeComSuiteID:                "suite-test-only",
		WeComProviderCallbackToken:  hex.EncodeToString(tokenBytes),
		WeComProviderEncodingAESKey: strings.TrimSuffix(base64.StdEncoding.EncodeToString(key), "="),
	}}
	config.SetCurrent(&cfg)
	t.Cleanup(func() {
		if hadPrevious {
			config.SetCurrent(&previous)
			return
		}
		config.SetCurrent(&config.Config{})
	})
	return cfg
}

func mustEncryptWeComCallback(t *testing.T, encodingAESKey, receiveID string, message []byte) string {
	t.Helper()
	encrypted, err := encryptWeComCallbackForTest(encodingAESKey, receiveID, message)
	if err != nil {
		t.Fatal(err)
	}
	return encrypted
}

func weComCallbackSignatureForTest(token, timestamp, nonce, encrypted string) string {
	values := []string{token, timestamp, nonce, encrypted}
	sort.Strings(values)
	sum := sha1.Sum([]byte(strings.Join(values, "")))
	return hex.EncodeToString(sum[:])
}
