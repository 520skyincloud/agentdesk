package bootstrap

import (
	"testing"

	"agent-desk/internal/pkg/config"
)

func TestBackgroundWorkersEnabled(t *testing.T) {
	if backgroundWorkersEnabled(nil) {
		t.Fatal("nil config must not start background workers")
	}
	if backgroundWorkersEnabled(&config.Config{}) {
		t.Fatal("disabled config must not start background workers")
	}
	if !backgroundWorkersEnabled(&config.Config{
		BackgroundWorkers: config.BackgroundWorkerConfig{Enabled: true},
	}) {
		t.Fatal("enabled config must start background workers")
	}
}
