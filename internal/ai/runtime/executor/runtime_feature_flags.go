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

	runtimeReplyContractLegacy = "legacy"
	runtimeReplyContractV2     = "v2"

	runtimeValidatorLegacy = "legacy"
	runtimeValidatorV2     = "v2"

	runtimeActionLedgerShadow        = "shadow"
	runtimeActionLedgerAuthoritative = "authoritative"
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
	return runtimeFeatureModes{
		ContextCompiler: runtimeModeEnv("AI_RUNTIME_CONTEXT_COMPILER", runtimeContextCompilerLegacy, runtimeContextCompilerLegacy, runtimeContextCompilerShadow, runtimeContextCompilerV2),
		IntentContract:  runtimeModeEnv("AI_RUNTIME_INTENT_CONTRACT", runtimeIntentContractV1, runtimeIntentContractV1, runtimeIntentContractV2),
		ReplyContract:   runtimeModeEnv("AI_RUNTIME_REPLY_CONTRACT", runtimeReplyContractLegacy, runtimeReplyContractLegacy, runtimeReplyContractV2),
		Validator:       runtimeModeEnv("AI_RUNTIME_VALIDATOR", runtimeValidatorLegacy, runtimeValidatorLegacy, runtimeValidatorV2),
		ActionLedger:    runtimeModeEnv("AI_RUNTIME_ACTION_LEDGER", runtimeActionLedgerShadow, runtimeActionLedgerShadow, runtimeActionLedgerAuthoritative),
	}
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
