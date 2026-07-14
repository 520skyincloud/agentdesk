package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-desk/internal/bootstrap"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/services"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	exitPassed    = 0
	exitViolation = 1
	exitError     = 2
)

type commandErrorOutput struct {
	Status      string    `json:"status"`
	GeneratedAt time.Time `json:"generatedAt"`
	Error       string    `json:"error"`
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tenant_integrity_audit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config/config.yaml", "path to config file")
	sampleLimit := flags.Int("sample-limit", 20, "maximum sample record IDs per violation")
	pretty := flags.Bool("pretty", false, "pretty-print JSON output")
	if err := flags.Parse(args); err != nil {
		writeCommandError(stdout, *pretty, err)
		return exitError
	}
	if *sampleLimit <= 0 {
		writeCommandError(stdout, *pretty, fmt.Errorf("sample-limit must be greater than zero"))
		return exitError
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		writeCommandError(stdout, *pretty, fmt.Errorf("load config failed: %w", err))
		return exitError
	}
	dbConfig, err := readOnlyDBConfig(cfg.DB)
	if err != nil {
		writeCommandError(stdout, *pretty, err)
		return exitError
	}
	db, err := bootstrap.InitDB(dbConfig)
	if err != nil {
		writeCommandError(stdout, *pretty, fmt.Errorf("open audit database failed: %w", err))
		return exitError
	}
	db.Logger = logger.Default.LogMode(logger.Silent)
	sqlDB, err := db.DB()
	if err != nil {
		writeCommandError(stdout, *pretty, fmt.Errorf("access audit database failed: %w", err))
		return exitError
	}
	defer sqlDB.Close()

	var report *services.TenantIntegrityAuditReport
	err = db.Transaction(func(tx *gorm.DB) error {
		var auditErr error
		report, auditErr = services.TenantIntegrityAuditService.Audit(tx, services.TenantIntegrityAuditOptions{
			SampleLimit: *sampleLimit,
		})
		return auditErr
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeCommandError(stdout, *pretty, fmt.Errorf("run tenant integrity audit failed: %w", err))
		return exitError
	}
	if err := writeJSON(stdout, *pretty, report); err != nil {
		fmt.Fprintf(stderr, "write audit report failed: %v\n", err)
		return exitError
	}
	if report.HasViolations() {
		return exitViolation
	}
	return exitPassed
}

func readOnlyDBConfig(cfg config.DBConfig) (config.DBConfig, error) {
	if strings.TrimSpace(cfg.Type) != "sqlite" {
		return cfg, nil
	}
	path, err := sqliteDatabasePath(cfg.DSN)
	if err != nil {
		return config.DBConfig{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config.DBConfig{}, fmt.Errorf("sqlite database does not exist: %s", path)
		}
		return config.DBConfig{}, fmt.Errorf("stat sqlite database failed: %w", err)
	}
	if info.IsDir() {
		return config.DBConfig{}, fmt.Errorf("sqlite database path is a directory: %s", path)
	}

	base, rawQuery, _ := strings.Cut(cfg.DSN, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return config.DBConfig{}, fmt.Errorf("parse sqlite DSN query failed: %w", err)
	}
	query.Set("mode", "ro")
	cfg.DSN = base + "?" + query.Encode()
	return cfg, nil
}

func sqliteDatabasePath(dsn string) (string, error) {
	base, rawQuery, _ := strings.Cut(strings.TrimSpace(dsn), "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("parse sqlite DSN query failed: %w", err)
	}
	if strings.EqualFold(query.Get("mode"), "memory") {
		return "", fmt.Errorf("tenant integrity audit requires a persistent sqlite database")
	}
	base = strings.TrimPrefix(base, "file:")
	if base == "" || base == ":memory:" {
		return "", fmt.Errorf("tenant integrity audit requires a persistent sqlite database")
	}
	absPath, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve sqlite database path failed: %w", err)
	}
	return absPath, nil
}

func writeCommandError(writer io.Writer, pretty bool, err error) {
	_ = writeJSON(writer, pretty, commandErrorOutput{
		Status: "error", GeneratedAt: time.Now().UTC(), Error: err.Error(),
	})
}

func writeJSON(writer io.Writer, pretty bool, value any) error {
	encoder := json.NewEncoder(writer)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(value)
}
