package models

import "time"

type PMSSandboxStore struct {
	ID              int64     `gorm:"primaryKey;autoIncrement"`
	StoreID         int64     `gorm:"type:bigint;not null;uniqueIndex"`
	ActiveDatasetID int64     `gorm:"type:bigint;not null;default:0"`
	LockVersion     int64     `gorm:"type:bigint;not null;default:0"`
	UpdatedAt       time.Time `gorm:"type:datetime;not null"`
}

type PMSSandboxDataset struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	StoreID   int64     `gorm:"type:bigint;not null;index"`
	Version   int64     `gorm:"type:bigint;not null;default:1"`
	CreatedAt time.Time `gorm:"type:datetime;not null"`
}

type PMSSandboxRoomType struct {
	ID         int64  `gorm:"primaryKey;autoIncrement"`
	StoreID    int64  `gorm:"type:bigint;not null;index"`
	DatasetID  int64  `gorm:"type:bigint;not null;index"`
	Name       string `gorm:"type:varchar(80);not null"`
	Rank       int    `gorm:"type:int;not null"`
	PriceCents int64  `gorm:"type:bigint;not null"`
	Enabled    bool   `gorm:"not null"`
}

type PMSSandboxRoom struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	StoreID     int64  `gorm:"type:bigint;not null;index"`
	DatasetID   int64  `gorm:"type:bigint;not null;uniqueIndex:uk_sandbox_room_number"`
	RoomTypeID  int64  `gorm:"type:bigint;not null;index"`
	Number      string `gorm:"type:varchar(40);not null;uniqueIndex:uk_sandbox_room_number"`
	Floor       string `gorm:"type:varchar(40);not null"`
	CleanStatus string `gorm:"type:varchar(20);not null"`
	Description string `gorm:"type:text"`
	Enabled     bool   `gorm:"not null"`
}

type PMSSandboxOrder struct {
	ID                int64     `gorm:"primaryKey;autoIncrement"`
	StoreID           int64     `gorm:"type:bigint;not null;index"`
	DatasetID         int64     `gorm:"type:bigint;not null;uniqueIndex:uk_sandbox_order_number"`
	Number            string    `gorm:"type:varchar(80);not null;uniqueIndex:uk_sandbox_order_number"`
	GuestName         string    `gorm:"type:varchar(80);not null"`
	Phone             string    `gorm:"type:varchar(30);not null;index"`
	RoomTypeID        int64     `gorm:"type:bigint;not null;index"`
	RoomID            int64     `gorm:"type:bigint;not null;index"`
	CheckIn           time.Time `gorm:"type:datetime;not null;index"`
	CheckOut          time.Time `gorm:"type:datetime;not null;index"`
	PayableCents      int64     `gorm:"type:bigint;not null"`
	IncludesBreakfast bool      `gorm:"not null"`
	Status            string    `gorm:"type:varchar(30);not null;index"`
	Version           int64     `gorm:"type:bigint;not null;default:1"`
	UpdatedAt         time.Time `gorm:"type:datetime;not null"`
}

type PMSSandboxGrade struct {
	ID                 int64  `gorm:"primaryKey;autoIncrement"`
	StoreID            int64  `gorm:"type:bigint;not null;index"`
	DatasetID          int64  `gorm:"type:bigint;not null;index"`
	Name               string `gorm:"type:varchar(80);not null"`
	BenefitsJSON       string `gorm:"type:text"`
	BirthdayJSON       string `gorm:"type:text"`
	FreeUpgradeMaxRank int    `gorm:"type:int;not null"`
	Enabled            bool   `gorm:"not null"`
}

type PMSSandboxMember struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	StoreID    int64     `gorm:"type:bigint;not null;index"`
	DatasetID  int64     `gorm:"type:bigint;not null;index"`
	Name       string    `gorm:"type:varchar(80);not null"`
	Phone      string    `gorm:"type:varchar(30);not null;index"`
	GradeID    int64     `gorm:"type:bigint;not null"`
	Birthday   string    `gorm:"type:varchar(10);not null"`
	ValidUntil time.Time `gorm:"type:datetime;not null"`
	Enabled    bool      `gorm:"not null"`
}

type PMSSandboxRule struct {
	ID             int64      `gorm:"primaryKey;autoIncrement"`
	StoreID        int64      `gorm:"type:bigint;not null;index"`
	DatasetID      int64      `gorm:"type:bigint;not null;uniqueIndex:uk_sandbox_rule_code"`
	Code           string     `gorm:"type:varchar(80);not null;uniqueIndex:uk_sandbox_rule_code"`
	Name           string     `gorm:"type:varchar(100);not null"`
	Text           string     `gorm:"type:text"`
	Action         string     `gorm:"type:varchar(40);not null"`
	AmountCents    int64      `gorm:"type:bigint;not null"`
	CheckoutTime   string     `gorm:"type:varchar(10);not null"`
	MinimumGradeID int64      `gorm:"type:bigint;not null;default:0"`
	Enabled        bool       `gorm:"not null"`
	ValidFrom      *time.Time `gorm:"type:datetime"`
	ValidUntil     *time.Time `gorm:"type:datetime"`
}

type PMSSandboxResource struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	StoreID         int64  `gorm:"type:bigint;not null;index"`
	DatasetID       int64  `gorm:"type:bigint;not null;uniqueIndex:uk_sandbox_resource_code"`
	Code            string `gorm:"type:varchar(80);not null;uniqueIndex:uk_sandbox_resource_code"`
	Name            string `gorm:"type:varchar(100);not null"`
	Token           string `gorm:"type:text"`
	CardPayload     string `gorm:"type:text"`
	MessageType     string `gorm:"type:varchar(30);not null;default:''"`
	SourceMessageID int64  `gorm:"type:bigint;not null;default:0"`
	Enabled         bool   `gorm:"not null"`
}

type PMSSandboxBinding struct {
	ID         int64 `gorm:"primaryKey;autoIncrement"`
	StoreID    int64 `gorm:"type:bigint;not null;index"`
	DatasetID  int64 `gorm:"type:bigint;not null;uniqueIndex:uk_sandbox_customer_binding"`
	CustomerID int64 `gorm:"type:bigint;not null;uniqueIndex:uk_sandbox_customer_binding"`
	OrderID    int64 `gorm:"type:bigint;not null"`
	MemberID   int64 `gorm:"type:bigint;not null"`
}
