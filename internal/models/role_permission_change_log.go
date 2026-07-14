package models

import "time"

// RolePermissionChangeLog 是角色权限集合变更的追加式审计记录。
type RolePermissionChangeLog struct {
	ID                        int64     `gorm:"primaryKey;autoIncrement"`
	RoleID                    int64     `gorm:"type:bigint;not null;index"`
	RoleCode                  string    `gorm:"type:varchar(100);not null;default:'';index"`
	BeforePermissionIDsJSON   string    `gorm:"type:text;not null"`
	AfterPermissionIDsJSON    string    `gorm:"type:text;not null"`
	BeforePermissionCodesJSON string    `gorm:"type:text;not null"`
	AfterPermissionCodesJSON  string    `gorm:"type:text;not null"`
	OperatorID                int64     `gorm:"type:bigint;not null;default:0;index"`
	OperatorName              string    `gorm:"type:varchar(100);not null;default:''"`
	CreatedAt                 time.Time `gorm:"type:datetime;not null;index"`
}
