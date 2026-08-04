package services

import (
	"context"
	"time"

	"agent-desk/internal/models"
)

type AIReplyExecutionStatus string

const (
	AIReplyExecutionStatusCompleted  AIReplyExecutionStatus = "completed"
	AIReplyExecutionStatusSkipped    AIReplyExecutionStatus = "skipped"
	AIReplyExecutionStatusSuperseded AIReplyExecutionStatus = "superseded"
	AIReplyExecutionStatusDeferred   AIReplyExecutionStatus = "deferred"
)

type AIReplyExecutionResult struct {
	Status     AIReplyExecutionStatus
	ReasonCode string
	RetryAt    *time.Time
}

var TriggerAIReplySyncHook func(ctx context.Context, conversation models.Conversation, message models.Message) (AIReplyExecutionResult, error)
