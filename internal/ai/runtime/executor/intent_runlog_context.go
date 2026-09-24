package executor

import (
	"encoding/json"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

const runtimeRecentRunLogContextLimit = 20

type runtimeRecentRunTrace struct {
	Log     models.AgentRunLog
	Message models.Message
	Runtime callbacks.RuntimeTraceData
}

type runtimeRecentBusinessTask struct {
	RunLogID int64
	Task     callbacks.ReplyTaskPlanTraceData
}

func runtimeRecentRunTraces(req RunInput) []runtimeRecentRunTrace {
	if req.Conversation.ID <= 0 || sqls.DB() == nil {
		return nil
	}
	cnd := sqls.NewCnd().Eq("conversation_id", req.Conversation.ID).Desc("id").Limit(runtimeRecentRunLogContextLimit)
	if req.UserMessage.ID > 0 {
		cnd.Lt("message_id", req.UserMessage.ID)
	}
	logs := services.AgentRunLogService.Find(cnd)
	ret := make([]runtimeRecentRunTrace, 0, len(logs))
	for _, log := range logs {
		if strings.TrimSpace(log.TraceData) == "" || (req.AIAgent.ID > 0 && log.AIAgentID != req.AIAgent.ID) {
			continue
		}
		source := services.MessageService.Get(log.MessageID)
		if source == nil || source.ConversationID != req.Conversation.ID {
			continue
		}
		if req.UserMessage.SessionNo > 0 && source.SessionNo != req.UserMessage.SessionNo {
			continue
		}
		trace, ok := runtimeTraceFromRunLog(log.TraceData)
		if !ok {
			continue
		}
		ret = append(ret, runtimeRecentRunTrace{Log: log, Message: *source, Runtime: trace})
	}
	return ret
}

func runtimeTraceFromRunLog(raw string) (callbacks.RuntimeTraceData, bool) {
	var wrapper struct {
		Runtime json.RawMessage `json:"runtime"`
	}
	if json.Unmarshal([]byte(raw), &wrapper) != nil {
		return callbacks.RuntimeTraceData{}, false
	}
	data := wrapper.Runtime
	if len(data) == 0 || string(data) == "null" {
		data = json.RawMessage(raw)
	}
	var trace callbacks.RuntimeTraceData
	if json.Unmarshal(data, &trace) != nil {
		return callbacks.RuntimeTraceData{}, false
	}
	return trace, true
}

func runtimeRecentUniqueBusinessTaskForRequest(req RunInput) *runtimeRecentBusinessTask {
	for _, recent := range runtimeRecentRunTraces(req) {
		candidates := runtimeTraceBusinessTasks(recent.Runtime)
		switch len(candidates) {
		case 0:
			if runtimeTraceBlocksEarlierBusinessContext(recent.Runtime) {
				return nil
			}
			continue
		case 1:
			return &runtimeRecentBusinessTask{RunLogID: recent.Log.ID, Task: candidates[0]}
		default:
			// The latest business turn is multi-topic, so an omitted reference is
			// not safe to bind to an older, unrelated single task.
			return nil
		}
	}
	return nil
}

func runtimeTraceBlocksEarlierBusinessContext(trace callbacks.RuntimeTraceData) bool {
	tasks := trace.Pipeline.Intent.IntentTasks
	if len(tasks) == 0 {
		return false
	}
	for _, task := range tasks {
		if canonicalIntentCode(task.Intent) != "interaction" {
			return false
		}
		if strings.TrimSpace(task.RelationToPrevious) != "independent" ||
			strings.TrimSpace(task.ResolutionState) == runtimeIntentResolutionResolvedFromContext {
			return false
		}
		switch strings.TrimSpace(task.SubIntent) {
		case "frustration", "answer_rejected":
			return false
		}
	}
	return true
}

func runtimeTraceBusinessTasks(trace callbacks.RuntimeTraceData) []callbacks.ReplyTaskPlanTraceData {
	ret := make([]callbacks.ReplyTaskPlanTraceData, 0, len(trace.Pipeline.ReplyPlan.TaskPlans))
	seen := make(map[string]struct{}, len(trace.Pipeline.ReplyPlan.TaskPlans))
	for _, task := range trace.Pipeline.ReplyPlan.TaskPlans {
		if !runtimeReplyTaskIsBusinessContext(task) {
			continue
		}
		key := strings.TrimSpace(task.TaskID)
		if key == "" {
			key = strings.Join([]string{
				strings.TrimSpace(task.Intent), strings.TrimSpace(task.SubIntent),
				strings.TrimSpace(firstNonEmptyReplyTaskText(task.ResolvedText, task.Text, task.OriginalText)),
			}, "\x00")
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		ret = append(ret, task)
	}
	return ret
}

func runtimeReplyTaskIsBusinessContext(task callbacks.ReplyTaskPlanTraceData) bool {
	if strings.TrimSpace(firstNonEmptyReplyTaskText(task.ResolvedText, task.Text, task.OriginalText)) == "" {
		return false
	}
	if strings.TrimSpace(task.OutputKind) == "context_only" || canonicalIntentCode(task.Intent) == "interaction" {
		return false
	}
	if task.NeedsHumanRoute || strings.TrimSpace(task.OutputKind) == "handoff" {
		return false
	}
	return strings.TrimSpace(task.Intent) != ""
}
