package models

import "time"

type AIReplyTurnAction struct {
	ID                     int64      `gorm:"primaryKey;autoIncrement"`
	TenantID               int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_turn_action,priority:1"`
	TurnID                 int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_turn_action,priority:2"`
	TaskKey                string     `gorm:"type:varchar(128);not null;default:'';index;uniqueIndex:uk_turn_action,priority:3"`
	ActionKey              string     `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uk_turn_action,priority:4"`
	ActionType             string     `gorm:"type:varchar(40);not null;default:'';index"`
	ResourceType           string     `gorm:"type:varchar(40);not null;default:'';index"`
	EligibilityFingerprint string     `gorm:"type:varchar(64);not null;default:'';index"`
	SourceEvidenceRef      string     `gorm:"type:varchar(16);not null;default:''"`
	SourceRecordID         string     `gorm:"type:varchar(255);not null;default:'';index"`
	ResourcePurpose        string     `gorm:"type:varchar(40);not null;default:'';index"`
	EligibilityReasonCode  string     `gorm:"type:varchar(80);not null;default:'';index"`
	Status                 string     `gorm:"type:varchar(24);not null;default:'requested';index;index:idx_turn_action_status_at,priority:1"`
	RequestedVersion       int        `gorm:"type:int;not null;default:1;index"`
	PreparedRevision       string     `gorm:"type:varchar(128);not null;default:''"`
	CommittedMessageID     int64      `gorm:"type:bigint;not null;default:0;index:idx_turn_action_message"`
	OutboxID               int64      `gorm:"type:bigint;not null;default:0;index:idx_turn_action_outbox"`
	ResultCode             string     `gorm:"type:varchar(80);not null;default:'';index"`
	DeliveredAt            *time.Time `gorm:"type:datetime;index"`
	CreatedAt              time.Time  `gorm:"type:datetime;not null;index"`
	CreateUserID           int64      `gorm:"type:bigint;not null;default:0;index"`
	CreateUserName         string     `gorm:"type:varchar(100);not null;default:''"`
	UpdatedAt              time.Time  `gorm:"type:datetime;not null;index;index:idx_turn_action_status_at,priority:2"`
	UpdateUserID           int64      `gorm:"type:bigint;not null;default:0;index"`
	UpdateUserName         string     `gorm:"type:varchar(100);not null;default:''"`
}
