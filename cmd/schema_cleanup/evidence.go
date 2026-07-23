package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-desk/internal/services"

	"gorm.io/gorm"
)

type schemaCleanupReleaseGateEvidence struct {
	Status      string                                 `json:"status"`
	GeneratedAt time.Time                              `json:"generatedAt"`
	Integrity   *services.TenantIntegrityAuditReport   `json:"integrity"`
	Readiness   *services.TenantReleaseReadinessReport `json:"readiness"`
}

type schemaCleanupRestoreDatabaseEvidence struct {
	Integrity *services.TenantIntegrityAuditReport   `json:"integrity"`
	Readiness *services.TenantReleaseReadinessReport `json:"readiness"`
}

type schemaCleanupRestoreGateEvidence struct {
	Status              string                                    `json:"status"`
	GeneratedAt         time.Time                                 `json:"generatedAt"`
	RestoreVerification *services.TenantRestoreVerificationReport `json:"restoreVerification"`
	Source              schemaCleanupRestoreDatabaseEvidence      `json:"source"`
	Restored            schemaCleanupRestoreDatabaseEvidence      `json:"restored"`
}

type schemaCleanupEvidenceBundle struct {
	Release schemaCleanupReleaseGateEvidence
	Restore schemaCleanupRestoreGateEvidence
}

type schemaCleanupEvidenceFiles struct {
	RepositoryRoot     string `json:"repositoryRoot"`
	ReleaseReportPath  string `json:"releaseReportPath"`
	ReleaseReportHash  string `json:"releaseReportHash"`
	RestoreReportPath  string `json:"restoreReportPath"`
	RestoreReportHash  string `json:"restoreReportHash"`
	BackupArtifactPath string `json:"backupArtifactPath"`
	BackupArtifactHash string `json:"backupArtifactHash"`
	MaxAgeSeconds      int64  `json:"maxAgeSeconds"`
}

func (r *schemaCleanupCommandRuntime) prepare(
	db *gorm.DB,
	options schemaCleanupCommandOptions,
) (*schemaCleanupPreparedOperation, schemaCleanupPrepareOutput, error) {
	now := r.now().UTC()
	repositoryRoot, err := resolveSchemaCleanupRepositoryRoot(options.RepositoryRoot)
	if err != nil {
		return nil, schemaCleanupPrepareOutput{}, err
	}
	operationDirectory, err := prepareSchemaCleanupOperationDirectoryPath(
		options.OperationDirectory,
		repositoryRoot,
	)
	if err != nil {
		return nil, schemaCleanupPrepareOutput{}, err
	}
	evidence, evidenceFiles, gates, err := loadAndValidateSchemaCleanupEvidence(
		options,
		repositoryRoot,
		now,
	)
	if err != nil {
		return nil, schemaCleanupPrepareOutput{}, err
	}
	pilot, err := services.LegacySchemaCleanupService.ResolvePilotIdentity(
		db,
		options.PilotTenantName,
		options.PilotStoreName,
	)
	if err != nil {
		return nil, schemaCleanupPrepareOutput{}, err
	}
	if err := validateSchemaCleanupEvidencePilot(evidence, *pilot); err != nil {
		return nil, schemaCleanupPrepareOutput{}, err
	}
	currentSnapshot, err := r.capture(db)
	if err != nil {
		return nil, schemaCleanupPrepareOutput{}, fmt.Errorf("capture current database snapshot failed: %w", err)
	}
	if !schemaCleanupSnapshotsEqual(currentSnapshot, evidence.Restore.RestoreVerification.Source) {
		return nil, schemaCleanupPrepareOutput{}, fmt.Errorf(
			"current database no longer matches the independently restored backup source",
		)
	}
	gates.DatabaseUnchanged = true
	inventory, err := r.inspect(db)
	if err != nil {
		return nil, schemaCleanupPrepareOutput{}, fmt.Errorf("inspect cleanup schema failed: %w", err)
	}
	if !inventory.Ready {
		return nil, schemaCleanupPrepareOutput{}, fmt.Errorf(
			"cleanup inventory contains objects outside the fixed B14 allowlist",
		)
	}
	if evidence.Release.Readiness.EvidenceStart == nil {
		return nil, schemaCleanupPrepareOutput{}, fmt.Errorf("release evidence is missing its tag_gray evidence window")
	}
	if err := r.runLiveGate(db, *pilot, evidence.Release.Readiness.EvidenceStart.UTC()); err != nil {
		return nil, schemaCleanupPrepareOutput{}, fmt.Errorf("live B13 gate failed: %w", err)
	}
	gates.PreCleanupGatePassed = true

	prepared, err := newSchemaCleanupPreparedOperation(
		r.random,
		schemaCleanupPlanOptions{
			Environment:        options.Environment,
			OperationDirectory: operationDirectory,
			CreatedAt:          now,
			ExpiresAt:          now.Add(options.PlanTTL),
			Pilot:              *pilot,
			Evidence:           evidenceFiles,
			Snapshot:           currentSnapshot,
			InventoryDigest:    inventory.InventoryDigest,
			InventoryCode:      inventory.InventoryCode,
		},
	)
	if err != nil {
		return nil, schemaCleanupPrepareOutput{}, err
	}
	output := schemaCleanupPrepareOutput{
		Status:               "prepared",
		GeneratedAt:          now,
		OperationID:          prepared.Plan.OperationID,
		OperationDirectory:   prepared.Plan.OperationDirectory,
		ExpiresAt:            prepared.Plan.ExpiresAt,
		RequiredConfirmation: prepared.Plan.RequiredConfirmation,
		Pilot:                *pilot,
		Inventory:            inventory,
		Gates:                gates,
	}
	return prepared, output, nil
}

func (r *schemaCleanupCommandRuntime) revalidatePreparedOperation(
	db *gorm.DB,
	options schemaCleanupCommandOptions,
) (
	*schemaCleanupPlan,
	[]byte,
	schemaCleanupEvidenceBundle,
	*services.LegacySchemaCleanupInventory,
	schemaCleanupGateSummary,
	error,
) {
	now := r.now().UTC()
	plan, token, err := readSchemaCleanupPreparedOperation(options.OperationDirectory)
	if err != nil {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{}, err
	}
	if plan.Environment != options.Environment {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("operation environment does not match the prepared plan")
	}
	if plan.RequiredConfirmation != options.Confirmation {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("destructive confirmation does not match the prepared operation")
	}
	if now.Before(plan.CreatedAt.Add(-5*time.Minute)) || !now.Before(plan.ExpiresAt) {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("prepared schema cleanup operation has expired")
	}
	evidenceMaxAge := time.Duration(plan.Evidence.MaxAgeSeconds) * time.Second
	evidenceOptions := schemaCleanupCommandOptions{
		ReleaseReportPath:  plan.Evidence.ReleaseReportPath,
		RestoreReportPath:  plan.Evidence.RestoreReportPath,
		BackupArtifactPath: plan.Evidence.BackupArtifactPath,
		BackupSHA256:       plan.Evidence.BackupArtifactHash,
		EvidenceMaxAge:     evidenceMaxAge,
	}
	evidence, files, gates, err := loadAndValidateSchemaCleanupEvidence(
		evidenceOptions,
		plan.Evidence.RepositoryRoot,
		now,
	)
	if err != nil {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{}, err
	}
	if files != plan.Evidence {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("B13 evidence files changed after schema cleanup preparation")
	}
	pilot, err := services.LegacySchemaCleanupService.ResolvePilotIdentity(
		db,
		plan.Pilot.TenantName,
		plan.Pilot.StoreName,
	)
	if err != nil {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{}, err
	}
	if *pilot != plan.Pilot {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("pilot identity changed after schema cleanup preparation")
	}
	if err := validateSchemaCleanupEvidencePilot(evidence, plan.Pilot); err != nil {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{}, err
	}
	currentSnapshot, err := r.capture(db)
	if err != nil {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("capture current database snapshot failed: %w", err)
	}
	if !schemaCleanupSnapshotsEqual(currentSnapshot, plan.Snapshot) ||
		!schemaCleanupSnapshotsEqual(currentSnapshot, evidence.Restore.RestoreVerification.Source) {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("database changed after schema cleanup preparation")
	}
	gates.DatabaseUnchanged = true
	inventory, err := r.inspect(db)
	if err != nil {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("inspect cleanup schema failed: %w", err)
	}
	if !inventory.Ready || inventory.InventoryDigest != plan.InventoryDigest {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("cleanup inventory changed after schema cleanup preparation")
	}
	if evidence.Release.Readiness.EvidenceStart == nil {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("release evidence is missing its tag_gray evidence window")
	}
	if err := r.runLiveGate(db, plan.Pilot, evidence.Release.Readiness.EvidenceStart.UTC()); err != nil {
		return nil, nil, schemaCleanupEvidenceBundle{}, nil, schemaCleanupGateSummary{},
			fmt.Errorf("live B13 gate failed: %w", err)
	}
	gates.PreCleanupGatePassed = true
	return plan, token, evidence, inventory, gates, nil
}

func loadAndValidateSchemaCleanupEvidence(
	options schemaCleanupCommandOptions,
	repositoryRoot string,
	now time.Time,
) (
	schemaCleanupEvidenceBundle,
	schemaCleanupEvidenceFiles,
	schemaCleanupGateSummary,
	error,
) {
	releaseRaw, releasePath, releaseHash, err := readSchemaCleanupEvidenceFile(
		options.ReleaseReportPath,
		repositoryRoot,
		16<<20,
	)
	if err != nil {
		return schemaCleanupEvidenceBundle{}, schemaCleanupEvidenceFiles{}, schemaCleanupGateSummary{}, err
	}
	restoreRaw, restorePath, restoreHash, err := readSchemaCleanupEvidenceFile(
		options.RestoreReportPath,
		repositoryRoot,
		32<<20,
	)
	if err != nil {
		return schemaCleanupEvidenceBundle{}, schemaCleanupEvidenceFiles{}, schemaCleanupGateSummary{}, err
	}
	backupPath, backupHash, err := hashSchemaCleanupExternalFile(
		options.BackupArtifactPath,
		repositoryRoot,
	)
	if err != nil {
		return schemaCleanupEvidenceBundle{}, schemaCleanupEvidenceFiles{}, schemaCleanupGateSummary{}, err
	}
	expectedBackupHash := strings.ToLower(strings.TrimSpace(options.BackupSHA256))
	if !isSchemaCleanupSHA256(expectedBackupHash) || backupHash != expectedBackupHash {
		return schemaCleanupEvidenceBundle{}, schemaCleanupEvidenceFiles{}, schemaCleanupGateSummary{},
			fmt.Errorf("encrypted backup checksum does not match the pre-recorded value")
	}

	var release schemaCleanupReleaseGateEvidence
	if err := json.Unmarshal(releaseRaw, &release); err != nil {
		return schemaCleanupEvidenceBundle{}, schemaCleanupEvidenceFiles{}, schemaCleanupGateSummary{},
			fmt.Errorf("decode B13 release evidence failed")
	}
	var restore schemaCleanupRestoreGateEvidence
	if err := json.Unmarshal(restoreRaw, &restore); err != nil {
		return schemaCleanupEvidenceBundle{}, schemaCleanupEvidenceFiles{}, schemaCleanupGateSummary{},
			fmt.Errorf("decode independent restore evidence failed")
	}
	if err := validateSchemaCleanupReleaseEvidence(release, now, options.EvidenceMaxAge); err != nil {
		return schemaCleanupEvidenceBundle{}, schemaCleanupEvidenceFiles{}, schemaCleanupGateSummary{}, err
	}
	if err := validateSchemaCleanupRestoreEvidence(
		restore,
		expectedBackupHash,
		now,
		options.EvidenceMaxAge,
	); err != nil {
		return schemaCleanupEvidenceBundle{}, schemaCleanupEvidenceFiles{}, schemaCleanupGateSummary{}, err
	}
	files := schemaCleanupEvidenceFiles{
		RepositoryRoot:    repositoryRoot,
		ReleaseReportPath: releasePath, ReleaseReportHash: releaseHash,
		RestoreReportPath: restorePath, RestoreReportHash: restoreHash,
		BackupArtifactPath: backupPath, BackupArtifactHash: backupHash,
		MaxAgeSeconds: int64(options.EvidenceMaxAge / time.Second),
	}
	gates := schemaCleanupGateSummary{
		WorkersStopped:        true,
		ServerStoppedAttested: true,
		ReleasePassed:         true,
		BackupEncrypted:       true,
		BackupChecksumMatched: true,
		RestorePassed:         true,
	}
	return schemaCleanupEvidenceBundle{Release: release, Restore: restore}, files, gates, nil
}

func validateSchemaCleanupReleaseEvidence(
	evidence schemaCleanupReleaseGateEvidence,
	now time.Time,
	maxAge time.Duration,
) error {
	if evidence.Status != "passed" ||
		evidence.Integrity == nil ||
		evidence.Readiness == nil ||
		evidence.Integrity.Status != "passed" ||
		evidence.Integrity.HasViolations() ||
		evidence.Readiness.Status != "passed" ||
		evidence.Readiness.HasViolations() ||
		evidence.Readiness.Level != services.TenantReleaseReadinessTagGray ||
		evidence.Readiness.EvidenceStart == nil {
		return fmt.Errorf("B13 release report has not passed the complete tag_gray gate")
	}
	for _, generatedAt := range []time.Time{
		evidence.GeneratedAt,
		evidence.Integrity.GeneratedAt,
		evidence.Readiness.GeneratedAt,
	} {
		if err := validateSchemaCleanupEvidenceTime(generatedAt, now, maxAge); err != nil {
			return fmt.Errorf("B13 release report is stale or has an invalid timestamp")
		}
	}
	return nil
}

func validateSchemaCleanupRestoreEvidence(
	evidence schemaCleanupRestoreGateEvidence,
	expectedBackupHash string,
	now time.Time,
	maxAge time.Duration,
) error {
	verification := evidence.RestoreVerification
	if evidence.Status != "passed" ||
		verification == nil ||
		verification.Status != "passed" ||
		verification.HasViolations() ||
		evidence.Source.Integrity == nil ||
		evidence.Source.Readiness == nil ||
		evidence.Restored.Integrity == nil ||
		evidence.Restored.Readiness == nil {
		return fmt.Errorf("independent restore report has not passed")
	}
	for _, integrity := range []*services.TenantIntegrityAuditReport{
		evidence.Source.Integrity,
		evidence.Restored.Integrity,
	} {
		if integrity.Status != "passed" || integrity.HasViolations() {
			return fmt.Errorf("independent restore report contains a failed Tenant integrity gate")
		}
	}
	for _, readiness := range []*services.TenantReleaseReadinessReport{
		evidence.Source.Readiness,
		evidence.Restored.Readiness,
	} {
		if readiness.Status != "passed" ||
			readiness.HasViolations() ||
			readiness.Level != services.TenantReleaseReadinessTagGray ||
			readiness.EvidenceStart == nil {
			return fmt.Errorf("independent restore report contains a failed tag_gray gate")
		}
	}
	backup := verification.Backup
	comparison := verification.Comparison
	if strings.ToLower(backup.SHA256) != expectedBackupHash ||
		!backup.ExpectedChecksumMatches ||
		!backup.ExternalToRepository ||
		!backup.RestrictedPermissions ||
		backup.EncryptionFormat == "" ||
		backup.EncryptionFormat == "unknown" ||
		!comparison.EndpointDistinct ||
		!comparison.DatabaseDriverMatches ||
		!comparison.SchemaMatches ||
		!comparison.DataMatches ||
		!comparison.MigrationMatches ||
		comparison.MissingFromRestoreCount != 0 ||
		comparison.UnexpectedInRestoreCount != 0 ||
		comparison.SchemaMismatchTableCount != 0 ||
		comparison.DataMismatchTableCount != 0 {
		return fmt.Errorf("independent restore report does not prove an encrypted, exact and isolated restore")
	}
	if !schemaCleanupSnapshotsEqual(verification.Source, verification.Restored) {
		return fmt.Errorf("independent restore source and restored database summaries differ")
	}
	for _, generatedAt := range []time.Time{
		evidence.GeneratedAt,
		verification.GeneratedAt,
		evidence.Source.Integrity.GeneratedAt,
		evidence.Source.Readiness.GeneratedAt,
		evidence.Restored.Integrity.GeneratedAt,
		evidence.Restored.Readiness.GeneratedAt,
	} {
		if err := validateSchemaCleanupEvidenceTime(generatedAt, now, maxAge); err != nil {
			return fmt.Errorf("independent restore report is stale or has an invalid timestamp")
		}
	}
	return nil
}

func validateSchemaCleanupEvidencePilot(
	evidence schemaCleanupEvidenceBundle,
	pilot services.LegacySchemaCleanupPilotIdentity,
) error {
	reports := []*services.TenantReleaseReadinessReport{
		evidence.Release.Readiness,
		evidence.Restore.Source.Readiness,
		evidence.Restore.Restored.Readiness,
	}
	for _, report := range reports {
		if report == nil ||
			report.Tenant.ID != pilot.TenantID ||
			report.Tenant.Code != pilot.TenantCode ||
			!containsSchemaCleanupStoreID(report.SelectedStoreIDs, pilot.StoreID) {
			return fmt.Errorf("B13 evidence is not bound to the resolved pilot Tenant and Store")
		}
	}
	return nil
}

func validateSchemaCleanupEvidenceTime(generatedAt, now time.Time, maxAge time.Duration) error {
	if generatedAt.IsZero() || maxAge <= 0 {
		return fmt.Errorf("evidence timestamp is missing")
	}
	generatedAt = generatedAt.UTC()
	now = now.UTC()
	if generatedAt.After(now.Add(5*time.Minute)) || now.Sub(generatedAt) > maxAge {
		return fmt.Errorf("evidence timestamp is outside the accepted window")
	}
	return nil
}

func schemaCleanupSnapshotsEqual(
	left services.DatabaseRestoreSnapshotSummary,
	right services.DatabaseRestoreSnapshotSummary,
) bool {
	return left.DatabaseDriver == right.DatabaseDriver &&
		left.ApplicationTableCount == right.ApplicationTableCount &&
		left.TotalRows == right.TotalRows &&
		left.SchemaSHA256 == right.SchemaSHA256 &&
		left.DataSHA256 == right.DataSHA256 &&
		left.MigrationSHA256 == right.MigrationSHA256 &&
		left.MigrationRows == right.MigrationRows &&
		left.MigrationArchiveRows == right.MigrationArchiveRows &&
		left.FailedMigrationRows == right.FailedMigrationRows
}

func readSchemaCleanupEvidenceFile(
	path string,
	repositoryRoot string,
	maxBytes int64,
) ([]byte, string, string, error) {
	resolvedPath, info, err := inspectSchemaCleanupExternalFile(path, repositoryRoot)
	if err != nil {
		return nil, "", "", err
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		return nil, "", "", fmt.Errorf("schema cleanup evidence file size is invalid")
	}
	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, "", "", fmt.Errorf("read schema cleanup evidence file failed")
	}
	sum := sha256.Sum256(raw)
	return raw, resolvedPath, hex.EncodeToString(sum[:]), nil
}

func hashSchemaCleanupExternalFile(
	path string,
	repositoryRoot string,
) (string, string, error) {
	resolvedPath, info, err := inspectSchemaCleanupExternalFile(path, repositoryRoot)
	if err != nil {
		return "", "", err
	}
	if info.Size() <= 0 {
		return "", "", fmt.Errorf("encrypted backup artifact is empty")
	}
	sum, err := schemaCleanupFileSHA256(resolvedPath)
	if err != nil {
		return "", "", err
	}
	return resolvedPath, sum, nil
}

func inspectSchemaCleanupExternalFile(
	path string,
	repositoryRoot string,
) (string, os.FileInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", nil, fmt.Errorf("schema cleanup evidence paths must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, fmt.Errorf("inspect schema cleanup evidence file failed")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("schema cleanup evidence must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", nil, fmt.Errorf("schema cleanup evidence permissions must not allow group or other access")
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve schema cleanup evidence path failed")
	}
	if !schemaCleanupPathOutsideRepository(resolvedPath, repositoryRoot) {
		return "", nil, fmt.Errorf("schema cleanup evidence must be stored outside the repository")
	}
	return resolvedPath, info, nil
}

func isSchemaCleanupSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
