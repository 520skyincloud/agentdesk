package services

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const tenantInvitationCipherVersion = "v1"

func generateTenantCode() (string, error) {
	return randomToken("tn_")
}

func generateTenantInvitationCode() (string, error) {
	return randomToken("inv_")
}

func normalizeTenantInvitationCode(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

func hashTenantInvitationCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeTenantInvitationCode(code)))
	return hex.EncodeToString(sum[:])
}

func encryptTenantInvitationCode(code, encodedKey string) (string, error) {
	aead, err := tenantInvitationAEAD(encodedKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, []byte(normalizeTenantInvitationCode(code)), nil)
	payload := append(nonce, ciphertext...)
	return tenantInvitationCipherVersion + "." + base64.RawURLEncoding.EncodeToString(payload), nil
}

func decryptTenantInvitationCode(value, encodedKey string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ".", 2)
	if len(parts) != 2 || parts[0] != tenantInvitationCipherVersion {
		return "", errors.New("unsupported invitation ciphertext version")
	}
	aead, err := tenantInvitationAEAD(encodedKey)
	if err != nil {
		return "", err
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) <= aead.NonceSize() {
		return "", errors.New("invalid invitation ciphertext")
	}
	if base64.RawURLEncoding.EncodeToString(payload) != parts[1] {
		return "", errors.New("non-canonical invitation ciphertext")
	}
	nonce := payload[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, payload[aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("invitation ciphertext authentication failed")
	}
	return string(plaintext), nil
}

func tenantInvitationAEAD(encodedKey string) (cipher.AEAD, error) {
	encodedKey = strings.TrimSpace(encodedKey)
	if encodedKey == "" {
		return nil, errors.New("公司邀请码加密密钥未配置")
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(encodedKey)
	}
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("公司邀请码加密密钥必须是 base64 编码的 32 字节值")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
