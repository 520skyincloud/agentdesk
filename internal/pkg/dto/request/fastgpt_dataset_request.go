package request

type ProvisionFastGPTDatasetRequest struct {
	StoreID int64  `json:"storeId"`
	Name    string `json:"name"`
}

type FastGPTDatasetActionRequest struct {
	KnowledgeBaseID int64  `json:"knowledgeBaseId"`
	CollectionID    string `json:"collectionId"`
	Query           string `json:"query"`
}

type ActivateFastGPTKnowledgeBaseRequest struct {
	WxWorkInstanceID int64 `json:"wxWorkInstanceId"`
	KnowledgeBaseID  int64 `json:"knowledgeBaseId"`
}

type DeleteFastGPTDatasetRequest struct {
	KnowledgeBaseID  int64  `json:"knowledgeBaseId"`
	ConfirmationName string `json:"confirmationName"`
}

type FastGPTModelProfileDetailRequest struct {
	WxWorkInstanceID int64 `json:"wxWorkInstanceId"`
}

type FastGPTModelCredentialRequest struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
}

type FastGPTModelProfileRequest struct {
	WxWorkInstanceID int64                          `json:"wxWorkInstanceId"`
	ProfileID        string                         `json:"profileId"`
	Name             string                         `json:"name"`
	Embedding        FastGPTModelCredentialRequest  `json:"embedding"`
	DocumentParser   FastGPTModelCredentialRequest  `json:"documentParser"`
	Vision           FastGPTModelCredentialRequest  `json:"vision"`
	RerankEnabled    bool                           `json:"rerankEnabled"`
	Rerank           *FastGPTModelCredentialRequest `json:"rerank"`
	TestToken        string                         `json:"testToken"`
}

type FastGPTProfileTemplateCredentialRequest struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIMode  string `json:"apiMode,omitempty"`
}

type UpdateFastGPTProfileTemplateRequest struct {
	Name           string                                  `json:"name"`
	Chat           FastGPTProfileTemplateCredentialRequest `json:"chat"`
	ASR            FastGPTProfileTemplateCredentialRequest `json:"asr"`
	Embedding      FastGPTProfileTemplateCredentialRequest `json:"embedding"`
	DocumentParser FastGPTProfileTemplateCredentialRequest `json:"documentParser"`
	Vision         FastGPTProfileTemplateCredentialRequest `json:"vision"`
	Rerank         FastGPTProfileTemplateCredentialRequest `json:"rerank"`
}

type ModelProfileSlotRequest struct {
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

type UpdateModelProfileTemplateRequest struct {
	Name                         string                    `json:"name"`
	GatewayBaseURL               string                    `json:"gatewayBaseUrl"`
	CustomerTagEvolutionEnabled  bool                      `json:"customerTagEvolutionEnabled"`
	CustomerTagEvolutionStoreIDs []int64                   `json:"customerTagEvolutionStoreIds"`
	ReplyTagContextEnabled       bool                      `json:"replyTagContextEnabled"`
	Slots                        []ModelProfileSlotRequest `json:"slots"`
}

type TestModelProfileSlotRequest struct {
	StoreID   int64  `json:"storeId"`
	UsageCode string `json:"usageCode"`
}
