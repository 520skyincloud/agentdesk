package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestValidateStructuredResponsesPayload(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "output text", raw: `{"output_text":"{\"ok\":true}"}`, want: true},
		{name: "nested output", raw: `{"output":[{"content":[{"type":"output_text","text":"{\"ok\":true}"}]}]}`, want: true},
		{name: "schema not followed", raw: `{"output_text":"OK"}`, want: false},
		{name: "wrong value", raw: `{"output_text":"{\"ok\":false}"}`, want: false},
		{name: "empty", raw: `{"output":[]}`, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validateStructuredResponsesPayload([]byte(test.raw)); got != test.want {
				t.Fatalf("got=%v want=%v", got, test.want)
			}
		})
	}
}

func TestValidateTextModelResponsesExercisesStrictSchemaForRuntimeSlots(t *testing.T) {
	for _, usageCode := range []enums.ModelUsageSlot{
		enums.ModelUsageSlotIntentDetectLLM,
		enums.ModelUsageSlotReplyLLM,
	} {
		t.Run(string(usageCode), func(t *testing.T) {
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
				_, _ = w.Write([]byte(`{"output_text":"{\"ok\":true}"}`))
			}))
			defer server.Close()

			slot := models.ModelProfileSlot{
				UsageCode: usageCode, ModelType: enums.AIModelTypeLLM,
				ModelName: "deepseek-v4-flash", APIMode: "responses", TimeoutMS: 1000,
			}
			if err := (&newAPIStoreCredentialValidator{}).validateTextModel(
				context.Background(), server.URL+"/v1", slot, "test-key", false,
			); err != nil {
				t.Fatal(err)
			}
			textConfig, ok := captured["text"].(map[string]any)
			if !ok {
				t.Fatalf("missing Responses text config: %#v", captured)
			}
			format, ok := textConfig["format"].(map[string]any)
			if !ok || format["type"] != "json_schema" || format["name"] != "agentdesk_connection_test" || format["strict"] != true {
				t.Fatalf("unexpected Responses format: %#v", textConfig)
			}
			if _, ok := format["schema"].(map[string]any); !ok {
				t.Fatalf("missing connection-test JSON Schema: %#v", format)
			}
			reasoning, ok := captured["reasoning"].(map[string]any)
			if !ok || reasoning["effort"] != "none" {
				t.Fatalf("DeepSeek reasoning must be disabled: %#v", captured["reasoning"])
			}
		})
	}
}

func TestValidateTextModelResponsesLeavesPlainSlotsUnconstrained(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output_text":"OK"}`))
	}))
	defer server.Close()

	slot := models.ModelProfileSlot{
		UsageCode: enums.ModelUsageSlotMemorySummary, ModelType: enums.AIModelTypeLLM,
		ModelName: "gpt-plain", APIMode: "responses", TimeoutMS: 1000,
	}
	if err := (&newAPIStoreCredentialValidator{}).validateTextModel(
		context.Background(), server.URL+"/v1", slot, "test-key", false,
	); err != nil {
		t.Fatal(err)
	}
	if _, exists := captured["text"]; exists {
		t.Fatalf("plain Responses slot unexpectedly received JSON Schema: %#v", captured["text"])
	}
	if _, exists := captured["reasoning"]; exists {
		t.Fatalf("non-DeepSeek slot unexpectedly received reasoning override: %#v", captured["reasoning"])
	}
}
