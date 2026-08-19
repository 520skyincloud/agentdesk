package executor

import (
	"fmt"
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
)

type runtimeFeatureModes struct {
	ContextCompiler string
	IntentContract  string
	ReplyContract   string
	Validator       string
	ActionLedger    string
}

func resolveRuntimeFeatureModes(_ RunInput) runtimeFeatureModes {
	// Production has exactly one online contract set. Legacy and V3 code stays
	// available for historical data readers and focused tests, but environment
	// flags and allowlists can no longer split live conversations across paths.
	return runtimeFeatureModes{
		ContextCompiler: runtimeContextCompilerV2,
		IntentContract:  runtimeIntentContractV2,
		ReplyContract:   runtimeReplyContractV2,
		Validator:       runtimeValidatorV2,
		ActionLedger:    runtimeActionLedgerAuthoritative,
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
	if modes.IntentContract == runtimeIntentContractV3 && modes.ContextCompiler != runtimeContextCompilerV2 {
		return fmt.Errorf("AI runtime intent contract v3 requires context compiler v2")
	}
	return nil
}
