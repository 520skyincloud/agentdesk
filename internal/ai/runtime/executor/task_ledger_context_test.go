package executor

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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
	inputs, plannedByKey, err := buildRuntimeTaskInputs(plans, 14, messages, 1, 2)
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
	if !ok || normalizeRuntimeTaskText(duplicatePlan.Text) != normalizeRuntimeTaskText("有咖啡吗。") || duplicatePlan.Intent != "hotel_info" {
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
	inputs, _, err := buildRuntimeTaskInputs(plans, 22, messages, 1, 2)
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

func TestBuildRuntimeTaskInputsKeepsMultipleQuestionsOnSameMessage(t *testing.T) {
	messages := []models.Message{{
		ID: 41, MessageType: enums.IMMessageTypeText,
		Content: "咖啡在哪里，停车场怎么走？",
	}}
	plans := []callbacks.ReplyTaskPlanTraceData{
		{Sequence: 1, Intent: "hotel_info", SubIntent: "coffee", Text: "咖啡在哪里", Output: "knowledge_text_reply"},
		{Sequence: 2, Intent: "hotel_info", SubIntent: "parking", Text: "停车场怎么走", Output: "knowledge_text_reply"},
	}
	inputs, plannedByKey, err := buildRuntimeTaskInputs(plans, 41, messages, 9, 10)
	if err != nil {
		t.Fatalf("build runtime task inputs: %v", err)
	}
	if len(inputs) != 2 {
		t.Fatalf("expected two tasks for one source message, got %#v", inputs)
	}
	if inputs[0].SourceMessageID != 41 || inputs[1].SourceMessageID != 41 {
		t.Fatalf("both tasks must bind the same source message: %#v", inputs)
	}
	if len(plannedByKey) != 2 {
		t.Fatalf("both scoped task keys must preserve their original plans: %#v", plannedByKey)
	}
	for _, input := range inputs {
		key := services.AIReplyTurnTaskService.StableTaskKey(input)
		if _, ok := plannedByKey[key]; !ok {
			t.Fatalf("persisted task key %q lost its original plan", key)
		}
	}
}

func TestBuildRuntimeTaskInputsBindsLongVoiceQuestionsToReadyAnalysis(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.MessageAnalysis{}); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })

	transcript := "我想办理入住，然后想问一下附近哪里有咖啡"
	message := models.Message{
		ID: 501, TenantID: 1, ConversationID: 9, SessionNo: 1,
		MessageType: enums.IMMessageTypeVoice, Content: "voice.amr",
		Payload: `{"mediaText":"旧的载荷文本","mediaUnderstandingStatus":"understood"}`,
	}
	if err := services.MessageAnalysisService.RecordMediaReady(&message, transcript, services.MessageAnalyzerIdentity{
		Kind: "asr", Name: "voice-asr", Version: "v2",
	}); err != nil {
		t.Fatal(err)
	}
	plans := []callbacks.ReplyTaskPlanTraceData{
		{Sequence: 1, Intent: "hotel_info", SubIntent: "checkin_process", RequestMode: "answer", Text: "办理入住", Output: "knowledge_text_reply"},
		{Sequence: 2, Intent: "hotel_info", SubIntent: "surrounding_facilities", RequestMode: "answer", Text: "附近哪里有咖啡", Output: "knowledge_text_reply"},
	}
	inputs, _, err := buildRuntimeTaskInputs(plans, message.ID, []models.Message{message}, 1, 10)
	if err != nil {
		t.Fatalf("build runtime task inputs: %v", err)
	}
	if len(inputs) != 2 {
		t.Fatalf("long voice must create both tasks, got %#v", inputs)
	}
	if inputs[0].SourceMessageID != message.ID || inputs[1].SourceMessageID != message.ID ||
		inputs[0].AnalysisRevision != 1 || inputs[1].AnalysisRevision != 1 {
		t.Fatalf("voice source binding lost authoritative revision: %#v", inputs)
	}
	if inputs[0].SourceSpanEnd <= inputs[0].SourceSpanStart || inputs[1].SourceSpanEnd <= inputs[1].SourceSpanStart ||
		inputs[0].SourceSpanStart == inputs[1].SourceSpanStart {
		t.Fatalf("voice questions must keep distinct source spans: %#v", inputs)
	}
	if inputs[0].CanonicalQuestionHash == "" || inputs[1].CanonicalQuestionHash == "" ||
		inputs[0].CanonicalQuestionHash == inputs[1].CanonicalQuestionHash {
		t.Fatalf("voice questions must keep distinct canonical hashes: %#v", inputs)
	}
	for _, input := range inputs {
		var bindings contracts.TaskSourceBindingsV1
		if err := json.Unmarshal([]byte(input.SourceBindingsJSON), &bindings); err != nil {
			t.Fatalf("decode source bindings: %v", err)
		}
		if bindings.SchemaVersion != contracts.TaskSourceBindingsV1SchemaVersion || bindings.PrimaryMessageID != message.ID || len(bindings.Bindings) != 1 {
			t.Fatalf("unexpected source bindings: %#v", bindings)
		}
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

func TestMatchRuntimeTaskSourceMessageUsesTriggerForRewrittenTask(t *testing.T) {
	messages := []models.Message{
		{ID: 2066, MessageType: enums.IMMessageTypeText, Content: "？"},
		{ID: 2067, MessageType: enums.IMMessageTypeText, Content: "给我办入住"},
	}
	plan := callbacks.ReplyTaskPlanTraceData{Sequence: 1, Text: "怎么办理入住"}
	got := matchRuntimeTaskSourceMessage(plan, 2067, messages, map[int64]struct{}{})
	if got != 2067 {
		t.Fatalf("rewritten task must bind trigger message 2067, got %d", got)
	}
}
