package models

import (
	"time"

	"agent-desk/internal/pkg/enums"
)

// ConversationEvolutionState is the durable inactivity cursor and worker
// lease for one Tenant conversation session.
type ConversationEvolutionState struct {
	ID                      int64        `gorm:"primaryKey;autoIncrement"`
	TenantID                int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_evolution_state,priority:1"`
	ConversationID          int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_evolution_state,priority:2"`
	SessionNo               int          `gorm:"type:int;not null;default:1;index;uniqueIndex:uk_conversation_evolution_state,priority:3"`
	StoreID                 int64        `gorm:"type:bigint;not null;default:0;index"`
	CustomerID              int64        `gorm:"type:bigint;not null;default:0;index"`
	StoreCustomerRelationID int64        `gorm:"type:bigint;not null;default:0;index"`
	LastObservedMessageID   int64        `gorm:"type:bigint;not null;default:0;index"`
	LastEvolvedMessageID    int64        `gorm:"type:bigint;not null;default:0;index"`
	NextEvolutionAt         *time.Time   `gorm:"type:datetime;index"`
	LastEvolutionRunID      int64        `gorm:"type:bigint;not null;default:0;index"`
	LastStatus              string       `gorm:"type:varchar(30);not null;default:'waiting';index"`
	SummaryVersion          int64        `gorm:"type:bigint;not null;default:0"`
	AttemptCount            int          `gorm:"type:int;not null;default:0"`
	NextRetryAt             *time.Time   `gorm:"type:datetime;index"`
	LeaseOwner              string       `gorm:"type:varchar(128);not null;default:'';index"`
	LeaseExpiresAt          *time.Time   `gorm:"type:datetime;index"`
	LastErrorClass          string       `gorm:"type:varchar(80);not null;default:'';index"`
	Status                  enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// ConversationEvolutionRun is immutable by input checkpoint and stores only
// redacted outputs and branch statuses.
type ConversationEvolutionRun struct {
	ID                      int64      `gorm:"primaryKey;autoIncrement"`
	RunKey                  string     `gorm:"type:varchar(191);not null;default:'';uniqueIndex"`
	TenantID                int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_evolution_run,priority:1"`
	ConversationID          int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_evolution_run,priority:2"`
	SessionNo               int        `gorm:"type:int;not null;default:1;index;uniqueIndex:uk_conversation_evolution_run,priority:3"`
	EndMessageID            int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_conversation_evolution_run,priority:4"`
	StoreID                 int64      `gorm:"type:bigint;not null;default:0;index"`
	CustomerID              int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreCustomerRelationID int64      `gorm:"type:bigint;not null;default:0;index"`
	IntentProfileID         int64      `gorm:"type:bigint;not null;default:0;index"`
	ModelProfileID          int64      `gorm:"type:bigint;not null;default:0;index"`
	ModelProfileRevision    int64      `gorm:"type:bigint;not null;default:0;index"`
	CredentialRevision      int64      `gorm:"type:bigint;not null;default:0;index"`
	InputHash               string     `gorm:"type:varchar(64);not null;default:'';index"`
	ChunkCount              int        `gorm:"type:int;not null;default:0"`
	RunStatus               string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	SummaryStatus           string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	KnowledgeStatus         string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	TagStatus               string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	RetryCount              int        `gorm:"type:int;not null;default:0"`
	RedactedResult          string     `gorm:"type:text"`
	LastErrorClass          string     `gorm:"type:varchar(80);not null;default:'';index"`
	StartedAt               *time.Time `gorm:"type:datetime;index"`
	FinishedAt              *time.Time `gorm:"type:datetime;index"`
	AuditFields
}
