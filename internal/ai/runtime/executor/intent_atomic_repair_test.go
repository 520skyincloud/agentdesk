package executor

import (
	"reflect"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestRepairRuntimeIntentAtomicKnowledgeTasksSplitsOneCompoundTask(t *testing.T) {
	text := "再一起问四个：你们有没有外卖机器人，外卖地址应该怎么填，布草是不是一客一换，携程抖音美团的价格是不是一样？"
	tasks := []callbacks.IntentTaskTraceData{{
		Intent: "hotel_info", SubIntent: "compound_information", Objective: "compound_information",
		RelationToPrevious: "independent", ResolutionState: "clear", Text: text, ResolvedText: text,
		SourceRefs: []string{"U1"}, NeedsKnowledge: true,
	}}

	got, repaired := repairRuntimeIntentAtomicKnowledgeTasks(tasks, text, []string{text}, true)
	if repaired != 4 || len(got) != 4 {
		t.Fatalf("expected four atomic tasks, repaired=%d tasks=%#v", repaired, got)
	}
	wantText := []string{"你们有没有外卖机器人", "外卖地址应该怎么填", "布草是不是一客一换", "携程抖音美团的价格是不是一样"}
	wantObjectives := []string{"availability", "location", "policy", "price"}
	for index := range got {
		if got[index].Text != wantText[index] || got[index].ResolvedText != wantText[index] || got[index].Objective != wantObjectives[index] {
			t.Fatalf("unexpected atomic task %d: %#v", index, got[index])
		}
		if !reflect.DeepEqual(got[index].SourceRefs, []string{"U1"}) {
			t.Fatalf("source refs changed for task %d: %#v", index, got[index].SourceRefs)
		}
	}
	if got[1].SubIntent != "location_info" {
		t.Fatalf("location atom must keep spatial-fact disclosure active, got %#v", got[1])
	}
}

func TestRepairRuntimeIntentAtomicKnowledgeTasksSplitsOnlyMergedTask(t *testing.T) {
	text := "再一起问四个：你们有没有外卖机器人，外卖地址应该怎么填，布草是不是一客一换，携程抖音美团的价格是不是一样？"
	tasks := []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", SubIntent: "compound_information", Objective: "compound_information", RelationToPrevious: "independent", ResolutionState: "clear", Text: "再一起问四个：你们有没有外卖机器人，外卖地址应该怎么填", ResolvedText: "再一起问四个：你们有没有外卖机器人，外卖地址应该怎么填", SourceRefs: []string{"U1"}, NeedsKnowledge: true},
		{Intent: "hotel_info", SubIntent: "linen_policy", Objective: "policy", RelationToPrevious: "independent", ResolutionState: "clear", Text: "布草是不是一客一换", ResolvedText: "布草是不是一客一换", SourceRefs: []string{"U1"}, NeedsKnowledge: true},
		{Intent: "hotel_info", SubIntent: "platform_price", Objective: "price", RelationToPrevious: "independent", ResolutionState: "clear", Text: "携程抖音美团的价格是不是一样", ResolvedText: "携程抖音美团的价格是不是一样", SourceRefs: []string{"U1"}, NeedsKnowledge: true},
	}

	got, repaired := repairRuntimeIntentAtomicKnowledgeTasks(tasks, text, []string{text}, true)
	if repaired != 2 || len(got) != 4 {
		t.Fatalf("expected only the merged pair to split, repaired=%d tasks=%#v", repaired, got)
	}
	for index, want := range []string{"你们有没有外卖机器人", "外卖地址应该怎么填", "布草是不是一客一换", "携程抖音美团的价格是不是一样"} {
		if got[index].Text != want {
			t.Fatalf("unexpected task order/text at %d: %#v", index, got)
		}
	}
}

func TestRepairRuntimeIntentAtomicKnowledgeTasksSplitsSafeMethodTask(t *testing.T) {
	text := "入住方式和开门方式分别说，不要混在一起。"
	task := callbacks.IntentTaskTraceData{
		Intent: "hotel_info", SubIntent: "checkin_process", Objective: "method",
		RelationToPrevious: "independent", ResolutionState: "clear",
		Text: text, ResolvedText: text, SourceRefs: []string{"U1"}, NeedsKnowledge: true,
	}
	got, repaired := repairRuntimeIntentAtomicKnowledgeTasks([]callbacks.IntentTaskTraceData{task}, text, []string{text}, true)
	if repaired != 2 || len(got) != 2 {
		t.Fatalf("expected safe non-compound knowledge task to split, repaired=%d tasks=%#v", repaired, got)
	}
	for index, want := range []string{"入住方式", "开门方式"} {
		if got[index].Text != want || got[index].ResolvedText != want || got[index].Objective != "method" {
			t.Fatalf("unexpected repaired method task %d: %#v", index, got[index])
		}
	}
}

func TestRepairRuntimeIntentAtomicKnowledgeTasksDoesNotSplitActionsOrSingleAspect(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
		task callbacks.IntentTaskTraceData
	}{
		{name: "action", text: "帮我送浴巾，顺便打扫一下", task: callbacks.IntentTaskTraceData{Intent: "service_request", SubIntent: "compound", Objective: "action_request", Text: "帮我送浴巾，顺便打扫一下", ResolvedText: "帮我送浴巾，顺便打扫一下", NeedsKnowledge: true, NeedsHumanRoute: true}},
		{name: "single aspect", text: "房间有几瓶矿泉水，免费吗？", task: callbacks.IntentTaskTraceData{Intent: "hotel_info", SubIntent: "drinking_water", Objective: "compound_information", Text: "房间有几瓶矿泉水，免费吗？", ResolvedText: "房间有几瓶矿泉水，免费吗？", NeedsKnowledge: true}},
		{name: "implicit same object aspects", text: "房间里有几瓶矿泉水，都是免费的吗？", task: callbacks.IntentTaskTraceData{Intent: "hotel_info", SubIntent: "drinking_water", Objective: "compound_information", Text: "房间里有几瓶矿泉水，都是免费的吗？", ResolvedText: "房间里有几瓶矿泉水，都是免费的吗？", Entities: []callbacks.IntentEntityTraceData{{Text: "矿泉水", Type: "supply"}}, NeedsKnowledge: true}},
		{name: "repeated same object aspects", text: "房间有几瓶矿泉水，另外矿泉水收费吗？", task: callbacks.IntentTaskTraceData{Intent: "hotel_info", SubIntent: "drinking_water", Objective: "compound_information", Text: "房间有几瓶矿泉水，另外矿泉水收费吗？", ResolvedText: "房间有几瓶矿泉水，另外矿泉水收费吗？", Entities: []callbacks.IntentEntityTraceData{{Text: "矿泉水", Type: "supply"}}, NeedsKnowledge: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, repaired := repairRuntimeIntentAtomicKnowledgeTasks([]callbacks.IntentTaskTraceData{tt.task}, tt.text, []string{tt.text}, true)
			if repaired != 0 || len(got) != 1 || got[0].Text != tt.task.Text {
				t.Fatalf("task must remain unchanged, repaired=%d tasks=%#v", repaired, got)
			}
		})
	}
}

func TestRepairRuntimeIntentAtomicKnowledgeTasksKeepsThreeVoiceQuestions(t *testing.T) {
	text := "麻烦分别告诉我，房间空调有没有，矿泉水配几瓶收不收费，入住要怎么操作。"
	task := callbacks.IntentTaskTraceData{
		Intent: "hotel_info", SubIntent: "compound_information", Objective: "compound_information",
		RelationToPrevious: "independent", ResolutionState: "clear", Text: text, ResolvedText: text,
		SourceRefs: []string{"U1"}, NeedsKnowledge: true,
		Entities: []callbacks.IntentEntityTraceData{
			{Text: "空调", Type: "facility"},
			{Text: "矿泉水", Type: "supply"},
			{Text: "入住", Type: "service"},
		},
	}

	got, repaired := repairRuntimeIntentAtomicKnowledgeTasks([]callbacks.IntentTaskTraceData{task}, text, []string{text}, true)
	if repaired != 3 || len(got) != 3 {
		t.Fatalf("expected three repaired voice tasks, repaired=%d tasks=%#v", repaired, got)
	}
	wantTexts := []string{"房间空调有没有", "矿泉水配几瓶收不收费", "入住要怎么操作"}
	wantObjectives := []string{"availability", "compound_information", "method"}
	for index := range got {
		if got[index].Text != wantTexts[index] || got[index].ResolvedText != wantTexts[index] || got[index].Objective != wantObjectives[index] {
			t.Fatalf("unexpected voice task %d: %#v", index, got[index])
		}
		if !reflect.DeepEqual(got[index].SourceRefs, []string{"U1"}) {
			t.Fatalf("voice task %d sourceRefs changed: %#v", index, got[index].SourceRefs)
		}
	}
}

func TestRepairRuntimeIntentAtomicKnowledgeTasksPreservesContextResolvedTask(t *testing.T) {
	text := "那麦田有办公桌吗，沙发呢？"
	task := callbacks.IntentTaskTraceData{
		Intent: "hotel_info", SubIntent: "compound_information", Objective: "compound_information",
		RelationToPrevious: "reference_previous", ResolutionState: "resolved_from_context",
		Text: text, ResolvedText: "麦田房型有没有办公桌和沙发", SourceRefs: []string{"U1"}, NeedsKnowledge: true,
	}

	got, repaired := repairRuntimeIntentAtomicKnowledgeTasks([]callbacks.IntentTaskTraceData{task}, text, []string{text}, true)
	if repaired != 0 || len(got) != 1 || got[0].ResolvedText != task.ResolvedText {
		t.Fatalf("context-enriched compound task must not be narrowed locally, repaired=%d tasks=%#v", repaired, got)
	}
}

func TestRepairRuntimeIntentAtomicKnowledgeTasksKeepsIndependentSourceRefs(t *testing.T) {
	text := "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 早餐几点\n2. [消息] 停车免费吗"
	task := callbacks.IntentTaskTraceData{
		Intent: "hotel_info", SubIntent: "compound_information", Objective: "compound_information",
		RelationToPrevious: "independent", ResolutionState: "clear", Text: "早餐几点，停车免费吗", ResolvedText: "早餐几点，停车免费吗",
		SourceRefs: []string{"U1", "U2"}, NeedsKnowledge: true,
	}

	got, repaired := repairRuntimeIntentAtomicKnowledgeTasks([]callbacks.IntentTaskTraceData{task}, text, []string{"早餐几点", "停车免费吗"}, true)
	if repaired != 2 || len(got) != 2 {
		t.Fatalf("expected two source-bound tasks, repaired=%d tasks=%#v", repaired, got)
	}
	if !reflect.DeepEqual(got[0].SourceRefs, []string{"U1"}) || !reflect.DeepEqual(got[1].SourceRefs, []string{"U2"}) {
		t.Fatalf("independent source refs must not cover each other, got %#v", got)
	}
}
