package executor

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestCompleteGeneratedReplyProtocolFailureReturnsExecutorError(t *testing.T) {
	protocolErr := fmt.Errorf("%w: missing content for task-2", errGeneratedReplyProtocol)
	summary := &RunResult{Status: "fallback", ReplyText: `{"replyParts":[]}`}
	collector := callbacks.NewRuntimeTraceCollector()

	got, err := completeGeneratedReplyProtocolFailure(summary, collector, protocolErr, "generate")

	if got != summary || !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("protocol failure must return the executor error, summary=%#v err=%v", got, err)
	}
	if summary.Status != "error" || summary.ReplyText != "" || summary.ErrorMessage == "" {
		t.Fatalf("protocol failure must suppress output and mark the run failed, got %#v", summary)
	}
	if collector.Data.Output.FinishReason != "generated_reply_protocol_error" || collector.Data.Pipeline.Validate.Status != "failed" {
		t.Fatalf("protocol failure trace must remain retryable and diagnosable, got %#v", collector.Data)
	}
	if strings.Contains(summary.ReplyText, "replyParts") {
		t.Fatalf("internal protocol leaked into the final reply: %q", summary.ReplyText)
	}
}
