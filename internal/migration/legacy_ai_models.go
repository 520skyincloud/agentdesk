package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

// These structs are migration-only views of tables retired from the runtime.
// They must never be registered in models.Models or imported outside migration.
type legacyAIConfig struct {
	ID                  int64
	Name                string
	Provider            enums.AIProvider
	BaseURL             string
	APIKey              string
	APIMode             string
	ModelType           enums.AIModelType
	ModelName           string
	Dimension           int
	MaxContextTokens    int
	MaxOutputTokens     int
	TimeoutMS           int
	MaxRetryCount       int
	RPMLimit            int
	TPMLimit            int
	IntentDetectEnabled bool
	Status              enums.Status
	SortNo              int
	Remark              string
	models.AuditFields
}

func (legacyAIConfig) TableName() string { return "t_ai_config" }

type legacyAIAgentModelBinding struct {
	ID         int64
	AIConfigID int64
}

func (legacyAIAgentModelBinding) TableName() string { return "t_ai_agent" }

type legacyTenantAIModelGrant struct {
	ID         int64
	TenantID   int64
	AIConfigID int64
	Status     enums.Status
	models.AuditFields
}

func (legacyTenantAIModelGrant) TableName() string { return "t_tenant_ai_model_grant" }

type legacyStoreAIModelSetting struct {
	ID                int64
	TenantID          int64
	CompanyID         int64
	StoreID           int64
	WxWorkInstanceID  int64
	UsageCode         string
	AIConfigID        int64
	Provider          enums.AIProvider
	BaseURL           string
	APIKey            string
	APIMode           string
	ModelType         enums.AIModelType
	ModelName         string
	Dimension         int
	MaxContextTokens  int
	MaxOutputTokens   int
	TimeoutMS         int
	MaxRetryCount     int
	RPMLimit          int
	TPMLimit          int
	Status            enums.Status
	ConfigFingerprint string
	LastTestStatus    string
	LastTestedAt      *time.Time
	LastTestLatencyMS int64
	Remark            string
	models.AuditFields
}

func (legacyStoreAIModelSetting) TableName() string { return "t_store_ai_model_setting" }

type legacyConversationTag struct {
	ID             int64
	TenantID       int64
	ConversationID int64
	TagID          int64
	models.AuditFields
}

func (legacyConversationTag) TableName() string { return "t_conversation_tag" }
