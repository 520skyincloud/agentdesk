package models

import "time"

// EmailVerificationCode stores one hashed, single-use email verification challenge.
type EmailVerificationCode struct {
	ID                    int64      `gorm:"primaryKey;autoIncrement"`
	Email                 string     `gorm:"type:varchar(100);not null;index"`
	Purpose               string     `gorm:"type:varchar(40);not null;index"`
	ScopeTokenHash        string     `gorm:"type:varchar(64);not null;default:'';index"`
	CodeSalt              string     `gorm:"type:varchar(64);not null"`
	CodeHash              string     `gorm:"type:varchar(64);not null"`
	VerificationTokenHash string     `gorm:"type:varchar(64);not null;default:'';index"`
	ExpiresAt             time.Time  `gorm:"type:datetime;not null;index"`
	VerifiedAt            *time.Time `gorm:"type:datetime;index"`
	ConsumedAt            *time.Time `gorm:"type:datetime;index"`
	AttemptCount          int        `gorm:"type:int;not null;default:0"`
	MaxAttempts           int        `gorm:"type:int;not null;default:5"`
	RequestIP             string     `gorm:"type:varchar(64);not null;default:'';index"`
	UserAgent             string     `gorm:"type:varchar(500);not null;default:''"`
	LastError             string     `gorm:"type:varchar(500);not null;default:''"`
	CreatedAt             time.Time  `gorm:"type:datetime;not null;index"`
	UpdatedAt             time.Time  `gorm:"type:datetime;not null;index"`
}
