package executor

import "agent-desk/internal/ai/runtime/internal/impl/callbacks"

func buildKnowledgeEvidenceCandidateTrace(task knowledgeEvidenceJudgeTask, selectedIDs []string) []callbacks.KnowledgeEvidenceCandidateTraceData {
	visible := make(map[string]bool, len(task.Candidates))
	for _, candidate := range task.Candidates {
		visible[candidate.CandidateID] = true
	}
	selected := make(map[string]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		selected[id] = true
	}
	raw := allKnowledgeEvidenceJudgeTaskCandidates(task)
	items := make([]callbacks.KnowledgeEvidenceCandidateTraceData, 0, len(raw))
	for _, candidate := range raw {
		disposition := "candidate_budget_excluded"
		if visible[candidate.CandidateID] {
			disposition = "judge_not_selected"
		}
		if selected[candidate.CandidateID] {
			disposition = "selected_for_reply"
		}
		question, _ := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		items = append(items, callbacks.KnowledgeEvidenceCandidateTraceData{
			CandidateID: candidate.CandidateID, Layer: candidate.Layer,
			KnowledgeBaseID: candidate.Hit.KnowledgeBaseID,
			SourceRecordID:  candidate.Hit.SourceRecordID, RawRankNo: candidate.RawRankNo,
			QuestionPreview: preview(question, 240), ContentPreview: preview(candidate.Hit.Content, 800),
			Score: float64(candidate.Hit.Score), EnteredJudge: visible[candidate.CandidateID],
			Selected: selected[candidate.CandidateID], Disposition: disposition,
		})
	}
	return items
}
