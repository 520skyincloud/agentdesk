package modelconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	InvocationErrorModelCallFailed                = "model_call_failed"
	InvocationErrorNetwork                        = "network_error"
	InvocationErrorTimeout                        = "timeout"
	InvocationErrorCredentialRejected             = "credential_rejected"
	InvocationErrorEndpointNotFound               = "endpoint_not_found"
	InvocationErrorRateLimited                    = "rate_limited"
	InvocationErrorUpstream                       = "upstream_error"
	InvocationErrorPayloadRejected                = "model_or_payload_rejected"
	InvocationErrorStructuredOutputSchemaRejected = "structured_output_schema_rejected"
	InvocationErrorInvalidResponse                = "invalid_response"
	InvocationErrorEmptyOutput                    = "empty_output"
)

type InvocationError struct {
	Class          string
	StatusCode     int
	ResponseStatus string
	ProviderCode   string
	Retryable      bool
}

func (e *InvocationError) Error() string {
	if e == nil {
		return ""
	}
	class := strings.TrimSpace(e.Class)
	if class == "" {
		class = InvocationErrorModelCallFailed
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("model invocation failed (%s, status=%d)", class, e.StatusCode)
	}
	return fmt.Sprintf("model invocation failed (%s)", class)
}

func NewInvocationError(class string, statusCode int, retryable bool) error {
	return &InvocationError{Class: strings.TrimSpace(class), StatusCode: statusCode, Retryable: retryable}
}

func NewInvocationErrorWithMetadata(class string, statusCode int, responseStatus, providerCode string, retryable bool) error {
	return &InvocationError{
		Class:          strings.TrimSpace(class),
		StatusCode:     statusCode,
		ResponseStatus: strings.TrimSpace(responseStatus),
		ProviderCode:   strings.TrimSpace(providerCode),
		Retryable:      retryable,
	}
}

type InvocationErrorMetadata struct {
	Class          string
	StatusCode     int
	ResponseStatus string
	ProviderCode   string
	Retryable      bool
}

func InvocationErrorDetails(err error) (InvocationErrorMetadata, bool) {
	var invocationErr *InvocationError
	if !errors.As(err, &invocationErr) || invocationErr == nil {
		return InvocationErrorMetadata{}, false
	}
	return InvocationErrorMetadata{
		Class:          strings.TrimSpace(invocationErr.Class),
		StatusCode:     invocationErr.StatusCode,
		ResponseStatus: strings.TrimSpace(invocationErr.ResponseStatus),
		ProviderCode:   strings.TrimSpace(invocationErr.ProviderCode),
		Retryable:      invocationErr.Retryable,
	}, true
}

func InvocationErrorClass(err error) string {
	if err == nil {
		return ""
	}
	var invocationErr *InvocationError
	if errors.As(err, &invocationErr) && strings.TrimSpace(invocationErr.Class) != "" {
		return strings.TrimSpace(invocationErr.Class)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return InvocationErrorTimeout
	}
	return InvocationErrorModelCallFailed
}

func InvocationErrorRetryable(err error) bool {
	if err == nil {
		return false
	}
	var invocationErr *InvocationError
	if errors.As(err, &invocationErr) {
		return invocationErr.Retryable
	}
	return true
}
