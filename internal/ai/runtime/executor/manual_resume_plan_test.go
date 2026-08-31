package executor

import (
	"context"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyruntime"
)

type recordingManualResumeIntentDetector struct {
	calls int
	req   RunInput
	value callbacks.IntentTraceData
}

func (d *recordingManualResumeIntentDetector) DetectRuntimeIntent(_ context.Context, req RunInput, _ adapter.HistoryBuildResult, _ []models.ReplyIntentConfig) (callbacks.IntentTraceData, error) {
	d.calls++
	d.req = req
	return d.value, nil
}

func TestManualResumeFrozenTaskBypassesIntent(t *testing.T) {
	snapshot := replyruntime.ManualResumeSnapshot{
		RunLogID:         44,
		ContractMode:     replyruntime.ManualResumeContractV2,
		SourcesValidated: true,
		Sources:          []replyruntime.ManualResumeSource{{Ref: "U1", MessageID: 101, MessageType: "text", Text: "拖鞋去哪里拿？"}},
		FrozenTasks: []replyruntime.ManualResumeTaskPlan{{
			TaskID: "task-7", Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "location",
			RelationToPrevious: "independent", ResolutionState: "clear", OriginalText: "拖鞋去哪里拿？", ResolvedText: "拖鞋去哪里拿？", SourceRefs: []string{"U1"},
		}},
	}
	ctx := replyruntime.WithManualResumeSnapshot(context.Background(), snapshot)
	detector := &recordingManualResumeIntentDetector{}
	intent, _, configured, manualResume := detectRuntimeIntentForPipeline(ctx, RunInput{UserMessage: models.Message{ID: 101, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿？"}}, adapter.HistoryBuildResult{}, detector)
	if !configured || !manualResume || detector.calls != 0 {
		t.Fatalf("frozen manual-resume tasks must bypass Intent: configured=%v manual=%v calls=%d", configured, manualResume, detector.calls)
	}
	if len(intent.IntentTasks) != 1 || intent.IntentTasks[0].Text != "拖鞋去哪里拿？" || strings.Join(intent.IntentTasks[0].SourceRefs, ",") != "U1" {
		t.Fatalf("frozen TaskPlan boundary must be reused exactly, got %#v", intent.IntentTasks)
	}
	plan := buildRuntimePipelinePlanWithModel(ctx, RunInput{UserMessage: models.Message{ID: 101, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿？"}}, adapter.HistoryBuildResult{}, detector)
	if len(plan.ReplyPlan.TaskPlans) != 1 || plan.ReplyPlan.TaskPlans[0].TaskID != "task-7" {
		t.Fatalf("frozen TaskID must survive the rebuilt ReplyPlan, got %#v", plan.ReplyPlan.TaskPlans)
	}
}

func TestManualResumeOnlyNewSourcesEnterIntentAndMergeInSourceOrder(t *testing.T) {
	snapshot := replyruntime.ManualResumeSnapshot{
		RunLogID:         45,
		ContractMode:     replyruntime.ManualResumeContractV2,
		SourcesValidated: true,
		Sources: []replyruntime.ManualResumeSource{
			{Ref: "U1", MessageID: 201, MessageType: "text", Text: "拖鞋去哪里拿？"},
			{Ref: "U2", MessageID: 202, MessageType: "text", Text: "停车免费吗？"},
		},
		NewSources: []replyruntime.ManualResumeSource{{Ref: "U2", MessageID: 202, MessageType: "text", Text: "停车免费吗？"}},
		FrozenTasks: []replyruntime.ManualResumeTaskPlan{{
			TaskID: "task-1", Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "location",
			RelationToPrevious: "independent", ResolutionState: "clear", OriginalText: "拖鞋去哪里拿？", ResolvedText: "拖鞋去哪里拿？", SourceRefs: []string{"U1"},
		}},
	}
	detector := &recordingManualResumeIntentDetector{value: callbacks.IntentTraceData{
		PrimaryIntent:            "hotel_info",
		SemanticContractExpected: true,
		SourceRefsValidated:      true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "hotel_info", SubIntent: "parking", Objective: "price", RelationToPrevious: "independent", ResolutionState: "clear",
			Text: "停车免费吗？", ResolvedText: "停车免费吗？", SourceRefs: []string{"U1"}, NeedsKnowledge: true,
		}},
	}}
	ctx := replyruntime.WithManualResumeSnapshot(context.Background(), snapshot)
	request := RunInput{UserMessage: models.Message{ID: 202, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿？\n停车免费吗？"}}
	intent, _, configured, manualResume := detectRuntimeIntentForPipeline(ctx, request, adapter.HistoryBuildResult{}, detector)
	if !configured || !manualResume || detector.calls != 1 {
		t.Fatalf("only genuinely new sources should trigger one Intent call: configured=%v manual=%v calls=%d", configured, manualResume, detector.calls)
	}
	if strings.Contains(detector.req.UserMessage.Content, "拖鞋") || !strings.Contains(detector.req.UserMessage.Content, "停车免费吗") {
		t.Fatalf("Intent input must contain only the new physical source, got %q", detector.req.UserMessage.Content)
	}
	if len(intent.IntentTasks) != 2 || strings.Join(intent.IntentTasks[0].SourceRefs, ",") != "U1" || strings.Join(intent.IntentTasks[1].SourceRefs, ",") != "U2" {
		t.Fatalf("frozen and new tasks must merge in physical source order with remapped refs, got %#v", intent.IntentTasks)
	}
}

func TestManualResumeLegacySnapshotDoesNotClaimV2Contract(t *testing.T) {
	snapshot := replyruntime.ManualResumeSnapshot{
		RunLogID:     46,
		ContractMode: replyruntime.ManualResumeContractLegacy,
		Sources:      []replyruntime.ManualResumeSource{{Ref: "U1", MessageID: 301, MessageType: "text", Text: "拖鞋去哪里拿？"}},
		FrozenTasks: []replyruntime.ManualResumeTaskPlan{{
			TaskID: "task-3", Intent: "hotel_info", SubIntent: "supplies_self_help",
			OriginalText: "拖鞋去哪里拿？", ResolvedText: "拖鞋去哪里拿？", SourceRefs: []string{"U1"},
		}},
	}
	ctx := replyruntime.WithManualResumeSnapshot(context.Background(), snapshot)
	detector := &recordingManualResumeIntentDetector{}
	intent, _, configured, manualResume := detectRuntimeIntentForPipeline(ctx, RunInput{UserMessage: models.Message{ID: 301, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿？"}}, adapter.HistoryBuildResult{}, detector)
	if !configured || !manualResume || detector.calls != 0 {
		t.Fatalf("safe legacy frozen tasks should remain reusable without another Intent call: configured=%v manual=%v calls=%d", configured, manualResume, detector.calls)
	}
	if intent.SemanticContractExpected || intent.SourceRefsValidated {
		t.Fatalf("legacy recovery must not masquerade as a validated V2 result: %#v", intent)
	}
}

func TestManualResumeFrozenTaskPreservesExecutionMetadata(t *testing.T) {
	snapshot := replyruntime.ManualResumeSnapshot{
		RunLogID:         47,
		ContractMode:     replyruntime.ManualResumeContractV2,
		SourcesValidated: true,
		Sources:          []replyruntime.ManualResumeSource{{Ref: "U1", MessageID: 401, MessageType: "text", Text: "拖鞋去哪里拿？"}},
		FrozenTasks: []replyruntime.ManualResumeTaskPlan{{
			TaskID: "task-9", Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "location",
			RelationToPrevious: "independent", ResolutionState: "clear",
			Entities:       []replyruntime.ManualResumeEntity{{Text: "拖鞋", Type: "supply"}},
			OriginalText:   "拖鞋去哪里拿？",
			ResolvedText:   "拖鞋去哪里拿？",
			SourceRefs:     []string{"U1"},
			NeedsKnowledge: true,
			OutputKind:     "text",
			ReplyRequired:  true,
			Output:         "knowledge_text_reply",
			MissingAspects: []string{"location", "method"},
		}},
	}
	ctx := replyruntime.WithManualResumeSnapshot(context.Background(), snapshot)
	plan := buildRuntimePipelinePlanWithModel(ctx, RunInput{UserMessage: models.Message{ID: 401, MessageType: enums.IMMessageTypeText, Content: "拖鞋去哪里拿？"}}, adapter.HistoryBuildResult{}, &recordingManualResumeIntentDetector{})
	if len(plan.ReplyPlan.TaskPlans) != 1 {
		t.Fatalf("expected one restored task, got %#v", plan.ReplyPlan.TaskPlans)
	}
	task := plan.ReplyPlan.TaskPlans[0]
	if task.TaskID != "task-9" || len(task.Entities) != 1 || task.Entities[0].Text != "拖鞋" || !task.NeedsKnowledge ||
		task.OutputKind != "text" || !task.ReplyRequired || task.Output != "knowledge_text_reply" ||
		strings.Join(task.MissingAspects, ",") != "location,method" || strings.Join(task.SourceRefs, ",") != "U1" {
		t.Fatalf("frozen task metadata must survive intent and ReplyPlan reconstruction: %#v", task)
	}
}

func TestManualResumeDeferredKnowledgeTaskReactivatesAsTextTask(t *testing.T) {
	snapshot := replyruntime.ManualResumeSnapshot{
		RunLogID:         49,
		ContractMode:     replyruntime.ManualResumeContractV2,
		SourcesValidated: true,
		Sources:          []replyruntime.ManualResumeSource{{Ref: "U1", MessageID: 451, MessageType: "text", Text: "东西落房间了"}},
		FrozenTasks: []replyruntime.ManualResumeTaskPlan{{
			TaskID: "task-10", Intent: "service_request", SubIntent: "lost_item", Objective: "action_request",
			RelationToPrevious: "independent", ResolutionState: "clear",
			OriginalText:   "东西落房间了",
			ResolvedText:   "东西落房间了",
			SourceRefs:     []string{"U1"},
			NeedsKnowledge: true,
			OutputKind:     "handoff",
			ReplyRequired:  false,
			Output:         runtimeKnowledgeDeferredHandoffOutput,
			MissingAspects: []string{"room_number"},
		}},
	}
	ctx := replyruntime.WithManualResumeSnapshot(context.Background(), snapshot)
	plan := buildRuntimePipelinePlanWithModel(ctx, RunInput{UserMessage: models.Message{ID: 451, MessageType: enums.IMMessageTypeText, Content: "东西落房间了"}}, adapter.HistoryBuildResult{}, &recordingManualResumeIntentDetector{})
	if len(plan.ReplyPlan.TaskPlans) != 1 {
		t.Fatalf("expected one restored deferred task, got %#v", plan.ReplyPlan.TaskPlans)
	}
	task := plan.ReplyPlan.TaskPlans[0]
	if task.TaskID != "task-10" || task.OutputKind != "text" || !task.ReplyRequired || task.Output != "knowledge_text_reply" ||
		!task.NeedsKnowledge || task.NeedsHumanRoute || strings.Join(task.MissingAspects, ",") != "room_number" {
		t.Fatalf("deferred execution marker must not keep the restored knowledge Task outside Generate: %#v", task)
	}
}

func TestManualResumeExactRepeatKeepsFrozenTaskID(t *testing.T) {
	snapshot := replyruntime.ManualResumeSnapshot{
		RunLogID:         48,
		ContractMode:     replyruntime.ManualResumeContractV2,
		SourcesValidated: true,
		Sources: []replyruntime.ManualResumeSource{
			{Ref: "U1", MessageID: 501, MessageType: "text", Text: "停车免费吗？"},
			{Ref: "U2", MessageID: 502, MessageType: "text", Text: "停车免费吗？"},
		},
		NewSources: []replyruntime.ManualResumeSource{{Ref: "U2", MessageID: 502, MessageType: "text", Text: "停车免费吗？"}},
		FrozenTasks: []replyruntime.ManualResumeTaskPlan{{
			TaskID: "task-11", Intent: "hotel_info", SubIntent: "parking", Objective: "price",
			RelationToPrevious: "independent", ResolutionState: "clear",
			OriginalText: "停车免费吗？", ResolvedText: "停车免费吗？", SourceRefs: []string{"U1"},
			NeedsKnowledge: true, OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply",
		}},
	}
	detector := &recordingManualResumeIntentDetector{value: callbacks.IntentTraceData{
		SemanticContractExpected: true,
		SourceRefsValidated:      true,
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: "hotel_info", SubIntent: "parking", Objective: "price",
			RelationToPrevious: "follow_up", ResolutionState: "resolved_from_context",
			Text: "停车免费吗？", ResolvedText: "停车免费吗？", SourceRefs: []string{"U1"}, NeedsKnowledge: true,
		}},
	}}
	ctx := replyruntime.WithManualResumeSnapshot(context.Background(), snapshot)
	plan := buildRuntimePipelinePlanWithModel(ctx, RunInput{UserMessage: models.Message{ID: 502, MessageType: enums.IMMessageTypeText, Content: "停车免费吗？"}}, adapter.HistoryBuildResult{}, detector)
	if detector.calls != 1 || len(plan.ReplyPlan.TaskPlans) != 1 {
		t.Fatalf("an exact repeat should be classified once and remain one task: calls=%d plan=%#v", detector.calls, plan.ReplyPlan.TaskPlans)
	}
	task := plan.ReplyPlan.TaskPlans[0]
	if task.TaskID != "task-11" || strings.Join(task.SourceRefs, ",") != "U1,U2" {
		t.Fatalf("an exact repeat must preserve the frozen TaskID while retaining both source messages: %#v", task)
	}
}

func TestMergeManualResumeIntentTasksUsesConservativeLifecycleRules(t *testing.T) {
	frozen := callbacks.IntentTaskTraceData{
		Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "location",
		RelationToPrevious: "independent", ResolutionState: "clear",
		Entities:       []callbacks.IntentEntityTraceData{{Text: "拖鞋", Type: "supply"}},
		Text:           "拖鞋去哪里拿？",
		ResolvedText:   "拖鞋去哪里拿？",
		SourceRefs:     []string{"U1"},
		NeedsKnowledge: true,
	}

	t.Run("independent appends", func(t *testing.T) {
		incoming := callbacks.IntentTaskTraceData{
			Intent: "hotel_info", SubIntent: "parking", Objective: "price",
			RelationToPrevious: "independent", ResolutionState: "clear",
			Text: "停车免费吗？", ResolvedText: "停车免费吗？", SourceRefs: []string{"U2"}, NeedsKnowledge: true,
		}
		merged, ok := mergeManualResumeIntentTasks([]callbacks.IntentTaskTraceData{frozen}, []callbacks.IntentTaskTraceData{incoming})
		if !ok || len(merged) != 2 || merged[1].Text != "停车免费吗？" {
			t.Fatalf("an independent new task must append without altering the frozen task: %#v ok=%v", merged, ok)
		}
	})

	t.Run("exact duplicate replaces once", func(t *testing.T) {
		incoming := frozen
		incoming.SourceRefs = []string{"U2"}
		merged, ok := mergeManualResumeIntentTasks([]callbacks.IntentTaskTraceData{frozen}, []callbacks.IntentTaskTraceData{incoming})
		if !ok || len(merged) != 1 || strings.Join(merged[0].SourceRefs, ",") != "U1,U2" {
			t.Fatalf("an exact repeat must stay one task while retaining both real sources: %#v ok=%v", merged, ok)
		}
	})

	t.Run("clarification replaces unique frozen task", func(t *testing.T) {
		incoming := callbacks.IntentTaskTraceData{
			Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "location",
			RelationToPrevious: "clarification_answer", ResolutionState: "resolved_from_context",
			Entities:       []callbacks.IntentEntityTraceData{{Text: "拖鞋", Type: "supply"}},
			Text:           "我是问拖鞋在哪拿",
			ResolvedText:   "拖鞋在哪里领取？",
			SourceRefs:     []string{"U2"},
			NeedsKnowledge: true,
		}
		merged, ok := mergeManualResumeIntentTasks([]callbacks.IntentTaskTraceData{frozen}, []callbacks.IntentTaskTraceData{incoming})
		if !ok || len(merged) != 1 || merged[0].Text != incoming.Text || strings.Join(merged[0].SourceRefs, ",") != "U2,U1" {
			t.Fatalf("a uniquely grounded clarification must replace rather than duplicate its frozen task: %#v ok=%v", merged, ok)
		}
	})

	for _, relation := range []string{"correction", "modify_previous", "cancel_previous"} {
		t.Run(relation+" keeps current source primary", func(t *testing.T) {
			incoming := callbacks.IntentTaskTraceData{
				Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "location",
				RelationToPrevious: relation, ResolutionState: "resolved_from_context",
				Entities:       []callbacks.IntentEntityTraceData{{Text: "拖鞋", Type: "supply"}},
				Text:           "我说的是拖鞋",
				ResolvedText:   "客户指的是拖鞋领取位置",
				SourceRefs:     []string{"U2"},
				NeedsKnowledge: true,
			}
			merged, ok := mergeManualResumeIntentTasks([]callbacks.IntentTaskTraceData{frozen}, []callbacks.IntentTaskTraceData{incoming})
			if !ok || len(merged) != 1 || strings.Join(merged[0].SourceRefs, ",") != "U2,U1" {
				t.Fatalf("%s must replace exactly once with the current source first: %#v ok=%v", relation, merged, ok)
			}
		})
	}

	t.Run("ambiguous correction falls back", func(t *testing.T) {
		second := frozen
		second.Entities = []callbacks.IntentEntityTraceData{{Text: "牙刷", Type: "supply"}}
		second.Text = "牙刷去哪里拿？"
		second.ResolvedText = second.Text
		second.SourceRefs = []string{"U2"}
		incoming := callbacks.IntentTaskTraceData{
			Intent: "interaction", SubIntent: "correction", Objective: "explanation",
			RelationToPrevious: "correction", ResolutionState: "resolved_from_context",
			Text: "不是这个意思", ResolvedText: "不是这个意思", SourceRefs: []string{"U3"},
		}
		if merged, ok := mergeManualResumeIntentTasks([]callbacks.IntentTaskTraceData{frozen, second}, []callbacks.IntentTaskTraceData{incoming}); ok || merged != nil {
			t.Fatalf("an ambiguous correction must reject direct frozen-task execution: %#v ok=%v", merged, ok)
		}
		clarified := mergeManualResumeIntentTasksWithClarification([]callbacks.IntentTaskTraceData{frozen, second}, []callbacks.IntentTaskTraceData{incoming})
		if len(clarified) != 1 || clarified[0].Intent != "interaction" || clarified[0].SubIntent != "clarify" || clarified[0].ResolutionState != runtimeIntentResolutionAmbiguous {
			t.Fatalf("an ambiguous correction must discard every potentially affected frozen task and keep only a safe clarification: %#v", clarified)
		}
	})

	t.Run("independent task survives ambiguous correction", func(t *testing.T) {
		second := frozen
		second.Entities = []callbacks.IntentEntityTraceData{{Text: "牙刷", Type: "supply"}}
		second.Text = "牙刷去哪里拿？"
		second.ResolvedText = second.Text
		second.SourceRefs = []string{"U2"}
		independent := callbacks.IntentTaskTraceData{
			Intent: "hotel_info", SubIntent: "parking", Objective: "price",
			RelationToPrevious: "independent", ResolutionState: "clear",
			Text: "停车免费吗？", ResolvedText: "停车免费吗？", SourceRefs: []string{"U3"}, NeedsKnowledge: true,
		}
		ambiguous := callbacks.IntentTaskTraceData{
			Intent: "interaction", SubIntent: "correction", Objective: "explanation",
			RelationToPrevious: "correction", ResolutionState: "resolved_from_context",
			Text: "不是这个意思", ResolvedText: "不是这个意思", SourceRefs: []string{"U4"},
		}
		clarified := mergeManualResumeIntentTasksWithClarification(
			[]callbacks.IntentTaskTraceData{frozen, second},
			[]callbacks.IntentTaskTraceData{independent, ambiguous},
		)
		if len(clarified) != 2 || clarified[0].Text != independent.Text || clarified[1].SubIntent != "clarify" {
			t.Fatalf("safe independent work may continue, but ambiguous replacement must not execute frozen tasks: %#v", clarified)
		}
	})
}

func TestManualResumeAmbiguousMergeUsesOneIntentAndClarifiesLocally(t *testing.T) {
	snapshot := replyruntime.ManualResumeSnapshot{
		RunLogID:         49,
		ContractMode:     replyruntime.ManualResumeContractV2,
		SourcesValidated: true,
		Sources: []replyruntime.ManualResumeSource{
			{Ref: "U1", MessageID: 601, MessageType: "text", Text: "早餐几点，拖鞋和牙刷去哪里拿？"},
			{Ref: "U2", MessageID: 602, MessageType: "text", Text: "不是这个意思"},
		},
		NewSources: []replyruntime.ManualResumeSource{{Ref: "U2", MessageID: 602, MessageType: "text", Text: "不是这个意思"}},
		FrozenTasks: []replyruntime.ManualResumeTaskPlan{
			{TaskID: "task-1", Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "location", RelationToPrevious: "independent", ResolutionState: "clear", Entities: []replyruntime.ManualResumeEntity{{Text: "拖鞋", Type: "supply"}}, OriginalText: "拖鞋去哪里拿", ResolvedText: "拖鞋去哪里拿", SourceRefs: []string{"U1"}, NeedsKnowledge: true},
			{TaskID: "task-2", Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "location", RelationToPrevious: "independent", ResolutionState: "clear", Entities: []replyruntime.ManualResumeEntity{{Text: "牙刷", Type: "supply"}}, OriginalText: "牙刷去哪里拿", ResolvedText: "牙刷去哪里拿", SourceRefs: []string{"U1"}, NeedsKnowledge: true},
		},
	}
	first := callbacks.IntentTraceData{SemanticContractExpected: true, SourceRefsValidated: true, IntentTasks: []callbacks.IntentTaskTraceData{{
		Intent: "interaction", SubIntent: "correction", Objective: "explanation", RelationToPrevious: "correction", ResolutionState: "resolved_from_context",
		Text: "不是这个意思", ResolvedText: "不是这个意思", SourceRefs: []string{"U1"},
	}}}
	detector := &recordingManualResumeIntentDetector{value: first}
	ctx := replyruntime.WithManualResumeSnapshot(context.Background(), snapshot)
	intent, _, configured, manualResume := detectRuntimeIntentForPipeline(ctx, RunInput{UserMessage: models.Message{ID: 602, MessageType: enums.IMMessageTypeText, Content: snapshot.Sources[0].Text + "\n" + snapshot.Sources[1].Text}}, adapter.HistoryBuildResult{}, detector)
	if !configured || !manualResume || detector.calls != 1 {
		t.Fatalf("ambiguous lifecycle merge must use the existing new-source Intent exactly once: configured=%v manual=%v calls=%d", configured, manualResume, detector.calls)
	}
	if strings.Contains(detector.req.UserMessage.Content, "早餐") {
		t.Fatalf("the one permitted Intent input must contain only genuinely new physical sources: %q", detector.req.UserMessage.Content)
	}
	if len(intent.IntentTasks) != 1 || intent.IntentTasks[0].Intent != "interaction" || intent.IntentTasks[0].SubIntent != "clarify" || intent.IntentTasks[0].Text != "不是这个意思" {
		t.Fatalf("an ambiguous correction must not revive or execute any frozen sibling: %#v", intent.IntentTasks)
	}
}

func TestManualResumeV2SnapshotDoesNotMasqueradeLegacyNewIntentAsV2(t *testing.T) {
	snapshot := replyruntime.ManualResumeSnapshot{
		ContractMode: replyruntime.ManualResumeContractV2, SourcesValidated: true,
		Sources:     []replyruntime.ManualResumeSource{{Ref: "U1", MessageID: 701, MessageType: "text", Text: "拖鞋在哪拿"}, {Ref: "U2", MessageID: 702, MessageType: "text", Text: "停车免费吗"}},
		NewSources:  []replyruntime.ManualResumeSource{{Ref: "U2", MessageID: 702, MessageType: "text", Text: "停车免费吗"}},
		FrozenTasks: []replyruntime.ManualResumeTaskPlan{{TaskID: "task-1", Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "location", RelationToPrevious: "independent", ResolutionState: "clear", OriginalText: "拖鞋在哪拿", ResolvedText: "拖鞋在哪拿", SourceRefs: []string{"U1"}, NeedsKnowledge: true}},
	}
	detector := &recordingManualResumeIntentDetector{value: callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{{Intent: "hotel_info", SubIntent: "parking", Text: "停车免费吗", ResolvedText: "停车免费吗", SourceRefs: []string{"U1"}, NeedsKnowledge: true}}}}
	ctx := replyruntime.WithManualResumeSnapshot(context.Background(), snapshot)
	intent, _, configured, manualResume := detectRuntimeIntentForPipeline(ctx, RunInput{UserMessage: models.Message{ID: 702, MessageType: enums.IMMessageTypeText, Content: "停车免费吗"}}, adapter.HistoryBuildResult{}, detector)
	if !configured || !manualResume || intent.SemanticContractExpected || intent.SourceRefsValidated {
		t.Fatalf("legacy new-source classification must downgrade the combined result instead of claiming V2: %#v", intent)
	}
}
