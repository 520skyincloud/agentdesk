package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-desk/internal/pkg/config"
)

func TestFastGPTConnectorUsesOfficialDatasetEndpoints(t *testing.T) {
	requested := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer fastgpt-test" {
			t.Errorf("missing bearer auth")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/core/dataset/create":
			_, _ = io.WriteString(w, `{"code":200,"data":{"_id":"dataset-1","name":"门店库"}}`)
		case "/api/core/dataset/collection/create/localFile":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			if r.FormValue("data") == "" {
				t.Errorf("missing dataset form data")
			}
			_, _ = io.WriteString(w, `{"code":200,"data":{"collectionId":"collection-1","results":{"insertLen":1}}}`)
		case "/api/core/dataset/collection/listV2":
			_, _ = io.WriteString(w, `{"code":200,"data":{"list":[{"_id":"collection-1","name":"guide.md","dataAmount":3,"trainingAmount":0}]}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":404,"message":"not found"}`)
		}
	}))
	defer server.Close()

	config.SetCurrent(&config.Config{FastGPT: config.FastGPTConfig{
		Enabled: true, BaseURL: server.URL, APIKey: "fastgpt-test", TimeoutMS: 5000, VectorModel: "embed", AgentModel: "llm",
	}})
	t.Cleanup(func() { config.SetCurrent(&config.Config{}) })
	connector, err := NewPlatformFastGPTConnector()
	if err != nil {
		t.Fatalf("new connector: %v", err)
	}
	dataset, err := connector.CreateDataset(context.Background(), "门店库", "")
	if err != nil || dataset.ID != "dataset-1" {
		t.Fatalf("create dataset = %#v, %v", dataset, err)
	}
	collectionID, err := connector.UploadLocalFile(context.Background(), dataset.ID, "guide.md", strings.NewReader("hello"))
	if err != nil || collectionID != "collection-1" {
		t.Fatalf("upload = %q, %v", collectionID, err)
	}
	collections, err := connector.ListCollections(context.Background(), dataset.ID)
	if err != nil || len(collections) != 1 || collections[0].DataAmount != 3 {
		t.Fatalf("collections = %#v, %v", collections, err)
	}
	want := []string{"POST /api/core/dataset/create", "POST /api/core/dataset/collection/create/localFile", "POST /api/core/dataset/collection/listV2"}
	if fmt.Sprint(requested) != fmt.Sprint(want) {
		t.Fatalf("requested %v want %v", requested, want)
	}
}
