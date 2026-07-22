package ai

import (
	"context"
	"testing"
)

func TestWithoutModelUsageRecordingSuppressesNestedRecorder(t *testing.T) {
	previous := RecordModelUsageForContext
	t.Cleanup(func() {
		RecordModelUsageForContext = previous
	})

	recorded := 0
	RecordModelUsageForContext = func(context.Context, ModelUsageRecord) {
		recorded++
	}

	RecordModelUsage(context.Background(), ModelUsageRecord{Stage: "embedding"})
	RecordModelUsage(WithoutModelUsageRecording(context.Background()), ModelUsageRecord{Stage: "embedding"})
	if recorded != 1 {
		t.Fatalf("expected only the unsuppressed usage event, got %d", recorded)
	}
}
