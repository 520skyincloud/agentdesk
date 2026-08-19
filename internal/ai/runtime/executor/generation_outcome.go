package executor

import "agent-desk/internal/ai/runtime/internal/impl/callbacks"

type GenerationOutcome string

const (
	GenerationOutcomeGenerated        GenerationOutcome = "generated"
	GenerationOutcomeRepaired         GenerationOutcome = "repaired"
	GenerationOutcomeSafeDegraded     GenerationOutcome = "safe_degraded"
	GenerationOutcomeGenerationFailed GenerationOutcome = "generation_failed"
	GenerationOutcomeSkipped          GenerationOutcome = "skipped"
)

func setGenerationOutcome(summary *RunResult, collector *callbacks.RuntimeTraceCollector, outcome GenerationOutcome) {
	if summary != nil {
		summary.GenerationOutcome = outcome
	}
	if collector != nil {
		collector.Data.Output.GenerationOutcome = string(outcome)
	}
}
