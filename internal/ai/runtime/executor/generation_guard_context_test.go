package executor

import (
	"context"
	"testing"
)

func TestGenerationGuardInstructionContext(t *testing.T) {
	base := context.Background()
	if got := generationGuardInstruction(base); got != "" {
		t.Fatalf("empty guard instruction=%q", got)
	}
	if got := generationGuardInstruction(nil); got != "" {
		t.Fatalf("nil context guard instruction=%q", got)
	}

	guarded := WithGenerationGuardInstruction(base, "  只回答新增问题，不要重复上一答案。  ")
	if got := generationGuardInstruction(guarded); got != "只回答新增问题，不要重复上一答案。" {
		t.Fatalf("guard instruction=%q", got)
	}
	if got := generationGuardInstruction(base); got != "" {
		t.Fatalf("base context was mutated: %q", got)
	}
	if got := WithGenerationGuardInstruction(base, "   "); got != base {
		t.Fatal("blank guard instruction should preserve the original context")
	}
}
