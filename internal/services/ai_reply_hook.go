package services

import (
	"context"
	"errors"
	"strings"
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

type AIReplyExecutionErrorCode string

const (
	AIReplyExecutionErrorIntentDetectFailed      AIReplyExecutionErrorCode = "intent_detect_failed"
	AIReplyExecutionErrorGenerationFailed        AIReplyExecutionErrorCode = "generation_failed"
	AIReplyExecutionErrorKnowledgeUnavailable    AIReplyExecutionErrorCode = "knowledge_unavailable"
	AIReplyExecutionErrorEmptyOutput             AIReplyExecutionErrorCode = "empty_output"
	AIReplyExecutionErrorResourceInvariantBroken AIReplyExecutionErrorCode = "resource_invariant_broken"
	AIReplyExecutionErrorCommitFailed            AIReplyExecutionErrorCode = "commit_failed"
)

type AIReplyExecutionError struct {
	code  AIReplyExecutionErrorCode
	cause error
}

func (e *AIReplyExecutionError) Error() string {
	if e == nil {
		return ""
	}
	return string(e.code)
}

func (e *AIReplyExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func NewAIReplyExecutionError(code AIReplyExecutionErrorCode, cause error) error {
	if strings.TrimSpace(string(code)) == "" {
		code = AIReplyExecutionErrorGenerationFailed
	}
	return &AIReplyExecutionError{code: code, cause: cause}
}

func AIReplyExecutionErrorCodeOf(err error) (AIReplyExecutionErrorCode, bool) {
	var controlled *AIReplyExecutionError
	if !errors.As(err, &controlled) || controlled == nil || strings.TrimSpace(string(controlled.code)) == "" {
		return "", false
	}
	return controlled.code, true
}

type AIReplyExecutionResult struct {
	Status               AIReplyExecutionStatus
	ReasonCode           string
	RetryAt              *time.Time
	CommittedMessageIDs  []int64
	PersistedInterruptID int64
}

var TriggerAIReplySyncHook func(ctx context.Context, conversation models.Conversation, message models.Message) (AIReplyExecutionResult, error)
