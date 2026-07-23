package request

type UpdateCustomerTagPolicyRequest struct {
	QuietPeriodMinutes            int     `json:"quietPeriodMinutes"`
	MinimumConfidence             float64 `json:"minimumConfidence"`
	MaxOperationsPerRun           int     `json:"maxOperationsPerRun"`
	EvolutionDefaultEnabled       bool    `json:"evolutionDefaultEnabled"`
	ReplyTagContextDefaultEnabled bool    `json:"replyTagContextDefaultEnabled"`
}

type BatchToggleCustomerTagRuntimeRequest struct {
	StoreIDs                    []int64 `json:"storeIds"`
	AllStores                   bool    `json:"allStores"`
	CustomerTagEvolutionEnabled *bool   `json:"customerTagEvolutionEnabled"`
	ReplyTagContextEnabled      *bool   `json:"replyTagContextEnabled"`
}
