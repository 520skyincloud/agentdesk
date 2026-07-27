package models

import "time"

// AIUsageGatewayCall stores the transport-level evidence needed to reconcile
// one model request with an external API gateway usage log. It never contains
// prompts, model responses, API keys, or customer message content.
type AIUsageGatewayCall struct {
	ID                       int64      `gorm:"primaryKey;autoIncrement"`
	TenantID                 int64      `gorm:"type:bigint;not null;default:0;index"`
	CallKey                  string     `gorm:"type:varchar(191);not null;uniqueIndex"`
	EventKey                 string     `gorm:"type:varchar(191);not null;default:'';index"`
	StoreID                  int64      `gorm:"type:bigint;not null;default:0;index"`
	WxWorkInstanceID         int64      `gorm:"type:bigint;not null;default:0;index"`
	ConversationID           int64      `gorm:"type:bigint;not null;default:0;index"`
	MessageID                int64      `gorm:"type:bigint;not null;default:0;index"`
	LocalRequestID           string     `gorm:"type:varchar(128);not null;default:'';index"`
	Stage                    string     `gorm:"type:varchar(50);not null;default:'';index"`
	ModelProfileID           int64      `gorm:"type:bigint;not null;default:0;index"`
	ModelProfileRevision     int64      `gorm:"type:bigint;not null;default:0;index"`
	UsageSlot                string     `gorm:"type:varchar(80);not null;default:'';index"`
	CredentialRevision       int64      `gorm:"type:bigint;not null;default:0;index"`
	KeyFingerprint           string     `gorm:"type:varchar(64);not null;default:'';index"`
	Gateway                  string     `gorm:"type:varchar(40);not null;default:'';index"`
	GatewayRequestID         string     `gorm:"type:varchar(191);not null;default:'';index"`
	UpstreamRequestID        string     `gorm:"type:varchar(191);not null;default:'';index"`
	StartedAt                time.Time  `gorm:"type:datetime;not null;index"`
	FinishedAt               time.Time  `gorm:"type:datetime;not null;index"`
	LatencyMS                int64      `gorm:"type:bigint;not null;default:0"`
	HTTPStatus               int        `gorm:"type:int;not null;default:0"`
	ReconcileStatus          string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	MatchStrategy            string     `gorm:"type:varchar(40);not null;default:''"`
	MatchConfidence          string     `gorm:"type:varchar(20);not null;default:''"`
	ExternalModel            string     `gorm:"type:varchar(120);not null;default:'';index"`
	ExternalTokenName        string     `gorm:"type:varchar(120);not null;default:'';index"`
	ExternalChannelID        int64      `gorm:"type:bigint;not null;default:0"`
	ExternalPromptTokens     int64      `gorm:"type:bigint;not null;default:0"`
	ExternalCompletionTokens int64      `gorm:"type:bigint;not null;default:0"`
	ExternalQuota            int64      `gorm:"type:bigint;not null;default:0"`
	ExternalCreatedAt        *time.Time `gorm:"type:datetime;index"`
	ReconciledAt             *time.Time `gorm:"type:datetime;index"`
	LastError                string     `gorm:"type:text"`
	LastErrorClass           string     `gorm:"type:varchar(80);not null;default:'';index"`
	CreatedAt                time.Time  `gorm:"type:datetime;not null;index"`
	UpdatedAt                time.Time  `gorm:"type:datetime;not null"`
}
