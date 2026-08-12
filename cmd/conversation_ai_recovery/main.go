package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"agent-desk/internal/bootstrap"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/logx"
	"agent-desk/internal/services"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("conversation AI recovery failed", "error", err)
		os.Exit(1)
	}
	slog.Info("conversation AI recovery completed")
}

func run(args []string) error {
	flags := flag.NewFlagSet("conversation-ai-recovery", flag.ContinueOnError)
	configPath := flags.String("config", "config/config.yaml", "path to config file")
	conversationID := flags.Int64("conversation", 0, "conversation ID to restore")
	reason := flags.String("reason", "模型协议修复后恢复AI接待", "auditable recovery reason")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *conversationID <= 0 {
		return fmt.Errorf("conversation ID must be positive")
	}
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("recovery reason is required")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config failed: %w", err)
	}
	config.SetCurrent(cfg)
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
	return services.ConversationAIRecoveryService.Restore(*conversationID, *reason, time.Now())
}
