package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-desk/internal/services"
)

const (
	schemaCleanupPlanVersion      = "b14-operation-v1"
	schemaCleanupPlanFilename     = "plan.json"
	schemaCleanupTokenFilename    = "operation.token"
	schemaCleanupConsumedFilename = "consumed.json"
	schemaCleanupResultFilename   = "result.json"
)

var schemaCleanupCryptoRandomReader io.Reader = rand.Reader

type schemaCleanupPlan struct {
	Version              string                                    `json:"version"`
	OperationID          string                                    `json:"operationId"`
	OperationDirectory   string                                    `json:"operationDirectory"`
	Environment          string                                    `json:"environment"`
	CreatedAt            time.Time                                 `json:"createdAt"`
	ExpiresAt            time.Time                                 `json:"expiresAt"`
	RequiredConfirmation string                                    `json:"requiredConfirmation"`
	Pilot                services.LegacySchemaCleanupPilotIdentity `json:"pilot"`
	Evidence             schemaCleanupEvidenceFiles                `json:"evidence"`
	Snapshot             services.DatabaseRestoreSnapshotSummary   `json:"snapshot"`
	InventoryDigest      string                                    `json:"inventoryDigest"`
	InventoryCode        string                                    `json:"inventoryCode"`
	TokenSHA256          string                                    `json:"tokenSha256"`
	AuthorizationHMAC    string                                    `json:"authorizationHmac"`
}

type schemaCleanupPlanOptions struct {
	Environment        string
	OperationDirectory string
	CreatedAt          time.Time
	ExpiresAt          time.Time
	Pilot              services.LegacySchemaCleanupPilotIdentity
	Evidence           schemaCleanupEvidenceFiles
	Snapshot           services.DatabaseRestoreSnapshotSummary
	InventoryDigest    string
	InventoryCode      string
}

type schemaCleanupPreparedOperation struct {
	Plan  *schemaCleanupPlan
	Token []byte
}

type schemaCleanupConsumedMarker struct {
	Version     string    `json:"version"`
	OperationID string    `json:"operationId"`
	ConsumedAt  time.Time `json:"consumedAt"`
}

func newSchemaCleanupPreparedOperation(
	random io.Reader,
	options schemaCleanupPlanOptions,
) (*schemaCleanupPreparedOperation, error) {
	if random == nil {
		return nil, fmt.Errorf("schema cleanup random source is required")
	}
	operationBytes := make([]byte, 16)
	if _, err := io.ReadFull(random, operationBytes); err != nil {
		return nil, fmt.Errorf("generate schema cleanup operation id failed")
	}
	token := make([]byte, 32)
	if _, err := io.ReadFull(random, token); err != nil {
		return nil, fmt.Errorf("generate schema cleanup operation token failed")
	}
	operationID := hex.EncodeToString(operationBytes)
	tokenHash := sha256.Sum256(token)
	plan := &schemaCleanupPlan{
		Version:            schemaCleanupPlanVersion,
		OperationID:        operationID,
		OperationDirectory: options.OperationDirectory,
		Environment:        options.Environment,
		CreatedAt:          options.CreatedAt.UTC(),
		ExpiresAt:          options.ExpiresAt.UTC(),
		RequiredConfirmation: strings.Join([]string{
			"DELETE_B14_LEGACY_SCHEMA",
			options.Environment,
			operationID,
		}, ":"),
		Pilot:           options.Pilot,
		Evidence:        options.Evidence,
		Snapshot:        options.Snapshot,
		InventoryDigest: options.InventoryDigest,
		InventoryCode:   options.InventoryCode,
		TokenSHA256:     hex.EncodeToString(tokenHash[:]),
	}
	authorization, err := schemaCleanupPlanAuthorization(plan, token)
	if err != nil {
		return nil, err
	}
	plan.AuthorizationHMAC = authorization
	return &schemaCleanupPreparedOperation{Plan: plan, Token: token}, nil
}

func writeSchemaCleanupOperation(prepared *schemaCleanupPreparedOperation) error {
	if prepared == nil || prepared.Plan == nil || len(prepared.Token) != 32 {
		return fmt.Errorf("prepared schema cleanup operation is incomplete")
	}
	directory := prepared.Plan.OperationDirectory
	if err := os.Mkdir(directory, 0o700); err != nil {
		return fmt.Errorf("create schema cleanup operation directory failed")
	}
	cleanup := true
	defer func() {
		if !cleanup {
			return
		}
		_ = os.Remove(filepath.Join(directory, schemaCleanupTokenFilename))
		_ = os.Remove(filepath.Join(directory, schemaCleanupPlanFilename))
		_ = os.Remove(directory)
	}()
	planRaw, err := json.MarshalIndent(prepared.Plan, "", "  ")
	if err != nil {
		return fmt.Errorf("encode schema cleanup operation plan failed")
	}
	planRaw = append(planRaw, '\n')
	if err := writeSchemaCleanupExclusiveFile(
		filepath.Join(directory, schemaCleanupPlanFilename),
		planRaw,
	); err != nil {
		return err
	}
	tokenRaw := []byte(base64.RawURLEncoding.EncodeToString(prepared.Token) + "\n")
	if err := writeSchemaCleanupExclusiveFile(
		filepath.Join(directory, schemaCleanupTokenFilename),
		tokenRaw,
	); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func readSchemaCleanupPreparedOperation(
	operationDirectory string,
) (*schemaCleanupPlan, []byte, error) {
	resolvedDirectory, err := inspectSchemaCleanupOperationDirectory(operationDirectory)
	if err != nil {
		return nil, nil, err
	}
	consumedPath := filepath.Join(resolvedDirectory, schemaCleanupConsumedFilename)
	if _, err := os.Lstat(consumedPath); err == nil {
		return nil, nil, fmt.Errorf("schema cleanup operation token has already been consumed")
	} else if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("inspect schema cleanup consumption marker failed")
	}
	planRaw, err := readSchemaCleanupOperationFile(
		filepath.Join(resolvedDirectory, schemaCleanupPlanFilename),
		4<<20,
	)
	if err != nil {
		return nil, nil, err
	}
	var plan schemaCleanupPlan
	if err := json.Unmarshal(planRaw, &plan); err != nil {
		return nil, nil, fmt.Errorf("decode schema cleanup operation plan failed")
	}
	if plan.Version != schemaCleanupPlanVersion ||
		plan.OperationDirectory != resolvedDirectory ||
		!isSchemaCleanupSHA256(plan.InventoryDigest) ||
		len(plan.InventoryCode) != 16 ||
		!strings.HasPrefix(plan.InventoryDigest, plan.InventoryCode) ||
		!isSchemaCleanupSHA256(plan.TokenSHA256) ||
		!isSchemaCleanupSHA256(plan.AuthorizationHMAC) {
		return nil, nil, fmt.Errorf("schema cleanup operation plan is invalid")
	}
	tokenRaw, err := readSchemaCleanupOperationFile(
		filepath.Join(resolvedDirectory, schemaCleanupTokenFilename),
		1024,
	)
	if err != nil {
		return nil, nil, err
	}
	token, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(tokenRaw)))
	if err != nil || len(token) != 32 {
		return nil, nil, fmt.Errorf("schema cleanup operation token is invalid")
	}
	tokenHash := sha256.Sum256(token)
	if !hmac.Equal([]byte(plan.TokenSHA256), []byte(hex.EncodeToString(tokenHash[:]))) {
		return nil, nil, fmt.Errorf("schema cleanup operation token does not match the plan")
	}
	authorization, err := schemaCleanupPlanAuthorization(&plan, token)
	if err != nil {
		return nil, nil, err
	}
	if !hmac.Equal([]byte(plan.AuthorizationHMAC), []byte(authorization)) {
		return nil, nil, fmt.Errorf("schema cleanup operation plan authorization is invalid")
	}
	return &plan, token, nil
}

func consumeSchemaCleanupOperationToken(
	plan *schemaCleanupPlan,
	token []byte,
	consumedAt time.Time,
) error {
	if plan == nil || len(token) != 32 {
		return fmt.Errorf("schema cleanup operation token is incomplete")
	}
	authorization, err := schemaCleanupPlanAuthorization(plan, token)
	if err != nil || !hmac.Equal([]byte(plan.AuthorizationHMAC), []byte(authorization)) {
		return fmt.Errorf("schema cleanup operation token authorization failed")
	}
	markerRaw, err := json.Marshal(schemaCleanupConsumedMarker{
		Version: schemaCleanupPlanVersion, OperationID: plan.OperationID, ConsumedAt: consumedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("encode schema cleanup consumption marker failed")
	}
	markerRaw = append(markerRaw, '\n')
	if err := writeSchemaCleanupExclusiveFile(
		filepath.Join(plan.OperationDirectory, schemaCleanupConsumedFilename),
		markerRaw,
	); err != nil {
		return fmt.Errorf("consume schema cleanup operation token failed: %w", err)
	}
	tokenPath := filepath.Join(plan.OperationDirectory, schemaCleanupTokenFilename)
	file, err := os.OpenFile(tokenPath, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("erase consumed schema cleanup operation token failed")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync consumed schema cleanup operation token failed")
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close consumed schema cleanup operation token failed")
	}
	return nil
}

func writeSchemaCleanupExecutionResult(
	operationDirectory string,
	output schemaCleanupExecuteOutput,
) error {
	raw, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("encode schema cleanup execution result failed")
	}
	raw = append(raw, '\n')
	return writeSchemaCleanupExclusiveFile(
		filepath.Join(operationDirectory, schemaCleanupResultFilename),
		raw,
	)
}

func schemaCleanupPlanAuthorization(plan *schemaCleanupPlan, token []byte) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("schema cleanup operation plan is required")
	}
	canonical := *plan
	canonical.AuthorizationHMAC = ""
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode schema cleanup authorization payload failed")
	}
	mac := hmac.New(sha256.New, token)
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func prepareSchemaCleanupOperationDirectoryPath(
	operationDirectory string,
	repositoryRoot string,
) (string, error) {
	operationDirectory = strings.TrimSpace(operationDirectory)
	if operationDirectory == "" || !filepath.IsAbs(operationDirectory) {
		return "", fmt.Errorf("operation-dir must be an absolute path")
	}
	operationDirectory = filepath.Clean(operationDirectory)
	if _, err := os.Lstat(operationDirectory); err == nil {
		return "", fmt.Errorf("operation-dir must not already exist")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect operation-dir failed")
	}
	parent := filepath.Dir(operationDirectory)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return "", fmt.Errorf("operation-dir parent must already exist")
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return "", fmt.Errorf("operation-dir parent must be a regular directory")
	}
	if parentInfo.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("operation-dir parent permissions must not allow group or other access")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("resolve operation-dir parent failed")
	}
	resolvedDirectory := filepath.Join(resolvedParent, filepath.Base(operationDirectory))
	if !schemaCleanupPathOutsideRepository(resolvedDirectory, repositoryRoot) {
		return "", fmt.Errorf("operation-dir must be outside the repository")
	}
	return resolvedDirectory, nil
}

func inspectSchemaCleanupOperationDirectory(operationDirectory string) (string, error) {
	operationDirectory = strings.TrimSpace(operationDirectory)
	if operationDirectory == "" || !filepath.IsAbs(operationDirectory) {
		return "", fmt.Errorf("operation-dir must be an absolute path")
	}
	info, err := os.Lstat(operationDirectory)
	if err != nil {
		return "", fmt.Errorf("inspect schema cleanup operation directory failed")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("schema cleanup operation directory must be a restricted non-symlink directory")
	}
	resolved, err := filepath.EvalSymlinks(operationDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve schema cleanup operation directory failed")
	}
	return resolved, nil
}

func readSchemaCleanupOperationFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect schema cleanup operation file failed")
	}
	if info.Mode()&os.ModeSymlink != 0 ||
		!info.Mode().IsRegular() ||
		info.Mode().Perm()&0o077 != 0 ||
		info.Size() <= 0 ||
		info.Size() > maxBytes {
		return nil, fmt.Errorf("schema cleanup operation file is invalid")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read schema cleanup operation file failed")
	}
	return raw, nil
}

func writeSchemaCleanupExclusiveFile(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create restricted schema cleanup file failed")
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return fmt.Errorf("write restricted schema cleanup file failed")
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync restricted schema cleanup file failed")
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close restricted schema cleanup file failed")
	}
	complete = true
	return nil
}

func resolveSchemaCleanupRepositoryRoot(repositoryRoot string) (string, error) {
	repositoryRoot = strings.TrimSpace(repositoryRoot)
	if repositoryRoot == "" {
		current, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current directory failed")
		}
		for {
			if _, goModErr := os.Stat(filepath.Join(current, "go.mod")); goModErr == nil {
				if _, gitErr := os.Stat(filepath.Join(current, ".git")); gitErr == nil {
					repositoryRoot = current
					break
				}
			}
			parent := filepath.Dir(current)
			if parent == current {
				return "", fmt.Errorf("repository-root was not provided and could not be detected")
			}
			current = parent
		}
	}
	if !filepath.IsAbs(repositoryRoot) {
		return "", fmt.Errorf("repository-root must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository-root failed")
	}
	return resolved, nil
}

func schemaCleanupPathOutsideRepository(path string, repositoryRoot string) bool {
	relative, err := filepath.Rel(repositoryRoot, path)
	if err != nil {
		return false
	}
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func schemaCleanupFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open schema cleanup evidence failed")
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash schema cleanup evidence failed")
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
