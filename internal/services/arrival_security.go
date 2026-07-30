package services

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/securex"
)

const arrivalSessionTokenVersion = "arr2"

type arrivalSecurity struct {
	cipher          *securex.AESGCM
	identityKey     []byte
	sessionKey      []byte
	dataMasterKeyID string
}

func newArrivalSecurity() (*arrivalSecurity, error) {
	cfg := config.Current().Arrival
	cipher, err := securex.NewAESGCM(cfg.DataMasterKey)
	if err != nil {
		return nil, fmt.Errorf("到店联动数据加密未配置")
	}
	identityKey := []byte(strings.TrimSpace(cfg.IdentityHMACKey))
	sessionKey := []byte(strings.TrimSpace(cfg.SessionSecret))
	if len(identityKey) < 32 {
		return nil, fmt.Errorf("到店联动身份指纹密钥未配置")
	}
	if len(sessionKey) < 32 {
		return nil, fmt.Errorf("到店联动会话签名密钥未配置")
	}
	return &arrivalSecurity{
		cipher:          cipher,
		identityKey:     identityKey,
		sessionKey:      sessionKey,
		dataMasterKeyID: strings.TrimSpace(cfg.DataMasterKeyID),
	}, nil
}

func (s *arrivalSecurity) Encrypt(purpose, plaintext string) (string, string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", "", nil
	}
	return s.cipher.Encrypt(plaintext, []byte("arrival:"+purpose+":"+s.dataMasterKeyID))
}

func (s *arrivalSecurity) Decrypt(purpose, ciphertext, nonce string) (string, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return "", nil
	}
	return s.cipher.Decrypt(ciphertext, nonce, []byte("arrival:"+purpose+":"+s.dataMasterKeyID))
}

func (s *arrivalSecurity) Fingerprint(purpose, value string) string {
	mac := hmac.New(sha256.New, s.identityKey)
	_, _ = mac.Write([]byte(purpose))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *arrivalSecurity) SessionToken(sessionID int64, expiresAt time.Time) string {
	payload := arrivalSessionTokenVersion + "." + strconv.FormatInt(sessionID, 10) + "." + strconv.FormatInt(expiresAt.Unix(), 10)
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *arrivalSecurity) ContactState(contactWayID int64) string {
	payload := "a" + strconv.FormatInt(contactWayID, 36)
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write([]byte("contact_state:" + payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(signature) > 16 {
		signature = signature[:16]
	}
	return payload + "." + signature
}

func (s *arrivalSecurity) AcquisitionContactState(contactWayID int64) string {
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write([]byte("acquisition_contact_state:" + strconv.FormatInt(contactWayID, 10)))
	sum := mac.Sum(nil)
	return "acq" + hex.EncodeToString(sum[:24])
}

func (s *arrivalSecurity) PublicResourceToken(contactWayID int64) string {
	payload := "qr2." + strconv.FormatInt(contactWayID, 10)
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write([]byte("public_qr:" + payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *arrivalSecurity) ParsePublicResourceToken(token string) (int64, error) {
	parts := strings.Split(strings.TrimSpace(strings.TrimSuffix(token, ".png")), ".")
	if len(parts) != 3 || parts[0] != "qr2" {
		return 0, fmt.Errorf("二维码资源令牌无效")
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("二维码资源令牌无效")
	}
	payload := strings.Join(parts[:2], ".")
	mac := hmac.New(sha256.New, s.sessionKey)
	_, _ = mac.Write([]byte("public_qr:" + payload))
	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(actual, mac.Sum(nil)) {
		return 0, fmt.Errorf("二维码资源令牌无效")
	}
	return id, nil
}

func (s *arrivalSecurity) ParseSessionToken(token string, now time.Time) (int64, time.Time, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 4 || parts[0] != arrivalSessionTokenVersion {
		return 0, time.Time{}, fmt.Errorf("到店会话令牌无效")
	}
	sessionID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || sessionID <= 0 {
		return 0, time.Time{}, fmt.Errorf("到店会话令牌无效")
	}
	expiresUnix, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || expiresUnix <= 0 {
		return 0, time.Time{}, fmt.Errorf("到店会话令牌无效")
	}
	payload := strings.Join(parts[:3], ".")
	expectedMAC := hmac.New(sha256.New, s.sessionKey)
	_, _ = expectedMAC.Write([]byte(payload))
	actual, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || !hmac.Equal(actual, expectedMAC.Sum(nil)) {
		return 0, time.Time{}, fmt.Errorf("到店会话令牌无效")
	}
	expiresAt := time.Unix(expiresUnix, 0)
	if !expiresAt.After(now) {
		return 0, time.Time{}, fmt.Errorf("到店会话令牌已过期")
	}
	return sessionID, expiresAt, nil
}

type arrivalSelectionTokenPayload struct {
	AttemptID int64  `json:"a"`
	Value     string `json:"v"`
	ExpiresAt int64  `json:"e"`
}

func (s *arrivalSecurity) SelectionToken(purpose string, attemptID int64, value string, expiresAt time.Time) (string, error) {
	payload, err := json.Marshal(arrivalSelectionTokenPayload{
		AttemptID: attemptID,
		Value:     strings.TrimSpace(value),
		ExpiresAt: expiresAt.Unix(),
	})
	if err != nil {
		return "", err
	}
	ciphertext, nonce, err := s.Encrypt("selection:"+strings.TrimSpace(purpose), string(payload))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(nonce + "." + ciphertext)), nil
}

func (s *arrivalSecurity) ParseSelectionToken(purpose, token string, attemptID int64, now time.Time) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return "", fmt.Errorf("选择凭证无效")
	}
	parts := strings.SplitN(string(raw), ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("选择凭证无效")
	}
	plaintext, err := s.Decrypt("selection:"+strings.TrimSpace(purpose), parts[1], parts[0])
	if err != nil {
		return "", fmt.Errorf("选择凭证无效")
	}
	payload := arrivalSelectionTokenPayload{}
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil ||
		payload.AttemptID != attemptID ||
		payload.ExpiresAt <= now.Unix() ||
		strings.TrimSpace(payload.Value) == "" {
		return "", fmt.Errorf("选择凭证无效或已过期")
	}
	return strings.TrimSpace(payload.Value), nil
}

func randomArrivalToken(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 24
	}
	buf := make([]byte, byteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func randomWeComAuthorizationState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
