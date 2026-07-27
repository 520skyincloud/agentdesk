package config

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeploymentTemplatesContainNoRuntimeSecrets(t *testing.T) {
	root := repositoryRoot(t)
	for _, relativePath := range []string{
		"config/config.example.yaml",
		"docker/agent-desk.yaml",
		"docker-compose.yml",
		".env.example",
	} {
		content, err := os.ReadFile(filepath.Join(root, relativePath))
		if err != nil {
			t.Fatalf("read %s: %v", relativePath, err)
		}
		for _, retired := range []string{"newAPIUsage", "AGENT_DESK_NEW_API_USAGE"} {
			if strings.Contains(string(content), retired) {
				t.Fatalf("%s contains retired configuration %s", relativePath, retired)
			}
		}
	}

	for _, relativePath := range []string{"config/config.example.yaml", "docker/agent-desk.yaml"} {
		rootMap := readYAMLMap(t, filepath.Join(root, relativePath))
		for _, path := range [][]string{
			{"auth", "invitationEncryptionKey"},
			{"customerSession", "secret"},
			{"email", "password"},
			{"fastGPT", "integrationToken"},
			{"storage", "assetURLSigningSecret"},
			{"storage", "oss", "accessKeyId"},
			{"storage", "oss", "accessKeySecret"},
			{"oidc", "clientSecret"},
			{"oidc", "stateSecret"},
			{"wxWork", "corpSecret"},
			{"wxWork", "stateSecret"},
			{"wxWork", "rsaPrivateKey"},
			{"wxWork", "token"},
			{"wxWork", "encodingAESKey"},
			{"storeCredential", "masterKey"},
		} {
			if value := yamlPath(rootMap, path...); strings.TrimSpace(value) != "" {
				t.Fatalf("%s contains a value at %s", relativePath, strings.Join(path, "."))
			}
		}
	}
}

func TestComposeRequiresDeploymentSecrets(t *testing.T) {
	root := repositoryRoot(t)
	compose := readYAMLMap(t, filepath.Join(root, "docker-compose.yml"))
	for _, path := range [][]string{
		{"services", "mysql", "environment", "MYSQL_PASSWORD"},
		{"services", "mysql", "environment", "MYSQL_ROOT_PASSWORD"},
		{"services", "agent-desk", "environment", "AGENT_DESK_DB_DSN"},
		{"services", "agent-desk", "environment", "AGENT_DESK_INVITATION_ENCRYPTION_KEY"},
		{"services", "agent-desk", "environment", "AGENT_DESK_CUSTOMER_SESSION_SECRET"},
		{"services", "agent-desk", "environment", "AGENT_DESK_ASSET_URL_SIGNING_SECRET"},
		{"services", "agent-desk", "environment", "AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY"},
		{"services", "agent-desk", "environment", "AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID"},
	} {
		value := yamlPath(compose, path...)
		if !strings.HasPrefix(value, "${") || !strings.Contains(value, ":?") {
			t.Fatalf("compose path %s must require an environment value, got %q", strings.Join(path, "."), value)
		}
	}
}

func TestDeploymentTemplatesDeclareBackgroundWorkerMode(t *testing.T) {
	root := repositoryRoot(t)
	for _, relativePath := range []string{"config/config.example.yaml", "docker/agent-desk.yaml"} {
		rootMap := readYAMLMap(t, filepath.Join(root, relativePath))
		workers, ok := rootMap["backgroundWorkers"].(map[string]any)
		if !ok {
			t.Fatalf("%s does not declare backgroundWorkers", relativePath)
		}
		enabled, ok := workers["enabled"].(bool)
		if !ok || !enabled {
			t.Fatalf("%s must enable background workers by default", relativePath)
		}
	}

	compose := readYAMLMap(t, filepath.Join(root, "docker-compose.yml"))
	if value := yamlPath(compose, "services", "agent-desk", "environment", "AGENT_DESK_BACKGROUND_WORKERS_ENABLED"); value != "${AGENT_DESK_BACKGROUND_WORKERS_ENABLED:-true}" {
		t.Fatalf("compose background worker override=%q", value)
	}

	values := readEnvFile(t, filepath.Join(root, ".env.example"))
	if value := values["AGENT_DESK_BACKGROUND_WORKERS_ENABLED"]; value != "true" {
		t.Fatalf(".env.example background worker default=%q", value)
	}
}

func TestReleaseImageContainsTenantIntegrityAuditBinary(t *testing.T) {
	root := repositoryRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(content)
	for _, required := range []string{
		"./cmd/tenant_integrity_audit",
		"/app/tenant-integrity-audit",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("release image is missing maintenance binary contract %q", required)
		}
	}
	if strings.Contains(dockerfile, "schema-cleanup") || strings.Contains(dockerfile, "cmd/schema_cleanup") {
		t.Fatal("release image must not include the retired in-place legacy schema cleanup binary")
	}
}

func TestExampleEnvironmentLeavesSecretsBlank(t *testing.T) {
	root := repositoryRoot(t)
	values := readEnvFile(t, filepath.Join(root, ".env.example"))
	for _, name := range []string{
		"AGENT_DESK_MYSQL_PASSWORD",
		"AGENT_DESK_MYSQL_ROOT_PASSWORD",
		"AGENT_DESK_DB_DSN",
		"AGENT_DESK_INVITATION_ENCRYPTION_KEY",
		"AGENT_DESK_CUSTOMER_SESSION_SECRET",
		"AGENT_DESK_ASSET_URL_SIGNING_SECRET",
		"AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY",
		"AGENT_DESK_FASTGPT_INTEGRATION_TOKEN",
		"AGENT_DESK_EMAIL_PASSWORD",
		"AGENT_DESK_OIDC_CLIENT_SECRET",
		"AGENT_DESK_OSS_ACCESS_KEY_SECRET",
		"AGENT_DESK_WXWORK_CORP_SECRET",
	} {
		value, ok := values[name]
		if !ok {
			t.Fatalf(".env.example does not declare %s", name)
		}
		if strings.TrimSpace(value) != "" {
			t.Fatalf(".env.example contains a value for %s", name)
		}
	}
}

func TestRepositoryDoesNotContainRuntimeBackups(t *testing.T) {
	root := repositoryRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "README.md" {
			t.Fatalf("runtime backup artifact is present in repository: backups/%s", entry.Name())
		}
	}
	for _, ignoreFile := range []string{".gitignore", ".dockerignore"} {
		content, err := os.ReadFile(filepath.Join(root, ignoreFile))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "backups") {
			t.Fatalf("%s does not exclude runtime backups", ignoreFile)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve deployment contract test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
}

func readYAMLMap(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{}
	if err := yaml.Unmarshal(content, &value); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return value
}

func yamlPath(value map[string]any, path ...string) string {
	current := value
	for index, name := range path {
		item, ok := current[name]
		if !ok || item == nil {
			return ""
		}
		if index == len(path)-1 {
			return strings.TrimSpace(toString(item))
		}
		next, ok := item.(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

func toString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func readEnvFile(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if ok {
			values[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}
