package executor

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"
)

func TestRuntimeStructuredOutputContractsAreInvocationScoped(t *testing.T) {
	base := modelconfig.Config{ModelName: "deepseek-v4-flash", APIMode: "responses"}
	intentConfig, err := withRuntimeIntentStructuredOutput(base)
	if err != nil {
		t.Fatal(err)
	}
	replyConfig, err := withRuntimeReplyStructuredOutput(base)
	if err != nil {
		t.Fatal(err)
	}
	if base.StructuredOutput != nil {
		t.Fatal("base model slot must remain unconstrained for plain-text callers")
	}
	assertRuntimeStructuredOutput(t, intentConfig, "intent_tasks_v2", contracts.SchemaIntentTasksV2)
	assertRuntimeStructuredOutput(t, replyConfig, "reply_output_v2", contracts.SchemaReplyOutputV2)
}

func TestRuntimeIntentStructuredOutputAcceptsInvocationSchema(t *testing.T) {
	base := modelconfig.Config{ModelName: "deepseek-v4-flash", APIMode: "responses"}
	runtimeSchema, _, err := contracts.BuildRuntimeIntentSchema(contracts.MustSchema(contracts.SchemaIntentTasksV2), nil)
	if err == nil {
		t.Fatal("empty published intent catalog must fail")
	}
	runtimeSchema, _, err = contracts.BuildRuntimeIntentSchema(contracts.MustSchema(contracts.SchemaIntentTasksV2), []models.ReplyIntentConfig{{Code: "interaction", Status: enums.StatusOk}})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := withRuntimeIntentStructuredOutputSchema(base, runtimeSchema)
	if err != nil {
		t.Fatal(err)
	}
	if configured.StructuredOutput == nil || string(configured.StructuredOutput.JSONSchema) != string(runtimeSchema) {
		t.Fatal("invocation schema was not attached")
	}
}

func assertRuntimeStructuredOutput(t *testing.T, config modelconfig.Config, name, schemaName string) {
	t.Helper()
	if config.StructuredOutput == nil || config.StructuredOutput.Name != name || !config.StructuredOutput.Strict {
		t.Fatalf("unexpected structured output contract: %#v", config.StructuredOutput)
	}
	var got any
	var want any
	if err := json.Unmarshal(config.StructuredOutput.JSONSchema, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contracts.MustSchema(schemaName), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("structured output schema mismatch: got=%#v want=%#v", got, want)
	}
}

func TestParseRuntimeReplyOutputV2RequiresStrictJSONOnly(t *testing.T) {
	valid := `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["task_1"],"content":"酒店提供免费停车。"}]}`
	if parsed, err := parseRuntimeReplyOutputV2(valid); err != nil || len(parsed.Parts) != 1 {
		t.Fatalf("parse valid reply output=%+v err=%v", parsed, err)
	}
	for name, raw := range map[string]string{
		"markdown":           "```json\n" + valid + "\n```",
		"extra field":        `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["task_1"],"content":"ok"}],"reason":"extra"}`,
		"server-owned refs":  `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["task_1"],"content":"ok","evidenceRefs":[],"actionRefs":[]}]}`,
		"duplicate task key": `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["task_1","task_1"],"content":"ok"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRuntimeReplyOutputV2(raw); err == nil {
				t.Fatalf("parseRuntimeReplyOutputV2(%s) error=nil", name)
			}
		})
	}
}

func TestCompleteRuntimeGenerationPreservesSafeDegradedOutcome(t *testing.T) {
	summary := &RunResult{
		Status: string(GenerationOutcomeSafeDegraded), GenerationOutcome: GenerationOutcomeSafeDegraded,
		ReplyText:  "当前门店地址是：合肥市包河区水阳江路392号。",
		ReplyParts: []contracts.ReplyPartV2{{TaskKeys: []string{"address"}, Content: "当前门店地址是：合肥市包河区水阳江路392号。"}},
	}
	collector := callbacks.NewRuntimeTraceCollector()
	collector.Data.Pipeline.Generate.Status = string(GenerationOutcomeSafeDegraded)
	collector.Data.Pipeline.Generate.Mode = string(GenerationOutcomeSafeDegraded)
	collector.Data.Pipeline.Generate.Reason = "generation failed; only authoritative scalar facts were allowed through safe degraded mode"
	collector.Data.Pipeline.Generate.InitialErrorCode = "json_root_not_object"
	collector.Data.Pipeline.Generate.RepairErrorCode = "missing_required_task"
	collector.Data.Pipeline.Validate.Status = "passed"
	collector.Data.Pipeline.Validate.Reason = "safe degraded scalar facts passed deterministic validation"
	if err := completeRuntimeGeneration(summary, collector, "test-model", time.Now()); err != nil {
		t.Fatal(err)
	}
	if summary.Status != string(GenerationOutcomeSafeDegraded) || summary.GenerationOutcome != GenerationOutcomeSafeDegraded ||
		collector.Data.Pipeline.Generate.Status != string(GenerationOutcomeSafeDegraded) ||
		collector.Data.Pipeline.Generate.Mode != string(GenerationOutcomeSafeDegraded) ||
		collector.Data.Output.FinishReason != string(GenerationOutcomeSafeDegraded) ||
		collector.Data.Output.GenerationOutcome != string(GenerationOutcomeSafeDegraded) {
		t.Fatalf("safe degraded outcome was overwritten: summary=%#v generate=%#v output=%#v", summary, collector.Data.Pipeline.Generate, collector.Data.Output)
	}
	if collector.Data.Pipeline.Generate.InitialErrorCode == "" || collector.Data.Pipeline.Generate.RepairErrorCode == "" {
		t.Fatalf("safe degraded failure evidence was lost: %#v", collector.Data.Pipeline.Generate)
	}
	if collector.Data.Error.Message == "" || collector.Data.Error.Stage != "generate_safe_degraded" {
		t.Fatalf("safe degraded mode must preserve a diagnosable generation error: %#v", collector.Data.Error)
	}
}

func TestReplyValidatorV2AddsSafetyWithoutChangingLegacyValidation(t *testing.T) {
	input := validReplyValidationInputForTest("定位已经发送给你。")
	legacy := NewReplyValidatorForMode(runtimeValidatorLegacy).Validate(input)
	if legacy.Status != "passed" {
		t.Fatalf("legacy validator unexpectedly rejected valid legacy output: %+v", legacy)
	}
	v2 := NewReplyValidatorForMode(runtimeValidatorV2).Validate(input)
	if v2.Status != "rejected" || v2.Checks.Safety != "failed" || !validationHasCode(v2, "unsupported_action_claim") {
		t.Fatalf("v2 validator did not reject unsupported action claim: %+v", v2)
	}
}

func TestReplyValidatorV2RejectsInternalIdentifiers(t *testing.T) {
	input := validReplyValidationInputForTest("内部 task_1 已处理。")
	result := NewReplyValidatorForMode(runtimeValidatorV2).Validate(input)
	if result.Status != "rejected" || !validationHasCode(result, "internal_identifier_exposed") {
		t.Fatalf("internal identifier was not rejected: %+v", result)
	}
}

func validReplyValidationInputForTest(content string) ReplyValidationInput {
	return ReplyValidationInput{
		Output: contracts.ReplyOutputV2{
			SchemaVersion: contracts.ReplyOutputV2SchemaVersion,
			Parts:         []contracts.ReplyPartV2{{TaskKeys: []string{"task_1"}, Content: content, EvidenceRefs: []string{}, ActionRefs: []string{}}},
		},
		Plan: contracts.ReplyPlanV2{
			SchemaVersion: contracts.ReplyPlanV2SchemaVersion, TurnVersion: 1, ShouldGenerate: true,
			Tasks: []contracts.ReplyPlanTaskV2{{
				TaskKey: "task_1", Sequence: 1, Intent: "hotel_info", SubIntent: "parking",
				Objective: "回答停车信息", OutputMode: "text",
				Knowledge:    contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed", ReasonCode: "test"},
				EvidenceRefs: []string{}, ActionRefs: []string{}, Constraints: []string{"no_internal_terms"},
			}},
			GlobalConstraints: contracts.ReplyPlanGlobalConstraints{MaxReplyParts: 3, MaxQuestionsPerPart: 2, ForbiddenClaims: []string{"unprepared_resource_sent"}},
		},
		Evidence: contracts.EvidenceBundleV1{
			SchemaVersion: contracts.EvidenceBundleV1SchemaVersion, ScopeFingerprint: "scope-test", RetrievalStatus: "not_needed",
			Items: []contracts.EvidenceItemV1{}, Resources: []contracts.EvidenceResourceV1{},
		},
		ActionLedger: contracts.ActionLedgerV1{
			SchemaVersion: contracts.ActionLedgerV1SchemaVersion, TurnVersion: 1, Actions: []contracts.ActionLedgerItemV1{},
		},
	}
}

func validationHasCode(result contracts.ValidationResultV1, code string) bool {
	for _, issue := range result.Errors {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestParseRuntimeReplyOutputV2LooseExtraction(t *testing.T) {
	raw := "好的，这是给客人的回复：\n{\"schemaVersion\":\"reply_output.v2\",\"parts\":[{\"taskKeys\":[\"t1\"],\"content\":\"行李可以免费寄存在一楼前台寄存柜。\"}]}\n以上。"
	parsed, err := parseRuntimeReplyOutputV2(extractLooseJSONObject(raw))
	if err != nil {
		t.Fatalf("loose extraction must recover JSON object: %v", err)
	}
	if len(parsed.Parts) != 1 || parsed.Parts[0].Content != "行李可以免费寄存在一楼前台寄存柜。" {
		t.Fatalf("unexpected parsed parts: %#v", parsed.Parts)
	}
}
