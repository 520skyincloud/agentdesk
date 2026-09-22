package executor

import (
	"encoding/json"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

func restoreRuntimeRejectedTask(req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData) (callbacks.IntentTraceData, bool) {
	if !runtimeIntentHasDetachedCorrection(intent) || req.Conversation.ID <= 0 || sqls.DB() == nil {
		return intent, false
	}
	history = adapter.ExcludeCurrentTurnSources(history, req.UserMessage)
	if len(history.RawItems) == 0 || history.RawItems[len(history.RawItems)-1].SenderType != enums.IMSenderTypeAI {
		return intent, false
	}
	var messageID int64
	for index := len(history.RawItems) - 2; index >= 0; index-- {
		item := history.RawItems[index]
		if item.SenderType == enums.IMSenderTypeCustomer {
			messageID = item.ID
			break
		}
		if item.SenderType != enums.IMSenderTypeAI {
			return intent, false
		}
	}
	if messageID <= 0 {
		return intent, false
	}
	log := services.AgentRunLogService.FindOne(sqls.NewCnd().
		Eq("conversation_id", req.Conversation.ID).Eq("message_id", messageID).Desc("id"))
	if log == nil || (req.AIAgent.ID > 0 && log.AIAgentID != req.AIAgent.ID) {
		return intent, false
	}
	var projection struct {
		Runtime callbacks.RuntimeTraceData `json:"runtime"`
	}
	if json.Unmarshal([]byte(log.TraceData), &projection) != nil {
		return intent, false
	}
	return rebindRuntimeRejectedTask(intent, projection.Runtime)
}

func runtimeIntentHasDetachedCorrection(intent callbacks.IntentTraceData) bool {
	for _, task := range intent.IntentTasks {
		if runtimeDetachedCorrectionTask(task) {
			return true
		}
	}
	return false
}

func runtimeDetachedCorrectionTask(task callbacks.IntentTaskTraceData) bool {
	if task.Intent != "interaction" || task.NeedsResource || task.NeedsTool || task.NeedsHumanRoute {
		return false
	}
	relation := semanticGateNormalizeRelation(task.RelationToPrevious)
	return relation == "answer_rejected" || relation == "correction"
}

func rebindRuntimeRejectedTask(intent callbacks.IntentTraceData, previous callbacks.RuntimeTraceData) (callbacks.IntentTraceData, bool) {
	failed := make(map[string]bool)
	for _, task := range previous.Pipeline.EvidenceJudge.Tasks {
		switch task.Decision {
		case knowledgeEvidenceDecisionInsufficient, knowledgeEvidenceDecisionMalformed, knowledgeEvidenceDecisionProtocolInvalid, knowledgeEvidenceDecisionTimeout:
			failed[task.TaskID] = true
		}
	}
	explicitFailedCandidates := make([]callbacks.ReplyTaskPlanTraceData, 0)
	taskSpecificFailedCandidates := make([]callbacks.ReplyTaskPlanTraceData, 0)
	uncommittedTextCandidates := make([]callbacks.ReplyTaskPlanTraceData, 0)
	for _, task := range previous.Pipeline.ReplyPlan.TaskPlans {
		if task.OutputKind == "context_only" || task.Intent == "interaction" || task.NeedsResource || task.NeedsHumanRoute ||
			task.SubIntent == "create_ticket" || (task.NeedsTool && !isPMSRuntimeSubIntent(task.SubIntent)) {
			continue
		}
		if runtimePreviousReplyTaskCommitted(task.TaskID, previous) {
			continue
		}
		uncommittedTextCandidates = append(uncommittedTextCandidates, task)
		if failed[task.TaskID] {
			explicitFailedCandidates = append(explicitFailedCandidates, task)
		} else if runtimePreviousReplyTaskSpecificIncomplete(task) {
			taskSpecificFailedCandidates = append(taskSpecificFailedCandidates, task)
		}
	}
	failedCandidates := taskSpecificFailedCandidates
	if len(explicitFailedCandidates) > 0 {
		failedCandidates = explicitFailedCandidates
	} else if len(taskSpecificFailedCandidates) == 0 &&
		runtimePreviousReplyHasGlobalTextFailure(previous) && len(uncommittedTextCandidates) == 1 {
		failedCandidates = uncommittedTextCandidates
	}
	if len(failedCandidates) != 1 {
		return intent, false
	}
	target := failedCandidates[0]
	targetText := firstNonEmptyReplyTaskText(target.ResolvedText, target.Text, target.OriginalText)
	if strings.TrimSpace(targetText) == "" {
		return intent, false
	}
	changed := false
	intent.IntentTasks = append([]callbacks.IntentTaskTraceData(nil), intent.IntentTasks...)
	for index := range intent.IntentTasks {
		task := &intent.IntentTasks[index]
		if !runtimeDetachedCorrectionTask(*task) {
			continue
		}
		task.Intent, task.SubIntent, task.Objective = target.Intent, target.SubIntent, target.Objective
		task.NeedsKnowledge, task.NeedsTool = target.NeedsKnowledge, target.NeedsTool
		task.NeedsHumanRoute, task.NeedsResource, task.ResourceAction = false, false, ""
		task.ResolutionState = runtimeIntentResolutionResolvedFromContext
		task.RelationToPrevious = "correction"
		task.ResolvedText = targetText + "\n当前客户纠正（重新回答原问题，不切换主题）：" + task.Text
		task.Reason = appendIntentReason(task.Reason, "rebound to the unique previous reply task "+target.TaskID)
		changed = true
	}
	if !changed {
		return intent, false
	}
	return deriveModelIntentFromTasks(intent), true
}

func runtimePreviousReplyTaskSpecificIncomplete(task callbacks.ReplyTaskPlanTraceData) bool {
	if isPMSRuntimeSubIntent(task.SubIntent) {
		if !runtimeReplyTaskHasPMSFact(task) || len(task.MissingAspects) > 0 {
			return true
		}
	}
	if isUngroundedKnowledgeReplyTask(task) {
		return true
	}
	return false
}

func runtimePreviousReplyHasGlobalTextFailure(previous callbacks.RuntimeTraceData) bool {
	stage := strings.TrimSpace(previous.Error.Stage)
	if stage != "" && stage != "generate" && stage != "validate" && stage != "question_coverage" {
		return false
	}
	if strings.TrimSpace(previous.Pipeline.Generate.Status) == "failed" ||
		strings.TrimSpace(previous.Pipeline.Validate.Status) == "failed" {
		return true
	}
	switch stage {
	case "generate", "validate", "question_coverage":
		return true
	}
	switch strings.TrimSpace(previous.Output.FinishReason) {
	case "generated_reply_protocol_error", "question_coverage":
		return true
	}
	return false
}

func runtimePreviousReplyTaskCommitted(taskID string, previous callbacks.RuntimeTraceData) bool {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false
	}
	for _, message := range previous.Output.CommitMessages {
		if strings.TrimSpace(message.Status) != "sent" {
			continue
		}
		for _, committedTaskID := range message.TaskIDs {
			if strings.TrimSpace(committedTaskID) == taskID {
				return true
			}
		}
	}
	return false
}
