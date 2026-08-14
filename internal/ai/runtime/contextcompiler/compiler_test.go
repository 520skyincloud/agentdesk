package contextcompiler

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"
)

func TestCompilerGenerateHasHardBudgetAndFixedOrder(t *testing.T) {
	input := compilerFixtureInput(CompileStageGenerate)
	input.RecentHistory = buildCompilerHistory(10)
	input.ReplyTagText = "称呼客人为李先生，语气简洁。"
	input.Memory = &models.ConversationSessionSummary{StableFacts: strings.Repeat("稳定事实。", 100), CustomerPreferences: "喜静"}
	result, err := New(nil).Compile(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.EstimatedInput > result.AvailableInput {
		t.Fatalf("estimated=%d available=%d", result.EstimatedInput, result.AvailableInput)
	}
	if len(result.Messages) < 4 || string(result.Messages[0].Role) != "system" || string(result.Messages[1].Role) != "system" || string(result.Messages[len(result.Messages)-1].Role) != "user" {
		t.Fatalf("message order=%+v", result.Messages)
	}
	if got := result.Messages[len(result.Messages)-1].Content; got != input.CurrentMessages[0].Content {
		t.Fatalf("current message changed: %q", got)
	}
	if !strings.Contains(result.Messages[0].Content, "reply_output.v2") {
		t.Fatalf("stable policy lacks reply contract: %q", result.Messages[0].Content)
	}
	var snapshot contracts.RuntimeContextSnapshotV1
	// P1 分层块头：snapshot 消息带 [RUNTIME_CONTRACT] 前缀，JSON 从第一个 '{' 开始。
	snapshotJSON := result.Messages[1].Content
	if idx := strings.Index(snapshotJSON, "{"); idx > 0 {
		snapshotJSON = snapshotJSON[idx:]
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
		t.Fatalf("runtime snapshot: %v", err)
	}
	if snapshot.SchemaVersion != contracts.RuntimeContextSnapshotV1SchemaVersion || len(snapshot.Tasks) != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	for _, message := range result.Messages {
		if strings.Contains(message.Content, "[历史消息]") || strings.Contains(message.Content, "[AI客服]") || strings.Contains(message.Content, "API_KEY") {
			t.Fatalf("compiled message leaked internal label/secret: %q", message.Content)
		}
	}
	if len(result.Fingerprint) != 64 || result.Estimator != "conservative" {
		t.Fatalf("fingerprint=%q estimator=%q", result.Fingerprint, result.Estimator)
	}
}

func TestCompilerGenerateAllowsMandatoryPolicyBeyondSoftCategoryShare(t *testing.T) {
	input := compilerFixtureInput(CompileStageGenerate)
	input.StablePolicy = strings.Repeat("规则", 600)

	result, err := New(nil).Compile(t.Context(), input)
	if err != nil {
		t.Fatalf("mandatory policy that fits the total budget must compile: %v", err)
	}
	softShare := min(1200, result.AvailableInput*15/100)
	if result.CategoryTokens["stable_policy"] <= softShare {
		t.Fatalf("fixture did not cross soft share: stable=%d share=%d", result.CategoryTokens["stable_policy"], softShare)
	}
	if result.EstimatedInput > result.AvailableInput {
		t.Fatalf("estimated=%d available=%d", result.EstimatedInput, result.AvailableInput)
	}
}

func TestCompilerGenerateStillRejectsMandatoryPromptBeyondTotalBudget(t *testing.T) {
	input := compilerFixtureInput(CompileStageGenerate)
	input.StablePolicy = strings.Repeat("规则", 5000)

	_, err := New(nil).Compile(t.Context(), input)
	if !errors.Is(err, ErrMandatoryContextOverflow) {
		t.Fatalf("error=%v want mandatory overflow", err)
	}
}

func TestCompilerIntentKeepsAtMostFourCompleteTurns(t *testing.T) {
	input := compilerFixtureInput(CompileStageIntent)
	input.RecentHistory = buildCompilerHistory(7)
	result, err := New(nil).Compile(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	assistantCount := 0
	for _, message := range result.Messages {
		if string(message.Role) == "assistant" {
			assistantCount++
		}
	}
	if assistantCount > 4 {
		t.Fatalf("assistant history count=%d want<=4", assistantCount)
	}
	if !strings.Contains(result.Messages[0].Content, "intent_tasks.v2") {
		t.Fatalf("intent contract missing")
	}
}

func TestCompilerRejectsMissingRequiredEvidence(t *testing.T) {
	input := compilerFixtureInput(CompileStageGenerate)
	input.Evidence.Items = nil
	_, err := New(nil).Compile(t.Context(), input)
	if !errors.Is(err, ErrRequiredEvidenceOverflow) {
		t.Fatalf("error=%v want required evidence overflow", err)
	}
}

func TestCompilerRejectsCrossScopeMessages(t *testing.T) {
	input := compilerFixtureInput(CompileStageGenerate)
	input.CurrentMessages[0].TenantID = 99
	_, err := New(nil).Compile(t.Context(), input)
	if !errors.Is(err, ErrRuntimeScopeMismatch) {
		t.Fatalf("error=%v want scope mismatch", err)
	}
}

func TestCompilerFingerprintIsDeterministicAndRevisionSensitive(t *testing.T) {
	input := compilerFixtureInput(CompileStageGenerate)
	first, err := New(nil).Compile(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(nil).Compile(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("fingerprint not deterministic")
	}
	input.Model.CredentialRevision++
	third, err := New(nil).Compile(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == third.Fingerprint {
		t.Fatalf("credential revision must change fingerprint")
	}
}

func compilerFixtureInput(stage CompileStage) CompileInput {
	now := time.Now().UTC()
	message := historyFixtureMessage(100, 100, enums.IMSenderTypeCustomer, "停车场入口在哪里？", now)
	model := services.ModelCallConfig{
		TenantID: 1, StoreID: 3, StoreStaffBindingID: 4, ProfileRevision: 5,
		CredentialRevision: 6, ModelName: "deepseek-v4-flash", MaxContextTokens: 8192, MaxOutputTokens: 512,
	}
	instance := models.WxWorkProtocolInstance{ID: 5, TenantID: 1, StoreID: 3, StoreStaffBindingID: 4, ContextMaxTokens: 8000}
	plan := &contracts.ReplyPlanV2{
		SchemaVersion: contracts.ReplyPlanV2SchemaVersion, TurnVersion: 2, ShouldGenerate: true,
		Tasks: []contracts.ReplyPlanTaskV2{{
			TaskKey: "task_parking", Sequence: 1, Intent: "hotel_info", SubIntent: "parking",
			Objective: "回答停车场入口", OutputMode: "text",
			Knowledge:    contracts.ReplyPlanKnowledge{Policy: "required", Status: "has_context"},
			EvidenceRefs: []string{"K1"}, ActionRefs: []string{}, Constraints: []string{"no_unsupported_facts"},
		}},
		GlobalConstraints: contracts.ReplyPlanGlobalConstraints{MaxReplyParts: 3, MaxQuestionsPerPart: 4, ForbiddenClaims: []string{"unprepared_resource_sent"}},
	}
	evidence := &contracts.EvidenceBundleV1{
		SchemaVersion: contracts.EvidenceBundleV1SchemaVersion, ScopeFingerprint: strings.Repeat("a", 64), RetrievalStatus: "has_context",
		Items: []contracts.EvidenceItemV1{{
			Ref: "K1", SourceType: "fastgpt", TaskKeys: []string{"task_parking"}, Title: "停车",
			Content: "停车场入口位于酒店东侧。", Score: 0.95, Answerability: "supporting", ResourceRefs: []string{},
		}}, Resources: []contracts.EvidenceResourceV1{},
	}
	return CompileInput{
		Stage: stage,
		Scope: RuntimeScope{TenantID: 1, StoreID: 3, ConversationID: 2, SessionNo: 1, WxWorkInstanceID: 5, StoreStaffBindingID: 4, TurnID: 8, TurnVersion: 2, JobID: 9},
		Model: model, Instance: instance, Agent: models.AIAgent{SystemPrompt: "你是酒店客服。"},
		CurrentMessages: []models.Message{message}, DialogueState: &contracts.DialogueStateSnapshotV1{
			SchemaVersion: contracts.DialogueStateSnapshotV1SchemaVersion, ConversationID: 2, SessionNo: 1, Revision: 3,
			Focus:         contracts.DialogueStateFocus{Topic: "停车", RelationToPrior: "new_topic", ActiveTaskKeys: []string{"task_parking"}},
			ResolvedTasks: []contracts.DialogueStateResolvedTask{}, OpenTasks: []contracts.DialogueStateOpenTask{}, SessionFacts: []contracts.DialogueStateSessionFact{}, UpdatedAt: now,
		},
		ReplyPlan: plan, Evidence: evidence, StablePolicy: "你是酒店客服，只根据当前门店证据回答。",
		IntentInstruction: "你是酒店 IntentDetect，只拆分当前问题。", IntentProfileRevision: 11,
	}
}

func buildCompilerHistory(turnCount int) []models.Message {
	now := time.Now().UTC()
	ret := make([]models.Message, 0, turnCount*2)
	for i := 0; i < turnCount; i++ {
		customerID := int64(i*2 + 1)
		assistantID := customerID + 1
		ret = append(ret,
			historyFixtureMessage(customerID, customerID, enums.IMSenderTypeCustomer, "历史问题"+strings.Repeat("问", i+1), now),
			historyFixtureMessage(assistantID, assistantID, enums.IMSenderTypeAI, "历史回答"+strings.Repeat("答", i+1), now),
		)
	}
	return ret
}

func TestBuildGeneratePolicyDoesNotDuplicatePersonaPrompt(t *testing.T) {
	// SystemPrompt 已合并过 personaPrompt（BuildRuntimeAIAgent 行为），
	// buildGeneratePolicy 不应再单独追加一次 Instance.PersonaPrompt。
	persona := "专属人设：说话简短。"
	policy := buildGeneratePolicy(CompileInput{
		Agent:                 models.AIAgent{SystemPrompt: "你是酒店客服。\n\n员工号专属人格提示词：\n" + persona},
		Instance:              models.WxWorkProtocolInstance{PersonaPrompt: persona},
		GenerationInstruction: "只回答知识库有的内容。",
		ReplyContract:         ReplyContractV2,
	})
	if count := strings.Count(policy, persona); count != 1 {
		t.Fatalf("persona prompt duplicated %d times, want 1:\n%s", count, policy)
	}
}

func TestBuildGeneratePolicyFallsBackToPersonaWhenSystemPromptMissing(t *testing.T) {
	persona := "专属人设：说话简短。"
	policy := buildGeneratePolicy(CompileInput{
		Agent:                 models.AIAgent{},
		Instance:              models.WxWorkProtocolInstance{PersonaPrompt: persona},
		GenerationInstruction: "只回答知识库有的内容。",
		ReplyContract:         ReplyContractV2,
	})
	if !strings.Contains(policy, persona) {
		t.Fatalf("expected persona fallback when system prompt missing, got:\n%s", policy)
	}
}
