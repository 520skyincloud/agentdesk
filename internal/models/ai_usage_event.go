package models

import "time"

// AIUsageEvent is an immutable evidence record for one upstream model call or
// one provider operation. Pricing and balance mutations intentionally live
// outside this raw metering table.
type AIUsageEvent struct {
	ID                     int64      `gorm:"primaryKey;autoIncrement"`
	TenantID               int64      `gorm:"type:bigint;not null;default:0;index"`
	EventKey               string     `gorm:"type:varchar(191);not null;uniqueIndex"`
	CompanyID              int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreID                int64      `gorm:"type:bigint;not null;default:0;index"`
	WxWorkInstanceID       int64      `gorm:"type:bigint;not null;default:0;index"`
	ConversationID         int64      `gorm:"type:bigint;not null;default:0;index"`
	MessageID              int64      `gorm:"type:bigint;not null;default:0;index"`
	KnowledgeBaseID        int64      `gorm:"type:bigint;not null;default:0;index"`
	RequestID              string     `gorm:"type:varchar(128);not null;default:'';index"`
	Stage                  string     `gorm:"type:varchar(50);not null;default:'';index"`
	Provider               string     `gorm:"type:varchar(50);not null;default:'';index"`
	Model                  string     `gorm:"type:varchar(120);not null;default:'';index"`
	ModelProfileID         int64      `gorm:"type:bigint;not null;default:0;index"`
	ModelProfileRevision   int64      `gorm:"type:bigint;not null;default:0;index"`
	UsageSlot              string     `gorm:"type:varchar(80);not null;default:'';index"`
	CredentialRevision     int64      `gorm:"type:bigint;not null;default:0;index"`
	KeyFingerprint         string     `gorm:"type:varchar(64);not null;default:'';index"`
	AIConfigID             int64      `gorm:"type:bigint;not null;default:0;index"`
	ModelSource            string     `gorm:"type:varchar(50);not null;default:'';index"`
	UpstreamRequestID      string     `gorm:"type:varchar(191);not null;default:'';index"`
	Gateway                string     `gorm:"type:varchar(40);not null;default:'';index"`
	GatewayRequestID       string     `gorm:"type:varchar(191);not null;default:'';index"`
	GatewayUpstreamID      string     `gorm:"type:varchar(191);not null;default:'';index"`
	CallStartedAt          *time.Time `gorm:"type:datetime;index"`
	CallFinishedAt         *time.Time `gorm:"type:datetime;index"`
	PromptTokens           int64      `gorm:"type:bigint;not null;default:0"`
	CompletionTokens       int64      `gorm:"type:bigint;not null;default:0"`
	CachedPromptTokens     int64      `gorm:"type:bigint;not null;default:0"`
	ReasoningTokens        int64      `gorm:"type:bigint;not null;default:0"`
	OperationType          string     `gorm:"type:varchar(50);not null;default:'';index"`
	RequestCount           int64      `gorm:"type:bigint;not null;default:0"`
	RerankCount            int64      `gorm:"type:bigint;not null;default:0"`
	TrainingCount          int64      `gorm:"type:bigint;not null;default:0"`
	FileBytes              int64      `gorm:"type:bigint;not null;default:0"`
	EstimatedContextTokens int64      `gorm:"type:bigint;not null;default:0"`
	MetricSource           string     `gorm:"type:varchar(40);not null;default:'';index"`
	LatencyMS              int64      `gorm:"type:bigint;not null;default:0"`
	Status                 string     `gorm:"type:varchar(30);not null;default:'';index"`
	ErrorClass             string     `gorm:"type:varchar(80);not null;default:'';index"`
	ErrorMessage           string     `gorm:"type:text"`
	CreatedAt              time.Time  `gorm:"type:datetime;not null;index"`
}
