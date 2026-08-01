package response

import "time"

type BillingQueryOptionsResponse struct {
	ScopeMode                  string                        `json:"scopeMode"`
	CanFilterTenants           bool                          `json:"canFilterTenants"`
	DefaultTenantID            int64                         `json:"defaultTenantId"`
	DefaultStoreID             int64                         `json:"defaultStoreId"`
	DefaultStoreStaffBindingID int64                         `json:"defaultStoreStaffBindingId"`
	Tenants                    []BillingTenantOptionResponse `json:"tenants"`
	Stores                     []BillingStoreOptionResponse  `json:"stores"`
}

type BillingTenantOptionResponse struct {
	TenantID   int64  `json:"tenantId"`
	TenantCode string `json:"tenantCode"`
	TenantName string `json:"tenantName"`
}

type BillingStoreOptionResponse struct {
	TenantID             int64    `json:"tenantId"`
	TenantName           string   `json:"tenantName"`
	StoreID              int64    `json:"storeId"`
	StoreCode            string   `json:"storeCode"`
	StoreName            string   `json:"storeName"`
	BindingCount         int      `json:"bindingCount"`
	CredentialStatus     string   `json:"credentialStatus"`
	CredentialRevision   int64    `json:"credentialRevision"`
	ModelProfileRevision int64    `json:"modelProfileRevision"`
	ModelNames           []string `json:"modelNames"`
}

type BillingTokenSummaryResponse struct {
	UnlimitedQuota bool    `json:"unlimitedQuota"`
	TotalGranted   int64   `json:"totalGranted"`
	TotalUsed      int64   `json:"totalUsed"`
	TotalAvailable int64   `json:"totalAvailable"`
	GrantedCNY     float64 `json:"grantedCny"`
	UsedCNY        float64 `json:"usedCny"`
	AvailableCNY   float64 `json:"availableCny"`
	ExpiresAt      int64   `json:"expiresAt"`
}

type BillingOfficialUsageLogResponse struct {
	StoreID               int64   `json:"storeId"`
	StoreName             string  `json:"storeName"`
	StoreStaffBindingID   int64   `json:"storeStaffBindingId"`
	StoreStaffAccountName string  `json:"storeStaffAccountName"`
	ID                    int64   `json:"id"`
	CreatedAt             int64   `json:"createdAt"`
	ModelName             string  `json:"modelName"`
	PromptTokens          int64   `json:"promptTokens"`
	CompletionTokens      int64   `json:"completionTokens"`
	UseTime               int64   `json:"useTime"`
	Quota                 int64   `json:"quota"`
	CostCNY               float64 `json:"costCny"`
	RequestID             string  `json:"requestId"`
}

type BillingOfficialStoreResponse struct {
	TenantID              int64                             `json:"tenantId"`
	TenantName            string                            `json:"tenantName"`
	StoreID               int64                             `json:"storeId"`
	StoreCode             string                            `json:"storeCode"`
	StoreName             string                            `json:"storeName"`
	StoreStaffBindingID   int64                             `json:"storeStaffBindingId"`
	StoreStaffAccountName string                            `json:"storeStaffAccountName"`
	CredentialRevision    int64                             `json:"credentialRevision"`
	ModelProfileRevision  int64                             `json:"modelProfileRevision"`
	ModelNames            []string                          `json:"modelNames"`
	Status                string                            `json:"status"`
	ErrorClass            string                            `json:"errorClass"`
	ErrorMessage          string                            `json:"errorMessage"`
	Truncated             bool                              `json:"truncated"`
	PeriodLogCount        int                               `json:"periodLogCount"`
	PeriodQuota           int64                             `json:"periodQuota"`
	PeriodCostCNY         float64                           `json:"periodCostCny"`
	PeriodPromptTokens    int64                             `json:"periodPromptTokens"`
	PeriodOutputTokens    int64                             `json:"periodOutputTokens"`
	Summary               BillingTokenSummaryResponse       `json:"summary"`
	Logs                  []BillingOfficialUsageLogResponse `json:"logs"`
}

type BillingOfficialAggregateResponse struct {
	StoreCount                   int     `json:"storeCount"`
	SuccessfulStores             int     `json:"successfulStores"`
	FailedStores                 int     `json:"failedStores"`
	CredentialAccountCount       int     `json:"credentialAccountCount"`
	SuccessfulCredentialAccounts int     `json:"successfulCredentialAccounts"`
	FailedCredentialAccounts     int     `json:"failedCredentialAccounts"`
	LogCount                     int     `json:"logCount"`
	PeriodQuota                  int64   `json:"periodQuota"`
	PeriodCostCNY                float64 `json:"periodCostCny"`
	PeriodPromptTokens           int64   `json:"periodPromptTokens"`
	PeriodOutputTokens           int64   `json:"periodOutputTokens"`
}

type BillingOfficialSectionResponse struct {
	Aggregate BillingOfficialAggregateResponse `json:"aggregate"`
	Stores    []BillingOfficialStoreResponse   `json:"stores"`
}

type BillingLocalUsageEventResponse struct {
	ID                    int64     `json:"id"`
	TenantID              int64     `json:"tenantId"`
	TenantName            string    `json:"tenantName"`
	StoreID               int64     `json:"storeId"`
	StoreName             string    `json:"storeName"`
	StoreStaffBindingID   int64     `json:"storeStaffBindingId"`
	StoreStaffAccountName string    `json:"storeStaffAccountName"`
	RequestID             string    `json:"requestId"`
	Stage                 string    `json:"stage"`
	OperationType         string    `json:"operationType"`
	ModelName             string    `json:"modelName"`
	ModelProfileRevision  int64     `json:"modelProfileRevision"`
	UsageSlot             string    `json:"usageSlot"`
	CredentialRevision    int64     `json:"credentialRevision"`
	PromptTokens          int64     `json:"promptTokens"`
	CompletionTokens      int64     `json:"completionTokens"`
	CachedPromptTokens    int64     `json:"cachedPromptTokens"`
	LatencyMS             int64     `json:"latencyMs"`
	Status                string    `json:"status"`
	ErrorClass            string    `json:"errorClass"`
	CreatedAt             time.Time `json:"createdAt"`
}

type BillingLocalAggregateResponse struct {
	EventCount         int64 `json:"eventCount"`
	RequestCount       int64 `json:"requestCount"`
	FailedCount        int64 `json:"failedCount"`
	PromptTokens       int64 `json:"promptTokens"`
	CompletionTokens   int64 `json:"completionTokens"`
	CachedPromptTokens int64 `json:"cachedPromptTokens"`
}

type BillingLocalSectionResponse struct {
	Aggregate BillingLocalAggregateResponse    `json:"aggregate"`
	Events    []BillingLocalUsageEventResponse `json:"events"`
	Truncated bool                             `json:"truncated"`
}

type BillingReconciliationItemResponse struct {
	StoreID               int64      `json:"storeId"`
	StoreName             string     `json:"storeName"`
	StoreStaffBindingID   int64      `json:"storeStaffBindingId"`
	StoreStaffAccountName string     `json:"storeStaffAccountName"`
	RequestID             string     `json:"requestId"`
	Status                string     `json:"status"`
	OfficialModel         string     `json:"officialModel"`
	LocalModel            string     `json:"localModel"`
	OfficialTokens        int64      `json:"officialTokens"`
	LocalTokens           int64      `json:"localTokens"`
	OfficialCostCNY       float64    `json:"officialCostCny"`
	OfficialAt            *time.Time `json:"officialAt"`
	LocalAt               *time.Time `json:"localAt"`
}

type BillingReconciliationResponse struct {
	OfficialLogCount      int                                 `json:"officialLogCount"`
	LocalGatewayCallCount int                                 `json:"localGatewayCallCount"`
	MatchedCount          int                                 `json:"matchedCount"`
	OfficialOnlyCount     int                                 `json:"officialOnlyCount"`
	LocalOnlyCount        int                                 `json:"localOnlyCount"`
	MissingRequestIDCount int                                 `json:"missingRequestIdCount"`
	MatchRate             float64                             `json:"matchRate"`
	Items                 []BillingReconciliationItemResponse `json:"items"`
	Truncated             bool                                `json:"truncated"`
}

type BillingQueryResponse struct {
	ScopeMode        string                         `json:"scopeMode"`
	TenantID         int64                          `json:"tenantId"`
	TenantName       string                         `json:"tenantName"`
	StartDate        string                         `json:"startDate"`
	EndDate          string                         `json:"endDate"`
	BusinessTimezone string                         `json:"businessTimezone"`
	QueriedAt        time.Time                      `json:"queriedAt"`
	Official         BillingOfficialSectionResponse `json:"official"`
	Local            BillingLocalSectionResponse    `json:"local"`
	Reconciliation   BillingReconciliationResponse  `json:"reconciliation"`
}
