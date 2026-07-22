package fastgpt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const maxResponseBytes = 4 << 20

var ErrTransportUnavailable = errors.New("FastGPT transport unavailable")

// ManagedConnectionID identifies datasets created through the only supported
// Agent Desk server-to-server integration.
const ManagedConnectionID = "agentdesk_integration"

type Config struct {
	BaseURL          string
	IntegrationToken string
	StoreID          string
	Timeout          time.Duration
	MaxRetries       int
}

type Gateway struct {
	config     Config
	httpClient *http.Client
}

type TransportError struct {
	cause error
}

func (e *TransportError) Error() string { return ErrTransportUnavailable.Error() }

func (e *TransportError) Unwrap() error {
	if e != nil && errors.Is(e.cause, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if e != nil && errors.Is(e.cause, context.Canceled) {
		return context.Canceled
	}
	return ErrTransportUnavailable
}

type Dataset struct {
	ID   string `json:"_id"`
	Name string `json:"name"`
}

// StoreTenant is the non-sensitive FastGPT Team identity for one Agent Desk
// store. It deliberately never includes a FastGPT user credential or model key.
type StoreTenant struct {
	ExternalStoreID string `json:"externalStoreId"`
	TeamID          string `json:"teamId"`
	TeamName        string `json:"teamName"`
	Status          string `json:"status"`
}

// DatasetProfileSnapshot is the safe model-routing metadata exposed by the
// FastGPT integration. Credentials and upstream endpoints remain in FastGPT.
type DatasetProfileSnapshot struct {
	ProfileID       string `json:"profileId"`
	ProfileName     string `json:"profileName"`
	ProfileRevision string `json:"profileRevision"`
	ProfileStatus   string `json:"profileStatus"`
	Fingerprint     string `json:"fingerprint"`
}

type ModelCredential struct {
	Provider       string `json:"provider"`
	BaseURL        string `json:"baseUrl"`
	Model          string `json:"model"`
	APIKey         string `json:"apiKey,omitempty"`
	KeyConfigured  bool   `json:"keyConfigured,omitempty"`
	KeyFingerprint string `json:"keyFingerprint,omitempty"`
}

type ModelProfile struct {
	ID             string           `json:"_id"`
	Name           string           `json:"name"`
	Revision       int64            `json:"revision"`
	Embedding      ModelCredential  `json:"embedding"`
	DocumentParser ModelCredential  `json:"documentParser"`
	Vision         ModelCredential  `json:"vision"`
	Rerank         *ModelCredential `json:"rerank,omitempty"`
}

type ModelProfileInput struct {
	DatasetID      string           `json:"datasetId"`
	ProfileID      string           `json:"profileId,omitempty"`
	Name           string           `json:"name"`
	Embedding      ModelCredential  `json:"embedding"`
	DocumentParser ModelCredential  `json:"documentParser"`
	Vision         ModelCredential  `json:"vision"`
	Rerank         *ModelCredential `json:"rerank,omitempty"`
	DisableRerank  bool             `json:"-"`
	TestToken      string           `json:"testToken,omitempty"`
}

type ModelProfileTestResult struct {
	TestToken string    `json:"testToken"`
	ExpiresAt time.Time `json:"expiresAt"`
	Results   []struct {
		Stage            string `json:"stage"`
		Status           string `json:"status"`
		PromptTokens     int64  `json:"promptTokens"`
		CompletionTokens int64  `json:"completionTokens"`
	} `json:"results"`
}

type ModelProfileUpsertResult struct {
	Profile           ModelProfile `json:"profile"`
	BoundDatasetCount int64        `json:"boundDatasetCount"`
}

// UsageEvent is immutable evidence emitted by FastGPT. Token fields are only
// populated when FastGPT received them from the upstream provider.
type UsageEvent struct {
	ExternalEventID  string    `json:"externalEventId"`
	Kind             string    `json:"kind"`
	CreatedAt        time.Time `json:"createTime"`
	Stage            string    `json:"stage"`
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	ProfileID        string    `json:"profileId"`
	ProfileRevision  int64     `json:"profileRevision"`
	RequestID        string    `json:"requestId"`
	PromptTokens     int64     `json:"promptTokens"`
	CompletionTokens int64     `json:"completionTokens"`
	CachedTokens     int64     `json:"cachedTokens"`
	ReasoningTokens  int64     `json:"reasoningTokens"`
	LatencyMS        int64     `json:"latencyMs"`
	OperationType    string    `json:"operationType"`
	RequestCount     int64     `json:"requestCount"`
	RerankCount      int64     `json:"rerankCount"`
	TrainingCount    int64     `json:"trainingCount"`
	FileBytes        int64     `json:"fileBytes"`
	Status           string    `json:"status"`
	ErrorClass       string    `json:"errorClass"`
}

type UsageEventPage struct {
	Events     []UsageEvent `json:"events"`
	NextCursor string       `json:"nextCursor"`
}

type Collection struct {
	ID             string `json:"_id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	DataAmount     int    `json:"dataAmount"`
	TrainingAmount int    `json:"trainingAmount"`
	Forbid         bool   `json:"forbid"`
}

type SearchDatasetRequest struct {
	DatasetID  string
	Query      string
	TokenLimit int
	Similarity float64
	SearchMode string
	UseRerank  bool
	TopK       int
}

type SearchDatasetHit struct {
	DataID       string  `json:"dataId"`
	CollectionID string  `json:"collectionId"`
	DatasetID    string  `json:"datasetId"`
	SourceName   string  `json:"sourceName"`
	Question     string  `json:"question"`
	Answer       string  `json:"answer"`
	Score        float64 `json:"score"`
}

type SearchDatasetResult struct {
	DatasetID string             `json:"datasetId"`
	Hits      []SearchDatasetHit `json:"hits"`
	LatencyMS int64              `json:"latencyMs"`
}

type apiResponse struct {
	Code       int             `json:"code"`
	StatusText string          `json:"statusText"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
}

type rawSearchHit struct {
	ID             string          `json:"id"`
	UnderscoreID   string          `json:"_id"`
	DataID         string          `json:"dataId"`
	CollectionID   string          `json:"collectionId"`
	DatasetID      string          `json:"datasetId"`
	SourceName     string          `json:"sourceName"`
	Q              string          `json:"q"`
	A              string          `json:"a"`
	Question       string          `json:"question"`
	Answer         string          `json:"answer"`
	Score          json.RawMessage `json:"score"`
	Similarity     float64         `json:"similarity"`
	RelevanceScore float64         `json:"relevanceScore"`
}

type rawSearchScore struct {
	Type  string  `json:"type"`
	Value float64 `json:"value"`
}

func NewGateway(config Config) (*Gateway, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.IntegrationToken = strings.TrimSpace(config.IntegrationToken)
	if config.BaseURL == "" || config.IntegrationToken == "" {
		return nil, errors.New("FastGPT gateway requires base URL and an integration token")
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.MaxRetries < 0 {
		config.MaxRetries = 0
	}
	return &Gateway{config: config, httpClient: &http.Client{Timeout: config.Timeout}}, nil
}

func (g *Gateway) ForStore(storeID int64) *Gateway {
	clone := *g
	clone.config.StoreID = fmt.Sprintf("%d", storeID)
	return &clone
}

func (g *Gateway) requireStoreScope() error {
	if strings.TrimSpace(g.config.StoreID) == "" {
		return errors.New("FastGPT integration requires a store scope")
	}
	return nil
}

// EnsureStoreTenant creates or resolves the managed Team for one Store.
func (g *Gateway) EnsureStoreTenant(ctx context.Context, teamName string) (*StoreTenant, error) {
	if err := g.requireStoreScope(); err != nil {
		return nil, err
	}
	data, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/tenant/ensure", nil, map[string]any{
		"externalStoreId": g.config.StoreID,
		"teamName":        strings.TrimSpace(teamName),
	}, false)
	if err != nil {
		return nil, err
	}
	var tenant StoreTenant
	if err := json.Unmarshal(data, &tenant); err != nil {
		return nil, fmt.Errorf("parse FastGPT integration tenant: %w", err)
	}
	if strings.TrimSpace(tenant.TeamID) == "" || strings.TrimSpace(tenant.ExternalStoreID) != g.config.StoreID {
		return nil, errors.New("FastGPT integration tenant response is invalid")
	}
	return &tenant, nil
}

func (g *Gateway) CreateDataset(ctx context.Context, name, intro string) (*Dataset, error) {
	if err := g.requireStoreScope(); err != nil {
		return nil, err
	}
	data, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/create", nil, map[string]any{
		"externalStoreId": g.config.StoreID,
		"name":            strings.TrimSpace(name),
		"intro":           strings.TrimSpace(intro),
	}, false)
	if err != nil {
		return nil, err
	}
	var result struct {
		DatasetID   string `json:"datasetId"`
		DatasetName string `json:"datasetName"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.DatasetID == "" {
		return nil, errors.New("FastGPT integration create response is missing datasetId")
	}
	return &Dataset{ID: result.DatasetID, Name: firstNonBlank(result.DatasetName, name)}, nil
}

func (g *Gateway) GetDataset(ctx context.Context, datasetID string) (*Dataset, error) {
	if err := g.requireStoreScope(); err != nil {
		return nil, err
	}
	data, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/detail", nil, map[string]any{
		"externalStoreId": g.config.StoreID, "datasetId": strings.TrimSpace(datasetID),
	}, true)
	if err != nil {
		return nil, err
	}
	var result struct {
		DatasetID   string `json:"datasetId"`
		DatasetName string `json:"datasetName"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse FastGPT integration dataset: %w", err)
	}
	return &Dataset{ID: result.DatasetID, Name: result.DatasetName}, nil
}

func (g *Gateway) DeleteDataset(ctx context.Context, datasetID string) error {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return errors.New("FastGPT delete dataset requires datasetId")
	}
	if err := g.requireStoreScope(); err != nil {
		return err
	}
	_, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/delete", nil, map[string]any{
		"externalStoreId": g.config.StoreID, "datasetId": datasetID,
	}, false)
	return err
}

func (g *Gateway) GetDatasetProfileSnapshot(ctx context.Context, datasetID string) (*DatasetProfileSnapshot, error) {
	if err := g.requireStoreScope(); err != nil {
		return nil, err
	}
	data, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/profile", nil, map[string]any{
		"externalStoreId": g.config.StoreID, "datasetId": strings.TrimSpace(datasetID),
	}, true)
	if err != nil {
		return nil, err
	}
	var raw struct {
		ProfileID       string            `json:"datasetModelProfileId"`
		ProfileName     string            `json:"profileName"`
		ProfileRevision int64             `json:"profileRevision"`
		ProfileStatus   string            `json:"profileStatus"`
		Fingerprint     map[string]string `json:"fingerprint"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse FastGPT profile snapshot: %w", err)
	}
	if raw.ProfileStatus == "" {
		return nil, errors.New("FastGPT profile snapshot is missing status")
	}
	parts := make([]string, 0, len(raw.Fingerprint))
	for _, key := range []string{"embedding", "documentParser", "vision", "rerank"} {
		if value := strings.TrimSpace(raw.Fingerprint[key]); value != "" {
			parts = append(parts, key+":"+value)
		}
	}
	return &DatasetProfileSnapshot{
		ProfileID:   strings.TrimSpace(raw.ProfileID),
		ProfileName: strings.TrimSpace(raw.ProfileName),
		ProfileRevision: func() string {
			if raw.ProfileRevision <= 0 {
				return ""
			}
			return fmt.Sprintf("%d", raw.ProfileRevision)
		}(),
		ProfileStatus: strings.TrimSpace(raw.ProfileStatus),
		Fingerprint:   strings.Join(parts, ","),
	}, nil
}

func (g *Gateway) GetModelProfile(ctx context.Context, datasetID string) (*ModelProfile, error) {
	if err := g.requireStoreScope(); err != nil {
		return nil, err
	}
	data, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/model-profile/detail", nil, map[string]any{
		"externalStoreId": g.config.StoreID,
		"datasetId":       strings.TrimSpace(datasetID),
	}, true)
	if err != nil {
		return nil, err
	}
	var result struct {
		Profile *ModelProfile `json:"profile"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse FastGPT model profile: %w", err)
	}
	return result.Profile, nil
}

func (g *Gateway) TestModelProfile(ctx context.Context, input ModelProfileInput) (*ModelProfileTestResult, error) {
	if err := g.requireStoreScope(); err != nil {
		return nil, err
	}
	payload := g.modelProfilePayload(input)
	data, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/model-profile/test", nil, payload, false)
	if err != nil {
		return nil, err
	}
	var result ModelProfileTestResult
	if err := json.Unmarshal(data, &result); err != nil || strings.TrimSpace(result.TestToken) == "" {
		return nil, errors.New("FastGPT model profile test response is invalid")
	}
	return &result, nil
}

func (g *Gateway) UpsertModelProfile(ctx context.Context, input ModelProfileInput) (*ModelProfileUpsertResult, error) {
	if err := g.requireStoreScope(); err != nil {
		return nil, err
	}
	payload := g.modelProfilePayload(input)
	data, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/model-profile/upsert", nil, payload, false)
	if err != nil {
		return nil, err
	}
	var result ModelProfileUpsertResult
	if err := json.Unmarshal(data, &result); err != nil || strings.TrimSpace(result.Profile.ID) == "" {
		return nil, errors.New("FastGPT model profile save response is invalid")
	}
	return &result, nil
}

func (g *Gateway) modelProfilePayload(input ModelProfileInput) map[string]any {
	payload := map[string]any{
		"externalStoreId": g.config.StoreID,
		"datasetId":       strings.TrimSpace(input.DatasetID),
		"name":            strings.TrimSpace(input.Name),
		"embedding":       input.Embedding,
		"documentParser":  input.DocumentParser,
		"vision":          input.Vision,
	}
	if strings.TrimSpace(input.ProfileID) != "" {
		payload["profileId"] = strings.TrimSpace(input.ProfileID)
	}
	if input.DisableRerank {
		payload["rerank"] = nil
	} else if input.Rerank != nil {
		payload["rerank"] = input.Rerank
	}
	if strings.TrimSpace(input.TestToken) != "" {
		payload["testToken"] = strings.TrimSpace(input.TestToken)
	}
	return payload
}

func (g *Gateway) ListUsageEvents(ctx context.Context, datasetID, cursor string, limit int) (*UsageEventPage, error) {
	if err := g.requireStoreScope(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	data, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/usage/list", nil, map[string]any{
		"externalStoreId": g.config.StoreID, "datasetId": strings.TrimSpace(datasetID), "cursor": strings.TrimSpace(cursor), "limit": limit,
	}, true)
	if err != nil {
		return nil, err
	}
	var page UsageEventPage
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("parse FastGPT usage events: %w", err)
	}
	for i := range page.Events {
		if strings.TrimSpace(page.Events[i].ExternalEventID) == "" {
			return nil, errors.New("FastGPT usage event is missing externalEventId")
		}
	}
	return &page, nil
}

func (g *Gateway) UploadLocalFile(ctx context.Context, datasetID, filename string, reader io.Reader) (string, error) {
	if err := g.requireStoreScope(); err != nil {
		return "", err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", path.Base(filename))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, reader); err != nil {
		return "", err
	}
	dataPayload, _ := json.Marshal(map[string]any{
		"externalStoreId": g.config.StoreID,
		"datasetId":       strings.TrimSpace(datasetID), "parentId": nil,
		"trainingType": "chunk", "chunkSettingMode": "auto",
		"chunkSplitter": "", "qaPrompt": "", "metadata": map[string]any{},
	})
	if err := writer.WriteField("data", string(dataPayload)); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := g.newRequest(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/upload", nil, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	data, err := g.execute(req)
	if err != nil {
		return "", err
	}
	var result struct {
		CollectionID string `json:"collectionId"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.CollectionID == "" {
		return "", errors.New("FastGPT upload response is missing collectionId")
	}
	return result.CollectionID, nil
}

func (g *Gateway) ListCollections(ctx context.Context, datasetID string) ([]Collection, error) {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return nil, errors.New("FastGPT list collections requires datasetId")
	}
	if err := g.requireStoreScope(); err != nil {
		return nil, err
	}
	data, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/collections", nil, map[string]any{
		"externalStoreId": g.config.StoreID, "datasetId": datasetID,
	}, true)
	if err != nil {
		return nil, err
	}
	var result struct {
		Collections []struct {
			ID             string `json:"collectionId"`
			Name           string `json:"name"`
			Type           string `json:"type"`
			DataAmount     int    `json:"dataAmount"`
			TrainingAmount int    `json:"trainingAmount"`
			Forbid         bool   `json:"forbid"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse FastGPT integration collections: %w", err)
	}
	collections := make([]Collection, 0, len(result.Collections))
	for _, item := range result.Collections {
		collections = append(collections, Collection{
			ID:             item.ID,
			Name:           item.Name,
			Type:           item.Type,
			DataAmount:     item.DataAmount,
			TrainingAmount: item.TrainingAmount,
			Forbid:         item.Forbid,
		})
	}
	return collections, nil
}

func (g *Gateway) DeleteCollections(ctx context.Context, datasetID string, collectionIDs []string) error {
	if err := g.requireStoreScope(); err != nil {
		return err
	}
	_, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/delete-collections", nil, map[string]any{
		"externalStoreId": g.config.StoreID, "datasetId": strings.TrimSpace(datasetID), "collectionIds": collectionIDs,
	}, false)
	return err
}

func (g *Gateway) SearchDataset(ctx context.Context, input SearchDatasetRequest) (*SearchDatasetResult, error) {
	input.DatasetID = strings.TrimSpace(input.DatasetID)
	input.Query = strings.TrimSpace(input.Query)
	if input.DatasetID == "" || input.Query == "" {
		return nil, errors.New("FastGPT search requires datasetId and query")
	}
	if input.TokenLimit <= 0 || input.TokenLimit > 4000 {
		input.TokenLimit = 4000
	}
	if input.SearchMode == "" {
		input.SearchMode = "mixedRecall"
	}
	if err := g.requireStoreScope(); err != nil {
		return nil, err
	}
	startedAt := time.Now()
	payload := map[string]any{
		"externalStoreId": g.config.StoreID,
		"datasetId":       input.DatasetID,
		"text":            input.Query,
		"tokenLimit":      input.TokenLimit,
		"similarity":      input.Similarity,
		"searchMode":      input.SearchMode,
		"useRerank":       input.UseRerank,
		"topK":            input.TopK,
	}
	data, err := g.doJSON(ctx, http.MethodPost, "/api/integration/agent-desk/dataset/search", nil, payload, true)
	latencyMS := time.Since(startedAt).Milliseconds()
	if err != nil {
		return nil, err
	}
	hits, err := parseSearchHits(data, input.DatasetID)
	if err != nil {
		return nil, err
	}
	filtered := hits[:0]
	for _, hit := range hits {
		if hit.Score < input.Similarity {
			continue
		}
		filtered = append(filtered, hit)
	}
	if input.TopK > 0 && len(filtered) > input.TopK {
		filtered = filtered[:input.TopK]
	}
	return &SearchDatasetResult{DatasetID: input.DatasetID, Hits: filtered, LatencyMS: latencyMS}, nil
}

func parseSearchHits(data json.RawMessage, expectedDatasetID string) ([]SearchDatasetHit, error) {
	var rawHits []rawSearchHit
	if err := json.Unmarshal(data, &rawHits); err != nil {
		var wrapped struct {
			List []rawSearchHit `json:"list"`
		}
		if wrappedErr := json.Unmarshal(data, &wrapped); wrappedErr != nil {
			return nil, fmt.Errorf("parse FastGPT search results: %w", err)
		}
		rawHits = wrapped.List
	}
	result := make([]SearchDatasetHit, 0, len(rawHits))
	for _, item := range rawHits {
		datasetID := strings.TrimSpace(item.DatasetID)
		if datasetID == "" || datasetID != expectedDatasetID {
			return nil, fmt.Errorf("FastGPT search dataset mismatch: expected %s, got %s", expectedDatasetID, datasetID)
		}
		score, scoreErr := parseSearchScore(item.Score)
		if scoreErr != nil {
			return nil, scoreErr
		}
		if score == 0 {
			score = item.RelevanceScore
		}
		if score == 0 {
			score = item.Similarity
		}
		result = append(result, SearchDatasetHit{
			DataID: firstNonBlank(item.DataID, item.ID, item.UnderscoreID), CollectionID: strings.TrimSpace(item.CollectionID),
			DatasetID: datasetID, SourceName: strings.TrimSpace(item.SourceName),
			Question: firstNonBlank(item.Q, item.Question), Answer: firstNonBlank(item.A, item.Answer), Score: score,
		})
	}
	return result, nil
}

func parseSearchScore(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var numeric float64
	if err := json.Unmarshal(raw, &numeric); err == nil {
		return numeric, nil
	}
	var scores []rawSearchScore
	if err := json.Unmarshal(raw, &scores); err != nil {
		return 0, fmt.Errorf("parse FastGPT search score: %w", err)
	}
	// FastGPT mixed recall can return multiple score stages. RRF values use a
	// much smaller fusion scale, so they must not override a comparable
	// full-text or embedding relevance score when applying a 0..1 threshold.
	for _, scoreType := range []string{"rerank", "embedding", "fulltext", "rrf"} {
		for _, score := range scores {
			if strings.EqualFold(strings.TrimSpace(score.Type), scoreType) {
				return score.Value, nil
			}
		}
	}
	return 0, nil
}

func (g *Gateway) doJSON(ctx context.Context, method, endpoint string, query url.Values, payload any, retryable bool) (json.RawMessage, error) {
	var encoded []byte
	var err error
	if payload != nil {
		encoded, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	attempts := 1
	if retryable {
		attempts += g.config.MaxRetries
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		var body io.Reader
		if encoded != nil {
			body = bytes.NewReader(encoded)
		}
		req, reqErr := g.newRequest(ctx, method, endpoint, query, body)
		if reqErr != nil {
			return nil, reqErr
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		data, execErr := g.execute(req)
		if execErr == nil {
			return data, nil
		}
		lastErr = execErr
		var statusErr *HTTPStatusError
		if !errors.As(execErr, &statusErr) || statusErr.StatusCode < 500 || attempt+1 >= attempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
		}
	}
	return nil, lastErr
}

func (g *Gateway) newRequest(ctx context.Context, method, endpoint string, query url.Values, body io.Reader) (*http.Request, error) {
	target := g.config.BaseURL + endpoint
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, &TransportError{cause: err}
	}
	req.Header.Set("X-Agent-Desk-Token", g.config.IntegrationToken)
	return req, nil
}

type HTTPStatusError struct {
	StatusCode int
	Message    string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("FastGPT HTTP status %d", e.StatusCode)
}

type APIResponseError struct {
	Code int
}

func (e *APIResponseError) Error() string { return fmt.Sprintf("FastGPT API code %d", e.Code) }

func (g *Gateway) execute(req *http.Request) (json.RawMessage, error) {
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, &TransportError{cause: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode}
	}
	var envelope apiResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("invalid FastGPT response: %w", err)
	}
	if envelope.Code != 200 {
		return nil, &APIResponseError{Code: envelope.Code}
	}
	return envelope.Data, nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
