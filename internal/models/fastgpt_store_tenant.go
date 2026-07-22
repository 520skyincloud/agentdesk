package models

import "time"

// FastGPTStoreTenant stores the non-sensitive binding from one Agent Desk
// store to one FastGPT Tenant Team. Model credentials stay in FastGPT.
type FastGPTStoreTenant struct {
	ID                            int64      `gorm:"primaryKey;autoIncrement"`
	CompanyID                     int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreID                       int64      `gorm:"type:bigint;not null;default:0;uniqueIndex"`
	TenantTeamID                  string     `gorm:"type:varchar(128);not null;default:'';uniqueIndex"`
	TenantTeamName                string     `gorm:"type:varchar(200);not null;default:''"`
	Status                        string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	LastSyncedAt                  *time.Time `gorm:"type:datetime;index"`
	LastError                     string     `gorm:"type:text"`
	ProfileTemplateRevision       int64      `gorm:"type:bigint;not null;default:0;index"`
	ProfileTemplateTargetRevision int64      `gorm:"type:bigint;not null;default:0;index"`
	ProfileTemplateSyncStatus     string     `gorm:"type:varchar(30);not null;default:'unconfigured';index"`
	ProfileTemplateAttemptCount   int        `gorm:"type:int;not null;default:0"`
	ProfileTemplateNextRetryAt    *time.Time `gorm:"type:datetime;index"`
	ProfileTemplateSyncedAt       *time.Time `gorm:"type:datetime;index"`
	ProfileTemplateLastError      string     `gorm:"type:varchar(80);not null;default:''"`
	AuditFields
}

// FastGPTProfileTemplate is the platform-wide non-sensitive model routing
// template. Per-store API keys are stored separately as encrypted credentials.
type FastGPTProfileTemplate struct {
	ID                     int64  `gorm:"primaryKey;autoIncrement"`
	Name                   string `gorm:"type:varchar(120);not null;default:''"`
	Revision               int64  `gorm:"type:bigint;not null;default:0;index"`
	ChatProvider           string `gorm:"type:varchar(80);not null;default:''"`
	ChatBaseURL            string `gorm:"type:varchar(500);not null;default:''"`
	ChatModel              string `gorm:"type:varchar(160);not null;default:''"`
	ChatAPIMode            string `gorm:"type:varchar(40);not null;default:'chat_completions'"`
	ASRProvider            string `gorm:"type:varchar(80);not null;default:''"`
	ASRBaseURL             string `gorm:"type:varchar(500);not null;default:''"`
	ASRModel               string `gorm:"type:varchar(160);not null;default:''"`
	EmbeddingProvider      string `gorm:"type:varchar(80);not null;default:''"`
	EmbeddingBaseURL       string `gorm:"type:varchar(500);not null;default:''"`
	EmbeddingModel         string `gorm:"type:varchar(160);not null;default:''"`
	DocumentParserProvider string `gorm:"type:varchar(80);not null;default:''"`
	DocumentParserBaseURL  string `gorm:"type:varchar(500);not null;default:''"`
	DocumentParserModel    string `gorm:"type:varchar(160);not null;default:''"`
	VisionProvider         string `gorm:"type:varchar(80);not null;default:''"`
	VisionBaseURL          string `gorm:"type:varchar(500);not null;default:''"`
	VisionModel            string `gorm:"type:varchar(160);not null;default:''"`
	RerankProvider         string `gorm:"type:varchar(80);not null;default:''"`
	RerankBaseURL          string `gorm:"type:varchar(500);not null;default:''"`
	RerankModel            string `gorm:"type:varchar(160);not null;default:''"`
	Status                 string `gorm:"type:varchar(30);not null;default:'active';index"`
	AuditFields
}

// FastGPTUsageSyncState keeps a cursor for importing immutable FastGPT usage
// evidence once the server-side integration API becomes available.
type FastGPTUsageSyncState struct {
	ID              int64      `gorm:"primaryKey;autoIncrement"`
	CompanyID       int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreID         int64      `gorm:"type:bigint;not null;default:0;index"`
	KnowledgeBaseID int64      `gorm:"type:bigint;not null;default:0;uniqueIndex"`
	TenantTeamID    string     `gorm:"type:varchar(128);not null;default:'';index"`
	Cursor          string     `gorm:"type:varchar(255);not null;default:''"`
	LastSyncedAt    *time.Time `gorm:"type:datetime;index"`
	LastError       string     `gorm:"type:text"`
	CreatedAt       time.Time  `gorm:"type:datetime;not null"`
	UpdatedAt       time.Time  `gorm:"type:datetime;not null"`
}
