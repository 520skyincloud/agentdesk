package response

import "time"

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
	ID              int64                      `json:"id"`
	Code            string                     `json:"code"`
	Name            string                     `json:"name"`
	Description     string                     `json:"description"`
	Revision        int64                      `json:"revision"`
	GatewayBaseURL  string                     `json:"gatewayBaseUrl"`
	Status          string                     `json:"status"`
	PublishedAt     *time.Time                 `json:"publishedAt"`
	PublishedByName string                     `json:"publishedByName"`
	Slots           []ModelProfileSlotResponse `json:"slots"`
	CreatedAt       time.Time                  `json:"createdAt"`
	UpdatedAt       time.Time                  `json:"updatedAt"`
}

type ModelProfileCatalogResponse struct {
	Profiles      []ModelProfileTemplateResponse `json:"profiles"`
	RequiredSlots []ModelUsageSlotOptionResponse `json:"requiredSlots"`
}

type ModelUsageSlotOptionResponse struct {
	UsageCode         string `json:"usageCode"`
	DisplayName       string `json:"displayName"`
	ExpectedModelType string `json:"expectedModelType"`
}

type ModelProfileValidationIssueResponse struct {
	UsageCode string `json:"usageCode"`
	Message   string `json:"message"`
}

type ModelProfileValidationResponse struct {
	TemplateID int64                                 `json:"templateId"`
	Revision   int64                                 `json:"revision"`
	Status     string                                `json:"status"`
	Issues     []ModelProfileValidationIssueResponse `json:"issues"`
}

type StoreModelProfileOptionResponse struct {
	TemplateID int64    `json:"templateId"`
	Code       string   `json:"code"`
	Name       string   `json:"name"`
	Revision   int64    `json:"revision"`
	Status     string   `json:"status"`
	ModelNames []string `json:"modelNames"`
}

type StoreModelProfileAssignmentResponse struct {
	TenantID                int64      `json:"tenantId"`
	StoreID                 int64      `json:"storeId"`
	StoreCode               string     `json:"storeCode"`
	StoreName               string     `json:"storeName"`
	AssignmentID            int64      `json:"assignmentId"`
	Status                  string     `json:"status"`
	ReadinessStatus         string     `json:"readinessStatus"`
	ActiveTemplateID        int64      `json:"activeTemplateId"`
	ActiveTemplateName      string     `json:"activeTemplateName"`
	ActiveTemplateRevision  int64      `json:"activeTemplateRevision"`
	PendingTemplateID       int64      `json:"pendingTemplateId"`
	PendingTemplateName     string     `json:"pendingTemplateName"`
	PendingTemplateRevision int64      `json:"pendingTemplateRevision"`
	PendingRequestedAt      *time.Time `json:"pendingRequestedAt"`
	LastValidatedAt         *time.Time `json:"lastValidatedAt"`
	LastReadyAt             *time.Time `json:"lastReadyAt"`
	LastErrorMessage        string     `json:"lastErrorMessage"`
}

type StoreModelProfileAssignmentsResponse struct {
	TenantID int64                                 `json:"tenantId"`
	Profiles []StoreModelProfileOptionResponse     `json:"profiles"`
	Stores   []StoreModelProfileAssignmentResponse `json:"stores"`
}
