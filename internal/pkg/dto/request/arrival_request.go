package request

type ArrivalBootstrapRequest struct {
	SchemaVersion string `json:"schemaVersion" binding:"required"`
	LoginCode     string `json:"loginCode" binding:"required"`
	Scene         string `json:"scene" binding:"required"`
	ScanEventID   string `json:"scanEventId" binding:"required"`
}

type ArrivalBindRequest struct {
	SchemaVersion string `json:"schemaVersion" binding:"required"`
	LoginCode     string `json:"loginCode" binding:"required"`
	BindTicket    string `json:"bindTicket" binding:"required"`
}

type CreateArrivalInvitationRequest struct {
	StoreID               int64 `json:"storeId" binding:"required"`
	TenantAuthorizationID int64 `json:"tenantAuthorizationId"`
}

type UpdateArrivalConnectionProviderRequest struct {
	StoreID                  int64  `json:"storeId" binding:"required"`
	ContactProvider          string `json:"contactProvider" binding:"required"`
	StaticContactPlugID      string `json:"staticContactPlugId"`
	WxWorkProtocolInstanceID int64  `json:"wxWorkProtocolInstanceId"`
}

type SendArrivalBindingCardRequest struct {
	ConversationID int64 `json:"conversationId" binding:"required"`
}

type DisableArrivalConnectionRequest struct {
	ConnectionID int64  `json:"connectionId" binding:"required"`
	Reason       string `json:"reason"`
}

type VerifyArrivalConnectionRequest struct {
	ConnectionID int64 `json:"connectionId" binding:"required"`
}

type BeginWeComProviderAuthorizationRequest struct {
	InvitationToken string `json:"invitationToken" binding:"required"`
}

type CompleteArrivalConnectionRequest struct {
	AuthorizationState       string `json:"authorizationState" binding:"required"`
	ContactMemberToken       string `json:"contactMemberToken" binding:"required"`
	WxWorkProtocolInstanceID int64  `json:"wxWorkProtocolInstanceId" binding:"required"`
}
