package request

type BillingQueryRequest struct {
	TenantID  int64   `json:"tenantId"`
	StoreIDs  []int64 `json:"storeIds"`
	StartDate string  `json:"startDate"`
	EndDate   string  `json:"endDate"`
	ModelName string  `json:"modelName"`
	RequestID string  `json:"requestId"`
	Limit     int     `json:"limit"`
}
