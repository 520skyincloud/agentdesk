package executor

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/contextcompiler"
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

func TestBuildRuntimeTaskInputsBindsContextAndPrimaryToOneTask(t *testing.T) {
	messages := []models.Message{
		{ID: 61, MessageType: enums.IMMessageTypeText, Content: "好困啊"},
		{ID: 62, MessageType: enums.IMMessageTypeText, Content: "有没有咖啡"},
	}
	plans := []callbacks.ReplyTaskPlanTraceData{{
		Sequence: 1, Intent: "hotel_info", SubIntent: "coffee", RequestMode: "answer",
		Text: "有没有咖啡", Output: "knowledge_text_reply",
		SourceRefs: []string{"U2", "U1"}, SourceMessageIDs: []int64{62, 61},
	}}
	inputs, plannedByKey, err := buildRuntimeTaskInputs(plans, 62, messages, 9, 10)
	if err != nil {
		t.Fatalf("build runtime task inputs: %v", err)
	}
	if len(inputs) != 1 || inputs[0].SourceMessageID != 62 {
		t.Fatalf("coffee request must create one primary task: %#v", inputs)
	}
	var bindings contracts.TaskSourceBindingsV1
	if err := json.Unmarshal([]byte(inputs[0].SourceBindingsJSON), &bindings); err != nil {
		t.Fatalf("decode source bindings: %v", err)
	}
	if bindings.PrimaryMessageID != 62 || len(bindings.Bindings) != 2 ||
		bindings.Bindings[0].MessageID != 62 || bindings.Bindings[1].MessageID != 61 {
		t.Fatalf("context and primary bindings were not preserved: %#v", bindings)
	}
	taskKey := services.AIReplyTurnTaskService.StableTaskKey(inputs[0])
	if planned, ok := plannedByKey[taskKey]; !ok || len(planned.SourceMessageIDs) != 2 {
		t.Fatalf("planned task lost source bindings: %#v", plannedByKey)
	}
	if !runtimeTaskSourcesCovered(messages, []models.AIReplyTurnTask{{
		SourceMessageID: inputs[0].SourceMessageID, SourceBindingsJSON: inputs[0].SourceBindingsJSON,
	}}) {
		t.Fatal("one coffee task must cover both the primary request and its context")
	}
	if !runtimeTaskCoversMessage(models.AIReplyTurnTask{
		SourceMessageID: inputs[0].SourceMessageID, SourceBindingsJSON: inputs[0].SourceBindingsJSON,
	}, 61) {
		t.Fatal("context message recovery must converge on the existing coffee task")
	}
}

func TestResolveIntentV2TaskSourcesPromotesMatchingPrimary(t *testing.T) {
	messages := []models.Message{
		{ID: 61, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "好困啊"},
		{ID: 62, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "有没有咖啡"},
	}
	scope := intentV2SourceScope{
		Envelope: contextcompiler.BuildTurnInputEnvelope(contextcompiler.EnvelopeScope{}, messages),
		RequiredRefs: map[string]struct{}{
			"U1": {}, "U2": {},
		},
	}
	parsed := contracts.IntentTasksV2{Tasks: []contracts.IntentTaskV2{{
		Text: "有没有咖啡", SourceRefs: []string{"U1", "U2"},
	}}}
	if err := resolveIntentV2TaskSources(&parsed, scope); err != nil {
		t.Fatalf("resolve source refs: %v", err)
	}
	if len(parsed.Tasks[0].SourceMessageIDs) != 2 || parsed.Tasks[0].SourceMessageIDs[0] != 62 || parsed.Tasks[0].SourceMessageIDs[1] != 61 {
		t.Fatalf("matching coffee message was not promoted to primary: %#v", parsed.Tasks[0])
	}
}

func TestResolveIntentV2TaskSourcesRejectsCorrectionWithoutPriorContext(t *testing.T) {
	messages := []models.Message{
		{ID: 63, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "有咖啡吗"},
		{ID: 64, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "不是咖啡，我问的是早餐"},
	}
	scope := intentV2SourceScope{
		Envelope:     contextcompiler.BuildTurnInputEnvelope(contextcompiler.EnvelopeScope{}, messages),
		RequiredRefs: map[string]struct{}{"U2": {}},
	}
	parsed := contracts.IntentTasksV2{DialogueAct: "correction", Tasks: []contracts.IntentTaskV2{{
		Text: "早餐几点", SourceRefs: []string{"U2"},
	}}}
	err := resolveIntentV2TaskSources(&parsed, scope)
	if err == nil || !strings.Contains(err.Error(), "source_context_ref_missing") {
		t.Fatalf("correction without prior context must trigger protocol repair, got %v", err)
	}
}

func TestIntentV2ReadyMediaIsRequiredButPendingMediaIsNot(t *testing.T) {
	base := RunInput{
		Conversation: models.Conversation{ID: 70, TenantID: 1},
		UserMessage: models.Message{
			ID: 72, ConversationID: 70, SessionNo: 1, SenderType: enums.IMSenderTypeCustomer,
			MessageType: enums.IMMessageTypeText, Content: "这是什么",
		},
	}
	ready := models.Message{
		ID: 71, ConversationID: 70, SessionNo: 1, SenderType: enums.IMSenderTypeCustomer,
		MessageType: enums.IMMessageTypeImage, Content: "a.jpg",
		Payload: `{"mediaText":"图片 A 的内容","mediaUnderstandingStatus":"understood"}`,
	}
	readyScope := buildIntentV2SourceScope(base, []models.Message{ready})
	if _, ok := readyScope.RequiredRefs["U1"]; !ok {
		t.Fatalf("ready image URef must be required: %#v", readyScope.RequiredRefs)
	}
	if _, ok := readyScope.RequiredRefs["U2"]; !ok {
		t.Fatalf("current text URef must be required: %#v", readyScope.RequiredRefs)
	}

	pending := ready
	pending.Payload = `{"mediaUnderstandingStatus":"retrying"}`
	pendingScope := buildIntentV2SourceScope(base, []models.Message{pending})
	if _, ok := pendingScope.RequiredRefs["U1"]; ok {
		t.Fatalf("pending image URef must not be required as fact: %#v", pendingScope.RequiredRefs)
	}
}

func TestResolveValidateAndDeriveIntentV2TasksKeepsReadyMediaBinding(t *testing.T) {
	messages := []models.Message{
		{
			ID: 81, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeImage,
			Content: "image.jpg", Payload: `{"mediaText":"像素风浅棕黄色圆形食物，疑似鸡蛋或面包","mediaUnderstandingStatus":"understood"}`,
		},
		{ID: 82, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "这啥你知道不"},
	}
	scope := intentV2SourceScope{
		Envelope:     contextcompiler.BuildTurnInputEnvelope(contextcompiler.EnvelopeScope{}, messages),
		Messages:     messages,
		RequiredRefs: map[string]struct{}{"U1": {}, "U2": {}},
	}
	parsed := contracts.IntentTasksV2{
		SchemaVersion: contracts.IntentTasksV2SchemaVersion,
		DialogueAct:   "follow_up",
		Tasks: []contracts.IntentTaskV2{{
			Sequence: 1, Intent: "interaction", SubIntent: "clarify", Text: "这啥你知道不",
			RequestMode: "social", Confidence: 0.6, SourceRefs: []string{"U1", "U2"},
		}},
	}
	derived, err := resolveValidateAndDeriveIntentV2Tasks(&parsed, scope, []models.ReplyIntentConfig{{
		ID: 1, Code: "interaction", Name: "互动", Status: enums.StatusOk,
	}})
	if err != nil {
		t.Fatalf("resolve and derive ready media follow-up: %v", err)
	}
	trace := AdaptIntentV2ToLegacyTrace(parsed, derived)
	if len(trace.IntentTasks) != 1 {
		t.Fatalf("unexpected intent tasks: %#v", trace.IntentTasks)
	}
	task := trace.IntentTasks[0]
	if task.SubIntent != "media_context_follow_up" || task.RequestMode != "answer" {
		t.Fatalf("ready media follow-up was not normalized to a direct answer: %#v", task)
	}
	if len(task.SourceMessageIDs) != 2 || task.SourceMessageIDs[0] != 82 || task.SourceMessageIDs[1] != 81 {
		t.Fatalf("resolved message bindings were lost after capability derivation: %#v", task)
	}
}

func TestBuildRuntimeTaskInputsKeepsImageObservationRevisionOnContextBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.MessageAnalysis{}); err != nil {
		t.Fatal(err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })

	observation := "像素风浅棕黄色圆形食物，疑似鸡蛋或面包"
	image := models.Message{
		ID: 83, TenantID: 1, ConversationID: 9, SessionNo: 1,
		MessageType: enums.IMMessageTypeImage, Content: "image.jpg", Payload: `{"resourceId":"asset-83"}`,
	}
	if err := services.MessageAnalysisService.RecordMediaReady(&image, observation, services.MessageAnalyzerIdentity{
		Kind: "vision", Name: "vision-model", Version: "v1",
	}); err != nil {
		t.Fatal(err)
	}
	question := models.Message{
		ID: 84, TenantID: 1, ConversationID: 9, SessionNo: 1,
		MessageType: enums.IMMessageTypeText, Content: "这啥你知道不",
	}
	plans := []callbacks.ReplyTaskPlanTraceData{{
		Sequence: 1, Intent: "interaction", SubIntent: "media_context_follow_up", RequestMode: "answer",
		Text: "这啥你知道不", Output: "text_reply", SourceRefs: []string{"U2", "U1"}, SourceMessageIDs: []int64{84, 83},
	}}
	inputs, _, err := buildRuntimeTaskInputs(plans, question.ID, []models.Message{image, question}, 1, 10)
	if err != nil {
		t.Fatalf("build image follow-up task: %v", err)
	}
	if len(inputs) != 1 || inputs[0].SourceMessageID != question.ID {
		t.Fatalf("image follow-up must keep the text question as primary: %#v", inputs)
	}
	var bindings contracts.TaskSourceBindingsV1
	if err := json.Unmarshal([]byte(inputs[0].SourceBindingsJSON), &bindings); err != nil {
		t.Fatalf("decode source bindings: %v", err)
	}
	if bindings.PrimaryMessageID != question.ID || len(bindings.Bindings) != 2 ||
		bindings.Bindings[0].MessageID != question.ID || bindings.Bindings[1].MessageID != image.ID ||
		bindings.Bindings[1].AnalysisRevision != 1 || bindings.Bindings[1].End != len([]rune(observation)) {
		t.Fatalf("ready image observation binding was not preserved: %#v", bindings)
	}
}

func TestImageReferentsProduceDistinctRuntimeTaskKeys(t *testing.T) {
	messages := []models.Message{
		{ID: 91, MessageType: enums.IMMessageTypeImage, Content: "a.jpg", Payload: `{"mediaText":"图片 A 的内容","mediaUnderstandingStatus":"understood"}`},
		{ID: 92, MessageType: enums.IMMessageTypeText, Content: "这是什么"},
		{ID: 93, MessageType: enums.IMMessageTypeImage, Content: "b.jpg", Payload: `{"mediaText":"图片 B 的内容","mediaUnderstandingStatus":"understood"}`},
		{ID: 94, MessageType: enums.IMMessageTypeText, Content: "这是什么"},
	}
	plans := []callbacks.ReplyTaskPlanTraceData{
		{Sequence: 1, Intent: "interaction", SubIntent: "media_context_follow_up", RequestMode: "answer", Text: "这是什么", Output: "text_reply", SourceMessageIDs: []int64{92, 91}},
		{Sequence: 2, Intent: "interaction", SubIntent: "media_context_follow_up", RequestMode: "answer", Text: "这是什么", Output: "text_reply", SourceMessageIDs: []int64{94, 93}},
	}
	inputs, _, err := buildRuntimeTaskInputs(plans, 94, messages, 1, 2)
	if err != nil {
		t.Fatalf("build image reference tasks: %v", err)
	}
	if len(inputs) != 2 {
		t.Fatalf("expected two image reference tasks, got %#v", inputs)
	}
	if inputs[0].ReferenceFingerprint == "" || inputs[1].ReferenceFingerprint == "" ||
		inputs[0].ReferenceFingerprint == inputs[1].ReferenceFingerprint {
		t.Fatalf("distinct images must keep distinct reference fingerprints: %#v", inputs)
	}
	firstKey := services.AIReplyTurnTaskService.StableTaskKey(inputs[0])
	secondKey := services.AIReplyTurnTaskService.StableTaskKey(inputs[1])
	if firstKey == secondKey {
		t.Fatalf("distinct images reused task key %q", firstKey)
	}
}

func TestIntentV2AdjacentContextKeepsTwoPriorMessages(t *testing.T) {
	messages := []models.Message{
		{ID: 71, Content: "有咖啡吗"},
		{ID: 72, Content: "停车场在哪"},
		{ID: 73, Content: "不是咖啡，我问的是早餐"},
	}
	selected := withIntentV2AdjacentContext(messages, map[int64]struct{}{73: {}}, 2)
	if len(selected) != 3 || selected[0].ID != 71 || selected[1].ID != 72 || selected[2].ID != 73 {
		t.Fatalf("correction envelope lost adjacent context: %#v", selected)
	}
}

func TestResolveRuntimeTaskRelationTargetsUsesSharedSourceSpan(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AIReplyTurnTask{}); err != nil {
		t.Fatal(err)
	}
	turn := &models.AIReplyTurn{ID: 90, TenantID: 1, Version: 2}
	coffee := models.AIReplyTurnTask{
		ID: 91, TenantID: 1, TurnID: turn.ID, IntroducedVersion: 1, SourceMessageID: 81,
		TaskKey: "task-coffee-shared-source", TaskType: enums.AIReplyTurnTaskTypeKnowledge,
		SourceSpanStart: 0, SourceSpanEnd: 4, Status: enums.AIReplyTurnTaskStatusCommitted,
	}
	parking := models.AIReplyTurnTask{
		ID: 92, TenantID: 1, TurnID: turn.ID, IntroducedVersion: 1, SourceMessageID: 81,
		TaskKey: "task-parking-shared-source", TaskType: enums.AIReplyTurnTaskTypeKnowledge,
		SourceSpanStart: 5, SourceSpanEnd: 10, Status: enums.AIReplyTurnTaskStatusCommitted,
	}
	if err := db.Create(&coffee).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&parking).Error; err != nil {
		t.Fatal(err)
	}
	bindings, err := json.Marshal(contracts.TaskSourceBindingsV1{
		SchemaVersion: contracts.TaskSourceBindingsV1SchemaVersion, PrimaryMessageID: 82,
		Bindings: []contracts.TaskSourceBindingItemV1{{MessageID: 82}, {MessageID: 81}},
	})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []services.AIReplyTurnTaskInput{{
		SourceMessageID: 82, RelationType: "correction", SourceBindingsJSON: string(bindings),
	}}
	inputs = resolveRuntimeTaskRelationTargetsDB(db, turn, []models.Message{
		{ID: 81, MessageType: enums.IMMessageTypeText, Content: "有咖啡吗，停车场在哪"},
		{ID: 82, MessageType: enums.IMMessageTypeText, Content: "不是咖啡，我问的是早餐"},
	}, inputs)
	if inputs[0].RelatedTaskID != coffee.ID {
		t.Fatalf("correction target=%d want coffee task=%d", inputs[0].RelatedTaskID, coffee.ID)
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
