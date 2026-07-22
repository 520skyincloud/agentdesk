package ai

import (
	"time"

	"github.com/mlogclub/simple/sqls"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/modelconfig"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"
)

func newOpenAIClient(config models.AIConfig) openai.Client {
	return newOpenAIClientWithRuntimeConfig(legacyRuntimeConfig(config))
}

func newOpenAIClientWithRuntimeConfig(config modelconfig.Config) openai.Client {
	timeout := 60 * time.Second
	if config.TimeoutMS > 0 {
		timeout = time.Duration(config.TimeoutMS) * time.Millisecond
	}
	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithBaseURL(config.BaseURL),
		option.WithHTTPClient(usagex.NewHTTPClient(timeout)),
	}
	if config.TimeoutMS > 0 {
		opts = append(opts, option.WithRequestTimeout(time.Duration(config.TimeoutMS)*time.Millisecond))
	}
	if config.MaxRetryCount >= 0 {
		opts = append(opts, option.WithMaxRetries(config.MaxRetryCount))
	}

	return openai.NewClient(opts...)
}

func legacyRuntimeConfig(config models.AIConfig) modelconfig.Config {
	return modelconfig.Config{
		Provider: config.Provider, BaseURL: config.BaseURL, APIKey: config.APIKey,
		APIMode: config.APIMode, ModelType: config.ModelType, ModelName: config.ModelName,
		Dimension: config.Dimension, MaxContextTokens: config.MaxContextTokens,
		MaxOutputTokens: config.MaxOutputTokens, TimeoutMS: config.TimeoutMS,
		MaxRetryCount: config.MaxRetryCount,
	}
}

func NewOpenAIClientForService(config models.AIConfig) openai.Client {
	return newOpenAIClient(config)
}

func GetEnabledAIConfig(modelType enums.AIModelType) (*models.AIConfig, error) {
	item := repositories.AIConfigRepository.GetEnabled(sqls.DB(), modelType)
	if item == nil {
		return nil, errorsx.BusinessError(2005, "未配置可用的 AI 配置")
	}
	return item, nil
}
