package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestTenantRestoreVerificationPassesEquivalentIsolatedRestore(t *testing.T) {
	root := t.TempDir()
	repositoryRoot := filepath.Join(root, "repository")
	if err := os.Mkdir(repositoryRoot, 0o700); err != nil {
		t.Fatalf("create repository root: %v", err)
	}
	artifactPath, checksum := writeTenantRestoreArtifact(
		t,
		filepath.Join(root, "backup.age"),
		[]byte("age-encryption.org/v1\nfixture encrypted payload"),
		0o600,
	)
	source := newTenantRestoreVerificationSQLite(t, filepath.Join(root, "source.db"))
	restored := newTenantRestoreVerificationSQLite(t, filepath.Join(root, "restored.db"))

	report, err := TenantRestoreVerificationService.Verify(
		source,
		restored,
		TenantRestoreVerificationOptions{
			BackupArtifactPath:   artifactPath,
			ExpectedBackupSHA256: checksum,
			RepositoryRoot:       repositoryRoot,
			MismatchSampleLimit:  5,
		},
	)
	if err != nil {
		t.Fatalf("verify restored database: %v", err)
	}
	if report.HasViolations() || report.Status != "passed" {
		t.Fatalf("equivalent isolated restore did not pass: %#v", report)
	}
	if !report.Comparison.EndpointDistinct ||
		!report.Comparison.SchemaMatches ||
		!report.Comparison.DataMatches ||
		!report.Comparison.MigrationMatches {
		t.Fatalf("unexpected restore comparison: %#v", report.Comparison)
	}
	if !report.Backup.ExternalToRepository ||
		!report.Backup.RestrictedPermissions ||
		!report.Backup.ExpectedChecksumMatches ||
		report.Backup.EncryptionFormat != "age" {
		t.Fatalf("unexpected backup evidence: %#v", report.Backup)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal restore report: %v", err)
	}
	for _, secret := range []string{"credential-restore-test-secret", "private restored customer content"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("restore report exposed source content %q", secret)
		}
	}
}

func TestTenantRestoreVerificationRejectsSameDatabaseAndDataMismatch(t *testing.T) {
	root := t.TempDir()
	repositoryRoot := filepath.Join(root, "repository")
	if err := os.Mkdir(repositoryRoot, 0o700); err != nil {
		t.Fatalf("create repository root: %v", err)
	}
	artifactPath, checksum := writeTenantRestoreArtifact(
		t,
		filepath.Join(root, "backup.age"),
		[]byte("age-encryption.org/v1\nfixture encrypted payload"),
		0o600,
	)
	source := newTenantRestoreVerificationSQLite(t, filepath.Join(root, "source.db"))
	options := TenantRestoreVerificationOptions{
		BackupArtifactPath: artifactPath, ExpectedBackupSHA256: checksum, RepositoryRoot: repositoryRoot,
	}

	sameDatabase, err := TenantRestoreVerificationService.Verify(source, source, options)
	if err != nil {
		t.Fatalf("verify same database: %v", err)
	}
	if !tenantRestoreVerificationHasViolation(sameDatabase, "SOURCE_RESTORE_SAME_DATABASE") {
		t.Fatalf("same database was not rejected: %#v", sameDatabase)
	}

	restored := newTenantRestoreVerificationSQLite(t, filepath.Join(root, "restored.db"))
	if err := restored.Exec(
		"UPDATE t_restore_evidence SET customer_content = ? WHERE id = ?",
		"mismatched content",
		1,
	).Error; err != nil {
		t.Fatalf("mutate restored data: %v", err)
	}
	mismatched, err := TenantRestoreVerificationService.Verify(source, restored, options)
	if err != nil {
		t.Fatalf("verify mismatched restored database: %v", err)
	}
	if !tenantRestoreVerificationHasViolation(mismatched, "DATA_FINGERPRINT_MISMATCH") {
		t.Fatalf("data mismatch was not rejected: %#v", mismatched)
	}
	if mismatched.Comparison.DataMismatchTableCount != 1 ||
		len(mismatched.Comparison.DataMismatchTableSamples) != 1 ||
		mismatched.Comparison.DataMismatchTableSamples[0] != "t_restore_evidence" {
		t.Fatalf("unexpected mismatch diagnostics: %#v", mismatched.Comparison)
	}
}

func TestInspectBackupArtifactEnforcesRepositoryEncryptionChecksumAndPermissions(t *testing.T) {
	root := t.TempDir()
	repositoryRoot := filepath.Join(root, "repository")
	if err := os.Mkdir(repositoryRoot, 0o700); err != nil {
		t.Fatalf("create repository root: %v", err)
	}
	artifactPath, checksum := writeTenantRestoreArtifact(
		t,
		filepath.Join(repositoryRoot, "plain.sql"),
		[]byte("CREATE TABLE leaked_backup (id INTEGER);"),
		0o644,
	)
	evidence, violations, err := inspectBackupArtifact(
		artifactPath,
		strings.Repeat("0", sha256.Size*2),
		repositoryRoot,
	)
	if err != nil {
		t.Fatalf("inspect policy-violating backup: %v", err)
	}
	if evidence.ExternalToRepository ||
		evidence.RestrictedPermissions ||
		evidence.ExpectedChecksumMatches ||
		evidence.EncryptionFormat != "unknown" ||
		evidence.SHA256 != checksum {
		t.Fatalf("unexpected backup policy evidence: %#v", evidence)
	}
	for _, code := range []string{
		"BACKUP_INSIDE_REPOSITORY",
		"BACKUP_PERMISSIONS_TOO_BROAD",
		"BACKUP_ENCRYPTION_UNVERIFIED",
		"BACKUP_CHECKSUM_MISMATCH",
	} {
		if !tenantRestoreViolationListHasCode(violations, code) {
			t.Fatalf("backup policy violations missing %s: %#v", code, violations)
		}
	}
}

func TestDetectBackupEncryptionFormat(t *testing.T) {
	tests := map[string]string{
		"age-encryption.org/v1\n":                   "age",
		"-----BEGIN AGE ENCRYPTED FILE-----\n":      "age-armored",
		"-----BEGIN PGP MESSAGE-----\n":             "openpgp-armored",
		"Salted__encrypted":                         "openssl-salted",
		"SQLite format 3\x00unencrypted database":   "unknown",
		"-- MySQL dump\nCREATE TABLE t_example ();": "unknown",
	}
	for input, expected := range tests {
		if actual := detectBackupEncryptionFormat([]byte(input)); actual != expected {
			t.Fatalf("detectBackupEncryptionFormat(%q)=%q want %q", input, actual, expected)
		}
	}
}

func newTenantRestoreVerificationSQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+path+"?_busy_timeout=5000"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open restore verification sqlite: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE t_migration (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			version INTEGER NOT NULL UNIQUE,
			remark TEXT NOT NULL,
			success NUMERIC NOT NULL
		)`,
		`CREATE TABLE t_migration_definition_archive (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_migration_id INTEGER NOT NULL UNIQUE,
			version INTEGER NOT NULL,
			remark TEXT NOT NULL,
			success NUMERIC NOT NULL
		)`,
		`CREATE TABLE t_restore_evidence (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			api_key TEXT NOT NULL,
			customer_content TEXT NOT NULL
		)`,
		`CREATE INDEX idx_restore_evidence_tenant ON t_restore_evidence (tenant_id)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create restore verification schema: %v", err)
		}
	}
	if err := db.Exec(
		"INSERT INTO t_migration (id, version, remark, success) VALUES (1, 75, 'fixture', ?)",
		true,
	).Error; err != nil {
		t.Fatalf("seed restore verification migration: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO t_restore_evidence (id, tenant_id, api_key, customer_content) VALUES (1, 7, ?, ?)",
		"credential-restore-test-secret",
		"private restored customer content",
	).Error; err != nil {
		t.Fatalf("seed restore verification evidence: %v", err)
	}
	return db
}

func writeTenantRestoreArtifact(
	t *testing.T,
	path string,
	content []byte,
	mode os.FileMode,
) (string, string) {
	t.Helper()
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatalf("write backup artifact: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod backup artifact: %v", err)
	}
	digest := sha256.Sum256(content)
	return path, hex.EncodeToString(digest[:])
}

func tenantRestoreVerificationHasViolation(report *TenantRestoreVerificationReport, code string) bool {
	if report == nil {
		return false
	}
	return tenantRestoreViolationListHasCode(report.Violations, code)
}

func tenantRestoreViolationListHasCode(violations []TenantRestoreVerificationViolation, code string) bool {
	for _, violation := range violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}
