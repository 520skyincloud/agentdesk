package response

import "time"

type FastGPTDatasetJobResponse struct {
	ID              int64      `json:"id"`
	StoreID         int64      `json:"storeId"`
	KnowledgeBaseID int64      `json:"knowledgeBaseId"`
	Action          string     `json:"action"`
	Status          string     `json:"status"`
	DatasetID       string     `json:"datasetId"`
	CollectionID    string     `json:"collectionId"`
	Filename        string     `json:"filename"`
	AttemptCount    int        `json:"attemptCount"`
	NextRetryAt     *time.Time `json:"nextRetryAt"`
	LastError       string     `json:"lastError"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type FastGPTCollectionResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	DataAmount     int    `json:"dataAmount"`
	TrainingAmount int    `json:"trainingAmount"`
	Forbid         bool   `json:"forbid"`
}

type FastGPTModelCredentialResponse struct {
	Provider       string `json:"provider"`
	BaseURL        string `json:"baseUrl"`
	Model          string `json:"model"`
	KeyConfigured  bool   `json:"keyConfigured"`
	KeyFingerprint string `json:"keyFingerprint"`
}

type FastGPTModelProfileResponse struct {
	ID             string                          `json:"id"`
	Name           string                          `json:"name"`
	Revision       int64                           `json:"revision"`
	Status         string                          `json:"status"`
	Embedding      FastGPTModelCredentialResponse  `json:"embedding"`
	DocumentParser FastGPTModelCredentialResponse  `json:"documentParser"`
	Vision         FastGPTModelCredentialResponse  `json:"vision"`
	Rerank         *FastGPTModelCredentialResponse `json:"rerank,omitempty"`
}

type FastGPTModelProfileTestStageResponse struct {
	Stage            string `json:"stage"`
	Status           string `json:"status"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
}

type FastGPTModelProfileTestResponse struct {
	TestToken string                                 `json:"testToken"`
	ExpiresAt time.Time                              `json:"expiresAt"`
	Results   []FastGPTModelProfileTestStageResponse `json:"results"`
}

type FastGPTModelProfileSaveResponse struct {
	Profile           FastGPTModelProfileResponse `json:"profile"`
	BoundDatasetCount int64                       `json:"boundDatasetCount"`
}
