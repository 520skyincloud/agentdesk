package config

import "testing"

func TestPMSSandboxRequiresExplicitTestEnvironmentAndSelection(t *testing.T) {
	previous := CurrentOrNil()
	t.Cleanup(func() { SetCurrent(previous) })
	for _, tc := range []struct {
		name      string
		cfg       PMSConfig
		available bool
		active    bool
	}{
		{"default", PMSConfig{}, false, false},
		{"no environment", PMSConfig{Enabled: true, Provider: "sandbox", SandboxEnabled: true}, false, false},
		{"production", PMSConfig{Enabled: true, Provider: "sandbox", SandboxEnabled: true, Environment: "production"}, false, false},
		{"prepare before switch", PMSConfig{Enabled: true, Provider: "hpms", SandboxEnabled: true, Environment: "test-2"}, true, false},
		{"explicit switch", PMSConfig{Enabled: true, Provider: "sandbox", SandboxEnabled: true, Environment: "test-2"}, true, true},
		{"PMS disabled", PMSConfig{Provider: "sandbox", SandboxEnabled: true, Environment: "test-2"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetCurrent(&Config{PMS: tc.cfg})
			if PMSSandboxAvailable() != tc.available || PMSSandboxEnabled() != tc.active {
				t.Fatalf("available=%v active=%v", PMSSandboxAvailable(), PMSSandboxEnabled())
			}
		})
	}
}

func TestPMSProviderDefaultAndEnvironmentOverrides(t *testing.T) {
	if PMSProvider(PMSConfig{}) != "hpms" {
		t.Fatal("legacy provider must remain HPMS")
	}
	t.Setenv("AGENT_DESK_PMS_PROVIDER", "sandbox")
	t.Setenv("AGENT_DESK_PMS_ENVIRONMENT", "test-2")
	t.Setenv("AGENT_DESK_PMS_SANDBOX_ENABLED", "true")
	cfg := &Config{}
	applyPMSEnv(cfg)
	if cfg.PMS.Provider != "sandbox" || cfg.PMS.Environment != "test-2" || !cfg.PMS.SandboxEnabled {
		t.Fatal("sandbox overrides not applied")
	}
}
