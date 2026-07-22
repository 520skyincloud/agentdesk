package ai

import (
	"context"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/usagex"
)

var ResolveConfigForContext func(context.Context, enums.AIModelType) (*models.AIConfig, error)

func newOpenAIClient(config models.AIConfig) openai.Client {
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

func GetAIConfigForContext(ctx context.Context, modelType enums.AIModelType) (*models.AIConfig, error) {
	if ResolveConfigForContext != nil {
		return ResolveConfigForContext(ctx, modelType)
	}
	return nil, errorsx.BusinessError(2005, "模型调用缺少门店凭据解析器")
}
