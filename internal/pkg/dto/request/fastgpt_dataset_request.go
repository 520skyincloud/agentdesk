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
