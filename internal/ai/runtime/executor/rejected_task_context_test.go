package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestRejectedAnswerReturnsToUniqueFailedKnowledgeTask(t *testing.T) {
	previous := callbacks.RuntimeTraceData{}
	previous.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", Intent: "hotel_info", SubIntent: "parking", Text: "停车从哪里进", NeedsKnowledge: true},
		{TaskID: "T2", Intent: "hotel_info", SubIntent: "food_delivery", Text: "外卖机器人能送上楼吗", NeedsKnowledge: true},
	}
	previous.Pipeline.EvidenceJudge.Tasks = []callbacks.KnowledgeEvidenceJudgeTaskTraceData{
		{TaskID: "T1", Decision: knowledgeEvidenceDecisionDirectSingle},
		{TaskID: "T2", Decision: knowledgeEvidenceDecisionInsufficient},
	}
	for _, relation := range []string{"answer_rejected", "correction"} {
		intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "interaction", SubIntent: "frustration", Text: "啥玩意", ResolvedText: "啥玩意", SourceRefs: []string{"U1"}, RelationToPrevious: relation},
		}}
		got, changed := rebindRuntimeRejectedTask(intent, previous)
		if !changed || len(got.IntentTasks) != 1 || got.IntentTasks[0].SubIntent != "food_delivery" ||
			!got.IntentTasks[0].NeedsKnowledge || got.NeedsHumanRoute || !strings.Contains(got.IntentTasks[0].ResolvedText, "外卖机器人") {
			t.Fatalf("correction did not re-enter its failed business task: %#v", got)
		}
		if got.IntentTasks[0].Text != "啥玩意" || len(got.IntentTasks[0].SourceRefs) != 1 || got.IntentTasks[0].SourceRefs[0] != "U1" ||
			intent.IntentTasks[0].Intent != "interaction" {
			t.Fatalf("correction lost provenance or mutated input: %#v", got)
		}
	}
}

func TestRejectedAnswerDoesNotGuessAcrossMultipleFailuresOrRepeatActions(t *testing.T) {
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{
		{Intent: "interaction", SubIntent: "frustration", Text: "不是这个意思", RelationToPrevious: "correction"},
	}}
	for _, tasks := range [][]callbacks.ReplyTaskPlanTraceData{
		{
			{TaskID: "T1", Intent: "hotel_info", SubIntent: "food_delivery", Text: "外卖", NeedsKnowledge: true},
			{TaskID: "T2", Intent: "hotel_info", SubIntent: "parking", Text: "停车", NeedsKnowledge: true},
		},
		{{TaskID: "T1", Intent: "service_request", SubIntent: "create_ticket", Text: "创建维修工单", NeedsTool: true}},
		{{TaskID: "T1", Intent: "hotel_variable", Text: "入住小程序", NeedsResource: true}},
	} {
		previous := callbacks.RuntimeTraceData{}
		previous.Pipeline.ReplyPlan.TaskPlans = tasks
		if _, changed := rebindRuntimeRejectedTask(intent, previous); changed {
			t.Fatalf("ambiguous or executable task was silently replayed: %#v", tasks)
		}
	}
}

func TestRejectedAnswerDoesNotReplaceExplicitCorrectedBusinessTarget(t *testing.T) {
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", SubIntent: "invoice", Text: "我问的是发票", RelationToPrevious: "correction", NeedsKnowledge: true},
	}}
	previous := callbacks.RuntimeTraceData{}
	previous.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{{TaskID: "T1", Intent: "hotel_info", SubIntent: "food_delivery", Text: "外卖怎么送", NeedsKnowledge: true}}
	if _, changed := rebindRuntimeRejectedTask(intent, previous); changed {
		t.Fatal("an actionable current correction was overridden by old topic")
	}
}
