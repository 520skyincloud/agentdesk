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
	schemaCleanupExitPassed    = 0
	schemaCleanupExitViolation = 1
	schemaCleanupExitError     = 2

	schemaCleanupActionInspect = "inspect"
	schemaCleanupActionPrepare = "prepare"
	schemaCleanupActionExecute = "execute"

	schemaCleanupShutdownConfirmation = "STOPPED_8083_AND_ALL_WORKERS"
)

type schemaCleanupCommandErrorOutput struct {
	Status      string    `json:"status"`
	GeneratedAt time.Time `json:"generatedAt"`
	Error       string    `json:"error"`
}

type schemaCleanupInspectOutput struct {
	Status      string                                 `json:"status"`
	GeneratedAt time.Time                              `json:"generatedAt"`
	Inventory   *services.LegacySchemaCleanupInventory `json:"inventory"`
}

type schemaCleanupPrepareOutput struct {
	Status               string                                    `json:"status"`
	GeneratedAt          time.Time                                 `json:"generatedAt"`
	OperationID          string                                    `json:"operationId"`
	OperationDirectory   string                                    `json:"operationDirectory"`
	ExpiresAt            time.Time                                 `json:"expiresAt"`
	RequiredConfirmation string                                    `json:"requiredConfirmation"`
	Pilot                services.LegacySchemaCleanupPilotIdentity `json:"pilot"`
	Inventory            *services.LegacySchemaCleanupInventory    `json:"inventory"`
	Gates                schemaCleanupGateSummary                  `json:"gates"`
}

type schemaCleanupExecuteOutput struct {
	Status      string                                    `json:"status"`
	GeneratedAt time.Time                                 `json:"generatedAt"`
	OperationID string                                    `json:"operationId"`
	Pilot       services.LegacySchemaCleanupPilotIdentity `json:"pilot"`
	Applied     []string                                  `json:"applied"`
	BeforeCode  string                                    `json:"beforeCode"`
	AfterCode   string                                    `json:"afterCode"`
	Gates       schemaCleanupGateSummary                  `json:"gates"`
	Error       string                                    `json:"error,omitempty"`
}

type schemaCleanupGateSummary struct {
	WorkersStopped        bool `json:"workersStopped"`
	ServerStoppedAttested bool `json:"serverStoppedAttested"`
	ReleasePassed         bool `json:"releasePassed"`
	BackupEncrypted       bool `json:"backupEncrypted"`
	BackupChecksumMatched bool `json:"backupChecksumMatched"`
	RestorePassed         bool `json:"restorePassed"`
	DatabaseUnchanged     bool `json:"databaseUnchanged"`
	PreCleanupGatePassed  bool `json:"preCleanupGatePassed"`
	PostCleanupGatePassed bool `json:"postCleanupGatePassed"`
	TokenConsumed         bool `json:"tokenConsumed"`
}

type schemaCleanupCommandOptions struct {
	Action               string
	ConfigPath           string
	Environment          string
	OperationDirectory   string
	RepositoryRoot       string
	ReleaseReportPath    string
	RestoreReportPath    string
	BackupArtifactPath   string
	BackupSHA256         string
	PilotTenantName      string
	PilotStoreName       string
	ShutdownConfirmation string
	Confirmation         string
	EvidenceMaxAge       time.Duration
	PlanTTL              time.Duration
	Pretty               bool
}

type schemaCleanupCommandRuntime struct {
	now         func() time.Time
	random      io.Reader
	inspect     func(*gorm.DB) (*services.LegacySchemaCleanupInventory, error)
	apply       func(*gorm.DB, string) (*services.LegacySchemaCleanupExecutionResult, error)
	capture     func(*gorm.DB) (services.DatabaseRestoreSnapshotSummary, error)
	runLiveGate func(*gorm.DB, services.LegacySchemaCleanupPilotIdentity, time.Time) error
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(args []string, stdout, stderr io.Writer) int {
	return newSchemaCleanupCommandRuntime().execute(args, stdout, stderr)
}

func newSchemaCleanupCommandRuntime() *schemaCleanupCommandRuntime {
	return &schemaCleanupCommandRuntime{
		now:    time.Now,
		random: schemaCleanupCryptoRandomReader,
		inspect: func(db *gorm.DB) (*services.LegacySchemaCleanupInventory, error) {
			return services.LegacySchemaCleanupService.Inspect(db)
		},
		apply: func(
			db *gorm.DB,
			expectedDigest string,
		) (*services.LegacySchemaCleanupExecutionResult, error) {
			return services.LegacySchemaCleanupService.Execute(db, expectedDigest)
		},
		capture: func(db *gorm.DB) (services.DatabaseRestoreSnapshotSummary, error) {
			return services.TenantRestoreVerificationService.CaptureDatabaseSnapshot(db)
		},
		runLiveGate: runSchemaCleanupLiveGate,
	}
}

func (r *schemaCleanupCommandRuntime) execute(args []string, stdout, stderr io.Writer) int {
	options, err := parseSchemaCleanupCommandOptions(args, stderr)
	if err != nil {
		r.writeError(stdout, options.Pretty, err)
		return schemaCleanupExitError
	}
	cfg, err := config.Load(options.ConfigPath)
	if err != nil {
		r.writeError(stdout, options.Pretty, fmt.Errorf("load cleanup config failed: %w", err))
		return schemaCleanupExitError
	}
	if options.Action != schemaCleanupActionInspect {
		if err := validateSchemaCleanupMaintenanceConfig(cfg, options); err != nil {
			r.writeError(stdout, options.Pretty, err)
			return schemaCleanupExitError
		}
	}
	dbConfig, err := existingSchemaCleanupDBConfig(cfg.DB)
	if err != nil {
		r.writeError(stdout, options.Pretty, err)
		return schemaCleanupExitError
	}
	db, sqlDB, err := openSchemaCleanupDatabase(dbConfig)
	if err != nil {
		r.writeError(stdout, options.Pretty, fmt.Errorf("open cleanup database failed"))
		return schemaCleanupExitError
	}
	defer sqlDB.Close()

	switch options.Action {
	case schemaCleanupActionInspect:
		return r.inspectAction(db, options, stdout, stderr)
	case schemaCleanupActionPrepare:
		return r.prepareAction(db, options, stdout, stderr)
	case schemaCleanupActionExecute:
		return r.executeAction(db, options, stdout, stderr)
	default:
		r.writeError(stdout, options.Pretty, fmt.Errorf("unsupported schema cleanup action"))
		return schemaCleanupExitError
	}
}

func (r *schemaCleanupCommandRuntime) inspectAction(
	db *gorm.DB,
	options schemaCleanupCommandOptions,
	stdout io.Writer,
	stderr io.Writer,
) int {
	inventory, err := r.inspect(db)
	if err != nil {
		r.writeError(stdout, options.Pretty, fmt.Errorf("inspect cleanup schema failed: %w", err))
		return schemaCleanupExitError
	}
	status := "ready"
	exitCode := schemaCleanupExitPassed
	if !inventory.Ready {
		status = "blocked"
		exitCode = schemaCleanupExitViolation
	}
	if err := writeSchemaCleanupJSON(stdout, options.Pretty, schemaCleanupInspectOutput{
		Status: status, GeneratedAt: r.now().UTC(), Inventory: inventory,
	}); err != nil {
		fmt.Fprintf(stderr, "write schema cleanup inventory failed: %v\n", err)
		return schemaCleanupExitError
	}
	return exitCode
}

func (r *schemaCleanupCommandRuntime) prepareAction(
	db *gorm.DB,
	options schemaCleanupCommandOptions,
	stdout io.Writer,
	stderr io.Writer,
) int {
	prepared, output, err := r.prepare(db, options)
	if err != nil {
		r.writeError(stdout, options.Pretty, err)
		return schemaCleanupExitViolation
	}
	if err := writeSchemaCleanupOperation(prepared); err != nil {
		r.writeError(stdout, options.Pretty, err)
		return schemaCleanupExitError
	}
	if err := writeSchemaCleanupJSON(stdout, options.Pretty, output); err != nil {
		fmt.Fprintf(stderr, "write schema cleanup preparation failed: %v\n", err)
		return schemaCleanupExitError
	}
	return schemaCleanupExitPassed
}

func (r *schemaCleanupCommandRuntime) executeAction(
	db *gorm.DB,
	options schemaCleanupCommandOptions,
	stdout io.Writer,
	stderr io.Writer,
) int {
	plan, token, evidence, inventory, gates, err := r.revalidatePreparedOperation(db, options)
	if err != nil {
		r.writeError(stdout, options.Pretty, err)
		return schemaCleanupExitViolation
	}
	if err := consumeSchemaCleanupOperationToken(plan, token, r.now().UTC()); err != nil {
		r.writeError(stdout, options.Pretty, err)
		return schemaCleanupExitViolation
	}
	gates.TokenConsumed = true

	result, cleanupErr := r.apply(db, inventory.InventoryDigest)
	if cleanupErr == nil {
		cleanupErr = r.runLiveGate(db, plan.Pilot, evidence.Release.Readiness.EvidenceStart.UTC())
		if cleanupErr == nil {
			gates.PostCleanupGatePassed = true
		}
	}
	output := schemaCleanupExecuteOutput{
		Status:      "passed",
		GeneratedAt: r.now().UTC(),
		OperationID: plan.OperationID,
		Pilot:       plan.Pilot,
		Gates:       gates,
	}
	if result != nil {
		output.Applied = append([]string(nil), result.AppliedSteps...)
		if result.Before != nil {
			output.BeforeCode = result.Before.InventoryCode
		}
		if result.After != nil {
			output.AfterCode = result.After.InventoryCode
		}
	}
	if cleanupErr != nil {
		output.Status = "failed"
		output.Error = cleanupErr.Error()
	}
	if err := writeSchemaCleanupExecutionResult(plan.OperationDirectory, output); err != nil {
		r.writeError(stdout, options.Pretty, err)
		return schemaCleanupExitError
	}
	if cleanupErr != nil {
		r.writeError(
			stdout,
			options.Pretty,
			fmt.Errorf("schema cleanup failed after token consumption; restore the verified backup before retrying"),
		)
		return schemaCleanupExitViolation
	}
	if err := writeSchemaCleanupJSON(stdout, options.Pretty, output); err != nil {
		fmt.Fprintf(stderr, "write schema cleanup result failed: %v\n", err)
		return schemaCleanupExitError
	}
	return schemaCleanupExitPassed
}

func parseSchemaCleanupCommandOptions(args []string, stderr io.Writer) (schemaCleanupCommandOptions, error) {
	flags := flag.NewFlagSet("schema_cleanup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options schemaCleanupCommandOptions
	flags.StringVar(&options.Action, "action", schemaCleanupActionInspect, "action: inspect, prepare, or execute")
	flags.StringVar(&options.ConfigPath, "config", "config/config.yaml", "path to config file")
	flags.StringVar(&options.Environment, "environment", "", "target environment: rehearsal or production")
	flags.StringVar(&options.OperationDirectory, "operation-dir", "", "absolute external directory for the one-time operation")
	flags.StringVar(&options.RepositoryRoot, "repository-root", "", "repository root used to enforce external evidence storage")
	flags.StringVar(&options.ReleaseReportPath, "release-report", "", "absolute B13 tag_gray release report path")
	flags.StringVar(&options.RestoreReportPath, "restore-report", "", "absolute independent restore report path")
	flags.StringVar(&options.BackupArtifactPath, "backup-artifact", "", "absolute encrypted backup artifact path")
	flags.StringVar(&options.BackupSHA256, "backup-sha256", "", "pre-recorded encrypted backup SHA-256")
	flags.StringVar(&options.PilotTenantName, "pilot-tenant-name", "", "pilot Tenant legal or short name")
	flags.StringVar(&options.PilotStoreName, "pilot-store-name", "", "pilot Store name")
	flags.StringVar(
		&options.ShutdownConfirmation,
		"shutdown-confirmation",
		"",
		"exact shutdown attestation required for prepare and execute",
	)
	flags.StringVar(&options.Confirmation, "confirm", "", "exact one-time destructive confirmation printed by prepare")
	flags.DurationVar(&options.EvidenceMaxAge, "evidence-max-age", 2*time.Hour, "maximum accepted B13 evidence age")
	flags.DurationVar(&options.PlanTTL, "plan-ttl", 30*time.Minute, "one-time operation plan lifetime")
	flags.BoolVar(&options.Pretty, "pretty", false, "pretty-print JSON output")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 0 {
		return options, fmt.Errorf("unexpected positional arguments")
	}
	options.Action = strings.ToLower(strings.TrimSpace(options.Action))
	switch options.Action {
	case schemaCleanupActionInspect:
		return options, nil
	case schemaCleanupActionPrepare, schemaCleanupActionExecute:
	default:
		return options, fmt.Errorf("action must be inspect, prepare, or execute")
	}
	options.Environment = strings.ToLower(strings.TrimSpace(options.Environment))
	if options.Environment != "rehearsal" && options.Environment != "production" {
		return options, fmt.Errorf("environment must be rehearsal or production")
	}
	if strings.TrimSpace(options.OperationDirectory) == "" {
		return options, fmt.Errorf("operation-dir is required")
	}
	if options.ShutdownConfirmation != schemaCleanupShutdownConfirmation {
		return options, fmt.Errorf("shutdown-confirmation does not match the required maintenance attestation")
	}
	if options.Action == schemaCleanupActionPrepare {
		if strings.TrimSpace(options.ReleaseReportPath) == "" ||
			strings.TrimSpace(options.RestoreReportPath) == "" ||
			strings.TrimSpace(options.BackupArtifactPath) == "" ||
			strings.TrimSpace(options.BackupSHA256) == "" {
			return options, fmt.Errorf("release-report, restore-report, backup-artifact, and backup-sha256 are required")
		}
		if strings.TrimSpace(options.PilotTenantName) == "" || strings.TrimSpace(options.PilotStoreName) == "" {
			return options, fmt.Errorf("pilot-tenant-name and pilot-store-name are required")
		}
		if options.EvidenceMaxAge <= 0 || options.EvidenceMaxAge > 24*time.Hour {
			return options, fmt.Errorf("evidence-max-age must be greater than zero and no more than 24h")
		}
		if options.PlanTTL <= 0 || options.PlanTTL > 2*time.Hour {
			return options, fmt.Errorf("plan-ttl must be greater than zero and no more than 2h")
		}
	}
	if options.Action == schemaCleanupActionExecute && strings.TrimSpace(options.Confirmation) == "" {
		return options, fmt.Errorf("confirm is required for execute")
	}
	return options, nil
}

func validateSchemaCleanupMaintenanceConfig(
	cfg *config.Config,
	options schemaCleanupCommandOptions,
) error {
	if cfg == nil {
		return fmt.Errorf("schema cleanup config is required")
	}
	if cfg.BackgroundWorkers.Enabled {
		return fmt.Errorf("backgroundWorkers.enabled must be false")
	}
	if options.Environment == "production" && cfg.Server.Port != 8083 {
		return fmt.Errorf("production B14 cleanup requires the final 8083 configuration")
	}
	return nil
}

func openSchemaCleanupDatabase(cfg config.DBConfig) (*gorm.DB, *sql.DB, error) {
	db, err := bootstrap.InitDB(cfg)
	if err != nil {
		return nil, nil, err
	}
	db.Logger = logger.Default.LogMode(logger.Silent)
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	return db, sqlDB, nil
}

func existingSchemaCleanupDBConfig(cfg config.DBConfig) (config.DBConfig, error) {
	if strings.TrimSpace(cfg.Type) != "sqlite" {
		return cfg, nil
	}
	base, rawQuery, _ := strings.Cut(strings.TrimSpace(cfg.DSN), "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return config.DBConfig{}, fmt.Errorf("parse sqlite cleanup DSN failed")
	}
	if strings.EqualFold(query.Get("mode"), "memory") {
		return config.DBConfig{}, fmt.Errorf("schema cleanup requires a persistent sqlite database")
	}
	path := strings.TrimPrefix(base, "file:")
	if path == "" || path == ":memory:" {
		return config.DBConfig{}, fmt.Errorf("schema cleanup requires a persistent sqlite database")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return config.DBConfig{}, fmt.Errorf("resolve sqlite cleanup database failed")
	}
	info, err := os.Stat(absolutePath)
	if err != nil || info.IsDir() {
		return config.DBConfig{}, fmt.Errorf("sqlite cleanup database does not exist or is invalid")
	}
	query.Set("mode", "rw")
	cfg.DSN = "file:" + absolutePath + "?" + query.Encode()
	return cfg, nil
}

func runSchemaCleanupLiveGate(
	db *gorm.DB,
	pilot services.LegacySchemaCleanupPilotIdentity,
	evidenceStart time.Time,
) error {
	integrity, err := services.TenantIntegrityAuditService.Audit(
		db,
		services.TenantIntegrityAuditOptions{SampleLimit: 20},
	)
	if err != nil {
		return err
	}
	if integrity.Status != "passed" || integrity.HasViolations() {
		return fmt.Errorf("live Tenant integrity gate failed")
	}
	readiness, err := services.TenantReleaseReadinessService.Audit(
		db,
		services.TenantReleaseReadinessOptions{
			TenantID:      pilot.TenantID,
			StoreIDs:      []int64{pilot.StoreID},
			Level:         services.TenantReleaseReadinessTagGray,
			EvidenceStart: &evidenceStart,
			SampleLimit:   20,
		},
	)
	if err != nil {
		return err
	}
	if readiness.Status != "passed" ||
		readiness.HasViolations() ||
		!containsSchemaCleanupStoreID(readiness.SelectedStoreIDs, pilot.StoreID) {
		return fmt.Errorf("live B13 tag_gray readiness gate failed")
	}
	return nil
}

func containsSchemaCleanupStoreID(storeIDs []int64, target int64) bool {
	for _, storeID := range storeIDs {
		if storeID == target {
			return true
		}
	}
	return false
}

func (r *schemaCleanupCommandRuntime) writeError(stdout io.Writer, pretty bool, err error) {
	_ = writeSchemaCleanupJSON(stdout, pretty, schemaCleanupCommandErrorOutput{
		Status: "error", GeneratedAt: r.now().UTC(), Error: err.Error(),
	})
}

func writeSchemaCleanupJSON(writer io.Writer, pretty bool, value any) error {
	encoder := json.NewEncoder(writer)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
