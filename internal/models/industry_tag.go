package models

import (
	"time"

	"agent-desk/internal/pkg/enums"
)

// IndustryTagDefinition is a platform-owned immutable semantic definition for
// one industry profile. Tenants receive Tag projections from this catalog.
type IndustryTagDefinition struct {
	ID                 int64        `gorm:"primaryKey;autoIncrement"`
	IntentProfileID    int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_industry_tag_semantic,priority:1"`
	ParentID           int64        `gorm:"type:bigint;not null;default:0;index"`
	Name               string       `gorm:"type:varchar(80);not null;default:''"`
	SemanticKey        string       `gorm:"type:varchar(128);not null;default:'';index;uniqueIndex:uk_industry_tag_semantic,priority:2"`
	Aliases            string       `gorm:"type:text"`
	ConflictGroup      string       `gorm:"type:varchar(80);not null;default:'';index"`
	ApplicableScene    string       `gorm:"type:varchar(255);not null;default:''"`
	AIEnabled          bool         `gorm:"not null;default:false;index"`
	ReplyEnabled       bool         `gorm:"not null;default:false;index"`
	DefinitionRevision int64        `gorm:"type:bigint;not null;default:1;index"`
	SortNo             int          `gorm:"type:int;not null;default:0;index"`
	Status             enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// TenantCustomerTagPolicy stores tenant-level evolution defaults. The hard
// six-tag ceiling is enforced by service code and is not tenant configurable.
type TenantCustomerTagPolicy struct {
	ID                            int64        `gorm:"primaryKey;autoIncrement"`
	TenantID                      int64        `gorm:"type:bigint;not null;default:0;uniqueIndex"`
	IntentProfileID               int64        `gorm:"type:bigint;not null;default:0;index"`
	QuietPeriodMinutes            int          `gorm:"type:int;not null;default:1440"`
	MinimumConfidence             float64      `gorm:"type:decimal(5,4);not null;default:0.8"`
	MaxOperationsPerRun           int          `gorm:"type:int;not null;default:6"`
	EvolutionDefaultEnabled       bool         `gorm:"not null;default:false;index"`
	ReplyTagContextDefaultEnabled bool         `gorm:"not null;default:false;index"`
	Status                        enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// StoreCustomerTagRuntimePolicy stores the two independently deployable Store
// switches required for gray release and immediate rollback.
type StoreCustomerTagRuntimePolicy struct {
	ID                          int64        `gorm:"primaryKey;autoIncrement"`
	TenantID                    int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_store_customer_tag_runtime_policy,priority:1"`
	StoreID                     int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_store_customer_tag_runtime_policy,priority:2"`
	CustomerTagEvolutionEnabled bool         `gorm:"not null;default:false;index"`
	ReplyTagContextEnabled      bool         `gorm:"not null;default:false;index"`
	Status                      enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// CustomerTagRelation is the authoritative Store-isolated customer tag state.
type CustomerTagRelation struct {
	ID                      int64      `gorm:"primaryKey;autoIncrement"`
	TenantID                int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_customer_tag_relation,priority:1"`
	StoreID                 int64      `gorm:"type:bigint;not null;default:0;index"`
	CustomerID              int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreCustomerRelationID int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_customer_tag_relation,priority:2"`
	TagID                   int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_customer_tag_relation,priority:3"`
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
	TenantID                int64     `gorm:"type:bigint;not null;default:0;index"`
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

// StoreCustomerTagDecision is append-only evidence of the explicit
// supervisor choice made when reconciling one customer's Store relations.
type StoreCustomerTagDecision struct {
	ID                     int64                                   `gorm:"primaryKey;autoIncrement"`
	TenantID               int64                                   `gorm:"type:bigint;not null;default:0;index"`
	CustomerID             int64                                   `gorm:"type:bigint;not null;default:0;index"`
	SourceStoreID          int64                                   `gorm:"type:bigint;not null;default:0;index"`
	SourceStoreRelationID  int64                                   `gorm:"type:bigint;not null;default:0;index"`
	TargetStoreID          int64                                   `gorm:"type:bigint;not null;default:0;index"`
	TargetStoreRelationID  int64                                   `gorm:"type:bigint;not null;default:0;index"`
	Strategy               enums.StoreCustomerTagReconcileStrategy `gorm:"type:varchar(30);not null;default:'';index"`
	SourceTagIDsJSON       string                                  `gorm:"type:text;not null"`
	TargetBeforeTagIDsJSON string                                  `gorm:"type:text;not null"`
	TargetAfterTagIDsJSON  string                                  `gorm:"type:text;not null"`
	OperatorID             int64                                   `gorm:"type:bigint;not null;default:0;index"`
	OperatorName           string                                  `gorm:"type:varchar(100);not null;default:''"`
	CreatedAt              time.Time                               `gorm:"type:datetime;not null;index"`
}
