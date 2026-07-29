package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/securex"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server             ServerConfig             `yaml:"server"`
	DB                 DBConfig                 `yaml:"db"`
	Logger             LoggerConfig             `yaml:"logger"`
	Auth               AuthConfig               `yaml:"auth"`
	BootstrapAdmin     BootstrapAdminConfig     `yaml:"-"`
	Email              EmailConfig              `yaml:"email"`
	FastGPT            FastGPTConfig            `yaml:"fastGPT"`
	StoreCredential    StoreCredentialConfig    `yaml:"-"`
	Storage            StorageConfig            `yaml:"storage"`
	MCP                MCPConfig                `yaml:"mcp"`
	WxWork             WxWorkConfig             `yaml:"wxWork"`
	OIDC               OIDCConfig               `yaml:"oidc"`
	CustomerSession    CustomerSessionConfig    `yaml:"customerSession"`
	TenantRegistration TenantRegistrationConfig `yaml:"tenantRegistration"`
	BackgroundWorkers  BackgroundWorkerConfig   `yaml:"backgroundWorkers"`
	Arrival            ArrivalConfig            `yaml:"arrival"`
}

type EmailConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	Username  string `yaml:"username"`
	Password  string `yaml:"-"`
	From      string `yaml:"from"`
	FromName  string `yaml:"fromName"`
	TLSMode   string `yaml:"tlsMode"`
	PublicURL string `yaml:"publicUrl"`
}

func (c EmailConfig) Address() string {
	port := c.Port
	if port <= 0 {
		port = 587
	}
	return fmt.Sprintf("%s:%d", c.Host, port)
}

type FastGPTConfig struct {
	Enabled             bool   `yaml:"enabled"`
	BaseURL             string `yaml:"baseUrl"`
	IntegrationToken    string `yaml:"-"`
	TimeoutMS           int    `yaml:"timeoutMs"`
	MaxRetries          int    `yaml:"maxRetries"`
	RetrievalTokenLimit int    `yaml:"retrievalTokenLimit"`
}

// StoreCredentialConfig is loaded exclusively from deployment secrets.
// It must never be populated from the repository configuration file.
type StoreCredentialConfig struct {
	MasterKey   string `yaml:"-"`
	MasterKeyID string `yaml:"-"`
}

type WxWorkNotifyConfig struct {
	Enabled                bool    `yaml:"enabled"`
	ToUsers                []int64 `yaml:"toUsers"`
	Safe                   bool    `yaml:"safe"`
	EnableDuplicateCheck   bool    `yaml:"enableDuplicateCheck"`
	DuplicateCheckInterval int     `yaml:"duplicateCheckInterval"`
}

type ServerConfig struct {
	Port           int        `yaml:"port"`
	CORS           CORSConfig `yaml:"cors"`
	TrustedProxies []string   `yaml:"trustedProxies"`
}

func (s ServerConfig) Address() string {
	if s.Port <= 0 {
		return ":8080"
	}
	return fmt.Sprintf(":%d", s.Port)
}

type CORSConfig struct {
	// AllowedOrigins 是允许浏览器跨域访问的 Origin 白名单，必须包含协议和域名。
	// 留空表示不允许跨域请求；同源请求通常不会携带 Origin，不受影响。
	AllowedOrigins []string `yaml:"allowedOrigins"`
}

type DBConfig struct {
	Type                   string `yaml:"type"`
	DSN                    string `yaml:"dsn"`
	MaxIdleConns           int    `yaml:"maxIdleConns"`
	MaxOpenConns           int    `yaml:"maxOpenConns"`
	ConnMaxIdleTimeSeconds int    `yaml:"connMaxIdleTimeSeconds"`
	ConnMaxLifetimeSeconds int    `yaml:"connMaxLifetimeSeconds"`
}

type LoggerConfig struct {
	Level     string `yaml:"level"`
	Format    string `yaml:"format"`
	AddSource bool   `yaml:"addSource"`
}

type AuthConfig struct {
	TokenTTLHours           int    `yaml:"tokenTTLHours"`
	MaxFailedAttempts       int    `yaml:"maxFailedAttempts"`
	CredentialLockMinute    int    `yaml:"credentialLockMinute"`
	InvitationEncryptionKey string `yaml:"-"`
}

// BootstrapAdminConfig is environment-only and is consumed by the fresh
// database initializer. It must never be loaded from repository YAML.
type BootstrapAdminConfig struct {
	Username string `yaml:"-"`
	Password string `yaml:"-"`
}

type TenantRegistrationConfig struct {
	Enabled bool `yaml:"enabled"`
}

type BackgroundWorkerConfig struct {
	Enabled bool `yaml:"enabled"`
}

type ArrivalConfig struct {
	Enabled                     bool     `yaml:"enabled"`
	PublicBaseURL               string   `yaml:"publicBaseUrl"`
	MiniProgramAppID            string   `yaml:"miniProgramAppId"`
	MiniProgramAppSecret        string   `yaml:"-"`
	SessionSecret               string   `yaml:"-"`
	IdentityHMACKey             string   `yaml:"-"`
	DataMasterKey               string   `yaml:"-"`
	DataMasterKeyID             string   `yaml:"-"`
	WeComSuiteID                string   `yaml:"weComSuiteId"`
	WeComSuiteSecret            string   `yaml:"-"`
	WeComProviderCallbackToken  string   `yaml:"-"`
	WeComProviderEncodingAESKey string   `yaml:"-"`
	WeComAuthType               int      `yaml:"-"`
	WeChatAPIBaseURL            string   `yaml:"weChatApiBaseUrl"`
	WeComAPIBaseURL             string   `yaml:"weComApiBaseUrl"`
	QRCodeAllowedHostSuffixes   []string `yaml:"qrCodeAllowedHostSuffixes"`
	SessionTTLMinutes           int      `yaml:"sessionTtlMinutes"`
	InvitationTTLMinutes        int      `yaml:"invitationTtlMinutes"`
	ContactWayTTLMinutes        int      `yaml:"contactWayTtlMinutes"`
	DeliveryRateLimitSeconds    int      `yaml:"deliveryRateLimitSeconds"`
}

func (c ArrivalConfig) SessionTTL() int {
	if c.SessionTTLMinutes <= 0 {
		return 30
	}
	return c.SessionTTLMinutes
}

func (c ArrivalConfig) InvitationTTL() int {
	if c.InvitationTTLMinutes <= 0 {
		return 1440
	}
	return c.InvitationTTLMinutes
}

func (c ArrivalConfig) ContactWayTTL() int {
	if c.ContactWayTTLMinutes <= 0 {
		return 1440
	}
	return c.ContactWayTTLMinutes
}

func (c ArrivalConfig) DeliveryRateLimit() int {
	if c.DeliveryRateLimitSeconds <= 0 {
		return 60
	}
	return c.DeliveryRateLimitSeconds
}

func (c ArrivalConfig) WeChatBaseURL() string {
	if value := strings.TrimRight(strings.TrimSpace(c.WeChatAPIBaseURL), "/"); value != "" {
		return value
	}
	return "https://api.weixin.qq.com"
}

func (c ArrivalConfig) WeComBaseURL() string {
	if value := strings.TrimRight(strings.TrimSpace(c.WeComAPIBaseURL), "/"); value != "" {
		return value
	}
	return "https://qyapi.weixin.qq.com"
}

type CustomerSessionConfig struct {
	Secret                  string `yaml:"-"`
	TTLMinutes              int    `yaml:"ttlMinutes"`
	RefreshThresholdMinutes int    `yaml:"refreshThresholdMinutes"`
}

func (c CustomerSessionConfig) TTL() int {
	if c.TTLMinutes <= 0 {
		return 120
	}
	return c.TTLMinutes
}

func (c CustomerSessionConfig) RefreshThreshold() int {
	if c.RefreshThresholdMinutes <= 0 {
		return 30
	}
	return c.RefreshThresholdMinutes
}

type StorageConfig struct {
	Default               enums.AssetProvider `yaml:"default"`
	MaxUploadSizeMB       int64               `yaml:"maxUploadSizeMB"`
	AssetURLSigningSecret string              `yaml:"-"`
	AssetURLTTLSeconds    int                 `yaml:"assetURLTTLSeconds"`
	Local                 LocalStorageConfig  `yaml:"local"`
	OSS                   OSSStorageConfig    `yaml:"oss"`
}

func (s StorageConfig) MaxUploadSizeBytes() int64 {
	if s.MaxUploadSizeMB <= 0 {
		return 5 << 20
	}
	return s.MaxUploadSizeMB << 20
}

func (s StorageConfig) MaxRequestBodySizeBytes() int64 {
	limit := s.MaxUploadSizeBytes()
	return limit + (1 << 20)
}

func (s StorageConfig) AssetURLTTL() int {
	if s.AssetURLTTLSeconds <= 0 {
		return 3600
	}
	return s.AssetURLTTLSeconds
}

type LocalStorageConfig struct {
	Root    string `yaml:"root"`
	BaseURL string `yaml:"baseUrl"`
}

type OSSStorageConfig struct {
	Endpoint        string `yaml:"endpoint"`
	Bucket          string `yaml:"bucket"`
	AccessKeyID     string `yaml:"-"`
	AccessKeySecret string `yaml:"-"`
	BaseURL         string `yaml:"baseUrl"`
	Private         bool   `yaml:"private"`
	SignedURLExpire int    `yaml:"signedUrlExpireSeconds"`
}

type MCPConfig struct {
	Enabled bool                       `yaml:"enabled"`
	Servers map[string]MCPServerConfig `yaml:"servers"`
}

type MCPServerConfig struct {
	Enabled   bool              `yaml:"enabled"`
	Endpoint  string            `yaml:"endpoint"`
	TimeoutMS int               `yaml:"timeoutMs"`
	Headers   map[string]string `yaml:"headers"`
}

type OIDCConfig struct {
	Enabled      bool     `yaml:"enabled"`
	Issuer       string   `yaml:"issuer"`
	ClientID     string   `yaml:"clientId"`
	ClientSecret string   `yaml:"-"`
	RedirectURL  string   `yaml:"redirectUrl"`
	StateSecret  string   `yaml:"-"`
	Scopes       []string `yaml:"scopes"`
}

// WxWorkConfig 定义企业微信接入配置。
//
// 当前主要用于后台管理台的企业微信登录流程：
// 1. /api/auth/wxwork/login 生成企业微信授权地址
// 2. 企业微信回调到 OAuthRedirect
// 3. 后端通过 code 换取企业成员身份并完成系统登录
//
// 其中 OAuthRedirect、CorpID、CorpSecret、AgentID 为登录流程核心配置。
type WxWorkConfig struct {
	// Enabled 表示是否启用企业微信登录能力。
	// false 时不会初始化企业微信 SDK，相关登录接口不可用。
	Enabled bool `yaml:"enabled"`
	// CorpID 为企业微信公司 ID，例如 wwxxxxxxxxxxxxxxxx。
	CorpID string `yaml:"corpId"`
	// CorpSecret 为企业微信应用 Secret，用于换取 access_token。
	CorpSecret string `yaml:"-"`
	// AgentID 为企业微信自建应用 AgentID。
	AgentID string `yaml:"agentId"`
	// OAuthRedirect 为企业微信网页授权回调地址。
	// 必须填写完整 URL，且通常指向后端接口 /api/auth/wxwork/callback。
	OAuthRedirect string `yaml:"oauthRedirect"`
	// StateSecret 为登录 state 的签名密钥，用于防止篡改和重放。
	// 建议填写独立随机字符串；留空时业务代码会退回使用 CorpSecret。
	StateSecret string `yaml:"-"`
	// RSAPrivateKey 为企业微信回调解密私钥。
	// 当前登录流程未使用，保留给消息回调等场景。
	RSAPrivateKey string `yaml:"-"`
	// Token 为企业微信回调 Token。
	// 当前登录流程未使用，保留给消息回调等场景。
	Token string `yaml:"-"`
	// EncodingAESKey 为企业微信消息加解密密钥。
	// 当前登录流程未使用，保留给消息回调等场景。
	EncodingAESKey string `yaml:"-"`
	// Notify 为企业微信应用消息通知配置。
	Notify WxWorkNotifyConfig `yaml:"notify"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		BackgroundWorkers: BackgroundWorkerConfig{Enabled: true},
		Arrival:           ArrivalConfig{WeComAuthType: 1},
	}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, err
	}
	applyDeploymentSecretEnv(cfg)
	if dbDSN := strings.TrimSpace(os.Getenv("AGENT_DESK_DB_DSN")); dbDSN != "" {
		cfg.DB.DSN = dbDSN
	}
	if enabledValue := strings.TrimSpace(os.Getenv("AGENT_DESK_TENANT_REGISTRATION_ENABLED")); enabledValue != "" {
		enabled, parseErr := strconv.ParseBool(enabledValue)
		if parseErr != nil {
			return nil, fmt.Errorf("parse AGENT_DESK_TENANT_REGISTRATION_ENABLED: %w", parseErr)
		}
		cfg.TenantRegistration.Enabled = enabled
	}
	if enabledValue := strings.TrimSpace(os.Getenv("AGENT_DESK_BACKGROUND_WORKERS_ENABLED")); enabledValue != "" {
		enabled, parseErr := strconv.ParseBool(enabledValue)
		if parseErr != nil {
			return nil, fmt.Errorf("parse AGENT_DESK_BACKGROUND_WORKERS_ENABLED: %w", parseErr)
		}
		cfg.BackgroundWorkers.Enabled = enabled
	}
	if err := applyFastGPTEnv(cfg); err != nil {
		return nil, err
	}
	if err := applyOptionalFeatureEnv(cfg); err != nil {
		return nil, err
	}
	applyStoreCredentialEnv(cfg)
	if isProductionEnvironment() {
		if err := ValidateProduction(cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func applyDeploymentSecretEnv(cfg *Config) {
	if cfg == nil {
		return
	}
	cfg.Auth.InvitationEncryptionKey = strings.TrimSpace(os.Getenv("AGENT_DESK_INVITATION_ENCRYPTION_KEY"))
	cfg.BootstrapAdmin.Username = strings.TrimSpace(os.Getenv("AGENT_DESK_BOOTSTRAP_ADMIN_USERNAME"))
	cfg.BootstrapAdmin.Password = strings.TrimSpace(os.Getenv("AGENT_DESK_BOOTSTRAP_ADMIN_PASSWORD"))
	cfg.CustomerSession.Secret = strings.TrimSpace(os.Getenv("AGENT_DESK_CUSTOMER_SESSION_SECRET"))
	cfg.Storage.AssetURLSigningSecret = strings.TrimSpace(os.Getenv("AGENT_DESK_ASSET_URL_SIGNING_SECRET"))
	cfg.Storage.OSS.AccessKeyID = strings.TrimSpace(os.Getenv("AGENT_DESK_OSS_ACCESS_KEY_ID"))
	cfg.Storage.OSS.AccessKeySecret = strings.TrimSpace(os.Getenv("AGENT_DESK_OSS_ACCESS_KEY_SECRET"))
	cfg.Email.Password = strings.TrimSpace(os.Getenv("AGENT_DESK_EMAIL_PASSWORD"))
	cfg.OIDC.ClientSecret = strings.TrimSpace(os.Getenv("AGENT_DESK_OIDC_CLIENT_SECRET"))
	cfg.OIDC.StateSecret = strings.TrimSpace(os.Getenv("AGENT_DESK_OIDC_STATE_SECRET"))
	cfg.WxWork.CorpSecret = strings.TrimSpace(os.Getenv("AGENT_DESK_WXWORK_CORP_SECRET"))
	cfg.WxWork.StateSecret = strings.TrimSpace(os.Getenv("AGENT_DESK_WXWORK_STATE_SECRET"))
	cfg.WxWork.RSAPrivateKey = strings.TrimSpace(os.Getenv("AGENT_DESK_WXWORK_RSA_PRIVATE_KEY"))
	cfg.WxWork.Token = strings.TrimSpace(os.Getenv("AGENT_DESK_WXWORK_TOKEN"))
	cfg.WxWork.EncodingAESKey = strings.TrimSpace(os.Getenv("AGENT_DESK_WXWORK_ENCODING_AES_KEY"))
	cfg.Arrival.MiniProgramAppSecret = strings.TrimSpace(os.Getenv("AGENT_DESK_MINIPROGRAM_APP_SECRET"))
	cfg.Arrival.SessionSecret = strings.TrimSpace(os.Getenv("AGENT_DESK_ARRIVAL_SESSION_SECRET"))
	cfg.Arrival.IdentityHMACKey = strings.TrimSpace(os.Getenv("AGENT_DESK_ARRIVAL_IDENTITY_HMAC_KEY"))
	cfg.Arrival.DataMasterKey = strings.TrimSpace(os.Getenv("AGENT_DESK_ARRIVAL_DATA_MASTER_KEY"))
	cfg.Arrival.DataMasterKeyID = strings.TrimSpace(os.Getenv("AGENT_DESK_ARRIVAL_DATA_MASTER_KEY_ID"))
	cfg.Arrival.WeComSuiteSecret = strings.TrimSpace(os.Getenv("AGENT_DESK_WECOM_SUITE_SECRET"))
	cfg.Arrival.WeComProviderCallbackToken = strings.TrimSpace(os.Getenv("AGENT_DESK_WECOM_PROVIDER_CALLBACK_TOKEN"))
	cfg.Arrival.WeComProviderEncodingAESKey = strings.TrimSpace(os.Getenv("AGENT_DESK_WECOM_PROVIDER_ENCODING_AES_KEY"))
}

func applyStoreCredentialEnv(cfg *Config) {
	if cfg == nil {
		return
	}
	cfg.StoreCredential.MasterKey = strings.TrimSpace(os.Getenv("AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY"))
	cfg.StoreCredential.MasterKeyID = strings.TrimSpace(os.Getenv("AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID"))
}

func applyOptionalFeatureEnv(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	for _, item := range []struct {
		name   string
		target *bool
	}{
		{name: "AGENT_DESK_EMAIL_ENABLED", target: &cfg.Email.Enabled},
		{name: "AGENT_DESK_OIDC_ENABLED", target: &cfg.OIDC.Enabled},
		{name: "AGENT_DESK_WXWORK_ENABLED", target: &cfg.WxWork.Enabled},
		{name: "AGENT_DESK_ARRIVAL_ENABLED", target: &cfg.Arrival.Enabled},
	} {
		value := strings.TrimSpace(os.Getenv(item.name))
		if value == "" {
			continue
		}
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("parse %s: %w", item.name, err)
		}
		*item.target = enabled
	}
	applyNonSecretEnvironment(cfg)
	return applyArrivalEnvironment(cfg)
}

func applyNonSecretEnvironment(cfg *Config) {
	for _, item := range []struct {
		name   string
		target *string
	}{
		{name: "AGENT_DESK_EMAIL_HOST", target: &cfg.Email.Host},
		{name: "AGENT_DESK_EMAIL_USERNAME", target: &cfg.Email.Username},
		{name: "AGENT_DESK_EMAIL_FROM", target: &cfg.Email.From},
		{name: "AGENT_DESK_EMAIL_PUBLIC_URL", target: &cfg.Email.PublicURL},
		{name: "AGENT_DESK_OIDC_ISSUER", target: &cfg.OIDC.Issuer},
		{name: "AGENT_DESK_OIDC_CLIENT_ID", target: &cfg.OIDC.ClientID},
		{name: "AGENT_DESK_OIDC_REDIRECT_URL", target: &cfg.OIDC.RedirectURL},
		{name: "AGENT_DESK_WXWORK_CORP_ID", target: &cfg.WxWork.CorpID},
		{name: "AGENT_DESK_WXWORK_AGENT_ID", target: &cfg.WxWork.AgentID},
		{name: "AGENT_DESK_WXWORK_OAUTH_REDIRECT", target: &cfg.WxWork.OAuthRedirect},
		{name: "AGENT_DESK_ARRIVAL_PUBLIC_BASE_URL", target: &cfg.Arrival.PublicBaseURL},
		{name: "AGENT_DESK_MINIPROGRAM_APP_ID", target: &cfg.Arrival.MiniProgramAppID},
		{name: "AGENT_DESK_WECOM_SUITE_ID", target: &cfg.Arrival.WeComSuiteID},
		{name: "AGENT_DESK_ARRIVAL_WECHAT_API_BASE_URL", target: &cfg.Arrival.WeChatAPIBaseURL},
		{name: "AGENT_DESK_ARRIVAL_WECOM_API_BASE_URL", target: &cfg.Arrival.WeComAPIBaseURL},
	} {
		if value := strings.TrimSpace(os.Getenv(item.name)); value != "" {
			*item.target = value
		}
	}
}

func applyArrivalEnvironment(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	if value := strings.TrimSpace(os.Getenv("AGENT_DESK_WECOM_AUTH_TYPE")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || (parsed != 0 && parsed != 1) {
			return fmt.Errorf("parse AGENT_DESK_WECOM_AUTH_TYPE: value must be 0 or 1")
		}
		cfg.Arrival.WeComAuthType = parsed
	}
	for _, item := range []struct {
		name   string
		target *int
	}{
		{name: "AGENT_DESK_ARRIVAL_SESSION_TTL_MINUTES", target: &cfg.Arrival.SessionTTLMinutes},
		{name: "AGENT_DESK_ARRIVAL_INVITATION_TTL_MINUTES", target: &cfg.Arrival.InvitationTTLMinutes},
		{name: "AGENT_DESK_ARRIVAL_CONTACT_WAY_TTL_MINUTES", target: &cfg.Arrival.ContactWayTTLMinutes},
		{name: "AGENT_DESK_ARRIVAL_DELIVERY_RATE_LIMIT_SECONDS", target: &cfg.Arrival.DeliveryRateLimitSeconds},
	} {
		value := strings.TrimSpace(os.Getenv(item.name))
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("parse %s: value must be a positive integer", item.name)
		}
		*item.target = parsed
	}
	if value := strings.TrimSpace(os.Getenv("AGENT_DESK_ARRIVAL_QR_ALLOWED_HOST_SUFFIXES")); value != "" {
		cfg.Arrival.QRCodeAllowedHostSuffixes = splitNonEmptyCSV(value)
	}
	return nil
}

func splitNonEmptyCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func isProductionEnvironment() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_DESK_ENV")), "production")
}

// ValidateProduction rejects incomplete deployment secrets before any database
// migration or background worker can start. Error messages only name variables;
// they never include configured secret values.
func ValidateProduction(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("production configuration is nil")
	}
	invalid := make([]string, 0)
	require := func(ok bool, name string) {
		if !ok {
			invalid = append(invalid, name)
		}
	}
	require(strings.TrimSpace(cfg.DB.DSN) != "", "AGENT_DESK_DB_DSN")
	require(validBase64Key(cfg.Auth.InvitationEncryptionKey), "AGENT_DESK_INVITATION_ENCRYPTION_KEY")
	require(strongSecret(cfg.CustomerSession.Secret), "AGENT_DESK_CUSTOMER_SESSION_SECRET")
	require(strongSecret(cfg.Storage.AssetURLSigningSecret), "AGENT_DESK_ASSET_URL_SIGNING_SECRET")
	_, credentialKeyErr := securex.NewAESGCM(cfg.StoreCredential.MasterKey)
	require(credentialKeyErr == nil, "AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY")
	require(strings.TrimSpace(cfg.StoreCredential.MasterKeyID) != "", "AGENT_DESK_STORE_MODEL_CREDENTIAL_MASTER_KEY_ID")

	if cfg.FastGPT.Enabled {
		require(validProductionHTTPSURL(cfg.FastGPT.BaseURL), "AGENT_DESK_FASTGPT_BASE_URL")
		require(strings.TrimSpace(cfg.FastGPT.IntegrationToken) != "", "AGENT_DESK_FASTGPT_INTEGRATION_TOKEN")
	}
	if cfg.Email.Enabled {
		require(strings.TrimSpace(cfg.Email.Host) != "", "AGENT_DESK_EMAIL_HOST")
		require(strings.TrimSpace(cfg.Email.Username) != "", "AGENT_DESK_EMAIL_USERNAME")
		require(strings.TrimSpace(cfg.Email.Password) != "", "AGENT_DESK_EMAIL_PASSWORD")
		require(strings.TrimSpace(cfg.Email.From) != "", "AGENT_DESK_EMAIL_FROM")
		require(strings.TrimSpace(cfg.Email.PublicURL) != "", "AGENT_DESK_EMAIL_PUBLIC_URL")
	}
	if cfg.OIDC.Enabled {
		require(strings.TrimSpace(cfg.OIDC.Issuer) != "", "AGENT_DESK_OIDC_ISSUER")
		require(strings.TrimSpace(cfg.OIDC.ClientID) != "", "AGENT_DESK_OIDC_CLIENT_ID")
		require(strings.TrimSpace(cfg.OIDC.ClientSecret) != "", "AGENT_DESK_OIDC_CLIENT_SECRET")
		require(strings.TrimSpace(cfg.OIDC.RedirectURL) != "", "AGENT_DESK_OIDC_REDIRECT_URL")
		require(strongSecret(cfg.OIDC.StateSecret), "AGENT_DESK_OIDC_STATE_SECRET")
	}
	if cfg.WxWork.Enabled {
		require(strings.TrimSpace(cfg.WxWork.CorpID) != "", "AGENT_DESK_WXWORK_CORP_ID")
		require(strings.TrimSpace(cfg.WxWork.CorpSecret) != "", "AGENT_DESK_WXWORK_CORP_SECRET")
		require(strings.TrimSpace(cfg.WxWork.AgentID) != "", "AGENT_DESK_WXWORK_AGENT_ID")
		require(strings.TrimSpace(cfg.WxWork.OAuthRedirect) != "", "AGENT_DESK_WXWORK_OAUTH_REDIRECT")
		require(strongSecret(cfg.WxWork.StateSecret), "AGENT_DESK_WXWORK_STATE_SECRET")
	}
	if cfg.Storage.Default == enums.AssetProviderOSS {
		require(strings.TrimSpace(cfg.Storage.OSS.AccessKeyID) != "", "AGENT_DESK_OSS_ACCESS_KEY_ID")
		require(strings.TrimSpace(cfg.Storage.OSS.AccessKeySecret) != "", "AGENT_DESK_OSS_ACCESS_KEY_SECRET")
	}
	if cfg.Arrival.Enabled {
		require(validProductionHTTPSURL(cfg.Arrival.PublicBaseURL), "AGENT_DESK_ARRIVAL_PUBLIC_BASE_URL")
		require(strings.TrimSpace(cfg.Arrival.MiniProgramAppID) != "", "AGENT_DESK_MINIPROGRAM_APP_ID")
		require(strings.TrimSpace(cfg.Arrival.MiniProgramAppSecret) != "", "AGENT_DESK_MINIPROGRAM_APP_SECRET")
		require(strongSecret(cfg.Arrival.SessionSecret), "AGENT_DESK_ARRIVAL_SESSION_SECRET")
		require(strongSecret(cfg.Arrival.IdentityHMACKey), "AGENT_DESK_ARRIVAL_IDENTITY_HMAC_KEY")
		_, arrivalKeyErr := securex.NewAESGCM(cfg.Arrival.DataMasterKey)
		require(arrivalKeyErr == nil, "AGENT_DESK_ARRIVAL_DATA_MASTER_KEY")
		require(strings.TrimSpace(cfg.Arrival.DataMasterKeyID) != "", "AGENT_DESK_ARRIVAL_DATA_MASTER_KEY_ID")
		require(strings.TrimSpace(cfg.Arrival.WeComSuiteID) != "", "AGENT_DESK_WECOM_SUITE_ID")
		require(strings.TrimSpace(cfg.Arrival.WeComSuiteSecret) != "", "AGENT_DESK_WECOM_SUITE_SECRET")
		require(strongSecret(cfg.Arrival.WeComProviderCallbackToken), "AGENT_DESK_WECOM_PROVIDER_CALLBACK_TOKEN")
		require(validWeComEncodingAESKey(cfg.Arrival.WeComProviderEncodingAESKey), "AGENT_DESK_WECOM_PROVIDER_ENCODING_AES_KEY")
		require(cfg.Arrival.WeComAuthType == 0 || cfg.Arrival.WeComAuthType == 1, "AGENT_DESK_WECOM_AUTH_TYPE")
		require(validProductionHTTPSURL(cfg.Arrival.WeChatBaseURL()), "AGENT_DESK_ARRIVAL_WECHAT_API_BASE_URL")
		require(validProductionHTTPSURL(cfg.Arrival.WeComBaseURL()), "AGENT_DESK_ARRIVAL_WECOM_API_BASE_URL")
	}
	if len(invalid) == 0 {
		return nil
	}
	slices.Sort(invalid)
	invalid = slices.Compact(invalid)
	return fmt.Errorf("production configuration is incomplete or invalid: %s", strings.Join(invalid, ", "))
}

func validWeComEncodingAESKey(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(value + "=")
	return err == nil && len(decoded) == 32
}

func validBase64Key(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	return err == nil && len(decoded) == 32
}

func strongSecret(value string) bool {
	return len([]byte(strings.TrimSpace(value))) >= 32
}

func validProductionHTTPSURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil &&
		strings.EqualFold(parsed.Scheme, "https") &&
		strings.TrimSpace(parsed.Host) != "" &&
		parsed.User == nil
}

func applyFastGPTEnv(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	if value := strings.TrimSpace(os.Getenv("AGENT_DESK_FASTGPT_ENABLED")); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("parse AGENT_DESK_FASTGPT_ENABLED: %w", err)
		}
		cfg.FastGPT.Enabled = enabled
	}
	if value := strings.TrimSpace(os.Getenv("AGENT_DESK_FASTGPT_BASE_URL")); value != "" {
		cfg.FastGPT.BaseURL = value
	}
	if value := strings.TrimSpace(os.Getenv("AGENT_DESK_FASTGPT_INTEGRATION_TOKEN")); value != "" {
		cfg.FastGPT.IntegrationToken = value
	}
	if value := strings.TrimSpace(os.Getenv("AGENT_DESK_FASTGPT_RETRIEVAL_TOKEN_LIMIT")); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("parse AGENT_DESK_FASTGPT_RETRIEVAL_TOKEN_LIMIT: %w", err)
		}
		cfg.FastGPT.RetrievalTokenLimit = limit
	}
	return nil
}
