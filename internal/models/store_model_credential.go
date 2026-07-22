package models

import (
	"time"

	"agent-desk/internal/pkg/enums"
)

// StoreModelCredential stores one active and at most one candidate encrypted
// NewAPI credential for a Tenant + Store. Secret material is never returned.
type StoreModelCredential struct {
	ID                      int64                          `gorm:"primaryKey;autoIncrement"`
	TenantID                int64                          `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_store_model_credential,priority:1"`
	StoreID                 int64                          `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_store_model_credential,priority:2"`
	EncryptedKey            string                         `gorm:"type:text"`
	KeyNonce                string                         `gorm:"type:varchar(255);not null;default:''"`
	KeyFingerprint          string                         `gorm:"type:varchar(64);not null;default:'';index"`
	CipherVersion           string                         `gorm:"type:varchar(30);not null;default:''"`
	MasterKeyID             string                         `gorm:"type:varchar(100);not null;default:'';index"`
	CredentialRevision      int64                          `gorm:"type:bigint;not null;default:0;index"`
	Status                  enums.StoreCredentialStatus    `gorm:"type:varchar(30);not null;default:'unconfigured';index"`
	CandidateEncryptedKey   string                         `gorm:"type:text"`
	CandidateKeyNonce       string                         `gorm:"type:varchar(255);not null;default:''"`
	CandidateKeyFingerprint string                         `gorm:"type:varchar(64);not null;default:'';index"`
	CandidateCipherVersion  string                         `gorm:"type:varchar(30);not null;default:''"`
	CandidateMasterKeyID    string                         `gorm:"type:varchar(100);not null;default:'';index"`
	CandidateRevision       int64                          `gorm:"type:bigint;not null;default:0;index"`
	CandidateStatus         enums.StoreCredentialStatus    `gorm:"type:varchar(30);not null;default:'';index"`
	CandidateApprovalStatus enums.CredentialApprovalStatus `gorm:"type:varchar(30);not null;default:'not_required';index"`
	CandidateRequestedBy    int64                          `gorm:"type:bigint;not null;default:0;index"`
	CandidateRequestedAt    *time.Time                     `gorm:"type:datetime;index"`
	CandidateApprovedBy     int64                          `gorm:"type:bigint;not null;default:0;index"`
	CandidateApprovedAt     *time.Time                     `gorm:"type:datetime;index"`
	LastTestStatus          string                         `gorm:"type:varchar(30);not null;default:'';index"`
	LastTestedAt            *time.Time                     `gorm:"type:datetime;index"`
	LastTestLatencyMS       int64                          `gorm:"type:bigint;not null;default:0"`
	LastFastGPTSyncStatus   string                         `gorm:"type:varchar(30);not null;default:'';index"`
	LastFastGPTSyncedAt     *time.Time                     `gorm:"type:datetime;index"`
	LastErrorClass          string                         `gorm:"type:varchar(80);not null;default:'';index"`
	LastErrorMessage        string                         `gorm:"type:text"`
	AuditFields
}

// StoreCredentialPolicy controls whether one Store staff account can maintain
// its own credential and whether a supervisor must approve activation.
type StoreCredentialPolicy struct {
	ID                         int64        `gorm:"primaryKey;autoIncrement"`
	TenantID                   int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_store_credential_policy,priority:1"`
	StoreID                    int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_store_credential_policy,priority:2"`
	AllowCredentialSelfService bool         `gorm:"not null;default:false;index"`
	RequireSupervisorApproval  bool         `gorm:"not null;default:false;index"`
	Status                     enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// StoreModelCredentialAuditLog is append-only and contains no secret value,
// ciphertext, nonce, or complete fingerprint.
type StoreModelCredentialAuditLog struct {
	ID               int64                       `gorm:"primaryKey;autoIncrement"`
	TenantID         int64                       `gorm:"type:bigint;not null;default:0;index"`
	StoreID          int64                       `gorm:"type:bigint;not null;default:0;index"`
	CredentialID     int64                       `gorm:"type:bigint;not null;default:0;index"`
	RequestID        string                      `gorm:"type:varchar(128);not null;default:'';index"`
	Action           enums.CredentialAuditAction `gorm:"type:varchar(30);not null;default:'';index"`
	Result           enums.CredentialAuditResult `gorm:"type:varchar(30);not null;default:'';index"`
	FromRevision     int64                       `gorm:"type:bigint;not null;default:0"`
	ToRevision       int64                       `gorm:"type:bigint;not null;default:0"`
	FingerprintLast6 string                      `gorm:"type:varchar(6);not null;default:''"`
	OperatorID       int64                       `gorm:"type:bigint;not null;default:0;index"`
	OperatorName     string                      `gorm:"type:varchar(100);not null;default:''"`
	OperatorRole     string                      `gorm:"type:varchar(100);not null;default:''"`
	ApproverID       int64                       `gorm:"type:bigint;not null;default:0;index"`
	ApproverName     string                      `gorm:"type:varchar(100);not null;default:''"`
	ErrorClass       string                      `gorm:"type:varchar(80);not null;default:'';index"`
	ClientIP         string                      `gorm:"type:varchar(64);not null;default:''"`
	CreatedAt        time.Time                   `gorm:"type:datetime;not null;index"`
}
