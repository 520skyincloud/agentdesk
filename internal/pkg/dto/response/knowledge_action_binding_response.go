package response

// KnowledgeActionBindingResponse 知识记录动作绑定。
type KnowledgeActionBindingResponse struct {
	ID              int64  `json:"id"`
	TenantID        int64  `json:"tenantId"`
	StoreID         int64  `json:"storeId"`
	KnowledgeBaseID int64  `json:"knowledgeBaseId"`
	SourceRecordID  string `json:"sourceRecordId"`
	ActionCode      string `json:"actionCode"`
	Enabled         bool   `json:"enabled"`
	SortNo          int    `json:"sortNo"`
	Remark          string `json:"remark"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	CreateUserName  string `json:"createUserName"`
	UpdateUserName  string `json:"updateUserName"`
}
