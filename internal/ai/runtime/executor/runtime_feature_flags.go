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
	runtimeReplyContractV3     = "v3"

	runtimeValidatorLegacy = "legacy"
	runtimeValidatorV2     = "v2"
	runtimeValidatorV3     = "v3"

	runtimeActionLedgerShadow        = "shadow"
	runtimeActionLedgerAuthoritative = "authoritative"

	// AI_RUNTIME_MULTIMODAL_V3 is retained as a compatibility variable because
	// older deployments still carry it. It must not silently enable the strict
	// span/group protocol. That protocol is opt-in through the explicit
	// experimental switch below.
	runtimeMultimodalV3StrictEnv = "AI_RUNTIME_MULTIMODAL_V3_STRICT"
)

type runtimeFeatureModes struct {
	ContextCompiler string
	IntentContract  string
	ReplyContract   string
	Validator       string
	ActionLedger    string
}

func runtimeTraceIdentity(modes runtimeFeatureModes) (string, string) {
	version := "v2"
	if modes.IntentContract == runtimeIntentContractV3 || modes.ReplyContract == runtimeReplyContractV3 || modes.Validator == runtimeValidatorV3 {
		version = "v3"
	}
	mode := strings.Join([]string{
		"context=" + modes.ContextCompiler,
		"intent=" + modes.IntentContract,
		"reply=" + modes.ReplyContract,
		"validator=" + modes.Validator,
		"ledger=" + modes.ActionLedger,
	}, ";")
	return version, mode
}

func resolveRuntimeFeatureModes(req RunInput) runtimeFeatureModes {
	if !runtimeV2ScopeEnabled(req) {
		return legacyRuntimeFeatureModes()
	}
	// V2 已是默认主链：未显式设置环境变量时，全部落到 v2/authoritative。
	// legacyRuntimeFeatureModes() 仅在 runtimeV2ScopeEnabled 白名单排除时作为回退手段保留。
	modes := runtimeFeatureModes{
		ContextCompiler: runtimeModeEnv("AI_RUNTIME_CONTEXT_COMPILER", runtimeContextCompilerV2, runtimeContextCompilerLegacy, runtimeContextCompilerShadow, runtimeContextCompilerV2),
		// V3 is deliberately excluded from ordinary environment overrides. A
		// stale production variable must not re-enable the fragile span/group
		// protocol after this rollout.
		IntentContract: runtimeModeEnv("AI_RUNTIME_INTENT_CONTRACT", runtimeIntentContractV2, runtimeIntentContractV1, runtimeIntentContractV2),
		ReplyContract:  runtimeModeEnv("AI_RUNTIME_REPLY_CONTRACT", runtimeReplyContractV2, runtimeReplyContractLegacy, runtimeReplyContractV2),
		Validator:      runtimeModeEnv("AI_RUNTIME_VALIDATOR", runtimeValidatorV2, runtimeValidatorLegacy, runtimeValidatorV2),
		ActionLedger:   runtimeModeEnv("AI_RUNTIME_ACTION_LEDGER", runtimeActionLedgerAuthoritative, runtimeActionLedgerShadow, runtimeActionLedgerAuthoritative),
	}
	// Strict V3 remains available for isolated experiments, but it is no longer
	// activated by the old production flag. The normal serving path keeps the
	// V2 model-facing contracts and the V2 task ledger protections together.
	if multimodalV3Enabled() {
		modes.IntentContract = runtimeIntentContractV3
		modes.ContextCompiler = runtimeContextCompilerV2
		modes.ReplyContract = runtimeReplyContractV3
		modes.Validator = runtimeValidatorV3
		modes.ActionLedger = runtimeActionLedgerAuthoritative
	}
	return modes
}

// multimodalV3Enabled is intentionally an explicit experimental opt-in. The
// former AI_RUNTIME_MULTIMODAL_V3 flag is ignored for activation so an old
// server environment cannot put customer traffic back on the strict path.
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
	if modes.ReplyContract == runtimeReplyContractV3 && modes.ContextCompiler != runtimeContextCompilerV2 {
		return fmt.Errorf("AI runtime reply contract v3 requires context compiler v2")
	}
	if modes.Validator == runtimeValidatorV3 && modes.ReplyContract != runtimeReplyContractV3 {
		return fmt.Errorf("AI runtime validator v3 requires reply contract v3")
	}
	if modes.ActionLedger == runtimeActionLedgerAuthoritative &&
		modes.ReplyContract != runtimeReplyContractV2 && modes.ReplyContract != runtimeReplyContractV3 {
		return fmt.Errorf("AI runtime authoritative action ledger requires reply contract v2 or v3")
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
