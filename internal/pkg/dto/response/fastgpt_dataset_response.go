package response

import "time"

type FastGPTDatasetJobResponse struct {
	ID                        int64      `json:"id"`
	StoreID                   int64      `json:"storeId"`
	TargetStoreStaffBindingID int64      `json:"targetStoreStaffBindingId"`
	KnowledgeBaseID           int64      `json:"knowledgeBaseId"`
	Action                    string     `json:"action"`
	Status                    string     `json:"status"`
	DatasetID                 string     `json:"datasetId"`
	CollectionID              string     `json:"collectionId"`
	Filename                  string     `json:"filename"`
	AttemptCount              int        `json:"attemptCount"`
	TargetProfileID           int64      `json:"targetProfileId"`
	TargetProfileRevision     int64      `json:"targetProfileRevision"`
	TargetCredentialRevision  int64      `json:"targetCredentialRevision"`
	NextRetryAt               *time.Time `json:"nextRetryAt"`
	LastError                 string     `json:"lastError"`
	LastErrorClass            string     `json:"lastErrorClass"`
	CreatedAt                 time.Time  `json:"createdAt"`
	UpdatedAt                 time.Time  `json:"updatedAt"`
}

type FastGPTCollectionResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	DataAmount     int    `json:"dataAmount"`
	TrainingAmount int    `json:"trainingAmount"`
	Forbid         bool   `json:"forbid"`
}

type FastGPTStoreReadinessResponse struct {
	StoreID                    int64      `json:"storeId"`
	KnowledgeBaseID            int64      `json:"knowledgeBaseId"`
	TeamStatus                 string     `json:"teamStatus"`
	ReadinessStatus            string     `json:"readinessStatus"`
	ModelProfileName           string     `json:"modelProfileName"`
	TargetProfileID            int64      `json:"targetProfileId"`
	TargetProfileRevision      int64      `json:"targetProfileRevision"`
	AppliedProfileID           int64      `json:"appliedProfileId"`
	AppliedProfileRevision     int64      `json:"appliedProfileRevision"`
	TargetStoreStaffBindingID  int64      `json:"targetStoreStaffBindingId"`
	AppliedStoreStaffBindingID int64      `json:"appliedStoreStaffBindingId"`
	TargetCredentialRevision   int64      `json:"targetCredentialRevision"`
	AppliedCredentialRevision  int64      `json:"appliedCredentialRevision"`
	LastSyncedAt               *time.Time `json:"lastSyncedAt"`
	LastErrorClass             string     `json:"lastErrorClass"`
}
