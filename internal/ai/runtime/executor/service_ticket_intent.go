package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/toolx"
)

func runtimeIntentHasTicketTask(intent callbacks.IntentTraceData) bool {
	for _, task := range intent.IntentTasks {
		if task.Intent == "service_request" && strings.TrimSpace(task.SubIntent) == "create_ticket" {
			return true
		}
	}
	return false
}

func retainRuntimeTicketTools(intent callbacks.IntentTraceData) callbacks.IntentTraceData {
	if !runtimeIntentHasTicketTask(intent) {
		return intent
	}
	intent.IntentTasks = append([]callbacks.IntentTaskTraceData(nil), intent.IntentTasks...)
	intent.NeedsKnowledge = false
	for index := range intent.IntentTasks {
		task := &intent.IntentTasks[index]
		if task.Intent == "service_request" && strings.TrimSpace(task.SubIntent) == "create_ticket" {
			task.NeedsKnowledge = false
			task.NeedsTool = true
			task.NeedsHumanRoute = false
		}
		intent.NeedsKnowledge = intent.NeedsKnowledge || task.NeedsKnowledge
	}
	intent.NeedsTool = true
	intent.ToolCodes = appendIfMissing(intent.ToolCodes, toolx.GraphPrepareTicketDraft.Code)
	intent.ToolCodes = appendIfMissing(intent.ToolCodes, toolx.GraphCreateTicketConfirm.Code)
	return intent
}
