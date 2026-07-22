package ai

import (
	"context"
	"fmt"
	"time"

	openai "github.com/openai/openai-go/v3"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/usagex"
)

type EmbeddingResult struct {
	Vector     []float32
	TokensUsed int
	ModelName  string
	Dimension  int
}

type embedding struct{}

var Embedding = &embedding{}

func (s *embedding) GetModel(ctx context.Context) (*models.AIConfig, error) {
	config, err := GetAIConfigForContext(ctx, enums.AIModelTypeEmbedding)
	if err != nil {
		return nil, errorsx.BusinessError(2001, "未配置可用的 Embedding 模型")
	}
	return config, nil
}

func (s *embedding) GenerateEmbedding(ctx context.Context, text string) (*EmbeddingResult, error) {
	if text == "" {
		return nil, errorsx.InvalidParam("文本内容不能为空")
	}

	result, err := s.callEmbeddingAPI(ctx, text)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (s *embedding) GenerateEmbeddingWithConfig(ctx context.Context, config models.AIConfig, text string) (*EmbeddingResult, error) {
	if text == "" {
		return nil, errorsx.InvalidParam("文本内容不能为空")
	}
	return s.callEmbeddingAPIWithConfig(ctx, config, text)
}

func (s *embedding) GenerateBatchEmbeddings(ctx context.Context, texts []string) ([]EmbeddingResult, error) {
	if len(texts) == 0 {
		return nil, errorsx.InvalidParam("文本列表不能为空")
	}

	results := make([]EmbeddingResult, 0, len(texts))
	for _, text := range texts {
		result, err := s.callEmbeddingAPI(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("failed to generate embedding for text: %w", err)
		}
		results = append(results, *result)
	}

	return results, nil
}

func (s *embedding) callEmbeddingAPI(ctx context.Context, text string) (*EmbeddingResult, error) {
	config, err := GetAIConfigForContext(ctx, enums.AIModelTypeEmbedding)
	if err != nil {
		return nil, err
	}
	return s.callEmbeddingAPIWithConfig(ctx, *config, text)
}

func (s *embedding) callEmbeddingAPIWithConfig(ctx context.Context, config models.AIConfig, text string) (*EmbeddingResult, error) {
	callCtx, capture := usagex.WithCapture(ctx)
	startedAt := time.Now()
	client := newOpenAIClient(config)
	embeddingResp, err := client.Embeddings.New(callCtx, openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfString: openai.String(text),
		},
		Model: openai.EmbeddingModel(config.ModelName),
	})
	if err != nil {
		RecordModelUsage(callCtx, ModelUsageRecord{
			Stage: "embedding", OperationType: "embedding",
			Config: config, LatencyMS: time.Since(startedAt).Milliseconds(),
			Status: "failed", ErrorClass: "model_call_failed",
			Receipt: lastCapturedReceipt(capture),
		})
		return nil, fmt.Errorf("failed to call embedding api: %w", err)
	}

	if len(embeddingResp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data in response")
	}
	vector := make([]float32, 0, len(embeddingResp.Data[0].Embedding))
	for _, item := range embeddingResp.Data[0].Embedding {
		vector = append(vector, float32(item))
	}

	result := &EmbeddingResult{
		Vector:     vector,
		TokensUsed: int(embeddingResp.Usage.TotalTokens),
		ModelName:  embeddingResp.Model,
		Dimension:  len(vector),
	}
	RecordModelUsage(callCtx, ModelUsageRecord{
		Stage: "embedding", OperationType: "embedding",
		Config: config, PromptTokens: int64(result.TokensUsed),
		LatencyMS: time.Since(startedAt).Milliseconds(), Status: "completed",
		Receipt: lastCapturedReceipt(capture),
	})
	return result, nil
}

func lastCapturedReceipt(capture *usagex.Capture) *usagex.Receipt {
	if capture == nil {
		return nil
	}
	receipts := capture.Receipts()
	if len(receipts) == 0 {
		return nil
	}
	receipt := receipts[len(receipts)-1]
	return &receipt
}

func (s *embedding) GetDimension(ctx context.Context) (int, error) {
	model, err := s.GetModel(ctx)
	if err != nil {
		return 0, err
	}
	return model.Dimension, nil
}
