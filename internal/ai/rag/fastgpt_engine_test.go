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

	results, _, err := Retrieve.retrieveFastGPTKnowledge(context.Background(), RetrieveRequest{Query: "有剃须刀吗", ContextMaxTokens: 2000}, []models.KnowledgeBase{
		{ID: 3, DatasetID: "dataset-1", DefaultTopK: 5, DefaultScoreThreshold: 0.2, DefaultRerankLimit: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results=%#v", results)
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
