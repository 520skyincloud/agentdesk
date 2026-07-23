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
	"strconv"
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

type commandReleaseGateOutput struct {
	Status      string                                 `json:"status"`
	GeneratedAt time.Time                              `json:"generatedAt"`
	Integrity   *services.TenantIntegrityAuditReport   `json:"integrity"`
	Readiness   *services.TenantReleaseReadinessReport `json:"readiness"`
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
	readinessTenantID := flags.Int64("readiness-tenant-id", 0, "tenant ID for release-readiness audit")
	readinessTenantCode := flags.String("readiness-tenant-code", "", "tenant code for release-readiness audit")
	readinessStoreIDs := flags.String("readiness-store-ids", "", "comma-separated Store IDs; defaults to all active Tenant Stores")
	readinessLevel := flags.String("readiness-level", string(services.TenantReleaseReadinessConfiguration), "release gate: configuration, pilot, or tag_gray")
	readinessEvidenceStart := flags.String("readiness-evidence-start", "", "RFC3339 lower bound for pilot evidence")
	if err := flags.Parse(args); err != nil {
		writeCommandError(stdout, *pretty, err)
		return exitError
	}
	if *sampleLimit <= 0 {
		writeCommandError(stdout, *pretty, fmt.Errorf("sample-limit must be greater than zero"))
		return exitError
	}
	readinessOptions, err := parseReadinessCommandOptions(
		flags,
		*readinessTenantID,
		*readinessTenantCode,
		*readinessStoreIDs,
		*readinessLevel,
		*readinessEvidenceStart,
		*sampleLimit,
	)
	if err != nil {
		writeCommandError(stdout, *pretty, err)
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
	var readinessReport *services.TenantReleaseReadinessReport
	err = db.Transaction(func(tx *gorm.DB) error {
		var auditErr error
		report, auditErr = services.TenantIntegrityAuditService.Audit(tx, services.TenantIntegrityAuditOptions{
			SampleLimit: *sampleLimit,
		})
		if auditErr != nil || readinessOptions == nil {
			return auditErr
		}
		readinessReport, auditErr = services.TenantReleaseReadinessService.Audit(tx, *readinessOptions)
		return auditErr
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeCommandError(stdout, *pretty, fmt.Errorf("run tenant integrity audit failed: %w", err))
		return exitError
	}
	output := any(report)
	if readinessReport != nil {
		status := "passed"
		if report.HasViolations() || readinessReport.HasViolations() {
			status = "failed"
		}
		output = commandReleaseGateOutput{
			Status: status, GeneratedAt: time.Now().UTC(), Integrity: report, Readiness: readinessReport,
		}
	}
	if err := writeJSON(stdout, *pretty, output); err != nil {
		fmt.Fprintf(stderr, "write audit report failed: %v\n", err)
		return exitError
	}
	if report.HasViolations() || (readinessReport != nil && readinessReport.HasViolations()) {
		return exitViolation
	}
	return exitPassed
}

func parseReadinessCommandOptions(
	flags *flag.FlagSet,
	tenantID int64,
	tenantCode string,
	rawStoreIDs string,
	rawLevel string,
	rawEvidenceStart string,
	sampleLimit int,
) (*services.TenantReleaseReadinessOptions, error) {
	if !readinessCommandRequested(flags) {
		return nil, nil
	}
	tenantCode = strings.TrimSpace(tenantCode)
	if tenantID <= 0 && tenantCode == "" {
		return nil, fmt.Errorf("readiness-tenant-id or readiness-tenant-code is required")
	}
	if tenantID > 0 && tenantCode != "" {
		return nil, fmt.Errorf("readiness-tenant-id and readiness-tenant-code are mutually exclusive")
	}
	if tenantID < 0 {
		return nil, fmt.Errorf("readiness-tenant-id must be positive")
	}
	level, err := services.ParseTenantReleaseReadinessLevel(rawLevel)
	if err != nil {
		return nil, err
	}
	storeIDs, err := parseReadinessStoreIDs(rawStoreIDs)
	if err != nil {
		return nil, err
	}
	var evidenceStart *time.Time
	rawEvidenceStart = strings.TrimSpace(rawEvidenceStart)
	if rawEvidenceStart != "" {
		parsed, parseErr := time.Parse(time.RFC3339, rawEvidenceStart)
		if parseErr != nil {
			return nil, fmt.Errorf("readiness-evidence-start must use RFC3339: %w", parseErr)
		}
		parsed = parsed.UTC()
		evidenceStart = &parsed
	}
	if (level == services.TenantReleaseReadinessPilot || level == services.TenantReleaseReadinessTagGray) && evidenceStart == nil {
		return nil, fmt.Errorf("readiness-evidence-start is required for pilot and tag_gray")
	}
	return &services.TenantReleaseReadinessOptions{
		TenantID: tenantID, TenantCode: tenantCode, StoreIDs: storeIDs,
		Level: level, EvidenceStart: evidenceStart, SampleLimit: sampleLimit,
	}, nil
}

func readinessCommandRequested(flags *flag.FlagSet) bool {
	if flags == nil {
		return false
	}
	requested := false
	flags.Visit(func(item *flag.Flag) {
		if strings.HasPrefix(item.Name, "readiness-") {
			requested = true
		}
	})
	return requested
}

func parseReadinessStoreIDs(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	seen := make(map[int64]struct{})
	ret := make([]int64, 0)
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			return nil, fmt.Errorf("readiness-store-ids contains an empty value")
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("readiness-store-ids must contain positive integers")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ret = append(ret, id)
	}
	return ret, nil
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
