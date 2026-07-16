package request

type TenantAIModelDefaultRequest struct {
	UsageCode  string `json:"usageCode"`
	AIConfigID int64  `json:"aiConfigId"`
}

type UpdateTenantAIModelAccessRequest struct {
	TenantID           int64                         `json:"tenantId"`
	GrantedAIConfigIDs []int64                       `json:"grantedAiConfigIds"`
	Defaults           []TenantAIModelDefaultRequest `json:"defaults"`
}

type UpdateTenantAIModelAssignmentsRequest struct {
	TenantID         int64                         `json:"tenantId"`
	WxWorkInstanceID int64                         `json:"wxWorkInstanceId"`
	Assignments      []TenantAIModelDefaultRequest `json:"assignments"`
}
