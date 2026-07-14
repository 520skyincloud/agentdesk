package models

import "time"

// UserRoleChangeLog 是账号角色集合变更的追加式审计记录。
type UserRoleChangeLog struct {
	ID                  int64     `gorm:"primaryKey;autoIncrement"`
	TenantID            int64     `gorm:"type:bigint;not null;default:0;index"`
	UserID              int64     `gorm:"type:bigint;not null;index"`
	BeforeRoleIDsJSON   string    `gorm:"type:text;not null"`
	AfterRoleIDsJSON    string    `gorm:"type:text;not null"`
	BeforeRoleCodesJSON string    `gorm:"type:text;not null"`
	AfterRoleCodesJSON  string    `gorm:"type:text;not null"`
	OperatorID          int64     `gorm:"type:bigint;not null;default:0;index"`
	OperatorName        string    `gorm:"type:varchar(100);not null;default:''"`
	CreatedAt           time.Time `gorm:"type:datetime;not null;index"`
}
