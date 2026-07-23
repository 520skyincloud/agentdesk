package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/services"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSchemaCleanupPrepareExecuteAndRejectReplay(t *testing.T) {
	fixture := newSchemaCleanupCommandFixture(t, "lifecycle")
	runner := fixture.runner()

	var prepareStdout bytes.Buffer
	var prepareStderr bytes.Buffer
	prepareExit := runner.execute(fixture.prepareArgs(), &prepareStdout, &prepareStderr)
	if prepareExit != schemaCleanupExitPassed {
		t.Fatalf(
			"prepare exit=%d stdout=%s stderr=%s",
			prepareExit,
			prepareStdout.String(),
			prepareStderr.String(),
		)
	}
	var prepared schemaCleanupPrepareOutput
	if err := json.Unmarshal(prepareStdout.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare output: %v; output=%s", err, prepareStdout.String())
	}
	if prepared.Status != "prepared" ||
		prepared.Pilot.TenantID != fixture.tenantID ||
		prepared.Pilot.StoreID != fixture.storeID ||
		prepared.RequiredConfirmation == "" {
		t.Fatalf("unexpected prepare output: %#v", prepared)
	}
	if strings.Contains(prepareStdout.String(), fixture.legacySecret) ||
		strings.Contains(prepareStdout.String(), fixture.backupSHA256) {
		t.Fatal("prepare output exposed a secret or full backup fingerprint")
	}

	var executeStdout bytes.Buffer
	var executeStderr bytes.Buffer
	executeExit := runner.execute(
		fixture.executeArgs(prepared.RequiredConfirmation),
		&executeStdout,
		&executeStderr,
	)
	if executeExit != schemaCleanupExitPassed {
		t.Fatalf(
			"execute exit=%d stdout=%s stderr=%s",
			executeExit,
			executeStdout.String(),
			executeStderr.String(),
		)
	}
	var executed schemaCleanupExecuteOutput
	if err := json.Unmarshal(executeStdout.Bytes(), &executed); err != nil {
		t.Fatalf("decode execute output: %v; output=%s", err, executeStdout.String())
	}
	if executed.Status != "passed" ||
		!executed.Gates.TokenConsumed ||
		!executed.Gates.PreCleanupGatePassed ||
		!executed.Gates.PostCleanupGatePassed ||
		len(executed.Applied) != 16 {
		t.Fatalf("unexpected execute output: %#v", executed)
	}
	if strings.Contains(executeStdout.String(), fixture.legacySecret) ||
		strings.Contains(executeStdout.String(), fixture.backupSHA256) {
		t.Fatal("execute output exposed a secret or full backup fingerprint")
	}

	db := fixture.openDB(t)
	for _, table := range services.LegacySchemaCleanupFixedTables() {
		if db.Migrator().HasTable(table) {
			t.Fatalf("legacy table still exists: %s", table)
		}
	}
	for _, target := range services.LegacySchemaCleanupFixedColumns() {
		if db.Migrator().HasColumn(target[0], target[1]) {
			t.Fatalf("legacy column still exists: %s.%s", target[0], target[1])
		}
	}
	var keepValue string
	if err := db.Table("t_cleanup_keep").Select("value").Where("id = ?", 1).Scan(&keepValue).Error; err != nil {
		t.Fatalf("read preserved value: %v", err)
	}
	if keepValue != "untouched" {
		t.Fatalf("preserved value=%q", keepValue)
	}
	tokenInfo, err := os.Stat(filepath.Join(fixture.operationDirectory, schemaCleanupTokenFilename))
	if err != nil || tokenInfo.Size() != 0 {
		t.Fatalf("consumed token file was not erased: info=%v err=%v", tokenInfo, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.operationDirectory, schemaCleanupConsumedFilename)); err != nil {
		t.Fatalf("consumption marker missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.operationDirectory, schemaCleanupResultFilename)); err != nil {
		t.Fatalf("execution result missing: %v", err)
	}

	var replayStdout bytes.Buffer
	replayExit := runner.execute(
		fixture.executeArgs(prepared.RequiredConfirmation),
		&replayStdout,
		io.Discard,
	)
	if replayExit != schemaCleanupExitViolation ||
		!strings.Contains(replayStdout.String(), "already been consumed") {
		t.Fatalf("replay exit=%d output=%s", replayExit, replayStdout.String())
	}
}

func TestSchemaCleanupExecuteRejectsDatabaseChangeWithoutConsumingToken(t *testing.T) {
	fixture := newSchemaCleanupCommandFixture(t, "database-change")
	runner := fixture.runner()
	var prepareStdout bytes.Buffer
	if exitCode := runner.execute(fixture.prepareArgs(), &prepareStdout, io.Discard); exitCode != schemaCleanupExitPassed {
		t.Fatalf("prepare exit=%d output=%s", exitCode, prepareStdout.String())
	}
	var prepared schemaCleanupPrepareOutput
	if err := json.Unmarshal(prepareStdout.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}

	db := fixture.openDB(t)
	if err := db.Exec("INSERT INTO t_cleanup_keep (id, value) VALUES (?, ?)", 2, "changed").Error; err != nil {
		t.Fatalf("change database after prepare: %v", err)
	}
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()

	var executeStdout bytes.Buffer
	exitCode := runner.execute(
		fixture.executeArgs(prepared.RequiredConfirmation),
		&executeStdout,
		io.Discard,
	)
	if exitCode != schemaCleanupExitViolation ||
		!strings.Contains(executeStdout.String(), "database changed") {
		t.Fatalf("execute exit=%d output=%s", exitCode, executeStdout.String())
	}
	tokenInfo, err := os.Stat(filepath.Join(fixture.operationDirectory, schemaCleanupTokenFilename))
	if err != nil || tokenInfo.Size() == 0 {
		t.Fatalf("blocked execution consumed the token: info=%v err=%v", tokenInfo, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.operationDirectory, schemaCleanupConsumedFilename)); !os.IsNotExist(err) {
		t.Fatalf("blocked execution wrote a consumption marker: %v", err)
	}
	db = fixture.openDB(t)
	if !db.Migrator().HasTable("t_ai_config") ||
		!db.Migrator().HasColumn("t_ai_agent", "ai_config_id") {
		t.Fatal("blocked execution modified the legacy schema")
	}
}

func TestSchemaCleanupExecuteRejectsPlanTamperingWithoutConsumingToken(t *testing.T) {
	fixture := newSchemaCleanupCommandFixture(t, "plan-tampering")
	runner := fixture.runner()
	var prepareStdout bytes.Buffer
	if exitCode := runner.execute(fixture.prepareArgs(), &prepareStdout, io.Discard); exitCode != schemaCleanupExitPassed {
		t.Fatalf("prepare exit=%d output=%s", exitCode, prepareStdout.String())
	}
	var prepared schemaCleanupPrepareOutput
	if err := json.Unmarshal(prepareStdout.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}

	planPath := filepath.Join(fixture.operationDirectory, schemaCleanupPlanFilename)
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan schemaCleanupPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	plan.Pilot.StoreName = "tampered"
	writeSchemaCleanupJSONTestFile(t, planPath, plan)

	var executeStdout bytes.Buffer
	exitCode := runner.execute(
		fixture.executeArgs(prepared.RequiredConfirmation),
		&executeStdout,
		io.Discard,
	)
	if exitCode != schemaCleanupExitViolation ||
		!strings.Contains(executeStdout.String(), "authorization is invalid") {
		t.Fatalf("execute exit=%d output=%s", exitCode, executeStdout.String())
	}
	tokenInfo, err := os.Stat(filepath.Join(fixture.operationDirectory, schemaCleanupTokenFilename))
	if err != nil || tokenInfo.Size() == 0 {
		t.Fatalf("tampered plan consumed the token: info=%v err=%v", tokenInfo, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.operationDirectory, schemaCleanupConsumedFilename)); !os.IsNotExist(err) {
		t.Fatalf("tampered plan wrote a consumption marker: %v", err)
	}
}

func TestSchemaCleanupPrepareRequiresResolvedPilotInEvidence(t *testing.T) {
	fixture := newSchemaCleanupCommandFixture(t, "pilot-evidence")
	raw, err := os.ReadFile(fixture.releaseReportPath)
	if err != nil {
		t.Fatal(err)
	}
	var release schemaCleanupReleaseGateEvidence
	if err := json.Unmarshal(raw, &release); err != nil {
		t.Fatal(err)
	}
	release.Readiness.SelectedStoreIDs = []int64{3}
	writeSchemaCleanupJSONTestFile(t, fixture.releaseReportPath, release)

	var stdout bytes.Buffer
	exitCode := fixture.runner().execute(fixture.prepareArgs(), &stdout, io.Discard)
	if exitCode != schemaCleanupExitViolation ||
		!strings.Contains(stdout.String(), "not bound to the resolved pilot") {
		t.Fatalf("prepare exit=%d output=%s", exitCode, stdout.String())
	}
	if _, err := os.Stat(fixture.operationDirectory); !os.IsNotExist(err) {
		t.Fatalf("failed pilot gate created an operation directory: %v", err)
	}
}

func TestSchemaCleanupInspectDoesNotCreateMissingSQLiteDatabase(t *testing.T) {
	t.Setenv("AGENT_DESK_ENV", "")
	t.Setenv("AGENT_DESK_DB_DSN", "")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "missing.db")
	configPath := filepath.Join(dir, "config.yaml")
	writeSchemaCleanupTestFile(t, configPath, []byte(fmt.Sprintf(
		"db:\n  type: sqlite\n  dsn: file:%s\n",
		dbPath,
	)), 0o600)
	var stdout bytes.Buffer
	exitCode := newSchemaCleanupCommandRuntime().execute(
		[]string{"-action", "inspect", "-config", configPath},
		&stdout,
		io.Discard,
	)
	if exitCode != schemaCleanupExitError {
		t.Fatalf("inspect exit=%d output=%s", exitCode, stdout.String())
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("inspect created missing sqlite database: %v", err)
	}
}

func TestSchemaCleanupFixedAllowlistCannotBeExpandedByCLI(t *testing.T) {
	var stderr bytes.Buffer
	options, err := parseSchemaCleanupCommandOptions([]string{
		"-action", "prepare",
		"-environment", "rehearsal",
		"-operation-dir", "/tmp/operation",
		"-release-report", "/tmp/release.json",
		"-restore-report", "/tmp/restore.json",
		"-backup-artifact", "/tmp/backup.age",
		"-backup-sha256", strings.Repeat("0", 64),
		"-pilot-tenant-name", "tenant",
		"-pilot-store-name", "store",
		"-shutdown-confirmation", schemaCleanupShutdownConfirmation,
		"-table", "t_unapproved",
	}, &stderr)
	if err == nil || options.Action != schemaCleanupActionPrepare {
		t.Fatalf("unapproved target flag was accepted: options=%#v err=%v", options, err)
	}
}

type schemaCleanupCommandFixture struct {
	now                time.Time
	root               string
	repositoryRoot     string
	secureRoot         string
	configPath         string
	dbPath             string
	releaseReportPath  string
	restoreReportPath  string
	backupPath         string
	backupSHA256       string
	operationDirectory string
	tenantID           int64
	storeID            int64
	legacySecret       string
}

func newSchemaCleanupCommandFixture(t *testing.T, name string) *schemaCleanupCommandFixture {
	t.Helper()
	t.Setenv("AGENT_DESK_ENV", "")
	t.Setenv("AGENT_DESK_DB_DSN", "")
	t.Setenv("AGENT_DESK_BACKGROUND_WORKERS_ENABLED", "")
	root := t.TempDir()
	repositoryRoot := filepath.Join(root, "repository")
	secureRoot := filepath.Join(root, "secure")
	if err := os.Mkdir(repositoryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repositoryRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSchemaCleanupTestFile(t, filepath.Join(repositoryRoot, "go.mod"), []byte("module fixture\n"), 0o644)
	if err := os.Mkdir(secureRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secureRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := &schemaCleanupCommandFixture{
		now:                time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
		root:               root,
		repositoryRoot:     repositoryRoot,
		secureRoot:         secureRoot,
		configPath:         filepath.Join(repositoryRoot, "config.yaml"),
		dbPath:             filepath.Join(repositoryRoot, "target.db"),
		releaseReportPath:  filepath.Join(secureRoot, "release.json"),
		restoreReportPath:  filepath.Join(secureRoot, "restore.json"),
		backupPath:         filepath.Join(secureRoot, "backup.age"),
		operationDirectory: filepath.Join(secureRoot, "b14-"+name),
		tenantID:           41,
		storeID:            73,
		legacySecret:       "legacy-plaintext-key-must-not-appear",
	}
	fixture.createDatabase(t)
	fixture.createEvidence(t)
	writeSchemaCleanupTestFile(t, fixture.configPath, []byte(fmt.Sprintf(
		"server:\n  port: 8083\ndb:\n  type: sqlite\n  dsn: file:%s?_busy_timeout=5000\nbackgroundWorkers:\n  enabled: false\n",
		fixture.dbPath,
	)), 0o600)
	return fixture
}

func (f *schemaCleanupCommandFixture) runner() *schemaCleanupCommandRuntime {
	runner := newSchemaCleanupCommandRuntime()
	runner.now = func() time.Time { return f.now }
	runner.random = bytes.NewReader(bytes.Repeat([]byte{0x5a}, 64))
	runner.runLiveGate = func(
		_ *gorm.DB,
		_ services.LegacySchemaCleanupPilotIdentity,
		_ time.Time,
	) error {
		return nil
	}
	return runner
}

func (f *schemaCleanupCommandFixture) prepareArgs() []string {
	return []string{
		"-action", "prepare",
		"-config", f.configPath,
		"-environment", "rehearsal",
		"-operation-dir", f.operationDirectory,
		"-repository-root", f.repositoryRoot,
		"-release-report", f.releaseReportPath,
		"-restore-report", f.restoreReportPath,
		"-backup-artifact", f.backupPath,
		"-backup-sha256", f.backupSHA256,
		"-pilot-tenant-name", "丽斯文旅",
		"-pilot-store-name", "高铁南站店",
		"-shutdown-confirmation", schemaCleanupShutdownConfirmation,
	}
}

func (f *schemaCleanupCommandFixture) executeArgs(confirmation string) []string {
	return []string{
		"-action", "execute",
		"-config", f.configPath,
		"-environment", "rehearsal",
		"-operation-dir", f.operationDirectory,
		"-shutdown-confirmation", schemaCleanupShutdownConfirmation,
		"-confirm", confirmation,
	}
}

func (f *schemaCleanupCommandFixture) openDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+f.dbPath+"?_busy_timeout=5000"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	return db
}

func (f *schemaCleanupCommandFixture) createDatabase(t *testing.T) {
	t.Helper()
	db := f.openDB(t)
	statements := []string{
		"CREATE TABLE t_tenant (id BIGINT PRIMARY KEY, tenant_code TEXT, legal_name TEXT, short_name TEXT, status BIGINT)",
		"CREATE TABLE t_store (id BIGINT PRIMARY KEY, tenant_id BIGINT, store_code TEXT, name TEXT, status BIGINT)",
		"CREATE TABLE t_migration (id BIGINT PRIMARY KEY, success BOOLEAN)",
		"CREATE TABLE t_migration_definition_archive (id BIGINT PRIMARY KEY)",
		"CREATE TABLE t_cleanup_keep (id BIGINT PRIMARY KEY, value TEXT)",
		"CREATE TABLE t_conversation_service_session (id BIGINT PRIMARY KEY, tag_ids_json TEXT, keep_text TEXT)",
		"CREATE TABLE t_ai_agent (id BIGINT PRIMARY KEY, ai_config_id BIGINT NOT NULL DEFAULT 0, keep_text TEXT)",
		"CREATE TABLE t_agent_run_log (id BIGINT PRIMARY KEY, ai_config_id BIGINT NOT NULL DEFAULT 0, keep_text TEXT)",
		"CREATE TABLE t_ai_usage_event (id BIGINT PRIMARY KEY, ai_config_id BIGINT NOT NULL DEFAULT 0, keep_text TEXT)",
		"CREATE TABLE t_skill_run_log (id BIGINT PRIMARY KEY, ai_config_id BIGINT NOT NULL DEFAULT 0, keep_text TEXT)",
		"CREATE INDEX idx_t_ai_agent_ai_config_id ON t_ai_agent (ai_config_id)",
		"CREATE INDEX idx_t_agent_run_log_ai_config_id ON t_agent_run_log (ai_config_id)",
		"CREATE INDEX idx_t_ai_usage_event_ai_config_id ON t_ai_usage_event (ai_config_id)",
		"CREATE INDEX idx_t_skill_run_log_ai_config_id ON t_skill_run_log (ai_config_id)",
		"CREATE TABLE t_ai_config (id BIGINT PRIMARY KEY, api_key TEXT)",
		"CREATE TABLE t_tenant_ai_model_grant (id BIGINT PRIMARY KEY, ai_config_id BIGINT)",
		"CREATE TABLE t_store_ai_model_setting (id BIGINT PRIMARY KEY, ai_config_id BIGINT, api_key TEXT)",
		"CREATE TABLE t_conversation_tag (id BIGINT PRIMARY KEY, tag_id BIGINT)",
		"CREATE TABLE t_knowledge_document (id BIGINT PRIMARY KEY, content TEXT)",
		"CREATE TABLE t_knowledge_faq (id BIGINT PRIMARY KEY, answer TEXT)",
		"CREATE TABLE t_knowledge_chunk (id BIGINT PRIMARY KEY, content TEXT)",
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create fixture schema %q: %v", statement, err)
		}
	}
	inserts := []struct {
		query string
		args  []any
	}{
		{
			query: "INSERT INTO t_tenant (id, tenant_code, legal_name, short_name, status) VALUES (?, ?, ?, ?, ?)",
			args:  []any{f.tenantID, "lissi-travel", "丽斯文旅有限公司", "丽斯文旅", 0},
		},
		{
			query: "INSERT INTO t_store (id, tenant_id, store_code, name, status) VALUES (?, ?, ?, ?, ?)",
			args:  []any{f.storeID, f.tenantID, "south-rail", "高铁南站店", 0},
		},
		{query: "INSERT INTO t_migration (id, success) VALUES (?, ?)", args: []any{1, true}},
		{query: "INSERT INTO t_migration_definition_archive (id) VALUES (?)", args: []any{1}},
		{query: "INSERT INTO t_cleanup_keep (id, value) VALUES (?, ?)", args: []any{1, "untouched"}},
		{query: "INSERT INTO t_conversation_service_session (id, tag_ids_json, keep_text) VALUES (?, ?, ?)", args: []any{1, "[9]", "keep"}},
		{query: "INSERT INTO t_ai_agent (id, ai_config_id, keep_text) VALUES (?, ?, ?)", args: []any{1, 9, "keep"}},
		{query: "INSERT INTO t_agent_run_log (id, ai_config_id, keep_text) VALUES (?, ?, ?)", args: []any{1, 9, "keep"}},
		{query: "INSERT INTO t_ai_usage_event (id, ai_config_id, keep_text) VALUES (?, ?, ?)", args: []any{1, 9, "keep"}},
		{query: "INSERT INTO t_skill_run_log (id, ai_config_id, keep_text) VALUES (?, ?, ?)", args: []any{1, 9, "keep"}},
		{query: "INSERT INTO t_ai_config (id, api_key) VALUES (?, ?)", args: []any{9, f.legacySecret}},
		{query: "INSERT INTO t_tenant_ai_model_grant (id, ai_config_id) VALUES (?, ?)", args: []any{1, 9}},
		{query: "INSERT INTO t_store_ai_model_setting (id, ai_config_id, api_key) VALUES (?, ?, ?)", args: []any{1, 9, f.legacySecret}},
		{query: "INSERT INTO t_conversation_tag (id, tag_id) VALUES (?, ?)", args: []any{1, 9}},
		{query: "INSERT INTO t_knowledge_document (id, content) VALUES (?, ?)", args: []any{1, "private-content"}},
		{query: "INSERT INTO t_knowledge_faq (id, answer) VALUES (?, ?)", args: []any{1, "private-content"}},
		{query: "INSERT INTO t_knowledge_chunk (id, content) VALUES (?, ?)", args: []any{1, "private-content"}},
	}
	for _, insert := range inserts {
		if err := db.Exec(insert.query, insert.args...).Error; err != nil {
			t.Fatalf("insert fixture row: %v", err)
		}
	}
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()
}

func (f *schemaCleanupCommandFixture) createEvidence(t *testing.T) {
	t.Helper()
	backupContent := []byte("age-encryption.org/v1\nfixture-encrypted-backup\n")
	writeSchemaCleanupTestFile(t, f.backupPath, backupContent, 0o600)
	backupHash := sha256.Sum256(backupContent)
	f.backupSHA256 = hex.EncodeToString(backupHash[:])

	db := f.openDB(t)
	snapshot, err := services.TenantRestoreVerificationService.CaptureDatabaseSnapshot(db)
	if err != nil {
		t.Fatalf("capture fixture snapshot: %v", err)
	}
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()
	evidenceStart := f.now.Add(-30 * time.Minute)
	integrity := &services.TenantIntegrityAuditReport{
		Status: "passed", GeneratedAt: f.now.Add(-10 * time.Minute),
		DatabaseDriver: "sqlite", Violations: []services.TenantIntegrityAuditViolation{},
	}
	readiness := &services.TenantReleaseReadinessReport{
		Status: "passed", GeneratedAt: f.now.Add(-10 * time.Minute),
		Level: services.TenantReleaseReadinessTagGray, EvidenceStart: &evidenceStart,
		Tenant: services.TenantReleaseReadinessTenant{
			ID: f.tenantID, Code: "lissi-travel", Name: "丽斯文旅",
		},
		SelectedStoreCount: 1,
		SelectedStoreIDs:   []int64{f.storeID},
		Violations:         []services.TenantReleaseReadinessViolation{},
	}
	release := schemaCleanupReleaseGateEvidence{
		Status: "passed", GeneratedAt: f.now.Add(-10 * time.Minute),
		Integrity: integrity, Readiness: readiness,
	}
	writeSchemaCleanupJSONTestFile(t, f.releaseReportPath, release)

	restore := schemaCleanupRestoreGateEvidence{
		Status: "passed", GeneratedAt: f.now.Add(-5 * time.Minute),
		RestoreVerification: &services.TenantRestoreVerificationReport{
			Status: "passed", GeneratedAt: f.now.Add(-5 * time.Minute),
			Backup: services.BackupArtifactVerification{
				SizeBytes: int64(len(backupContent)), SHA256: f.backupSHA256,
				EncryptionFormat: "age", ExternalToRepository: true,
				RestrictedPermissions: true, ExpectedChecksumMatches: true,
			},
			Source:   snapshot,
			Restored: snapshot,
			Comparison: services.DatabaseRestoreSnapshotComparison{
				EndpointDistinct: true, DatabaseDriverMatches: true,
				SchemaMatches: true, DataMatches: true, MigrationMatches: true,
				MissingFromRestoreSamples:  []string{},
				UnexpectedInRestoreSamples: []string{},
				SchemaMismatchTableSamples: []string{},
				DataMismatchTableSamples:   []string{},
			},
			Violations: []services.TenantRestoreVerificationViolation{},
		},
		Source: schemaCleanupRestoreDatabaseEvidence{
			Integrity: integrity, Readiness: readiness,
		},
		Restored: schemaCleanupRestoreDatabaseEvidence{
			Integrity: integrity, Readiness: readiness,
		},
	}
	writeSchemaCleanupJSONTestFile(t, f.restoreReportPath, restore)
}

func writeSchemaCleanupJSONTestFile(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeSchemaCleanupTestFile(t, path, append(raw, '\n'), 0o600)
}

func writeSchemaCleanupTestFile(t *testing.T, path string, raw []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, raw, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
