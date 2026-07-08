package request

import "agent-desk/internal/pkg/enums"

type UpdateStoreAIModelSettingsRequest struct {
	CompanyID        int64                              `json:"companyId"`
	StoreID          int64                              `json:"storeId"`
	WxWorkInstanceID int64                              `json:"wxWorkInstanceId"`
	Settings         []StoreAIModelSettingUpdateRequest `json:"settings"`
}

type StoreAIModelSettingUpdateRequest struct {
	UsageCode        string            `json:"usageCode"`
	AIConfigID       int64             `json:"aiConfigId"`
	Enabled          bool              `json:"enabled"`
	Provider         enums.AIProvider  `json:"provider"`
	BaseURL          string            `json:"baseUrl"`
	APIKey           string            `json:"apiKey"`
	APIMode          string            `json:"apiMode"`
	ModelType        enums.AIModelType `json:"modelType"`
	ModelName        string            `json:"modelName"`
	Dimension        int               `json:"dimension"`
	MaxContextTokens int               `json:"maxContextTokens"`
	MaxOutputTokens  int               `json:"maxOutputTokens"`
	TimeoutMS        int               `json:"timeoutMs"`
	MaxRetryCount    int               `json:"maxRetryCount"`
	RPMLimit         int               `json:"rpmLimit"`
	TPMLimit         int               `json:"tpmLimit"`
	Remark           string            `json:"remark"`
}
