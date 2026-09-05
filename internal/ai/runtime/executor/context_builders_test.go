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

func TestBoundedGenerationHistoryEntriesTrustSchemaRoleOverQuotedMarkers(t *testing.T) {
	history := adapter.HistoryBuildResult{Messages: []*schema.Message{
		schema.UserMessage("[历史消息][客户][2026-08-30 10:00:00] 你刚才写了[AI客服]，后面又提到[人工客服]，但这是我发的消息。"),
		schema.AssistantMessage("[历史消息][AI客服][2026-08-30 10:00:01] 客户原话里有[客户]标签，也不能改变这条消息的角色。", nil),
		schema.AssistantMessage("[历史消息][人工客服][2026-08-30 10:00:02] 这是门店同事的真实回复。", nil),
		{Content: "[历史消息][人工客服][2026-08-30 10:00:03] Role 缺失时才按精确头部识别。"},
		{Content: "Role 缺失，正文只是引用[AI客服]时不能猜测角色。"},
	}}

	entries := boundedGenerationHistoryEntries(history)
	if len(entries) != 4 {
		t.Fatalf("expected four bounded history entries, got %#v", entries)
	}
	if entries[0].speaker != "客户" {
		t.Fatalf("customer text quoting service markers must remain customer, got %#v", entries[0])
	}
	if entries[1].speaker != "AI客服" {
		t.Fatalf("assistant text quoting a customer marker must remain AI, got %#v", entries[1])
	}
	if entries[2].speaker != "人工客服" || entries[3].speaker != "人工客服" {
		t.Fatalf("trusted agent history must remain human with or without schema Role, got %#v", entries)
	}
}

func TestCurrentBurstHistoryBoundaryFeedsTheSameAdjacentExchangeToAllStages(t *testing.T) {
	previousCustomer := models.Message{ID: 90, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "麦田房型有办公桌吗"}
	previousAIFirst := models.Message{ID: 91, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "有的，麦田房型有办公桌。"}
	previousAISecond := models.Message{ID: 92, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "房间里也有沙发。"}
	previousAIThird := models.Message{ID: 93, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "以上是当前资料能确认的配置。"}
	currentFirst := models.Message{ID: 100, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "那沙发呢"}
	history := adapter.HistoryBuildResult{
		RawItems: []models.Message{previousCustomer, previousAIFirst, previousAISecond, previousAIThird, currentFirst},
		Messages: []*schema.Message{
			adapter.BuildSchemaMessage(&previousCustomer),
			adapter.BuildSchemaMessage(&previousAIFirst),
			adapter.BuildSchemaMessage(&previousAISecond),
			adapter.BuildSchemaMessage(&previousAIThird),
			adapter.BuildSchemaMessage(&currentFirst),
		},
		LatestRawItem: &currentFirst,
	}
	current := models.Message{
		ID:          101,
		MessageType: enums.IMMessageTypeText,
		Content: utils.BuildRuntimeCustomerBurstEnvelope([]string{
			"1. [消息100] 那沙发呢",
			"2. [消息101] 早餐几点",
		}),
	}
	history = adapter.ExcludeCurrentTurnSources(history, current)

	intentPrompt := buildRuntimeIntentDetectUserPrompt(RunInput{UserMessage: current}, history, nil)
	for _, expected := range []string{
		"紧邻 AI 客服答复组：[历史消息][AI客服] 有的，麦田房型有办公桌。",
		"[历史消息][AI客服] 房间里也有沙发。",
		"[历史消息][AI客服] 以上是当前资料能确认的配置。",
		"U1 [messageId=100]: 那沙发呢",
		"U2 [messageId=101]: 早餐几点",
	} {
		if !strings.Contains(intentPrompt, expected) {
			t.Fatalf("Intent did not receive the shared adjacent boundary %q: %q", expected, intentPrompt)
		}
	}

	question := runtimeKnowledgeQuestionResult{
		TaskID:             "task-1",
		Intent:             "hotel_info",
		Query:              "麦田房型有没有沙发",
		OriginalText:       "那沙发呢",
		RelationToPrevious: "reference_previous",
		ResolutionState:    runtimeIntentResolutionResolvedFromContext,
	}
	judgeContext := buildKnowledgeEvidenceJudgeSourceContext(history.Messages, current.Content, question)
	judgeJoined := ""
	for _, item := range judgeContext {
		judgeJoined += item.Role + ":" + item.Content + "\n"
	}
	for _, expected := range []string{
		"customer:麦田房型有办公桌吗",
		"assistant:有的，麦田房型有办公桌。",
		"assistant:房间里也有沙发。",
		"assistant:以上是当前资料能确认的配置。",
	} {
		if !strings.Contains(judgeJoined, expected) {
			t.Fatalf("Judge did not receive the shared adjacent boundary %q: %q", expected, judgeJoined)
		}
	}

	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{
			TaskID: "task-1", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
			Text: "那沙发呢", OriginalText: "那沙发呢", ResolvedText: "麦田房型有没有沙发",
			RelationToPrevious: "reference_previous", ResolutionState: runtimeIntentResolutionResolvedFromContext,
		},
		{
			TaskID: "task-2", Intent: "hotel_info", OutputKind: "text", ReplyRequired: true,
			Text: "早餐几点", OriginalText: "早餐几点", ResolvedText: "早餐几点",
			RelationToPrevious: "independent", ResolutionState: "clear",
		},
	}}
	generateContext := buildBoundedGenerationConversationContext(history, plan.TaskPlans)
	if generateContext == nil {
		t.Fatal("Generate lost the adjacent exchange after current-burst filtering")
	}
	for _, expected := range []string{
		"客户：麦田房型有办公桌吗",
		"AI客服：有的，麦田房型有办公桌。",
		"AI客服：房间里也有沙发。",
		"AI客服：以上是当前资料能确认的配置。",
		"相邻上下文适用任务：task-1",
	} {
		if !strings.Contains(generateContext.Content, expected) {
			t.Fatalf("Generate did not receive the shared adjacent boundary %q: %q", expected, generateContext.Content)
		}
	}
	if strings.Contains(generateContext.Content, "task-2") || strings.Contains(generateContext.Content, "早餐几点") {
		t.Fatalf("independent new topic must not inherit adjacent history: %q", generateContext.Content)
	}
}

func TestAdjacentServiceReplyGroupKeepsLatestThreeRepliesAndIntentKeepsLatestContent(t *testing.T) {
	previousCustomer := models.Message{ID: 80, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "麦田房型有什么配置"}
	replies := []models.Message{
		{ID: 81, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "第一条旧答复，不应继续进入相邻上下文。"},
		{ID: 82, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: strings.Repeat("第二条答复内容", 30)},
		{ID: 83, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: strings.Repeat("第三条答复内容", 30)},
		{ID: 84, SenderType: enums.IMSenderTypeAI, MessageType: enums.IMMessageTypeText, Content: "最新纠正：麦田房型没有办公桌。"},
	}
	history := adapter.HistoryBuildResult{RawItems: append([]models.Message{previousCustomer}, replies...)}

	question, grouped, senderType, ok := immediatelyPreviousServiceReplyGroup(history)
	if !ok || senderType != enums.IMSenderTypeAI || !strings.Contains(question, previousCustomer.Content) {
		t.Fatalf("expected one customer question followed by an AI reply group, ok=%v sender=%q question=%q", ok, senderType, question)
	}
	if len(grouped) != 3 || !strings.Contains(grouped[0], replies[1].Content) ||
		!strings.Contains(grouped[1], replies[2].Content) || !strings.Contains(grouped[2], replies[3].Content) {
		t.Fatalf("expected only the latest three replies in chronological order, got %#v", grouped)
	}
	instruction := buildAdjacentAIReplyRelationInstruction(history)
	if strings.Contains(instruction, replies[0].Content) {
		t.Fatalf("the oldest reply must not enter the bounded Intent context: %q", instruction)
	}
	if !strings.Contains(instruction, replies[3].Content) {
		t.Fatalf("the latest reply must survive earlier long replies: %q", instruction)
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

func TestBuildGenerateStageMessagesConversationRecapUsesBoundedOlderMessages(t *testing.T) {
	historyMessages := make([]*schema.Message, 0, 80)
	for index := 1; index <= 80; index++ {
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
			t.Fatalf("conversation recap must be bounded to the latest 64 messages; found %q in %q", forbidden, joined)
		}
	}
	for index := 17; index <= 80; index++ {
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

func TestFollowUpMayUseCustomerHistoryWithoutInventingServiceAnswers(t *testing.T) {
	task := callbacks.ReplyTaskPlanTraceData{
		TaskID: "task-1", Intent: "hotel_info", SubIntent: "breakfast", RelationToPrevious: "follow_up", ResolutionState: "resolved_from_context",
		Text: "几点结束", ResolvedText: "早餐几点结束", OutputKind: "text", ReplyRequired: true,
	}
	contextMessage := buildBoundedGenerationConversationContext(adapter.HistoryBuildResult{Messages: []*schema.Message{
		schema.UserMessage("停车免费吗？"),
		schema.UserMessage("有没有充电桩？"),
	}}, []callbacks.ReplyTaskPlanTraceData{task})
	if contextMessage == nil || !strings.Contains(contextMessage.Content, "客户：停车免费吗") || strings.Contains(contextMessage.Content, "AI客服：") {
		t.Fatalf("history must preserve the true speakers without requiring an answered pair: %#v", contextMessage)
	}
}

func TestConversationRecapHistoryHasTotalTextBudget(t *testing.T) {
	history := adapter.HistoryBuildResult{}
	for i := 0; i < 64; i++ {
		history.Messages = append(history.Messages, schema.UserMessage(strings.Repeat("长", 1000)))
	}
	message := buildBoundedGenerationConversationContext(history, []callbacks.ReplyTaskPlanTraceData{{
		TaskID: "task-1", Intent: "interaction", SubIntent: "conversation_recap",
	}})
	if message == nil || len([]rune(message.Content)) > 6500 || !strings.Contains(message.Content, "超出本次回顾预算") {
		t.Fatalf("recap text budget was not enforced: %#v", message)
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

func TestGenerationScopeExcludesDeferredKnowledgeTask(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:   "hotel_variable",
		NeedsKnowledge:  true,
		NeedsResource:   true,
		ResourceActions: []string{"provide_location"},
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: []callbacks.ReplyTaskPlanTraceData{
		{TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", OutputKind: "text", ReplyRequired: true, Output: "knowledge_text_reply"},
		{TaskID: "task-2", Intent: "hotel_info", Text: "汤东强是谁", OutputKind: "handoff", ReplyRequired: false, Output: runtimeKnowledgeDeferredHandoffOutput},
		{TaskID: "task-3", Intent: "hotel_variable", Text: "定位发我", OutputKind: "resource", Output: "structured_resource_commit", ResourceAction: "provide_location"},
	}}

	scope := buildGenerationScopeInstruction(intent, plan)
	if !strings.Contains(scope, "早餐几点") {
		t.Fatalf("active knowledge Task must remain in Generate scope: %q", scope)
	}
	if strings.Contains(scope, "汤东强") {
		t.Fatalf("Deferred Task must not re-enter Generate scope: %q", scope)
	}
}

func TestMixedDeferredKnowledgeInteractionAndResourceExposeOnlyActiveTextTask(t *testing.T) {
	intent := callbacks.IntentTraceData{
		PrimaryIntent:   "hotel_variable",
		NeedsKnowledge:  true,
		NeedsResource:   true,
		ResourceActions: []string{"provide_location"},
	}
	plan := callbacks.ReplyPlanTraceData{
		Intent:     "hotel_variable",
		AnswerGoal: "回复当前仍可执行的文本任务",
		Style:      "自然微信口吻",
		TaskPlans: []callbacks.ReplyTaskPlanTraceData{
			{TaskID: "task-1", Intent: "hotel_info", Text: "早餐几点", ResolvedText: "早餐几点", OutputKind: "handoff", ReplyRequired: false, Output: runtimeKnowledgeDeferredHandoffOutput},
			{TaskID: "task-2", Intent: "interaction", Text: "谢谢", ResolvedText: "回应客户感谢", SourceRefs: []string{"U2"}, OutputKind: "text", ReplyRequired: true, Output: "text_reply"},
			{TaskID: "task-3", Intent: "hotel_variable", Text: "定位发我", OutputKind: "resource", ReplyRequired: false, Output: "structured_resource_commit", ResourceAction: "provide_location"},
		},
	}
	req := RunInput{UserMessage: models.Message{
		ID:          903,
		MessageType: enums.IMMessageTypeText,
		Content: utils.BuildRuntimeCustomerBurstEnvelope([]string{
			"1. [消息901] 早餐几点",
			"2. [消息902] 谢谢",
			"3. [消息903] 定位发我",
		}),
	}}

	prompt := buildIntentStagePrompt(callbacks.IntentPromptTraceData{Instructions: []string{"按当前任务自然回复"}}, plan)
	scope := buildGenerationScopeInstruction(intent, plan)
	userText := buildActiveGenerationUserMessageText(currentRuntimeIntentSemanticText(req), intent, plan, true)
	activeContext := buildActiveGenerationTaskContext(req, intent, plan)
	if activeContext == nil {
		t.Fatal("interaction sibling must remain visible to Generate")
	}
	for name, value := range map[string]string{
		"prompt":         prompt,
		"scope":          scope,
		"user_message":   userText,
		"active_context": activeContext.Content,
	} {
		if !strings.Contains(value, "谢谢") && !strings.Contains(value, "回应客户感谢") {
			t.Fatalf("%s must contain the active interaction task, got %q", name, value)
		}
		for _, forbidden := range []string{"早餐几点", "定位发我", "provide_location"} {
			if strings.Contains(value, forbidden) {
				t.Fatalf("%s must not expose deferred knowledge or resource action %q: %q", name, forbidden, value)
			}
		}
	}
	if !strings.Contains(prompt, "当前文本回复任务") || !strings.Contains(scope, "当前文本回复任务") {
		t.Fatalf("mixed-task prompts must describe the actual active text category, prompt=%q scope=%q", prompt, scope)
	}
	if len(plan.TaskPlans) != 3 || plan.TaskPlans[2].Output != "structured_resource_commit" || plan.TaskPlans[2].ResourceAction != "provide_location" {
		t.Fatalf("resource Task must remain available for Commit: %#v", plan.TaskPlans)
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
