package runtime

import (
	"strings"
	"testing"

	"agent-desk/internal/pkg/modelconfig"
	svc "agent-desk/internal/services"
)

func TestReplyUsageErrorFieldsPreservesInvocationMetadata(t *testing.T) {
	cause := modelconfig.NewInvocationErrorWithMetadata(
		modelconfig.InvocationErrorPayloadRejected,
		400,
		"bad_request",
		"invalid_schema",
		false,
	)
	err := svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorGenerationFailed, cause)
	class, message := replyUsageErrorFields(err)
	if class != modelconfig.InvocationErrorPayloadRejected {
		t.Fatalf("class = %q, want %q", class, modelconfig.InvocationErrorPayloadRejected)
	}
	for _, expected := range []string{
		"code=generation_failed",
		"cause=model_or_payload_rejected",
		"http=400",
		"provider_status=bad_request",
		"provider_code=invalid_schema",
		"retryable=false",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("message %q does not contain %q", message, expected)
		}
	}
}

func TestReplyUsageErrorFieldsAvoidsRawCauseText(t *testing.T) {
	err := svc.NewAIReplyExecutionError(
		svc.AIReplyExecutionErrorGenerationFailed,
		modelconfig.NewInvocationError(modelconfig.InvocationErrorNetwork, 0, true),
	)
	class, message := replyUsageErrorFields(err)
	if class != modelconfig.InvocationErrorNetwork {
		t.Fatalf("class = %q, want %q", class, modelconfig.InvocationErrorNetwork)
	}
	if strings.Contains(message, "model invocation failed") {
		t.Fatalf("usage diagnostic leaked raw cause text: %q", message)
	}
	if !strings.Contains(message, "retryable=true") {
		t.Fatalf("message %q does not preserve retryability", message)
	}
}
