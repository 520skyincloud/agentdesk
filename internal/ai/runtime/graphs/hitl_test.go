package graphs

import "testing"

func TestParseConfirmationDecisionAcceptsFuzzyVoiceTranscripts(t *testing.T) {
	confirmCases := []string{
		"确认确认",
		"嗯确认",
		"可以的",
		"没问题",
		"行 就这样",
		"OKOK",
		"对的",
	}
	for _, input := range confirmCases {
		if got := ParseConfirmationDecision(input); got != ConfirmationDecisionConfirm {
			t.Fatalf("expected confirm for %q, got %q", input, got)
		}
	}

	cancelCases := []string{"取消取消", "不用了", "不要了", "算了吧", "no", "不确认", "不要创建", "先不建"}
	for _, input := range cancelCases {
		if got := ParseConfirmationDecision(input); got != ConfirmationDecisionCancel {
			t.Fatalf("expected cancel for %q, got %q", input, got)
		}
	}
}
