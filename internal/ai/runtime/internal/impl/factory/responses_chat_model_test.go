package factory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"

	"github.com/cloudwego/eino/schema"
)

func TestResponsesChatModelSendsDeepSeekStrictJSONSchema(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path=%s", r.URL.Path)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed","output_text":"{\"ok\":true}","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`))
	}))
	defer server.Close()

	config, err := (modelconfig.Config{
		Provider: enums.AIProviderOpenAI, BaseURL: server.URL + "/v1", APIKey: "test-key",
		APIMode: "responses", ModelName: "deepseek-v4-flash", MaxOutputTokens: 64, TimeoutMS: 1000,
	}).WithJSONSchema("reply_output.v2", []byte(`{"type":"object","additionalProperties":false,"required":["ok"],"properties":{"ok":{"const":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	chatModel, err := NewChatModelFactory().Build(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	response, err := chatModel.Generate(context.Background(), []*schema.Message{
		schema.SystemMessage("Return JSON."), schema.UserMessage("ping"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != `{"ok":true}` {
		t.Fatalf("content=%q", response.Content)
	}
	textConfig, ok := captured["text"].(map[string]any)
	if !ok {
		t.Fatalf("missing text config: %#v", captured)
	}
	format, ok := textConfig["format"].(map[string]any)
	if !ok || format["type"] != "json_schema" || format["name"] != "reply_output_v2" || format["strict"] != true {
		t.Fatalf("unexpected response format: %#v", textConfig)
	}
	if _, ok := format["schema"].(map[string]any); !ok {
		t.Fatalf("missing JSON Schema: %#v", format)
	}
	reasoning, ok := captured["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "none" {
		t.Fatalf("DeepSeek reasoning must be disabled: %#v", captured["reasoning"])
	}
}

func TestResponsesChatModelLeavesPlainCallsUnconstrained(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_2","status":"completed","output_text":"OK","usage":{}}`))
	}))
	defer server.Close()

	chatModel, err := NewChatModelFactory().Build(context.Background(), modelconfig.Config{
		Provider: enums.AIProviderOpenAI, BaseURL: server.URL + "/v1", APIMode: "responses",
		ModelName: "gpt-plain", TimeoutMS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("ping")}); err != nil {
		t.Fatal(err)
	}
	if _, exists := captured["text"]; exists {
		t.Fatalf("plain call unexpectedly received structured output: %#v", captured["text"])
	}
	if _, exists := captured["reasoning"]; exists {
		t.Fatalf("non-DeepSeek call unexpectedly received reasoning override: %#v", captured["reasoning"])
	}
}

func TestResponsesChatModelPreservesFunctionToolLoop(t *testing.T) {
	requests := make([]map[string]any, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var captured map[string]any
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		requests = append(requests, captured)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			_, _ = w.Write([]byte(`{"id":"resp_tool_1","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_weather_1","name":"get_weather","arguments":"{\"location\":\"合肥\"}","status":"completed"}],"usage":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp_tool_2","status":"completed","output_text":"{\"ok\":true}","usage":{}}`))
	}))
	defer server.Close()

	config, err := (modelconfig.Config{
		Provider: enums.AIProviderOpenAI, BaseURL: server.URL + "/v1", APIMode: "responses",
		ModelName: "deepseek-v4-flash", TimeoutMS: 1000,
	}).WithJSONSchema("reply_output.v2", []byte(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	base, err := NewChatModelFactory().Build(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	withTools, err := base.WithTools([]*schema.ToolInfo{{
		Name: "get_weather", Desc: "查询天气",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"location": {Type: schema.String, Required: true},
		}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := withTools.Generate(context.Background(), []*schema.Message{schema.UserMessage("合肥天气如何")})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].ID != "call_weather_1" || first.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("unexpected tool calls: %#v", first.ToolCalls)
	}
	second, err := withTools.Generate(context.Background(), []*schema.Message{
		schema.UserMessage("合肥天气如何"),
		first,
		{Role: schema.Tool, ToolCallID: "call_weather_1", ToolName: "get_weather", Content: `{"temperature":"28C"}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != `{"ok":true}` || len(requests) != 2 {
		t.Fatalf("unexpected final response=%q requests=%d", second.Content, len(requests))
	}
	tools, ok := requests[0]["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("missing Responses tools: %#v", requests[0]["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["type"] != "function" || tool["name"] != "get_weather" {
		t.Fatalf("unexpected Responses tool: %#v", tools[0])
	}
	if requests[0]["tool_choice"] != "auto" {
		t.Fatalf("unexpected tool choice: %#v", requests[0]["tool_choice"])
	}
	input, ok := requests[1]["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("unexpected tool-loop input: %#v", requests[1]["input"])
	}
	functionCall, _ := input[1].(map[string]any)
	functionOutput, _ := input[2].(map[string]any)
	if functionCall["type"] != "function_call" || functionCall["call_id"] != "call_weather_1" ||
		functionOutput["type"] != "function_call_output" || functionOutput["call_id"] != "call_weather_1" {
		t.Fatalf("tool call correlation was lost: call=%#v output=%#v", functionCall, functionOutput)
	}
}
