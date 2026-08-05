package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent-desk/internal/pkg/modelconfig"
)

func TestLLMSendsDeepSeekThinkingDisabledThroughNewAPI(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-thinking-disabled","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	_, err := LLM.ChatWithRuntimeConfig(context.Background(), modelconfig.Config{
		BaseURL: server.URL + "/v1", APIKey: "test-key", ModelName: "deepseek-v4-flash",
		MaxOutputTokens: 64, TimeoutMS: 1000,
	}, "system", "ping")
	if err != nil {
		t.Fatal(err)
	}
	thinking, ok := captured["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("NewAPI request must disable DeepSeek thinking, got %#v", captured["thinking"])
	}
}
