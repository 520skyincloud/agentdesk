package executor

import (
	"encoding/json"
	"reflect"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestHandoffSubjectsContainOnlyPendingQuestions(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.EvidenceJudge.DeferredTaskIDs = []string{"T2", "T3"}
	collector.Data.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", OriginalText: "咖啡有吗"},
		{TaskID: "T2", OriginalText: "行李能送上来吗"},
		{TaskID: "T3", OriginalText: "机器人有吗能送门口吗", MissingAspects: []string{"是否送到房门口"}},
	}
	data, _ := json.Marshal(collector.Data)
	want := []string{"行李能送上来吗", "机器人有吗能送门口吗（仅待确认：是否送到房门口）"}
	if got := HandoffQuestionSubjectsFromTrace(string(data)); !reflect.DeepEqual(got, want) {
		t.Fatalf("subjects=%v want=%v", got, want)
	}
}

func TestHandoffSubjectsKeepObjectWhenMissingAspectOmitsIt(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.EvidenceJudge.DeferredTaskIDs = []string{"T2", "T3"}
	collector.Data.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "T1", OriginalText: "矿泉水几瓶，收费吗"},
		{TaskID: "T2", OriginalText: "我住1315，出不了门，可以送一条毛巾过来吗？", MissingAspects: []string{"能否送到1315房间"}},
		{TaskID: "T3", OriginalText: "行李能送到1315吗", MissingAspects: []string{"能否送到1315房间"}},
	}
	want := []string{
		"我住1315，出不了门，可以送一条毛巾过来吗？（仅待确认：能否送到1315房间）",
		"行李能送到1315吗（仅待确认：能否送到1315房间）",
	}
	if got := runtimeHandoffQuestionSubjects(collector); !reflect.DeepEqual(got, want) {
		t.Fatalf("different pending objects collapsed or answered task included: %v", got)
	}
}
