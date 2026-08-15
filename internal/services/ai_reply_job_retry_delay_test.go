package services

import (
	"testing"
	"time"
)

// 契约 14.3：协议/验证类失败使用短预算，不得落入 15s/1m/3m 通用退避。
func TestProtocolFailuresUseShortRetryBudget(t *testing.T) {
	for _, class := range []string{"generation_failed", "intent_detect_failed", "empty_output"} {
		delay := aiReplyJobRetryDelayFor(class, 0)
		if delay >= 15*time.Second {
			t.Fatalf("protocol failure class %s must use short delay, got %v", class, delay)
		}
		if delay < 500*time.Millisecond {
			t.Fatalf("protocol delay too aggressive: %v", delay)
		}
	}
	// 网络/知识类保留长退避（上游不可用）。
	if delay := aiReplyJobRetryDelayFor("runtime_error", 0); delay < 15*time.Second {
		t.Fatalf("network failure must keep long backoff, got %v", delay)
	}
}
