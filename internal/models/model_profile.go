package models

import "agent-desk/internal/pkg/enums"

// ModelProfileTemplate is the platform-wide non-sensitive model template.
// Store API keys remain exclusively in StoreModelCredential.
type ModelProfileTemplate struct {
	ID                           int64  `gorm:"primaryKey;autoIncrement"`
	Name                         string `gorm:"type:varchar(120);not null;default:''"`
	Revision                     int64  `gorm:"type:bigint;not null;default:0;index"`
	GatewayBaseURL               string `gorm:"type:varchar(500);not null;default:''"`
	CustomerTagEvolutionEnabled  bool   `gorm:"not null;default:false;index"`
	CustomerTagEvolutionStoreIDs string `gorm:"type:text"`
	ReplyTagContextEnabled       bool   `gorm:"not null;default:false;index"`
	Status                       string `gorm:"type:varchar(30);not null;default:'active';index"`
	AuditFields
}

// ModelProfileSlot allows new model usages to be added without adding columns
// to the template table. It never stores an API key.
type ModelProfileSlot struct {
	ID               int64             `gorm:"primaryKey;autoIncrement"`
	TemplateID       int64             `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_model_profile_slot"`
	UsageCode        string            `gorm:"type:varchar(80);not null;default:'';index;uniqueIndex:uk_model_profile_slot"`
	DisplayName      string            `gorm:"type:varchar(120);not null;default:''"`
	ModelType        enums.AIModelType `gorm:"type:varchar(30);not null;default:'';index"`
	Provider         string            `gorm:"type:varchar(80);not null;default:''"`
	ModelName        string            `gorm:"type:varchar(160);not null;default:''"`
	APIMode          string            `gorm:"type:varchar(40);not null;default:'chat_completions'"`
	Dimension        int               `gorm:"type:int;not null;default:0"`
	MaxContextTokens int               `gorm:"type:int;not null;default:0"`
	MaxOutputTokens  int               `gorm:"type:int;not null;default:0"`
	TimeoutMS        int               `gorm:"type:int;not null;default:30000"`
	MaxRetryCount    int               `gorm:"type:int;not null;default:0"`
	Temperature      float64           `gorm:"type:decimal(5,3);not null;default:0"`
	SchemaVersion    string            `gorm:"type:varchar(80);not null;default:''"`
	PromptTemplate   string            `gorm:"type:text"`
	JSONSchema       string            `gorm:"type:text"`
	Enabled          bool              `gorm:"not null;default:true;index"`
	SortNo           int               `gorm:"type:int;not null;default:0;index"`
	AuditFields
}
