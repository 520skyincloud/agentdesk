package executor

import (
	"fmt"
	"testing"

	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/services"
)

func TestRuntimeErrorStageClassifiesContextCompilerFailures(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("compile: %w", contextcompiler.ErrMandatoryContextOverflow),
		fmt.Errorf("compile: %w", contextcompiler.ErrRequiredEvidenceOverflow),
		fmt.Errorf("compile: %w", contextcompiler.ErrInvalidContextLimit),
		fmt.Errorf("compile: %w", contextcompiler.ErrRuntimeScopeMismatch),
	} {
		if got := runtimeErrorStage(err, "intent_detect"); got != "context_build" {
			t.Fatalf("error=%v stage=%q want=context_build", err, got)
		}
	}
}

func TestRuntimeErrorStagePreservesControlledStage(t *testing.T) {
	err := services.NewAIReplyExecutionError(services.AIReplyExecutionErrorIntentDetectFailed, fmt.Errorf("upstream timeout"))
	if got := runtimeErrorStage(err, "context_build"); got != "intent_detect" {
		t.Fatalf("stage=%q want=intent_detect", got)
	}
}
