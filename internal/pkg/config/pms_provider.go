package config

import "strings"

// The sandbox requires an explicit test environment, independently of the
// provider selection, so administrators can prepare data before switching AI.
func PMSSandboxAvailable() bool {
	cfg := CurrentOrNil()
	return cfg != nil && cfg.PMS.SandboxEnabled && strings.TrimSpace(cfg.PMS.Environment) == "test-2"
}

func PMSSandboxEnabled() bool {
	cfg := CurrentOrNil()
	return PMSSandboxAvailable() && cfg.PMS.Enabled && PMSProvider(cfg.PMS) == "sandbox"
}

func PMSProvider(cfg PMSConfig) string {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		return "hpms"
	}
	return provider
}
