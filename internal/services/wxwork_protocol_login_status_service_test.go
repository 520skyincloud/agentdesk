package services

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseWxWorkProtocolLoginStatusOfficialCodes(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantStatus   string
		wantCode     int
		requiresCode bool
	}{
		{name: "waiting", raw: `{"data":{"status":0}}`, wantStatus: "pending", wantCode: 0},
		{name: "scanned", raw: `{"data":{"status":1}}`, wantStatus: "scanned", wantCode: 1},
		{name: "success", raw: `{"data":{"status":2}}`, wantStatus: "success", wantCode: 2},
		{name: "refused", raw: `{"data":{"status":4}}`, wantStatus: "refused", wantCode: 4},
		{name: "verification", raw: `{"data":{"status":10}}`, wantStatus: "verification_required", wantCode: 10, requiresCode: true},
		{name: "nested response string", raw: `"{\"data\":{\"status\":10}}"`, wantStatus: "verification_required", wantCode: 10, requiresCode: true},
		{name: "nested data string", raw: `{"data":"{\"status\":10}"}`, wantStatus: "verification_required", wantCode: 10, requiresCode: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWxWorkProtocolLoginStatus(tt.raw)
			if got.Status != tt.wantStatus || got.StatusCode != tt.wantCode || got.RequiresCode != tt.requiresCode {
				t.Fatalf("got status=%q code=%d requiresCode=%v", got.Status, got.StatusCode, got.RequiresCode)
			}
		})
	}
}

func TestParseWxWorkProtocolLoginStatusDoesNotGuessFromContains(t *testing.T) {
	got := parseWxWorkProtocolLoginStatus(`{"data":{"status":"login is still pending"}}`)
	if got.Status != "pending" {
		t.Fatalf("expected pending, got %q", got.Status)
	}
}

func TestParseWxWorkProtocolLoginStatusDoesNotExposeProviderResponse(t *testing.T) {
	result := parseWxWorkProtocolLoginStatus(`{"data":{"status":10,"key":"must-not-return"}}`)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal login status: %v", err)
	}
	if strings.Contains(string(encoded), "must-not-return") || strings.Contains(string(encoded), "rawResponse") {
		t.Fatalf("login status leaked provider response: %s", encoded)
	}
}

func TestClaimWxWorkLoginVerificationAttemptLimit(t *testing.T) {
	const instanceID int64 = 991234
	WxWorkProtocolService.ResetLoginVerificationAttempts(instanceID)
	defer WxWorkProtocolService.ResetLoginVerificationAttempts(instanceID)
	for i := 0; i < wxWorkLoginVerificationMaxAttempts; i++ {
		if !claimWxWorkLoginVerificationAttempt(instanceID) {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if claimWxWorkLoginVerificationAttempt(instanceID) {
		t.Fatal("attempt above the limit should be rejected")
	}
}
