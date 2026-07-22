package runtime

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/graphs"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"
)

func TestRuntimeReplyExecutorResumeMessageTextUsesMediaTranscript(t *testing.T) {
	message := models.Message{
		MessageType: enums.IMMessageTypeVoice,
		Content:     "wx_protocol_1001.mp3",
		Payload:     `{"mediaText":"确认确认","mediaSummary":"确认确认","mediaUnderstandingStatus":"understood"}`,
	}
	got := newRuntimeReplyExecutor().resumeMessageText(message)
	if !strings.Contains(got, "确认确认") {
		t.Fatalf("expected transcript in resume message text, got %q", got)
	}
	if graphs.ParseConfirmationDecision(got) != graphs.ConfirmationDecisionConfirm {
		t.Fatalf("expected transcript to be recognized as confirm, got %q", got)
	}
}

func TestNormalizeRuntimeReplyModelConfigClampsLargeOutputBudget(t *testing.T) {
	config := normalizeRuntimeReplyModelConfig(modelconfig.Config{MaxOutputTokens: 64800})
	if config.MaxOutputTokens != runtimeReplyMaxOutputTokens {
		t.Fatalf("expected large reply output budget to clamp to %d, got %d", runtimeReplyMaxOutputTokens, config.MaxOutputTokens)
	}
}

func TestNormalizeRuntimeReplyModelConfigKeepsLowerOutputBudget(t *testing.T) {
	config := normalizeRuntimeReplyModelConfig(modelconfig.Config{MaxOutputTokens: 320})
	if config.MaxOutputTokens != 320 {
		t.Fatalf("expected lower reply output budget to be preserved, got %d", config.MaxOutputTokens)
	}
}

func TestNormalizeRuntimeReplyModelConfigSetsDefaultOutputBudget(t *testing.T) {
	config := normalizeRuntimeReplyModelConfig(modelconfig.Config{})
	if config.MaxOutputTokens != runtimeReplyMaxOutputTokens {
		t.Fatalf("expected empty reply output budget to default to %d, got %d", runtimeReplyMaxOutputTokens, config.MaxOutputTokens)
	}
}
