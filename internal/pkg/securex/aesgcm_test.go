package securex

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestAESGCMRoundTripRequiresMatchingAAD(t *testing.T) {
	masterKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := NewAESGCM(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("tenant:7:store:9:revision:2")
	ciphertext, nonce, err := cipher.Encrypt("sk-sensitive", aad)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, "sk-sensitive") {
		t.Fatal("ciphertext contains plaintext")
	}
	plaintext, err := cipher.Decrypt(ciphertext, nonce, aad)
	if err != nil || plaintext != "sk-sensitive" {
		t.Fatalf("Decrypt()=(%q,%v)", plaintext, err)
	}
	if _, err := cipher.Decrypt(ciphertext, nonce, []byte("tenant:8:store:9:revision:2")); err == nil {
		t.Fatal("decrypt with a different tenant AAD must fail")
	}
}

func TestAESGCMRejectsInvalidMasterKeyAndTampering(t *testing.T) {
	if _, err := NewAESGCM("short-key"); err == nil {
		t.Fatal("short master key must be rejected")
	}
	masterKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := NewAESGCM(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Encrypt("sk-sensitive", []byte("scope"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cipher.Decrypt(ciphertext+"A", nonce, []byte("scope")); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
}

func TestFingerprintHelpers(t *testing.T) {
	fingerprint := Fingerprint("  sk-sensitive  ")
	if len(fingerprint) != 64 || FingerprintLast6(fingerprint) != fingerprint[len(fingerprint)-6:] {
		t.Fatalf("fingerprint helpers returned %q", fingerprint)
	}
}
