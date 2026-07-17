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
