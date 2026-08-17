package executor

import (
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

const runtimeObservationDeferredReason = "waiting_bound_observation"

// partitionRuntimePlansByObservationReadiness is the pre-knowledge media gate.
// A Task may query knowledge or enter Generate only after every persisted
// messageId/sourceRevision dependency is ready. This keeps an empty-media query
// from becoming a reusable knowledge checkpoint.
func partitionRuntimePlansByObservationReadiness(
	req RunInput,
	plans []callbacks.ReplyTaskPlanTraceData,
) ([]callbacks.ReplyTaskPlanTraceData, []string, error) {
	ready := make([]callbacks.ReplyTaskPlanTraceData, 0, len(plans))
	deferred := make([]string, 0)
	for _, plan := range plans {
		available, err := runtimeObservationBindingsReady(req, plan.ObservationBindings)
		if err != nil {
			return nil, nil, services.NewAIReplyExecutionError(
				services.AIReplyExecutionErrorResourceInvariantBroken,
				fmt.Errorf("task %s observation dependency: %w", plan.TaskKey, err),
			)
		}
		if !available {
			deferred = appendUniqueStrings(deferred, plan.TaskKey)
			continue
		}
		ready = append(ready, plan)
	}
	return ready, deferred, nil
}

func runtimeObservationBindingsReady(req RunInput, bindings []callbacks.TaskObservationBindingTraceData) (bool, error) {
	if len(bindings) == 0 {
		return true, nil
	}
	if sqls.DB() == nil || req.Conversation.TenantID <= 0 || req.Conversation.ID <= 0 || req.UserMessage.SessionNo <= 0 {
		return false, fmt.Errorf("observation scope is unavailable")
	}
	for _, binding := range bindings {
		if binding.MessageID <= 0 || binding.SourceRevision <= 0 {
			return false, fmt.Errorf("invalid message/revision binding")
		}
		message := repositories.MessageRepository.GetInTenant(sqls.DB(), binding.MessageID, req.Conversation.TenantID)
		if message == nil || message.ConversationID != req.Conversation.ID || message.SessionNo != req.UserMessage.SessionNo ||
			message.SenderType != enums.IMSenderTypeCustomer {
			return false, fmt.Errorf("bound message %d crosses runtime scope", binding.MessageID)
		}
		analysis := repositories.MessageAnalysisRepository.GetByRevisionInTenant(
			sqls.DB(), req.Conversation.TenantID, binding.MessageID, binding.SourceRevision,
		)
		if analysis == nil {
			return false, nil
		}
		switch enums.NormalizeMessageAnalysisStatus(analysis.AnalysisStatus) {
		case enums.MessageAnalysisStatusPending,
			enums.MessageAnalysisStatusProcessing,
			enums.MessageAnalysisStatusFailedRetryable:
			return false, nil
		case enums.MessageAnalysisStatusReady:
			if analysis.SchemaVersion != contracts.MessageAnalysisV2SchemaVersion || strings.TrimSpace(analysis.AnalysisJSON) == "" {
				return false, fmt.Errorf("bound analysis %d/%d is ready without message_analysis.v2 evidence", binding.MessageID, binding.SourceRevision)
			}
			ready, readyErr := services.MessageAnalysisService.ReadyV2ForMessages([]models.Message{*message})
			if readyErr != nil {
				return false, fmt.Errorf("bound analysis %d/%d cannot be decoded: %w", binding.MessageID, binding.SourceRevision, readyErr)
			}
			parsed, ok := ready[binding.MessageID]
			if !ok || parsed.SourceRevision != binding.SourceRevision {
				return false, fmt.Errorf("bound analysis %d/%d no longer matches the authoritative media revision", binding.MessageID, binding.SourceRevision)
			}
		case enums.MessageAnalysisStatusFailedTerminal:
			return false, fmt.Errorf("bound analysis %d/%d failed terminally", binding.MessageID, binding.SourceRevision)
		case enums.MessageAnalysisStatusStale:
			return false, fmt.Errorf("bound analysis %d/%d is stale", binding.MessageID, binding.SourceRevision)
		default:
			return false, nil
		}
	}
	return true, nil
}
