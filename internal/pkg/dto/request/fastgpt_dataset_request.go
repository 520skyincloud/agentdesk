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
