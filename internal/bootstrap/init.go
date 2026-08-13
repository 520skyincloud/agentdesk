package bootstrap

import (
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/oidcclient"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/logx"
	"agent-desk/internal/services"
	"agent-desk/internal/services/cronx"
	"agent-desk/internal/wxwork"
	"context"
	"log/slog"

	_ "agent-desk/internal/services/event_handlers"
)

func Init(configPath string) error {
	if err := contracts.ValidateEmbeddedSchemas(); err != nil {
		slog.Error("validate AI runtime contracts failed", "error", err)
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("init config failed", "error", err)
		return err
	}
	config.SetCurrent(cfg)

	logx.Init(logx.Config{
		Level:     cfg.Logger.Level,
		Format:    cfg.Logger.Format,
		AddSource: cfg.Logger.AddSource,
	})

	if _, err := InitDB(cfg.DB); err != nil {
		slog.Error("init db failed", "error", err)
		return err
	}
	if err := InitMigrations(); err != nil {
		slog.Error("init migrations failed", "error", err)
		return err
	}
	if err := services.ReplyActionCatalogService.Seed(); err != nil {
		slog.Error("seed reply action catalog failed", "error", err)
		return err
	}
	if backgroundWorkersEnabled(cfg) {
		cronx.Init()
	} else {
		slog.Info("background workers disabled")
	}

	wxwork.Init()
	if err := oidcclient.Init(context.Background()); err != nil {
		slog.Error("init oidc failed", "error", err)
		return err
	}
	return nil
}

func backgroundWorkersEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.BackgroundWorkers.Enabled
}
