package enums

type ArrivalIdentityStatus string

const (
	ArrivalIdentityStatusMatched ArrivalIdentityStatus = "matched"
	ArrivalIdentityStatusCreated ArrivalIdentityStatus = "created"
)

type ArrivalBindingStatus string

const (
	ArrivalBindingStatusBound          ArrivalBindingStatus = "bound"
	ArrivalBindingStatusUnbound        ArrivalBindingStatus = "unbound"
	ArrivalBindingStatusLegacyUnmapped ArrivalBindingStatus = "legacy_unmapped"
)

type ArrivalOfficialRelationStatus string

const (
	ArrivalOfficialRelationStatusUnconfirmed ArrivalOfficialRelationStatus = "unconfirmed"
	ArrivalOfficialRelationStatusConfirmed   ArrivalOfficialRelationStatus = "official_relation_confirmed"
	ArrivalOfficialRelationStatusRevoked     ArrivalOfficialRelationStatus = "revoked"
)

type ArrivalDeliveryStatus string

const (
	ArrivalDeliveryStatusSent            ArrivalDeliveryStatus = "sent"
	ArrivalDeliveryStatusRateLimited     ArrivalDeliveryStatus = "rate_limited"
	ArrivalDeliveryStatusNotBound        ArrivalDeliveryStatus = "not_bound"
	ArrivalDeliveryStatusInstanceOffline ArrivalDeliveryStatus = "instance_offline"
	ArrivalDeliveryStatusFailed          ArrivalDeliveryStatus = "failed"
)

type ArrivalContactWayMode string

const (
	ArrivalContactWayModeQRCode       ArrivalContactWayMode = "qr_code"
	ArrivalContactWayModePluginButton ArrivalContactWayMode = "plugin_button"
	ArrivalContactWayModeNone         ArrivalContactWayMode = "none"
)

type ArrivalConnectionStatus string

const (
	ArrivalConnectionStatusPendingAuthorization ArrivalConnectionStatus = "pending_authorization"
	ArrivalConnectionStatusPendingBinding       ArrivalConnectionStatus = "pending_binding"
	ArrivalConnectionStatusActive               ArrivalConnectionStatus = "active"
	ArrivalConnectionStatusDisabled             ArrivalConnectionStatus = "disabled"
	ArrivalConnectionStatusInvalid              ArrivalConnectionStatus = "invalid"
)

type WeComAuthorizationStatus string

const (
	WeComAuthorizationStatusPending WeComAuthorizationStatus = "pending"
	WeComAuthorizationStatusActive  WeComAuthorizationStatus = "active"
	WeComAuthorizationStatusRevoked WeComAuthorizationStatus = "revoked"
	WeComAuthorizationStatusInvalid WeComAuthorizationStatus = "invalid"
)

type ArrivalContactWayStatus string

const (
	ArrivalContactWayStatusProvisioning ArrivalContactWayStatus = "provisioning"
	ArrivalContactWayStatusActive       ArrivalContactWayStatus = "active"
	ArrivalContactWayStatusFailed       ArrivalContactWayStatus = "failed"
	ArrivalContactWayStatusExpired      ArrivalContactWayStatus = "expired"
	ArrivalContactWayStatusCleaned      ArrivalContactWayStatus = "cleaned"
)

type ArrivalCallbackStatus string

const (
	ArrivalCallbackStatusProcessing ArrivalCallbackStatus = "processing"
	ArrivalCallbackStatusProcessed  ArrivalCallbackStatus = "processed"
	ArrivalCallbackStatusIgnored    ArrivalCallbackStatus = "ignored"
	ArrivalCallbackStatusFailed     ArrivalCallbackStatus = "failed"
)
