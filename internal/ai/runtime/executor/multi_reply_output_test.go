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
	if !strings.Contains(instruction, `"taskKeys":["任务键1","任务键2"]`) ||
		!strings.Contains(instruction, "taskKeys=task-1：停车在哪里") ||
		!strings.Contains(instruction, "taskKeys=task-3：早餐几点") {
		t.Fatalf("unexpected instruction: %s", instruction)
	}
	if strings.Contains(instruction, "定位发我") {
		t.Fatalf("structured variable task must stay out of generated text contract: %s", instruction)
	}
}

func TestNormalizeGeneratedReplyPartsCombinesSharedKnowledgeAnswerGroup(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskKey: "task-coffee", AnswerGroup: "knowledge_answer_coffee", Intent: "hotel_info", Text: "想喝咖啡了", Output: "knowledge_text_reply"},
		{TaskKey: "task-coffee-location", AnswerGroup: "knowledge_answer_coffee", Intent: "hotel_info", Text: "咖啡在哪", Output: "knowledge_text_reply"},
		{TaskKey: "task-parking", Intent: "hotel_info", Text: "停车场在哪", Output: "knowledge_text_reply"},
	}}
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) != 2 || strings.Join(groups[0].TaskKeys, ",") != "task-coffee,task-coffee-location" {
		t.Fatalf("shared knowledge tasks were not grouped: %#v", groups)
	}
	raw := `{"replyParts":[{"taskKeys":["task-coffee","task-coffee-location"],"content":"速溶咖啡在1313房间对面的洗衣房，可自行取用。"},{"taskKeys":["task-parking"],"content":"停车入口在昭潭路。"}]}`
	got, err := normalizeGeneratedReplyPartsStrict(raw, plan)
	if err != nil {
		t.Fatalf("normalize grouped reply parts: %v", err)
	}
	want := "速溶咖啡在1313房间对面的洗衣房，可自行取用。\n<<NEXT_MESSAGE>>\n停车入口在昭潭路。"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}

	separate := `{"replyParts":[{"taskKeys":["task-coffee"],"content":"有咖啡。"},{"taskKeys":["task-coffee-location"],"content":"咖啡在洗衣房。"},{"taskKeys":["task-parking"],"content":"停车入口在昭潭路。"}]}`
	if got, err := normalizeGeneratedReplyPartsStrict(separate, plan); err == nil || got != "" {
		t.Fatalf("shared answer group must not be split into duplicate replies, got=%q err=%v", got, err)
	}
}

func TestNormalizeGeneratedReplyPartsOrdersPartsByTask(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := `{"replyParts":[{"taskId":"task-2","content":"早餐在一楼。"},{"taskId":"task-1","content":"停车从辅路入口进。"}]}`
	got, err := normalizeGeneratedReplyPartsStrict(raw, plan)
	if err != nil {
		t.Fatalf("normalize reply parts: %v", err)
	}
	want := "停车从辅路入口进。\n<<NEXT_MESSAGE>>\n早餐在一楼。"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNormalizeGeneratedReplyPartsRejectsUnstructuredFallback(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", Text: "停车在哪里", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", Text: "早餐几点", Output: "knowledge_text_reply"},
	}}
	raw := "停车从辅路入口进，早餐在一楼。"
	if got, err := normalizeGeneratedReplyPartsStrict(raw, plan); err == nil || got != "" {
		t.Fatalf("invalid structured output must be rejected, got=%q err=%v", got, err)
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
