package factory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"

	"github.com/cloudwego/eino/schema"
)

func TestProviderExtraFieldsDisablesDeepSeekV4Thinking(t *testing.T) {
	for _, baseURL := range []string{
		"https://api.deepseek.com/v1",
		"https://newapi.example.com/v1",
	} {
		fields := providerExtraFields(modelconfig.Config{BaseURL: baseURL, ModelName: "deepseek-v4-flash"})
		thinking, ok := fields["thinking"].(map[string]any)
		if !ok {
			t.Fatalf("baseURL=%s: expected DeepSeek V4 thinking extra field, got %#v", baseURL, fields)
		}
		if thinking["type"] != "disabled" {
			t.Fatalf("baseURL=%s: expected DeepSeek V4 thinking to be disabled, got %#v", baseURL, thinking)
		}
		if fields["enable_thinking"] != false {
			t.Fatalf("baseURL=%s: expected DeepSeek V4 enable_thinking=false, got %#v", baseURL, fields)
		}
	}
}

func TestChatModelFactorySendsDeepSeekThinkingDisabledThroughNewAPI(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-thinking-disabled", "object": "chat.completion", "created": time.Now().Unix(),
			"model": "deepseek-v4-flash",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer server.Close()

	chatModel, err := NewChatModelFactory().Build(context.Background(), modelconfig.Config{
		Provider: enums.AIProviderOpenAI, BaseURL: server.URL + "/v1", APIKey: "test-key",
		ModelName: "deepseek-v4-flash", MaxOutputTokens: 64, TimeoutMS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("ping")}); err != nil {
		t.Fatal(err)
	}
	thinking, ok := captured["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("NewAPI request must disable DeepSeek thinking, got %#v", captured["thinking"])
	}
	if captured["enable_thinking"] != false {
		t.Fatalf("NewAPI request must set DeepSeek enable_thinking=false, got %#v", captured["enable_thinking"])
	}
}

func TestChatModelFactoryUsesExactlyInitialCallPlusTwoRetries(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "upstream_5xx",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"message":"temporarily unavailable"}}`))
			},
		},
		{
			name: "empty_output",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "chatcmpl-empty", "object": "chat.completion", "created": time.Now().Unix(),
					"model": "deepseek-v4-flash",
					"choices": []map[string]any{{
						"index": 0, "finish_reason": "stop",
						"message": map[string]any{"role": "assistant", "content": ""},
					}},
					"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 0, "total_tokens": 1},
				})
			},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				scenario.handler(w, r)
			}))
			defer server.Close()

			chatModel, err := NewChatModelFactory().Build(context.Background(), modelconfig.Config{
				Provider: enums.AIProviderOpenAI, BaseURL: server.URL + "/v1", APIKey: "test-key",
				ModelName: "deepseek-v4-flash", MaxOutputTokens: 64, TimeoutMS: 1000, MaxRetryCount: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("ping")}); err == nil {
				t.Fatal("expected model call to fail after retries")
			}
			if got := calls.Load(); got != 3 {
				t.Fatalf("NewAPI calls=%d want 3", got)
			}
		})
	}
}

func TestChatModelFactoryRetriesAfterPerAttemptTimeout(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(250 * time.Millisecond):
		}
	}))
	defer server.Close()

	chatModel, err := NewChatModelFactory().Build(context.Background(), modelconfig.Config{
		Provider: enums.AIProviderOpenAI, BaseURL: server.URL + "/v1", APIKey: "test-key",
		ModelName: "gpt-timeout", MaxOutputTokens: 64, TimeoutMS: 30, MaxRetryCount: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	callCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := chatModel.Generate(callCtx, []*schema.Message{schema.UserMessage("ping")}); err == nil {
		t.Fatal("expected model call to fail after timed out retries")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("NewAPI timeout calls=%d want 3", got)
	}
}

func TestProviderExtraFieldsKeepsQwenThinkingDisabled(t *testing.T) {
	fields := providerExtraFields(modelconfig.Config{
		BaseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
		ModelName: "qwen3-max",
	})
	if fields["enable_thinking"] != false {
		t.Fatalf("expected qwen3 thinking to be disabled, got %#v", fields)
	}
}
