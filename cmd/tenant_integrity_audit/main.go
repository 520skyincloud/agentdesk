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

type commandRestoreDatabaseOutput struct {
	Integrity *services.TenantIntegrityAuditReport   `json:"integrity"`
	Readiness *services.TenantReleaseReadinessReport `json:"readiness"`
}

type commandRestoreGateOutput struct {
	Status              string                                    `json:"status"`
	GeneratedAt         time.Time                                 `json:"generatedAt"`
	RestoreVerification *services.TenantRestoreVerificationReport `json:"restoreVerification"`
	Source              commandRestoreDatabaseOutput              `json:"source"`
	Restored            commandRestoreDatabaseOutput              `json:"restored"`
}

type restoreCommandOptions struct {
	ConfigPath           string
	BackupArtifactPath   string
	ExpectedBackupSHA256 string
	RepositoryRoot       string
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
	restoreConfigPath := flags.String("restore-config", "", "path to isolated restored-database config")
	backupArtifactPath := flags.String("backup-artifact", "", "absolute path to encrypted backup artifact outside the repository")
	backupSHA256 := flags.String("backup-sha256", "", "pre-recorded SHA-256 of the encrypted backup artifact")
	restoreRepositoryRoot := flags.String("restore-repository-root", "", "repository root used to enforce external backup storage; auto-detected by default")
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
	restoreOptions, err := parseRestoreCommandOptions(
		flags,
		*restoreConfigPath,
		*backupArtifactPath,
		*backupSHA256,
		*restoreRepositoryRoot,
		readinessOptions,
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
	if restoreOptions != nil {
		return executeRestoreVerification(
			cfg,
			*restoreOptions,
			*readinessOptions,
			*sampleLimit,
			*pretty,
			stdout,
			stderr,
		)
	}
	return executeTenantAudit(cfg, readinessOptions, *sampleLimit, *pretty, stdout, stderr)
}

func executeTenantAudit(
	cfg *config.Config,
	readinessOptions *services.TenantReleaseReadinessOptions,
	sampleLimit int,
	pretty bool,
	stdout io.Writer,
	stderr io.Writer,
) int {
	dbConfig, err := readOnlyDBConfig(cfg.DB)
	if err != nil {
		writeCommandError(stdout, pretty, err)
		return exitError
	}
	db, sqlDB, err := openAuditDatabase(dbConfig)
	if err != nil {
		writeCommandError(stdout, pretty, fmt.Errorf("open audit database failed: %w", err))
		return exitError
	}
	defer sqlDB.Close()

	var report *services.TenantIntegrityAuditReport
	var readinessReport *services.TenantReleaseReadinessReport
	err = db.Transaction(func(tx *gorm.DB) error {
		var auditErr error
		report, auditErr = services.TenantIntegrityAuditService.Audit(tx, services.TenantIntegrityAuditOptions{
			SampleLimit: sampleLimit,
		})
		if auditErr != nil || readinessOptions == nil {
			return auditErr
		}
		readinessReport, auditErr = services.TenantReleaseReadinessService.Audit(tx, *readinessOptions)
		return auditErr
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeCommandError(stdout, pretty, fmt.Errorf("run tenant integrity audit failed: %w", err))
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
	if err := writeJSON(stdout, pretty, output); err != nil {
		fmt.Fprintf(stderr, "write audit report failed: %v\n", err)
		return exitError
	}
	if report.HasViolations() || (readinessReport != nil && readinessReport.HasViolations()) {
		return exitViolation
	}
	return exitPassed
}

func executeRestoreVerification(
	sourceConfig *config.Config,
	options restoreCommandOptions,
	readinessOptions services.TenantReleaseReadinessOptions,
	sampleLimit int,
	pretty bool,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if strings.TrimSpace(os.Getenv("AGENT_DESK_DB_DSN")) != "" {
		writeCommandError(
			stdout,
			pretty,
			fmt.Errorf("AGENT_DESK_DB_DSN must be unset so source and restore configs resolve independent databases"),
		)
		return exitError
	}
	if sourceConfig.BackgroundWorkers.Enabled {
		writeCommandError(stdout, pretty, fmt.Errorf("source audit config must set backgroundWorkers.enabled=false"))
		return exitError
	}
	restoredConfig, err := config.Load(options.ConfigPath)
	if err != nil {
		writeCommandError(stdout, pretty, fmt.Errorf("load restore config failed: %w", err))
		return exitError
	}
	if restoredConfig.BackgroundWorkers.Enabled {
		writeCommandError(stdout, pretty, fmt.Errorf("restore audit config must set backgroundWorkers.enabled=false"))
		return exitError
	}
	if err := applyRestoreAuditDSNOverrides(sourceConfig, restoredConfig); err != nil {
		writeCommandError(stdout, pretty, err)
		return exitError
	}
	if options.RepositoryRoot == "" {
		options.RepositoryRoot, err = findRepositoryRoot()
		if err != nil {
			writeCommandError(stdout, pretty, err)
			return exitError
		}
	}
	sourceDBConfig, err := readOnlyDBConfig(sourceConfig.DB)
	if err != nil {
		writeCommandError(stdout, pretty, fmt.Errorf("prepare source audit database failed: %w", err))
		return exitError
	}
	restoredDBConfig, err := readOnlyDBConfig(restoredConfig.DB)
	if err != nil {
		writeCommandError(stdout, pretty, fmt.Errorf("prepare restored audit database failed: %w", err))
		return exitError
	}
	sourceDB, sourceSQLDB, err := openAuditDatabase(sourceDBConfig)
	if err != nil {
		writeCommandError(stdout, pretty, fmt.Errorf("open source audit database failed: %w", err))
		return exitError
	}
	defer sourceSQLDB.Close()
	restoredDB, restoredSQLDB, err := openAuditDatabase(restoredDBConfig)
	if err != nil {
		writeCommandError(stdout, pretty, fmt.Errorf("open restored audit database failed: %w", err))
		return exitError
	}
	defer restoredSQLDB.Close()

	var sourceIntegrity *services.TenantIntegrityAuditReport
	var sourceReadiness *services.TenantReleaseReadinessReport
	var restoredIntegrity *services.TenantIntegrityAuditReport
	var restoredReadiness *services.TenantReleaseReadinessReport
	var restoreVerification *services.TenantRestoreVerificationReport
	err = sourceDB.Transaction(func(sourceTx *gorm.DB) error {
		return restoredDB.Transaction(func(restoredTx *gorm.DB) error {
			var auditErr error
			sourceIntegrity, auditErr = services.TenantIntegrityAuditService.Audit(
				sourceTx,
				services.TenantIntegrityAuditOptions{SampleLimit: sampleLimit},
			)
			if auditErr != nil {
				return auditErr
			}
			sourceReadiness, auditErr = services.TenantReleaseReadinessService.Audit(sourceTx, readinessOptions)
			if auditErr != nil {
				return auditErr
			}
			restoredIntegrity, auditErr = services.TenantIntegrityAuditService.Audit(
				restoredTx,
				services.TenantIntegrityAuditOptions{SampleLimit: sampleLimit},
			)
			if auditErr != nil {
				return auditErr
			}
			restoredReadiness, auditErr = services.TenantReleaseReadinessService.Audit(restoredTx, readinessOptions)
			if auditErr != nil {
				return auditErr
			}
			restoreVerification, auditErr = services.TenantRestoreVerificationService.Verify(
				sourceTx,
				restoredTx,
				services.TenantRestoreVerificationOptions{
					BackupArtifactPath:   options.BackupArtifactPath,
					ExpectedBackupSHA256: options.ExpectedBackupSHA256,
					RepositoryRoot:       options.RepositoryRoot,
					MismatchSampleLimit:  sampleLimit,
				},
			)
			return auditErr
		}, &sql.TxOptions{ReadOnly: true})
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeCommandError(stdout, pretty, fmt.Errorf("run restore verification failed: %w", err))
		return exitError
	}

	failed := restoreVerification.HasViolations() ||
		sourceIntegrity.HasViolations() ||
		sourceReadiness.HasViolations() ||
		restoredIntegrity.HasViolations() ||
		restoredReadiness.HasViolations()
	status := "passed"
	if failed {
		status = "failed"
	}
	output := commandRestoreGateOutput{
		Status:              status,
		GeneratedAt:         time.Now().UTC(),
		RestoreVerification: restoreVerification,
		Source: commandRestoreDatabaseOutput{
			Integrity: sourceIntegrity,
			Readiness: sourceReadiness,
		},
		Restored: commandRestoreDatabaseOutput{
			Integrity: restoredIntegrity,
			Readiness: restoredReadiness,
		},
	}
	if err := writeJSON(stdout, pretty, output); err != nil {
		fmt.Fprintf(stderr, "write restore verification report failed: %v\n", err)
		return exitError
	}
	if failed {
		return exitViolation
	}
	return exitPassed
}

func openAuditDatabase(cfg config.DBConfig) (*gorm.DB, *sql.DB, error) {
	db, err := bootstrap.InitDB(cfg)
	if err != nil {
		return nil, nil, err
	}
	db.Logger = logger.Default.LogMode(logger.Silent)
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	return db, sqlDB, nil
}

func applyRestoreAuditDSNOverrides(sourceConfig, restoredConfig *config.Config) error {
	if sourceConfig == nil || restoredConfig == nil {
		return fmt.Errorf("restore audit database configs are required")
	}
	sourceDSN := strings.TrimSpace(os.Getenv("AGENT_DESK_RESTORE_AUDIT_SOURCE_DB_DSN"))
	restoredDSN := strings.TrimSpace(os.Getenv("AGENT_DESK_RESTORE_AUDIT_RESTORED_DB_DSN"))
	if (sourceDSN == "") != (restoredDSN == "") {
		return fmt.Errorf(
			"AGENT_DESK_RESTORE_AUDIT_SOURCE_DB_DSN and AGENT_DESK_RESTORE_AUDIT_RESTORED_DB_DSN must be set together",
		)
	}
	if sourceDSN == "" {
		return nil
	}
	sourceConfig.DB.DSN = sourceDSN
	restoredConfig.DB.DSN = restoredDSN
	return nil
}

func parseRestoreCommandOptions(
	flags *flag.FlagSet,
	configPath string,
	backupArtifactPath string,
	expectedBackupSHA256 string,
	repositoryRoot string,
	readinessOptions *services.TenantReleaseReadinessOptions,
) (*restoreCommandOptions, error) {
	if !restoreCommandRequested(flags) {
		return nil, nil
	}
	configPath = strings.TrimSpace(configPath)
	backupArtifactPath = strings.TrimSpace(backupArtifactPath)
	expectedBackupSHA256 = strings.TrimSpace(expectedBackupSHA256)
	repositoryRoot = strings.TrimSpace(repositoryRoot)
	if configPath == "" {
		return nil, fmt.Errorf("restore-config is required for restore verification")
	}
	if backupArtifactPath == "" {
		return nil, fmt.Errorf("backup-artifact is required for restore verification")
	}
	if !filepath.IsAbs(backupArtifactPath) {
		return nil, fmt.Errorf("backup-artifact must be an absolute path")
	}
	if expectedBackupSHA256 == "" {
		return nil, fmt.Errorf("backup-sha256 is required for restore verification")
	}
	if readinessOptions == nil {
		return nil, fmt.Errorf("readiness-tenant-id or readiness-tenant-code is required for restore verification")
	}
	return &restoreCommandOptions{
		ConfigPath:           configPath,
		BackupArtifactPath:   backupArtifactPath,
		ExpectedBackupSHA256: expectedBackupSHA256,
		RepositoryRoot:       repositoryRoot,
	}, nil
}

func findRepositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current directory failed: %w", err)
	}
	for {
		goModInfo, goModErr := os.Stat(filepath.Join(current, "go.mod"))
		gitInfo, gitErr := os.Stat(filepath.Join(current, ".git"))
		if goModErr == nil && !goModInfo.IsDir() && gitErr == nil && gitInfo != nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("restore-repository-root was not provided and repository root could not be detected")
		}
		current = parent
	}
}

func restoreCommandRequested(flags *flag.FlagSet) bool {
	if flags == nil {
		return false
	}
	requested := false
	flags.Visit(func(item *flag.Flag) {
		switch item.Name {
		case "restore-config", "backup-artifact", "backup-sha256", "restore-repository-root":
			requested = true
		}
	})
	return requested
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
