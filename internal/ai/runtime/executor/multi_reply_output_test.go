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
	instruction := buildMultiReplyOutputInstruction(plan)
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
	got := normalizeGeneratedReplyParts(raw, plan)
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
	if got := normalizeGeneratedReplyParts(raw, plan); got != raw {
		t.Fatalf("invalid structured output must preserve existing reply, got %q", got)
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
