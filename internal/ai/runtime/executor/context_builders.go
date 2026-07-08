package executor

import (
	"context"
	"strings"

	"agent-desk/internal/ai/replyengine"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"

	"github.com/cloudwego/eino/schema"
)

func buildRunMessages(ctx context.Context, req RunInput, summary *RunResult, collector *callbacks.RuntimeTraceCollector, gate *KnowledgeAnswerabilityGate) []*schema.Message {
	history := adapter.BuildHistoryMessages(req.Conversation.ID, req.UserMessage.ID, 0)
	if summary != nil {
		summary.HistoryMessageCount = len(history.Messages)
		summary.ContextMemorySource = history.MemorySource
		summary.ContextMemoryMessageCount = history.MemoryItemCount
	}
	if collector != nil {
		collector.Data.Input.HistoryMessageCount = len(history.Messages)
		collector.Data.Input.ContextMemorySource = history.MemorySource
		collector.Data.Input.ContextMemoryMessageCount = history.MemoryItemCount
		collector.Data.Input.KnowledgeBaseIDs = utils.SplitInt64s(req.AIAgent.KnowledgeIDs)
		collector.Data.Input.CurrentUserMessagePreview = preview(req.UserMessage.Content, 120)
	}
	plan := buildRuntimePipelinePlanWithModel(ctx, req, history, nil)
	if collector != nil {
		collector.SetPipeline(plan.Normalize, plan.Intent, plan.PromptSelect, plan.Context, plan.ToolKnowledge, plan.ReplyPlan, plan.Generate, plan.Validate)
	}
	messages := make([]*schema.Message, 0, len(history.Messages)+3)
	if history.MemoryMessage != nil {
		messages = append(messages, history.MemoryMessage)
	}
	messages = append(messages, history.Messages...)
	if instruction := buildRecentMediaContextInstruction(req, history, plan.Intent); strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	if instruction := buildWeatherToolInstruction(plan.Intent); strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	if instruction := buildCurrentTurnBoundaryInstruction(req, plan.Intent); strings.TrimSpace(instruction) != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	if strings.TrimSpace(plan.Prompt) != "" {
		messages = append(messages, schema.SystemMessage(plan.Prompt))
	}
	if !plan.Intent.ShouldReply {
		if summary != nil {
			summary.ReplyText = ""
			summary.SkipReply = true
		}
		return messages
	}
	appendRetrievedContext(ctx, req, plan.Intent, summary, collector, gate, &messages)
	messages = append(messages, schema.UserMessage(strings.TrimSpace(req.UserMessage.Content)))
	return messages
}

func buildCurrentTurnBoundaryInstruction(req RunInput, intent callbacks.IntentTraceData) string {
	currentText := strings.TrimSpace(req.UserMessage.Content)
	if currentText == "" {
		return ""
	}
	parts := []string{
		"当前轮回复边界：最终回复只回答最后这条当前用户消息。",
		"最近原始消息、媒体理解和长期记忆只用于解释指代、补足背景或避免重复询问；如果当前消息是新主题，禁止补答上一轮早餐、停车、投诉、安全、转人工等旧主题。",
		"如果知识库或变量结果与当前问题无关，不能强行拼进回复。",
		"当前消息：" + preview(currentText, 240),
	}
	if intent.PrimaryIntent == "hotel_info" || intent.PrimaryIntent == "service_request" {
		parts = append(parts, "酒店信息/服务请求：只围绕当前问题使用知识库结果，不要把同一会话里的其他酒店问题一起回答。")
	}
	if intent.PrimaryIntent == "hotel_variable" {
		parts = append(parts, "酒店变量：只围绕当前请求的电话、定位、小程序等变量回复；未配置的变量要明确说未配置，不能说让同事发或稍后处理。")
	}
	return strings.Join(parts, "\n")
}

func buildWeatherToolInstruction(intent callbacks.IntentTraceData) string {
	if strings.TrimSpace(intent.SubIntent) != "weather_query" && strings.TrimSpace(intent.ResourceAction) != "get_weather" {
		return ""
	}
	return "当前阶段已识别为天气查询。你必须调用 get_weather 工具获取真实天气后再回复；不要直接说你查不到、以手机天气为准。若当前消息没有明确城市或地点，先简短追问城市；若有城市或地点，直接把它作为 location 调用工具。"
}

func buildRecentMediaContextInstruction(req RunInput, history adapter.HistoryBuildResult, intent callbacks.IntentTraceData) string {
	if strings.TrimSpace(intent.SubIntent) != "media_context_follow_up" {
		return ""
	}
	if isRuntimeMediaMessage(req.UserMessage.MessageType) {
		return ""
	}
	if !replyengine.LooksLikeMediaFollowUp(req.UserMessage.Content) {
		return ""
	}
	mediaText := recentUsableMediaTextFromHistory(history)
	if mediaText == "" {
		if recent := findRecentUsableMediaUnderstanding(req); recent != nil {
			mediaText = strings.TrimSpace(strings.Join([]string{recent.MediaText, recent.MediaSummary}, "\n"))
		}
	}
	if mediaText == "" {
		return ""
	}
	return "本轮图片/文件上下文：当前用户问题是在追问最近一条已解析的图片或文件文本。请直接结合下面内容回答当前问法，不要把图片/文件当成无关历史，不要机械复述整段内容，不要说系统识别。语音仍按既有语转文文本链路处理。\n" + preview(mediaText, 1200)
}

func recentUsableMediaTextFromHistory(history adapter.HistoryBuildResult) string {
	for i := len(history.RawItems) - 1; i >= 0; i-- {
		item := history.RawItems[i]
		if item.SenderType != "" && item.SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		if !isRuntimeMediaMessage(item.MessageType) {
			continue
		}
		mediaText, mediaSummary, status := utils.RuntimeMediaUnderstandingFromPayload(item.Payload)
		if strings.TrimSpace(status) != "understood" {
			continue
		}
		text := strings.TrimSpace(strings.Join([]string{mediaText, mediaSummary}, "\n"))
		if text != "" {
			return text
		}
	}
	return ""
}

func appendRetrievedContext(ctx context.Context, req RunInput, intent callbacks.IntentTraceData, summary *RunResult, collector *callbacks.RuntimeTraceCollector, gate *KnowledgeAnswerabilityGate, messages *[]*schema.Message) knowledgeGuardDecision {
	if messages == nil {
		return knowledgeGuardDecision{}
	}
	if gate == nil {
		gate = NewKnowledgeAnswerabilityGate()
	}
	state, err := gate.Evaluate(ctx, answerabilityGateInput{
		Request:   req,
		Summary:   summary,
		Collector: collector,
		Messages:  append([]*schema.Message(nil), (*messages)...),
		Intent:    intent,
	})
	if err != nil || state == nil {
		errorMessage := ""
		if err != nil {
			errorMessage = err.Error()
		} else {
			errorMessage = "answerability gate returned nil state"
		}
		if collector != nil {
			collector.SetAnswerability(callbacks.AnswerabilityTraceData{
				Status:       answerabilityStatusUnanswerable,
				Reason:       "answerability gate failed",
				ErrorMessage: errorMessage,
			})
		}
		return buildKnowledgeUnavailableDecision(req.AIAgent, utils.SplitInt64s(req.AIAgent.KnowledgeIDs))
	}
	*messages = append((*messages)[:0], state.Input.Messages...)
	if state.SkipGate {
		return knowledgeGuardDecision{}
	}
	return state.Decision
}
