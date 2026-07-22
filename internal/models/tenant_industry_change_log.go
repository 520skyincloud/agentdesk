package models

import "time"

// TenantIndustryChangeLog is append-only evidence for the industry Profile
// selected for a Tenant. It deliberately stores no Prompt or Schema content.
type TenantIndustryChangeLog struct {
	ID                    int64     `gorm:"primaryKey;autoIncrement"`
	TenantID              int64     `gorm:"type:bigint;not null;default:0;index"`
	BeforeIntentProfileID int64     `gorm:"type:bigint;not null;default:0;index"`
	AfterIntentProfileID  int64     `gorm:"type:bigint;not null;default:0;index"`
	BeforeRevision        int64     `gorm:"type:bigint;not null;default:0"`
	AfterRevision         int64     `gorm:"type:bigint;not null;default:0"`
	Action                string    `gorm:"type:varchar(30);not null;default:'';index"`
	Reason                string    `gorm:"type:varchar(500);not null;default:''"`
	OperatorID            int64     `gorm:"type:bigint;not null;default:0;index"`
	OperatorName          string    `gorm:"type:varchar(100);not null;default:''"`
	CreatedAt             time.Time `gorm:"type:datetime;not null;index"`
}
