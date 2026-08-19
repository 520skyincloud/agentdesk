package contextcompiler

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestRequiredEvidencePrefersAuthoritativeStoreFact(t *testing.T) {
	bundle := &contracts.EvidenceBundleV1{Items: []contracts.EvidenceItemV1{
		{Ref: "K1", SourceType: "fastgpt", TaskKeys: []string{"t1"}, Content: "知识答案", Score: 1, Answerability: "supporting"},
		{Ref: "S1", SourceType: "store_fact", TaskKeys: []string{"t1"}, Content: "系统事实", Score: 0.5, Answerability: "supporting"},
	}}
	selected, ok := selectRequiredEvidence(bundle, []string{"t1"})
	if !ok || len(selected) != 1 || selected[0].Ref != "S1" {
		t.Fatalf("required evidence=%#v ok=%v", selected, ok)
	}
}

func TestProjectedEvidenceRemovesFAQLabels(t *testing.T) {
	bundle := &contracts.EvidenceBundleV1{SchemaVersion: contracts.EvidenceBundleV1SchemaVersion, Items: []contracts.EvidenceItemV1{{
		Ref: "K1", SourceType: "fastgpt", Content: "问题：怎么入住？ 答案：先在小程序登记，再刷脸开门。",
	}}}
	projected := projectEvidence(bundle, bundle.Items)
	if len(projected.Items) != 1 || strings.Contains(projected.Items[0].Content, "答案：") || strings.Contains(projected.Items[0].Content, "问题：") {
		t.Fatalf("projected evidence leaked FAQ wrapper: %#v", projected.Items)
	}
}
