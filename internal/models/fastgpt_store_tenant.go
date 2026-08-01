package models

import "time"

// FastGPTStoreTenant stores the non-sensitive binding from one Agent Desk
// Store to one FastGPT Tenant Team and records which local Profile/Credential
// revisions have been applied. Secret material remains in StoreModelCredential
// and is never persisted in this binding.
type FastGPTStoreTenant struct {
	ID                         int64      `gorm:"primaryKey;autoIncrement"`
	TenantID                   int64      `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_fastgpt_store_tenant,priority:1;index"`
	StoreID                    int64      `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_fastgpt_store_tenant,priority:2;index"`
	TenantTeamID               string     `gorm:"type:varchar(128);not null;default:'';uniqueIndex"`
	TenantTeamName             string     `gorm:"type:varchar(200);not null;default:''"`
	Status                     string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	TargetProfileID            int64      `gorm:"type:bigint;not null;default:0;index"`
	TargetProfileRevision      int64      `gorm:"type:bigint;not null;default:0;index"`
	AppliedProfileID           int64      `gorm:"type:bigint;not null;default:0;index"`
	AppliedProfileRevision     int64      `gorm:"type:bigint;not null;default:0;index"`
	TargetStoreStaffBindingID  int64      `gorm:"type:bigint;not null;default:0;index"`
	AppliedStoreStaffBindingID int64      `gorm:"type:bigint;not null;default:0;index"`
	TargetCredentialRevision   int64      `gorm:"type:bigint;not null;default:0;index"`
	AppliedCredentialRevision  int64      `gorm:"type:bigint;not null;default:0;index"`
	AppliedKeyFingerprint      string     `gorm:"type:varchar(64);not null;default:'';index" json:"-"`
	ReadinessStatus            string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	LastSyncedAt               *time.Time `gorm:"type:datetime;index"`
	LastError                  string     `gorm:"type:text"`
	AuditFields
}

// FastGPTUsageSyncState keeps a cursor for importing immutable FastGPT usage
// evidence once the server-side integration API becomes available.
type FastGPTUsageSyncState struct {
	ID                  int64      `gorm:"primaryKey;autoIncrement"`
	TenantID            int64      `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_fastgpt_usage_sync_state,priority:1;index"`
	StoreID             int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreStaffBindingID int64      `gorm:"type:bigint;not null;default:0;index"`
	KnowledgeBaseID     int64      `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_fastgpt_usage_sync_state,priority:2;index"`
	TenantTeamID        string     `gorm:"type:varchar(128);not null;default:'';index"`
	Cursor              string     `gorm:"type:varchar(255);not null;default:''"`
	ModelProfileID      int64      `gorm:"type:bigint;not null;default:0;index"`
	ProfileRevision     int64      `gorm:"type:bigint;not null;default:0;index"`
	CredentialRevision  int64      `gorm:"type:bigint;not null;default:0;index"`
	KeyFingerprint      string     `gorm:"type:varchar(64);not null;default:'';index" json:"-"`
	FastGPTProfileID    string     `gorm:"type:varchar(128);not null;default:'';index"`
	FastGPTRevision     string     `gorm:"type:varchar(80);not null;default:'';index"`
	LastSyncedAt        *time.Time `gorm:"type:datetime;index"`
	LastError           string     `gorm:"type:text"`
	CreatedAt           time.Time  `gorm:"type:datetime;not null"`
	UpdatedAt           time.Time  `gorm:"type:datetime;not null"`
}
