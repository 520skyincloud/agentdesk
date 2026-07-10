package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-desk/internal/models"
)

func TestBuildFastGPTCloudRequestURLIncludesDataset(t *testing.T) {
	requestURL, err := buildFastGPTCloudRequestURL(fastGPTCloudKnowledgeConfig{
		BaseURL:     "https://example.test/",
		Endpoint:    "/api/validate/company-fastgpt",
		DatasetID:   "dataset-nanqi",
		DatasetName: "合肥南七店",
	}, "有没有剃须刀")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !strings.HasPrefix(requestURL, "https://example.test/api/validate/company-fastgpt?") {
		t.Fatalf("unexpected request url: %s", requestURL)
	}
	for _, want := range []string{
		"q=%E6%9C%89%E6%B2%A1%E6%9C%89%E5%89%83%E9%A1%BB%E5%88%80",
		"datasetId=dataset-nanqi",
		"dataset_id=dataset-nanqi",
		"datasetName=%E5%90%88%E8%82%A5%E5%8D%97%E4%B8%83%E5%BA%97",
	} {
		if !strings.Contains(requestURL, want) {
			t.Fatalf("expected url to contain %q, got %s", want, requestURL)
		}
	}
}

func TestFetchFastGPTCloudKnowledgeRejectsMismatchedDataset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("datasetId"); got != "dataset-nanqi" {
			t.Fatalf("expected datasetId to be forwarded, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(fastGPTCloudResponse{
			OK: true,
			Result: fastGPTCloudResult{
				Hit:         true,
				Answer:      "品牌手册内容不能进入南七知识库回答",
				Score:       0.9,
				DatasetID:   "dataset-brand",
				DatasetName: "品牌手册",
			},
		})
	}))
	defer server.Close()

	result, err := fetchFastGPTCloudKnowledge(context.Background(), models.KnowledgeBase{
		ID:   3,
		Name: "合肥南七店",
		Remark: `{
			"baseUrl":"` + server.URL + `",
			"endpoint":"/api/validate/company-fastgpt",
			"datasetId":"dataset-nanqi",
			"datasetName":"合肥南七店"
		}`,
	}, "有没有剃须刀")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Hit {
		t.Fatalf("expected mismatched dataset to be ignored, got hit result: %+v", result)
	}
}

func TestFetchFastGPTCloudKnowledgeAcceptsConfiguredDataset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(fastGPTCloudResponse{
			OK: true,
			Result: fastGPTCloudResult{
				Hit:         true,
				Answer:      "酒店提供一次性剃须刀。",
				Score:       0.9,
				DatasetID:   "dataset-nanqi",
				DatasetName: "合肥南七店",
			},
		})
	}))
	defer server.Close()

	result, err := fetchFastGPTCloudKnowledge(context.Background(), models.KnowledgeBase{
		ID:   3,
		Name: "合肥南七店",
		Remark: `{
			"baseUrl":"` + server.URL + `",
			"endpoint":"/api/validate/company-fastgpt",
			"datasetId":"dataset-nanqi",
			"datasetName":"合肥南七店"
		}`,
	}, "有没有剃须刀")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !result.Hit || result.DatasetID != "dataset-nanqi" {
		t.Fatalf("expected configured dataset result, got %+v", result)
	}
}
