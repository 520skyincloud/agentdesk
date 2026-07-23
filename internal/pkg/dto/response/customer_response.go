package response

import "agent-desk/internal/pkg/enums"

type CustomerResponse struct {
	ID             int64                           `json:"id"`
	Name           string                          `json:"name"`
	Avatar         string                          `json:"avatar"`
	Gender         enums.Gender                    `json:"gender"`
	LastActiveAt   string                          `json:"lastActiveAt"`
	PrimaryMobile  string                          `json:"primaryMobile"`
	PrimaryEmail   string                          `json:"primaryEmail"`
	Status         enums.Status                    `json:"status"`
	Remark         string                          `json:"remark"`
	StoreRelations []StoreCustomerRelationResponse `json:"storeRelations"`
	CreatedAt      string                          `json:"createdAt"`
	UpdatedAt      string                          `json:"updatedAt"`
}

type StoreCustomerRelationResponse struct {
	ID                 int64                 `json:"id"`
	CustomerID         int64                 `json:"customerId"`
	StoreID            int64                 `json:"storeId"`
	StoreName          string                `json:"storeName"`
	WxWorkInstanceID   int64                 `json:"wxWorkInstanceId"`
	WxWorkInstanceName string                `json:"wxWorkInstanceName"`
	LastConversationID int64                 `json:"lastConversationId"`
	LastActiveAt       string                `json:"lastActiveAt"`
	VisitCount         int                   `json:"visitCount"`
	Tags               string                `json:"tags"`
	StableNotes        string                `json:"stableNotes"`
	CustomerTags       []CustomerTagResponse `json:"customerTags"`
	Status             enums.Status          `json:"status"`
	CreatedAt          string                `json:"createdAt"`
	UpdatedAt          string                `json:"updatedAt"`
}

type StoreCustomerTagDecisionResponse struct {
	ID                    int64                                   `json:"id"`
	TenantID              int64                                   `json:"tenantId"`
	CustomerID            int64                                   `json:"customerId"`
	SourceStoreID         int64                                   `json:"sourceStoreId"`
	SourceStoreRelationID int64                                   `json:"sourceStoreRelationId"`
	TargetStoreID         int64                                   `json:"targetStoreId"`
	TargetStoreRelationID int64                                   `json:"targetStoreRelationId"`
	Strategy              enums.StoreCustomerTagReconcileStrategy `json:"strategy"`
	SourceTagIDs          []int64                                 `json:"sourceTagIds"`
	TargetBeforeTagIDs    []int64                                 `json:"targetBeforeTagIds"`
	TargetAfterTagIDs     []int64                                 `json:"targetAfterTagIds"`
	OperatorID            int64                                   `json:"operatorId"`
	OperatorName          string                                  `json:"operatorName"`
	CreatedAt             string                                  `json:"createdAt"`
}
