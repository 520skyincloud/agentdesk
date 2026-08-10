package executor

import (
	"context"
	"strings"
)

type generationGuardInstructionKey struct{}

func WithGenerationGuardInstruction(ctx context.Context, instruction string) context.Context {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return ctx
	}
	return context.WithValue(ctx, generationGuardInstructionKey{}, instruction)
}

func generationGuardInstruction(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(generationGuardInstructionKey{}).(string)
	return strings.TrimSpace(value)
}
