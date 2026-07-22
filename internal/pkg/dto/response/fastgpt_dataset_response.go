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

type FastGPTProfileTemplateCredentialResponse struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIMode  string `json:"apiMode,omitempty"`
}

type FastGPTProfileTemplateStoreSyncResponse struct {
	StoreID         int64      `json:"storeId"`
	StoreName       string     `json:"storeName"`
	ProfileName     string     `json:"profileName"`
	ProfileRevision string     `json:"profileRevision"`
	TargetRevision  int64      `json:"targetRevision"`
	Status          string     `json:"status"`
	LastError       string     `json:"lastError"`
	LastSyncedAt    *time.Time `json:"lastSyncedAt"`
}

type FastGPTProfileTemplateSyncSummaryResponse struct {
	Total   int `json:"total"`
	Pending int `json:"pending"`
	Ready   int `json:"ready"`
	Failed  int `json:"failed"`
	Blocked int `json:"blocked"`
}

type FastGPTProfileTemplateResponse struct {
	ID             int64                                     `json:"id"`
	Name           string                                    `json:"name"`
	Revision       int64                                     `json:"revision"`
	Status         string                                    `json:"status"`
	Chat           FastGPTProfileTemplateCredentialResponse  `json:"chat"`
	ASR            FastGPTProfileTemplateCredentialResponse  `json:"asr"`
	Embedding      FastGPTProfileTemplateCredentialResponse  `json:"embedding"`
	DocumentParser FastGPTProfileTemplateCredentialResponse  `json:"documentParser"`
	Vision         FastGPTProfileTemplateCredentialResponse  `json:"vision"`
	Rerank         FastGPTProfileTemplateCredentialResponse  `json:"rerank"`
	Sync           FastGPTProfileTemplateSyncSummaryResponse `json:"sync"`
	Stores         []FastGPTProfileTemplateStoreSyncResponse `json:"stores"`
	UpdatedAt      time.Time                                 `json:"updatedAt"`
}

type ModelProfileSlotResponse struct {
	ID               int64   `json:"id"`
	UsageCode        string  `json:"usageCode"`
	DisplayName      string  `json:"displayName"`
	ModelType        string  `json:"modelType"`
	Provider         string  `json:"provider"`
	ModelName        string  `json:"modelName"`
	APIMode          string  `json:"apiMode"`
	Dimension        int     `json:"dimension"`
	MaxContextTokens int     `json:"maxContextTokens"`
	MaxOutputTokens  int     `json:"maxOutputTokens"`
	TimeoutMS        int     `json:"timeoutMs"`
	MaxRetryCount    int     `json:"maxRetryCount"`
	Temperature      float64 `json:"temperature"`
	SchemaVersion    string  `json:"schemaVersion"`
	PromptTemplate   string  `json:"promptTemplate"`
	JSONSchema       string  `json:"jsonSchema"`
	Enabled          bool    `json:"enabled"`
	SortNo           int     `json:"sortNo"`
}

type ModelProfileTemplateResponse struct {
	ID                           int64                      `json:"id"`
	Name                         string                     `json:"name"`
	Revision                     int64                      `json:"revision"`
	GatewayBaseURL               string                     `json:"gatewayBaseUrl"`
	CustomerTagEvolutionEnabled  bool                       `json:"customerTagEvolutionEnabled"`
	CustomerTagEvolutionStoreIDs []int64                    `json:"customerTagEvolutionStoreIds"`
	ReplyTagContextEnabled       bool                       `json:"replyTagContextEnabled"`
	Status                       string                     `json:"status"`
	Slots                        []ModelProfileSlotResponse `json:"slots"`
	UpdatedAt                    time.Time                  `json:"updatedAt"`
}

type TestModelProfileSlotResponse struct {
	StoreID            int64  `json:"storeId"`
	UsageCode          string `json:"usageCode"`
	ModelName          string `json:"modelName"`
	TemplateRevision   int64  `json:"templateRevision"`
	CredentialRevision int64  `json:"credentialRevision"`
	Status             string `json:"status"`
	LatencyMS          int64  `json:"latencyMs"`
}
