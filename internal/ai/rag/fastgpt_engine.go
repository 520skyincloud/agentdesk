package rag

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
)

const (
	fastGPTDefaultContextTokens = 400
	fastGPTMaxContextTokens     = 4000
)

var (
	fastGPTMarkdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\((https?://[^)\s]+)\)`)
	fastGPTBareImagePattern     = regexp.MustCompile(`https?://[^\s<>"']+(?i:\.(?:png|jpe?g|gif|webp))(?:\?[^\s<>"']*)?`)
)

type FastGPTSyncSource struct {
	SourceRecordID string
	Title          string
	Description    string
	Resources      []FastGPTSyncResource
}

type FastGPTSyncResource struct {
	SourceURL   string
	Title       string
	Description string
	SortNo      int
}

func newPlatformFastGPTGateway() (*fastgptapi.Gateway, error) {
	cfg := config.Current().FastGPT
	if !cfg.Enabled {
		return nil, fmt.Errorf("platform FastGPT connection engine is disabled")
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}
	return fastgptapi.NewGateway(fastgptapi.Config{
		BaseURL: strings.TrimSpace(cfg.BaseURL), APIKey: strings.TrimSpace(cfg.APIKey),
		Timeout: timeout, MaxRetries: maxRetries,
		VectorModel: strings.TrimSpace(cfg.VectorModel), AgentModel: strings.TrimSpace(cfg.AgentModel), VLMModel: strings.TrimSpace(cfg.VLMModel),
	})
}

func isFastGPTKnowledgeBase(knowledgeBase models.KnowledgeBase) bool {
	return knowledgeBase.KnowledgeType == string(enums.KnowledgeBaseTypeFastGPTCloud) ||
		knowledgeBase.ChunkProvider == string(enums.KnowledgeChunkProviderFastGPT)
}

func splitFastGPTKnowledgeBases(knowledgeBases []models.KnowledgeBase) ([]models.KnowledgeBase, []models.KnowledgeBase) {
	local := make([]models.KnowledgeBase, 0, len(knowledgeBases))
	fastGPT := make([]models.KnowledgeBase, 0, len(knowledgeBases))
	for _, knowledgeBase := range knowledgeBases {
		if isFastGPTKnowledgeBase(knowledgeBase) {
			fastGPT = append(fastGPT, knowledgeBase)
			continue
		}
		local = append(local, knowledgeBase)
	}
	return local, fastGPT
}

func (s *retrieve) retrieveFastGPTKnowledge(ctx context.Context, req RetrieveRequest, knowledgeBases []models.KnowledgeBase) ([]RetrieveResult, int64, error) {
	if strings.TrimSpace(req.Query) == "" || len(knowledgeBases) == 0 {
		return nil, 0, nil
	}
	gateway, err := newPlatformFastGPTGateway()
	if err != nil {
		return nil, 0, err
	}
	startedAt := time.Now()
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		results  []RetrieveResult
		errCount int
	)
	for _, knowledgeBase := range knowledgeBases {
		knowledgeBase := knowledgeBase
		wg.Add(1)
		go func() {
			defer wg.Done()
			datasetID := strings.TrimSpace(knowledgeBase.DatasetID)
			if datasetID == "" {
				mu.Lock()
				errCount++
				mu.Unlock()
				return
			}
			topK, scoreThreshold := resolveKnowledgeBaseSearchOptions(req, &knowledgeBase)
			searchResult, searchErr := gateway.SearchDataset(ctx, fastgptapi.SearchDatasetRequest{
				DatasetID: datasetID, Query: req.Query, TokenLimit: resolveFastGPTTokenLimit(req.ContextMaxTokens),
				Similarity: float64(scoreThreshold), SearchMode: "mixedRecall",
				UseRerank: knowledgeBase.DefaultRerankLimit > 0, TopK: topK,
			})
			if searchErr != nil {
				slog.Warn("FastGPT knowledge lookup failed", "knowledge_base_id", knowledgeBase.ID, "dataset_id", datasetID, "error", searchErr)
				mu.Lock()
				errCount++
				mu.Unlock()
				return
			}
			mapped := make([]RetrieveResult, 0, len(searchResult.Hits))
			for _, hit := range searchResult.Hits {
				mapped = append(mapped, buildFastGPTRetrieveResult(knowledgeBase, hit))
			}
			mu.Lock()
			results = append(results, mapped...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sortRetrieveResults(results)
	if len(results) == 0 && errCount > 0 {
		return nil, time.Since(startedAt).Milliseconds(), fmt.Errorf("FastGPT knowledge lookup failed")
	}
	return results, time.Since(startedAt).Milliseconds(), nil
}

func resolveFastGPTTokenLimit(contextMaxTokens int) int {
	configuredLimit := config.Current().FastGPT.RetrievalTokenLimit
	if configuredLimit <= 0 {
		configuredLimit = fastGPTDefaultContextTokens
	}
	if configuredLimit > fastGPTMaxContextTokens {
		configuredLimit = fastGPTMaxContextTokens
	}
	if contextMaxTokens > 0 && contextMaxTokens < configuredLimit {
		return contextMaxTokens
	}
	return configuredLimit
}

func buildFastGPTRetrieveResult(knowledgeBase models.KnowledgeBase, hit fastgptapi.SearchDatasetHit) RetrieveResult {
	contentParts := make([]string, 0, 2)
	if question := strings.TrimSpace(hit.Question); question != "" {
		contentParts = append(contentParts, "问题："+question)
	}
	if answer := stripFastGPTImageURLs(hit.Answer); answer != "" {
		contentParts = append(contentParts, "答案："+answer)
	}
	return RetrieveResult{
		KnowledgeBaseID: knowledgeBase.ID,
		DocumentTitle:   strings.TrimSpace(hit.SourceName),
		Title:           strings.TrimSpace(hit.Question),
		SectionPath:     fmt.Sprintf("FastGPT知识库/%d/%s", knowledgeBase.ID, strings.TrimSpace(hit.CollectionID)),
		Content:         strings.Join(contentParts, "\n"),
		SourceRecordID:  strings.TrimSpace(hit.DataID),
		Score:           float32(hit.Score),
		ChunkType:       string(enums.KnowledgeChunkTypeText),
	}
}

// FetchFastGPTSyncSource resolves image references from a FastGPT source record.
// The caller still applies the configured trusted-host policy before downloading.
func FetchFastGPTSyncSource(ctx context.Context, knowledgeBase models.KnowledgeBase, query string) (FastGPTSyncSource, error) {
	if !isFastGPTKnowledgeBase(knowledgeBase) {
		return FastGPTSyncSource{}, fmt.Errorf("knowledge base is not a FastGPT source")
	}
	gateway, err := newPlatformFastGPTGateway()
	if err != nil {
		return FastGPTSyncSource{}, err
	}
	topK, scoreThreshold := resolveKnowledgeBaseSearchOptions(RetrieveRequest{}, &knowledgeBase)
	if topK < 10 {
		topK = 10
	}
	result, err := gateway.SearchDataset(ctx, fastgptapi.SearchDatasetRequest{
		DatasetID: strings.TrimSpace(knowledgeBase.DatasetID), Query: strings.TrimSpace(query),
		TokenLimit: resolveFastGPTTokenLimit(0), Similarity: float64(scoreThreshold),
		SearchMode: "mixedRecall", UseRerank: knowledgeBase.DefaultRerankLimit > 0, TopK: topK,
	})
	if err != nil {
		return FastGPTSyncSource{}, err
	}
	for _, hit := range result.Hits {
		resources := extractFastGPTImageResources(hit.Answer)
		if strings.TrimSpace(hit.DataID) == "" || len(resources) == 0 {
			continue
		}
		return FastGPTSyncSource{
			SourceRecordID: strings.TrimSpace(hit.DataID),
			Title:          firstFastGPTValue(hit.Question, hit.SourceName, knowledgeBase.Name),
			Description:    stripFastGPTImageURLs(hit.Answer),
			Resources:      resources,
		}, nil
	}
	return FastGPTSyncSource{}, fmt.Errorf("FastGPT did not return a source record with image resources")
}

func extractFastGPTImageResources(content string) []FastGPTSyncResource {
	urls := make([]string, 0)
	seen := map[string]bool{}
	appendURL := func(rawURL string) {
		rawURL = strings.TrimRight(strings.TrimSpace(rawURL), ".,;，。；")
		if rawURL == "" || seen[rawURL] {
			return
		}
		seen[rawURL] = true
		urls = append(urls, rawURL)
	}
	for _, match := range fastGPTMarkdownImagePattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			appendURL(match[1])
		}
	}
	for _, rawURL := range fastGPTBareImagePattern.FindAllString(content, -1) {
		appendURL(rawURL)
	}
	resources := make([]FastGPTSyncResource, 0, len(urls))
	for index, rawURL := range urls {
		resources = append(resources, FastGPTSyncResource{SourceURL: rawURL, Title: fmt.Sprintf("图片%d", index+1), SortNo: index + 1})
	}
	return resources
}

func stripFastGPTImageURLs(content string) string {
	content = fastGPTMarkdownImagePattern.ReplaceAllString(content, "")
	content = fastGPTBareImagePattern.ReplaceAllString(content, "")
	lines := strings.Split(content, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

func firstFastGPTValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
