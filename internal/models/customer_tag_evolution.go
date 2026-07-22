package models

import (
	"time"

	"agent-desk/internal/pkg/enums"
)

// CustomerTagRelation is the authoritative store-isolated customer tag state.
type CustomerTagRelation struct {
	ID                      int64      `gorm:"primaryKey;autoIncrement"`
	CompanyID               int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreID                 int64      `gorm:"type:bigint;not null;default:0;index"`
	CustomerID              int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreCustomerRelationID int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_customer_tag_relation"`
	TagID                   int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_customer_tag_relation"`
	Source                  string     `gorm:"type:varchar(30);not null;default:'';index"`
	RelationStatus          string     `gorm:"type:varchar(30);not null;default:'active';index"`
	Confidence              float64    `gorm:"type:decimal(5,4);not null;default:0"`
	EvidenceCount           int        `gorm:"type:int;not null;default:0"`
	FirstMatchedAt          *time.Time `gorm:"type:datetime;index"`
	LastMatchedAt           *time.Time `gorm:"type:datetime;index"`
	ManualProtected         bool       `gorm:"not null;default:false;index"`
	LastEvolutionRunID      int64      `gorm:"type:bigint;not null;default:0;index"`
	InactivatedAt           *time.Time `gorm:"type:datetime;index"`
	AuditFields
}

// CustomerTagChangeLog is append-only evidence for manual and AI tag changes.
type CustomerTagChangeLog struct {
	ID                      int64     `gorm:"primaryKey;autoIncrement"`
	CompanyID               int64     `gorm:"type:bigint;not null;default:0;index"`
	StoreID                 int64     `gorm:"type:bigint;not null;default:0;index"`
	CustomerID              int64     `gorm:"type:bigint;not null;default:0;index"`
	StoreCustomerRelationID int64     `gorm:"type:bigint;not null;default:0;index"`
	ConversationID          int64     `gorm:"type:bigint;not null;default:0;index"`
	EvolutionRunID          int64     `gorm:"type:bigint;not null;default:0;index"`
	Action                  string    `gorm:"type:varchar(30);not null;default:'';index"`
	OldTagID                int64     `gorm:"type:bigint;not null;default:0;index"`
	NewTagID                int64     `gorm:"type:bigint;not null;default:0;index"`
	EvidenceMessageIDs      string    `gorm:"type:text"`
	Source                  string    `gorm:"type:varchar(30);not null;default:'';index"`
	Confidence              float64   `gorm:"type:decimal(5,4);not null;default:0"`
	OperatorType            string    `gorm:"type:varchar(30);not null;default:'';index"`
	OperatorID              int64     `gorm:"type:bigint;not null;default:0;index"`
	OperatorName            string    `gorm:"type:varchar(100);not null;default:''"`
	CreatedAt               time.Time `gorm:"type:datetime;not null;index"`
}

// ConversationEvolutionState is the durable 24-hour inactivity cursor.
type ConversationEvolutionState struct {
	ID                      int64        `gorm:"primaryKey;autoIncrement"`
	ConversationID          int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_evolution_state"`
	SessionNo               int          `gorm:"type:int;not null;default:1;index;uniqueIndex:uk_conversation_evolution_state"`
	CompanyID               int64        `gorm:"type:bigint;not null;default:0;index"`
	StoreID                 int64        `gorm:"type:bigint;not null;default:0;index"`
	CustomerID              int64        `gorm:"type:bigint;not null;default:0;index"`
	StoreCustomerRelationID int64        `gorm:"type:bigint;not null;default:0;index"`
	LastObservedMessageID   int64        `gorm:"type:bigint;not null;default:0;index"`
	LastEvolvedMessageID    int64        `gorm:"type:bigint;not null;default:0;index"`
	NextEvolutionAt         *time.Time   `gorm:"type:datetime;index"`
	LastEvolutionRunID      int64        `gorm:"type:bigint;not null;default:0;index"`
	LastStatus              string       `gorm:"type:varchar(30);not null;default:'waiting';index"`
	SummaryVersion          int64        `gorm:"type:bigint;not null;default:0"`
	LastErrorClass          string       `gorm:"type:varchar(80);not null;default:''"`
	Status                  enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// ConversationEvolutionRun is immutable by input checkpoint and stores only
// redacted model results and branch statuses.
type ConversationEvolutionRun struct {
	ID                 int64      `gorm:"primaryKey;autoIncrement"`
	RunKey             string     `gorm:"type:varchar(191);not null;default:'';uniqueIndex"`
	ConversationID     int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_evolution_run"`
	SessionNo          int        `gorm:"type:int;not null;default:1;index;uniqueIndex:uk_conversation_evolution_run"`
	EndMessageID       int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_evolution_run"`
	CompanyID          int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreID            int64      `gorm:"type:bigint;not null;default:0;index"`
	CustomerID         int64      `gorm:"type:bigint;not null;default:0;index"`
	InputHash          string     `gorm:"type:varchar(64);not null;default:'';index"`
	TemplateRevision   int64      `gorm:"type:bigint;not null;default:0;index"`
	CredentialRevision int64      `gorm:"type:bigint;not null;default:0;index"`
	ChunkCount         int        `gorm:"type:int;not null;default:0"`
	RunStatus          string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	SummaryStatus      string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	KnowledgeStatus    string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	TagStatus          string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	RetryCount         int        `gorm:"type:int;not null;default:0"`
	RedactedResult     string     `gorm:"type:text"`
	LastErrorClass     string     `gorm:"type:varchar(80);not null;default:''"`
	StartedAt          *time.Time `gorm:"type:datetime;index"`
	FinishedAt         *time.Time `gorm:"type:datetime;index"`
	AuditFields
}
