package factory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-desk/internal/models"

	"github.com/cloudwego/eino/schema"
)

func TestResponsesChatModelRejectsIncompleteResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp-incomplete", "status": "incomplete", "output_text": "半截回复",
			"incomplete_details": map[string]any{"reason": "max_output_tokens"},
		})
	}))
	defer server.Close()

	model := newResponsesChatModel(models.AIConfig{BaseURL: server.URL, ModelName: "test-model", MaxOutputTokens: 1024})
	_, err := model.Generate(context.Background(), []*schema.Message{schema.UserMessage("我的房住到几号？")})
	if err == nil || !strings.Contains(err.Error(), "responses api incomplete") || !strings.Contains(err.Error(), "max_output_tokens") {
		t.Fatalf("incomplete response must be rejected with its reason, got %v", err)
	}
}

func TestResponsesChatModelAcceptsCompletedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp-complete", "status": "completed", "output_text": "您这笔订单是9月25日12点前退房。",
			"usage": map[string]any{"input_tokens": 20, "output_tokens": 30, "total_tokens": 50},
		})
	}))
	defer server.Close()

	model := newResponsesChatModel(models.AIConfig{BaseURL: server.URL, ModelName: "test-model", MaxOutputTokens: 1024})
	message, err := model.Generate(context.Background(), []*schema.Message{schema.UserMessage("我的房住到几号？")})
	if err != nil {
		t.Fatalf("completed response failed: %v", err)
	}
	if message.Content != "您这笔订单是9月25日12点前退房。" || message.ResponseMeta == nil || message.ResponseMeta.FinishReason != "completed" {
		t.Fatalf("completed response was not preserved: %#v", message)
	}
}
