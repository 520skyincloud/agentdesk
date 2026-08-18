package executor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	runtimeContextCompilerLegacy = "legacy"
	runtimeContextCompilerShadow = "shadow"
	runtimeContextCompilerV2     = "v2"

	runtimeIntentContractV1 = "v1"
	runtimeIntentContractV2 = "v2"
	runtimeIntentContractV3 = "v3"

	runtimeReplyContractLegacy = "legacy"
	runtimeReplyContractV2     = "v2"

	runtimeValidatorLegacy = "legacy"
	runtimeValidatorV2     = "v2"

	runtimeActionLedgerShadow        = "shadow"
	runtimeActionLedgerAuthoritative = "authoritative"

	// The old production flag is intentionally ignored. Strict V3 remains
	// available only as an explicit experiment so stale server configuration
	// cannot put customer traffic back on the span/group protocol.
	runtimeMultimodalV3StrictEnv = "AI_RUNTIME_MULTIMODAL_V3_STRICT"
)

type runtimeFeatureModes struct {
	ContextCompiler string
	IntentContract  string
	ReplyContract   string
	Validator       string
	ActionLedger    string
}

func resolveRuntimeFeatureModes(req RunInput) runtimeFeatureModes {
	if !runtimeV2ScopeEnabled(req) {
		return legacyRuntimeFeatureModes()
	}
	// V2 已是默认主链：未显式设置环境变量时，全部落到 v2/authoritative。
	// legacyRuntimeFeatureModes() 仅在 runtimeV2ScopeEnabled 白名单排除时作为回退手段保留。
	modes := runtimeFeatureModes{
		ContextCompiler: runtimeModeEnv("AI_RUNTIME_CONTEXT_COMPILER", runtimeContextCompilerV2, runtimeContextCompilerLegacy, runtimeContextCompilerShadow, runtimeContextCompilerV2),
		IntentContract:  runtimeModeEnv("AI_RUNTIME_INTENT_CONTRACT", runtimeIntentContractV2, runtimeIntentContractV1, runtimeIntentContractV2),
		ReplyContract:   runtimeModeEnv("AI_RUNTIME_REPLY_CONTRACT", runtimeReplyContractV2, runtimeReplyContractLegacy, runtimeReplyContractV2),
		Validator:       runtimeModeEnv("AI_RUNTIME_VALIDATOR", runtimeValidatorV2, runtimeValidatorLegacy, runtimeValidatorV2),
		ActionLedger:    runtimeModeEnv("AI_RUNTIME_ACTION_LEDGER", runtimeActionLedgerAuthoritative, runtimeActionLedgerShadow, runtimeActionLedgerAuthoritative),
	}
	// Strict V3 is isolated from ordinary production overrides.
	if multimodalV3Enabled() {
		modes.IntentContract = runtimeIntentContractV3
		modes.ContextCompiler = runtimeContextCompilerV2
	}
	return modes
}

// multimodalV3Enabled is an explicit experimental opt-in. The former
// AI_RUNTIME_MULTIMODAL_V3 variable is retained only for compatibility and
// cannot activate strict V3 serving behavior.
func multimodalV3Enabled() bool {
	return strings.TrimSpace(os.Getenv(runtimeMultimodalV3StrictEnv)) == "on"
}

func legacyRuntimeFeatureModes() runtimeFeatureModes {
	return runtimeFeatureModes{
		ContextCompiler: runtimeContextCompilerLegacy,
		IntentContract:  runtimeIntentContractV1, ReplyContract: runtimeReplyContractLegacy,
		Validator: runtimeValidatorLegacy, ActionLedger: runtimeActionLedgerShadow,
	}
}

func validateRuntimeFeatureModes(modes runtimeFeatureModes) error {
	if modes.ReplyContract == runtimeReplyContractV2 && modes.ContextCompiler != runtimeContextCompilerV2 {
		return fmt.Errorf("AI runtime reply contract v2 requires context compiler v2")
	}
	if modes.Validator == runtimeValidatorV2 && modes.ReplyContract != runtimeReplyContractV2 {
		return fmt.Errorf("AI runtime validator v2 requires reply contract v2")
	}
	if modes.ActionLedger == runtimeActionLedgerAuthoritative && modes.ReplyContract != runtimeReplyContractV2 {
		return fmt.Errorf("AI runtime authoritative action ledger requires reply contract v2")
	}
	if modes.IntentContract == runtimeIntentContractV3 && modes.ContextCompiler != runtimeContextCompilerV2 {
		return fmt.Errorf("AI runtime intent contract v3 requires context compiler v2")
	}
	return nil
}

func runtimeModeEnv(name, fallback string, allowed ...string) string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return fallback
}

func runtimeV2ScopeEnabled(req RunInput) bool {
	checks := []struct {
		name  string
		value int64
	}{
		{name: "AI_RUNTIME_V2_TENANT_IDS", value: req.Conversation.TenantID},
		{name: "AI_RUNTIME_V2_STORE_IDS", value: req.Conversation.StoreID},
		{name: "AI_RUNTIME_V2_BINDING_IDS", value: req.Conversation.StoreStaffBindingID},
	}
	for _, check := range checks {
		values, configured := runtimeInt64Allowlist(os.Getenv(check.name))
		if configured && !values[check.value] {
			return false
		}
	}
	return true
}

func runtimeInt64Allowlist(raw string) (map[int64]bool, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	ret := make(map[int64]bool)
	for _, item := range strings.Split(raw, ",") {
		value, err := strconv.ParseInt(strings.TrimSpace(item), 10, 64)
		if err == nil && value > 0 {
			ret[value] = true
		}
	}
	return ret, true
}
