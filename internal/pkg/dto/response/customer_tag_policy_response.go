package response

type CustomerTagPolicyResponse struct {
	TenantID                      int64   `json:"tenantId"`
	IntentProfileID               int64   `json:"intentProfileId"`
	IndustryName                  string  `json:"industryName"`
	IndustryCode                  string  `json:"industryCode"`
	QuietPeriodMinutes            int     `json:"quietPeriodMinutes"`
	MinimumConfidence             float64 `json:"minimumConfidence"`
	MaxOperationsPerRun           int     `json:"maxOperationsPerRun"`
	EvolutionDefaultEnabled       bool    `json:"evolutionDefaultEnabled"`
	ReplyTagContextDefaultEnabled bool    `json:"replyTagContextDefaultEnabled"`
	UpdatedAt                     string  `json:"updatedAt"`
}

type StoreCustomerTagRuntimePolicyResponse struct {
	StoreID                     int64  `json:"storeId"`
	StoreCode                   string `json:"storeCode"`
	StoreName                   string `json:"storeName"`
	StoreStatus                 int    `json:"storeStatus"`
	PolicyReady                 bool   `json:"policyReady"`
	CustomerTagEvolutionEnabled bool   `json:"customerTagEvolutionEnabled"`
	ReplyTagContextEnabled      bool   `json:"replyTagContextEnabled"`
	UpdatedAt                   string `json:"updatedAt"`
}

type BatchToggleCustomerTagRuntimeResponse struct {
	AffectedStoreCount int `json:"affectedStoreCount"`
}
