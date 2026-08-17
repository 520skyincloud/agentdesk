package knowledgepolicy

import (
	"testing"

	"agent-desk/internal/ai/rag"
)

func TestEvidenceClassificationUsesCurrentQuestionNotParentRetrievalHint(t *testing.T) {
	result := Judge(EvidenceJudgeInput{
		TenantID: 1,
		StoreID:  2,
		Task: Task{
			Query:         "附近有什么好玩的；早餐几点",
			EvidenceQuery: "早餐几点",
			Intent:        "hotel_info",
			SubIntent:     "breakfast_time",
		},
		Candidate: rag.RetrieveResult{
			KnowledgeBaseID: 7,
			SourceRecordID:  "breakfast-time",
			Title:           "早餐供应时间",
			Content:         "问题：早餐几点？答案：早餐七点开始。",
			Score:           0.9,
		},
	})
	if result.ClaimType != "fact" {
		t.Fatalf("claim type=%q, want fact from current question", result.ClaimType)
	}
	if result.Answerability == "blocked" || result.Answerability == "context_only" {
		t.Fatalf("current-topic evidence was downgraded by parent retrieval hint: %+v", result)
	}
}
