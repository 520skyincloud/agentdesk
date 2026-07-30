package models

import (
	"time"

	"agent-desk/internal/pkg/enums"
)

// MiniProgramIdentity is a tenant-scoped stable identity derived from a real
// wx.login code exchange. Raw WeChat identifiers are encrypted at rest.
type MiniProgramIdentity struct {
	ID                 int64        `gorm:"primaryKey;autoIncrement"`
	TenantID           int64        `gorm:"type:bigint;not null;index;uniqueIndex:uk_arrival_identity,priority:1"`
	AppID              string       `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uk_arrival_identity,priority:2"`
	OpenIDCiphertext   string       `gorm:"type:text;not null"`
	OpenIDNonce        string       `gorm:"type:varchar(128);not null;default:''"`
	OpenIDFingerprint  string       `gorm:"type:varchar(64);not null;default:'';index;uniqueIndex:uk_arrival_identity,priority:3"`
	UnionIDCiphertext  string       `gorm:"type:text"`
	UnionIDNonce       string       `gorm:"type:varchar(128);not null;default:''"`
	UnionIDFingerprint string       `gorm:"type:varchar(64);not null;default:'';index"`
	LastLoginAt        *time.Time   `gorm:"type:datetime;index"`
	Status             enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// WeComSuiteCredential stores the provider suite ticket and short-lived token
// cache. Secrets are encrypted with the arrival data master key.
type WeComSuiteCredential struct {
	ID                         int64        `gorm:"primaryKey;autoIncrement"`
	SuiteID                    string       `gorm:"type:varchar(128);not null;uniqueIndex"`
	SuiteTicketCiphertext      string       `gorm:"type:text"`
	SuiteTicketNonce           string       `gorm:"type:varchar(128);not null;default:''"`
	SuiteAccessTokenCiphertext string       `gorm:"type:text"`
	SuiteAccessTokenNonce      string       `gorm:"type:varchar(128);not null;default:''"`
	SuiteAccessTokenExpiresAt  *time.Time   `gorm:"type:datetime;index"`
	LastTicketAt               *time.Time   `gorm:"type:datetime;index"`
	Status                     enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// WeComTenantAuthorization represents one authorized WeCom corporation. One
// authorization can be shared by multiple stores in the same tenant.
type WeComTenantAuthorization struct {
	ID                          int64                          `gorm:"primaryKey;autoIncrement"`
	TenantID                    int64                          `gorm:"type:bigint;not null;index"`
	SuiteCredentialID           int64                          `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_arrival_corp_auth,priority:1"`
	CorpIDCiphertext            string                         `gorm:"type:text;not null"`
	CorpIDNonce                 string                         `gorm:"type:varchar(128);not null;default:''"`
	CorpIDFingerprint           string                         `gorm:"type:varchar(64);not null;default:'';index;uniqueIndex:uk_arrival_corp_auth,priority:2"`
	CorpName                    string                         `gorm:"type:varchar(200);not null;default:''"`
	PermanentCodeCiphertext     string                         `gorm:"type:text;not null"`
	PermanentCodeNonce          string                         `gorm:"type:varchar(128);not null;default:''"`
	CorpAccessTokenCiphertext   string                         `gorm:"type:text"`
	CorpAccessTokenNonce        string                         `gorm:"type:varchar(128);not null;default:''"`
	CorpAccessTokenExpiresAt    *time.Time                     `gorm:"type:datetime;index"`
	AuthorizedScopeSnapshotJSON string                         `gorm:"type:text"`
	AuthorizationStatus         enums.WeComAuthorizationStatus `gorm:"type:varchar(40);not null;default:'pending';index"`
	AuthorizedAt                *time.Time                     `gorm:"type:datetime;index"`
	RevokedAt                   *time.Time                     `gorm:"type:datetime;index"`
	AuditFields
}

// StoreArrivalConnection binds a real store to an authorized corporation,
// one contact member, and one existing protocol instance.
type StoreArrivalConnection struct {
	ID                        int64                         `gorm:"primaryKey;autoIncrement"`
	TenantID                  int64                         `gorm:"type:bigint;not null;index"`
	StoreID                   int64                         `gorm:"type:bigint;not null;uniqueIndex"`
	StoreScene                string                        `gorm:"type:varchar(64);not null;uniqueIndex"`
	TenantAuthorizationID     int64                         `gorm:"type:bigint;not null;default:0;index"`
	ContactMemberCiphertext   string                        `gorm:"type:text"`
	ContactMemberNonce        string                        `gorm:"type:varchar(128);not null;default:''"`
	ContactMemberFingerprint  string                        `gorm:"type:varchar(64);not null;default:'';index"`
	WxWorkProtocolInstanceID  int64                         `gorm:"type:bigint;not null;default:0;index"`
	ConnectionStatus          enums.ArrivalConnectionStatus `gorm:"type:varchar(40);not null;default:'pending_authorization';index"`
	LastVerifiedAt            *time.Time                    `gorm:"type:datetime;index"`
	LastVerificationErrorCode string                        `gorm:"type:varchar(80);not null;default:''"`
	LastContactProvisionedAt  *time.Time                    `gorm:"type:datetime;index"`
	Status                    enums.Status                  `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

type StoreArrivalInvitation struct {
	ID         int64        `gorm:"primaryKey;autoIncrement"`
	TenantID   int64        `gorm:"type:bigint;not null;index"`
	StoreID    int64        `gorm:"type:bigint;not null;index"`
	TokenHash  string       `gorm:"type:varchar(64);not null;uniqueIndex"`
	ExpiresAt  time.Time    `gorm:"type:datetime;not null;index"`
	UsedAt     *time.Time   `gorm:"type:datetime;index"`
	UsedByCorp string       `gorm:"type:varchar(64);not null;default:''"`
	Status     enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

type WeComAuthorizationAttempt struct {
	ID                    int64        `gorm:"primaryKey;autoIncrement"`
	TenantID              int64        `gorm:"type:bigint;not null;index"`
	StoreID               int64        `gorm:"type:bigint;not null;index"`
	InvitationID          int64        `gorm:"type:bigint;not null;index"`
	StateHash             string       `gorm:"type:varchar(64);not null;uniqueIndex"`
	PreAuthCodeHash       string       `gorm:"type:varchar(64);not null;default:''"`
	TenantAuthorizationID int64        `gorm:"type:bigint;not null;default:0;index"`
	ExpiresAt             time.Time    `gorm:"type:datetime;not null;index"`
	CompletedAt           *time.Time   `gorm:"type:datetime;index"`
	Status                enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

type ArrivalScanEvent struct {
	ID                    int64                       `gorm:"primaryKey;autoIncrement"`
	TenantID              int64                       `gorm:"type:bigint;not null;index"`
	StoreID               int64                       `gorm:"type:bigint;not null;index"`
	MiniProgramIdentityID int64                       `gorm:"type:bigint;not null;index"`
	ScanEventHash         string                      `gorm:"type:varchar(64);not null;uniqueIndex"`
	RequestFingerprint    string                      `gorm:"type:varchar(64);not null;default:'';index"`
	SchemaVersion         string                      `gorm:"type:varchar(40);not null;default:'arrival_scan_input.v1'"`
	IdentityStatus        enums.ArrivalIdentityStatus `gorm:"type:varchar(20);not null;default:'matched';index"`
	BindingStatus         enums.ArrivalBindingStatus  `gorm:"type:varchar(30);not null;default:'unbound';index"`
	DeliveryStatus        enums.ArrivalDeliveryStatus `gorm:"type:varchar(30);not null;default:'not_bound';index"`
	ContactWayID          int64                       `gorm:"type:bigint;not null;default:0;index"`
	DeliveryAttemptedAt   *time.Time                  `gorm:"type:datetime;index"`
	DeliveryCompletedAt   *time.Time                  `gorm:"type:datetime;index"`
	DeliveryErrorCode     string                      `gorm:"type:varchar(80);not null;default:''"`
	Status                enums.Status                `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

type ArrivalSession struct {
	ID          int64      `gorm:"primaryKey;autoIncrement"`
	TenantID    int64      `gorm:"type:bigint;not null;index"`
	StoreID     int64      `gorm:"type:bigint;not null;index"`
	ScanEventID int64      `gorm:"type:bigint;not null;uniqueIndex"`
	TokenHash   string     `gorm:"type:varchar(64);not null;default:'';uniqueIndex"`
	ExpiresAt   time.Time  `gorm:"type:datetime;not null;index"`
	RevokedAt   *time.Time `gorm:"type:datetime;index"`
	AuditFields
}

type ArrivalContactWay struct {
	ID                       int64                            `gorm:"primaryKey;autoIncrement"`
	TenantID                 int64                            `gorm:"type:bigint;not null;index"`
	StoreID                  int64                            `gorm:"type:bigint;not null;index"`
	ScanEventID              int64                            `gorm:"type:bigint;not null;uniqueIndex"`
	TenantAuthorizationID    int64                            `gorm:"type:bigint;not null;index"`
	ProviderMode             enums.ArrivalContactProviderMode `gorm:"type:varchar(40);not null;default:'contact_way';index"`
	AcquisitionLinkID        int64                            `gorm:"type:bigint;not null;default:0;index"`
	ContactStateHash         string                           `gorm:"type:varchar(64);not null;uniqueIndex"`
	ConfigID                 string                           `gorm:"type:varchar(191);not null;default:'';index"`
	OriginalQRCodeCiphertext string                           `gorm:"type:text"`
	OriginalQRCodeNonce      string                           `gorm:"type:varchar(128);not null;default:''"`
	OriginalPNGBase64        string                           `gorm:"type:text"`
	PublicResourceTokenHash  string                           `gorm:"type:varchar(64);not null;default:'';uniqueIndex"`
	ArtworkPNGBase64         string                           `gorm:"type:text"`
	SourcePayloadHash        string                           `gorm:"type:varchar(64);not null;default:''"`
	PublishedPayloadHash     string                           `gorm:"type:varchar(64);not null;default:''"`
	Mode                     enums.ArrivalContactWayMode      `gorm:"type:varchar(30);not null;default:'none';index"`
	ContactWayStatus         enums.ArrivalContactWayStatus    `gorm:"type:varchar(30);not null;default:'provisioning';index"`
	FailureCode              string                           `gorm:"type:varchar(80);not null;default:''"`
	FailureStage             string                           `gorm:"type:varchar(80);not null;default:'';index"`
	ProviderHTTPStatus       int                              `gorm:"type:int;not null;default:0"`
	ProviderErrorCode        int                              `gorm:"type:int;not null;default:0;index"`
	ProviderErrorMessage     string                           `gorm:"type:varchar(500);not null;default:''"`
	FailureRetryable         bool                             `gorm:"type:int;not null;default:0;index"`
	ProvisionAttemptCount    int                              `gorm:"type:int;not null;default:0"`
	LastProvisionRequestID   string                           `gorm:"type:varchar(128);not null;default:'';index"`
	LastProvisionAttemptAt   *time.Time                       `gorm:"type:datetime;index"`
	NextProvisionRetryAt     *time.Time                       `gorm:"type:datetime;index"`
	ExpiresAt                *time.Time                       `gorm:"type:datetime;index"`
	CleanedAt                *time.Time                       `gorm:"type:datetime;index"`
	Status                   enums.Status                     `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// ArrivalAcquisitionLink stores one reusable WeCom customer-acquisition link
// for one authorization, store, and explicitly bound contact member.
type ArrivalAcquisitionLink struct {
	ID                        int64                              `gorm:"primaryKey;autoIncrement"`
	TenantID                  int64                              `gorm:"type:bigint;not null;index"`
	TenantAuthorizationID     int64                              `gorm:"type:bigint;not null;index;uniqueIndex:uk_arrival_acquisition_link,priority:1"`
	StoreID                   int64                              `gorm:"type:bigint;not null;index;uniqueIndex:uk_arrival_acquisition_link,priority:2"`
	ContactMemberFingerprint  string                             `gorm:"type:varchar(64);not null;index;uniqueIndex:uk_arrival_acquisition_link,priority:3"`
	ProviderLinkID            string                             `gorm:"type:varchar(191);not null;default:'';index"`
	ProviderLinkURLCiphertext string                             `gorm:"type:text"`
	ProviderLinkURLNonce      string                             `gorm:"type:varchar(128);not null;default:''"`
	ProviderCreateTime        int64                              `gorm:"type:bigint;not null;default:0"`
	LinkStatus                enums.ArrivalAcquisitionLinkStatus `gorm:"type:varchar(30);not null;default:'provisioning';index"`
	QuotaTotal                int64                              `gorm:"type:bigint;not null;default:0"`
	QuotaBalance              int64                              `gorm:"type:bigint;not null;default:0"`
	LastVerifiedAt            *time.Time                         `gorm:"type:datetime;index"`
	LastCustomerSyncAt        *time.Time                         `gorm:"type:datetime;index"`
	FailureCode               string                             `gorm:"type:varchar(80);not null;default:''"`
	FailureStage              string                             `gorm:"type:varchar(80);not null;default:'';index"`
	ProviderHTTPStatus        int                                `gorm:"type:int;not null;default:0"`
	ProviderErrorCode         int                                `gorm:"type:int;not null;default:0;index"`
	ProviderErrorMessage      string                             `gorm:"type:varchar(500);not null;default:''"`
	FailureRetryable          bool                               `gorm:"type:int;not null;default:0;index"`
	ProvisionAttemptCount     int                                `gorm:"type:int;not null;default:0"`
	LastProvisionRequestID    string                             `gorm:"type:varchar(128);not null;default:'';index"`
	LastProvisionAttemptAt    *time.Time                         `gorm:"type:datetime;index"`
	NextProvisionRetryAt      *time.Time                         `gorm:"type:datetime;index"`
	Status                    enums.Status                       `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

type ArrivalStoreBinding struct {
	ID                              int64                               `gorm:"primaryKey;autoIncrement"`
	TenantID                        int64                               `gorm:"type:bigint;not null;index"`
	StoreID                         int64                               `gorm:"type:bigint;not null;index;uniqueIndex:uk_arrival_store_binding,priority:2"`
	MiniProgramIdentityID           int64                               `gorm:"type:bigint;not null;index;uniqueIndex:uk_arrival_store_binding,priority:1"`
	TenantAuthorizationID           int64                               `gorm:"type:bigint;not null;default:0;index"`
	ExternalUserIDCiphertext        string                              `gorm:"type:text"`
	ExternalUserIDNonce             string                              `gorm:"type:varchar(128);not null;default:''"`
	ExternalUserIDFingerprint       string                              `gorm:"type:varchar(64);not null;default:'';index"`
	ContactMemberCiphertext         string                              `gorm:"type:text"`
	ContactMemberNonce              string                              `gorm:"type:varchar(128);not null;default:''"`
	ContactMemberFingerprint        string                              `gorm:"type:varchar(64);not null;default:'';index"`
	WxWorkProtocolInstanceID        int64                               `gorm:"type:bigint;not null;default:0;index"`
	CustomerID                      int64                               `gorm:"type:bigint;not null;default:0;index"`
	ConversationID                  int64                               `gorm:"type:bigint;not null;default:0;index"`
	ProtocolConversationCiphertext  string                              `gorm:"type:text"`
	ProtocolConversationNonce       string                              `gorm:"type:varchar(128);not null;default:''"`
	ProtocolConversationFingerprint string                              `gorm:"type:varchar(64);not null;default:'';index"`
	OfficialRelationStatus          enums.ArrivalOfficialRelationStatus `gorm:"type:varchar(40);not null;default:'unconfirmed';index"`
	BindingStatus                   enums.ArrivalBindingStatus          `gorm:"type:varchar(30);not null;default:'unbound';index"`
	EvidenceHash                    string                              `gorm:"type:varchar(64);not null;default:''"`
	OfficialRelationshipAt          *time.Time                          `gorm:"type:datetime;index"`
	ProtocolMappedAt                *time.Time                          `gorm:"type:datetime;index"`
	Status                          enums.Status                        `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

type WeComProviderCallbackEvent struct {
	ID               int64                       `gorm:"primaryKey;autoIncrement"`
	TenantID         int64                       `gorm:"type:bigint;not null;default:0;index"`
	StoreID          int64                       `gorm:"type:bigint;not null;default:0;index"`
	EventHash        string                      `gorm:"type:varchar(64);not null;uniqueIndex"`
	CallbackKind     string                      `gorm:"type:varchar(30);not null;default:'';index"`
	InfoType         string                      `gorm:"type:varchar(80);not null;default:'';index"`
	SuiteFingerprint string                      `gorm:"type:varchar(64);not null;default:'';index"`
	CorpFingerprint  string                      `gorm:"type:varchar(64);not null;default:'';index"`
	OccurredAt       *time.Time                  `gorm:"type:datetime;index"`
	CallbackStatus   enums.ArrivalCallbackStatus `gorm:"type:varchar(30);not null;default:'processing';index"`
	FailureCode      string                      `gorm:"type:varchar(80);not null;default:''"`
	AuditFields
}

type ArrivalAuditLog struct {
	ID           int64     `gorm:"primaryKey;autoIncrement"`
	TenantID     int64     `gorm:"type:bigint;not null;default:0;index"`
	StoreID      int64     `gorm:"type:bigint;not null;default:0;index"`
	Action       string    `gorm:"type:varchar(80);not null;default:'';index"`
	EntityType   string    `gorm:"type:varchar(80);not null;default:'';index"`
	EntityID     int64     `gorm:"type:bigint;not null;default:0;index"`
	Result       string    `gorm:"type:varchar(30);not null;default:'';index"`
	RequestID    string    `gorm:"type:varchar(128);not null;default:'';index"`
	DetailJSON   string    `gorm:"type:text"`
	OperatorID   int64     `gorm:"type:bigint;not null;default:0;index"`
	OperatorName string    `gorm:"type:varchar(100);not null;default:''"`
	CreatedAt    time.Time `gorm:"type:datetime;not null;index"`
}
