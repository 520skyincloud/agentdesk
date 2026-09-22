package retrievers

import (
	"testing"

	"agent-desk/internal/ai/rag"
)

func TestRebuiltSelectionTraceDoesNotCallJudgeRejectionContextLimit(t *testing.T) {
	raw := []rag.RetrieveResult{
		{SourceRecordID: "address", Content: "delivery address"},
		{SourceRecordID: "robot", Content: "delivery robot"},
	}
	result := &KnowledgeRetrieveResult{RawHits: raw, Options: KnowledgeRetrieveOptions{ContextMaxTokens: 1000}}
	RebuildKnowledgeRetrieveSelection(result, raw[1:])
	if len(result.TraceItems) != 2 || result.TraceItems[0].DiscardReason != "judge_not_selected" ||
		!result.TraceItems[1].UsedInContext || result.TraceItems[1].DiscardReason != "" {
		t.Fatalf("trace attributed judge selection to context truncation: %#v", result.TraceItems)
	}
}

func TestRebuiltEmptySelectionTracePreservesRawCandidates(t *testing.T) {
	raw := []rag.RetrieveResult{{SourceRecordID: "robot", Content: "delivery robot", Score: 0.8}}
	result := &KnowledgeRetrieveResult{RawHits: raw}
	RebuildKnowledgeRetrieveSelection(result, nil)
	if len(result.RawHits) != 1 || len(result.Hits) != 0 || len(result.TraceItems) != 1 ||
		result.TraceItems[0].DiscardReason != "judge_not_selected" || result.TraceItems[0].RawRankNo != 1 {
		t.Fatalf("empty judge outcome lost original retrieval: %#v", result)
	}
}
