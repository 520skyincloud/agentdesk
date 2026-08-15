package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/models"
)

func v3EnvelopeFixture() contextcompiler.TurnInputEnvelope {
	return contextcompiler.BuildTurnInputEnvelope(contextcompiler.EnvelopeScope{TenantID: 1, StoreID: 1, ConversationID: 2}, []models.Message{
		{ID: 1399, SenderType: "customer", MessageType: "voice", Content: "",
			Payload: `{"mediaUnderstandingStatus":"understood","mediaText":"有没有刮胡刀，还有咖啡和其他用品？"}`},
		{ID: 1400, SenderType: "customer", MessageType: "voice", Content: "",
			Payload: `{"mediaUnderstandingStatus":"understood","mediaText":"床单想换怎么办"}`},
	})
}

// 契约 10.7：每个非空 URef 必须在 utteranceCoverage 恰好出现一次。
func TestV3UtteranceCoverageSetEquality(t *testing.T) {
	envelope := v3EnvelopeFixture()
	good := []intentCoverageItemWire{
		{SourceRef: "U1", Status: "covered", TaskSequences: []int{1}},
		{SourceRef: "U2", Status: "covered", TaskSequences: []int{2}},
	}
	if issues := validateV3UtteranceCoverage(envelope, good); len(issues) != 0 {
		t.Fatalf("valid coverage rejected: %v", issues)
	}
	missingU2 := good[:1]
	if issues := validateV3UtteranceCoverage(envelope, missingU2); len(issues) == 0 {
		t.Fatal("missing U2 coverage must be rejected (1399/1400 串线场景)")
	}
	dup := append(append([]intentCoverageItemWire{}, good...), intentCoverageItemWire{SourceRef: "U1", Status: "ignored", IgnoredReason: "policy_no_reply"})
	if issues := validateV3UtteranceCoverage(envelope, dup); len(issues) == 0 {
		t.Fatal("duplicate coverage must be rejected")
	}
}

// 契约 10.5：协议失败时降级为逐 utterance 全文 QuestionUnit，不转人工。
func TestV3DegradePerUtterance(t *testing.T) {
	envelope := v3EnvelopeFixture()
	trace, err := degradeIntentV3(envelope, intentTasksV3Wire{}, nil, nil)
	if err != nil {
		t.Fatalf("degrade must not fail: %v", err)
	}
	if !strings.Contains(trace.Reason, "degraded_single_task") {
		t.Fatalf("degraded trace reason: %q", trace.Reason)
	}
	if len(trace.IntentTasks) != 2 {
		t.Fatalf("expected one task per unique utterance, got %d", len(trace.IntentTasks))
	}
}

// 契约 2.1：成组开关只允许整组（Intent V3 + Context V2）。
func TestV3GroupFlagForcesIntentContract(t *testing.T) {
	t.Setenv("AI_RUNTIME_MULTIMODAL_V3", "on")
	t.Setenv("AI_RUNTIME_INTENT_CONTRACT", "v2")
	resolved := resolveRuntimeFeatureModes(RunInput{})
	if resolved.IntentContract != runtimeIntentContractV3 {
		t.Fatalf("group flag must force intent v3, got %s", resolved.IntentContract)
	}
	if resolved.ContextCompiler != runtimeContextCompilerV2 {
		t.Fatalf("group flag must force context v2, got %s", resolved.ContextCompiler)
	}
}
