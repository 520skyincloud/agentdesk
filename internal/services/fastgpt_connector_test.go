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
		if r.Header.Get("X-Agent-Desk-Token") != "fastgpt-integration-test" {
			t.Errorf("missing managed integration auth")
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("managed connector must not use a legacy FastGPT API key")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/integration/agent-desk/dataset/create":
			_, _ = io.WriteString(w, `{"code":200,"data":{"datasetId":"dataset-1","datasetName":"门店库"}}`)
		case "/api/integration/agent-desk/dataset/upload":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			if !strings.Contains(r.FormValue("data"), `"externalStoreId":"71"`) {
				t.Errorf("missing dataset form data")
			}
			_, _ = io.WriteString(w, `{"code":200,"data":{"collectionId":"collection-1"}}`)
		case "/api/integration/agent-desk/dataset/collections":
			_, _ = io.WriteString(w, `{"code":200,"data":{"collections":[{"collectionId":"collection-1","name":"guide.md","dataAmount":3,"trainingAmount":0}]}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":404,"message":"not found"}`)
		}
	}))
	defer server.Close()

	config.SetCurrent(&config.Config{FastGPT: config.FastGPTConfig{
		Enabled: true, BaseURL: server.URL, IntegrationToken: "fastgpt-integration-test", TimeoutMS: 5000,
	}})
	t.Cleanup(func() { config.SetCurrent(&config.Config{}) })
	connector, err := NewManagedStoreFastGPTConnector()
	if err != nil {
		t.Fatalf("new connector: %v", err)
	}
	if _, err := connector.CreateDataset(context.Background(), "门店库", ""); err == nil || !strings.Contains(err.Error(), "store scope") {
		t.Fatalf("unscoped managed connector must fail, err=%v", err)
	}
	connector = connector.ForStore(71)
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
	want := []string{"POST /api/integration/agent-desk/dataset/create", "POST /api/integration/agent-desk/dataset/upload", "POST /api/integration/agent-desk/dataset/collections"}
	if fmt.Sprint(requested) != fmt.Sprint(want) {
		t.Fatalf("requested %v want %v", requested, want)
	}
}
