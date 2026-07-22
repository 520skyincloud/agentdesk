package rag

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent-desk/internal/ai"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestRerankAcceptsObjectDocumentAndRecordsActualUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rerank" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results":[{"document":{"text":"早餐时间是7点"},"index":0,"relevance_score":0.82}],
			"usage":{"prompt_tokens":121,"completion_tokens":0,"total_tokens":121}
		}`))
	}))
	defer server.Close()

	previous := ai.RecordModelUsageForContext
	t.Cleanup(func() {
		ai.RecordModelUsageForContext = previous
	})
	var recorded ai.ModelUsageRecord
	ai.RecordModelUsageForContext = func(_ context.Context, item ai.ModelUsageRecord) {
		recorded = item
	}

	results, usage, err := Rerank.RerankWithConfigAndUsage(context.Background(), models.AIConfig{
		Provider: enums.AIProviderOpenAI, BaseURL: server.URL + "/v1",
		APIKey: "sk-test", ModelName: "rerank-test", TimeoutMS: 3000,
	}, "早餐时间", []string{"早餐时间是7点"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Index != 0 || results[0].RelevanceScore != 0.82 {
		t.Fatalf("unexpected rerank results: %#v", results)
	}
	if usage == nil || usage.PromptTokens != 121 || usage.TotalTokens != 121 {
		t.Fatalf("unexpected rerank usage: %#v", usage)
	}
	if recorded.Stage != "rerank" || recorded.PromptTokens != 121 || recorded.CompletionTokens != 0 {
		t.Fatalf("actual rerank usage was not recorded: %#v", recorded)
	}
}
