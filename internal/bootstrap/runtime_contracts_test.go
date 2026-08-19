package bootstrap

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestRuntimeContractSchemasValidateBeforeStartup(t *testing.T) {
	fingerprint, err := contracts.ValidateProductionRuntimeContracts()
	if err != nil {
		t.Fatalf("validate production AI runtime schemas: %v", err)
	}
	if fingerprint.ContractSet != contracts.RuntimeContractSetStableV2 || len(fingerprint.IntentSchemaHash) != 64 || len(fingerprint.ReplySchemaHash) != 64 {
		t.Fatalf("unexpected production contract fingerprint: %+v", fingerprint)
	}
}
