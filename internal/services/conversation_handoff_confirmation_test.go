package services

import "testing"

func TestHandoffFallbackDoesNotInterpretBusinessCancellations(t *testing.T) {
	for _, text := range []string{"不要取消人工", "取消订单", "不用沙发了", "问题还是没有好", "不用取消，继续找同事", "这件事我已解决，不再需要人工接待了，谢谢"} {
		if got := parseHumanHandoffConfirmationFallback(text); got != humanHandoffConfirmationUnknown {
			t.Errorf("%q must be left to the semantic classifier, got %q", text, got)
		}
	}
	for _, text := range []string{"取消", "不用了", "别转人工", "已解决"} {
		if got := parseHumanHandoffConfirmationFallback(text); got != humanHandoffConfirmationCancel {
			t.Errorf("%q should be an explicit cancel, got %q", text, got)
		}
	}
}
