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
	"strconv"
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

func TestGatewaySearchDatasetPreservesFastGPTMixedRecallOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"data":{"list":[{"id":"data-first","datasetId":"dataset-1","collectionId":"c1","q":"最终排序第一","a":"第一条","score":0.72},{"id":"data-second","datasetId":"dataset-1","collectionId":"c1","q":"单项分数更高","a":"第二条","score":0.95},{"id":"data-filtered","datasetId":"dataset-1","collectionId":"c1","q":"低于阈值","a":"忽略","score":0.1}]}}`)
	}))
	defer server.Close()

	gateway, _ := NewGateway(Config{BaseURL: server.URL, APIKey: "secret", Timeout: time.Second})
	result, err := gateway.SearchDataset(context.Background(), SearchDatasetRequest{
		DatasetID: "dataset-1", Query: "餐饮", Similarity: 0.2, TopK: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 || result.Hits[0].DataID != "data-first" || result.Hits[1].DataID != "data-second" {
		t.Fatalf("FastGPT order was not preserved: %#v", result.Hits)
	}
}

func TestGatewayManagedIntegrationUsesServiceTokenAndStoreScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Agent-Desk-Token") != "integration-secret" || r.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected auth headers: %#v", r.Header)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/api/integration/agent-desk/tenant/ensure":
			if payload["externalStoreId"] != "7" || payload["teamName"] != "南七店" {
				t.Fatalf("tenant payload=%#v", payload)
			}
			_, _ = io.WriteString(w, `{"code":200,"data":{"externalStoreId":"7","teamId":"team-7","teamName":"南七店","status":"active"}}`)
		case "/api/integration/agent-desk/dataset/search":
			if payload["externalStoreId"] != "7" || payload["datasetId"] != "dataset-7" || payload["tokenLimit"] != float64(400) || payload["searchMode"] != "mixedRecall" || payload["useRerank"] != true || payload["topK"] != float64(5) {
				t.Fatalf("search payload=%#v", payload)
			}
			_, _ = io.WriteString(w, `{"code":200,"data":{"datasetId":"dataset-7","list":[{"id":"data-7","datasetId":"dataset-7","q":"停车","a":"地下停车场","score":0.8}]}}`)
		case "/api/integration/agent-desk/dataset/profile":
			if payload["externalStoreId"] != "7" || payload["datasetId"] != "dataset-7" {
				t.Fatalf("profile payload=%#v", payload)
			}
			_, _ = io.WriteString(w, `{"code":200,"data":{"datasetId":"dataset-7","datasetModelProfileId":"profile-7","profileName":"南七默认模型","profileRevision":3,"profileStatus":"configured","fingerprint":{"embedding":"e1","documentParser":"d1"}}}`)
		case "/api/integration/agent-desk/dataset/collections":
			if payload["externalStoreId"] != "7" || payload["datasetId"] != "dataset-7" {
				t.Fatalf("collections payload=%#v", payload)
			}
			_, _ = io.WriteString(w, `{"code":200,"data":{"collections":[{"collectionId":"collection-7","name":"前厅资料.pdf","type":"file","dataAmount":12,"trainingAmount":2,"forbid":false}]}}`)
		case "/api/integration/agent-desk/usage/list":
			if payload["externalStoreId"] != "7" || payload["datasetId"] != "dataset-7" || payload["limit"] != float64(100) {
				t.Fatalf("usage payload=%#v", payload)
			}
			_, _ = io.WriteString(w, `{"code":200,"data":{"events":[{"externalEventId":"model:7","kind":"model","stage":"embedding","provider":"dashscope","model":"text-embedding-v4","profileId":"profile-7","profileRevision":3,"promptTokens":18,"completionTokens":0,"cachedTokens":0,"latencyMs":42,"status":"success"}],"nextCursor":"opaque-cursor"}}`)
		case "/api/integration/agent-desk/dataset/model-profile/detail":
			if payload["externalStoreId"] != "7" || payload["datasetId"] != "dataset-7" {
				t.Fatalf("model profile detail payload=%#v", payload)
			}
			_, _ = io.WriteString(w, `{"code":200,"data":{"datasetId":"dataset-7","profile":{"_id":"profile-7","name":"南七知识库模型","revision":4,"embedding":{"provider":"openai","baseUrl":"https://embedding.example/v1","model":"embedding-v4","keyConfigured":true,"keyFingerprint":"emb-1"},"documentParser":{"provider":"openai","baseUrl":"https://chat.example/v1","model":"chat-pro","keyConfigured":true,"keyFingerprint":"doc-1"},"vision":{"provider":"openai","baseUrl":"https://vision.example/v1","model":"vision-plus","keyConfigured":true,"keyFingerprint":"vis-1"}}}}`)
		case "/api/integration/agent-desk/dataset/model-profile/test":
			if payload["externalStoreId"] != "7" || payload["datasetId"] != "dataset-7" || payload["profileId"] != "profile-7" || payload["rerank"] != nil {
				t.Fatalf("model profile test payload=%#v", payload)
			}
			if embedding, ok := payload["embedding"].(map[string]any); !ok || embedding["apiKey"] != nil {
				t.Fatalf("saved key should be reused without exposing it: %#v", payload["embedding"])
			}
			_, _ = io.WriteString(w, `{"code":200,"data":{"testToken":"tested-token","expiresAt":"2026-07-18T10:00:00Z","results":[{"stage":"embedding","status":"success","promptTokens":2,"completionTokens":0},{"stage":"documentParser","status":"success","promptTokens":3,"completionTokens":1},{"stage":"vision","status":"success","promptTokens":4,"completionTokens":1}]}}`)
		case "/api/integration/agent-desk/dataset/model-profile/upsert":
			if payload["externalStoreId"] != "7" || payload["datasetId"] != "dataset-7" || payload["profileId"] != "profile-7" || payload["testToken"] != "tested-token" || payload["rerank"] != nil {
				t.Fatalf("model profile upsert payload=%#v", payload)
			}
			_, _ = io.WriteString(w, `{"code":200,"data":{"profile":{"_id":"profile-7","name":"南七知识库模型","revision":5,"embedding":{"provider":"openai","baseUrl":"https://embedding.example/v1","model":"embedding-v4","keyConfigured":true,"keyFingerprint":"emb-1"},"documentParser":{"provider":"openai","baseUrl":"https://chat.example/v1","model":"chat-pro","keyConfigured":true,"keyFingerprint":"doc-1"},"vision":{"provider":"openai","baseUrl":"https://vision.example/v1","model":"vision-plus","keyConfigured":true,"keyFingerprint":"vis-1"}},"boundDatasetCount":2}}`)
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	gateway, err := NewGateway(Config{BaseURL: server.URL, IntegrationToken: "integration-secret", UseIntegration: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	scoped := gateway.ForStore(7)
	tenant, err := scoped.EnsureStoreTenant(context.Background(), "南七店")
	if err != nil || tenant.TeamID != "team-7" {
		t.Fatalf("tenant=%#v err=%v", tenant, err)
	}
	result, err := scoped.SearchDataset(context.Background(), SearchDatasetRequest{DatasetID: "dataset-7", Query: "停车在哪", TokenLimit: 400, SearchMode: "mixedRecall", UseRerank: true, TopK: 5})
	if err != nil || len(result.Hits) != 1 || result.Hits[0].DatasetID != "dataset-7" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	profile, err := scoped.GetDatasetProfileSnapshot(context.Background(), "dataset-7")
	if err != nil || profile.ProfileID != "profile-7" || profile.ProfileName != "南七默认模型" || profile.ProfileRevision != "3" || profile.Fingerprint != "embedding:e1,documentParser:d1" {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	collections, err := scoped.ListCollections(context.Background(), "dataset-7")
	if err != nil || len(collections) != 1 || collections[0].ID != "collection-7" || collections[0].DataAmount != 12 || collections[0].TrainingAmount != 2 {
		t.Fatalf("collections=%#v err=%v", collections, err)
	}
	usage, err := scoped.ListUsageEvents(context.Background(), "dataset-7", "", 100)
	if err != nil || usage.NextCursor != "opaque-cursor" || len(usage.Events) != 1 || usage.Events[0].ExternalEventID != "model:7" || usage.Events[0].ProfileRevision != 3 || usage.Events[0].PromptTokens != 18 {
		t.Fatalf("usage=%#v err=%v", usage, err)
	}
	modelProfile, err := scoped.GetModelProfile(context.Background(), "dataset-7")
	if err != nil || modelProfile == nil || modelProfile.ID != "profile-7" || modelProfile.Embedding.KeyFingerprint != "emb-1" {
		t.Fatalf("modelProfile=%#v err=%v", modelProfile, err)
	}
	modelInput := ModelProfileInput{
		DatasetID: "dataset-7", ProfileID: "profile-7", Name: "南七知识库模型",
		Embedding:      ModelCredential{Provider: "openai", BaseURL: "https://embedding.example/v1", Model: "embedding-v4", KeyConfigured: true},
		DocumentParser: ModelCredential{Provider: "openai", BaseURL: "https://chat.example/v1", Model: "chat-pro", KeyConfigured: true},
		Vision:         ModelCredential{Provider: "openai", BaseURL: "https://vision.example/v1", Model: "vision-plus", KeyConfigured: true},
		DisableRerank:  true,
	}
	testResult, err := scoped.TestModelProfile(context.Background(), modelInput)
	if err != nil || testResult.TestToken != "tested-token" || len(testResult.Results) != 3 {
		t.Fatalf("testResult=%#v err=%v", testResult, err)
	}
	modelInput.TestToken = testResult.TestToken
	saved, err := scoped.UpsertModelProfile(context.Background(), modelInput)
	if err != nil || saved.Profile.Revision != 5 || saved.BoundDatasetCount != 2 {
		t.Fatalf("saved=%#v err=%v", saved, err)
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
	if err := gateway.DeleteCollections(ctx, datasetID, []string{collectionID}); err != nil {
		t.Fatal(err)
	}
	if err := gateway.DeleteDataset(ctx, datasetID); err != nil {
		t.Fatal(err)
	}
	datasetID = ""
}

// This is deliberately opt-in: it exercises the dedicated Agent Desk
// service credential against a candidate FastGPT environment and creates only
// a temporary Dataset under the selected Store's managed Team.
func TestGatewayManagedIntegrationLifecycle(t *testing.T) {
	if strings.TrimSpace(os.Getenv("FASTGPT_MANAGED_INTEGRATION_LIFECYCLE")) != "1" {
		t.Skip("managed FastGPT integration lifecycle is not enabled")
	}
	baseURL := firstNonBlank(os.Getenv("FASTGPT_MANAGED_INTEGRATION_BASE_URL"), os.Getenv("AGENT_DESK_FASTGPT_BASE_URL"))
	token := firstNonBlank(os.Getenv("FASTGPT_MANAGED_INTEGRATION_TOKEN"), os.Getenv("AGENT_DESK_FASTGPT_INTEGRATION_TOKEN"))
	storeIDRaw := strings.TrimSpace(os.Getenv("FASTGPT_MANAGED_INTEGRATION_STORE_ID"))
	teamName := firstNonBlank(os.Getenv("FASTGPT_MANAGED_INTEGRATION_TEAM_NAME"), "Agent Desk 受控集成验证门店")
	if baseURL == "" || token == "" || storeIDRaw == "" {
		t.Skip("managed FastGPT integration environment is not configured")
	}
	storeID, err := strconv.ParseInt(storeIDRaw, 10, 64)
	if err != nil || storeID <= 0 {
		t.Fatalf("invalid FASTGPT_MANAGED_INTEGRATION_STORE_ID=%q", storeIDRaw)
	}

	gateway, err := NewGateway(Config{
		BaseURL: baseURL, IntegrationToken: token, UseIntegration: true,
		Timeout: 30 * time.Second, MaxRetries: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	scoped := gateway.ForStore(storeID)
	tenant, err := scoped.EnsureStoreTenant(ctx, teamName)
	if err != nil || tenant.TeamID == "" || tenant.ExternalStoreID != storeIDRaw {
		t.Fatalf("tenant=%#v err=%v", tenant, err)
	}
	dataset, err := scoped.CreateDataset(ctx, "agent-desk-managed-integration-"+time.Now().Format("20060102-150405"), "temporary service integration test")
	if err != nil {
		t.Fatal(err)
	}
	datasetID := dataset.ID
	if strings.TrimSpace(os.Getenv("FASTGPT_MANAGED_INTEGRATION_KEEP_DATA")) != "1" {
		t.Cleanup(func() {
			if datasetID != "" {
				_ = scoped.DeleteDataset(context.Background(), datasetID)
			}
		})
	}
	collectionID, err := scoped.UploadLocalFile(ctx, datasetID, "agent-desk-managed-integration.txt", strings.NewReader("Agent Desk 受控集成验证。暗号是门店 Team 隔离成功。"))
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
		collections, listErr := scoped.ListCollections(ctx, datasetID)
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
	result, err := scoped.SearchDataset(ctx, SearchDatasetRequest{
		DatasetID: datasetID, Query: "门店 Team 隔离的暗号是什么", TokenLimit: 400,
		SearchMode: "mixedRecall", UseRerank: true, TopK: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DatasetID != datasetID || len(result.Hits) == 0 || !strings.Contains(result.Hits[0].Question+result.Hits[0].Answer, "门店 Team 隔离成功") {
		t.Fatalf("unexpected managed search result=%#v", result)
	}
	if err := scoped.DeleteCollections(ctx, datasetID, []string{collectionID}); err != nil {
		t.Fatal(err)
	}
	if err := scoped.DeleteDataset(ctx, datasetID); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(os.Getenv("FASTGPT_MANAGED_INTEGRATION_KEEP_DATA")) != "1" {
		datasetID = ""
	}
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
