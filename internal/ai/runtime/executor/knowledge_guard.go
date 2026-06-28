package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/cloudwego/eino/schema"
	"github.com/mlogclub/simple/common/strs"
)

type knowledgeGuardDecision struct {
	FallbackReply string
	Instructions  []*schema.Message
}

func buildKnowledgeUnavailableDecision(aiAgent models.AIAgent, knowledgeBaseIDs []int64) knowledgeGuardDecision {
	if len(knowledgeBaseIDs) == 0 {
		return knowledgeGuardDecision{}
	}
	return knowledgeGuardDecision{FallbackReply: resolveKnowledgeFallbackReply(aiAgent)}
}

func buildKnowledgeGuardDecision(aiAgent models.AIAgent, retrieveResult *retrievers.KnowledgeRetrieveResult) knowledgeGuardDecision {
	if retrieveResult == nil || len(retrieveResult.KnowledgeBaseIDs) == 0 {
		return knowledgeGuardDecision{}
	}
	fallbackReply := resolveKnowledgeFallbackReply(aiAgent)
	if len(retrieveResult.Hits) == 0 || strings.TrimSpace(retrieveResult.ContextText) == "" {
		return knowledgeGuardDecision{FallbackReply: fallbackReply}
	}
	instruction := buildKnowledgeRuntimeInstruction(retrieveResult.AnswerMode, fallbackReply)
	if instruction == "" {
		return knowledgeGuardDecision{}
	}
	return knowledgeGuardDecision{
		Instructions: []*schema.Message{schema.SystemMessage(instruction)},
	}
}

func buildKnowledgeNoContextDecision(aiAgent models.AIAgent, knowledgeBaseIDs []int64) knowledgeGuardDecision {
	if len(knowledgeBaseIDs) == 0 {
		return knowledgeGuardDecision{}
	}
	instruction := buildKnowledgeNoContextInstruction(resolveKnowledgeFallbackReply(aiAgent))
	if instruction == "" {
		return knowledgeGuardDecision{}
	}
	return knowledgeGuardDecision{
		Instructions: []*schema.Message{schema.SystemMessage(instruction)},
	}
}

func buildKnowledgeRetrievalErrorDecision(aiAgent models.AIAgent, knowledgeBaseIDs []int64) knowledgeGuardDecision {
	if len(knowledgeBaseIDs) == 0 {
		return knowledgeGuardDecision{}
	}
	instruction := buildKnowledgeRetrievalErrorInstruction(resolveKnowledgeFallbackReply(aiAgent))
	if instruction == "" {
		return knowledgeGuardDecision{}
	}
	return knowledgeGuardDecision{
		Instructions: []*schema.Message{schema.SystemMessage(instruction)},
	}
}

func resolveKnowledgeFallbackReply(aiAgent models.AIAgent) string {
	if reply := strings.TrimSpace(aiAgent.FallbackMessage); reply != "" {
		return reply
	}
	switch aiAgent.FallbackMode {
	case enums.AIAgentFallbackModeSuggestRetry:
		return "这个我还需要再确认一下，您方便再补充一点具体情况吗？"
	default:
		return "这个我先帮您记录一下，稍后让同事跟进确认。"
	}
}

func resolveKnowledgeHumanSupportFallback(aiAgent models.AIAgent) string {
	base := strings.TrimSpace(resolveKnowledgeFallbackReply(aiAgent))
	if strs.IsBlank(base) {
		base = "这个我先帮您记录一下，稍后让同事跟进确认。"
	}
	return base
}

func buildKnowledgeRuntimeInstruction(answerMode enums.KnowledgeAnswerMode, fallbackReply string) string {
	fallbackReply = strings.TrimSpace(fallbackReply)
	if fallbackReply == "" {
		fallbackReply = "这个我先帮您记录一下，稍后让同事跟进确认。"
	}
	if answerMode == enums.KnowledgeAnswerModeAssist {
		return "知识库回答约束：优先依据后续提供的知识片段回答，可以做轻度归纳，但不要编造片段中未提供的事实。回答中的具体事实、步骤、承诺、价格、时效、政策必须能被知识片段直接支持；若知识片段不足以直接支持答案，必须明确回复：" + fallbackReply
	}
	return "知识库回答约束：本轮只能依据后续提供的知识片段回答，不得使用模型常识补充未提供的事实，不得输出知识片段外的具体事实、步骤、承诺、建议、价格、时效或政策。若知识片段不足以支持回答，必须明确回复：" + fallbackReply
}

func buildKnowledgeNoContextInstruction(fallbackReply string) string {
	fallbackReply = strings.TrimSpace(fallbackReply)
	if fallbackReply == "" {
		fallbackReply = "这个我先帮您记录一下，稍后让同事跟进确认。"
	}
	return "知识库检索状态：当前没有从知识库检索到可用资料。\n" +
		"回复策略：\n" +
		"1. 如果用户只是寒暄、问候、感谢、确认或结束语，可以自然、简短地回复。\n" +
		"2. 如果用户表达不清楚或缺少上下文，应追问具体场景、对象、报错信息或操作步骤。\n" +
		"3. 如果用户结合图片/语音/文件追问，且上下文已有媒体理解结果，可以复述媒体里确定的对象，再说明酒店是否提供、是否有货、是否可借用等事实需要同事确认；不要只回固定模板。\n" +
		"4. 如果用户询问业务事实、规则、价格、流程、配置、时效、承诺、售后、退款、权限或政策，不得编造答案；可以短句说明需要确认，例如“你问的是图里这个桌面音箱吗？房间里不一定有，我帮你确认下。”必要时使用兜底：" + fallbackReply + "\n" +
		"5. 不得输出知识库未提供的具体事实、流程、承诺、价格、时效或政策。"
}

func buildKnowledgeRetrievalErrorInstruction(fallbackReply string) string {
	fallbackReply = strings.TrimSpace(fallbackReply)
	if fallbackReply == "" {
		fallbackReply = "这个我先帮您记录一下，稍后让同事跟进确认。"
	}
	return "知识库检索状态：知识库检索暂时不可用，当前没有可用的知识库资料。\n" +
		"回复策略：\n" +
		"1. 如果用户只是寒暄、问候、感谢、确认或结束语，可以自然、简短地回复，不要使用知识库兜底话术。\n" +
		"2. 如果用户表达不清楚或缺少上下文，应追问具体场景、对象、报错信息或操作步骤。\n" +
		"3. 如果用户结合图片/语音/文件追问，且上下文已有媒体理解结果，可以复述媒体里确定的对象，再说明酒店是否提供、是否有货、是否可借用等事实需要同事确认；不要只回固定模板。\n" +
		"4. 如果用户询问业务事实、规则、价格、流程、配置、时效、承诺、售后、退款、权限或政策，不得编造答案；可以短句说明需要确认，例如“你问的是图里这个桌面音箱吗？房间里不一定有，我帮你确认下。”必要时使用兜底：" + fallbackReply + "\n" +
		"5. 不得输出知识库未提供的具体事实、流程、承诺、价格、时效或政策。"
}
