package assetaccess

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/pkg/config"
)

const (
	PurposeInline    = "inline"
	PurposeWxWorkCDN = "wxwork_cdn"
	Version          = "1"
)

var (
	ErrExpired          = errors.New("asset access URL expired")
	ErrInvalidSignature = errors.New("asset access URL signature invalid")
	ErrMissingSecret    = errors.New("asset access URL signing secret is not configured")
)

type Claims struct {
	TenantID  int64
	Expires   int64
	Purpose   string
	Signature string
}

func BuildRelativeURL(assetID string, tenantID int64, purpose string) (string, error) {
	return BuildRelativeURLAt(assetID, tenantID, purpose, time.Now())
}

func BuildRelativeURLAt(assetID string, tenantID int64, purpose string, now time.Time) (string, error) {
	assetID = strings.TrimSpace(assetID)
	purpose = normalizePurpose(purpose)
	if assetID == "" || tenantID <= 0 || purpose == "" {
		return "", ErrInvalidSignature
	}
	secret, err := signingSecret()
	if err != nil {
		return "", err
	}
	expires := now.Add(time.Duration(config.Current().Storage.AssetURLTTL()) * time.Second).Unix()
	signature := sign(secret, assetID, tenantID, expires, purpose)
	query := url.Values{}
	query.Set("tenantId", strconv.FormatInt(tenantID, 10))
	query.Set("expires", strconv.FormatInt(expires, 10))
	query.Set("purpose", purpose)
	query.Set("signature", signature)
	query.Set("v", Version)
	return "/api/asset/file/" + url.PathEscape(assetID) + "?" + query.Encode(), nil
}

func Verify(assetID string, claims Claims, now time.Time) error {
	assetID = strings.TrimSpace(assetID)
	claims.Purpose = normalizePurpose(claims.Purpose)
	claims.Signature = strings.TrimSpace(claims.Signature)
	if assetID == "" || claims.TenantID <= 0 || claims.Expires <= 0 || claims.Purpose == "" || claims.Signature == "" {
		return ErrInvalidSignature
	}
	if now.Unix() > claims.Expires {
		return ErrExpired
	}
	secret, err := signingSecret()
	if err != nil {
		return err
	}
	expected := sign(secret, assetID, claims.TenantID, claims.Expires, claims.Purpose)
	provided, err := base64.RawURLEncoding.DecodeString(claims.Signature)
	if err != nil {
		return ErrInvalidSignature
	}
	want, err := base64.RawURLEncoding.DecodeString(expected)
	if err != nil || !hmac.Equal(provided, want) {
		return ErrInvalidSignature
	}
	return nil
}

func AssetIDFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	const prefix = "/api/asset/file/"
	index := strings.Index(parsed.Path, prefix)
	if index < 0 {
		return ""
	}
	assetID, err := url.PathUnescape(strings.TrimPrefix(parsed.Path[index:], prefix))
	if err != nil || strings.Contains(assetID, "/") {
		return ""
	}
	return strings.TrimSpace(assetID)
}

func HasIndependentSigningSecret() bool {
	return strings.TrimSpace(config.Current().Storage.AssetURLSigningSecret) != ""
}

func normalizePurpose(purpose string) string {
	switch strings.TrimSpace(purpose) {
	case PurposeInline:
		return PurposeInline
	case PurposeWxWorkCDN:
		return PurposeWxWorkCDN
	default:
		return ""
	}
}

func signingSecret() ([]byte, error) {
	cfg := config.Current()
	if secret := strings.TrimSpace(cfg.Storage.AssetURLSigningSecret); secret != "" {
		return []byte(secret), nil
	}
	root := strings.TrimSpace(cfg.CustomerSession.Secret)
	if root == "" {
		return nil, ErrMissingSecret
	}
	mac := hmac.New(sha256.New, []byte(root))
	_, _ = mac.Write([]byte("agent-desk:asset-access:v1"))
	return mac.Sum(nil), nil
}

func sign(secret []byte, assetID string, tenantID, expires int64, purpose string) string {
	canonical := fmt.Sprintf("%s\n%s\n%d\n%d\n%s", Version, assetID, tenantID, expires, purpose)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
