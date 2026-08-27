package runtime

import (
	"strings"
	"testing"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
)

func TestNormalizeRuntimeUserMessageContentKeepsMergedTurnWhenLastMessageIsVoice(t *testing.T) {
	content := utils.BuildRuntimeCustomerBurstEnvelope([]string{"1. [消息] 早餐几点", "2. [语音] 停车免费吗"})
	got := normalizeRuntimeUserMessageContent(
		enums.IMMessageTypeVoice,
		content,
		`{"mediaText":"停车免费吗","mediaUnderstandingStatus":"understood"}`,
	)
	if got != content {
		t.Fatalf("expected merged turn to remain unchanged, got %q", got)
	}
	if strings.Count(got, "停车免费吗") != 1 {
		t.Fatalf("expected the last voice transcript once, got %q", got)
	}
}

func TestNormalizeRuntimeUserMessageContentStillNormalizesSingleVoice(t *testing.T) {
	got := normalizeRuntimeUserMessageContent(
		enums.IMMessageTypeVoice,
		"voice.amr",
		`{"mediaText":"早餐几点","mediaUnderstandingStatus":"understood"}`,
	)
	if !strings.Contains(got, "早餐几点") || !strings.Contains(got, "voice.amr") {
		t.Fatalf("expected ordinary voice normalization to remain intact, got %q", got)
	}
}

func TestNormalizeRuntimeUserMessageContentKeepsMergedTurnWhenLastMessageIsText(t *testing.T) {
	content := utils.BuildRuntimeCustomerBurstEnvelope([]string{"1. [语音] 早餐几点", "2. [消息] 停车免费吗"})
	got := normalizeRuntimeUserMessageContent(enums.IMMessageTypeText, content, "")
	if got != content {
		t.Fatalf("expected merged turn ending in text to remain unchanged, got %q", got)
	}
	if strings.Count(got, "早餐几点") != 1 || strings.Count(got, "停车免费吗") != 1 {
		t.Fatalf("expected every merged source exactly once, got %q", got)
	}
}

func TestNormalizeRuntimeUserMessageContentRejectsUnfinishedVoiceText(t *testing.T) {
	for _, status := range []string{"", "pending", "failed", "empty"} {
		got := normalizeRuntimeUserMessageContent(
			enums.IMMessageTypeVoice,
			"voice.amr",
			`{"mediaText":"早餐几点","mediaUnderstandingStatus":"`+status+`"}`,
		)
		if got != "" {
			t.Fatalf("status %q must not expose unfinished voice text, got %q", status, got)
		}
	}
}
