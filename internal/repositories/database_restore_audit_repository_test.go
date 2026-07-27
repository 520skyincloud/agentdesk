package repositories

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type databaseRestoreMigrationFixture struct {
	ID      int64  `gorm:"primaryKey;autoIncrement"`
	Version int64  `gorm:"type:bigint;not null;uniqueIndex"`
	Remark  string `gorm:"type:text;not null"`
	Success bool   `gorm:"not null"`
}

func (databaseRestoreMigrationFixture) TableName() string {
	return "t_migration"
}

type databaseRestoreSecretFixture struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	TenantID        int64  `gorm:"type:bigint;not null;uniqueIndex:uk_restore_secret_tenant_key"`
	APIKey          string `gorm:"type:varchar(255);not null;uniqueIndex:uk_restore_secret_tenant_key"`
	CustomerContent string `gorm:"type:text;not null"`
}

func (databaseRestoreSecretFixture) TableName() string {
	return "t_restore_secret"
}

func TestDatabaseRestoreAuditRepositoryDetectsDataAndSchemaDifferences(t *testing.T) {
	source := newDatabaseRestoreSQLiteFixture(t, filepath.Join(t.TempDir(), "source.db"), false)
	restored := newDatabaseRestoreSQLiteFixture(t, filepath.Join(t.TempDir(), "restored.db"), true)

	sourceSnapshot, err := DatabaseRestoreAuditRepository.Capture(source)
	if err != nil {
		t.Fatalf("capture source snapshot: %v", err)
	}
	restoredSnapshot, err := DatabaseRestoreAuditRepository.Capture(restored)
	if err != nil {
		t.Fatalf("capture restored snapshot: %v", err)
	}
	if sourceSnapshot.EndpointFingerprint == restoredSnapshot.EndpointFingerprint {
		t.Fatal("separate sqlite files resolved to the same endpoint fingerprint")
	}
	if sourceSnapshot.SchemaSHA256 != restoredSnapshot.SchemaSHA256 {
		t.Fatalf("equivalent schemas differ: source=%s restored=%s", sourceSnapshot.SchemaSHA256, restoredSnapshot.SchemaSHA256)
	}
	if sourceSnapshot.DataSHA256 != restoredSnapshot.DataSHA256 {
		t.Fatalf("equivalent data differs: source=%s restored=%s", sourceSnapshot.DataSHA256, restoredSnapshot.DataSHA256)
	}
	if sourceSnapshot.MigrationSHA256 != restoredSnapshot.MigrationSHA256 {
		t.Fatalf("equivalent migrations differ: source=%s restored=%s", sourceSnapshot.MigrationSHA256, restoredSnapshot.MigrationSHA256)
	}
	if sourceSnapshot.MigrationRows != 1 || sourceSnapshot.FailedMigrationRows != 0 {
		t.Fatalf("unexpected migration summary: %#v", sourceSnapshot)
	}

	if err := restored.Exec(
		"UPDATE t_restore_secret SET customer_content = ? WHERE id = ?",
		"different customer content",
		2,
	).Error; err != nil {
		t.Fatalf("mutate restored fixture data: %v", err)
	}
	dataMismatch, err := DatabaseRestoreAuditRepository.Capture(restored)
	if err != nil {
		t.Fatalf("capture data-mismatched snapshot: %v", err)
	}
	if sourceSnapshot.DataSHA256 == dataMismatch.DataSHA256 {
		t.Fatal("data mutation did not change database fingerprint")
	}
	if sourceSnapshot.Tables["t_restore_secret"].DataSHA256 ==
		dataMismatch.Tables["t_restore_secret"].DataSHA256 {
		t.Fatal("data mutation did not change table fingerprint")
	}

	if err := restored.Exec("CREATE INDEX idx_restore_secret_content ON t_restore_secret (customer_content)").Error; err != nil {
		t.Fatalf("mutate restored fixture schema: %v", err)
	}
	schemaMismatch, err := DatabaseRestoreAuditRepository.Capture(restored)
	if err != nil {
		t.Fatalf("capture schema-mismatched snapshot: %v", err)
	}
	if sourceSnapshot.SchemaSHA256 == schemaMismatch.SchemaSHA256 {
		t.Fatal("index mutation did not change schema fingerprint")
	}
	if sourceSnapshot.Tables["t_restore_secret"].SchemaSHA256 ==
		schemaMismatch.Tables["t_restore_secret"].SchemaSHA256 {
		t.Fatal("index mutation did not change table schema fingerprint")
	}
}

func TestDatabaseRestoreAuditRepositoryMySQL(t *testing.T) {
	sourceDSN := strings.TrimSpace(os.Getenv("AGENT_DESK_RESTORE_AUDIT_TEST_MYSQL_SOURCE_DSN"))
	restoredDSN := strings.TrimSpace(os.Getenv("AGENT_DESK_RESTORE_AUDIT_TEST_MYSQL_RESTORED_DSN"))
	if sourceDSN == "" || restoredDSN == "" {
		t.Skip("restore-audit MySQL source and restored DSNs are not configured")
	}
	source, err := gorm.Open(mysql.Open(sourceDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open MySQL source fixture: %v", err)
	}
	restored, err := gorm.Open(mysql.Open(restoredDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open MySQL restored fixture: %v", err)
	}
	for _, db := range []*gorm.DB{source, restored} {
		resetDatabaseRestoreMySQLFixture(t, db)
	}
	t.Cleanup(func() {
		for _, db := range []*gorm.DB{source, restored} {
			dropDatabaseRestoreFixtureTables(t, db)
		}
	})

	sourceSnapshot, err := DatabaseRestoreAuditRepository.Capture(source)
	if err != nil {
		t.Fatalf("capture MySQL source snapshot: %v", err)
	}
	restoredSnapshot, err := DatabaseRestoreAuditRepository.Capture(restored)
	if err != nil {
		t.Fatalf("capture MySQL restored snapshot: %v", err)
	}
	if sourceSnapshot.EndpointFingerprint == restoredSnapshot.EndpointFingerprint {
		t.Fatal("separate MySQL databases resolved to the same endpoint fingerprint")
	}
	if sourceSnapshot.SchemaSHA256 != restoredSnapshot.SchemaSHA256 ||
		sourceSnapshot.DataSHA256 != restoredSnapshot.DataSHA256 ||
		sourceSnapshot.MigrationSHA256 != restoredSnapshot.MigrationSHA256 {
		t.Fatalf("equivalent MySQL restore snapshots differ: source=%#v restored=%#v", sourceSnapshot, restoredSnapshot)
	}
	if err := restored.Model(&databaseRestoreSecretFixture{}).
		Where("id = ?", 1).
		Update("customer_content", "mismatched MySQL content").Error; err != nil {
		t.Fatalf("mutate MySQL restored fixture: %v", err)
	}
	mismatched, err := DatabaseRestoreAuditRepository.Capture(restored)
	if err != nil {
		t.Fatalf("capture mismatched MySQL restore snapshot: %v", err)
	}
	if sourceSnapshot.DataSHA256 == mismatched.DataSHA256 {
		t.Fatal("MySQL data mutation did not change restore fingerprint")
	}
}

func TestDatabaseRestoreAuditRepositoryDoesNotExposeHashedContent(t *testing.T) {
	const apiKey = "credential-secret-must-never-appear"
	const customerContent = "private customer message"
	db := newDatabaseRestoreSQLiteFixtureWithValues(
		t,
		filepath.Join(t.TempDir(), "source.db"),
		apiKey,
		customerContent,
	)
	snapshot, err := DatabaseRestoreAuditRepository.Capture(db)
	if err != nil {
		t.Fatalf("capture source snapshot: %v", err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	output := string(encoded)
	for _, secret := range []string{apiKey, customerContent} {
		if strings.Contains(output, secret) {
			t.Fatalf("snapshot output exposed hashed source content %q", secret)
		}
	}
}

func newDatabaseRestoreSQLiteFixture(t *testing.T, path string, reverseRows bool) *gorm.DB {
	t.Helper()
	return newDatabaseRestoreSQLiteFixtureWithValuesAndOrder(
		t,
		path,
		"credential-secret-must-never-appear",
		"private customer message",
		reverseRows,
	)
}

func newDatabaseRestoreSQLiteFixtureWithValues(
	t *testing.T,
	path string,
	apiKey string,
	customerContent string,
) *gorm.DB {
	t.Helper()
	return newDatabaseRestoreSQLiteFixtureWithValuesAndOrder(t, path, apiKey, customerContent, false)
}

func newDatabaseRestoreSQLiteFixtureWithValuesAndOrder(
	t *testing.T,
	path string,
	apiKey string,
	customerContent string,
	reverseRows bool,
) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+path+"?_busy_timeout=5000"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite restore fixture: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE t_migration (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			version INTEGER NOT NULL UNIQUE,
			remark TEXT NOT NULL,
			success NUMERIC NOT NULL
		)`,
		`CREATE TABLE t_restore_secret (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			api_key TEXT NOT NULL,
			customer_content TEXT NOT NULL,
			UNIQUE (tenant_id, api_key)
		)`,
		`CREATE INDEX idx_restore_secret_tenant ON t_restore_secret (tenant_id)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create restore fixture schema: %v", err)
		}
	}
	if err := db.Exec(
		"INSERT INTO t_migration (id, version, remark, success) VALUES (1, 75, 'fixture', ?)",
		true,
	).Error; err != nil {
		t.Fatalf("seed migration fixture: %v", err)
	}
	rows := [][]any{
		{int64(1), int64(7), apiKey, customerContent},
		{int64(2), int64(8), "credential-second-secret", "second private message"},
	}
	if reverseRows {
		rows[0], rows[1] = rows[1], rows[0]
	}
	for _, row := range rows {
		if err := db.Exec(
			"INSERT INTO t_restore_secret (id, tenant_id, api_key, customer_content) VALUES (?, ?, ?, ?)",
			row...,
		).Error; err != nil {
			t.Fatalf("seed restore fixture data: %v", err)
		}
	}
	return db
}

func resetDatabaseRestoreMySQLFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	dropDatabaseRestoreFixtureTables(t, db)
	if err := db.AutoMigrate(
		&databaseRestoreMigrationFixture{},
		&databaseRestoreSecretFixture{},
	); err != nil {
		t.Fatalf("migrate MySQL restore fixture: %v", err)
	}
	if err := db.Create(&databaseRestoreMigrationFixture{
		ID: 1, Version: 75, Remark: "fixture", Success: true,
	}).Error; err != nil {
		t.Fatalf("seed MySQL migration fixture: %v", err)
	}
	for _, row := range []databaseRestoreSecretFixture{
		{ID: 1, TenantID: 7, APIKey: "credential-secret-must-never-appear", CustomerContent: "private customer message"},
		{ID: 2, TenantID: 8, APIKey: "credential-second-secret", CustomerContent: "second private message"},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed MySQL restore fixture: %v", err)
		}
	}
}

func dropDatabaseRestoreFixtureTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range []any{
		&databaseRestoreSecretFixture{},
		&databaseRestoreMigrationFixture{},
	} {
		if err := db.Migrator().DropTable(table); err != nil {
			t.Fatalf("drop restore fixture table %T: %v", table, err)
		}
	}
}
