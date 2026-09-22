package rag

import (
	"encoding/json"
	"testing"

	"agent-desk/internal/pkg/dto/response"
)

func TestRuntimeRetrieveLogMarksSelectionPending(t *testing.T) {
	req := &CreateRetrieveLogRequest{
		Hits:             []response.KnowledgeSearchResult{{Content: "robot delivery"}},
		SelectionPending: true,
	}
	hits := buildRetrieveTraceHits(req)
	if len(hits) != 1 || hits[0].DiscardReason != "pending_judge_selection" {
		t.Fatalf("raw retrieval falsely attributed to context pruning: %#v", hits)
	}
	var trace retrieveTraceData
	if err := json.Unmarshal([]byte(buildRetrieveTraceData(req)), &trace); err != nil || !trace.Retrieve.SelectionPending {
		t.Fatalf("pending selection stage missing from trace: %#v, %v", trace, err)
	}
}

func TestFinalRetrieveLogRetainsContextDiscardReason(t *testing.T) {
	req := &CreateRetrieveLogRequest{
		Hits:           []response.KnowledgeSearchResult{{Content: "a"}, {Content: "b"}},
		UsedHitRankNos: []int{2},
	}
	hits := buildRetrieveTraceHits(req)
	if hits[0].DiscardReason != "context_limit_or_duplicate" || hits[1].DiscardReason != "" || hits[1].ContextRankNo != 1 {
		t.Fatalf("completed retrieval semantics changed: %#v", hits)
	}
}
