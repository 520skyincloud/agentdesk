package executor

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
)

func TestBuildMultiReplyOutputInstructionUsesTextTasksOnly(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_variable", Text: "定位发我", Output: "structured_resource_commit", ResourceAction: "provide_location"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	instruction := buildMultiReplyOutputInstruction(plan, false)
	if !strings.Contains(instruction, `"taskId":"task-1"`) || !strings.Contains(instruction, "停车在哪里") || !strings.Contains(instruction, "早餐几点") {
		t.Fatalf("unexpected instruction: %s", instruction)
	}
	if strings.Contains(instruction, "定位发我") {
		t.Fatalf("structured variable task must stay out of generated text contract: %s", instruction)
	}
}

func TestNormalizeGeneratedReplyPartsOrdersPartsByTask(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-2","content":"早餐在一楼。"},{"taskId":"task-1","content":"停车从辅路入口进。"}]}`
	got := normalizeGeneratedReplyParts(raw, plan, false)
	want := "停车从辅路入口进。\n<<NEXT_MESSAGE>>\n早餐在一楼。"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNormalizeGeneratedReplyPartsUnwrapsSingleTaskProtocol(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"早餐供应到9:30。"}]}`
	if got := normalizeGeneratedReplyParts(raw, plan, false); got != "早餐供应到9:30。" {
		t.Fatalf("single-task protocol must be unwrapped, got %q", got)
	}
}

func TestNormalizeGeneratedReplyPartsUnwrapsMarkdownAndJSONStrings(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	payload := `{"replyParts":[{"taskId":"task-1","content":"停车从辅路入口进。"},{"taskId":"task-2","content":"早餐供应到9:30。"}]}`
	quoted, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal quoted reply protocol: %v", err)
	}
	wrapped, err := json.Marshal(string(quoted))
	if err != nil {
		t.Fatalf("marshal double-quoted reply protocol: %v", err)
	}

	want := "停车从辅路入口进。\n<<NEXT_MESSAGE>>\n早餐供应到9:30。"
	for name, raw := range map[string]string{
		"markdown":       "模型输出如下：\n```json\n" + payload + "\n```",
		"quoted":         string(quoted),
		"double_quoted":  string(wrapped),
		"common_wrapper": `{"result":` + string(quoted) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := normalizeGeneratedReplyParts(raw, plan, false); got != want {
				t.Fatalf("expected %q, got %q", want, got)
			}
		})
	}
}

func TestNormalizeGeneratedReplyPartsFallsBackWithoutLosingReply(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := "停车从辅路入口进，早餐在一楼。"
	if got := normalizeGeneratedReplyParts(raw, plan, false); got != raw {
		t.Fatalf("invalid structured output must preserve existing reply, got %q", got)
	}
}

func TestNormalizeGeneratedReplyPartsSuppressesMalformedProtocolWithoutDeferredHandoff(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"早餐供应到9:30。"}]`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
	if got != "" {
		t.Fatalf("malformed internal protocol must not leak, got %q", got)
	}
	if !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("malformed internal protocol must request a retry, got %v", err)
	}
}

func TestNormalizeGeneratedReplyPartsRejectsBareTaskProtocolShapes(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	for name, raw := range map[string]string{
		"bare_object": `{"taskId":"task-1","content":"早餐供应到9:30。"}`,
		"bare_array":  `[{"taskId":"task-1","content":"早餐供应到9:30。"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
			if got != "" || !errors.Is(err, errGeneratedReplyProtocol) {
				t.Fatalf("bare internal protocol shape must fail without leaking output, got=%q err=%v", got, err)
			}
		})
	}
}

func TestNormalizeGeneratedReplyPartsRejectsInvalidTaskIDs(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	for name, raw := range map[string]string{
		"missing":   `{"replyParts":[{"content":"停车从辅路入口进。"},{"taskId":"task-2","content":"早餐供应到9:30。"}]}`,
		"unknown":   `{"replyParts":[{"taskId":"task-1","content":"停车从辅路入口进。"},{"taskId":"task-3","content":"早餐供应到9:30。"}]}`,
		"duplicate": `{"replyParts":[{"taskId":"task-1","content":"停车从辅路入口进。"},{"taskId":"task-1","content":"早餐供应到9:30。"}]}`,
		"extra":     `{"replyParts":[{"taskId":"task-1","content":"停车从辅路入口进。"},{"taskId":"task-2","content":"早餐供应到9:30。"},{"taskId":"task-3","content":"多余内容"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := normalizeGeneratedReplyPartsResult(raw, plan, false)
			if got != "" || !errors.Is(err, errGeneratedReplyProtocol) {
				t.Fatalf("invalid task IDs must fail without leaking output, got=%q err=%v", got, err)
			}
		})
	}
}

func TestBuildMultiReplyOutputInstructionRequiresStructuredSingleTaskForDeferredHandoff(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
	}}
	instruction := buildMultiReplyOutputInstruction(plan, true)
	if !strings.Contains(instruction, `"taskId":"task-1"`) || !strings.Contains(instruction, "顺便问早餐几点") {
		t.Fatalf("expected a structured contract for the single active task, got %q", instruction)
	}
}

func TestNormalizeGeneratedReplyPartsKeepsActiveTaskForDeferredHandoff(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"酒店暂不提供早餐。"}]}`

	got := normalizeGeneratedReplyParts(raw, plan, true)

	if got != "酒店暂不提供早餐。" {
		t.Fatalf("expected only the answerable task content, got %q", got)
	}
}

func TestNormalizeGeneratedReplyPartsFailsClosedOnMalformedDeferredOutput(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"酒店暂不提供早餐。"}`
	if got := normalizeGeneratedReplyParts(raw, plan, true); got != "" {
		t.Fatalf("malformed deferred output must fail closed, got %q", got)
	}
}

func TestNormalizeGeneratedReplyPartsFailsClosedWhenActivePartIsMissing(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "停车免费吗", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-1","content":"酒店暂不提供早餐。"}]}`
	got, err := normalizeGeneratedReplyPartsResult(raw, plan, true)
	if got != "" {
		t.Fatalf("missing active task content must fail closed, got %q", got)
	}
	if !errors.Is(err, errGeneratedReplyProtocol) {
		t.Fatalf("missing active task content must request a retry, got %v", err)
	}
}

func TestBuildActiveGenerationUserMessageTextExcludesDeferredQuestion(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:  "service_request",
		NeedsKnowledge: true,
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "breakfast", Text: "顺便问早餐几点", Output: "knowledge_text_reply"},
	}}
	current := "空调坏了，我住1302\n顺便问早餐几点"
	got := buildActiveGenerationUserMessageText(current, intent, plan, true)
	if got != "顺便问早餐几点" {
		t.Fatalf("expected only the active answerable question, got %q", got)
	}
}

func TestBuildActiveGenerationUserMessageTextDoesNotRestoreDeferredQuestionForResourceOnlyPlan(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:   "hotel_variable",
		NeedsKnowledge:  true,
		NeedsResource:   true,
		ResourceActions: []string{"provide_mini_program"},
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_variable", SubIntent: "mini_program", Output: "structured_resource_commit", ResourceAction: "provide_mini_program"},
	}}
	current := "空调坏了，入住小程序发我"
	got := buildActiveGenerationUserMessageText(current, intent, plan, true)
	if strings.Contains(got, "空调坏了") || strings.Contains(got, "入住小程序发我") {
		t.Fatalf("resource-only active plan must not restore deferred customer questions, got %q", got)
	}
	if !strings.Contains(got, "没有需要 Generate 输出的文本任务") {
		t.Fatalf("expected an explicit no-text-task placeholder, got %q", got)
	}
}

func TestBuildTextReplyTaskGroupsCapsAtThreeMessages(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{}
	for _, text := range []string{"一", "二", "三", "四"} {
		plan.TaskPlans = append(plan.TaskPlans, callbacks.ReplyTaskPlanTraceData{Intent: "hotel_info", Text: text, Output: "knowledge_text_reply"})
	}
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) != 3 || strings.Join(groups[2].Texts, "") != "三四" {
		t.Fatalf("expected remaining tasks to be combined into the third message, got %#v", groups)
	}
}
