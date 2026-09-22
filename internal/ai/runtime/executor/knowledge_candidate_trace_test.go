package executor

import (
	"testing"

	"agent-desk/internal/ai/rag"
)

func TestKnowledgeCandidateTraceSeparatesBudgetAndJudgeSelection(t *testing.T) {
	candidates := []knowledgeEvidenceJudgeCandidate{
		{CandidateID: "T1C1", Layer: "store", RawRankNo: 1, Hit: rag.RetrieveResult{KnowledgeBaseID: 7, SourceRecordID: "delivery-address", Content: "问题：外卖地址怎么填\n答案：填写门店地址"}},
		{CandidateID: "T1C2", Layer: "store", RawRankNo: 2, Hit: rag.RetrieveResult{KnowledgeBaseID: 7, SourceRecordID: "delivery-robot", Content: "问题：外卖怎么送上楼\n答案：外卖机器人送上楼"}},
		{CandidateID: "T1C3", Layer: "general", RawRankNo: 3, Hit: rag.RetrieveResult{KnowledgeBaseID: 8, SourceRecordID: "general-policy", Content: "问题：配送规则\n答案：联系门店"}},
	}
	trace := buildKnowledgeEvidenceCandidateTrace(knowledgeEvidenceJudgeTask{
		Query: "外卖机器人可以送上来吗", RawCandidates: candidates, Candidates: candidates[:2],
	}, []string{"T1C2"})
	if len(trace) != 3 || trace[0].Disposition != "judge_not_selected" ||
		trace[1].Disposition != "selected_for_reply" || trace[2].Disposition != "candidate_budget_excluded" {
		t.Fatalf("candidate chain lost exact stage: %#v", trace)
	}
	if trace[1].SourceRecordID != "delivery-robot" || trace[1].RawRankNo != 2 || !trace[1].EnteredJudge || !trace[1].Selected {
		t.Fatalf("selected candidate lost original retrieval identity: %#v", trace[1])
	}
}

func TestKnowledgeCandidateTraceShowsFallbackSelectionOutsideJudge(t *testing.T) {
	raw := []knowledgeEvidenceJudgeCandidate{
		{CandidateID: "T1C1", Layer: "store", RawRankNo: 1, Hit: rag.RetrieveResult{SourceRecordID: "mixed-doc", FaqQuestion: "外卖地址", Content: "地址"}},
		{CandidateID: "T1C2", Layer: "store", RawRankNo: 1, Hit: rag.RetrieveResult{SourceRecordID: "mixed-doc", FaqQuestion: "机器人", Content: "送到房间"}},
	}
	trace := buildKnowledgeEvidenceCandidateTrace(knowledgeEvidenceJudgeTask{Candidates: raw[:1], RawCandidates: raw}, []string{"T1C2"})
	if trace[1].EnteredJudge || !trace[1].Selected || trace[0].RawRankNo != trace[1].RawRankNo {
		t.Fatalf("expanded FAQ and deterministic fallback provenance were conflated: %#v", trace)
	}
}
