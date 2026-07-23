package request

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

type GetModelProfileCatalogRequest struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
}

type CreateModelProfileRequest struct {
	SourceTemplateID int64                     `json:"sourceTemplateId"`
	Code             string                    `json:"code"`
	Name             string                    `json:"name"`
	Description      string                    `json:"description"`
	GatewayBaseURL   string                    `json:"gatewayBaseUrl"`
	Slots            []ModelProfileSlotRequest `json:"slots"`
}

type UpdateModelProfileRequest struct {
	ID             int64                     `json:"id"`
	Name           string                    `json:"name"`
	Description    string                    `json:"description"`
	GatewayBaseURL string                    `json:"gatewayBaseUrl"`
	Slots          []ModelProfileSlotRequest `json:"slots"`
}

type ModelProfileRevisionActionRequest struct {
	ID              int64 `json:"id"`
	ConfirmRevision int64 `json:"confirmRevision"`
}

type TestModelProfileRequest struct {
	ID       int64 `json:"id"`
	TenantID int64 `json:"tenantId"`
	StoreID  int64 `json:"storeId"`
}

type GetStoreModelProfileAssignmentsRequest struct {
	TenantID int64 `json:"tenantId"`
}

type AssignStoreModelProfileRequest struct {
	TenantID        int64 `json:"tenantId"`
	StoreID         int64 `json:"storeId"`
	TemplateID      int64 `json:"templateId"`
	ConfirmRevision int64 `json:"confirmRevision"`
}

type BatchAssignStoreModelProfileRequest struct {
	TenantID        int64   `json:"tenantId"`
	StoreIDs        []int64 `json:"storeIds"`
	TemplateID      int64   `json:"templateId"`
	ConfirmRevision int64   `json:"confirmRevision"`
}

type ActivatePendingStoreModelProfileRequest struct {
	TenantID        int64  `json:"tenantId"`
	StoreID         int64  `json:"storeId"`
	TemplateID      int64  `json:"templateId"`
	ConfirmRevision int64  `json:"confirmRevision"`
	CurrentPassword string `json:"currentPassword"`
	Confirmed       bool   `json:"confirmed"`
}
