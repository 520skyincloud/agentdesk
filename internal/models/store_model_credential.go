package models

import "time"

// StoreModelCredential stores one encrypted model gateway credential per store.
// Plaintext keys are decrypted only for an immediate server-side model call.
type StoreModelCredential struct {
	ID                      int64      `gorm:"primaryKey;autoIncrement"`
	CompanyID               int64      `gorm:"type:bigint;not null;default:0;index"`
	StoreID                 int64      `gorm:"type:bigint;not null;default:0;uniqueIndex"`
	EncryptedKey            string     `gorm:"type:text"`
	KeyNonce                string     `gorm:"type:varchar(128);not null;default:''"`
	KeyFingerprint          string     `gorm:"type:varchar(64);not null;default:'';index"`
	CredentialRevision      int64      `gorm:"type:bigint;not null;default:0;index"`
	Status                  string     `gorm:"type:varchar(30);not null;default:'unconfigured';index"`
	CandidateEncryptedKey   string     `gorm:"type:text"`
	CandidateKeyNonce       string     `gorm:"type:varchar(128);not null;default:''"`
	CandidateKeyFingerprint string     `gorm:"type:varchar(64);not null;default:'';index"`
	CandidateRevision       int64      `gorm:"type:bigint;not null;default:0;index"`
	CandidateStatus         string     `gorm:"type:varchar(30);not null;default:'';index"`
	LastTestStatus          string     `gorm:"type:varchar(30);not null;default:'';index"`
	LastTestedAt            *time.Time `gorm:"type:datetime;index"`
	LastTestLatencyMS       int64      `gorm:"type:bigint;not null;default:0"`
	LastFastGPTSyncStatus   string     `gorm:"type:varchar(30);not null;default:'';index"`
	LastFastGPTSyncedAt     *time.Time `gorm:"type:datetime;index"`
	LastErrorClass          string     `gorm:"type:varchar(80);not null;default:''"`
	AuditFields
}
