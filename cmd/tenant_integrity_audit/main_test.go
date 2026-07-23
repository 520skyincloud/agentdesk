package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-desk/internal/pkg/config"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestExecuteDoesNotCreateTablesOrModifyData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "audit.db")
	dsn := "file:" + dbPath + "?_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	if err := db.Exec("CREATE TABLE audit_marker (id INTEGER PRIMARY KEY, value TEXT NOT NULL)").Error; err != nil {
		t.Fatalf("create marker table: %v", err)
	}
	if err := db.Exec("INSERT INTO audit_marker (id, value) VALUES (1, 'unchanged')").Error; err != nil {
		t.Fatalf("insert marker row: %v", err)
	}
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()

	configPath := filepath.Join(dir, "config.yaml")
	configContent := fmt.Sprintf("db:\n  type: sqlite\n  dsn: %s\n", dsn)
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := execute([]string{"-config", configPath, "-sample-limit", "3"}, &stdout, &stderr)
	if exitCode != exitViolation {
		t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", exitCode, exitViolation, stdout.String(), stderr.String())
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not structured JSON: %v; output=%s", err, stdout.String())
	}
	if report["status"] != "failed" {
		t.Fatalf("report status = %v, want failed", report["status"])
	}

	verifyDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("reopen fixture db: %v", err)
	}
	var tables []string
	if err := verifyDB.Raw("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name").Scan(&tables).Error; err != nil {
		t.Fatalf("list tables: %v", err)
	}
	if len(tables) != 1 || tables[0] != "audit_marker" {
		t.Fatalf("audit changed schema: %#v", tables)
	}
	var value string
	if err := verifyDB.Raw("SELECT value FROM audit_marker WHERE id = 1").Scan(&value).Error; err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if value != "unchanged" {
		t.Fatalf("marker value = %q, want unchanged", value)
	}
}

func TestReadOnlyDBConfigRejectsMissingSQLiteDatabase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.db")
	_, err := readOnlyDBConfig(testDBConfig("file:" + missing + "?_busy_timeout=5000"))
	if err == nil {
		t.Fatal("expected missing sqlite database to be rejected")
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatalf("missing database was unexpectedly created: %v", statErr)
	}
}

func TestReadOnlyDBConfigForcesSQLiteReadOnlyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create sqlite file: %v", err)
	}
	cfg, err := readOnlyDBConfig(testDBConfig("file:" + path + "?mode=rw&_busy_timeout=1000"))
	if err != nil {
		t.Fatalf("prepare read-only config: %v", err)
	}
	if cfg.DSN != "file:"+path+"?_busy_timeout=1000&mode=ro" {
		t.Fatalf("read-only DSN = %q", cfg.DSN)
	}
}

func TestExecuteRejectsPilotReadinessWithoutEvidenceWindowBeforeOpeningDatabase(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := execute([]string{
		"-config", filepath.Join(t.TempDir(), "missing.yaml"),
		"-readiness-tenant-id", "5",
		"-readiness-level", "pilot",
	}, &stdout, &stderr)
	if exitCode != exitError {
		t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", exitCode, exitError, stdout.String(), stderr.String())
	}
	var output commandErrorOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode command error output: %v; output=%s", err, stdout.String())
	}
	if !strings.Contains(output.Error, "readiness-evidence-start is required") {
		t.Fatalf("unexpected command error: %#v", output)
	}
}

func TestParseReadinessStoreIDsDeduplicatesAndRejectsInvalidValues(t *testing.T) {
	ids, err := parseReadinessStoreIDs("5, 3,5")
	if err != nil {
		t.Fatalf("parse readiness Store IDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != 5 || ids[1] != 3 {
		t.Fatalf("parsed readiness Store IDs=%v", ids)
	}
	for _, raw := range []string{"1,,2", "1,zero", "0", "-1"} {
		if _, err := parseReadinessStoreIDs(raw); err == nil {
			t.Fatalf("invalid readiness Store IDs %q were accepted", raw)
		}
	}
}

func testDBConfig(dsn string) config.DBConfig {
	return config.DBConfig{Type: "sqlite", DSN: dsn}
}
