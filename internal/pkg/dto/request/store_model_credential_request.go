package request

type StoreModelCredentialRequest struct {
	StoreID int64 `json:"storeId"`
}

type UpdateStoreModelCredentialRequest struct {
	StoreID int64  `json:"storeId"`
	APIKey  string `json:"apiKey"`
}

type BillingQueryRequest struct {
	StoreID   int64  `json:"storeId"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}
