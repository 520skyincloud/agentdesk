package bootstrap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-desk/internal/pkg/config"

	"github.com/gin-gonic/gin"
)

func TestNewServerRegistersGinRoutes(t *testing.T) {
	config.SetCurrent(&config.Config{
		Auth:               config.AuthConfig{InvitationEncryptionKey: testInvitationEncryptionKey()},
		TenantRegistration: config.TenantRegistrationConfig{Enabled: true},
		Storage: config.StorageConfig{
			AssetURLSigningSecret: "server-route-asset-signing-secret",
			Local: config.LocalStorageConfig{
				Root:    "storage",
				BaseURL: "/storage",
			},
		},
	})

	app, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	routes := make(map[string]bool)
	for _, route := range app.Routes() {
		routes[route.Method+" "+route.Path] = true
	}

	expected := []string{
		http.MethodPost + " /api/auth/login",
		http.MethodPost + " /api/auth/email-code/send",
		http.MethodPost + " /api/auth/email-code/login",
		http.MethodGet + " /api/auth/oidc_login",
		http.MethodGet + " /api/auth/oidc_callback",
		http.MethodPost + " /api/auth/oidc_exchange",
		http.MethodGet + " /api/auth/profile",
		http.MethodPost + " /api/miniprogram/session/start",
		http.MethodPost + " /api/miniprogram/message/send",
		http.MethodGet + " /api/miniprogram/message/list",
		http.MethodGet + " /api/dashboard/user/list",
		http.MethodGet + " /api/dashboard/user/:id",
		http.MethodPost + " /api/dashboard/user/create",
		http.MethodGet + " /api/dashboard/tenant/list",
		http.MethodGet + " /api/dashboard/tenant/:id",
		http.MethodPost + " /api/dashboard/tenant/create",
		http.MethodGet + " /api/dashboard/tenant-invitation/current",
		http.MethodPost + " /api/dashboard/tenant-invitation/rotate",
		http.MethodGet + " /api/dashboard/tenant-registration/list",
		http.MethodPost + " /api/dashboard/tenant-registration/review",
		http.MethodPost + " /api/auth/register/validate_invite",
		http.MethodPost + " /api/auth/register",
		http.MethodPost + " /api/dashboard/conversation/send_message",
		http.MethodGet + " /api/dashboard/service-analytics/overview",
		http.MethodGet + " /api/dashboard/service-analytics/export",
		http.MethodGet + " /api/dashboard/service-session/list",
		http.MethodGet + " /api/dashboard/quality-inspection/pool",
		http.MethodGet + " /api/dashboard/quality-sampling/list",
		http.MethodGet + " /api/dashboard/conversation-evaluation/list",
		http.MethodGet + " /api/ws/dashboard",
		http.MethodGet + " /api/ws/open",
	}
	for _, route := range expected {
		if !routes[route] {
			t.Fatalf("expected route %s to be registered", route)
		}
	}
}

func TestNewServerKeepsPublicRegistrationDisabledByDefault(t *testing.T) {
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{Local: config.LocalStorageConfig{Root: "storage", BaseURL: "/storage"}},
	})

	app, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	routes := make(map[string]bool)
	for _, route := range app.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		http.MethodPost + " /api/auth/register/validate_invite",
		http.MethodPost + " /api/auth/register",
	} {
		if routes[route] {
			t.Fatalf("public registration route %s should be disabled", route)
		}
	}
	if !routes[http.MethodGet+" /api/dashboard/tenant-registration/list"] {
		t.Fatalf("dashboard registration review route should remain available")
	}
}

func TestNewServerRejectsEnabledRegistrationWithoutEncryptionKey(t *testing.T) {
	config.SetCurrent(&config.Config{
		TenantRegistration: config.TenantRegistrationConfig{Enabled: true},
		Storage:            config.StorageConfig{Local: config.LocalStorageConfig{Root: "storage", BaseURL: "/storage"}},
	})

	if _, err := NewServer(); err == nil || !strings.Contains(err.Error(), "tenant registration configuration") {
		t.Fatalf("NewServer() error=%v want registration configuration error", err)
	}
}

func TestNewServerRejectsEnabledRegistrationWithoutIndependentAssetSigningSecret(t *testing.T) {
	config.SetCurrent(&config.Config{
		Auth:               config.AuthConfig{InvitationEncryptionKey: testInvitationEncryptionKey()},
		TenantRegistration: config.TenantRegistrationConfig{Enabled: true},
		Storage:            config.StorageConfig{Local: config.LocalStorageConfig{Root: "storage", BaseURL: "/storage"}},
	})

	if _, err := NewServer(); err == nil || !strings.Contains(err.Error(), "assetURLSigningSecret") {
		t.Fatalf("NewServer() error=%v want asset signing configuration error", err)
	}
}

func TestNewServerDoesNotExposeLocalStorageDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "private.txt"), []byte("must-not-be-public"), 0600); err != nil {
		t.Fatalf("write private file: %v", err)
	}
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{Local: config.LocalStorageConfig{Root: root, BaseURL: "/storage"}},
	})
	app, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/storage/private.txt", nil))
	if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "must-not-be-public") {
		t.Fatalf("local storage bypass status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestConfigureTrustedProxiesControlsForwardedClientIP(t *testing.T) {
	tests := []struct {
		name           string
		trustedProxies []string
		wantIP         string
	}{
		{name: "forwarded header ignored by default", wantIP: "192.0.2.10"},
		{name: "configured proxy accepted", trustedProxies: []string{"192.0.2.0/24"}, wantIP: "198.51.100.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := gin.New()
			if err := configureTrustedProxies(app, tt.trustedProxies); err != nil {
				t.Fatalf("configureTrustedProxies() error = %v", err)
			}
			app.GET("/client-ip", func(ctx *gin.Context) { ctx.String(http.StatusOK, ctx.ClientIP()) })
			req := httptest.NewRequest(http.MethodGet, "/client-ip", nil)
			req.RemoteAddr = "192.0.2.10:12345"
			req.Header.Set("X-Forwarded-For", "198.51.100.9")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantIP {
				t.Fatalf("ClientIP()=%q want %q", got, tt.wantIP)
			}
		})
	}
}

func TestConfigureTrustedProxiesRejectsInvalidCIDR(t *testing.T) {
	if err := configureTrustedProxies(gin.New(), []string{"not-a-cidr"}); err == nil {
		t.Fatalf("configureTrustedProxies() should reject invalid CIDR")
	}
}

func testInvitationEncryptionKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
}

func TestNewServerExposesPublicAuthOptions(t *testing.T) {
	config.SetCurrent(&config.Config{
		Auth: config.AuthConfig{
			InvitationEncryptionKey: testInvitationEncryptionKey(),
		},
		Storage: config.StorageConfig{
			AssetURLSigningSecret: "server-route-asset-signing-secret",
			Local: config.LocalStorageConfig{
				Root:    "storage",
				BaseURL: "/storage",
			},
		},
		WxWork: config.WxWorkConfig{
			Enabled: true,
		},
		OIDC: config.OIDCConfig{
			Enabled:      false,
			ClientSecret: "must-not-leak",
		},
		Email:              config.EmailConfig{Enabled: true, Password: "smtp-secret"},
		TenantRegistration: config.TenantRegistrationConfig{Enabled: true},
	})

	app, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/options", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			WxWorkEnabled             bool `json:"wxworkEnabled"`
			OIDCEnabled               bool `json:"oidcEnabled"`
			TenantRegistrationEnabled bool `json:"tenantRegistrationEnabled"`
			EmailCodeEnabled          bool `json:"emailCodeEnabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !body.Success {
		t.Fatalf("success=false, body=%s", rec.Body.String())
	}
	if !body.Data.WxWorkEnabled {
		t.Fatalf("wxworkEnabled=false want true")
	}
	if body.Data.OIDCEnabled {
		t.Fatalf("oidcEnabled=true want false")
	}
	if !body.Data.EmailCodeEnabled {
		t.Fatalf("emailCodeEnabled=false want true")
	}
	if !body.Data.TenantRegistrationEnabled {
		t.Fatalf("tenantRegistrationEnabled=false want true")
	}
	if strings.Contains(rec.Body.String(), "must-not-leak") {
		t.Fatalf("response leaked sensitive OIDC config: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "smtp-secret") {
		t.Fatalf("response leaked SMTP config: %s", rec.Body.String())
	}
}

func TestNewServerSeparatesAPIStaticAndSPA(t *testing.T) {
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
			Local: config.LocalStorageConfig{
				Root:    "storage",
				BaseURL: "/storage",
			},
		},
	})

	app, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	tests := []struct {
		path        string
		wantStatus  int
		contentType string
	}{
		{path: "/api/not-exists", wantStatus: http.StatusNotFound, contentType: "application/json"},
		{path: "/dashboard/not-exists", wantStatus: http.StatusOK, contentType: "text/html"},
	}

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

		if rec.Code != tt.wantStatus {
			t.Fatalf("%s status=%d want %d", tt.path, rec.Code, tt.wantStatus)
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), tt.contentType) {
			t.Fatalf("%s Content-Type=%q want %q", tt.path, rec.Header().Get("Content-Type"), tt.contentType)
		}
	}
}

func TestNewServerAllowsConfiguredCORSOrigin(t *testing.T) {
	config.SetCurrent(&config.Config{
		Server: config.ServerConfig{
			CORS: config.CORSConfig{
				AllowedOrigins: []string{"https://console.example.com"},
			},
		},
		Storage: config.StorageConfig{
			Local: config.LocalStorageConfig{
				Root:    "storage",
				BaseURL: "/storage",
			},
		},
	})

	app, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	req.Header.Set("Origin", "https://console.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("Access-Control-Allow-Origin=%q want %q", got, "https://console.example.com")
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Fatalf("Access-Control-Allow-Methods=%q should contain %q", got, http.MethodPost)
	}
	for _, header := range []string{"Accept-Language", "X-Locale", "X-Request-Id", "X-Tenant-ID"} {
		if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, header) {
			t.Fatalf("Access-Control-Allow-Headers=%q should contain %q", got, header)
		}
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary=%q want %q", got, "Origin")
	}
}

func TestNewServerRejectsUnconfiguredCORSOrigin(t *testing.T) {
	config.SetCurrent(&config.Config{
		Server: config.ServerConfig{
			CORS: config.CORSConfig{
				AllowedOrigins: []string{"https://console.example.com"},
			},
		},
		Storage: config.StorageConfig{
			Local: config.LocalStorageConfig{
				Root:    "storage",
				BaseURL: "/storage",
			},
		},
	})

	app, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want %d", rec.Code, http.StatusForbidden)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin=%q want empty", got)
	}
}

func TestNewServerEchoesRequestID(t *testing.T) {
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
			Local: config.LocalStorageConfig{
				Root:    "storage",
				BaseURL: "/storage",
			},
		},
	})

	app, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/not-exists", nil)
	req.Header.Set("X-Request-Id", "trace-123")
	app.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got != "trace-123" {
		t.Fatalf("X-Request-Id=%q want %q", got, "trace-123")
	}
}

func TestNewServerGeneratesRequestID(t *testing.T) {
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
			Local: config.LocalStorageConfig{
				Root:    "storage",
				BaseURL: "/storage",
			},
		},
	})

	app, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/not-exists", nil))

	if got := rec.Header().Get("X-Request-Id"); got == "" {
		t.Fatalf("X-Request-Id should be generated")
	}
}
