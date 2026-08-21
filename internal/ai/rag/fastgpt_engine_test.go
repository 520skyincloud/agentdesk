package rag

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
)

func TestRetrieveFastGPTMapsQAToRetrieveResultWithoutFAQFields(t *testing.T) {
	requestedTokenLimit := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := struct {
			Limit int `json:"limit"`
		}{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requestedTokenLimit = payload.Limit
		_, _ = io.WriteString(w, `{"code":200,"data":[{"id":"data-1","datasetId":"dataset-1","collectionId":"collection-1","sourceName":"南七.xlsx","q":"有剃须刀吗","a":"前台自助柜可领取","score":0.88}]}`)
	}))
	defer server.Close()
	config.SetCurrent(&config.Config{FastGPT: config.FastGPTConfig{Enabled: true, BaseURL: server.URL, APIKey: "secret", TimeoutMS: 1000}})
	t.Cleanup(func() { config.SetCurrent(&config.Config{}) })

	results, _, failedKnowledgeBaseIDs, err := Retrieve.retrieveFastGPTKnowledge(context.Background(), RetrieveRequest{Query: "有剃须刀吗", ContextMaxTokens: 2000}, []models.KnowledgeBase{
		{ID: 3, DatasetID: "dataset-1", DefaultTopK: 5, DefaultScoreThreshold: 0.2, DefaultRerankLimit: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results=%#v", results)
	}
	if len(failedKnowledgeBaseIDs) != 0 {
		t.Fatalf("unexpected failed knowledge bases: %v", failedKnowledgeBaseIDs)
	}
	if requestedTokenLimit != fastGPTDefaultContextTokens {
		t.Fatalf("requested token limit=%d want %d", requestedTokenLimit, fastGPTDefaultContextTokens)
	}
	result := results[0]
	if result.SourceRecordID != "data-1" || result.DocumentTitle != "南七.xlsx" || result.Title != "有剃须刀吗" {
		t.Fatalf("result=%#v", result)
	}
	if result.FaqID != 0 || result.FaqQuestion != "" || result.Content != "问题：有剃须刀吗\n答案：前台自助柜可领取" {
		t.Fatalf("unexpected mapping=%#v", result)
	}
}

func TestRetrieveFastGPTReportsPartialKnowledgeBaseFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := struct {
			DatasetID string `json:"datasetId"`
		}{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.DatasetID == "store-dataset" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"code":502,"message":"store dataset unavailable"}`)
			return
		}
		_, _ = io.WriteString(w, `{"code":200,"data":[{"id":"general-1","datasetId":"general-dataset","collectionId":"general-collection","sourceName":"通用库.xlsx","q":"有布草一客一换吗","a":"是的","score":0.9}]}`)
	}))
	defer server.Close()
	config.SetCurrent(&config.Config{FastGPT: config.FastGPTConfig{Enabled: true, BaseURL: server.URL, APIKey: "secret", TimeoutMS: 1000, MaxRetries: 1}})
	t.Cleanup(func() { config.SetCurrent(&config.Config{}) })

	results, _, failedKnowledgeBaseIDs, err := Retrieve.retrieveFastGPTKnowledge(context.Background(), RetrieveRequest{Query: "有布草一客一换吗"}, []models.KnowledgeBase{
		{ID: 3, DatasetID: "store-dataset"},
		{ID: 4, DatasetID: "general-dataset"},
	})
	if err != nil {
		t.Fatalf("partial failure should preserve successful raw results: %v", err)
	}
	if len(results) != 1 || results[0].KnowledgeBaseID != 4 {
		t.Fatalf("successful general result missing: %#v", results)
	}
	if len(failedKnowledgeBaseIDs) != 1 || failedKnowledgeBaseIDs[0] != 3 {
		t.Fatalf("failed knowledge bases=%v, want [3]", failedKnowledgeBaseIDs)
	}
}

func TestFastGPTEngineExtractsImagesWithoutLeakingURLsIntoModelContext(t *testing.T) {
	answer := "从北门进入。\n![入口一](https://assets.example.com/one.png)\nhttps://assets.example.com/two.jpg"
	resources := extractFastGPTImageResources(answer)
	if len(resources) != 2 || resources[0].SourceURL != "https://assets.example.com/one.png" || resources[1].SourceURL != "https://assets.example.com/two.jpg" {
		t.Fatalf("resources=%#v", resources)
	}
	if got := stripFastGPTImageURLs(answer); got != "从北门进入。" {
		t.Fatalf("cleaned answer=%q", got)
	}
}

func TestFastGPTEngineStripsImagesFromChunkQuestion(t *testing.T) {
	result := buildFastGPTRetrieveResult(models.KnowledgeBase{ID: 7}, fastgptapi.SearchDatasetHit{
		DataID:       "data-7",
		CollectionID: "collection-7",
		Question:     "从北门进入。\nhttps://assets.example.com/entrance.png",
	})
	if result.Content != "问题：从北门进入。" {
		t.Fatalf("content=%q", result.Content)
	}
}

func TestFetchFastGPTSyncSourceReadsImagesFromChunkQuestion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"data":[{"id":"data-image-1","datasetId":"dataset-image-1","collectionId":"collection-image-1","sourceName":"guide.txt","q":"入口说明：https://assets.example.com/entrance.png","a":"","score":0.91}]}`)
	}))
	defer server.Close()
	config.SetCurrent(&config.Config{FastGPT: config.FastGPTConfig{Enabled: true, BaseURL: server.URL, APIKey: "secret", TimeoutMS: 1000}})
	t.Cleanup(func() { config.SetCurrent(&config.Config{}) })

	source, err := FetchFastGPTSyncSource(context.Background(), models.KnowledgeBase{
		ID:            7,
		DatasetID:     "dataset-image-1",
		KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud),
	}, "入口在哪里")
	if err != nil {
		t.Fatal(err)
	}
	if source.SourceRecordID != "data-image-1" || source.Description != "入口说明：" {
		t.Fatalf("source=%#v", source)
	}
	if len(source.Resources) != 1 || source.Resources[0].SourceURL != "https://assets.example.com/entrance.png" {
		t.Fatalf("resources=%#v", source.Resources)
	}
}
