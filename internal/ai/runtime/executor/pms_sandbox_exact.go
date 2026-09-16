package executor

import (
	"context"
	"errors"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/pms/sandbox"
	"agent-desk/internal/services"
)

func tryCompleteSandboxExactDemoReply(ctx context.Context, req RunInput, summary *RunResult, collector *callbacks.RuntimeTraceCollector) (bool, error) {
	if !config.PMSSandboxEnabled() {
		return false, nil
	}
	switch req.UserMessage.MessageType {
	case enums.IMMessageTypeText, enums.IMMessageTypeVoice:
	default:
		return false, nil
	}
	sources := adapter.BuildCurrentTurnSources(req.UserMessage)
	if len(sources) == 0 {
		return false, nil
	}
	if utils.IsRuntimeCustomerBurstEnvelope(req.UserMessage.Content) &&
		len(sources) != len(utils.RuntimeCustomerBurstItems(req.UserMessage.Content)) {
		return false, nil
	}
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.MessageType != enums.IMMessageTypeText && source.MessageType != enums.IMMessageTypeVoice {
			return false, nil
		}
		parts = append(parts, source.Text)
	}
	text := strings.Join(parts, "\n")
	scene, reply := sandbox.MatchDemoQuestion(text)
	if scene == "" {
		return false, nil
	}
	setCurrentTurnSourcesTrace(collector, req.UserMessage)
	refs := make([]string, 0, len(collector.Data.Input.CurrentTurnSources))
	for _, source := range collector.Data.Input.CurrentTurnSources {
		refs = append(refs, source.Ref)
	}
	intent := "hotel_info"
	if scene == "B" || scene == "C" || scene == "D" {
		intent = "service_request"
	}
	task := callbacks.ReplyTaskPlanTraceData{
		TaskID: "T1", Intent: intent, SandboxScene: scene,
		Text: text, OriginalText: text, ResolvedText: text, SourceRefs: refs,
		ResolutionState: "clear",
	}
	setSandboxFixedReply(&task, reply)
	collector.Data.Input.CurrentUserMessagePreview = preview(text, 120)
	collector.Data.Pipeline.Normalize = callbacks.NormalizeTraceData{
		CurrentUserText: preview(text, 240), CurrentMessageType: string(req.UserMessage.MessageType),
	}
	collector.Data.Pipeline.Intent = callbacks.IntentTraceData{
		DetectedIntent: intent, PrimaryIntent: intent, ShouldReply: true,
		MatchMode: "sandbox_exact_question", Reason: "approved demo question matched locally; Intent model skipped",
		IntentTasks: []callbacks.IntentTaskTraceData{{
			Intent: intent, SandboxScene: scene, Text: text, ResolvedText: text,
			SourceRefs: refs, ResolutionState: "clear",
		}},
	}
	collector.Data.Pipeline.ReplyPlan = callbacks.ReplyPlanTraceData{
		Intent: intent, ActiveTaskCount: 1, ReplyRequiredTaskCount: 1,
		TaskPlans: []callbacks.ReplyTaskPlanTraceData{task},
	}
	if scene == "F" {
		scope, err := services.PMSSandboxScopeForConversation(req.Conversation.ID, req.UserMessage.ID)
		if err != nil {
			return exactDemoFailure(summary, collector, err)
		}
		result, err := services.PMSSandboxService.ExecuteScene(ctx, scope, sandbox.SceneInput{Scene: "F"})
		if err != nil {
			return exactDemoFailure(summary, collector, err)
		}
		if result == nil || result.Resource == nil || result.State == nil ||
			result.State.Dataset.ID <= 0 || result.State.Dataset.StoreID != scope.StoreID ||
			result.Resource.ID <= 0 || result.Resource.SourceMessageID <= 0 ||
			strings.TrimSpace(result.Resource.CardPayload) == "" {
			return exactDemoFailure(summary, collector, errors.New("bound pillow card is unavailable"))
		}
		collector.Data.SandboxResources = []callbacks.SandboxResourceTraceData{{
			TaskID: task.TaskID, StoreID: scope.StoreID,
			DatasetID: result.State.Dataset.ID, ResourceID: result.Resource.ID,
		}}
	}
	if !completeSandboxFixedReply(summary, collector) || summary.ReplyText != reply {
		return exactDemoFailure(summary, collector, errors.New("fixed demo reply did not match approved text"))
	}
	collector.Data.Output.FinishReason = "sandbox_exact_question"
	collector.Data.Pipeline.Generate.Reason = "approved reply copied verbatim; no Intent, Judge or Generate model call"
	summary.TraceData = collector.Marshal()
	return true, nil
}

func exactDemoFailure(summary *RunResult, collector *callbacks.RuntimeTraceCollector, err error) (bool, error) {
	summary.Status, summary.ReplyText, summary.ErrorMessage = "error", "", err.Error()
	collector.Data.Status = "error"
	collector.Data.Error.Stage, collector.Data.Error.Message = "sandbox_exact_question", err.Error()
	collector.Data.Pipeline.Validate.Status, collector.Data.Pipeline.Validate.Reason = "failed", err.Error()
	collector.Data.Output.ReplyText = ""
	summary.TraceData = collector.Marshal()
	return true, err
}
