package request

type GetStoreModelCredentialRequest struct {
	TenantID            int64 `json:"tenantId"`
	StoreID             int64 `json:"storeId"`
	StoreStaffBindingID int64 `json:"storeStaffBindingId"`
}

type SubmitStoreModelCredentialRequest struct {
	TenantID            int64  `json:"tenantId"`
	StoreID             int64  `json:"storeId"`
	StoreStaffBindingID int64  `json:"storeStaffBindingId"`
	APIKey              string `json:"apiKey"`
	CurrentPassword     string `json:"currentPassword"`
	Confirmed           bool   `json:"confirmed"`
}

type SubmitSelfStoreModelCredentialRequest struct {
	APIKey          string `json:"apiKey"`
	CurrentPassword string `json:"currentPassword"`
	Confirmed       bool   `json:"confirmed"`
}

type DecideStoreModelCredentialRequest struct {
	TenantID            int64  `json:"tenantId"`
	StoreID             int64  `json:"storeId"`
	StoreStaffBindingID int64  `json:"storeStaffBindingId"`
	CandidateRevision   int64  `json:"candidateRevision"`
	CurrentPassword     string `json:"currentPassword"`
	Confirmed           bool   `json:"confirmed"`
}

type UpdateStoreCredentialPolicyRequest struct {
	TenantID                   int64   `json:"tenantId"`
	StoreIDs                   []int64 `json:"storeIds"`
	AllowCredentialSelfService bool    `json:"allowCredentialSelfService"`
	RequireSupervisorApproval  bool    `json:"requireSupervisorApproval"`
	CurrentPassword            string  `json:"currentPassword"`
	Confirmed                  bool    `json:"confirmed"`
}

type GetStoreModelCredentialAuditRequest struct {
	TenantID            int64 `json:"tenantId"`
	StoreID             int64 `json:"storeId"`
	StoreStaffBindingID int64 `json:"storeStaffBindingId"`
	Limit               int   `json:"limit"`
}
