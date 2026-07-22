package response

import "time"

type StoreModelCredentialResponse struct {
	StoreID             int64      `json:"storeId"`
	StoreName           string     `json:"storeName"`
	ProfileName         string     `json:"profileName"`
	ProfileRevision     int64      `json:"profileRevision"`
	ProfileStatus       string     `json:"profileStatus"`
	HasKey              bool       `json:"hasKey"`
	CredentialRevision  int64      `json:"credentialRevision"`
	CredentialStatus    string     `json:"credentialStatus"`
	LastTestStatus      string     `json:"lastTestStatus"`
	LastTestedAt        *time.Time `json:"lastTestedAt"`
	LastTestLatencyMS   int64      `json:"lastTestLatencyMs"`
	FastGPTSyncStatus   string     `json:"fastgptSyncStatus"`
	FastGPTLastSyncedAt *time.Time `json:"fastgptLastSyncedAt"`
}

type StoreModelCredentialUpdateResponse struct {
	StoreModelCredentialResponse
	ChangedAt time.Time `json:"changedAt"`
}

type StoreModelCredentialStoreOptionResponse struct {
	StoreID            int64  `json:"storeId"`
	StoreCode          string `json:"storeCode"`
	StoreName          string `json:"storeName"`
	CompanyID          int64  `json:"companyId"`
	HasKey             bool   `json:"hasKey"`
	CredentialRevision int64  `json:"credentialRevision"`
	CredentialStatus   string `json:"credentialStatus"`
}

type BillingTokenSummaryResponse struct {
	Name           string  `json:"name"`
	UnlimitedQuota bool    `json:"unlimitedQuota"`
	TotalGranted   int64   `json:"totalGranted"`
	TotalUsed      int64   `json:"totalUsed"`
	TotalAvailable int64   `json:"totalAvailable"`
	GrantedCNY     float64 `json:"grantedCny"`
	UsedCNY        float64 `json:"usedCny"`
	AvailableCNY   float64 `json:"availableCny"`
	ExpiresAt      int64   `json:"expiresAt"`
}

type BillingUsageLogResponse struct {
	ID               int64   `json:"id"`
	CreatedAt        int64   `json:"createdAt"`
	ModelName        string  `json:"modelName"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	UseTime          int64   `json:"useTime"`
	Quota            int64   `json:"quota"`
	CostCNY          float64 `json:"costCny"`
	RequestID        string  `json:"requestId"`
}

type BillingQueryResponse struct {
	StoreID            int64                       `json:"storeId"`
	StoreName          string                      `json:"storeName"`
	CredentialRevision int64                       `json:"credentialRevision"`
	CredentialStatus   string                      `json:"credentialStatus"`
	StartDate          string                      `json:"startDate"`
	EndDate            string                      `json:"endDate"`
	PeriodQuota        int64                       `json:"periodQuota"`
	PeriodCostCNY      float64                     `json:"periodCostCny"`
	PeriodPromptTokens int64                       `json:"periodPromptTokens"`
	PeriodOutputTokens int64                       `json:"periodOutputTokens"`
	QueriedAt          time.Time                   `json:"queriedAt"`
	Summary            BillingTokenSummaryResponse `json:"summary"`
	Logs               []BillingUsageLogResponse   `json:"logs"`
}
