package contextcompiler

import (
	"errors"
	"fmt"
	"math"
)

const (
	intentOutputCap   = 1024
	generateOutputCap = 512
	minimumInputLimit = 1024
)

var (
	ErrInvalidContextLimit      = errors.New("context_limit_invalid")
	ErrMandatoryContextOverflow = errors.New("context_mandatory_overflow")
	ErrRequiredEvidenceOverflow = errors.New("context_required_evidence_overflow")
)

type Budget struct {
	ContextLimit   int
	ReservedOutput int
	SafetyMargin   int
	AvailableInput int
}

func CalculateBudget(stage CompileStage, modelContextLimit, instanceContextLimit, modelOutputLimit int) (Budget, error) {
	if modelContextLimit <= 0 || instanceContextLimit <= 0 {
		return Budget{}, fmt.Errorf("%w: model=%d instance=%d", ErrInvalidContextLimit, modelContextLimit, instanceContextLimit)
	}
	contextLimit := min(modelContextLimit, instanceContextLimit)
	outputCap := generateOutputCap
	if stage == CompileStageIntent {
		outputCap = intentOutputCap
	} else if stage != CompileStageGenerate {
		return Budget{}, fmt.Errorf("%w: unknown stage %q", ErrInvalidContextLimit, stage)
	}
	reservedOutput := min(max(modelOutputLimit, 1), outputCap)
	safetyMargin := clamp(int(math.Ceil(float64(contextLimit)*0.05)), 256, 1024)
	available := contextLimit - reservedOutput - safetyMargin
	if available < minimumInputLimit {
		return Budget{}, fmt.Errorf("%w: available input %d is below %d", ErrInvalidContextLimit, available, minimumInputLimit)
	}
	return Budget{ContextLimit: contextLimit, ReservedOutput: reservedOutput, SafetyMargin: safetyMargin, AvailableInput: available}, nil
}

func clamp(value, lower, upper int) int {
	if value < lower {
		return lower
	}
	if value > upper {
		return upper
	}
	return value
}
