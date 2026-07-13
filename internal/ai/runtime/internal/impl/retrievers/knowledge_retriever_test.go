package retrievers

import (
	"testing"

	"agent-desk/internal/pkg/dto/response"
)

func TestInferRuntimeRetrieveSourceTypeRecognizesFastGPTEngineResults(t *testing.T) {
	hits := []response.KnowledgeSearchResult{{SectionPath: "FastGPT知识库/3/collection-1"}}
	if got := inferRuntimeRetrieveSourceType(hits); got != "fastgpt" {
		t.Fatalf("source type=%q", got)
	}
}
