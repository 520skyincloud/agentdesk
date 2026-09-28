package executor

import (
	"reflect"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestRuntimeBusinessContextFailedValidationKeepsGoalNotShownFacts(t *testing.T) {
	trace := runtimeFailedReplyBusinessContextFixture()
	trace.Pipeline.Validate.Status = "failed"
	tasks := runtimeTraceBusinessTasks(trace)
	if len(tasks) != 1 {
		t.Fatalf("failed reply lost the unfinished customer goal: %#v", tasks)
	}
	task := tasks[0]
	if task.SubIntent != "room_change" || task.SubjectScope != runtimeSubjectCurrentStay ||
		!reflect.DeepEqual(task.RequestedAspects, []string{"price_difference"}) ||
		runtimeIntentEntityValue(task.Entities, runtimeIntentEntityCustomerPhone) != "13800138000" {
		t.Fatalf("failed reply lost the goal or customer locator: %#v", task)
	}
	if len(task.SupportedFacts) != 0 || task.SelectionSource != "" || task.SelectionRef != "" ||
		len(task.SelectedCandidateIDs) != 0 || task.AnswerText != nil {
		t.Fatalf("sent failure notice was treated as delivered business evidence: %#v", task)
	}
	if len(trace.Pipeline.ReplyPlan.TaskPlans[0].SupportedFacts) != 1 {
		t.Fatal("context projection changed the original audit trace")
	}
}

func TestRuntimeBusinessContextFallbackDoesNotConfirmFactsWithSentTaskIDs(t *testing.T) {
	trace := runtimeFailedReplyBusinessContextFixture()
	trace.Pipeline.Generate.FallbackMode = "supported_facts"
	tasks := runtimeTraceBusinessTasks(trace)
	if len(tasks) != 1 || len(tasks[0].SupportedFacts) != 0 || tasks[0].SelectionSource != "" {
		t.Fatalf("legacy fallback without validate status leaked undelivered facts: %#v", tasks)
	}
	trace.Pipeline.Generate.FallbackMode = ""
	trace.Pipeline.Validate.Status = "passed"
	tasks = runtimeTraceBusinessTasks(trace)
	if len(tasks) != 1 || len(tasks[0].SupportedFacts) != 1 || tasks[0].SelectionSource != "customer" {
		t.Fatalf("validated and delivered business context was discarded: %#v", tasks)
	}
}

func runtimeFailedReplyBusinessContextFixture() callbacks.RuntimeTraceData {
	answer := "沐阳有房，房价268元。"
	trace := callbacks.RuntimeTraceData{Status: "completed"}
	trace.Pipeline.ReplyPlan.TaskPlans = []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "room", Intent: "hotel_info", SubIntent: "room_change",
		OriginalText: "我想看看沐阳，需要补多少", Text: "我想看看沐阳，需要补多少",
		SubjectScope: runtimeSubjectCurrentStay, RequestedAspects: []string{"price_difference"},
		SelectionSource: "customer", SelectionRef: "U1",
		Entities: []callbacks.IntentEntityTraceData{
			{Type: runtimeIntentEntityCustomerPhone, Text: "13800138000"},
			{Type: runtimeIntentEntityOrderLocator, Text: "接待单ID:REC-1"},
		},
		SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
			{FactID: "F1", Aspect: "pms_room_inventory", Statement: answer},
		},
		SelectedCandidateIDs: []string{"C1"}, SelectedLayer: "store", AnswerText: &answer,
	}}
	trace.Output.CommitMessages = []callbacks.CommitMessageTraceData{{
		Status: "sent", TaskIDs: []string{"room"}, Content: "您想换的房间和费用，这次暂时还不能给您准确答复，抱歉。",
	}}
	return trace
}
