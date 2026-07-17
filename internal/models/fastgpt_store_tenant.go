package models

import "time"

// FastGPTStoreTenant stores the non-sensitive binding from one Agent Desk
// store to one FastGPT Tenant Team. Model credentials stay in FastGPT.
type FastGPTStoreTenant struct {
	ID             int64      `gorm:"primaryKey;autoIncrement"`
	CompanyID      int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreID        int64      `gorm:"type:bigint;not null;default:0;uniqueIndex"`
	TenantTeamID   string     `gorm:"type:varchar(128);not null;default:'';uniqueIndex"`
	TenantTeamName string     `gorm:"type:varchar(200);not null;default:''"`
	Status         string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	LastSyncedAt   *time.Time `gorm:"type:datetime;index"`
	LastError      string     `gorm:"type:text"`
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
