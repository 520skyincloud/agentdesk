package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

const (
	fastGPTCloudBackendBaseURLEnv = "FASTGPT_CLOUD_BACKEND_BASE_URL"
	fastGPTCloudDefaultBaseURL    = "https://ht.iloveu2.cn"
	fastGPTCloudRequestTimeout    = 6 * time.Second
)

type fastGPTCloudKnowledgeConfig struct {
	BaseURL     string `json:"baseUrl"`
	Endpoint    string `json:"endpoint"`
	DatasetID   string `json:"datasetId"`
	DatasetName string `json:"datasetName"`
}

type fastGPTCloudResponse struct {
	OK     bool               `json:"ok"`
	Result fastGPTCloudResult `json:"result"`
	Source string             `json:"source"`
}

type fastGPTCloudResult struct {
	Hit             bool                `json:"hit"`
	Answer          string              `json:"answer"`
	MatchedQuestion string              `json:"matched_question"`
	Score           float64             `json:"score"`
	Route           string              `json:"route"`
	DatasetID       string              `json:"dataset_id"`
	DatasetName     string              `json:"dataset_name"`
	Quotes          []fastGPTCloudQuote `json:"quotes"`
}

type fastGPTCloudQuote struct {
	Question   string `json:"q"`
	Answer     string `json:"a"`
	SourceName string `json:"sourceName"`
	DatasetID  string `json:"datasetId"`
}

func isFastGPTCloudKnowledgeBase(knowledgeBase models.KnowledgeBase) bool {
	return knowledgeBase.KnowledgeType == string(enums.KnowledgeBaseTypeFastGPTCloud) ||
		knowledgeBase.ChunkProvider == string(enums.KnowledgeChunkProviderFastGPT)
}

func splitFastGPTCloudKnowledgeBases(knowledgeBases []models.KnowledgeBase) ([]models.KnowledgeBase, []models.KnowledgeBase) {
	if len(knowledgeBases) == 0 {
		return nil, nil
	}
	local := make([]models.KnowledgeBase, 0, len(knowledgeBases))
	cloud := make([]models.KnowledgeBase, 0)
	for _, knowledgeBase := range knowledgeBases {
		if isFastGPTCloudKnowledgeBase(knowledgeBase) {
			cloud = append(cloud, knowledgeBase)
			continue
		}
		local = append(local, knowledgeBase)
	}
	return local, cloud
}

func (s *retrieve) retrieveFastGPTCloudKnowledge(ctx context.Context, req RetrieveRequest, knowledgeBases []models.KnowledgeBase) ([]RetrieveResult, int64, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" || len(knowledgeBases) == 0 {
		return nil, 0, nil
	}

	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(ctx, fastGPTCloudRequestTimeout)
	defer cancel()

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
			result, err := fetchFastGPTCloudKnowledge(ctx, knowledgeBase, query)
			if err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
				slog.Warn("FastGPT cloud knowledge lookup failed",
					"knowledge_base_id", knowledgeBase.ID,
					"knowledge_base_name", knowledgeBase.Name,
					"error", err,
				)
				return
			}
			if !result.Hit || strings.TrimSpace(result.Answer) == "" {
				return
			}
			mu.Lock()
			results = append(results, buildFastGPTCloudRetrieveResult(knowledgeBase, result))
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].KnowledgeBaseID < results[j].KnowledgeBaseID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) == 0 && errCount > 0 {
		return nil, time.Since(startedAt).Milliseconds(), fmt.Errorf("fastgpt cloud knowledge lookup failed")
	}
	return results, time.Since(startedAt).Milliseconds(), nil
}

func fetchFastGPTCloudKnowledge(ctx context.Context, knowledgeBase models.KnowledgeBase, query string) (fastGPTCloudResult, error) {
	cfg := resolveFastGPTCloudKnowledgeConfig(knowledgeBase)
	requestURL := strings.TrimRight(cfg.BaseURL, "/") + cfg.Endpoint + "?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fastGPTCloudResult{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fastGPTCloudResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fastGPTCloudResult{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	payload := fastGPTCloudResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fastGPTCloudResult{}, err
	}
	if !payload.OK {
		return fastGPTCloudResult{}, fmt.Errorf("fastgpt cloud response not ok")
	}
	if payload.Result.DatasetID == "" {
		payload.Result.DatasetID = cfg.DatasetID
	}
	if payload.Result.DatasetName == "" {
		payload.Result.DatasetName = cfg.DatasetName
	}
	return payload.Result, nil
}

func resolveFastGPTCloudKnowledgeConfig(knowledgeBase models.KnowledgeBase) fastGPTCloudKnowledgeConfig {
	cfg := fastGPTCloudKnowledgeConfig{}
	raw := strings.TrimSpace(knowledgeBase.Remark)
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	if cfg.BaseURL == "" {
		cfg.BaseURL = strings.TrimSpace(os.Getenv(fastGPTCloudBackendBaseURLEnv))
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = fastGPTCloudDefaultBaseURL
	}
	cfg.Endpoint = normalizeFastGPTCloudEndpoint(cfg.Endpoint, knowledgeBase)
	return cfg
}

func normalizeFastGPTCloudEndpoint(endpoint string, knowledgeBase models.KnowledgeBase) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		text := knowledgeBase.Name + " " + knowledgeBase.Description + " " + knowledgeBase.Remark
		if strings.Contains(text, "公司") || strings.Contains(text, "品牌") ||
			strings.Contains(text, "6a1405c720c7c9214095cc4c") {
			endpoint = "/api/validate/company-fastgpt"
		} else {
			endpoint = "/api/validate/fastgpt"
		}
	}
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	return endpoint
}

func buildFastGPTCloudRetrieveResult(knowledgeBase models.KnowledgeBase, result fastGPTCloudResult) RetrieveResult {
	score := float32(result.Score)
	if score <= 0 {
		score = 0.99
	}
	title := strings.TrimSpace(result.DatasetName)
	if title == "" {
		title = strings.TrimSpace(knowledgeBase.Name)
	}
	content := buildFastGPTCloudContent(result)
	return RetrieveResult{
		KnowledgeBaseID: knowledgeBase.ID,
		DocumentTitle:   strings.TrimSpace(knowledgeBase.Name),
		Title:           title,
		SectionPath:     fmt.Sprintf("FastGPT云端知识库/%d/%s", knowledgeBase.ID, title),
		Content:         content,
		Score:           score,
		ChunkType:       string(enums.KnowledgeChunkTypeText),
	}
}

func buildFastGPTCloudContent(result fastGPTCloudResult) string {
	var b strings.Builder
	answer := strings.TrimSpace(result.Answer)
	if answer != "" {
		b.WriteString(answer)
	}
	if result.MatchedQuestion != "" {
		b.WriteString("\n匹配问题：")
		b.WriteString(strings.TrimSpace(result.MatchedQuestion))
	}
	for i, quote := range result.Quotes {
		if i >= 3 {
			break
		}
		question := strings.TrimSpace(quote.Question)
		answer := strings.TrimSpace(quote.Answer)
		if question == "" && answer == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("\n引用%d：", i+1))
		if question != "" {
			b.WriteString("Q: ")
			b.WriteString(question)
			b.WriteString(" ")
		}
		if answer != "" {
			b.WriteString("A: ")
			b.WriteString(answer)
		}
	}
	return strings.TrimSpace(b.String())
}
