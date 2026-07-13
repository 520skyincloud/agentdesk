package fastgpt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatewaySearchDatasetMapsAndFiltersOfficialResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/core/dataset/searchTest" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("authorization missing")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["datasetId"] != "dataset-1" || payload["searchMode"] != "mixedRecall" || payload["datasetSearchUsingExtensionQuery"] != false {
			t.Fatalf("payload=%#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"data":[{"id":"data-low","datasetId":"dataset-1","collectionId":"c1","sourceName":"guide.md","q":"低分","a":"忽略","score":0.2},{"id":"data-high","datasetId":"dataset-1","collectionId":"c2","sourceName":"hotel.xlsx","q":"停车在哪里","a":"从繁华大道辅路进入","score":0.91}]}`)
	}))
	defer server.Close()

	gateway, err := NewGateway(Config{BaseURL: server.URL, APIKey: "secret", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := gateway.SearchDataset(context.Background(), SearchDatasetRequest{
		DatasetID: "dataset-1", Query: "停车在哪里", TokenLimit: 3000,
		Similarity: 0.5, SearchMode: "mixedRecall", UseRerank: true, TopK: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].DataID != "data-high" || result.Hits[0].Question != "停车在哪里" {
		t.Fatalf("result=%#v", result)
	}
}

func TestGatewaySearchDatasetMapsFastGPTScoreStages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"data":{"list":[{"id":"data-1","datasetId":"dataset-1","collectionId":"c1","sourceName":"hotel.xlsx","q":"有剃须刀吗","a":"有","score":[{"type":"embedding","value":0.92},{"type":"rrf","value":0.016},{"type":"reRank","value":0.87}]}]}}`)
	}))
	defer server.Close()
	gateway, _ := NewGateway(Config{BaseURL: server.URL, APIKey: "secret", Timeout: time.Second})
	result, err := gateway.SearchDataset(context.Background(), SearchDatasetRequest{DatasetID: "dataset-1", Query: "剃须刀", Similarity: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Score != 0.87 {
		t.Fatalf("hits=%#v", result.Hits)
	}
}

func TestGatewaySearchDatasetPrefersFullTextScoreOverRRF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"data":{"list":[{"id":"data-1","datasetId":"dataset-1","collectionId":"c1","sourceName":"hotel.xlsx","q":"有剃须刀吗","a":"洗衣房自取","score":[{"type":"fullText","value":0.8382},{"type":"rrf","value":0.0081}]}]}}`)
	}))
	defer server.Close()
	gateway, _ := NewGateway(Config{BaseURL: server.URL, APIKey: "secret", Timeout: time.Second})
	result, err := gateway.SearchDataset(context.Background(), SearchDatasetRequest{DatasetID: "dataset-1", Query: "剃须刀", Similarity: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Score != 0.8382 {
		t.Fatalf("hits=%#v", result.Hits)
	}
}

func TestGatewaySearchDatasetIntegration(t *testing.T) {
	baseURL := firstNonBlank(os.Getenv("FASTGPT_INTEGRATION_BASE_URL"), os.Getenv("AGENT_DESK_FASTGPT_BASE_URL"))
	apiKey := firstNonBlank(os.Getenv("FASTGPT_INTEGRATION_API_KEY"), os.Getenv("AGENT_DESK_FASTGPT_API_KEY"))
	datasetID := strings.TrimSpace(os.Getenv("FASTGPT_INTEGRATION_DATASET_ID"))
	if baseURL == "" || apiKey == "" || datasetID == "" {
		t.Skip("FastGPT integration environment is not configured")
	}
	gateway, err := NewGateway(Config{BaseURL: baseURL, APIKey: apiKey, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := gateway.SearchDataset(context.Background(), SearchDatasetRequest{
		DatasetID: datasetID, Query: "酒店有剃须刀吗", TokenLimit: 400,
		SearchMode: "mixedRecall", UseRerank: true, TopK: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DatasetID != datasetID || len(result.Hits) == 0 {
		t.Fatalf("dataset=%s hits=%d", result.DatasetID, len(result.Hits))
	}
	for _, hit := range result.Hits {
		if hit.DatasetID != datasetID || hit.DataID == "" {
			t.Fatalf("invalid hit=%#v", hit)
		}
	}
}

func TestGatewayDatasetLifecycleIntegration(t *testing.T) {
	if strings.TrimSpace(os.Getenv("FASTGPT_INTEGRATION_LIFECYCLE")) != "1" {
		t.Skip("FastGPT lifecycle integration is not enabled")
	}
	baseURL := strings.TrimSpace(os.Getenv("FASTGPT_INTEGRATION_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("FASTGPT_INTEGRATION_API_KEY"))
	if baseURL == "" || apiKey == "" {
		t.Skip("FastGPT integration environment is not configured")
	}
	gateway, err := NewGateway(Config{BaseURL: baseURL, APIKey: apiKey, Timeout: 30 * time.Second, MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dataset, err := gateway.CreateDataset(ctx, "agent-desk-gateway-integration-"+time.Now().Format("20060102-150405"), "temporary integration test")
	if err != nil {
		t.Fatal(err)
	}
	datasetID := dataset.ID
	t.Cleanup(func() {
		if datasetID != "" {
			_ = gateway.DeleteDataset(context.Background(), datasetID)
		}
	})
	collectionID, err := gateway.UploadLocalFile(ctx, datasetID, "agent-desk-integration.txt", strings.NewReader("Agent Desk 网关生命周期验证。暗号是南七网关验证成功。"))
	if err != nil {
		t.Fatal(err)
	}
	ready := false
	for !ready {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
		collections, listErr := gateway.ListCollections(ctx, datasetID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, collection := range collections {
			if collection.ID == collectionID && collection.TrainingAmount == 0 && collection.DataAmount > 0 {
				ready = true
				break
			}
		}
	}
	result, err := gateway.SearchDataset(ctx, SearchDatasetRequest{
		DatasetID: datasetID, Query: "南七网关的暗号是什么", TokenLimit: 1000,
		SearchMode: "mixedRecall", UseRerank: true, TopK: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) == 0 || !strings.Contains(result.Hits[0].Question+result.Hits[0].Answer, "南七网关验证成功") {
		t.Fatalf("hits=%#v", result.Hits)
	}
	if err := gateway.DeleteCollections(ctx, []string{collectionID}); err != nil {
		t.Fatal(err)
	}
	if err := gateway.DeleteDataset(ctx, datasetID); err != nil {
		t.Fatal(err)
	}
	datasetID = ""
}

func TestGatewaySearchDatasetRejectsMismatchedDataset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"data":[{"id":"data-1","datasetId":"wrong-dataset","q":"q","a":"a","score":0.9}]}`)
	}))
	defer server.Close()
	gateway, _ := NewGateway(Config{BaseURL: server.URL, APIKey: "secret", Timeout: time.Second})
	if _, err := gateway.SearchDataset(context.Background(), SearchDatasetRequest{DatasetID: "dataset-1", Query: "q"}); err == nil {
		t.Fatal("expected dataset mismatch error")
	}
}

func TestGatewaySearchDatasetRetriesServerErrors(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `temporary`)
			return
		}
		_, _ = io.WriteString(w, `{"code":200,"data":[]}`)
	}))
	defer server.Close()
	gateway, _ := NewGateway(Config{BaseURL: server.URL, APIKey: "secret", Timeout: time.Second, MaxRetries: 1})
	if _, err := gateway.SearchDataset(context.Background(), SearchDatasetRequest{DatasetID: "dataset-1", Query: "q"}); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts=%d", attempts.Load())
	}
}

func TestGatewayRedactsAPIKeyFromUpstreamErrors(t *testing.T) {
	const apiKey = "fastgpt-super-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"invalid fastgpt-super-secret"}`)
	}))
	defer server.Close()
	gateway, _ := NewGateway(Config{BaseURL: server.URL, APIKey: apiKey, Timeout: time.Second})
	_, err := gateway.SearchDataset(context.Background(), SearchDatasetRequest{DatasetID: "dataset-1", Query: "q"})
	if err == nil || strings.Contains(err.Error(), apiKey) || !strings.Contains(err.Error(), "***") {
		t.Fatalf("expected redacted error, got %v", err)
	}
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected HTTPStatusError, got %T", err)
	}
}

func TestGatewayCreateDatasetAllowsFastGPTSystemDefaultModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["vectorModel"]; ok {
			t.Fatalf("unexpected vector model: %#v", payload)
		}
		if _, ok := payload["agentModel"]; ok {
			t.Fatalf("unexpected agent model: %#v", payload)
		}
		_, _ = io.WriteString(w, `{"code":200,"data":"dataset-1"}`)
	}))
	defer server.Close()
	gateway, _ := NewGateway(Config{BaseURL: server.URL, APIKey: "secret", Timeout: time.Second})
	dataset, err := gateway.CreateDataset(context.Background(), "门店知识库", "")
	if err != nil || dataset.ID != "dataset-1" {
		t.Fatalf("dataset=%#v err=%v", dataset, err)
	}
}

func TestGatewayDeleteDatasetUsesOfficialEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/core/dataset/delete" || r.URL.Query().Get("id") != "dataset-1" {
			t.Fatalf("request=%s %s", r.Method, r.URL.String())
		}
		_, _ = io.WriteString(w, `{"code":200,"data":null}`)
	}))
	defer server.Close()
	gateway, _ := NewGateway(Config{BaseURL: server.URL, APIKey: "secret", Timeout: time.Second})
	if err := gateway.DeleteDataset(context.Background(), "dataset-1"); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayListCollectionsPaginates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Offset int `json:"offset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Offset == 0 {
			items := make([]map[string]any, 0, 30)
			for index := 0; index < 30; index++ {
				items = append(items, map[string]any{"_id": fmt.Sprintf("collection-%d", index)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"list": items}})
			return
		}
		_, _ = io.WriteString(w, `{"code":200,"data":{"list":[{"_id":"collection-30"}]}}`)
	}))
	defer server.Close()
	gateway, _ := NewGateway(Config{BaseURL: server.URL, APIKey: "secret", Timeout: time.Second})
	collections, err := gateway.ListCollections(context.Background(), "dataset-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 31 || collections[30].ID != "collection-30" {
		t.Fatalf("collections=%#v", collections)
	}
}
