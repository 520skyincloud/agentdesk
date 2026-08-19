package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestCustomerVisibleBoundaryCleansEveryReplyPartAndRebuildsText(t *testing.T) {
	summary := &RunResult{ReplyParts: []contracts.ReplyPartV2{
		{TaskKeys: []string{"parking"}, Content: "停车场免费。定位我这边发你。"},
		{TaskKeys: []string{"phone"}, Content: "酒店电话是 0551-88886666。"},
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		PrimaryIntent: "hotel_variable", NeedsResource: true, ResourceActions: []string{"provide_location"},
	}
	if err := applyCustomerVisibleBoundary(summary, collector); err != nil {
		t.Fatal(err)
	}
	if len(summary.ReplyParts) != 2 || strings.Contains(summary.ReplyParts[0].Content, "发你") {
		t.Fatalf("reply parts were not cleaned: %#v", summary.ReplyParts)
	}
	if summary.ReplyText != joinValidatedReplyParts(summary.ReplyParts) {
		t.Fatalf("reply text and reply parts diverged: text=%q parts=%#v", summary.ReplyText, summary.ReplyParts)
	}
}

func TestCustomerVisibleBoundaryDropsInvalidPart(t *testing.T) {
	summary := &RunResult{ReplyParts: []contracts.ReplyPartV2{
		{TaskKeys: []string{"internal"}, Content: "内部 taskKey=internal"},
		{TaskKeys: []string{"parking"}, Content: "停车场免费。"},
	}}
	collector := callbacks.NewRuntimeTraceCollector()
	if err := applyCustomerVisibleBoundary(summary, collector); err != nil {
		t.Fatal(err)
	}
	if len(summary.ReplyParts) != 1 || summary.ReplyParts[0].Content != "停车场免费。" || summary.ReplyText != "停车场免费。" {
		t.Fatalf("invalid part was not removed safely: parts=%#v text=%q", summary.ReplyParts, summary.ReplyText)
	}
}
