package retrievers

import (
	"testing"

	"agent-desk/internal/ai/rag"
)

func TestRuntimeRerankAuditUsesEffectivePolicy(t *testing.T) {
	result := &KnowledgeRetrieveResult{
		Policies: []KnowledgeBaseRetrievePolicy{
			{KnowledgeBaseID: 1, RerankLimit: 3},
			{KnowledgeBaseID: 2, RerankLimit: 8},
		},
	}
	if !runtimeRerankEnabled(result) {
		t.Fatal("expected rerank audit to be enabled from the effective knowledge policy")
	}
	if got := runtimeRerankLimit(result.Policies); got != 8 {
		t.Fatalf("runtimeRerankLimit() = %d, want 8", got)
	}
}

func TestRuntimeRerankAuditUsesProviderTrace(t *testing.T) {
	result := &KnowledgeRetrieveResult{Trace: &rag.RetrieveTrace{RerankCount: 1}}
	if !runtimeRerankEnabled(result) {
		t.Fatal("expected provider trace to mark rerank enabled")
	}
	if got := runtimeRerankLimit(result.Policies); got != 0 {
		t.Fatalf("runtimeRerankLimit() = %d, want 0 without a configured limit", got)
	}
}

func TestRuntimeRerankAuditDisabledWithoutPolicyOrTrace(t *testing.T) {
	if runtimeRerankEnabled(&KnowledgeRetrieveResult{}) {
		t.Fatal("rerank audit must stay disabled without policy or provider evidence")
	}
}
