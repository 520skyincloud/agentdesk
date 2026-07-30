package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLoadReadsCORSAllowedOrigins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`server:
  port: 8083
  cors:
    allowedOrigins:
      - https://console.example.com
      - http://localhost:3000
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	got := cfg.Server.CORS.AllowedOrigins
	want := []string{"https://console.example.com", "http://localhost:3000"}
	if len(got) != len(want) {
		t.Fatalf("len(AllowedOrigins)=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllowedOrigins[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestLoadReadsTrustedProxiesAndTenantRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`server:
  trustedProxies:
    - 127.0.0.1
    - 10.0.0.0/8
tenantRegistration:
  enabled: true
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.TenantRegistration.Enabled {
		t.Fatalf("TenantRegistration.Enabled=false want true")
	}
	if got := strings.Join(cfg.Server.TrustedProxies, ","); got != "127.0.0.1,10.0.0.0/8" {
		t.Fatalf("TrustedProxies=%q", got)
	}
}

func TestLoadOverridesInvitationEncryptionKeyFromEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`auth:
  invitationEncryptionKey: yaml-key
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AGENT_DESK_INVITATION_ENCRYPTION_KEY", "environment-key")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Auth.InvitationEncryptionKey != "environment-key" {
		t.Fatalf("InvitationEncryptionKey=%q want environment override", cfg.Auth.InvitationEncryptionKey)
	}
}

func TestLoadReadsBootstrapAdminOnlyFromEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`bootstrapAdmin:
  username: ignored-yaml-admin
  password: ignored-yaml-password
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AGENT_DESK_BOOTSTRAP_ADMIN_USERNAME", "environment-admin")
	t.Setenv("AGENT_DESK_BOOTSTRAP_ADMIN_PASSWORD", "environment-password")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.BootstrapAdmin.Username != "environment-admin" {
		t.Fatalf("BootstrapAdmin.Username=%q want environment override", cfg.BootstrapAdmin.Username)
	}
	if cfg.BootstrapAdmin.Password != "environment-password" {
		t.Fatal("BootstrapAdmin.Password did not use environment override")
	}
}

func TestLoadOverridesTenantRegistrationFromEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("tenantRegistration:\n  enabled: false\n"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AGENT_DESK_TENANT_REGISTRATION_ENABLED", "true")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.TenantRegistration.Enabled {
		t.Fatalf("TenantRegistration.Enabled=false want environment override")
	}
}

func TestLoadEnablesBackgroundWorkersByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.BackgroundWorkers.Enabled {
		t.Fatal("BackgroundWorkers.Enabled=false want compatibility default true")
	}
}

func TestLoadOverridesBackgroundWorkersFromEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("backgroundWorkers:\n  enabled: true\n"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AGENT_DESK_BACKGROUND_WORKERS_ENABLED", "false")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.BackgroundWorkers.Enabled {
		t.Fatal("BackgroundWorkers.Enabled=true want environment override false")
	}
}

func TestLoadRejectsInvalidBackgroundWorkersEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AGENT_DESK_BACKGROUND_WORKERS_ENABLED", "sometimes")

	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "AGENT_DESK_BACKGROUND_WORKERS_ENABLED") {
		t.Fatalf("Load() error=%v want invalid environment error", err)
	}
}

func TestLoadOverridesAssetURLSigningSecretFromEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("storage:\n  assetURLSigningSecret: yaml-secret\n"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AGENT_DESK_ASSET_URL_SIGNING_SECRET", "environment-secret")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Storage.AssetURLSigningSecret != "environment-secret" {
		t.Fatalf("AssetURLSigningSecret=%q want environment override", cfg.Storage.AssetURLSigningSecret)
	}
}

func TestLoadRejectsInvalidTenantRegistrationEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("AGENT_DESK_TENANT_REGISTRATION_ENABLED", "sometimes")

	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "AGENT_DESK_TENANT_REGISTRATION_ENABLED") {
		t.Fatalf("Load() error=%v want invalid environment error", err)
	}
}

func TestLoadDefaultsAndOverridesWeComAuthType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DESK_WECOM_AUTH_TYPE", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Arrival.WeComAuthType != 1 {
		t.Fatalf("default WeComAuthType=%d want 1", cfg.Arrival.WeComAuthType)
	}

	t.Setenv("AGENT_DESK_WECOM_AUTH_TYPE", "0")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Arrival.WeComAuthType != 0 {
		t.Fatalf("environment WeComAuthType=%d want 0", cfg.Arrival.WeComAuthType)
	}
}

func TestLoadRejectsInvalidWeComAuthType(t *testing.T) {
	for _, value := range []string{"-1", "2", "all"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("AGENT_DESK_WECOM_AUTH_TYPE", value)
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "AGENT_DESK_WECOM_AUTH_TYPE") {
				t.Fatalf("Load() error=%v want auth type validation", err)
			}
		})
	}
}

func TestLoadDefaultsAndOverridesArrivalContactProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DESK_ARRIVAL_CONTACT_PROVIDER", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Arrival.ContactProviderMode(); got != "contact_way" {
		t.Fatalf("default arrival contact provider=%q want contact_way", got)
	}

	t.Setenv("AGENT_DESK_ARRIVAL_CONTACT_PROVIDER", "customer_acquisition")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Arrival.ContactProviderMode(); got != "customer_acquisition" {
		t.Fatalf("arrival contact provider=%q want customer_acquisition", got)
	}
}

func TestLoadRejectsInvalidArrivalContactProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DESK_ARRIVAL_CONTACT_PROVIDER", "automatic-fallback")
	if _, err := Load(path); err == nil ||
		!strings.Contains(err.Error(), "AGENT_DESK_ARRIVAL_CONTACT_PROVIDER") {
		t.Fatalf("Load() error=%v want contact provider validation", err)
	}
}

func TestValidateProductionSupportsWeComInstallTestAndFormalAuthTypes(t *testing.T) {
	for _, authType := range []int{1, 0} {
		t.Run(strconv.Itoa(authType), func(t *testing.T) {
			cfg := &Config{Arrival: ArrivalConfig{Enabled: true, WeComAuthType: authType}}
			err := ValidateProduction(cfg)
			if err == nil {
				t.Fatal("incomplete production config unexpectedly passed")
			}
			if strings.Contains(err.Error(), "AGENT_DESK_WECOM_AUTH_TYPE") {
				t.Fatalf("production auth type %d rejected: %v", authType, err)
			}
		})
	}

	for _, authType := range []int{-1, 2} {
		t.Run("invalid_"+strconv.Itoa(authType), func(t *testing.T) {
			cfg := &Config{Arrival: ArrivalConfig{Enabled: true, WeComAuthType: authType}}
			err := ValidateProduction(cfg)
			if err == nil || !strings.Contains(err.Error(), "AGENT_DESK_WECOM_AUTH_TYPE") {
				t.Fatalf("ValidateProduction() error=%v want auth type failure", err)
			}
		})
	}
}

func TestLoadAppliesFastGPTSecretEnvironment(t *testing.T) {
	t.Setenv("AGENT_DESK_FASTGPT_ENABLED", "true")
	t.Setenv("AGENT_DESK_FASTGPT_BASE_URL", "https://fastgpt.example.com")
	t.Setenv("AGENT_DESK_FASTGPT_INTEGRATION_TOKEN", "integration-from-environment")
	t.Setenv("AGENT_DESK_FASTGPT_RETRIEVAL_TOKEN_LIMIT", "400")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.FastGPT.Enabled || cfg.FastGPT.BaseURL != "https://fastgpt.example.com" || cfg.FastGPT.IntegrationToken != "integration-from-environment" || cfg.FastGPT.RetrievalTokenLimit != 400 {
		t.Fatalf("fastGPT=%#v", cfg.FastGPT)
	}
}

func TestLoadIgnoresFastGPTIntegrationTokenInYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "fastGPT:\n  enabled: true\n  baseUrl: https://fastgpt.example.com\n  integrationToken: must-not-load\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FastGPT.IntegrationToken != "" {
		t.Fatalf("FastGPT integration token must come only from deployment secrets, got %q", cfg.FastGPT.IntegrationToken)
	}
}

func TestLoadAppliesDeploymentEnvironment(t *testing.T) {
	values := map[string]string{
		"AGENT_DESK_DB_DSN":                  "environment-dsn",
		"AGENT_DESK_CUSTOMER_SESSION_SECRET": "customer-session-environment-secret",
		"AGENT_DESK_EMAIL_PASSWORD":          "email-environment-secret",
		"AGENT_DESK_OIDC_CLIENT_SECRET":      "oidc-client-environment-secret",
		"AGENT_DESK_OIDC_STATE_SECRET":       "oidc-state-environment-secret",
		"AGENT_DESK_OSS_ACCESS_KEY_ID":       "oss-environment-id",
		"AGENT_DESK_OSS_ACCESS_KEY_SECRET":   "oss-environment-secret",
		"AGENT_DESK_WXWORK_CORP_SECRET":      "wxwork-corp-environment-secret",
		"AGENT_DESK_WXWORK_STATE_SECRET":     "wxwork-state-environment-secret",
		"AGENT_DESK_WXWORK_RSA_PRIVATE_KEY":  "wxwork-rsa-environment-secret",
		"AGENT_DESK_WXWORK_TOKEN":            "wxwork-token-environment-secret",
		"AGENT_DESK_WXWORK_ENCODING_AES_KEY": "wxwork-aes-environment-secret",
		"AGENT_DESK_WXWORK_CORP_ID":          "ww-environment",
		"AGENT_DESK_WXWORK_AGENT_ID":         "1000008",
		"AGENT_DESK_WXWORK_OAUTH_REDIRECT":   "https://console.example.com/wxwork",
		"AGENT_DESK_WXWORK_ENABLED":          "true",
		"AGENT_DESK_EMAIL_ENABLED":           "true",
		"AGENT_DESK_OIDC_ENABLED":            "true",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("db:\n  dsn: yaml-dsn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.DSN != "environment-dsn" || cfg.CustomerSession.Secret != values["AGENT_DESK_CUSTOMER_SESSION_SECRET"] {
		t.Fatalf("deployment environment was not applied")
	}
	if !cfg.Email.Enabled || cfg.Email.Password != values["AGENT_DESK_EMAIL_PASSWORD"] {
		t.Fatalf("email environment was not applied")
	}
	if !cfg.OIDC.Enabled || cfg.OIDC.ClientSecret != values["AGENT_DESK_OIDC_CLIENT_SECRET"] || cfg.OIDC.StateSecret != values["AGENT_DESK_OIDC_STATE_SECRET"] {
		t.Fatalf("OIDC environment was not applied")
	}
	if !cfg.WxWork.Enabled || cfg.WxWork.CorpSecret != values["AGENT_DESK_WXWORK_CORP_SECRET"] || cfg.WxWork.EncodingAESKey != values["AGENT_DESK_WXWORK_ENCODING_AES_KEY"] {
		t.Fatalf("WxWork environment was not applied")
	}
	if cfg.Storage.OSS.AccessKeyID != values["AGENT_DESK_OSS_ACCESS_KEY_ID"] || cfg.Storage.OSS.AccessKeySecret != values["AGENT_DESK_OSS_ACCESS_KEY_SECRET"] {
		t.Fatalf("OSS environment was not applied")
	}
}

func TestLoadReadsStoreCredentialSecretsOnlyFromEnvironment(t *testing.T) {
	t.Setenv("AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY", "environment-master-key")
	t.Setenv("AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID", "kms-key-2026-07")
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`storeCredential:
  masterKey: yaml-must-be-ignored
  masterKeyId: yaml-id-must-be-ignored
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StoreCredential.MasterKey != "environment-master-key" || cfg.StoreCredential.MasterKeyID != "kms-key-2026-07" {
		t.Fatalf("store credential config=%#v", cfg.StoreCredential)
	}
}

func TestLoadDoesNotAcceptStoreCredentialSecretsFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("storeCredential:\n  masterKey: yaml-secret\n  masterKeyId: yaml-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StoreCredential.MasterKey != "" || cfg.StoreCredential.MasterKeyID != "" {
		t.Fatalf("YAML secret unexpectedly loaded: %#v", cfg.StoreCredential)
	}
}

func TestLoadDoesNotAcceptDeploymentSecretsFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`auth:
  invitationEncryptionKey: yaml-invitation-secret
customerSession:
  secret: yaml-session-secret
email:
  password: yaml-email-secret
storage:
  assetURLSigningSecret: yaml-asset-secret
  oss:
    accessKeyId: yaml-oss-id
    accessKeySecret: yaml-oss-secret
oidc:
  clientSecret: yaml-oidc-client-secret
  stateSecret: yaml-oidc-state-secret
wxWork:
  corpSecret: yaml-wxwork-corp-secret
  stateSecret: yaml-wxwork-state-secret
  rsaPrivateKey: yaml-wxwork-rsa-secret
  token: yaml-wxwork-token
  encodingAESKey: yaml-wxwork-aes-secret
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"invitation": cfg.Auth.InvitationEncryptionKey,
		"session":    cfg.CustomerSession.Secret,
		"email":      cfg.Email.Password,
		"asset":      cfg.Storage.AssetURLSigningSecret,
		"ossId":      cfg.Storage.OSS.AccessKeyID,
		"ossSecret":  cfg.Storage.OSS.AccessKeySecret,
		"oidcClient": cfg.OIDC.ClientSecret,
		"oidcState":  cfg.OIDC.StateSecret,
		"wxCorp":     cfg.WxWork.CorpSecret,
		"wxState":    cfg.WxWork.StateSecret,
		"wxRSA":      cfg.WxWork.RSAPrivateKey,
		"wxToken":    cfg.WxWork.Token,
		"wxAES":      cfg.WxWork.EncodingAESKey,
	} {
		if value != "" {
			t.Fatalf("%s secret unexpectedly loaded from YAML", name)
		}
	}
}

func TestLoadRejectsIncompleteProductionSecretsBeforeStartup(t *testing.T) {
	t.Setenv("AGENT_DESK_ENV", "production")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("db:\n  type: mysql\n  dsn: contains-private-dsn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("incomplete production configuration must be rejected")
	}
	for _, name := range []string{
		"AGENT_DESK_ASSET_URL_SIGNING_SECRET",
		"AGENT_DESK_CUSTOMER_SESSION_SECRET",
		"AGENT_DESK_INVITATION_ENCRYPTION_KEY",
		"AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY",
		"AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID",
	} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("production validation error %q does not name %s", err, name)
		}
	}
	if strings.Contains(err.Error(), "contains-private-dsn") {
		t.Fatalf("production validation leaked a configured value: %v", err)
	}
}

func TestLoadAcceptsCompleteProductionSecrets(t *testing.T) {
	t.Setenv("AGENT_DESK_ENV", "production")
	t.Setenv("AGENT_DESK_DB_DSN", "file:production.db")
	t.Setenv("AGENT_DESK_INVITATION_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("AGENT_DESK_CUSTOMER_SESSION_SECRET", "customer-session-secret-with-32-bytes")
	t.Setenv("AGENT_DESK_ASSET_URL_SIGNING_SECRET", "asset-signing-secret-with-at-least-32")
	t.Setenv("AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789")))
	t.Setenv("AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID", "deployment-key-v1")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("db:\n  type: sqlite\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("complete production configuration rejected: %v", err)
	}
}

func TestLoadRequiresHTTPSForProductionFastGPT(t *testing.T) {
	t.Setenv("AGENT_DESK_ENV", "production")
	t.Setenv("AGENT_DESK_DB_DSN", "file:production.db")
	t.Setenv("AGENT_DESK_INVITATION_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("AGENT_DESK_CUSTOMER_SESSION_SECRET", "customer-session-secret-with-32-bytes")
	t.Setenv("AGENT_DESK_ASSET_URL_SIGNING_SECRET", "asset-signing-secret-with-at-least-32")
	t.Setenv("AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789")))
	t.Setenv("AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID", "deployment-key-v1")
	t.Setenv("AGENT_DESK_FASTGPT_ENABLED", "true")
	t.Setenv("AGENT_DESK_FASTGPT_INTEGRATION_TOKEN", "integration-token")
	t.Setenv("AGENT_DESK_FASTGPT_BASE_URL", "http://fastgpt.example.com")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("db:\n  type: sqlite\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "AGENT_DESK_FASTGPT_BASE_URL") {
		t.Fatalf("insecure production FastGPT URL error=%v", err)
	}
	if strings.Contains(err.Error(), "http://fastgpt.example.com") {
		t.Fatalf("production validation leaked the configured FastGPT URL: %v", err)
	}

	t.Setenv("AGENT_DESK_FASTGPT_BASE_URL", "https://fastgpt.example.com")
	if _, err := Load(path); err != nil {
		t.Fatalf("HTTPS production FastGPT URL rejected: %v", err)
	}
}
