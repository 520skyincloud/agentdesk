package vectordb

import (
	"testing"

	"github.com/qdrant/go-client/qdrant"
)

func TestQdrantFilterRequiresTenantWithKnowledgeScope(t *testing.T) {
	provider := &QdrantProvider{}
	filter := provider.buildFilter(&SearchFilter{TenantID: 12, KnowledgeBaseIDs: []int64{34}})
	if filter == nil || len(filter.Must) != 2 {
		t.Fatalf("filter=%+v want tenant and knowledge conditions", filter)
	}
	assertTenantFilter(t, filter.Must, 12)

	failClosed := provider.buildFilter(&SearchFilter{KnowledgeBaseIDs: []int64{34}})
	if failClosed == nil || len(failClosed.Must) != 2 {
		t.Fatalf("fail-closed filter=%+v want tenant and knowledge conditions", failClosed)
	}
	assertTenantFilter(t, failClosed.Must, -1)
}

func assertTenantFilter(t *testing.T, conditions []*qdrant.Condition, want int64) {
	t.Helper()
	for _, condition := range conditions {
		field := condition.GetField()
		if field != nil && field.GetKey() == "tenant_id" {
			if field.GetMatch().GetInteger() != want {
				t.Fatalf("tenant filter=%d want=%d", field.GetMatch().GetInteger(), want)
			}
			return
		}
	}
	t.Fatal("tenant_id condition not found")
}
