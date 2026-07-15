package services

import "testing"

func TestParseUpstreamModelUsageKeepsOnlyProviderActualValues(t *testing.T) {
	usage := parseUpstreamModelUsage(map[string]any{
		"id": "req-media-1",
		"usage": map[string]any{
			"prompt_tokens":     float64(120),
			"completion_tokens": float64(8),
			"prompt_tokens_details": map[string]any{
				"cached_tokens": float64(80),
			},
			"completion_tokens_details": map[string]any{
				"reasoning_tokens": float64(3),
			},
		},
	})
	if usage == nil || usage.RequestID != "req-media-1" || usage.PromptTokens != 120 || usage.CompletionTokens != 8 || usage.CachedPromptTokens != 80 || usage.ReasoningTokens != 3 {
		t.Fatalf("usage=%#v", usage)
	}
}

func TestParseUpstreamModelUsageDoesNotInventMissingTokens(t *testing.T) {
	if usage := parseUpstreamModelUsage(map[string]any{"usage": map[string]any{}}); usage != nil {
		t.Fatalf("expected no actual usage, got %#v", usage)
	}
}
