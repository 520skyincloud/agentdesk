package services

// ArrivalProtocolBindingBridge is intentionally unavailable until a provider
// publishes and AgentDesk verifies a deterministic external_userid <-> vid
// mapping. Implementations must not use names, avatars, phone numbers, recent
// contacts, timestamps, or an assumed UnionID namespace.
type ArrivalProtocolBindingBridge interface {
	Available() bool
	Resolve(ArrivalProtocolBindingRequest) (*ArrivalProtocolBindingResolution, error)
}

type ArrivalProtocolBindingRequest struct {
	TenantID                     int64
	StoreID                      int64
	StoreStaffBindingID          int64
	TenantAuthorizationID        int64
	WxWorkProtocolInstanceID     int64
	CorpID                       string
	ContactMemberUserID          string
	ExternalUserID               string
	OfficialRelationshipEvidence string
}

type ArrivalProtocolBindingResolution struct {
	WxWorkProtocolInstanceID int64
	CorpID                   string
	ExternalUserID           string
	ProtocolUserID           string
	DisplayName              string
	EvidenceType             string
	EvidenceDigest           string
}

type unavailableArrivalProtocolBindingBridge struct{}

func (unavailableArrivalProtocolBindingBridge) Available() bool {
	return false
}

func (unavailableArrivalProtocolBindingBridge) Resolve(ArrivalProtocolBindingRequest) (*ArrivalProtocolBindingResolution, error) {
	return nil, nil
}

var ArrivalBindingBridge ArrivalProtocolBindingBridge = unavailableArrivalProtocolBindingBridge{}
