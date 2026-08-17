package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
)

func TestRuntimeTraceIdentityReportsEffectiveV3Mode(t *testing.T) {
	version, mode := runtimeTraceIdentity(runtimeFeatureModes{
		ContextCompiler: runtimeContextCompilerV2, IntentContract: runtimeIntentContractV3,
		ReplyContract: runtimeReplyContractV3, Validator: runtimeValidatorV3, ActionLedger: runtimeActionLedgerAuthoritative,
	})
	if version != "v3" || !strings.Contains(mode, "intent=v3") || !strings.Contains(mode, "reply=v3") {
		t.Fatalf("unexpected trace identity: version=%q mode=%q", version, mode)
	}
}

func TestResolveRuntimeFeatureModesDefaultsToV2(t *testing.T) {
	req := RunInput{Conversation: models.Conversation{TenantID: 1, StoreID: 2, StoreStaffBindingID: 3}}
	modes := resolveRuntimeFeatureModes(req)
	if modes.ContextCompiler != runtimeContextCompilerV2 || modes.IntentContract != runtimeIntentContractV2 ||
		modes.ReplyContract != runtimeReplyContractV2 || modes.Validator != runtimeValidatorV2 ||
		modes.ActionLedger != runtimeActionLedgerAuthoritative {
		t.Fatalf("unexpected default modes: %+v", modes)
	}
}

func TestLegacyMultimodalV3FlagCannotReenableStrictRuntime(t *testing.T) {
	t.Setenv("AI_RUNTIME_MULTIMODAL_V3", "on")
	t.Setenv("AI_RUNTIME_MULTIMODAL_V3_STRICT", "off")
	resolved := resolveRuntimeFeatureModes(RunInput{})
	if resolved.IntentContract != runtimeIntentContractV2 || resolved.ReplyContract != runtimeReplyContractV2 || resolved.Validator != runtimeValidatorV2 {
		t.Fatalf("legacy V3 flag must keep the simplified V2 serving path: %+v", resolved)
	}
}

func TestResolveRuntimeFeatureModesScopeMismatchFallsBackToLegacy(t *testing.T) {
	t.Setenv("AI_RUNTIME_V2_BINDING_IDS", "33")
	modes := resolveRuntimeFeatureModes(RunInput{Conversation: models.Conversation{TenantID: 11, StoreID: 22, StoreStaffBindingID: 34}})
	if modes != legacyRuntimeFeatureModes() {
		t.Fatalf("scope mismatch must fall back to legacy: %+v", modes)
	}
}

func TestResolveRuntimeFeatureModesHonorsScopedV2(t *testing.T) {
	t.Setenv("AI_RUNTIME_CONTEXT_COMPILER", "v2")
	t.Setenv("AI_RUNTIME_INTENT_CONTRACT", "v2")
	t.Setenv("AI_RUNTIME_REPLY_CONTRACT", "v2")
	t.Setenv("AI_RUNTIME_VALIDATOR", "v2")
	t.Setenv("AI_RUNTIME_ACTION_LEDGER", "authoritative")
	t.Setenv("AI_RUNTIME_V2_TENANT_IDS", "11")
	t.Setenv("AI_RUNTIME_V2_STORE_IDS", "22")
	t.Setenv("AI_RUNTIME_V2_BINDING_IDS", "33")

	enabled := resolveRuntimeFeatureModes(RunInput{Conversation: models.Conversation{TenantID: 11, StoreID: 22, StoreStaffBindingID: 33}})
	if enabled.ContextCompiler != runtimeContextCompilerV2 || enabled.IntentContract != runtimeIntentContractV2 ||
		enabled.ReplyContract != runtimeReplyContractV2 || enabled.Validator != runtimeValidatorV2 ||
		enabled.ActionLedger != runtimeActionLedgerAuthoritative {
		t.Fatalf("unexpected enabled modes: %+v", enabled)
	}
	disabled := resolveRuntimeFeatureModes(RunInput{Conversation: models.Conversation{TenantID: 11, StoreID: 22, StoreStaffBindingID: 34}})
	if disabled != legacyRuntimeFeatureModes() {
		t.Fatalf("scope mismatch must fall back completely: %+v", disabled)
	}
}

func TestValidateRuntimeFeatureModesRejectsUnsafeCombinations(t *testing.T) {
	tests := []runtimeFeatureModes{
		{ContextCompiler: runtimeContextCompilerLegacy, ReplyContract: runtimeReplyContractV2, Validator: runtimeValidatorLegacy, ActionLedger: runtimeActionLedgerShadow},
		{ContextCompiler: runtimeContextCompilerV2, ReplyContract: runtimeReplyContractLegacy, Validator: runtimeValidatorV2, ActionLedger: runtimeActionLedgerShadow},
		{ContextCompiler: runtimeContextCompilerV2, ReplyContract: runtimeReplyContractLegacy, Validator: runtimeValidatorLegacy, ActionLedger: runtimeActionLedgerAuthoritative},
	}
	for _, modes := range tests {
		if err := validateRuntimeFeatureModes(modes); err == nil {
			t.Fatalf("expected unsafe modes to be rejected: %+v", modes)
		}
	}
}

func TestValidateRuntimeFeatureModesAcceptsMigrationPhases(t *testing.T) {
	valid := []runtimeFeatureModes{
		legacyRuntimeFeatureModes(),
		{ContextCompiler: runtimeContextCompilerShadow, IntentContract: runtimeIntentContractV1, ReplyContract: runtimeReplyContractLegacy, Validator: runtimeValidatorLegacy, ActionLedger: runtimeActionLedgerShadow},
		{ContextCompiler: runtimeContextCompilerV2, IntentContract: runtimeIntentContractV2, ReplyContract: runtimeReplyContractLegacy, Validator: runtimeValidatorLegacy, ActionLedger: runtimeActionLedgerShadow},
		{ContextCompiler: runtimeContextCompilerV2, IntentContract: runtimeIntentContractV2, ReplyContract: runtimeReplyContractV2, Validator: runtimeValidatorLegacy, ActionLedger: runtimeActionLedgerShadow},
		{ContextCompiler: runtimeContextCompilerV2, IntentContract: runtimeIntentContractV2, ReplyContract: runtimeReplyContractV2, Validator: runtimeValidatorV2, ActionLedger: runtimeActionLedgerAuthoritative},
	}
	for _, modes := range valid {
		if err := validateRuntimeFeatureModes(modes); err != nil {
			t.Fatalf("expected migration phase to be valid: %+v: %v", modes, err)
		}
	}
}
