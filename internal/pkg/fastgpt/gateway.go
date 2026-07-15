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

type Config struct {
	BaseURL     string
	APIKey      string
	Timeout     time.Duration
	MaxRetries  int
	VectorModel string
	AgentModel  string
	VLMModel    string
}

type Gateway struct {
	config     Config
	httpClient *http.Client
}

type Dataset struct {
	ID   string `json:"_id"`
	Name string `json:"name"`
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
	Raw       json.RawMessage    `json:"raw,omitempty"`
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
	config.APIKey = strings.TrimSpace(config.APIKey)
	if config.BaseURL == "" || config.APIKey == "" {
		return nil, errors.New("FastGPT gateway requires base URL and API key")
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.MaxRetries < 0 {
		config.MaxRetries = 0
	}
	return &Gateway{config: config, httpClient: &http.Client{Timeout: config.Timeout}}, nil
}

func (g *Gateway) CreateDataset(ctx context.Context, name, intro string) (*Dataset, error) {
	payload := map[string]any{
		"parentId": nil, "type": "dataset", "name": strings.TrimSpace(name),
		"intro": strings.TrimSpace(intro), "avatar": "",
	}
	if model := strings.TrimSpace(g.config.VectorModel); model != "" {
		payload["vectorModel"] = model
	}
	if model := strings.TrimSpace(g.config.AgentModel); model != "" {
		payload["agentModel"] = model
	}
	if model := strings.TrimSpace(g.config.VLMModel); model != "" {
		payload["vlmModel"] = model
	}
	data, err := g.doJSON(ctx, http.MethodPost, "/api/core/dataset/create", nil, payload, false)
	if err != nil {
		return nil, err
	}
	var dataset Dataset
	if err := json.Unmarshal(data, &dataset); err == nil && dataset.ID != "" {
		return &dataset, nil
	}
	var datasetID string
	if err := json.Unmarshal(data, &datasetID); err == nil && datasetID != "" {
		return &Dataset{ID: datasetID, Name: strings.TrimSpace(name)}, nil
	}
	return nil, errors.New("FastGPT create dataset response is missing datasetId")
}

func (g *Gateway) GetDataset(ctx context.Context, datasetID string) (*Dataset, error) {
	data, err := g.doJSON(ctx, http.MethodGet, "/api/core/dataset/detail", url.Values{"id": []string{strings.TrimSpace(datasetID)}}, nil, true)
	if err != nil {
		return nil, err
	}
	var dataset Dataset
	if err := json.Unmarshal(data, &dataset); err != nil {
		return nil, fmt.Errorf("parse FastGPT dataset: %w", err)
	}
	return &dataset, nil
}

func (g *Gateway) DeleteDataset(ctx context.Context, datasetID string) error {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return errors.New("FastGPT delete dataset requires datasetId")
	}
	_, err := g.doJSON(ctx, http.MethodDelete, "/api/core/dataset/delete", url.Values{"id": []string{datasetID}}, nil, false)
	return err
}

func (g *Gateway) UploadLocalFile(ctx context.Context, datasetID, filename string, reader io.Reader) (string, error) {
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
		"datasetId": strings.TrimSpace(datasetID), "parentId": nil,
		"trainingType": "chunk", "chunkSettingMode": "auto",
		"chunkSplitter": "", "qaPrompt": "", "metadata": map[string]any{},
	})
	if err := writer.WriteField("data", string(dataPayload)); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := g.newRequest(ctx, http.MethodPost, "/api/core/dataset/collection/create/localFile", nil, &body)
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
	const pageSize = 30
	collections := make([]Collection, 0, pageSize)
	for page := 0; page < 100; page++ {
		data, err := g.doJSON(ctx, http.MethodPost, "/api/core/dataset/collection/listV2", nil, map[string]any{
			"offset": page * pageSize, "pageSize": pageSize, "datasetId": datasetID, "parentId": nil, "searchText": "",
		}, true)
		if err != nil {
			return nil, err
		}
		var result struct {
			List []Collection `json:"list"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("parse FastGPT collections: %w", err)
		}
		collections = append(collections, result.List...)
		if len(result.List) < pageSize {
			return collections, nil
		}
	}
	return nil, errors.New("FastGPT collection pagination exceeded safety limit")
}

func (g *Gateway) DeleteCollections(ctx context.Context, collectionIDs []string) error {
	_, err := g.doJSON(ctx, http.MethodDelete, "/api/core/dataset/collection/delete", nil, map[string]any{"collectionIds": collectionIDs}, false)
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
	startedAt := time.Now()
	data, err := g.doJSON(ctx, http.MethodPost, "/api/core/dataset/searchTest", nil, map[string]any{
		"datasetId":                        input.DatasetID,
		"text":                             input.Query,
		"limit":                            input.TokenLimit,
		"similarity":                       input.Similarity,
		"searchMode":                       input.SearchMode,
		"usingReRank":                      input.UseRerank,
		"datasetSearchUsingExtensionQuery": false,
	}, true)
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
	return &SearchDatasetResult{DatasetID: input.DatasetID, Hits: filtered, LatencyMS: latencyMS, Raw: data}, nil
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
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.config.APIKey)
	return req, nil
}

type HTTPStatusError struct {
	StatusCode int
	Message    string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("FastGPT HTTP %d: %s", e.StatusCode, e.Message)
}

func (g *Gateway) execute(req *http.Request) (json.RawMessage, error) {
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("FastGPT request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Message: g.sanitizeErrorText(string(body))}
	}
	var envelope apiResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("invalid FastGPT response: %w", err)
	}
	if envelope.Code != 200 {
		return nil, fmt.Errorf("FastGPT returned code %d: %s", envelope.Code, g.sanitizeErrorText(firstNonBlank(envelope.Message, envelope.StatusText)))
	}
	return envelope.Data, nil
}

func (g *Gateway) sanitizeErrorText(value string) string {
	value = strings.ReplaceAll(value, g.config.APIKey, "***")
	value = strings.ReplaceAll(value, "Bearer "+g.config.APIKey, "Bearer ***")
	return truncate(value, 500)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
