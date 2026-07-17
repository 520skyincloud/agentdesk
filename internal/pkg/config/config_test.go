package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsCORSAllowedOrigins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`server:
  port: 8083
  cors:
    allowedOrigins:
      - https://console.example.com
      - http://localhost:3000
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	got := cfg.Server.CORS.AllowedOrigins
	want := []string{"https://console.example.com", "http://localhost:3000"}
	if len(got) != len(want) {
		t.Fatalf("len(AllowedOrigins)=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllowedOrigins[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestLoadAppliesFastGPTSecretEnvironment(t *testing.T) {
	t.Setenv("AGENT_DESK_FASTGPT_ENABLED", "true")
	t.Setenv("AGENT_DESK_FASTGPT_BASE_URL", "https://fastgpt.example.com")
	t.Setenv("AGENT_DESK_FASTGPT_API_KEY", "secret-from-environment")
	t.Setenv("AGENT_DESK_FASTGPT_INTEGRATION_TOKEN", "integration-from-environment")
	t.Setenv("AGENT_DESK_FASTGPT_RETRIEVAL_TOKEN_LIMIT", "400")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.FastGPT.Enabled || cfg.FastGPT.BaseURL != "https://fastgpt.example.com" || cfg.FastGPT.APIKey != "secret-from-environment" || cfg.FastGPT.IntegrationToken != "integration-from-environment" || cfg.FastGPT.RetrievalTokenLimit != 400 {
		t.Fatalf("fastGPT=%#v", cfg.FastGPT)
	}
}

func TestLoadAppliesNewAPIUsageEnvironment(t *testing.T) {
	t.Setenv("AGENT_DESK_NEW_API_USAGE_ENABLED", "true")
	t.Setenv("AGENT_DESK_NEW_API_USAGE_BASE_URL", "https://new-api.example.com")
	t.Setenv("AGENT_DESK_NEW_API_USAGE_ACCESS_TOKEN", "access-token")
	t.Setenv("AGENT_DESK_NEW_API_USAGE_USER_ID", "9")
	t.Setenv("AGENT_DESK_NEW_API_USAGE_FASTGPT_TOKEN_NAME", "fastgpt-platform")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NewAPIUsage.Enabled || cfg.NewAPIUsage.BaseURL != "https://new-api.example.com" || cfg.NewAPIUsage.AccessToken != "access-token" || cfg.NewAPIUsage.UserID != 9 || cfg.NewAPIUsage.FastGPTTokenName != "fastgpt-platform" {
		t.Fatalf("newAPIUsage=%#v", cfg.NewAPIUsage)
	}
}
