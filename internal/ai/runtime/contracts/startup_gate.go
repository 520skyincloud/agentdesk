package contracts

import (
	"fmt"
	"os"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"
)

const RuntimeContractSetStableV2 = "stable_v2"

type RuntimeContractFingerprint struct {
	ContractSet      string
	IntentSchemaHash string
	ReplySchemaHash  string
}

// ValidateProductionRuntimeContracts runs before configuration, database and
// workers are initialized. It validates the real stable-v2 contracts through
// the same normalization path used by the Responses adapter.
func ValidateProductionRuntimeContracts() (RuntimeContractFingerprint, error) {
	if err := validateStableV2Environment(); err != nil {
		return RuntimeContractFingerprint{}, err
	}
	if err := ValidateEmbeddedSchemas(); err != nil {
		return RuntimeContractFingerprint{}, err
	}
	intentSchema, _, err := BuildRuntimeIntentSchema(MustSchema(SchemaIntentTasksV2), stableV2StartupIntentCatalog())
	if err != nil {
		return RuntimeContractFingerprint{}, fmt.Errorf("build stable-v2 intent schema: %w", err)
	}
	intentPrepared, err := modelconfig.PrepareResponsesJSONSchema(intentSchema)
	if err != nil {
		return RuntimeContractFingerprint{}, fmt.Errorf("validate stable-v2 intent schema: %w", err)
	}
	replyPrepared, err := modelconfig.PrepareResponsesJSONSchema(MustSchema(SchemaReplyOutputV2))
	if err != nil {
		return RuntimeContractFingerprint{}, fmt.Errorf("validate stable-v2 reply schema: %w", err)
	}
	return RuntimeContractFingerprint{
		ContractSet:      RuntimeContractSetStableV2,
		IntentSchemaHash: intentPrepared.Fingerprint,
		ReplySchemaHash:  replyPrepared.Fingerprint,
	}, nil
}

func validateStableV2Environment() error {
	constraints := map[string]string{
		"AI_RUNTIME_CONTEXT_COMPILER": "v2",
		"AI_RUNTIME_INTENT_CONTRACT":  "v2",
		"AI_RUNTIME_REPLY_CONTRACT":   "v2",
		"AI_RUNTIME_VALIDATOR":        "v2",
		"AI_RUNTIME_ACTION_LEDGER":    "authoritative",
	}
	for name, expected := range constraints {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
		if value != "" && value != expected {
			return fmt.Errorf("%s=%q is incompatible with production contract set %s", name, value, RuntimeContractSetStableV2)
		}
	}
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("AI_RUNTIME_MULTIMODAL_V3_STRICT"))); value != "" && value != "off" {
		return fmt.Errorf("AI_RUNTIME_MULTIMODAL_V3_STRICT=%q cannot serve production traffic; contract set must be %s", value, RuntimeContractSetStableV2)
	}
	return nil
}

func stableV2StartupIntentCatalog() []models.ReplyIntentConfig {
	return []models.ReplyIntentConfig{
		{Code: "hotel_info", Status: enums.StatusOk},
		{Code: "hotel_variable", Status: enums.StatusOk, NeedsResource: true, ResourceType: "store_variable"},
		{Code: "service_request", Status: enums.StatusOk},
		{Code: "human_complaint_risk", Status: enums.StatusOk},
		{Code: "interaction", Status: enums.StatusOk},
	}
}
