package factory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent-desk/internal/models"

	"github.com/cloudwego/eino/schema"
)

func TestProviderExtraFieldsDisablesDeepSeekV4Thinking(t *testing.T) {
	fields := providerExtraFields(models.AIConfig{
		BaseURL:   "https://api.deepseek.com/v1",
		ModelName: "deepseek-v4-flash",
	})
	thinking, ok := fields["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("expected DeepSeek V4 thinking extra field, got %#v", fields)
	}
	if thinking["type"] != "disabled" {
		t.Fatalf("expected DeepSeek V4 thinking to be disabled, got %#v", thinking)
	}
}

func TestHybridReplyThinkingPolicyReachesGatewayRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["enable_thinking"] != false || payload["max_completion_tokens"] != float64(1024) {
			t.Errorf("reply policy missing on wire: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat-test","object":"chat.completion","model":"qwen3.7-plus","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"目前可以选择云漫。"}}],"usage":{"prompt_tokens":10,"completion_tokens":8,"total_tokens":18}}`))
	}))
	defer server.Close()
	model, err := NewChatModelFactory().Build(context.Background(), models.AIConfig{
		BaseURL: server.URL, APIKey: "test", ModelName: "qwen3.7-plus", MaxOutputTokens: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		reply, err := model.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "有哪些房"}})
		if err != nil || reply == nil || reply.Content == "" {
			t.Fatalf("reply missing: %#v %v", reply, err)
		}
	}
}

func TestProviderExtraFieldsKeepsQwenThinkingDisabled(t *testing.T) {
	fields := providerExtraFields(models.AIConfig{
		BaseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
		ModelName: "qwen3-max",
	})
	if fields["enable_thinking"] != false {
		t.Fatalf("expected qwen3 thinking to be disabled, got %#v", fields)
	}
}

func TestProviderExtraFieldsKeepsHybridReplyThinkingDisabledThroughGateway(t *testing.T) {
	for _, name := range []string{"qwen3.7-plus", "qwen3.7-plus-2026-09-28"} {
		fields := providerExtraFields(models.AIConfig{BaseURL: "https://model-gateway.example/v1", ModelName: name})
		if fields["enable_thinking"] != false {
			t.Fatalf("gateway dropped reply thinking policy for %s: %#v", name, fields)
		}
	}
	for _, name := range []string{"other-model", "qwen3-vl-plus"} {
		if fields := providerExtraFields(models.AIConfig{BaseURL: "https://model-gateway.example/v1", ModelName: name}); len(fields) != 0 {
			t.Fatalf("unverified model should not inherit provider options: %#v", fields)
		}
	}
}
