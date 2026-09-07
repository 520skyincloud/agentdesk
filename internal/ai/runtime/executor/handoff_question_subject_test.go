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
		{TaskID: "T2", OriginalText: "能换纸币吗"},
		{TaskID: "T3", OriginalText: "机器人有吗能送门口吗", MissingAspects: []string{"是否送到房门口"}},
	}
	data, _ := json.Marshal(collector.Data)
	want := []string{"能换纸币吗", "是否送到房门口"}
	if got := HandoffQuestionSubjectsFromTrace(string(data)); !reflect.DeepEqual(got, want) {
		t.Fatalf("subjects=%v want=%v", got, want)
	}
}
