package services

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/repositories"

	"gorm.io/gorm"
)

var TenantRestoreVerificationService = newTenantRestoreVerificationService()

func newTenantRestoreVerificationService() *tenantRestoreVerificationService {
	return &tenantRestoreVerificationService{}
}

type tenantRestoreVerificationService struct{}

type TenantRestoreVerificationOptions struct {
	BackupArtifactPath   string
	ExpectedBackupSHA256 string
	RepositoryRoot       string
	MismatchSampleLimit  int
	Now                  time.Time
}

type TenantRestoreVerificationReport struct {
	Status      string                               `json:"status"`
	GeneratedAt time.Time                            `json:"generatedAt"`
	Backup      BackupArtifactVerification           `json:"backup"`
	Source      DatabaseRestoreSnapshotSummary       `json:"source"`
	Restored    DatabaseRestoreSnapshotSummary       `json:"restored"`
	Comparison  DatabaseRestoreSnapshotComparison    `json:"comparison"`
	Violations  []TenantRestoreVerificationViolation `json:"violations"`
}

type BackupArtifactVerification struct {
	SizeBytes               int64  `json:"sizeBytes"`
	SHA256                  string `json:"sha256"`
	EncryptionFormat        string `json:"encryptionFormat"`
	ExternalToRepository    bool   `json:"externalToRepository"`
	RestrictedPermissions   bool   `json:"restrictedPermissions"`
	ExpectedChecksumMatches bool   `json:"expectedChecksumMatches"`
}

type DatabaseRestoreSnapshotSummary struct {
	DatabaseDriver        string `json:"databaseDriver"`
	ApplicationTableCount int    `json:"applicationTableCount"`
	TotalRows             int64  `json:"totalRows"`
	SchemaSHA256          string `json:"schemaSha256"`
	DataSHA256            string `json:"dataSha256"`
	MigrationSHA256       string `json:"migrationSha256"`
	MigrationRows         int64  `json:"migrationRows"`
	MigrationArchiveRows  int64  `json:"migrationArchiveRows"`
	FailedMigrationRows   int64  `json:"failedMigrationRows"`
}

type DatabaseRestoreSnapshotComparison struct {
	EndpointDistinct           bool     `json:"endpointDistinct"`
	DatabaseDriverMatches      bool     `json:"databaseDriverMatches"`
	SchemaMatches              bool     `json:"schemaMatches"`
	DataMatches                bool     `json:"dataMatches"`
	MigrationMatches           bool     `json:"migrationMatches"`
	MissingFromRestoreCount    int      `json:"missingFromRestoreCount"`
	MissingFromRestoreSamples  []string `json:"missingFromRestoreSamples"`
	UnexpectedInRestoreCount   int      `json:"unexpectedInRestoreCount"`
	UnexpectedInRestoreSamples []string `json:"unexpectedInRestoreSamples"`
	SchemaMismatchTableCount   int      `json:"schemaMismatchTableCount"`
	SchemaMismatchTableSamples []string `json:"schemaMismatchTableSamples"`
	DataMismatchTableCount     int      `json:"dataMismatchTableCount"`
	DataMismatchTableSamples   []string `json:"dataMismatchTableSamples"`
}

type TenantRestoreVerificationViolation struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r *TenantRestoreVerificationReport) HasViolations() bool {
	return r != nil && len(r.Violations) > 0
}

func (s *tenantRestoreVerificationService) CaptureDatabaseSnapshot(
	db *gorm.DB,
) (DatabaseRestoreSnapshotSummary, error) {
	snapshot, err := repositories.DatabaseRestoreAuditRepository.Capture(db)
	if err != nil {
		return DatabaseRestoreSnapshotSummary{}, err
	}
	return databaseRestoreSnapshotSummary(snapshot), nil
}

func (s *tenantRestoreVerificationService) Verify(
	sourceDB *gorm.DB,
	restoredDB *gorm.DB,
	options TenantRestoreVerificationOptions,
) (*TenantRestoreVerificationReport, error) {
	if sourceDB == nil || restoredDB == nil {
		return nil, fmt.Errorf("restore verification requires source and restored databases")
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	sampleLimit := options.MismatchSampleLimit
	if sampleLimit <= 0 {
		sampleLimit = 20
	}
	if sampleLimit > 1000 {
		sampleLimit = 1000
	}

	backup, backupViolations, err := inspectBackupArtifact(
		options.BackupArtifactPath,
		options.ExpectedBackupSHA256,
		options.RepositoryRoot,
	)
	if err != nil {
		return nil, err
	}
	sourceSnapshot, err := repositories.DatabaseRestoreAuditRepository.Capture(sourceDB)
	if err != nil {
		return nil, fmt.Errorf("capture source database restore snapshot failed: %w", err)
	}
	restoredSnapshot, err := repositories.DatabaseRestoreAuditRepository.Capture(restoredDB)
	if err != nil {
		return nil, fmt.Errorf("capture restored database snapshot failed: %w", err)
	}

	report := &TenantRestoreVerificationReport{
		Status:      "passed",
		GeneratedAt: now.UTC(),
		Backup:      backup,
		Source:      databaseRestoreSnapshotSummary(sourceSnapshot),
		Restored:    databaseRestoreSnapshotSummary(restoredSnapshot),
		Violations:  append([]TenantRestoreVerificationViolation{}, backupViolations...),
	}
	report.Comparison = compareDatabaseRestoreSnapshots(sourceSnapshot, restoredSnapshot, sampleLimit)
	if !report.Comparison.EndpointDistinct {
		report.addViolation("SOURCE_RESTORE_SAME_DATABASE", "源库与恢复库解析到同一个数据库端点，恢复演练必须使用隔离数据库")
	}
	if !report.Comparison.DatabaseDriverMatches {
		report.addViolation("DATABASE_DRIVER_MISMATCH", "源库与恢复库数据库类型不一致")
	}
	if report.Comparison.MissingFromRestoreCount > 0 || report.Comparison.UnexpectedInRestoreCount > 0 {
		report.addViolation("APPLICATION_TABLE_SET_MISMATCH", "恢复库的应用表集合与源库不一致")
	}
	if !report.Comparison.SchemaMatches {
		report.addViolation("SCHEMA_FINGERPRINT_MISMATCH", "恢复库的列与索引 Schema 指纹和源库不一致")
	}
	if !report.Comparison.DataMatches {
		report.addViolation("DATA_FINGERPRINT_MISMATCH", "恢复库的全表数据指纹和源库不一致")
	}
	if !report.Comparison.MigrationMatches {
		report.addViolation("MIGRATION_FINGERPRINT_MISMATCH", "恢复库的 Migration 与归档指纹和源库不一致")
	}
	if sourceSnapshot.FailedMigrationRows > 0 {
		report.addViolation("SOURCE_FAILED_MIGRATION", "源库存在未成功的 Migration 记录")
	}
	if restoredSnapshot.FailedMigrationRows > 0 {
		report.addViolation("RESTORED_FAILED_MIGRATION", "恢复库存在未成功的 Migration 记录")
	}
	for _, snapshot := range []struct {
		label string
		value *repositories.DatabaseRestoreSnapshot
	}{
		{label: "源库", value: sourceSnapshot},
		{label: "恢复库", value: restoredSnapshot},
	} {
		if _, ok := snapshot.value.Tables["t_migration"]; !ok {
			report.addViolation("MISSING_MIGRATION_TABLE", snapshot.label+"缺少 t_migration")
		}
		if _, ok := snapshot.value.Tables["t_migration_definition_archive"]; !ok {
			report.addViolation("MISSING_MIGRATION_ARCHIVE_TABLE", snapshot.label+"缺少 t_migration_definition_archive")
		}
	}
	if report.HasViolations() {
		report.Status = "failed"
	}
	return report, nil
}

func inspectBackupArtifact(
	artifactPath string,
	expectedSHA256 string,
	repositoryRoot string,
) (BackupArtifactVerification, []TenantRestoreVerificationViolation, error) {
	artifactPath = strings.TrimSpace(artifactPath)
	if artifactPath == "" || !filepath.IsAbs(artifactPath) {
		return BackupArtifactVerification{}, nil, fmt.Errorf("backup artifact path must be absolute")
	}
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if len(expectedSHA256) != sha256.Size*2 {
		return BackupArtifactVerification{}, nil, fmt.Errorf("expected backup SHA-256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(expectedSHA256); err != nil {
		return BackupArtifactVerification{}, nil, fmt.Errorf("expected backup SHA-256 is invalid")
	}
	repositoryRoot = strings.TrimSpace(repositoryRoot)
	if repositoryRoot == "" {
		repositoryRoot = "."
	}
	repositoryRoot, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return BackupArtifactVerification{}, nil, fmt.Errorf("resolve repository root failed: %w", err)
	}
	repositoryRoot, err = filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return BackupArtifactVerification{}, nil, fmt.Errorf("resolve repository root symlinks failed: %w", err)
	}

	info, err := os.Lstat(artifactPath)
	if err != nil {
		return BackupArtifactVerification{}, nil, fmt.Errorf("inspect backup artifact failed: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return BackupArtifactVerification{}, nil, fmt.Errorf("backup artifact must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return BackupArtifactVerification{}, nil, fmt.Errorf("backup artifact must be a regular file")
	}
	if info.Size() <= 0 {
		return BackupArtifactVerification{}, nil, fmt.Errorf("backup artifact must not be empty")
	}
	resolvedArtifactPath, err := filepath.EvalSymlinks(artifactPath)
	if err != nil {
		return BackupArtifactVerification{}, nil, fmt.Errorf("resolve backup artifact failed: %w", err)
	}

	file, err := os.Open(resolvedArtifactPath)
	if err != nil {
		return BackupArtifactVerification{}, nil, fmt.Errorf("open backup artifact failed: %w", err)
	}
	defer file.Close()
	header := make([]byte, 128)
	headerSize, readErr := file.Read(header)
	if readErr != nil && readErr != io.EOF {
		return BackupArtifactVerification{}, nil, fmt.Errorf("read backup artifact header failed: %w", readErr)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return BackupArtifactVerification{}, nil, fmt.Errorf("rewind backup artifact failed: %w", err)
	}
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return BackupArtifactVerification{}, nil, fmt.Errorf("hash backup artifact failed: %w", err)
	}
	if size != info.Size() {
		return BackupArtifactVerification{}, nil, fmt.Errorf("backup artifact changed while it was being verified")
	}
	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	encryptionFormat := detectBackupEncryptionFormat(header[:headerSize])

	relativePath, err := filepath.Rel(repositoryRoot, resolvedArtifactPath)
	if err != nil {
		return BackupArtifactVerification{}, nil, fmt.Errorf("compare backup artifact and repository paths failed: %w", err)
	}
	externalToRepository := relativePath == ".." ||
		strings.HasPrefix(relativePath, ".."+string(filepath.Separator))
	restrictedPermissions := info.Mode().Perm()&0o077 == 0
	checksumMatches := actualSHA256 == expectedSHA256
	evidence := BackupArtifactVerification{
		SizeBytes:               size,
		SHA256:                  actualSHA256,
		EncryptionFormat:        encryptionFormat,
		ExternalToRepository:    externalToRepository,
		RestrictedPermissions:   restrictedPermissions,
		ExpectedChecksumMatches: checksumMatches,
	}
	violations := make([]TenantRestoreVerificationViolation, 0, 4)
	if !externalToRepository {
		violations = append(violations, TenantRestoreVerificationViolation{
			Code: "BACKUP_INSIDE_REPOSITORY", Message: "备份文件必须存放在 Git 仓库外",
		})
	}
	if !restrictedPermissions {
		violations = append(violations, TenantRestoreVerificationViolation{
			Code: "BACKUP_PERMISSIONS_TOO_BROAD", Message: "备份文件不得向 group 或 other 开放文件权限",
		})
	}
	if encryptionFormat == "unknown" {
		violations = append(violations, TenantRestoreVerificationViolation{
			Code: "BACKUP_ENCRYPTION_UNVERIFIED", Message: "备份文件不是受支持的 age、ASCII-armored OpenPGP 或 OpenSSL salted 加密容器",
		})
	}
	if !checksumMatches {
		violations = append(violations, TenantRestoreVerificationViolation{
			Code: "BACKUP_CHECKSUM_MISMATCH", Message: "备份文件 SHA-256 与恢复前固定值不一致",
		})
	}
	return evidence, violations, nil
}

func detectBackupEncryptionFormat(header []byte) string {
	trimmed := bytes.TrimSpace(header)
	switch {
	case bytes.HasPrefix(trimmed, []byte("age-encryption.org/v1")):
		return "age"
	case bytes.HasPrefix(trimmed, []byte("-----BEGIN AGE ENCRYPTED FILE-----")):
		return "age-armored"
	case bytes.HasPrefix(trimmed, []byte("-----BEGIN PGP MESSAGE-----")):
		return "openpgp-armored"
	case bytes.HasPrefix(header, []byte("Salted__")):
		return "openssl-salted"
	default:
		return "unknown"
	}
}

func compareDatabaseRestoreSnapshots(
	source *repositories.DatabaseRestoreSnapshot,
	restored *repositories.DatabaseRestoreSnapshot,
	sampleLimit int,
) DatabaseRestoreSnapshotComparison {
	comparison := DatabaseRestoreSnapshotComparison{
		EndpointDistinct:           source.EndpointFingerprint != restored.EndpointFingerprint,
		DatabaseDriverMatches:      source.DatabaseDriver == restored.DatabaseDriver,
		SchemaMatches:              source.SchemaSHA256 == restored.SchemaSHA256,
		DataMatches:                source.DataSHA256 == restored.DataSHA256,
		MigrationMatches:           source.MigrationSHA256 == restored.MigrationSHA256,
		MissingFromRestoreSamples:  []string{},
		UnexpectedInRestoreSamples: []string{},
		SchemaMismatchTableSamples: []string{},
		DataMismatchTableSamples:   []string{},
	}
	missing := make([]string, 0)
	unexpected := make([]string, 0)
	schemaMismatch := make([]string, 0)
	dataMismatch := make([]string, 0)
	for name, sourceTable := range source.Tables {
		restoredTable, ok := restored.Tables[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if sourceTable.SchemaSHA256 != restoredTable.SchemaSHA256 {
			schemaMismatch = append(schemaMismatch, name)
		}
		if sourceTable.RowCount != restoredTable.RowCount ||
			sourceTable.DataSHA256 != restoredTable.DataSHA256 {
			dataMismatch = append(dataMismatch, name)
		}
	}
	for name := range restored.Tables {
		if _, ok := source.Tables[name]; !ok {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	sort.Strings(schemaMismatch)
	sort.Strings(dataMismatch)
	comparison.MissingFromRestoreCount = len(missing)
	comparison.MissingFromRestoreSamples = truncateDatabaseRestoreSamples(missing, sampleLimit)
	comparison.UnexpectedInRestoreCount = len(unexpected)
	comparison.UnexpectedInRestoreSamples = truncateDatabaseRestoreSamples(unexpected, sampleLimit)
	comparison.SchemaMismatchTableCount = len(schemaMismatch)
	comparison.SchemaMismatchTableSamples = truncateDatabaseRestoreSamples(schemaMismatch, sampleLimit)
	comparison.DataMismatchTableCount = len(dataMismatch)
	comparison.DataMismatchTableSamples = truncateDatabaseRestoreSamples(dataMismatch, sampleLimit)
	return comparison
}

func databaseRestoreSnapshotSummary(
	snapshot *repositories.DatabaseRestoreSnapshot,
) DatabaseRestoreSnapshotSummary {
	return DatabaseRestoreSnapshotSummary{
		DatabaseDriver:        snapshot.DatabaseDriver,
		ApplicationTableCount: snapshot.ApplicationTableCount,
		TotalRows:             snapshot.TotalRows,
		SchemaSHA256:          snapshot.SchemaSHA256,
		DataSHA256:            snapshot.DataSHA256,
		MigrationSHA256:       snapshot.MigrationSHA256,
		MigrationRows:         snapshot.MigrationRows,
		MigrationArchiveRows:  snapshot.MigrationArchiveRows,
		FailedMigrationRows:   snapshot.FailedMigrationRows,
	}
}

func truncateDatabaseRestoreSamples(items []string, limit int) []string {
	if len(items) <= limit {
		return items
	}
	return append([]string{}, items[:limit]...)
}

func (r *TenantRestoreVerificationReport) addViolation(code, message string) {
	r.Violations = append(r.Violations, TenantRestoreVerificationViolation{
		Code: code, Message: message,
	})
}
