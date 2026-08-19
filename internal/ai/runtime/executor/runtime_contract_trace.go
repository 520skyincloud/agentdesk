package executor

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/modelconfig"
)

const runtimePromptVersionStableV2 = "stable_v2.2026-08-19"

var (
	runtimeBuildOnce sync.Once
	runtimeBuildName string
)

func buildRuntimeContractTrace(req RunInput, modes runtimeFeatureModes) (callbacks.RuntimeContractTraceData, error) {
	if modes.IntentContract != runtimeIntentContractV2 || modes.ReplyContract != runtimeReplyContractV2 ||
		modes.ContextCompiler != runtimeContextCompilerV2 || modes.Validator != runtimeValidatorV2 ||
		modes.ActionLedger != runtimeActionLedgerAuthoritative {
		return callbacks.RuntimeContractTraceData{}, fmt.Errorf("online runtime contract set is not stable_v2")
	}
	configs := loadEnabledIntentConfigs(resolveRuntimeIntentScope(req))
	intentSchema, _, err := contracts.BuildRuntimeIntentSchema(contracts.MustSchema(contracts.SchemaIntentTasksV2), configs)
	if err != nil {
		return callbacks.RuntimeContractTraceData{}, err
	}
	intentPrepared, err := modelconfig.PrepareResponsesJSONSchema(intentSchema)
	if err != nil {
		return callbacks.RuntimeContractTraceData{}, err
	}
	replyPrepared, err := modelconfig.PrepareResponsesJSONSchema(contracts.MustSchema(contracts.SchemaReplyOutputV2))
	if err != nil {
		return callbacks.RuntimeContractTraceData{}, err
	}
	profile := strings.TrimSpace(req.ModelConfig.ProfileCode)
	if req.ModelConfig.ProfileRevision > 0 {
		profile = fmt.Sprintf("%s@%d", profile, req.ModelConfig.ProfileRevision)
	}
	if strings.Trim(profile, "@0123456789") == "" {
		profile = strings.TrimSpace(req.ModelConfig.ModelName)
	}
	return callbacks.RuntimeContractTraceData{
		ContractSet:      contracts.RuntimeContractSetStableV2,
		IntentSchemaHash: intentPrepared.Fingerprint,
		ReplySchemaHash:  replyPrepared.Fingerprint,
		PromptVersion:    runtimePromptVersionStableV2,
		ModelProfile:     profile,
		RuntimeBuild:     runtimeBuildIdentity(),
	}, nil
}

func runtimeBuildIdentity() string {
	runtimeBuildOnce.Do(func() {
		for _, name := range []string{"AGENT_DESK_RUNTIME_BUILD", "REVISION", "GIT_COMMIT"} {
			if value := strings.TrimSpace(os.Getenv(name)); value != "" {
				runtimeBuildName = value
				return
			}
		}
		for _, path := range []string{"REVISION", "/opt/agentdesk/current/REVISION"} {
			if raw, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(raw)) != "" {
				runtimeBuildName = strings.TrimSpace(string(raw))
				return
			}
		}
		runtimeBuildName = "development"
	})
	return runtimeBuildName
}
