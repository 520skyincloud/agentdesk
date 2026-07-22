package securex

import (
	"encoding/base64"
	"testing"
)

func TestAESGCMRoundTripAndAAD(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := NewAESGCM(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, nonce, err := cipher.Encrypt("sk-secret", []byte("store:1:revision:2"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Decrypt(encrypted, nonce, []byte("store:1:revision:2"))
	if err != nil {
		t.Fatal(err)
	}
	if plain != "sk-secret" {
		t.Fatalf("plain=%q", plain)
	}
	if _, err := cipher.Decrypt(encrypted, nonce, []byte("store:2:revision:2")); err == nil {
		t.Fatal("expected AAD mismatch to fail")
	}
}
