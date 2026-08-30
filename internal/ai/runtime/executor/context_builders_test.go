package executor

import (
	"fmt"
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"

	"github.com/cloudwego/eino/schema"
)

func TestBuildGenerateStageMessagesRemovesRawHistoryAndKeepsActiveTaskContext(t *testing.T) {
	memory := schema.SystemMessage("以下是本会话更早消息的压缩记忆：旧停车问题")
	oldCustomer := schema.UserMessage("[历史消息][客户][2026-08-26 09:00:00] 停车免费吗")
	oldAI := schema.AssistantMessage("[历史消息][AI客服][2026-08-26 09:00:01] 停车免费。", nil)
	knowledgePolicy := schema.SystemMessage("知识库回答约束：只能依据 Judge 已确认事实回答。")
	rawKnowledge := schema.SystemMessage("【原始候选片段】门店有机器人，因此机器人可以送到房间。")
	mediaContext := schema.SystemMessage("本轮图片/文件上下文：图片里明确可见两瓶矿泉水。")

	req := RunInput{UserMessage: models.Message{Content: "客人刚才连续发了几条消息。请按顺序合并理解，最后统一回复当前真正的问题：\n1. [消息] 我问的是这两瓶\n2. [消息] 这两瓶是不是都免费"}}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID:        "task-1",
			Intent:        "hotel_info",
			OutputKind:    "text",
			ReplyRequired: true,
			OriginalText:  "这两瓶是不是都免费",
			ResolvedText:  "房间内的两瓶矿泉水是不是都免费",
			SourceRefs:    []string{"U2", "U1"},
			SupportedFacts: []callbacks.KnowledgeEvidenceFactTraceData{
				{FactID: "F1", Aspect: "quantity", Statement: "房间内有两瓶矿泉水", CriticalValues: []string{"两瓶"}},
				{FactID: "F2", Aspect: "price", Statement: "房间内矿泉水免费", CriticalValues: []string{"免费"}},
			},
			MissingAspects: []string{"配送范围"},
		},
		{
			TaskID:        "task-2",
			Intent:        "hotel_variable",
			OutputKind:    "resource",
			ReplyRequired: false,
			OriginalText:  "发小程序",
			ResolvedText:  "发送入住小程序",
		},
	}}
	history := adapter.HistoryBuildResult{
		MemoryMessage: memory,
		Messages:      []*schema.Message{oldCustomer, oldAI},
	}

	got := buildGenerateStageMessages(
		req,
		history,
		callbacks.IntentTraceData{PrimaryIntent: "hotel_info"},
		plan,
		[]*schema.Message{memory, oldCustomer, oldAI, knowledgePolicy, rawKnowledge, mediaContext},
		[]*schema.Message{rawKnowledge},
	)
	joined := joinSchemaMessageContents(got)

	for _, forbidden := range []string{"旧停车问题", "停车免费吗", "停车免费。", "发送入住小程序", "发小程序", "机器人可以送到房间"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("Generate context must not contain %q, got %q", forbidden, joined)
		}
	}
	for _, expected := range []string{
		"知识库回答约束：只能依据 Judge 已确认事实回答。",
		"本轮图片/文件上下文：图片里明确可见两瓶矿泉水。",
		"primary 来源：这两瓶是不是都免费",
		"context 来源：我问的是这两瓶",
		"自包含问题：房间内的两瓶矿泉水是不是都免费",
		"已确认事实 F1：房间内有两瓶矿泉水；必要值：两瓶",
		"已确认事实 F2：房间内矿泉水免费；必要值：免费",
		"尚未确认方面（禁止自行补全）：配送范围",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Generate context missing %q, got %q", expected, joined)
		}
	}
}

func TestBuildGenerateStageMessagesNeverExposesRawKnowledgeWithoutJudgeFacts(t *testing.T) {
	rawKnowledge := schema.SystemMessage("【旧版知识片段】早餐时间是 7:00-10:00。")
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		Intent: "hotel_info",
		Text:   "早餐几点",
		Output: "knowledge_text_reply",
	}}}

	got := buildGenerateStageMessages(
		RunInput{UserMessage: models.Message{Content: "早餐几点"}},
		adapter.HistoryBuildResult{},
		callbacks.IntentTraceData{},
		plan,
		[]*schema.Message{rawKnowledge},
		[]*schema.Message{rawKnowledge},
	)
	joined := joinSchemaMessageContents(got)
	if strings.Contains(joined, "早餐时间是 7:00-10:00") {
		t.Fatalf("Generate must not bypass Judge with raw knowledge context, got %q", joined)
	}
	if !strings.Contains(joined, "自包含问题：早餐几点") {
		t.Fatalf("active task must remain visible after raw knowledge is removed, got %q", joined)
	}
}

func TestBuildGenerateStageMessagesAddsAdjacentContextOnlyForDependentTasks(t *testing.T) {
	tests := []struct {
		name        string
		currentText string
		history     []*schema.Message
		task        callbacks.ReplyTaskPlanTraceData
		expected    []string
	}{
		{
			name:        "short confirmation answers the ai question",
			currentText: "是的啊",
			history: []*schema.Message{
				schema.UserMessage("[历史消息][客户][2026-08-27 23:39:00] 我是开电车来的你懂我意思吗"),
				schema.AssistantMessage("[历史消息][AI客服][2026-08-27 23:39:02] 明白，您是问咱们这儿有没有充电桩吗？", nil),
			},
			task: callbacks.ReplyTaskPlanTraceData{
				TaskID: "task-1", Intent: "hotel_info", SubIntent: "parking", OutputKind: "text", ReplyRequired: true,
				Text: "是的啊", ResolvedText: "酒店停车场有没有充电桩", RelationToPrevious: "clarification_answer", ResolutionState: "resolved_from_context",
			},
			expected: []string{"客户：我是开电车来的你懂我意思吗", "AI客服：明白，您是问咱们这儿有没有充电桩吗？"},
		},
		{
			name:        "slot value keeps the question that requested it",
			currentText: "吴朝伟",
			history: []*schema.Message{
				schema.UserMessage("[历史消息][客户][2026-08-27 23:34:00] 帮我查一下入住信息"),
				schema.AssistantMessage("[历史消息][AI客服][2026-08-27 23:34:02] 方便说下入住人姓名吗？", nil),
			},
			task: callbacks.ReplyTaskPlanTraceData{
				TaskID: "task-1", Intent: "interaction", SubIntent: "clarify", OutputKind: "text", ReplyRequired: true,
				Text: "吴朝伟", ResolvedText: "入住人姓名是吴朝伟", RelationToPrevious: "clarification_answer", ResolutionState: "resolved_from_context",
			},
			expected: []string{"客户：帮我查一下入住信息", "AI客服：方便说下入住人姓名吗？"},
		},
		{
			name:        "elliptical reference keeps the nearest business exchange",
			currentText: "玩的勒",
			history: []*schema.Message{
				schema.UserMessage("[历史消息][客户][2026-08-27 23:36:00] 附近有吃的没"),
				schema.AssistantMessage("[历史消息][AI客服][2026-08-27 23:36:02] 附近有小吃和餐馆。", nil),
			},
			task: callbacks.ReplyTaskPlanTraceData{
				TaskID: "task-1", Intent: "hotel_info", SubIntent: "surrounding_facilities", OutputKind: "text", ReplyRequired: true,
				Text: "玩的勒", ResolvedText: "酒店附近有什么可以玩的地方", RelationToPrevious: "reference_previous", ResolutionState: "resolved_from_context",
			},
			expected: []string{"客户：附近有吃的没", "AI客服：附近有小吃和餐馆。"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := adapter.HistoryBuildResult{Messages: tt.history}
			plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{tt.task}}
			got := buildGenerateStageMessages(
				RunInput{UserMessage: models.Message{Content: tt.currentText}},
				history,
				callbacks.IntentTraceData{},
				plan,
				append([]*schema.Message(nil), tt.history...),
				nil,
			)
			joined := joinSchemaMessageContents(got)
			for _, expected := range tt.expected {
				if !strings.Contains(joined, expected) {
					t.Fatalf("bounded Generate context missing %q, got %q", expected, joined)
				}
			}
			if strings.Contains(joined, "[历史消息]") {
				t.Fatalf("bounded context must use safe role labels instead of internal history markers, got %q", joined)
			}
		})
	}
}

func TestBuildGenerateStageMessagesDoesNotInjectOldHistoryForCurrentTurnSourceContext(t *testing.T) {
	history := []*schema.Message{
		schema.UserMessage("[历史消息][客户] 之前问过早餐几点"),
		schema.AssistantMessage("[历史消息][AI客服] 早餐暂不提供。", nil),
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", SubIntent: "parking", OutputKind: "text", ReplyRequired: true,
		Text: "我开电车来的你懂我意思吗", ResolvedText: "酒店停车场有没有电车充电桩",
		RelationToPrevious: "independent", ResolutionState: "resolved_from_context", SourceRefs: []string{"U3", "U2"},
	}}}

	got := buildGenerateStageMessages(
		RunInput{UserMessage: models.Message{Content: "我开电车来的你懂我意思吗"}},
		adapter.HistoryBuildResult{Messages: history},
		callbacks.IntentTraceData{},
		plan,
		append([]*schema.Message(nil), history...),
		nil,
	)
	joined := joinSchemaMessageContents(got)
	if strings.Contains(joined, "之前问过早餐") || strings.Contains(joined, "早餐暂不提供") {
		t.Fatalf("current-turn sourceRefs already resolve the task; old history must not be injected: %q", joined)
	}
	if !strings.Contains(joined, "酒店停车场有没有电车充电桩") {
		t.Fatalf("the self-contained current task must remain in Generate input: %q", joined)
	}
}

func TestBuildGenerateStageMessagesConversationRecapUsesAtMostEightRecentMessages(t *testing.T) {
	historyMessages := make([]*schema.Message, 0, 10)
	for index := 1; index <= 10; index++ {
		content := "[历史消息][客户] 历史问题" + fmt.Sprint(index)
		if index%2 == 0 {
			historyMessages = append(historyMessages, schema.AssistantMessage(strings.Replace(content, "[客户]", "[AI客服]", 1), nil))
			continue
		}
		historyMessages = append(historyMessages, schema.UserMessage(content))
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "interaction", SubIntent: "conversation_recap", OutputKind: "text", ReplyRequired: true,
		Text: "刚刚都问你什么了？", ResolvedText: "回顾刚才的会话内容",
	}}}

	got := buildGenerateStageMessages(
		RunInput{UserMessage: models.Message{Content: "刚刚都问你什么了？"}},
		adapter.HistoryBuildResult{Messages: historyMessages},
		callbacks.IntentTraceData{},
		plan,
		append([]*schema.Message(nil), historyMessages...),
		nil,
	)
	joined := joinSchemaMessageContents(got)
	for _, forbidden := range []string{"客户：历史问题1\n", "AI客服：历史问题2\n"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("conversation recap must be bounded to the latest eight messages; found %q in %q", forbidden, joined)
		}
	}
	for index := 3; index <= 10; index++ {
		if expected := "历史问题" + fmt.Sprint(index); !strings.Contains(joined, expected) {
			t.Fatalf("conversation recap missing recent message %q in %q", expected, joined)
		}
	}
}

func TestConversationRecapContextModeSupportsSubtypeAndExplicitQuestion(t *testing.T) {
	for name, task := range map[string]callbacks.ReplyTaskPlanTraceData{
		"subtype": {
			SubIntent: "conversation_recap",
			Text:      "帮我回顾一下",
		},
		"explicit question": {
			SubIntent: "clarify",
			Text:      "刚才聊了什么？",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := generationConversationContextMode(task); got != "recap" {
				t.Fatalf("expected recap context mode, got %q", got)
			}
		})
	}
}

func TestFollowUpReceivesAdjacentBoundedConversationContext(t *testing.T) {
	task := callbacks.ReplyTaskPlanTraceData{
		TaskID: "task-1", Intent: "hotel_info", SubIntent: "breakfast", RelationToPrevious: "follow_up", ResolutionState: "clear",
		Text: "几点结束", ResolvedText: "早餐几点结束", OutputKind: "text", ReplyRequired: true,
	}
	if got := generationConversationContextMode(task); got != "adjacent" {
		t.Fatalf("follow_up must receive adjacent bounded context, got %q", got)
	}
	contextMessage := buildBoundedGenerationConversationContext(adapter.HistoryBuildResult{Messages: []*schema.Message{
		schema.UserMessage("有早餐吗？"),
		schema.AssistantMessage("有的。", nil),
	}}, []callbacks.ReplyTaskPlanTraceData{task})
	if contextMessage == nil || !strings.Contains(contextMessage.Content, "有早餐吗") || !strings.Contains(contextMessage.Content, "有的") {
		t.Fatalf("follow_up bounded context must contain the adjacent pair, got %#v", contextMessage)
	}
}

func TestFollowUpDoesNotTreatTwoWaitingCustomerMessagesAsAdjacentServiceContext(t *testing.T) {
	task := callbacks.ReplyTaskPlanTraceData{
		TaskID: "task-1", Intent: "hotel_info", SubIntent: "breakfast", RelationToPrevious: "follow_up", ResolutionState: "resolved_from_context",
		Text: "几点结束", ResolvedText: "早餐几点结束", OutputKind: "text", ReplyRequired: true,
	}
	contextMessage := buildBoundedGenerationConversationContext(adapter.HistoryBuildResult{Messages: []*schema.Message{
		schema.UserMessage("停车免费吗？"),
		schema.UserMessage("有没有充电桩？"),
	}}, []callbacks.ReplyTaskPlanTraceData{task})
	if contextMessage != nil {
		t.Fatalf("waiting customer messages are current work, not an adjacent customer/service answer pair: %q", contextMessage.Content)
	}
}

func TestSetCurrentTurnSourcesTracePersistsPhysicalIdentity(t *testing.T) {
	collector := callbacks.NewRuntimeTraceCollector()
	message := models.Message{
		ID: 702, MessageType: enums.IMMessageTypeText,
		Content: utils.BuildRuntimeCustomerBurstEnvelope([]string{
			"1. [文字701] 有早餐吗？",
			"2. [语音702] 几点结束？",
		}),
	}
	setCurrentTurnSourcesTrace(collector, message)
	got := collector.Data.Input.CurrentTurnSources
	if len(got) != 2 || got[0].Ref != "U1" || got[0].MessageID != 701 || got[0].MessageType != string(enums.IMMessageTypeText) || got[0].Text != "有早餐吗？" ||
		got[1].Ref != "U2" || got[1].MessageID != 702 || got[1].MessageType != string(enums.IMMessageTypeVoice) || got[1].Text != "几点结束？" {
		t.Fatalf("runtime Trace must persist URef/messageId/messageType/text, got %#v", got)
	}
}

func TestBuildGenerateStageMessagesKeepsIndependentKnowledgeTaskIsolated(t *testing.T) {
	oldCustomer := schema.UserMessage("[历史消息][客户] 停车免费吗")
	oldAI := schema.AssistantMessage("[历史消息][AI客服] 停车免费。", nil)
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "hotel_info", SubIntent: "breakfast", OutputKind: "text", ReplyRequired: true,
		Text: "早餐几点", ResolvedText: "早餐几点", RelationToPrevious: "independent", ResolutionState: "clear",
	}}}

	got := buildGenerateStageMessages(
		RunInput{UserMessage: models.Message{Content: "早餐几点"}},
		adapter.HistoryBuildResult{Messages: []*schema.Message{oldCustomer, oldAI}},
		callbacks.IntentTraceData{},
		plan,
		[]*schema.Message{oldCustomer, oldAI},
		nil,
	)
	joined := joinSchemaMessageContents(got)
	if strings.Contains(joined, "停车免费") || strings.Contains(joined, "【当前任务所需的有界会话上下文】") {
		t.Fatalf("independent knowledge task must not receive old conversation content, got %q", joined)
	}
	if !strings.Contains(joined, "自包含问题：早餐几点") {
		t.Fatalf("independent active task must remain available, got %q", joined)
	}
}

func TestStructuredGenerateInstructionsScopePlainTextRulesToContent(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:   "hotel_variable",
		NeedsKnowledge:  true,
		NeedsResource:   true,
		ResourceActions: []string{"provide_mini_program"},
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", OutputKind: "text", ReplyRequired: true},
		{TaskID: "task-2", Intent: "hotel_info", Text: "停车收费吗", OutputKind: "text", ReplyRequired: true},
		{TaskID: "task-3", Intent: "hotel_variable", OutputKind: "resource", ResourceAction: "provide_mini_program"},
	}}
	scope := buildGenerationScopeInstruction(intent, plan)
	boundary := buildCurrentTurnBoundaryInstructionForReplyPlan(
		RunInput{UserMessage: models.Message{MessageType: enums.IMMessageTypeText, Content: "早餐几点，停车收费吗，小程序发我"}},
		adapter.HistoryBuildResult{},
		intent,
		plan,
	)
	for name, value := range map[string]string{"scope": scope, "boundary": boundary} {
		if strings.Contains(value, "最终回复只输出给客人的话") || strings.Contains(value, "最终文本只输出给客人的话") {
			t.Fatalf("%s must not ask structured Generate for a bare customer reply: %s", name, value)
		}
		if !strings.Contains(value, "replyParts") || !strings.Contains(value, "content") {
			t.Fatalf("%s must scope customer-facing text to replyParts content: %s", name, value)
		}
	}
}

func TestBuildActiveGenerationTaskContextSupportsLegacyReplyPlan(t *testing.T) {
	req := RunInput{UserMessage: models.Message{Content: "早餐几点"}}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		Intent: "hotel_info",
		Text:   "早餐几点",
		Output: "knowledge_text_reply",
	}}}

	message := buildActiveGenerationTaskContext(req, callbacks.IntentTraceData{}, plan)
	if message == nil {
		t.Fatal("expected legacy text task to produce active Generate context")
	}
	for _, expected := range []string{"任务 task-1", "primary 来源：早餐几点", "自包含问题：早餐几点"} {
		if !strings.Contains(message.Content, expected) {
			t.Fatalf("legacy task context missing %q, got %q", expected, message.Content)
		}
	}
	if got := buildActiveGenerationUserMessageText("早餐几点", callbacks.IntentTraceData{}, plan, false); got != "早餐几点" {
		t.Fatalf("expected legacy task text, got %q", got)
	}
}

func TestBuildActiveGenerationUserMessageUsesActiveReplyPlanWithoutDeferredHandoff(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:  "hotel_info",
		NeedsKnowledge: true,
		IntentTasks: []callbacks.IntentTaskTraceData{
			{Intent: "hotel_info", Text: "旧问题一", ResolvedText: "旧问题一", NeedsKnowledge: true},
			{Intent: "hotel_info", Text: "旧问题二", ResolvedText: "旧问题二", NeedsKnowledge: true},
		},
	}
	activePlan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{{
		TaskID:        "task-2",
		Intent:        "hotel_info",
		OutputKind:    "text",
		ReplyRequired: true,
		ResolvedText:  "本轮唯一活跃问题",
	}}}

	got := buildActiveGenerationUserMessageText("旧问题一\n旧问题二", intent, activePlan, false)
	if got != "本轮唯一活跃问题" {
		t.Fatalf("Generate user message must follow active ReplyPlan, got %q", got)
	}
}

func TestCurrentTurnBoundaryUsesOnlyActiveReplyTasks(t *testing.T) {
	req := RunInput{UserMessage: models.Message{Content: "早餐几点，机器人能不能送到房间，停车免费吗"}}
	intent := callbacks.IntentTraceData{PrimaryIntent: "hotel_info", NeedsKnowledge: true, IntentTasks: []callbacks.IntentTaskTraceData{
		{Intent: "hotel_info", Text: "早餐几点", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "机器人能不能送到房间", NeedsKnowledge: true},
		{Intent: "hotel_info", Text: "停车免费吗", NeedsKnowledge: true},
	}}
	activePlan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, ResolvedText: "早餐几点"},
		{TaskID: "task-3", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true, ResolvedText: "停车免费吗"},
	}}

	instruction := buildCurrentTurnBoundaryInstructionForReplyPlan(req, adapter.HistoryBuildResult{}, intent, activePlan)
	if strings.Contains(instruction, "机器人能不能送到房间") {
		t.Fatalf("deferred task must not re-enter Generate through the current-turn boundary: %q", instruction)
	}
	for _, expected := range []string{"当前活跃回答任务", "早餐几点", "停车免费吗"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("active boundary missing %q: %q", expected, instruction)
		}
	}
}

func TestRecentUsableMediaTextForGeneratePrefersCompleteTextAndFallsBackToSummary(t *testing.T) {
	history := adapter.HistoryBuildResult{RawItems: []models.Message{{
		SenderType:  enums.IMSenderTypeCustomer,
		MessageType: enums.IMMessageTypeVoice,
		Payload:     `{"mediaText":"早餐几点，停车免费吗，房间有几瓶矿泉水？","mediaSummary":"客户咨询早餐。","mediaUnderstandingStatus":"understood"}`,
	}}}
	if got := recentUsableMediaTextFromHistory(history); got != "早餐几点，停车免费吗，房间有几瓶矿泉水？" {
		t.Fatalf("expected full mediaText without lossy summary, got %q", got)
	}

	history.RawItems[0].Payload = `{"mediaText":"","mediaSummary":"客户咨询早餐和停车。","mediaUnderstandingStatus":"understood"}`
	if got := recentUsableMediaTextFromHistory(history); got != "客户咨询早餐和停车。" {
		t.Fatalf("expected mediaSummary fallback, got %q", got)
	}
}

func joinSchemaMessageContents(messages []*schema.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			parts = append(parts, message.Content)
		}
	}
	return strings.Join(parts, "\n")
}
