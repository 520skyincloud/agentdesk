package retrievers

import (
	"testing"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/pkg/dto/response"
)

func TestInferRuntimeRetrieveSourceTypeRecognizesFastGPTEngineResults(t *testing.T) {
	hits := []response.KnowledgeSearchResult{{SectionPath: "FastGPT知识库/3/collection-1"}}
	if got := inferRuntimeRetrieveSourceType(hits); got != "fastgpt" {
		t.Fatalf("source type=%q", got)
	}
}

func TestBuildRetrieverTraceItemsRecordsRawAndContextOrder(t *testing.T) {
	hits := []rag.RetrieveResult{
		{KnowledgeBaseID: 3, SourceRecordID: "data-1", DocumentTitle: "南七.xlsx", Score: 0.9},
		{KnowledgeBaseID: 3, SourceRecordID: "data-2", DocumentTitle: "南七.xlsx", Score: 0.8},
	}
	items := buildRetrieverTraceItems("附近餐饮", hits, hits[:1], nil)
	if len(items) != 2 || items[0].RawRankNo != 1 || items[0].ContextRankNo != 1 || !items[0].UsedInContext {
		t.Fatalf("unexpected first trace item: %#v", items)
	}
	if items[1].RawRankNo != 2 || items[1].ContextRankNo != 0 || items[1].DiscardReason == "" {
		t.Fatalf("unexpected discarded trace item: %#v", items[1])
	}
}

func TestApplyKnowledgeBasePriorityKeepsStoreHitsAheadOfHigherScoredGeneralHits(t *testing.T) {
	rawHits := []rag.RetrieveResult{
		{KnowledgeBaseID: 4, SourceRecordID: "general-1", Content: "通用答案", Score: 0.99},
		{KnowledgeBaseID: 3, SourceRecordID: "store-1", Content: "门店答案", Score: 0.45},
	}
	result := &KnowledgeRetrieveResult{}
	applyKnowledgeBasePriority(result, []int64{3}, []int64{3, 4}, rawHits)

	if len(result.RawHits) != 2 || result.RawHits[0].KnowledgeBaseID != 4 {
		t.Fatalf("raw hits not preserved: %#v", result.RawHits)
	}
	if len(result.Hits) != 1 || result.Hits[0].KnowledgeBaseID != 3 {
		t.Fatalf("store layer did not win: %#v", result.Hits)
	}
}

func TestApplyKnowledgeBasePriorityFallsBackToGeneralWhenStoreHasNoHits(t *testing.T) {
	rawHits := []rag.RetrieveResult{
		{KnowledgeBaseID: 4, SourceRecordID: "general-1", Content: "通用答案", Score: 0.7},
		{KnowledgeBaseID: 4, SourceRecordID: "general-2", Content: "通用补充", Score: 0.6},
	}
	result := &KnowledgeRetrieveResult{}
	applyKnowledgeBasePriority(result, []int64{3}, []int64{3, 4}, rawHits)

	if len(result.Hits) != 2 || result.Hits[0].KnowledgeBaseID != 4 || result.Hits[1].KnowledgeBaseID != 4 {
		t.Fatalf("general fallback not selected: %#v", result.Hits)
	}
}

func TestApplyKnowledgeBasePriorityPreservesSingleKnowledgeBaseBehavior(t *testing.T) {
	rawHits := []rag.RetrieveResult{
		{KnowledgeBaseID: 3, SourceRecordID: "store-1", Score: 0.8},
		{KnowledgeBaseID: 3, SourceRecordID: "store-2", Score: 0.7},
	}
	result := &KnowledgeRetrieveResult{}
	applyKnowledgeBasePriority(result, []int64{3}, []int64{3}, rawHits)

	if len(result.Hits) != len(rawHits) {
		t.Fatalf("single knowledge base hits=%d, want %d", len(result.Hits), len(rawHits))
	}
	for index := range rawHits {
		if result.Hits[index].SourceRecordID != rawHits[index].SourceRecordID {
			t.Fatalf("single knowledge base order changed: %#v", result.Hits)
		}
	}
}

func TestApplyKnowledgeBasePriorityKeepsAllExistingStoreKnowledgeBasesInOneLayer(t *testing.T) {
	rawHits := []rag.RetrieveResult{
		{KnowledgeBaseID: 5, SourceRecordID: "general-1", Content: "通用答案", Score: 0.99},
		{KnowledgeBaseID: 4, SourceRecordID: "store-2", Content: "门店补充答案", Score: 0.75},
		{KnowledgeBaseID: 3, SourceRecordID: "store-1", Content: "门店主答案", Score: 0.7},
	}
	result := &KnowledgeRetrieveResult{}
	applyKnowledgeBasePriority(result, []int64{3, 4}, []int64{3, 4, 5}, rawHits)

	if len(result.Hits) != 2 {
		t.Fatalf("store layer hits=%d, want 2: %#v", len(result.Hits), result.Hits)
	}
	for _, hit := range result.Hits {
		if hit.KnowledgeBaseID == 5 {
			t.Fatalf("general hit leaked into winning store layer: %#v", result.Hits)
		}
	}
}

func TestStoreKnowledgeBaseFailureBlocksGeneralFallback(t *testing.T) {
	storeFailure := &rag.RetrieveTrace{FailedKnowledgeBaseIDs: []int64{3}}
	if !shouldBlockGeneralKnowledgeFallback([]int64{3}, []rag.RetrieveResult{{KnowledgeBaseID: 4}}, storeFailure) {
		t.Fatal("store failure must block general fallback")
	}
	if shouldBlockGeneralKnowledgeFallback([]int64{3, 4}, []rag.RetrieveResult{{KnowledgeBaseID: 4}}, storeFailure) {
		t.Fatal("a successful hit from another store-layer knowledge base must preserve existing behavior")
	}
	if shouldBlockGeneralKnowledgeFallback([]int64{3}, []rag.RetrieveResult{{KnowledgeBaseID: 4}}, &rag.RetrieveTrace{FailedKnowledgeBaseIDs: []int64{4}}) {
		t.Fatal("general failure must not block a successful store answer")
	}
}
