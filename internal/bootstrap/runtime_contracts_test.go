package bootstrap

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestRuntimeContractSchemasValidateBeforeStartup(t *testing.T) {
	if err := contracts.ValidateEmbeddedSchemas(); err != nil {
		t.Fatalf("validate embedded AI runtime schemas: %v", err)
	}
}
