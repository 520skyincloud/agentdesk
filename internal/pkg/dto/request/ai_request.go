package request

import "agent-desk/internal/pkg/enums"

type AIAgentMCPToolRequest struct {
	ToolCode    string            `json:"toolCode"`
	ServerCode  string            `json:"serverCode"`
	ToolName    string            `json:"toolName"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Arguments   map[string]string `json:"arguments"`
}

type CreateAIConfigRequest struct {
	Name                string            `json:"name"`
	Provider            enums.AIProvider  `json:"provider"`
	BaseURL             string            `json:"baseUrl"`
	APIKey              string            `json:"apiKey"`
	APIMode             string            `json:"apiMode"`
	ModelType           enums.AIModelType `json:"modelType"`
	ModelName           string            `json:"modelName"`
	Dimension           int               `json:"dimension"`
	MaxContextTokens    int               `json:"maxContextTokens"`
	MaxOutputTokens     int               `json:"maxOutputTokens"`
	TimeoutMS           int               `json:"timeoutMs"`
	MaxRetryCount       int               `json:"maxRetryCount"`
	RPMLimit            int               `json:"rpmLimit"`
	TPMLimit            int               `json:"tpmLimit"`
	IntentDetectEnabled bool              `json:"intentDetectEnabled"`
	Remark              string            `json:"remark"`
}

type UpdateAIConfigRequest struct {
	ID int64 `json:"id"`
	CreateAIConfigRequest
}

type DeleteAIConfigRequest struct {
	ID int64 `json:"id"`
}

type UpdateAIConfigStatusRequest struct {
	ID     int64        `json:"id"`
	Status enums.Status `json:"status"`
}
