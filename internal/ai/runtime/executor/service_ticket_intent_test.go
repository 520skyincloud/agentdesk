package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/toolx"
)

func TestExplicitTicketIntentRetainsExistingConfirmationTool(t *testing.T) {
	intent := callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{
		{Intent: "service_request", SubIntent: "create_ticket", Text: "1401 空调坏了，创建一个维修工单", NeedsKnowledge: true},
	}}
	got := retainRuntimeTicketTools(intent)
	if !got.NeedsTool || got.NeedsKnowledge || !got.IntentTasks[0].NeedsTool || got.IntentTasks[0].NeedsHumanRoute ||
		!containsString(got.ToolCodes, toolx.GraphCreateTicketConfirm.Code) {
		t.Fatalf("explicit ticket did not bind existing tool: %#v", got)
	}
	if !intent.IntentTasks[0].NeedsKnowledge {
		t.Fatal("input tasks were mutated")
	}
}

func TestTicketIntentDoesNotConvertMaintenanceAdviceOrLoseMixedKnowledge(t *testing.T) {
	advice := callbacks.IntentTaskTraceData{Intent: "service_request", SubIntent: "maintenance", Text: "1401空调不制冷，先告诉我怎么办", NeedsKnowledge: true}
	onlyAdvice := retainRuntimeTicketTools(callbacks.IntentTraceData{NeedsKnowledge: true, IntentTasks: []callbacks.IntentTaskTraceData{advice}})
	if onlyAdvice.NeedsTool || len(onlyAdvice.ToolCodes) != 0 || !onlyAdvice.NeedsKnowledge {
		t.Fatalf("ordinary advice was converted to a write operation: %#v", onlyAdvice)
	}
	mixed := retainRuntimeTicketTools(callbacks.IntentTraceData{IntentTasks: []callbacks.IntentTaskTraceData{
		{Intent: "service_request", SubIntent: "create_ticket", Text: "创建维修工单", NeedsKnowledge: true},
		{Intent: "hotel_info", SubIntent: "parking", Text: "停车怎么走", NeedsKnowledge: true},
	}})
	if !mixed.NeedsKnowledge || !mixed.NeedsTool || mixed.IntentTasks[0].NeedsKnowledge || !mixed.IntentTasks[1].NeedsKnowledge {
		t.Fatalf("ticket and knowledge tasks did not retain separate contracts: %#v", mixed)
	}
}
