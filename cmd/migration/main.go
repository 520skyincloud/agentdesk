package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"agent-desk/internal/bootstrap"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/logx"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations completed")
}

func run(args []string) error {
	flags := flag.NewFlagSet("migration", flag.ContinueOnError)
	configPath := flags.String("config", "config/config.yaml", "path to config file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config failed: %w", err)
	}
	logx.Init(logx.Config{
		Level:     cfg.Logger.Level,
		Format:    cfg.Logger.Format,
		AddSource: cfg.Logger.AddSource,
	})

	db, err := bootstrap.InitDB(cfg.DB)
	if err != nil {
		return fmt.Errorf("init db failed: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("access db connection failed: %w", err)
	}
	defer sqlDB.Close()
	if err = bootstrap.InitMigrations(); err != nil {
		return fmt.Errorf("run migrations failed: %w", err)
	}
	return nil
}
