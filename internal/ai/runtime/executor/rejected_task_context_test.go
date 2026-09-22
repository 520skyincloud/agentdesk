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

func TestRejectedAnswerDoesNotReplayCompletedSingleTask(t *testing.T) {
	previous := callbacks.RuntimeTraceData{Status: "completed"}
	previous.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", SubIntent: "parking", Text: "酒店有停车场吗", NeedsKnowledge: true,
		OutputKind: "text", ReplyRequired: true, SelectedLayer: "store",
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "T1F1", Statement: "酒店提供免费停车服务。"}},
	}}
	previous.Pipeline.EvidenceJudge.Tasks = []callbacks.KnowledgeEvidenceJudgeTaskTraceData{{
		TaskID: "T1", Decision: knowledgeEvidenceDecisionDirectSingle,
	}}
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{
		Intent: "interaction", SubIntent: "frustration", Text: "不是这个意思", RelationToPrevious: "correction",
	}}}

	if got, changed := rebindRuntimeRejectedTask(intent, previous); changed {
		t.Fatalf("a completed single task must not be replayed only because it is the sole candidate: %#v", got)
	}
}

func TestRejectedAnswerRestoresSingleTaskWithExplicitProtocolFailure(t *testing.T) {
	previous := callbacks.RuntimeTraceData{Status: "error"}
	previous.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", SubIntent: "parking", Text: "酒店有停车场吗", NeedsKnowledge: true,
		OutputKind: "text", ReplyRequired: true, SelectedLayer: "store",
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "T1F1", Statement: "酒店提供免费停车服务。"}},
	}}
	previous.Pipeline.Validate.Status = "failed"
	previous.Output.FinishReason = "generated_reply_protocol_error"
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{
		Intent: "interaction", SubIntent: "frustration", Text: "刚才没回答我", RelationToPrevious: "answer_rejected",
	}}}

	got, changed := rebindRuntimeRejectedTask(intent, previous)
	if !changed || got.IntentTasks[0].SubIntent != "parking" || !strings.Contains(got.IntentTasks[0].ResolvedText, "酒店有停车场吗") {
		t.Fatalf("the failed single task must remain retryable: %#v", got)
	}
}

func TestRejectedAnswerDoesNotReplayCommittedSiblingAfterActionFailure(t *testing.T) {
	previous := callbacks.RuntimeTraceData{Status: "error"}
	previous.Error.Stage = "tool"
	previous.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID: "T1", Intent: "hotel_info", SubIntent: "parking", Text: "酒店有停车场吗", NeedsKnowledge: true,
			OutputKind: "text", ReplyRequired: true, SelectedLayer: "store",
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "T1F1", Statement: "酒店提供免费停车服务。"}},
		},
		{TaskID: "T2", Intent: "service_request", SubIntent: "create_ticket", Text: "帮我登记维修", NeedsTool: true},
	}
	previous.Pipeline.EvidenceJudge.Tasks = []callbacks.KnowledgeEvidenceJudgeTaskTraceData{{
		TaskID: "T1", Decision: knowledgeEvidenceDecisionDirectSingle,
	}}
	previous.Output.CommitMessages = []callbacks.CommitMessageTraceData{{Status: "sent", TaskIDs: []string{"T1"}}}
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{
		Intent: "interaction", SubIntent: "frustration", Text: "刚才没处理好", RelationToPrevious: "answer_rejected",
	}}}

	if got, changed := rebindRuntimeRejectedTask(intent, previous); changed {
		t.Fatalf("a sent sibling must not be replayed because another action failed: %#v", got)
	}
}

func TestRejectedAnswerRestoresUncommittedTaskAfterGlobalFailure(t *testing.T) {
	previous := callbacks.RuntimeTraceData{Status: "error"}
	previous.Error.Stage = "generate"
	previous.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "T1", Intent: "hotel_info", SubIntent: "parking", Text: "酒店有停车场吗", NeedsKnowledge: true,
		OutputKind: "text", ReplyRequired: true, SelectedLayer: "store",
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "T1F1", Statement: "酒店提供免费停车服务。"}},
	}}
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{
		Intent: "interaction", SubIntent: "frustration", Text: "刚才没回答", RelationToPrevious: "answer_rejected",
	}}}

	got, changed := rebindRuntimeRejectedTask(intent, previous)
	if !changed || got.IntentTasks[0].SubIntent != "parking" {
		t.Fatalf("an uncommitted task must remain retryable after a global failure: %#v", got)
	}
}

func TestRejectedAnswerReturnsToUniqueIncompletePMSTask(t *testing.T) {
	for _, subIntent := range []string{"order_query", "room_upgrade"} {
		previous := callbacks.RuntimeTraceData{}
		previous.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{{
			TaskID: "T1", Intent: "hotel_info", SubIntent: subIntent, Text: "帮我查这笔订单能不能升房", NeedsTool: false,
			OutputKind: "text", ReplyRequired: true,
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "T1P1", Aspect: "pms_order_reserve", Statement: "已定位当前订单。"}},
			MissingAspects: []string{"目标日期库存暂未确认"},
		}}
		intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "interaction", SubIntent: "frustration", Text: "我问的是刚才那个", RelationToPrevious: "answer_rejected",
		}}}

		got, changed := rebindRuntimeRejectedTask(intent, previous)
		if !changed || len(got.IntentTasks) != 1 || got.IntentTasks[0].SubIntent != subIntent || !got.IntentTasks[0].NeedsTool ||
			!strings.Contains(got.IntentTasks[0].ResolvedText, "帮我查这笔订单能不能升房") {
			t.Fatalf("incomplete PMS task was not restored for %s: %#v", subIntent, got)
		}
	}
}

func TestRejectedAnswerReturnsToUniqueTaskAfterGenerateOrValidateFailure(t *testing.T) {
	for _, configure := range []func(*callbacks.RuntimeTraceData){
		func(previous *callbacks.RuntimeTraceData) {
			previous.Status = "error"
			previous.Pipeline.Generate.Status = "failed"
			previous.Error.Stage = "generate"
		},
		func(previous *callbacks.RuntimeTraceData) {
			previous.Pipeline.Validate.Status = "failed"
			previous.Output.FinishReason = "generated_reply_protocol_error"
		},
	} {
		previous := callbacks.RuntimeTraceData{}
		previous.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{{
			TaskID: "T1", Intent: "hotel_info", SubIntent: "parking", Text: "停车场从哪里进", NeedsKnowledge: true,
			OutputKind: "text", ReplyRequired: true, SelectedLayer: "store",
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{{FactID: "T1F1", Statement: "酒店提供免费停车服务。"}},
		}}
		configure(&previous)
		intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "interaction", SubIntent: "frustration", Text: "刚才没回答我", RelationToPrevious: "correction",
		}}}

		got, changed := rebindRuntimeRejectedTask(intent, previous)
		if !changed || got.IntentTasks[0].SubIntent != "parking" || !got.IntentTasks[0].NeedsKnowledge ||
			!strings.Contains(got.IntentTasks[0].ResolvedText, "停车场从哪里进") {
			t.Fatalf("failed Generate/Validate task was not restored: %#v", got)
		}
	}
}
