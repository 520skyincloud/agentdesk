package enums

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
	AIReplyTurnTaskStatusPending        AIReplyTurnTaskStatus = "pending"
	AIReplyTurnTaskStatusRunning        AIReplyTurnTaskStatus = "running"
	AIReplyTurnTaskStatusReady          AIReplyTurnTaskStatus = "ready"
	AIReplyTurnTaskStatusFailed         AIReplyTurnTaskStatus = "failed"
	AIReplyTurnTaskStatusCommitted      AIReplyTurnTaskStatus = "committed"
	AIReplyTurnTaskStatusDelivered      AIReplyTurnTaskStatus = "delivered"
	AIReplyTurnTaskStatusCovered        AIReplyTurnTaskStatus = "covered"
	AIReplyTurnTaskStatusHandoffPending AIReplyTurnTaskStatus = "handoff_pending"
	AIReplyTurnTaskStatusHandoff        AIReplyTurnTaskStatus = "handoff"
	AIReplyTurnTaskStatusSkipped        AIReplyTurnTaskStatus = "skipped"
	AIReplyTurnTaskStatusSuperseded     AIReplyTurnTaskStatus = "superseded"
)

type AIReplyTurnTaskKnowledgeStatus string

const (
	AIReplyTurnTaskKnowledgeStatusNone    AIReplyTurnTaskKnowledgeStatus = "none"
	AIReplyTurnTaskKnowledgeStatusPending AIReplyTurnTaskKnowledgeStatus = "pending"
	AIReplyTurnTaskKnowledgeStatusHit     AIReplyTurnTaskKnowledgeStatus = "hit"
	AIReplyTurnTaskKnowledgeStatusNoHit   AIReplyTurnTaskKnowledgeStatus = "no_hit"
	AIReplyTurnTaskKnowledgeStatusFailed  AIReplyTurnTaskKnowledgeStatus = "failed"
)
