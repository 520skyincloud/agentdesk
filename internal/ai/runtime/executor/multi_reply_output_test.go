package executor

import (
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
	if got := normalizeGeneratedReplyParts(raw, plan, true); got != "" {
		t.Fatalf("missing active task content must fail closed, got %q", got)
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
