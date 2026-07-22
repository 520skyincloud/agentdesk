package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/usagex"
)

type rerank struct{}

var Rerank = &rerank{}

func (s *rerank) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error) {
	if len(documents) == 0 {
		return nil, nil
	}

	if topN <= 0 {
		topN = len(documents)
	}

	results, err := s.callRerankAPI(ctx, query, documents, topN)
	if err != nil {
		return nil, err
	}

	return results, nil
}

func (s *rerank) callRerankAPI(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error) {
	config, err := ai.GetAIConfigForContext(ctx, enums.AIModelTypeRerank)
	if err != nil {
		return nil, err
	}
	return s.RerankWithConfig(ctx, *config, query, documents, topN)
}

func (s *rerank) RerankWithConfig(ctx context.Context, config models.AIConfig, query string, documents []string, topN int) ([]RerankResult, error) {
	results, _, err := s.RerankWithConfigAndUsage(ctx, config, query, documents, topN)
	return results, err
}

func (s *rerank) RerankWithConfigAndUsage(ctx context.Context, config models.AIConfig, query string, documents []string, topN int) ([]RerankResult, *RerankUsage, error) {
	if len(documents) == 0 {
		return nil, &RerankUsage{}, nil
	}
	if topN <= 0 {
		topN = len(documents)
	}

	reqBody := RerankRequest{
		Model:     config.ModelName,
		Query:     query,
		Documents: documents,
		TopN:      topN,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := strings.TrimRight(config.BaseURL, "/")
	if !strings.HasSuffix(strings.ToLower(endpoint), "/v1") {
		endpoint += "/v1"
	}
	callCtx, capture := usagex.WithCapture(ctx)
	startedAt := time.Now()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint+"/rerank", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.APIKey)

	timeout := time.Duration(config.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := usagex.NewHTTPClient(timeout)

	resp, err := client.Do(req)
	if err != nil {
		ai.RecordModelUsage(callCtx, ai.ModelUsageRecord{
			Stage: "rerank", OperationType: "rerank", Config: config,
			LatencyMS: time.Since(startedAt).Milliseconds(), Status: "failed",
			ErrorClass: "model_call_failed", Receipt: lastRerankReceipt(capture),
		})
		return nil, nil, fmt.Errorf("failed to call rerank API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		ai.RecordModelUsage(callCtx, ai.ModelUsageRecord{
			Stage: "rerank", OperationType: "rerank", Config: config,
			LatencyMS: time.Since(startedAt).Milliseconds(), Status: "failed",
			ErrorClass: "model_call_failed", Receipt: lastRerankReceipt(capture),
		})
		return nil, nil, fmt.Errorf("rerank api status %d", resp.StatusCode)
	}

	var rerankResp RerankResponse
	if err := json.Unmarshal(body, &rerankResp); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	usage := &RerankUsage{
		PromptTokens: rerankResp.Usage.PromptTokens, CompletionTokens: rerankResp.Usage.CompletionTokens,
		TotalTokens: rerankResp.Usage.TotalTokens,
	}
	results := make([]RerankResult, 0, len(rerankResp.Results))
	for _, r := range rerankResp.Results {
		results = append(results, RerankResult{
			Index:          r.Index,
			RelevanceScore: r.RelevanceScore,
		})
	}
	ai.RecordModelUsage(callCtx, ai.ModelUsageRecord{
		Stage: "rerank", OperationType: "rerank", Config: config,
		PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
		LatencyMS: time.Since(startedAt).Milliseconds(), Status: "completed",
		Receipt: lastRerankReceipt(capture),
	})

	return results, usage, nil
}

func lastRerankReceipt(capture *usagex.Capture) *usagex.Receipt {
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

func (s *rerank) RerankResults(ctx context.Context, query string, results []RetrieveResult, topN int) ([]RetrieveResult, error) {
	if len(results) == 0 {
		return nil, nil
	}

	if topN <= 0 {
		topN = len(results)
	}

	documents := make([]string, 0, len(results))
	for _, r := range results {
		documents = append(documents, r.Content)
	}

	rerankResults, err := s.Rerank(ctx, query, documents, topN)
	if err != nil {
		return nil, err
	}

	rerankedResults := make([]RetrieveResult, 0, len(rerankResults))
	for _, rr := range rerankResults {
		if rr.Index < len(results) {
			result := results[rr.Index]
			result.Score = float32(rr.RelevanceScore)
			rerankedResults = append(rerankedResults, result)
		}
	}

	return rerankedResults, nil
}

func (s *rerank) SimpleRerank(query string, results []RetrieveResult, topN int) []RetrieveResult {
	if len(results) == 0 {
		return nil
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if topN > 0 && len(results) > topN {
		return results[:topN]
	}

	return results
}
