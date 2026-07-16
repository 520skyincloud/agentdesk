package response

import (
	"agent-desk/internal/pkg/enums"
)

type TenantAIModelGrantResponse struct {
	AIConfigID            int64             `json:"aiConfigId"`
	Name                  string            `json:"name"`
	Provider              enums.AIProvider  `json:"provider"`
	ModelType             enums.AIModelType `json:"modelType"`
	ModelName             string            `json:"modelName"`
	Status                enums.Status      `json:"status"`
	RequestCount          int64             `json:"requestCount"`
	PromptTokens          int64             `json:"promptTokens"`
	CompletionTokens      int64             `json:"completionTokens"`
	CachedPromptTokens    int64             `json:"cachedPromptTokens"`
	AssignmentCount       int64             `json:"assignmentCount"`
	AssignedUsageCodes    []string          `json:"assignedUsageCodes"`
	AssignedEmployeeCount int64             `json:"assignedEmployeeCount"`
}

type TenantAIModelUsageResponse struct {
	UsageCode           string            `json:"usageCode"`
	UsageName           string            `json:"usageName"`
	ExpectedModelType   enums.AIModelType `json:"expectedModelType"`
	AIConfigID          int64             `json:"aiConfigId"`
	EffectiveAIConfigID int64             `json:"effectiveAiConfigId"`
	EffectiveModelName  string            `json:"effectiveModelName"`
	EffectiveSource     string            `json:"effectiveSource"`
}

type TenantAIModelAccessResponse struct {
	TenantID         int64                        `json:"tenantId"`
	WxWorkInstanceID int64                        `json:"wxWorkInstanceId"`
	Grants           []TenantAIModelGrantResponse `json:"grants"`
	Usages           []TenantAIModelUsageResponse `json:"usages"`
}
