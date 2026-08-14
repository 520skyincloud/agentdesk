package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"
)

func TestValidateRuntimeTaskPlanBlocksAllFailedKnowledgeTasks(t *testing.T) {
	plan := runtimePipelinePlan{
		TaskState: runtimeTaskBatchState{
			Enabled:          true,
			SelectedTaskKeys: []string{"task-coffee", "task-parking"},
			FailedTaskKeys:   []string{"task-coffee", "task-parking"},
		},
	}
	skip, err := validateRuntimeTaskPlan(plan)
	if skip {
		t.Fatal("failed knowledge tasks must not be silently skipped")
	}
	code, ok := services.AIReplyExecutionErrorCodeOf(err)
	if !ok || code != services.AIReplyExecutionErrorKnowledgeUnavailable {
		t.Fatalf("error=%v code=%q", err, code)
	}
}

func TestBuildNoHitTaskInstructionScopesOnlyMissingKnowledge(t *testing.T) {
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskKey: "task-coffee", Text: "有现磨咖啡吗", Output: "knowledge_text_reply"},
		{TaskKey: "task-parking", Text: "停车场在哪", Output: "knowledge_text_reply"},
	}}
	instruction := buildNoHitTaskInstruction(plan, []string{"task-coffee"})
	if !strings.Contains(instruction, "task-coffee") || !strings.Contains(instruction, "有现磨咖啡吗") ||
		!strings.Contains(instruction, "当前资料未写明") {
		t.Fatalf("no-hit instruction=%q", instruction)
	}
	if strings.Contains(instruction, "task-parking") || strings.Contains(instruction, "停车场在哪") {
		t.Fatalf("no-hit instruction leaked successful task: %q", instruction)
	}
}

func TestBuildRuntimeTaskInputsMapsSeparateMessagesAndLabelsExactDuplicate(t *testing.T) {
	messages := []models.Message{
		{ID: 11, MessageType: enums.IMMessageTypeText, Content: "怎么办理入住"},
		{ID: 12, MessageType: enums.IMMessageTypeText, Content: "有咖啡吗？"},
		{ID: 13, MessageType: enums.IMMessageTypeText, Content: "停车场在哪里"},
		{ID: 14, MessageType: enums.IMMessageTypeText, Content: " 有咖啡吗。 "},
	}
	plans := []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "checkin_process", Text: "怎么办理入住", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", SubIntent: "service_facility", Text: "有咖啡吗", Output: "knowledge_text_reply"},
		{Intent: "hotel_info", SubIntent: "parking", Text: "停车场在哪里", Output: "knowledge_text_reply"},
	}
	inputs, plannedByKey, err := buildRuntimeTaskInputs(plans, 14, messages)
	if err != nil {
		t.Fatalf("build runtime task inputs: %v", err)
	}
	if len(inputs) != 4 {
		t.Fatalf("task inputs=%#v", inputs)
	}
	for index, wantMessageID := range []int64{11, 12, 13, 14} {
		if inputs[index].SourceMessageID != wantMessageID {
			t.Fatalf("input %d source=%d want=%d", index, inputs[index].SourceMessageID, wantMessageID)
		}
	}
	duplicateKey := services.AIReplyTurnTaskService.StableTaskKey(inputs[3])
	duplicatePlan, ok := plannedByKey[duplicateKey]
	if !ok || duplicatePlan.Text != "有咖啡吗。" || duplicatePlan.Intent != "hotel_info" {
		t.Fatalf("duplicate source was not assigned a stable task plan: %#v", duplicatePlan)
	}
	if services.AIReplyTurnTaskService.QuestionFingerprint(inputs[1].QuestionText) != services.AIReplyTurnTaskService.QuestionFingerprint(inputs[3].QuestionText) {
		t.Fatal("exact duplicate messages must share the deterministic question fingerprint")
	}
}

func TestBuildRuntimeTaskInputsSkipsPunctuationOnlyPlan(t *testing.T) {
	messages := []models.Message{
		{ID: 21, MessageType: enums.IMMessageTypeText, Content: "怎么办理入住"},
		{ID: 22, MessageType: enums.IMMessageTypeText, Content: "？？？"},
	}
	plans := []callbacks.ReplyTaskPlanTraceData{
		{Intent: "hotel_info", SubIntent: "checkin_process", Text: "怎么办理入住", Output: "knowledge_text_reply"},
		{Intent: "interaction", SubIntent: "chat", Text: "？？？", Output: "text_reply"},
	}
	inputs, _, err := buildRuntimeTaskInputs(plans, 22, messages)
	if err != nil {
		t.Fatalf("build runtime task inputs: %v", err)
	}
	if len(inputs) != 1 {
		t.Fatalf("punctuation-only plan must not create a task, got %d inputs: %#v", len(inputs), inputs)
	}
	if inputs[0].SourceMessageID != 21 {
		t.Fatalf("expected source=21, got %d", inputs[0].SourceMessageID)
	}
}

func TestMatchRuntimeTaskSourceMessagePrefersExactHash(t *testing.T) {
	messages := []models.Message{
		{ID: 31, MessageType: enums.IMMessageTypeText, Content: "停车场在哪里"},
		{ID: 32, MessageType: enums.IMMessageTypeText, Content: "停车场在哪里入口怎么走"},
	}
	plan := callbacks.ReplyTaskPlanTraceData{Sequence: 2, Text: "停车场在哪里"}
	// 严格相等优先：sequence=2 会指到 32，但文本哈希只等于 31，必须选 31 而非 contains 匹配。
	got := matchRuntimeTaskSourceMessage(plan, 31, messages, map[int64]struct{}{})
	if got != 31 {
		t.Fatalf("expected exact-hash match to 31, got %d", got)
	}
}
