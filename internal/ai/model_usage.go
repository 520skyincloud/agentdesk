package ai

import (
	"context"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/usagex"
)

type ModelUsageRecord struct {
	Stage            string
	OperationType    string
	Config           models.AIConfig
	PromptTokens     int64
	CompletionTokens int64
	LatencyMS        int64
	Status           string
	ErrorClass       string
	Receipt          *usagex.Receipt
	ExternalEventKey string
}

type suppressModelUsageRecordingKey struct{}

var RecordModelUsageForContext func(context.Context, ModelUsageRecord)

func WithoutModelUsageRecording(ctx context.Context) context.Context {
	return context.WithValue(ctx, suppressModelUsageRecordingKey{}, true)
}

func RecordModelUsage(ctx context.Context, record ModelUsageRecord) {
	record.Stage = strings.TrimSpace(record.Stage)
	if record.Stage == "" || RecordModelUsageForContext == nil || modelUsageRecordingSuppressed(ctx) {
		return
	}
	RecordModelUsageForContext(ctx, record)
}

func modelUsageRecordingSuppressed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	suppressed, _ := ctx.Value(suppressModelUsageRecordingKey{}).(bool)
	return suppressed
}
