package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-desk/internal/pkg/config"
)

func TestNewServerRegistersGinRoutes(t *testing.T) {
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

	routes := make(map[string]bool)
	for _, route := range app.Routes() {
		routes[route.Method+" "+route.Path] = true
	}

	expected := []string{
		http.MethodPost + " /api/auth/login",
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
		http.MethodPost + " /api/dashboard/conversation/send_message",
		http.MethodGet + " /api/ws/dashboard",
		http.MethodGet + " /api/ws/open",
	}
	for _, route := range expected {
		if !routes[route] {
			t.Fatalf("expected route %s to be registered", route)
		}
	}
}

func TestNewServerExposesPublicAuthOptions(t *testing.T) {
	config.SetCurrent(&config.Config{
		Storage: config.StorageConfig{
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
			WxWorkEnabled bool `json:"wxworkEnabled"`
			OIDCEnabled   bool `json:"oidcEnabled"`
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
	if strings.Contains(rec.Body.String(), "must-not-leak") {
		t.Fatalf("response leaked sensitive OIDC config: %s", rec.Body.String())
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
