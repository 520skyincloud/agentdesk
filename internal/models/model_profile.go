package models

import (
	"time"

	"agent-desk/internal/pkg/enums"
)

// ModelProfileTemplate is one immutable revision of a platform model profile.
// It contains no API key; StoreModelCredential is the only credential source.
type ModelProfileTemplate struct {
	ID              int64                    `gorm:"primaryKey;autoIncrement"`
	Code            string                   `gorm:"type:varchar(80);not null;default:'';index;uniqueIndex:uk_model_profile_revision,priority:1"`
	Name            string                   `gorm:"type:varchar(120);not null;default:'';index"`
	Description     string                   `gorm:"type:text"`
	Revision        int64                    `gorm:"type:bigint;not null;default:1;index;uniqueIndex:uk_model_profile_revision,priority:2"`
	GatewayBaseURL  string                   `gorm:"type:varchar(500);not null;default:''"`
	Status          enums.ModelProfileStatus `gorm:"type:varchar(30);not null;default:'draft';index"`
	PublishedAt     *time.Time               `gorm:"type:datetime;index"`
	PublishedBy     int64                    `gorm:"type:bigint;not null;default:0;index"`
	PublishedByName string                   `gorm:"type:varchar(100);not null;default:''"`
	AuditFields
}

// ModelProfileSlot describes one required runtime usage in a profile revision.
type ModelProfileSlot struct {
	ID               int64                `gorm:"primaryKey;autoIncrement"`
	TemplateID       int64                `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_model_profile_slot,priority:1"`
	UsageCode        enums.ModelUsageSlot `gorm:"type:varchar(80);not null;default:'';index;uniqueIndex:uk_model_profile_slot,priority:2"`
	DisplayName      string               `gorm:"type:varchar(120);not null;default:''"`
	ModelType        enums.AIModelType    `gorm:"type:varchar(30);not null;default:'';index"`
	Provider         string               `gorm:"type:varchar(80);not null;default:'newapi'"`
	ModelName        string               `gorm:"type:varchar(160);not null;default:'';index"`
	APIMode          string               `gorm:"type:varchar(40);not null;default:'chat_completions'"`
	Dimension        int                  `gorm:"type:int;not null;default:0"`
	MaxContextTokens int                  `gorm:"type:int;not null;default:0"`
	MaxOutputTokens  int                  `gorm:"type:int;not null;default:0"`
	TimeoutMS        int                  `gorm:"type:int;not null;default:30000"`
	MaxRetryCount    int                  `gorm:"type:int;not null;default:0"`
	Temperature      float64              `gorm:"type:decimal(5,3);not null;default:0"`
	SchemaVersion    string               `gorm:"type:varchar(80);not null;default:''"`
	PromptTemplate   string               `gorm:"type:text"`
	JSONSchema       string               `gorm:"type:text"`
	Enabled          bool                 `gorm:"not null;default:true;index"`
	SortNo           int                  `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// StoreModelProfileAssignment is the sole Store-to-profile binding.
type StoreModelProfileAssignment struct {
	ID                      int64                            `gorm:"primaryKey;autoIncrement"`
	TenantID                int64                            `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_store_model_profile_assignment,priority:1"`
	StoreID                 int64                            `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_store_model_profile_assignment,priority:2"`
	TemplateID              int64                            `gorm:"type:bigint;not null;default:0;index"` // Active template; zero means the Store has never completed readiness.
	TemplateRevision        int64                            `gorm:"type:bigint;not null;default:0;index"`
	PendingTemplateID       int64                            `gorm:"type:bigint;not null;default:0;index"`
	PendingTemplateRevision int64                            `gorm:"type:bigint;not null;default:0;index"`
	PendingRequestedAt      *time.Time                       `gorm:"type:datetime;index"`
	PendingRequestedBy      int64                            `gorm:"type:bigint;not null;default:0;index"`
	PendingRequestedByName  string                           `gorm:"type:varchar(100);not null;default:''"`
	Status                  enums.StoreModelAssignmentStatus `gorm:"type:varchar(30);not null;default:'assigned';index"`
	ReadinessStatus         string                           `gorm:"type:varchar(30);not null;default:'pending';index"`
	LastValidatedAt         *time.Time                       `gorm:"type:datetime;index"`
	LastReadyAt             *time.Time                       `gorm:"type:datetime;index"`
	LastErrorClass          string                           `gorm:"type:varchar(80);not null;default:'';index"`
	LastErrorMessage        string                           `gorm:"type:text"`
	AssignedAt              time.Time                        `gorm:"type:datetime;not null;index"`
	AssignedBy              int64                            `gorm:"type:bigint;not null;default:0;index"`
	AssignedByName          string                           `gorm:"type:varchar(100);not null;default:''"`
	AuditFields
}
