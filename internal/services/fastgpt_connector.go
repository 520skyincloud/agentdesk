package services

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/errorsx"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
)

// FastGPTConnector keeps the existing service contract while all FastGPT HTTP
// behavior lives in the shared, database-independent gateway.
type FastGPTConnector struct {
	gateway *fastgptapi.Gateway
}

type FastGPTDataset = fastgptapi.Dataset
type FastGPTCollection = fastgptapi.Collection

type FastGPTSearchResult struct {
	Raw json.RawMessage `json:"raw"`
}

func NewPlatformFastGPTConnector() (*FastGPTConnector, error) {
	cfg := config.Current().FastGPT
	if !cfg.Enabled {
		return nil, errorsx.InvalidParam("平台 FastGPT 连接尚未启用")
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	apiKey := strings.TrimSpace(cfg.APIKey)
	if baseURL == "" || apiKey == "" {
		return nil, errorsx.InvalidParam("平台 FastGPT 缺少地址或 API Key")
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}
	gateway, err := fastgptapi.NewGateway(fastgptapi.Config{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		Timeout:     timeout,
		MaxRetries:  maxRetries,
		VectorModel: strings.TrimSpace(cfg.VectorModel),
		AgentModel:  strings.TrimSpace(cfg.AgentModel),
		VLMModel:    strings.TrimSpace(cfg.VLMModel),
	})
	if err != nil {
		return nil, err
	}
	return &FastGPTConnector{gateway: gateway}, nil
}

func (c *FastGPTConnector) CreateDataset(ctx context.Context, name, intro string) (*FastGPTDataset, error) {
	return c.gateway.CreateDataset(ctx, name, intro)
}

func (c *FastGPTConnector) GetDataset(ctx context.Context, datasetID string) (*FastGPTDataset, error) {
	return c.gateway.GetDataset(ctx, datasetID)
}

func (c *FastGPTConnector) DeleteDataset(ctx context.Context, datasetID string) error {
	return c.gateway.DeleteDataset(ctx, datasetID)
}

func (c *FastGPTConnector) UploadLocalFile(ctx context.Context, datasetID, filename string, reader io.Reader) (string, error) {
	return c.gateway.UploadLocalFile(ctx, datasetID, filename, reader)
}

func (c *FastGPTConnector) ListCollections(ctx context.Context, datasetID string) ([]FastGPTCollection, error) {
	return c.gateway.ListCollections(ctx, datasetID)
}

func (c *FastGPTConnector) DeleteCollections(ctx context.Context, collectionIDs []string) error {
	return c.gateway.DeleteCollections(ctx, collectionIDs)
}

func (c *FastGPTConnector) SearchTest(ctx context.Context, datasetID, text string) (*FastGPTSearchResult, error) {
	tokenLimit := config.Current().FastGPT.RetrievalTokenLimit
	if tokenLimit <= 0 || tokenLimit > 4000 {
		tokenLimit = 400
	}
	result, err := c.gateway.SearchDataset(ctx, fastgptapi.SearchDatasetRequest{
		DatasetID: datasetID, Query: text, TokenLimit: tokenLimit,
		Similarity: 0, SearchMode: "mixedRecall", UseRerank: true, TopK: 20,
	})
	if err != nil {
		return nil, err
	}
	return &FastGPTSearchResult{Raw: result.Raw}, nil
}
