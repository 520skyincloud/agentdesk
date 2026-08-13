package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/modelconfig"
	"agent-desk/internal/pkg/strictjson"
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
	code     AIReplyExecutionErrorCode
	cause    error
	metadata AIReplyExecutionErrorMetadata
}

type AIReplyExecutionErrorMetadata struct {
	Code              AIReplyExecutionErrorCode
	CauseClass        string
	ProtocolCode      string
	JSONPath          string
	HTTPStatus        int
	ProviderStatus    string
	ProviderCode      string
	Retryable         bool
	RetryabilityKnown bool
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
	metadata := AIReplyExecutionErrorMetadata{Code: code}
	if invocation, ok := modelconfig.InvocationErrorDetails(cause); ok {
		metadata.CauseClass = strings.TrimSpace(invocation.Class)
		metadata.HTTPStatus = invocation.StatusCode
		metadata.ProviderStatus = strings.TrimSpace(invocation.ResponseStatus)
		metadata.ProviderCode = strings.TrimSpace(invocation.ProviderCode)
		metadata.Retryable = invocation.Retryable
		metadata.RetryabilityKnown = true
	}
	var protocolErr *strictjson.ProtocolError
	if errors.As(cause, &protocolErr) && protocolErr != nil {
		metadata.ProtocolCode = strings.TrimSpace(protocolErr.Code)
		metadata.JSONPath = strings.TrimSpace(protocolErr.Path)
		if metadata.CauseClass == "" {
			metadata.CauseClass = "protocol_error"
		}
		if !metadata.RetryabilityKnown {
			metadata.RetryabilityKnown = true
			metadata.Retryable = false
		}
	}
	if errors.Is(cause, context.DeadlineExceeded) && !metadata.RetryabilityKnown {
		metadata.CauseClass = modelconfig.InvocationErrorTimeout
		metadata.Retryable = true
		metadata.RetryabilityKnown = true
	}
	return &AIReplyExecutionError{code: code, cause: cause, metadata: metadata}
}

func AIReplyExecutionErrorCodeOf(err error) (AIReplyExecutionErrorCode, bool) {
	var controlled *AIReplyExecutionError
	if !errors.As(err, &controlled) || controlled == nil || strings.TrimSpace(string(controlled.code)) == "" {
		return "", false
	}
	return controlled.code, true
}

func AIReplyExecutionErrorDetailsOf(err error) (AIReplyExecutionErrorMetadata, bool) {
	var controlled *AIReplyExecutionError
	if !errors.As(err, &controlled) || controlled == nil || strings.TrimSpace(string(controlled.code)) == "" {
		return AIReplyExecutionErrorMetadata{}, false
	}
	metadata := controlled.metadata
	metadata.Code = controlled.code
	return metadata, true
}

type AIReplyExecutionResult struct {
	Status               AIReplyExecutionStatus
	ReasonCode           string
	RetryAt              *time.Time
	CommittedMessageIDs  []int64
	PersistedInterruptID int64
	CoveredByMessageID   int64
	CoveredByTaskID      int64
	TaskLedgerEnabled    bool
	TaskKeys             []string
	FailedTaskKeys       []string
	HumanTaskKeys        []string
	HasRemainingTasks    bool
}

var TriggerAIReplySyncHook func(ctx context.Context, conversation models.Conversation, message models.Message) (AIReplyExecutionResult, error)
