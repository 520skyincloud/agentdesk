package response

import "time"

type StoreModelCredentialResponse struct {
	TenantID                   int64      `json:"tenantId"`
	StoreID                    int64      `json:"storeId"`
	StoreStaffBindingID        int64      `json:"storeStaffBindingId"`
	StoreStaffAccountName      string     `json:"storeStaffAccountName"`
	StoreCode                  string     `json:"storeCode"`
	StoreName                  string     `json:"storeName"`
	ActiveProfileID            int64      `json:"activeProfileId"`
	ActiveProfileName          string     `json:"activeProfileName"`
	ActiveProfileRevision      int64      `json:"activeProfileRevision"`
	ActiveModelNames           []string   `json:"activeModelNames"`
	PendingProfileID           int64      `json:"pendingProfileId"`
	PendingProfileName         string     `json:"pendingProfileName"`
	PendingProfileRevision     int64      `json:"pendingProfileRevision"`
	PendingModelNames          []string   `json:"pendingModelNames"`
	HasKey                     bool       `json:"hasKey"`
	KeyMask                    string     `json:"keyMask"`
	FingerprintLast6           string     `json:"fingerprintLast6"`
	CredentialRevision         int64      `json:"credentialRevision"`
	CredentialStatus           string     `json:"credentialStatus"`
	CandidateRevision          int64      `json:"candidateRevision"`
	CandidateStatus            string     `json:"candidateStatus"`
	CandidateApprovalStatus    string     `json:"candidateApprovalStatus"`
	CandidateProfileID         int64      `json:"candidateProfileId"`
	CandidateProfileRevision   int64      `json:"candidateProfileRevision"`
	CandidateFingerprintLast6  string     `json:"candidateFingerprintLast6"`
	CandidateRequestedAt       *time.Time `json:"candidateRequestedAt"`
	AllowCredentialSelfService bool       `json:"allowCredentialSelfService"`
	RequireSupervisorApproval  bool       `json:"requireSupervisorApproval"`
	CanSelfService             bool       `json:"canSelfService"`
	LastTestStatus             string     `json:"lastTestStatus"`
	LastTestedAt               *time.Time `json:"lastTestedAt"`
	LastTestLatencyMS          int64      `json:"lastTestLatencyMs"`
	LastFastGPTSyncStatus      string     `json:"lastFastGPTSyncStatus"`
	LastFastGPTSyncedAt        *time.Time `json:"lastFastGPTSyncedAt"`
	LastErrorClass             string     `json:"lastErrorClass"`
	LastErrorMessage           string     `json:"lastErrorMessage"`
}

type StoreModelCredentialAuditResponse struct {
	ID               int64     `json:"id"`
	Action           string    `json:"action"`
	Result           string    `json:"result"`
	FromRevision     int64     `json:"fromRevision"`
	ToRevision       int64     `json:"toRevision"`
	ProfileID        int64     `json:"profileId"`
	ProfileRevision  int64     `json:"profileRevision"`
	FingerprintLast6 string    `json:"fingerprintLast6"`
	OperatorName     string    `json:"operatorName"`
	OperatorRole     string    `json:"operatorRole"`
	ApproverName     string    `json:"approverName"`
	ErrorClass       string    `json:"errorClass"`
	RequestID        string    `json:"requestId"`
	ClientIP         string    `json:"clientIp"`
	CreatedAt        time.Time `json:"createdAt"`
}
