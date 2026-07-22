package securex

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

const AESGCMCipherVersion = "aes-256-gcm-v1"

type AESGCM struct {
	aead cipher.AEAD
}

func NewAESGCM(masterKey string) (*AESGCM, error) {
	key, err := decodeMasterKey(masterKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential gcm: %w", err)
	}
	return &AESGCM{aead: aead}, nil
}

func (c *AESGCM) Encrypt(plaintext string, aad []byte) (ciphertext string, nonce string, err error) {
	if c == nil || c.aead == nil {
		return "", "", fmt.Errorf("credential cipher is not initialized")
	}
	if strings.TrimSpace(plaintext) == "" {
		return "", "", fmt.Errorf("credential plaintext is empty")
	}
	nonceBytes := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonceBytes); err != nil {
		return "", "", fmt.Errorf("generate credential nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonceBytes, []byte(plaintext), aad)
	return base64.RawStdEncoding.EncodeToString(sealed), base64.RawStdEncoding.EncodeToString(nonceBytes), nil
}

func (c *AESGCM) Decrypt(ciphertext string, nonce string, aad []byte) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("credential cipher is not initialized")
	}
	sealed, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(ciphertext))
	if err != nil {
		return "", fmt.Errorf("decode credential ciphertext: %w", err)
	}
	nonceBytes, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(nonce))
	if err != nil {
		return "", fmt.Errorf("decode credential nonce: %w", err)
	}
	plaintext, err := c.aead.Open(nil, nonceBytes, sealed, aad)
	if err != nil {
		return "", fmt.Errorf("decrypt credential: %w", err)
	}
	return string(plaintext), nil
}

func Fingerprint(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func FingerprintLast6(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 6 {
		return value
	}
	return value[len(value)-6:]
}

func decodeMasterKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("store model credential master key is not configured")
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, fmt.Errorf("store model credential master key must be 32 bytes encoded as base64 or hex")
}
