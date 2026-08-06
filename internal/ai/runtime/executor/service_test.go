package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestPrepareHotelVariableDirectCommitNeverFallsBackToText(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		action    string
		subIntent string
	}{
		{name: "location", content: "酒店在哪里，定位发我", action: "provide_location", subIntent: "location"},
		{name: "mini program", content: "入住小程序发我", action: "provide_mini_program", subIntent: "mini_program"},
		{name: "phone", content: "酒店电话多少", action: "provide_phone", subIntent: "phone"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := &RunResult{}
			collector := callbacks.NewRuntimeTraceCollector()
			collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
				PrimaryIntent:  "hotel_variable",
				SubIntent:      test.subIntent,
				ShouldReply:    true,
				NeedsResource:  true,
				ResourceAction: test.action,
			}
			req := RunInput{UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: test.content}}

			if !prepareHotelVariableDirectCommit(req, summary, collector) {
				t.Fatal("resource-only intent must proceed to strict structured Commit")
			}
			if summary.ReplyText != "" {
				t.Fatalf("missing resource must not be converted to text fallback: %q", summary.ReplyText)
			}
		})
	}
}
