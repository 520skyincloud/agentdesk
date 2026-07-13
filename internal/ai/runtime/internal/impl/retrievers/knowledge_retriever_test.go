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
