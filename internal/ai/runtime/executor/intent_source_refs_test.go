package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestRepairRuntimeIntentTaskSourceRefsFiltersAndRepairsPrimary(t *testing.T) {
	tasks := []callbacks.IntentTaskTraceData{{
		Intent:         "hotel_info",
		Text:           "有没有咖啡",
		SourceRefs:     []string{"U99", "U1"},
		NeedsKnowledge: true,
	}}
	got := repairRuntimeIntentTaskSourceRefs(tasks, []string{"好困啊", "有没有咖啡"})
	refs := got[0].SourceRefs
	if len(refs) != 2 || refs[0] != "U2" || refs[1] != "U1" {
		t.Fatalf("expected matched primary and nearby context with invalid ref removed, got %#v", refs)
	}
}

func TestRepairRuntimeIntentTaskSourceRefsFillsSingleSource(t *testing.T) {
	tasks := []callbacks.IntentTaskTraceData{{Intent: "hotel_info", Text: "早餐几点", NeedsKnowledge: true}}
	got := repairRuntimeIntentTaskSourceRefs(tasks, []string{"早餐几点"})
	if refs := got[0].SourceRefs; len(refs) != 1 || refs[0] != "U1" {
		t.Fatalf("expected missing single-source binding repaired, got %#v", refs)
	}
}

func TestRepairRuntimeIntentTaskSourceRefsBindsNonTaskBackground(t *testing.T) {
	tasks := []callbacks.IntentTaskTraceData{{Intent: "hotel_info", Text: "有没有咖啡", NeedsKnowledge: true}}
	got := repairRuntimeIntentTaskSourceRefs(tasks, []string{"好困啊", "有没有咖啡"})
	refs := got[0].SourceRefs
	if len(refs) != 2 || refs[0] != "U2" || refs[1] != "U1" {
		t.Fatalf("expected background bound after the primary source, got %#v", refs)
	}
}
