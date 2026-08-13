package request

// SetKnowledgeActionBindingRequest 绑定一条知识记录到动作。
type SetKnowledgeActionBindingRequest struct {
	TenantID        int64  `json:"tenantId"`
	StoreID         int64  `json:"storeId"`
	KnowledgeBaseID int64  `json:"knowledgeBaseId"`
	SourceRecordID  string `json:"sourceRecordId"`
	ActionCode      string `json:"actionCode"`
	Enabled         bool   `json:"enabled"`
}

// DeleteKnowledgeActionBindingRequest 删除绑定。
type DeleteKnowledgeActionBindingRequest struct {
	ID int64 `json:"id"`
}
