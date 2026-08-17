package enums

import "strings"

type AIReplyTurnTaskType string

const (
	AIReplyTurnTaskTypeText      AIReplyTurnTaskType = "text"
	AIReplyTurnTaskTypeKnowledge AIReplyTurnTaskType = "knowledge"
	AIReplyTurnTaskTypeResource  AIReplyTurnTaskType = "resource"
	AIReplyTurnTaskTypeHuman     AIReplyTurnTaskType = "human"
)

type AIReplyTurnTaskStage string

const (
	AIReplyTurnTaskStageIntent     AIReplyTurnTaskStage = "intent"
	AIReplyTurnTaskStageCapability AIReplyTurnTaskStage = "capability"
	AIReplyTurnTaskStageKnowledge  AIReplyTurnTaskStage = "knowledge"
	AIReplyTurnTaskStageGenerate   AIReplyTurnTaskStage = "generate"
	AIReplyTurnTaskStageCommit     AIReplyTurnTaskStage = "commit"
	AIReplyTurnTaskStageDelivery   AIReplyTurnTaskStage = "delivery"
	AIReplyTurnTaskStageHandoff    AIReplyTurnTaskStage = "handoff"
	AIReplyTurnTaskStageComplete   AIReplyTurnTaskStage = "complete"
)

type AIReplyTurnTaskStatus string

const (
	AIReplyTurnTaskStatusPending AIReplyTurnTaskStatus = "pending"
	AIReplyTurnTaskStatusRunning AIReplyTurnTaskStatus = "running"
	AIReplyTurnTaskStatusReady   AIReplyTurnTaskStatus = "ready"
	// WaitingCoverage means this task is an exact duplicate of a canonical
	// task that has not produced terminal evidence yet. It is intentionally
	// unfinished but not runnable: the canonical task transition resolves or
	// releases it in the same transaction.
	AIReplyTurnTaskStatusWaitingCoverage AIReplyTurnTaskStatus = "waiting_coverage"
	AIReplyTurnTaskStatusFailed          AIReplyTurnTaskStatus = "failed"
	AIReplyTurnTaskStatusCommitted       AIReplyTurnTaskStatus = "committed"
	AIReplyTurnTaskStatusDelivered       AIReplyTurnTaskStatus = "delivered"
	AIReplyTurnTaskStatusCovered         AIReplyTurnTaskStatus = "covered"
	AIReplyTurnTaskStatusHandoffPending  AIReplyTurnTaskStatus = "handoff_pending"
	AIReplyTurnTaskStatusHandoff         AIReplyTurnTaskStatus = "handoff"
	AIReplyTurnTaskStatusSkipped         AIReplyTurnTaskStatus = "skipped"
	AIReplyTurnTaskStatusSuperseded      AIReplyTurnTaskStatus = "superseded"
)

type AIReplyTurnTaskKnowledgeStatus string

const (
	AIReplyTurnTaskKnowledgeStatusNone      AIReplyTurnTaskKnowledgeStatus = "none"
	AIReplyTurnTaskKnowledgeStatusPending   AIReplyTurnTaskKnowledgeStatus = "pending"
	AIReplyTurnTaskKnowledgeStatusHit       AIReplyTurnTaskKnowledgeStatus = "hit"
	AIReplyTurnTaskKnowledgeStatusNoHit     AIReplyTurnTaskKnowledgeStatus = "no_hit"
	AIReplyTurnTaskKnowledgeStatusNoContext AIReplyTurnTaskKnowledgeStatus = "no_context"
	AIReplyTurnTaskKnowledgeStatusFailed    AIReplyTurnTaskKnowledgeStatus = "failed"
)

// AIReplyTurnTaskExecutionOutcome describes whether the task execution itself
// completed. It must not be used as proof that a customer-facing result was
// delivered or that the business question was answered.
type AIReplyTurnTaskExecutionOutcome string

const (
	AIReplyTurnTaskExecutionOutcomePending    AIReplyTurnTaskExecutionOutcome = "pending"
	AIReplyTurnTaskExecutionOutcomeCommitted  AIReplyTurnTaskExecutionOutcome = "committed"
	AIReplyTurnTaskExecutionOutcomeDelivered  AIReplyTurnTaskExecutionOutcome = "delivered"
	AIReplyTurnTaskExecutionOutcomeFailed     AIReplyTurnTaskExecutionOutcome = "failed"
	AIReplyTurnTaskExecutionOutcomeCovered    AIReplyTurnTaskExecutionOutcome = "covered"
	AIReplyTurnTaskExecutionOutcomeHandoff    AIReplyTurnTaskExecutionOutcome = "handoff"
	AIReplyTurnTaskExecutionOutcomeSkipped    AIReplyTurnTaskExecutionOutcome = "skipped"
	AIReplyTurnTaskExecutionOutcomeSuperseded AIReplyTurnTaskExecutionOutcome = "superseded"
)

// AIReplyTurnTaskDeliveryOutcome is the external delivery dimension of a
// task result. A committed message is not the same fact as a delivered one.
type AIReplyTurnTaskDeliveryOutcome string

const (
	AIReplyTurnTaskDeliveryOutcomePending       AIReplyTurnTaskDeliveryOutcome = "pending"
	AIReplyTurnTaskDeliveryOutcomeCommitted     AIReplyTurnTaskDeliveryOutcome = "committed"
	AIReplyTurnTaskDeliveryOutcomeDelivered     AIReplyTurnTaskDeliveryOutcome = "delivered"
	AIReplyTurnTaskDeliveryOutcomeFailed        AIReplyTurnTaskDeliveryOutcome = "failed"
	AIReplyTurnTaskDeliveryOutcomeNotApplicable AIReplyTurnTaskDeliveryOutcome = "not_applicable"
)

// AIReplyTurnTaskBusinessOutcome is the customer-visible semantic result.
// In particular, a delivered fallback, a knowledge miss, and a technical
// failure are terminal facts but are not answered.
type AIReplyTurnTaskBusinessOutcome string

const (
	AIReplyTurnTaskBusinessOutcomeAnswered         AIReplyTurnTaskBusinessOutcome = "answered"
	AIReplyTurnTaskBusinessOutcomeNoHit            AIReplyTurnTaskBusinessOutcome = "no_hit"
	AIReplyTurnTaskBusinessOutcomeNoContext        AIReplyTurnTaskBusinessOutcome = "no_context"
	AIReplyTurnTaskBusinessOutcomeTechnicalFailure AIReplyTurnTaskBusinessOutcome = "technical_failure"
	AIReplyTurnTaskBusinessOutcomeResourceSent     AIReplyTurnTaskBusinessOutcome = "resource_sent"
	AIReplyTurnTaskBusinessOutcomeHandoff          AIReplyTurnTaskBusinessOutcome = "handoff"
	AIReplyTurnTaskBusinessOutcomeCancelled        AIReplyTurnTaskBusinessOutcome = "cancelled"
	AIReplyTurnTaskBusinessOutcomeSuperseded       AIReplyTurnTaskBusinessOutcome = "superseded"
)

const (
	AIReplyTurnTaskResultCodeDeliveredNoHit     = "delivered_no_hit"
	AIReplyTurnTaskResultCodeDeliveredNoContext = "delivered_no_context"
	AIReplyTurnTaskResultCodeTechnicalFailure   = "technical_failure"
)

// AIReplyTurnTaskOutcome keeps execution, delivery, and business facts
// separate while allowing the dialogue reducer to consume one authoritative
// classification.
type AIReplyTurnTaskOutcome struct {
	Execution AIReplyTurnTaskExecutionOutcome
	Delivery  AIReplyTurnTaskDeliveryOutcome
	Business  AIReplyTurnTaskBusinessOutcome
	Terminal  bool
}

// ClassifyAIReplyTurnTaskOutcome maps persisted task evidence into the three
// result dimensions. The mapping is deliberately conservative: any failed
// execution or explicit technical result can never become answered.
func ClassifyAIReplyTurnTaskOutcome(
	status AIReplyTurnTaskStatus,
	taskType AIReplyTurnTaskType,
	knowledgeStatus AIReplyTurnTaskKnowledgeStatus,
	resultCode string,
) AIReplyTurnTaskOutcome {
	resultCode = strings.TrimSpace(strings.ToLower(resultCode))
	outcome := AIReplyTurnTaskOutcome{
		Execution: AIReplyTurnTaskExecutionOutcomePending,
		Delivery:  AIReplyTurnTaskDeliveryOutcomePending,
	}
	switch status {
	case AIReplyTurnTaskStatusCommitted:
		outcome.Execution = AIReplyTurnTaskExecutionOutcomeCommitted
		outcome.Delivery = AIReplyTurnTaskDeliveryOutcomeCommitted
		outcome.Terminal = true
	case AIReplyTurnTaskStatusDelivered:
		outcome.Execution = AIReplyTurnTaskExecutionOutcomeDelivered
		outcome.Delivery = AIReplyTurnTaskDeliveryOutcomeDelivered
		outcome.Terminal = true
	case AIReplyTurnTaskStatusFailed:
		outcome.Execution = AIReplyTurnTaskExecutionOutcomeFailed
		outcome.Delivery = AIReplyTurnTaskDeliveryOutcomeNotApplicable
		outcome.Terminal = true
	case AIReplyTurnTaskStatusCovered:
		outcome.Execution = AIReplyTurnTaskExecutionOutcomeCovered
		outcome.Delivery = AIReplyTurnTaskDeliveryOutcomeNotApplicable
		outcome.Terminal = true
	case AIReplyTurnTaskStatusHandoff, AIReplyTurnTaskStatusHandoffPending:
		outcome.Execution = AIReplyTurnTaskExecutionOutcomeHandoff
		outcome.Delivery = AIReplyTurnTaskDeliveryOutcomeNotApplicable
		outcome.Terminal = true
	case AIReplyTurnTaskStatusSkipped:
		outcome.Execution = AIReplyTurnTaskExecutionOutcomeSkipped
		outcome.Delivery = AIReplyTurnTaskDeliveryOutcomeNotApplicable
		outcome.Terminal = true
	case AIReplyTurnTaskStatusSuperseded:
		outcome.Execution = AIReplyTurnTaskExecutionOutcomeSuperseded
		outcome.Delivery = AIReplyTurnTaskDeliveryOutcomeNotApplicable
		outcome.Terminal = true
	}

	switch {
	case resultCode == AIReplyTurnTaskResultCodeTechnicalFailure || strings.HasPrefix(resultCode, AIReplyTurnTaskResultCodeTechnicalFailure+"_"):
		outcome.Business = AIReplyTurnTaskBusinessOutcomeTechnicalFailure
	case knowledgeStatus == AIReplyTurnTaskKnowledgeStatusFailed || outcome.Execution == AIReplyTurnTaskExecutionOutcomeFailed:
		outcome.Business = AIReplyTurnTaskBusinessOutcomeTechnicalFailure
	case knowledgeStatus == AIReplyTurnTaskKnowledgeStatusNoContext || strings.HasSuffix(resultCode, "_no_context"):
		outcome.Business = AIReplyTurnTaskBusinessOutcomeNoContext
	case knowledgeStatus == AIReplyTurnTaskKnowledgeStatusNoHit || strings.HasSuffix(resultCode, "_no_hit"):
		outcome.Business = AIReplyTurnTaskBusinessOutcomeNoHit
	case outcome.Execution == AIReplyTurnTaskExecutionOutcomeHandoff:
		outcome.Business = AIReplyTurnTaskBusinessOutcomeHandoff
	case outcome.Execution == AIReplyTurnTaskExecutionOutcomeCovered || outcome.Execution == AIReplyTurnTaskExecutionOutcomeSuperseded:
		outcome.Business = AIReplyTurnTaskBusinessOutcomeSuperseded
	case outcome.Execution == AIReplyTurnTaskExecutionOutcomeSkipped:
		outcome.Business = AIReplyTurnTaskBusinessOutcomeCancelled
	case outcome.Execution == AIReplyTurnTaskExecutionOutcomeCommitted || outcome.Execution == AIReplyTurnTaskExecutionOutcomeDelivered:
		if taskType == AIReplyTurnTaskTypeResource {
			outcome.Business = AIReplyTurnTaskBusinessOutcomeResourceSent
		} else {
			outcome.Business = AIReplyTurnTaskBusinessOutcomeAnswered
		}
	}
	return outcome
}
