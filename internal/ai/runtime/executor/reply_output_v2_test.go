package executor

import (
	"encoding/json"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
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
	if string(config.StructuredOutput.JSONSchema) != string(contracts.MustSchema(schemaName)) {
		t.Fatalf("structured output schema mismatch: got=%#v want=%#v", got, want)
	}
}

func TestParseRuntimeReplyOutputV2RequiresStrictJSONOnly(t *testing.T) {
	valid := `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["task_1"],"content":"酒店提供免费停车。","evidenceRefs":["K1"],"actionRefs":[]}]}`
	if parsed, err := parseRuntimeReplyOutputV2(valid); err != nil || len(parsed.Parts) != 1 {
		t.Fatalf("parse valid reply output=%+v err=%v", parsed, err)
	}
	for name, raw := range map[string]string{
		"markdown":           "```json\n" + valid + "\n```",
		"extra field":        `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["task_1"],"content":"ok","evidenceRefs":[],"actionRefs":[]}],"reason":"extra"}`,
		"duplicate task key": `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["task_1","task_1"],"content":"ok","evidenceRefs":[],"actionRefs":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRuntimeReplyOutputV2(raw); err == nil {
				t.Fatalf("parseRuntimeReplyOutputV2(%s) error=nil", name)
			}
		})
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
