package response

import "time"

type ArrivalStoreResponse struct {
	Name      string `json:"name"`
	BrandName string `json:"brandName"`
	Address   string `json:"address"`
	Phone     string `json:"phone"`
}

type ArrivalContactWayResponse struct {
	Available bool   `json:"available"`
	Mode      string `json:"mode"`
	QRCodeURL string `json:"qrCodeUrl"`
	PlugID    string `json:"plugId"`
}

type ArrivalScanResultResponse struct {
	SchemaVersion  string                    `json:"schemaVersion"`
	SessionToken   string                    `json:"sessionToken"`
	Store          ArrivalStoreResponse      `json:"store"`
	IdentityStatus string                    `json:"identityStatus"`
	BindingStatus  string                    `json:"bindingStatus"`
	DeliveryStatus string                    `json:"deliveryStatus"`
	ContactWay     ArrivalContactWayResponse `json:"contactWay"`
}

type ArrivalBindResultResponse struct {
	SchemaVersion string               `json:"schemaVersion"`
	BindingStatus string               `json:"bindingStatus"`
	Store         ArrivalStoreResponse `json:"store"`
}

type ArrivalConnectionResponse struct {
	ID                        int64      `json:"id"`
	TenantID                  int64      `json:"tenantId"`
	StoreID                   int64      `json:"storeId"`
	StoreCode                 string     `json:"storeCode"`
	StoreName                 string     `json:"storeName"`
	BrandName                 string     `json:"brandName"`
	Scene                     string     `json:"scene"`
	ConnectionStatus          string     `json:"connectionStatus"`
	AuthorizationStatus       string     `json:"authorizationStatus"`
	AuthorizedCorpName        string     `json:"authorizedCorpName"`
	ContactMemberConfigured   bool       `json:"contactMemberConfigured"`
	StoreStaffBindingID       int64      `json:"storeStaffBindingId"`
	StoreStaffAccountName     string     `json:"storeStaffAccountName"`
	WxWorkProtocolInstanceID  int64      `json:"wxWorkProtocolInstanceId"`
	WxWorkProtocolAccountName string     `json:"wxWorkProtocolAccountName"`
	WxWorkProtocolHealth      string     `json:"wxWorkProtocolHealth"`
	LastVerifiedAt            *time.Time `json:"lastVerifiedAt,omitempty"`
	LastErrorCode             string     `json:"lastErrorCode"`
	RecentScanCount           int64      `json:"recentScanCount"`
	RecentBoundCount          int64      `json:"recentBoundCount"`
	ContactProvider           string     `json:"contactProvider"`
	StaticContactPlugID       string     `json:"staticContactPlugId"`
	AcquisitionLinkStatus     string     `json:"acquisitionLinkStatus"`
	AcquisitionQuotaTotal     int64      `json:"acquisitionQuotaTotal"`
	AcquisitionQuotaBalance   int64      `json:"acquisitionQuotaBalance"`
	AcquisitionFailureCode    string     `json:"acquisitionFailureCode"`
	AcquisitionLastVerifiedAt *time.Time `json:"acquisitionLastVerifiedAt,omitempty"`
	UpdatedAt                 time.Time  `json:"updatedAt"`
}

type ArrivalProtocolInstanceOptionResponse struct {
	ID                    int64  `json:"id"`
	Name                  string `json:"name"`
	HealthStatus          string `json:"healthStatus"`
	StoreID               int64  `json:"storeId"`
	StoreStaffBindingID   int64  `json:"storeStaffBindingId"`
	StoreStaffAccountName string `json:"storeStaffAccountName"`
}

type ArrivalInvitationResponse struct {
	InvitationURL string    `json:"invitationUrl"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type ArrivalProviderInvitationResponse struct {
	Valid            bool      `json:"valid"`
	StoreName        string    `json:"storeName"`
	BrandName        string    `json:"brandName"`
	ConnectionStatus string    `json:"connectionStatus"`
	Authorized       bool      `json:"authorized"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type ArrivalAuthorizationBeginResponse struct {
	AuthorizationURL   string `json:"authorizationUrl"`
	AuthorizationState string `json:"authorizationState"`
	AlreadyAuthorized  bool   `json:"alreadyAuthorized"`
}

type ArrivalProviderOptionResponse struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type ArrivalProviderInstanceOptionResponse struct {
	ID                    int64  `json:"id"`
	Name                  string `json:"name"`
	HealthStatus          string `json:"healthStatus"`
	BoundStoreID          int64  `json:"boundStoreId"`
	StoreStaffBindingID   int64  `json:"storeStaffBindingId"`
	StoreStaffAccountName string `json:"storeStaffAccountName"`
}

type ArrivalProviderOptionsResponse struct {
	StoreName        string                                  `json:"storeName"`
	ConnectionStatus string                                  `json:"connectionStatus"`
	Members          []ArrivalProviderOptionResponse         `json:"members"`
	Instances        []ArrivalProviderInstanceOptionResponse `json:"instances"`
}

type ArrivalAuthorizationOptionResponse struct {
	ID       int64  `json:"id"`
	CorpName string `json:"corpName"`
	Status   string `json:"status"`
}

type ArrivalConnectionVerificationResponse struct {
	ConnectionStatus string `json:"connectionStatus"`
	AuthorizationOK  bool   `json:"authorizationOk"`
	MemberOK         bool   `json:"memberOk"`
	InstanceOK       bool   `json:"instanceOk"`
	ProviderMode     string `json:"providerMode"`
	ProviderOK       bool   `json:"providerOk"`
	QuotaTotal       int64  `json:"quotaTotal"`
	QuotaBalance     int64  `json:"quotaBalance"`
	ErrorCode        string `json:"errorCode"`
}

type ArrivalAuditLogResponse struct {
	ID           int64     `json:"id"`
	StoreID      int64     `json:"storeId"`
	Action       string    `json:"action"`
	EntityType   string    `json:"entityType"`
	EntityID     int64     `json:"entityId"`
	Result       string    `json:"result"`
	DetailJSON   string    `json:"detailJson"`
	OperatorName string    `json:"operatorName"`
	CreatedAt    time.Time `json:"createdAt"`
}
