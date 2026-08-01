package request

type ProvisionFastGPTDatasetRequest struct {
	StoreID             int64  `json:"storeId"`
	StoreStaffBindingID int64  `json:"storeStaffBindingId"`
	Name                string `json:"name"`
}

type AdoptFastGPTDatasetRequest struct {
	StoreID             int64  `json:"storeId"`
	DatasetID           string `json:"datasetId"`
	ExpectedDatasetName string `json:"expectedDatasetName"`
	VerificationQuery   string `json:"verificationQuery"`
}

type FastGPTDatasetActionRequest struct {
	KnowledgeBaseID int64  `json:"knowledgeBaseId"`
	CollectionID    string `json:"collectionId"`
	Query           string `json:"query"`
}

type ActivateFastGPTKnowledgeBaseRequest struct {
	StoreID         int64 `json:"storeId"`
	KnowledgeBaseID int64 `json:"knowledgeBaseId"`
}

type DeleteFastGPTDatasetRequest struct {
	KnowledgeBaseID  int64  `json:"knowledgeBaseId"`
	ConfirmationName string `json:"confirmationName"`
}

type FastGPTStoreActionRequest struct {
	StoreID int64 `json:"storeId"`
}
