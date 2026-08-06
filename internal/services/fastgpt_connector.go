package services

import (
	"context"
	"io"
	"strings"
	"time"

	"agent-desk/internal/models"
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
type FastGPTModelProfile = fastgptapi.ModelProfile
type FastGPTModelProfileInput = fastgptapi.ModelProfileInput
type FastGPTModelProfileTestResult = fastgptapi.ModelProfileTestResult
type FastGPTModelProfileUpsertResult = fastgptapi.ModelProfileUpsertResult

type FastGPTSearchResult struct {
	DatasetID string                        `json:"datasetId"`
	Hits      []fastgptapi.SearchDatasetHit `json:"hits"`
	LatencyMS int64                         `json:"latencyMs"`
}

// NewManagedStoreFastGPTConnector is the only production FastGPT transport.
// Store scope is asserted by the integration API on every remote operation.
func NewManagedStoreFastGPTConnector() (*FastGPTConnector, error) {
	cfg := config.Current().FastGPT
	if !cfg.Enabled {
		return nil, errorsx.InvalidParam("平台 FastGPT 连接尚未启用")
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	integrationToken := strings.TrimSpace(cfg.IntegrationToken)
	if baseURL == "" || integrationToken == "" {
		return nil, errorsx.InvalidParam("平台 FastGPT 缺少服务端集成凭据")
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxRetries := cfg.MaxRetries
	if maxRetries < 2 {
		maxRetries = 2
	}
	gateway, err := fastgptapi.NewGateway(fastgptapi.Config{
		BaseURL:          baseURL,
		IntegrationToken: integrationToken,
		Timeout:          timeout,
		MaxRetries:       maxRetries,
	})
	if err != nil {
		return nil, err
	}
	return &FastGPTConnector{gateway: gateway}, nil
}

func NewFastGPTConnectorForKnowledgeBase(knowledgeBase *models.KnowledgeBase) (*FastGPTConnector, error) {
	if knowledgeBase == nil || knowledgeBase.TenantID <= 0 || knowledgeBase.StoreID <= 0 ||
		strings.TrimSpace(knowledgeBase.DatasetID) == "" || strings.TrimSpace(knowledgeBase.ConnectionID) != fastgptapi.ManagedConnectionID {
		return nil, errorsx.InvalidParam("知识库尚未迁移到门店 FastGPT 托管连接")
	}
	return NewManagedStoreFastGPTConnector()
}

func (c *FastGPTConnector) ForStore(storeID int64) *FastGPTConnector {
	if c == nil || c.gateway == nil {
		return c
	}
	return &FastGPTConnector{gateway: c.gateway.ForStore(storeID)}
}

func (c *FastGPTConnector) EnsureStoreTenant(ctx context.Context, teamName string) (*fastgptapi.StoreTenant, error) {
	return c.gateway.EnsureStoreTenant(ctx, teamName)
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

func (c *FastGPTConnector) GetDatasetProfileSnapshot(ctx context.Context, datasetID string) (*fastgptapi.DatasetProfileSnapshot, error) {
	return c.gateway.GetDatasetProfileSnapshot(ctx, datasetID)
}

func (c *FastGPTConnector) GetModelProfile(ctx context.Context, datasetID string) (*FastGPTModelProfile, error) {
	return c.gateway.GetModelProfile(ctx, datasetID)
}

func (c *FastGPTConnector) TestModelProfile(ctx context.Context, input FastGPTModelProfileInput) (*FastGPTModelProfileTestResult, error) {
	return c.gateway.TestModelProfile(ctx, input)
}

func (c *FastGPTConnector) UpsertModelProfile(ctx context.Context, input FastGPTModelProfileInput) (*FastGPTModelProfileUpsertResult, error) {
	return c.gateway.UpsertModelProfile(ctx, input)
}

func (c *FastGPTConnector) ListUsageEvents(ctx context.Context, datasetID, cursor string, limit int) (*fastgptapi.UsageEventPage, error) {
	return c.gateway.ListUsageEvents(ctx, datasetID, cursor, limit)
}

func (c *FastGPTConnector) UploadLocalFile(ctx context.Context, datasetID, filename string, reader io.Reader) (string, error) {
	return c.gateway.UploadLocalFile(ctx, datasetID, filename, reader)
}

func (c *FastGPTConnector) ListCollections(ctx context.Context, datasetID string) ([]FastGPTCollection, error) {
	return c.gateway.ListCollections(ctx, datasetID)
}

func (c *FastGPTConnector) DeleteCollections(ctx context.Context, datasetID string, collectionIDs []string) error {
	return c.gateway.DeleteCollections(ctx, datasetID, collectionIDs)
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
	return &FastGPTSearchResult{DatasetID: result.DatasetID, Hits: result.Hits, LatencyMS: result.LatencyMS}, nil
}
