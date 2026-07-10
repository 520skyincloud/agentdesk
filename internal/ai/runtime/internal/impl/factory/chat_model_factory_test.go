package factory

import (
	"testing"

	"agent-desk/internal/models"
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

func TestProviderExtraFieldsKeepsQwenThinkingDisabled(t *testing.T) {
	fields := providerExtraFields(models.AIConfig{
		BaseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
		ModelName: "qwen3-max",
	})
	if fields["enable_thinking"] != false {
		t.Fatalf("expected qwen3 thinking to be disabled, got %#v", fields)
	}
}
