package assetaccess

import (
	"errors"
	"net/url"
	"strconv"
	"testing"
	"time"

	"agent-desk/internal/pkg/config"
)

func TestAssetAccessURLVerification(t *testing.T) {
	config.SetCurrent(&config.Config{Storage: config.StorageConfig{
		AssetURLSigningSecret: "asset-signing-secret-for-tests",
		AssetURLTTLSeconds:    60,
	}})
	now := time.Unix(1_800_000_000, 0)
	raw, err := BuildRelativeURLAt("asset-a", 101, PurposeInline, now)
	if err != nil {
		t.Fatalf("BuildRelativeURLAt() error = %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	claims := claimsFromQuery(t, parsed.Query())
	if err := Verify("asset-a", claims, now.Add(59*time.Second)); err != nil {
		t.Fatalf("Verify() before expiry error = %v", err)
	}
	if err := Verify("asset-a", claims, now.Add(61*time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("Verify() expired error = %v", err)
	}

	tamperedTenant := claims
	tamperedTenant.TenantID = 202
	if err := Verify("asset-a", tamperedTenant, now); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Verify() tampered tenant error = %v", err)
	}
	if err := Verify("asset-b", claims, now); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Verify() tampered asset error = %v", err)
	}
	tamperedPurpose := claims
	tamperedPurpose.Purpose = PurposeWxWorkCDN
	if err := Verify("asset-a", tamperedPurpose, now); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Verify() tampered purpose error = %v", err)
	}
}

func TestAssetAccessRequiresSigningSecret(t *testing.T) {
	config.SetCurrent(&config.Config{})
	if _, err := BuildRelativeURL("asset-a", 101, PurposeInline); !errors.Is(err, ErrMissingSecret) {
		t.Fatalf("BuildRelativeURL() error = %v", err)
	}
}

func TestAssetIDFromURL(t *testing.T) {
	for _, raw := range []string{
		"/api/asset/file/asset-a?expires=1",
		"https://files.example.com/api/asset/file/asset-a?expires=1",
	} {
		if got := AssetIDFromURL(raw); got != "asset-a" {
			t.Fatalf("AssetIDFromURL(%q) = %q", raw, got)
		}
	}
	if got := AssetIDFromURL("https://files.example.com/storage/asset-a"); got != "" {
		t.Fatalf("AssetIDFromURL() unexpected id = %q", got)
	}
}

func claimsFromQuery(t *testing.T, query url.Values) Claims {
	t.Helper()
	tenantID, err := strconv.ParseInt(query.Get("tenantId"), 10, 64)
	if err != nil {
		t.Fatalf("parse tenantId: %v", err)
	}
	expires, err := strconv.ParseInt(query.Get("expires"), 10, 64)
	if err != nil {
		t.Fatalf("parse expires: %v", err)
	}
	return Claims{
		TenantID:  tenantID,
		Expires:   expires,
		Purpose:   query.Get("purpose"),
		Signature: query.Get("signature"),
	}
}
