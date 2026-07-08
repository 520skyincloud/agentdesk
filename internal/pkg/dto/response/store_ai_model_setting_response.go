package response

type StoreAIModelSettingResponse struct {
	CompanyID               int64  `json:"companyId"`
	StoreID                 int64  `json:"storeId"`
	WxWorkInstanceID        int64  `json:"wxWorkInstanceId"`
	UsageCode               string `json:"usageCode"`
	UsageName               string `json:"usageName"`
	ExpectedModelType       string `json:"expectedModelType"`
	AIConfigID              int64  `json:"aiConfigId"`
	AIConfigName            string `json:"aiConfigName"`
	Enabled                 bool   `json:"enabled"`
	Provider                string `json:"provider"`
	BaseURL                 string `json:"baseUrl"`
	HasAPIKey               bool   `json:"hasApiKey"`
	APIMode                 string `json:"apiMode"`
	ModelType               string `json:"modelType"`
	ModelName               string `json:"modelName"`
	Dimension               int    `json:"dimension"`
	MaxContextTokens        int    `json:"maxContextTokens"`
	MaxOutputTokens         int    `json:"maxOutputTokens"`
	TimeoutMS               int    `json:"timeoutMs"`
	MaxRetryCount           int    `json:"maxRetryCount"`
	RPMLimit                int    `json:"rpmLimit"`
	TPMLimit                int    `json:"tpmLimit"`
	Remark                  string `json:"remark"`
	EffectiveAIConfigID     int64  `json:"effectiveAiConfigId"`
	EffectiveModelSettingID int64  `json:"effectiveModelSettingId"`
	EffectiveAIConfigName   string `json:"effectiveAiConfigName"`
	EffectiveModelName      string `json:"effectiveModelName"`
	EffectiveProvider       string `json:"effectiveProvider"`
	EffectiveBaseURL        string `json:"effectiveBaseUrl"`
	EffectiveModelSource    string `json:"effectiveModelSource"`
}
