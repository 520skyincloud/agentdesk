package contextcompiler

import "testing"

func TestCalculateBudgetUsesMinimumContextAndStageCap(t *testing.T) {
	budget, err := CalculateBudget(CompileStageGenerate, 8192, 8000, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if budget.ContextLimit != 8000 || budget.ReservedOutput != 1024 || budget.SafetyMargin != 400 || budget.AvailableInput != 6576 {
		t.Fatalf("budget=%+v", budget)
	}
	intent, err := CalculateBudget(CompileStageIntent, 8192, 8000, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if intent.ReservedOutput != 1024 || intent.AvailableInput != 6576 {
		t.Fatalf("intent budget=%+v", intent)
	}
}

func TestCalculateBudgetRejectsUnsafeConfiguration(t *testing.T) {
	if _, err := CalculateBudget(CompileStageGenerate, 0, 8000, 512); err == nil {
		t.Fatal("missing model limit must fail")
	}
	if _, err := CalculateBudget(CompileStageGenerate, 1400, 1400, 512); err == nil {
		t.Fatal("input budget below 1024 must fail")
	}
}
