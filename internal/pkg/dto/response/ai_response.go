package response

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

type AIAgentOptionResponse struct {
	ID     int64        `json:"id"`
	Name   string       `json:"name"`
	Status enums.Status `json:"status"`
}

type AIConfigResponse struct {
	ID                  int64             `json:"id"`
	Name                string            `json:"name"`
	Provider            enums.AIProvider  `json:"provider"`
	BaseURL             string            `json:"baseUrl"`
	HasAPIKey           bool              `json:"hasApiKey"`
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
	Status              enums.Status      `json:"status"`
	SortNo              int               `json:"sortNo"`
	Remark              string            `json:"remark"`
}

func BuildAIConfigResponse(item *models.AIConfig) AIConfigResponse {
	return AIConfigResponse{
		ID:                  item.ID,
		Name:                item.Name,
		Provider:            item.Provider,
		BaseURL:             item.BaseURL,
		HasAPIKey:           item.APIKey != "",
		APIMode:             item.APIMode,
		ModelType:           item.ModelType,
		ModelName:           item.ModelName,
		Dimension:           item.Dimension,
		MaxContextTokens:    item.MaxContextTokens,
		MaxOutputTokens:     item.MaxOutputTokens,
		TimeoutMS:           item.TimeoutMS,
		MaxRetryCount:       item.MaxRetryCount,
		RPMLimit:            item.RPMLimit,
		TPMLimit:            item.TPMLimit,
		IntentDetectEnabled: item.IntentDetectEnabled,
		Status:              item.Status,
		SortNo:              item.SortNo,
		Remark:              item.Remark,
	}
}
