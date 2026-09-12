package models

import "time"

// ServiceRecoveryCase records a service-recovery request and its idempotency
// key. External compensation execution is intentionally represented as a
// stateful record and remains disabled until an adapter is configured.
type ServiceRecoveryCase struct {
	ID             int64      `gorm:"primaryKey;autoIncrement"`
	ConversationID int64      `gorm:"type:bigint;not null;default:0;index"`
	TicketID       int64      `gorm:"type:bigint;not null;default:0;index"`
	CustomerID     int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreID        int64      `gorm:"type:bigint;not null;default:0;index"`
	RecoveryType   string     `gorm:"type:varchar(40);not null;default:'';index"`
	Status         string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	ApprovalStatus string     `gorm:"type:varchar(30);not null;default:'pending';index"`
	IdempotencyKey string     `gorm:"type:varchar(191);not null;uniqueIndex"`
	ExternalRef    string     `gorm:"type:varchar(191);not null;default:'';index"`
	Reason         string     `gorm:"type:text"`
	Payload        string     `gorm:"type:text"`
	RequestedAt    time.Time  `gorm:"type:datetime;not null;index"`
	CompletedAt    *time.Time `gorm:"type:datetime;index"`
	AuditFields
}

// CustomerEngagementProfile stores only consent, preferences and contact
// frequency state. It never infers sensitive attributes such as birthday.
type CustomerEngagementProfile struct {
	ID              int64      `gorm:"primaryKey;autoIncrement"`
	CustomerID      int64      `gorm:"type:bigint;not null;index;uniqueIndex:uk_engagement_profile"`
	StoreID         int64      `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_engagement_profile"`
	ConsentStatus   string     `gorm:"type:varchar(20);not null;default:'unknown';index"`
	UnsubscribedAt  *time.Time `gorm:"type:datetime;index"`
	LastContactAt   *time.Time `gorm:"type:datetime;index"`
	ContactCount    int        `gorm:"type:int;not null;default:0"`
	TagsJSON        string     `gorm:"type:text"`
	PreferencesJSON string     `gorm:"type:text"`
	AuditFields
}

// ProactiveReachout is an auditable, idempotent outbound suggestion/task.
// It is only sent after consent and frequency checks pass.
type ProactiveReachout struct {
	ID             int64      `gorm:"primaryKey;autoIncrement"`
	CustomerID     int64      `gorm:"type:bigint;not null;index"`
	StoreID        int64      `gorm:"type:bigint;not null;default:0;index"`
	CampaignCode   string     `gorm:"type:varchar(80);not null;index"`
	IdempotencyKey string     `gorm:"type:varchar(191);not null;uniqueIndex"`
	Status         string     `gorm:"type:varchar(30);not null;default:'draft';index"`
	Content        string     `gorm:"type:text"`
	Reason         string     `gorm:"type:text"`
	ScheduledAt    *time.Time `gorm:"type:datetime;index"`
	SentAt         *time.Time `gorm:"type:datetime;index"`
	AuditFields
}
