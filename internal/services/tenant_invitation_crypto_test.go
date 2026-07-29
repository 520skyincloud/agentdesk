package services

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestTenantInvitationCryptoRoundTripAndAuthentication(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	code, err := generateTenantInvitationCode()
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if !strings.HasPrefix(code, "inv_") || len(code) < 32 {
		t.Fatalf("unexpected invitation code: %q", code)
	}
	ciphertext, err := encryptTenantInvitationCode(code, key)
	if err != nil {
		t.Fatalf("encrypt code: %v", err)
	}
	if strings.Contains(ciphertext, code) {
		t.Fatal("ciphertext must not contain the plaintext invitation code")
	}
	plaintext, err := decryptTenantInvitationCode(ciphertext, key)
	if err != nil {
		t.Fatalf("decrypt code: %v", err)
	}
	if plaintext != normalizeTenantInvitationCode(code) {
		t.Fatalf("plaintext = %q, want %q", plaintext, normalizeTenantInvitationCode(code))
	}

	parts := strings.SplitN(ciphertext, ".", 2)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode ciphertext payload: %v", err)
	}
	payload[len(payload)-1] ^= 0x01
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(payload)
	if _, err := decryptTenantInvitationCode(tampered, key); err == nil {
		t.Fatal("tampered invitation ciphertext must be rejected")
	}
}

func TestTenantInvitationCryptoRejectsMissingOrInvalidKey(t *testing.T) {
	if _, err := encryptTenantInvitationCode("inv_test", ""); err == nil {
		t.Fatal("missing key must be rejected")
	}
	if _, err := encryptTenantInvitationCode("inv_test", base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("non-256-bit key must be rejected")
	}
}
