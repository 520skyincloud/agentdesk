package executor

import (
	"context"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
)

func TestRuntimeKnowledgeTaskIDsSurviveKnowledgeResourceKnowledgeFlow(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID:        "task-1",
			Intent:        "hotel_info",
			Text:          "早餐几点",
			ResolvedText:  "早餐几点",
			OutputKind:    "text",
			ReplyRequired: true,
			Output:        "knowledge_text_reply",
		},
		{
			TaskID:         "task-2",
			Intent:         "hotel_variable",
			Text:           "把入住小程序发给我",
			ResolvedText:   "把入住小程序发给我",
			OutputKind:     "resource",
			Output:         "structured_resource_commit",
			ResourceAction: "provide_mini_program",
		},
		{
			TaskID:        "task-3",
			Intent:        "hotel_info",
			Text:          "停车免费吗",
			ResolvedText:  "停车免费吗",
			OutputKind:    "text",
			ReplyRequired: true,
			Output:        "knowledge_text_reply",
		},
	}}
	retriever := &fakeKnowledgeContextRetriever{
		knowledgeBaseIDs: []int64{1},
		resultsByQuery: map[string]*retrievers.KnowledgeRetrieveResult{
			"早餐几点":  {KnowledgeBaseIDs: []int64{1}, Query: "早餐几点"},
			"停车免费吗": {KnowledgeBaseIDs: []int64{1}, Query: "停车免费吗"},
		},
	}

	batch, err := retrieveContextForRuntimeQuestions(
		context.Background(),
		retriever,
		retrievers.DefaultKnowledgeRetrieveOptions(),
		"早餐几点，把入住小程序发给我，停车免费吗",
		callbacks.IntentTraceData{NeedsKnowledge: true},
		plan,
	)
	if err != nil {
		t.Fatalf("retrieveContextForRuntimeQuestions returned error: %v", err)
	}
	if len(batch.Questions) != 2 {
		t.Fatalf("expected two knowledge questions, got %#v", batch.Questions)
	}
	if batch.Questions[0].TaskID != "task-1" || batch.Questions[0].Query != "早餐几点" {
		t.Fatalf("first knowledge task lost its ReplyPlan identity: %#v", batch.Questions[0])
	}
	if batch.Questions[1].TaskID != "task-3" || batch.Questions[1].Query != "停车免费吗" {
		t.Fatalf("second knowledge task lost its ReplyPlan identity: %#v", batch.Questions[1])
	}

	rebuilt := rebuildRuntimeKnowledgeReplyPlan(plan, batch.Questions, nil, false)
	if len(rebuilt.TaskPlans) != 3 {
		t.Fatalf("expected knowledge-resource-knowledge plan to remain intact, got %#v", rebuilt.TaskPlans)
	}
	for index, taskID := range []string{"task-1", "task-2", "task-3"} {
		if rebuilt.TaskPlans[index].TaskID != taskID {
			t.Fatalf("task order changed at index %d: got %#v", index, rebuilt.TaskPlans)
		}
	}
}

func TestKnowledgeEvidenceFactsBindByStableTaskIDForSimilarQuestions(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", Text: "合柴房型有办公桌吗", ResolvedText: "合柴房型有办公桌吗", Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "hotel_info", Text: "麦田房型有办公桌吗", ResolvedText: "麦田房型有办公桌吗", Output: "knowledge_text_reply"},
	}}
	questions := []runtimeKnowledgeQuestionResult{
		{TaskID: "task-1", Query: "合柴房型有办公桌吗"},
		{TaskID: "task-2", Query: "麦田房型有办公桌吗"},
	}
	trace := callbacks.KnowledgeEvidenceJudgeTraceData{Tasks: []callbacks.KnowledgeEvidenceJudgeTaskTraceData{
		{
			TaskID:        "task-2",
			SelectedLayer: knowledgeEvidenceLayerStore,
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
				FactID:    "task-2F1",
				Aspect:    "existence",
				Statement: "麦田房型没有办公桌。",
			}},
		},
		{
			TaskID:        "task-1",
			SelectedLayer: knowledgeEvidenceLayerStore,
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
				FactID:    "task-1F1",
				Aspect:    "existence",
				Statement: "合柴房型有办公桌。",
			}},
		},
	}}

	got := applyKnowledgeEvidenceJudgeTraceToReplyPlan(plan, trace, questions)
	if len(got.TaskPlans[0].SupportedFacts) != 1 || got.TaskPlans[0].SupportedFacts[0].Statement != "合柴房型有办公桌。" {
		t.Fatalf("task-1 received facts from the wrong similar question: %#v", got.TaskPlans[0])
	}
	if len(got.TaskPlans[1].SupportedFacts) != 1 || got.TaskPlans[1].SupportedFacts[0].Statement != "麦田房型没有办公桌。" {
		t.Fatalf("task-2 received facts from the wrong similar question: %#v", got.TaskPlans[1])
	}
}

func TestKnowledgeEvidenceTraceDoesNotUseFirstUnusedTaskForStablePlan(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", ResolvedText: "早餐几点", Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "hotel_info", Text: "停车免费吗", ResolvedText: "停车免费吗", Output: "knowledge_text_reply"},
	}}
	questions := []runtimeKnowledgeQuestionResult{
		{TaskID: "task-9", Query: "早餐几点"},
		{TaskID: "task-2", Query: "停车免费吗"},
	}
	trace := callbacks.KnowledgeEvidenceJudgeTraceData{Tasks: []callbacks.KnowledgeEvidenceJudgeTaskTraceData{
		{
			TaskID:        "task-9",
			SelectedLayer: knowledgeEvidenceLayerStore,
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
				FactID:    "task-9F1",
				Statement: "这条证据不属于 task-1。",
			}},
		},
		{
			TaskID:        "task-2",
			SelectedLayer: knowledgeEvidenceLayerStore,
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{
				FactID:    "task-2F1",
				Statement: "停车免费。",
			}},
		},
	}}

	got := applyKnowledgeEvidenceJudgeTraceToReplyPlan(plan, trace, questions)
	if len(got.TaskPlans[0].SupportedFacts) != 0 || got.TaskPlans[0].SelectedLayer != "" {
		t.Fatalf("stable task-1 must not consume an unmatched Judge task: %#v", got.TaskPlans[0])
	}
	if len(got.TaskPlans[1].SupportedFacts) != 1 || got.TaskPlans[1].SupportedFacts[0].Statement != "停车免费。" {
		t.Fatalf("task-2 should still receive its exact evidence: %#v", got.TaskPlans[1])
	}
}
