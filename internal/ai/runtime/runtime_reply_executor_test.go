package runtime

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/graphs"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
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
