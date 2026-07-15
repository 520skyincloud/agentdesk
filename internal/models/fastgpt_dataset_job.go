package models

import "time"

// FastGPTDatasetJob records durable dataset creation/upload/delete work.
type FastGPTDatasetJob struct {
	ID               int64      `gorm:"primaryKey;autoIncrement"`
	TenantID         int64      `gorm:"type:bigint;not null;default:0;index"`
	TaskKey          string     `gorm:"type:varchar(80);not null;uniqueIndex"`
	CompanyID        int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreID          int64      `gorm:"type:bigint;not null;default:0;index"`
	KnowledgeBaseID  int64      `gorm:"type:bigint;not null;default:0;index"`
	Action           string     `gorm:"type:varchar(30);not null;index"`
	Status           string     `gorm:"type:varchar(30);not null;index"`
	DatasetID        string     `gorm:"type:varchar(128);not null;default:'';index"`
	CollectionID     string     `gorm:"type:varchar(128);not null;default:'';index"`
	Filename         string     `gorm:"type:varchar(255);not null;default:''"`
	TemporaryAssetID string     `gorm:"type:varchar(64);not null;default:'';index"`
	AttemptCount     int        `gorm:"type:int;not null;default:0"`
	NextRetryAt      *time.Time `gorm:"type:datetime;index"`
	StartedAt        *time.Time `gorm:"type:datetime;index"`
	CompletedAt      *time.Time `gorm:"type:datetime;index"`
	LastError        string     `gorm:"type:text"`
	CreatedAt        time.Time  `gorm:"type:datetime;not null;index"`
	UpdatedAt        time.Time  `gorm:"type:datetime;not null;index"`
}
