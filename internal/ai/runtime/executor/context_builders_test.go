package executor

import (
	"strings"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

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
