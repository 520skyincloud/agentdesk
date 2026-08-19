package executor

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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

func TestReplyValidatorV2EnforcesMaxQuestionsPerPart(t *testing.T) {
	input := validReplyValidationInputForTest("停车场从东侧进。早餐7点开始。WiFi密码见房内提示。")
	input.Plan.GlobalConstraints.MaxQuestionsPerPart = 2
	input.Plan.Tasks = append(input.Plan.Tasks,
		contracts.ReplyPlanTaskV2{TaskKey: "task_2", Sequence: 2, Intent: "hotel_info", SubIntent: "breakfast", Objective: "早餐几点", OutputMode: "text", Knowledge: contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed"}, EvidenceRefs: []string{}, ActionRefs: []string{}, Constraints: []string{}},
		contracts.ReplyPlanTaskV2{TaskKey: "task_3", Sequence: 3, Intent: "hotel_info", SubIntent: "network_wifi", Objective: "WiFi密码", OutputMode: "text", Knowledge: contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed"}, EvidenceRefs: []string{}, ActionRefs: []string{}, Constraints: []string{}},
	)
	input.Output.Parts[0].TaskKeys = []string{"task_1", "task_2", "task_3"}
	result := NewReplyValidatorForMode(runtimeValidatorV2).Validate(input)
	if result.Status != "repairable_protocol_error" || !validationHasCode(result, "too_many_questions_in_part") {
		t.Fatalf("question limit was not enforced: %+v", result)
	}
}

func TestReplyValidatorV2RejectsShortAnswerBoundToUnrelatedTasks(t *testing.T) {
	input := validReplyValidationInputForTest("停车场从东侧进。")
	input.Plan.Tasks = append(input.Plan.Tasks, contracts.ReplyPlanTaskV2{
		TaskKey: "task_wifi", Sequence: 2, Intent: "hotel_info", SubIntent: "network_wifi", Objective: "WiFi密码是多少", OutputMode: "text",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed"}, EvidenceRefs: []string{}, ActionRefs: []string{}, Constraints: []string{},
	})
	input.Output.Parts[0].TaskKeys = []string{"task_1", "task_wifi"}
	result := NewReplyValidatorForMode(runtimeValidatorV2).Validate(input)
	if result.Status != "repairable_protocol_error" || !validationHasCode(result, "task_answer_obligation_missing") {
		t.Fatalf("unrelated task keys were accepted: %+v", result)
	}
}

func TestReplyValidatorV2RejectsMultipleSentencesThatStillMissOneTask(t *testing.T) {
	input := validReplyValidationInputForTest("停车场从东侧进。入口就在大门旁。")
	input.Plan.Tasks = append(input.Plan.Tasks, contracts.ReplyPlanTaskV2{
		TaskKey: "task_wifi", Sequence: 2, Intent: "hotel_info", SubIntent: "network_wifi", Objective: "WiFi密码是多少", OutputMode: "text",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed"}, EvidenceRefs: []string{}, ActionRefs: []string{}, Constraints: []string{},
	})
	input.Output.Parts[0].TaskKeys = []string{"task_1", "task_wifi"}
	result := NewReplyValidatorForMode(runtimeValidatorV2).Validate(input)
	if result.Status != "repairable_protocol_error" || !validationHasCode(result, "task_answer_obligation_missing") {
		t.Fatalf("multiple sentences about one topic were accepted as two answers: %+v", result)
	}
}

func TestReplyValidatorV2AcceptsOrderedImplicitAnswerUnits(t *testing.T) {
	input := validReplyValidationInputForTest("7点开始，12点前退房。")
	input.Plan.Tasks[0].SubIntent = "breakfast"
	input.Plan.Tasks[0].Objective = "早餐几点开始"
	input.Plan.Tasks = append(input.Plan.Tasks, contracts.ReplyPlanTaskV2{
		TaskKey: "task_checkout", Sequence: 2, Intent: "hotel_info", SubIntent: "checkout_process", Objective: "几点退房", OutputMode: "text",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed"}, EvidenceRefs: []string{}, ActionRefs: []string{}, Constraints: []string{},
	})
	input.Output.Parts[0].TaskKeys = []string{"task_1", "task_checkout"}
	result := NewReplyValidatorForMode(runtimeValidatorV2).Validate(input)
	if result.Status != "passed" {
		t.Fatalf("ordered implicit answer units should remain valid: %+v", result)
	}
}

func TestReplyValidatorV2ChecksEverySameTopicTask(t *testing.T) {
	input := validReplyValidationInputForTest("牙刷在自取柜。")
	input.Plan.Tasks[0].SubIntent = "supplies_self_help"
	input.Plan.Tasks[0].Objective = "牙刷在哪里"
	input.Plan.Tasks = append(input.Plan.Tasks, contracts.ReplyPlanTaskV2{
		TaskKey: "task_slippers", Sequence: 2, Intent: "hotel_info", SubIntent: "supplies_self_help", Objective: "拖鞋在哪里", OutputMode: "text",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed"}, EvidenceRefs: []string{}, ActionRefs: []string{}, Constraints: []string{},
	})
	input.Output.Parts[0].TaskKeys = []string{"task_1", "task_slippers"}
	result := NewReplyValidatorForMode(runtimeValidatorV2).Validate(input)
	if result.Status != "repairable_protocol_error" || !validationHasCode(result, "task_answer_obligation_missing") {
		t.Fatalf("same-topic tasks must each have an answer: %+v", result)
	}

	input.Output.Parts[0].Content = "牙刷和拖鞋都在自取柜。"
	result = NewReplyValidatorForMode(runtimeValidatorV2).Validate(input)
	if result.Status != "passed" {
		t.Fatalf("one concise sentence may answer both named objects: %+v", result)
	}
}

func TestReplyValidatorV2DoesNotAutoCoverUnknownCombinedTasks(t *testing.T) {
	input := validReplyValidationInputForTest("第一项可以。")
	input.Plan.Tasks[0].SubIntent = "custom_alpha"
	input.Plan.Tasks[0].Objective = "甲项规则"
	input.Plan.Tasks = append(input.Plan.Tasks, contracts.ReplyPlanTaskV2{
		TaskKey: "task_beta", Sequence: 2, Intent: "hotel_info", SubIntent: "custom_beta", Objective: "乙项规则", OutputMode: "text",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "forbidden", Status: "not_needed"}, EvidenceRefs: []string{}, ActionRefs: []string{}, Constraints: []string{},
	})
	input.Output.Parts[0].TaskKeys = []string{"task_1", "task_beta"}
	result := NewReplyValidatorForMode(runtimeValidatorV2).Validate(input)
	if result.Status != "repairable_protocol_error" || !validationHasCode(result, "task_answer_obligation_missing") {
		t.Fatalf("unknown combined tasks must be repaired instead of auto-covered: %+v", result)
	}
}

func TestReplyAnswerUnitQuestionDetectionKeepsAnswerPayload(t *testing.T) {
	if replyAnswerUnitLooksLikeQuestion("停车场怎么走：从东门进。") {
		t.Fatal("answer payload with a question-shaped lead must not be rejected")
	}
	if !replyAnswerUnitLooksLikeQuestion("停车场怎么走？") {
		t.Fatal("plain question should not count as an answer")
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

func TestApplyRuntimeReplyOutputV2PreservesValidSiblingAndRepairsOnlyInvalidTask(t *testing.T) {
	input := multiTaskReplyValidationInputForTest()
	summary := &RunResult{
		ReplyPlanV2: &input.Plan, EvidenceBundle: &input.Evidence, ActionLedgerV2: &input.ActionLedger,
		RuntimeValidatorMode: runtimeValidatorV2, ValidationGates: DefaultReplyValidationGates(),
	}
	collector := callbacks.NewRuntimeTraceCollector()
	raw := `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["parking"],"content":"停车场从酒店东侧入口进。"},{"taskKeys":["address"],"content":"地址是壹间公寓高新社区。"}]}`
	err := applyRuntimeReplyOutputV2(raw, summary, collector, RunInput{})
	var protocolErr *replyOutputProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("expected one pending-task protocol repair, got %v", err)
	}
	if len(summary.replyRepairState.PreservedParts) != 1 || summary.replyRepairState.PreservedParts[0].TaskKeys[0] != "parking" ||
		!reflect.DeepEqual(summary.replyRepairState.PendingTaskKeys, []string{"address"}) {
		t.Fatalf("valid sibling was not preserved: %#v", summary.replyRepairState)
	}
	repaired := `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["address"],"content":"当前门店地址是合肥市包河区水阳江路392号。"}]}`
	if err := applyRuntimeReplyOutputV2(repaired, summary, collector, RunInput{}); err != nil {
		t.Fatalf("repair output did not merge with preserved sibling: %v", err)
	}
	if len(summary.ReplyParts) != 2 || !strings.Contains(summary.ReplyText, "停车场") || !strings.Contains(summary.ReplyText, "水阳江路392号") {
		t.Fatalf("merged reply mismatch: parts=%#v text=%q", summary.ReplyParts, summary.ReplyText)
	}
}

func TestSafeRuntimeDegradedKeepsPreservedAnswersAndOriginalPlan(t *testing.T) {
	input := multiTaskReplyValidationInputForTest()
	summary := &RunResult{
		ReplyPlanV2: &input.Plan, EvidenceBundle: &input.Evidence, ActionLedgerV2: &input.ActionLedger,
		RuntimeValidatorMode: runtimeValidatorV2, ValidationGates: DefaultReplyValidationGates(),
		replyRepairState: runtimeReplyRepairState{
			PreservedParts:  []contracts.ReplyPartV2{{TaskKeys: []string{"parking"}, Content: "停车场从酒店东侧入口进。", EvidenceRefs: []string{"K1"}}},
			PendingTaskKeys: []string{"address"},
		},
	}
	collector := callbacks.NewRuntimeTraceCollector()
	if !applySafeRuntimeDegraded(summary, collector, RunInput{}, errors.New("repair failed")) {
		t.Fatal("safe degradation should keep the already-valid answer")
	}
	if len(summary.ReplyParts) != 2 || len(summary.ReplyPlanV2.Tasks) != 2 {
		t.Fatalf("safe degradation lost answers or mutated the plan: parts=%#v plan=%#v", summary.ReplyParts, summary.ReplyPlanV2)
	}
	for _, part := range summary.ReplyParts {
		if len(part.TaskKeys) != 1 || (part.TaskKeys[0] != "parking" && part.TaskKeys[0] != "address") {
			t.Fatalf("safe degradation committed an unanswered task: %#v", summary.ReplyParts)
		}
	}
}

func TestApplyRuntimeReplyOutputV2HardRejectedTaskDoesNotDropValidSibling(t *testing.T) {
	input := multiTaskReplyValidationInputForTest()
	summary := &RunResult{
		ReplyPlanV2: &input.Plan, EvidenceBundle: &input.Evidence, ActionLedgerV2: &input.ActionLedger,
		RuntimeValidatorMode: runtimeValidatorV2, ValidationGates: DefaultReplyValidationGates(),
	}
	collector := callbacks.NewRuntimeTraceCollector()
	raw := `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["parking"],"content":"停车场从酒店东侧入口进。"},{"taskKeys":["address"],"content":"我已经通知前台处理了。"}]}`
	if err := applyRuntimeReplyOutputV2(raw, summary, collector, RunInput{}); err != nil {
		t.Fatalf("hard-rejected task should settle without another Generate: %v", err)
	}
	if len(summary.ReplyParts) != 2 || !strings.Contains(summary.ReplyText, "停车场") || !strings.Contains(summary.ReplyText, "水阳江路392号") {
		t.Fatalf("valid sibling or safe address result was lost: parts=%#v text=%q", summary.ReplyParts, summary.ReplyText)
	}
}

func TestApplyRuntimeReplyOutputV2IsolatesTasksInsideMergedPart(t *testing.T) {
	input := multiTaskReplyValidationInputForTest()
	summary := &RunResult{
		ReplyPlanV2: &input.Plan, EvidenceBundle: &input.Evidence, ActionLedgerV2: &input.ActionLedger,
		RuntimeValidatorMode: runtimeValidatorV2, ValidationGates: DefaultReplyValidationGates(),
	}
	raw := `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["parking","address"],"content":"停车场从酒店东侧入口进。地址是壹间公寓高新社区。"}]}`
	collector := callbacks.NewRuntimeTraceCollector()
	var protocolErr *replyOutputProtocolError
	if err := applyRuntimeReplyOutputV2(raw, summary, collector, RunInput{}); !errors.As(err, &protocolErr) {
		t.Fatalf("merged part should request one pending-task repair: %v", err)
	}
	if len(summary.replyRepairState.PreservedParts) != 1 || summary.replyRepairState.PreservedParts[0].TaskKeys[0] != "parking" {
		t.Fatalf("valid task inside merged part was not isolated: %#v", summary.replyRepairState)
	}
	if err := applyRuntimeReplyOutputV2(`{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["address"],"content":"地址是壹间公寓高新社区。"}]}`, summary, collector, RunInput{}); err != nil {
		t.Fatalf("failed repair should settle deterministically: %v", err)
	}
	if len(summary.ReplyParts) != 2 || !strings.Contains(summary.ReplyText, "停车场") || !strings.Contains(summary.ReplyText, "水阳江路392号") ||
		strings.Contains(summary.ReplyText, "壹间公寓") {
		t.Fatalf("merged task isolation mismatch: parts=%#v text=%q", summary.ReplyParts, summary.ReplyText)
	}
}

func TestApplyRuntimeReplyOutputV2UsesExplicitSafeResultForUnrecoverableTask(t *testing.T) {
	input := multiTaskReplyValidationInputForTest()
	input.Plan.Tasks[1] = contracts.ReplyPlanTaskV2{
		TaskKey: "wifi", Sequence: 2, Intent: "hotel_info", SubIntent: "network_wifi", Objective: "WiFi密码是多少", OutputMode: "text",
		Knowledge: contracts.ReplyPlanKnowledge{Policy: "required", Status: "has_context"}, EvidenceRefs: []string{"K2"}, ActionRefs: []string{}, Constraints: []string{},
	}
	input.Evidence.Items[1] = contracts.EvidenceItemV1{
		Ref: "K2", SourceType: "fastgpt", TaskKeys: []string{"wifi"}, Title: "WiFi", Content: "房间内有网络提示卡。", Score: 1,
		Answerability: "supporting", ResourceRefs: []string{},
	}
	summary := &RunResult{
		ReplyPlanV2: &input.Plan, EvidenceBundle: &input.Evidence, ActionLedgerV2: &input.ActionLedger,
		RuntimeValidatorMode: runtimeValidatorV2, ValidationGates: DefaultReplyValidationGates(),
	}
	raw := `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["parking","wifi"],"content":"停车场从酒店东侧入口进。"}]}`
	collector := callbacks.NewRuntimeTraceCollector()
	var protocolErr *replyOutputProtocolError
	if err := applyRuntimeReplyOutputV2(raw, summary, collector, RunInput{}); !errors.As(err, &protocolErr) {
		t.Fatalf("missing task should receive one repair opportunity: %v", err)
	}
	failedRepair := `{"schemaVersion":"reply_output.v2","parts":[{"taskKeys":["wifi"],"content":"我已经通知前台处理了。"}]}`
	if err := applyRuntimeReplyOutputV2(failedRepair, summary, collector, RunInput{}); err != nil {
		t.Fatalf("repair exhaustion should receive a deterministic safe result: %v", err)
	}
	if len(summary.ReplyParts) != 2 || summary.ReplyParts[1].Content != "关于WiFi，当前没能确认，不能乱答。" ||
		!reflect.DeepEqual(summary.ReplyParts[1].TaskKeys, []string{"wifi"}) {
		t.Fatalf("safe task result mismatch: %#v", summary.ReplyParts)
	}
}

func TestSafeRuntimeDegradedDoesNotRaiseReplyPartLimit(t *testing.T) {
	input := multiTaskReplyValidationInputForTest()
	input.Plan.GlobalConstraints.MaxReplyParts = 1
	input.Plan.GlobalConstraints.MaxQuestionsPerPart = 1
	summary := &RunResult{
		ReplyPlanV2: &input.Plan, EvidenceBundle: &input.Evidence, ActionLedgerV2: &input.ActionLedger,
		RuntimeValidatorMode: runtimeValidatorV2, ValidationGates: DefaultReplyValidationGates(),
		replyRepairState: runtimeReplyRepairState{PreservedParts: []contracts.ReplyPartV2{
			{TaskKeys: []string{"parking"}, Content: "停车场从酒店东侧入口进。", EvidenceRefs: []string{"K1"}},
			{TaskKeys: []string{"address"}, Content: "当前门店地址是合肥市包河区水阳江路392号。", EvidenceRefs: []string{"S1"}},
		}},
	}
	if applySafeRuntimeDegraded(summary, callbacks.NewRuntimeTraceCollector(), RunInput{}, errors.New("hard failure")) {
		t.Fatalf("safe degradation must not bypass maxReplyParts: %#v", summary.ReplyParts)
	}
}

func multiTaskReplyValidationInputForTest() ReplyValidationInput {
	return ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			SchemaVersion: contracts.ReplyPlanV2SchemaVersion, TurnVersion: 1, ShouldGenerate: true,
			Tasks: []contracts.ReplyPlanTaskV2{
				{TaskKey: "parking", Sequence: 1, Intent: "hotel_info", SubIntent: "parking", Objective: "停车场怎么走", OutputMode: "text", Knowledge: contracts.ReplyPlanKnowledge{Policy: "required", Status: "has_context"}, EvidenceRefs: []string{"K1"}, ActionRefs: []string{}, Constraints: []string{}},
				{TaskKey: "address", Sequence: 2, Intent: "hotel_info", SubIntent: "address", Objective: "酒店地址发我", OutputMode: "text", Knowledge: contracts.ReplyPlanKnowledge{Policy: "required", Status: "has_context"}, EvidenceRefs: []string{"S1"}, ActionRefs: []string{}, Constraints: []string{}},
			},
			GlobalConstraints: contracts.ReplyPlanGlobalConstraints{MaxReplyParts: 3, MaxQuestionsPerPart: 4, ForbiddenClaims: []string{}},
		},
		Evidence: contracts.EvidenceBundleV1{
			SchemaVersion: contracts.EvidenceBundleV1SchemaVersion, ScopeFingerprint: "scope-test", RetrievalStatus: "has_context",
			Items: []contracts.EvidenceItemV1{
				{Ref: "K1", SourceType: "fastgpt", TaskKeys: []string{"parking"}, Title: "停车", Content: "停车场从酒店东侧入口进入。", Score: 1, Answerability: "supporting", ResourceRefs: []string{}},
				{Ref: "S1", SourceType: "store_fact", TaskKeys: []string{"address"}, Title: authoritativeStoreAddressEvidenceTitle, Content: "合肥市包河区水阳江路392号", Score: 1, Answerability: "supporting", ResourceRefs: []string{}},
			}, Resources: []contracts.EvidenceResourceV1{},
		},
		ActionLedger: contracts.ActionLedgerV1{SchemaVersion: contracts.ActionLedgerV1SchemaVersion, TurnVersion: 1, Actions: []contracts.ActionLedgerItemV1{}},
	}
}
