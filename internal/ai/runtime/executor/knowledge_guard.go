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
		instruction := buildKnowledgeNoContextInstruction(fallbackReply)
		if instruction == "" {
			return knowledgeGuardDecision{}
		}
		return knowledgeGuardDecision{Instructions: []*schema.Message{schema.SystemMessage(instruction)}}
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
		"1. 先判断用户意图，不要因为知识库未命中就直接输出固定兜底话术。\n" +
		"2. 寒暄、问候、感谢、确认、开玩笑、结束语、简单能力询问，可以自然短句回复。\n" +
		"3. 办入住、要小程序、要定位、问怎么去、转人工、补充房号、确认/取消等服务流程意图，应按当前系统已有工具/上下文继续处理或追问必要信息，不要走知识库兜底。\n" +
		"4. 用户表达不清楚或缺少上下文时，先追问具体场景、对象、房号、数量、时间、报错信息或操作步骤。\n" +
		"5. 用户结合图片/语音/文件追问，且上下文已有媒体理解结果，可以围绕确定内容继续问答；无法确定时说明需要确认，不要装作看懂。\n" +
		"6. 只有当用户询问酒店业务事实、规则、价格、流程、配置、时效、承诺、售后、退款、权限或政策，且当前没有资料支持时，才用简短兜底或转人工，例如：" + fallbackReply + "\n" +
		"7. 不得编造，不得输出知识库未提供的具体事实、流程、承诺、价格、时效或政策。"
}

func buildKnowledgeRetrievalErrorInstruction(fallbackReply string) string {
	fallbackReply = strings.TrimSpace(fallbackReply)
	if fallbackReply == "" {
		fallbackReply = "这个我先帮您记录一下，稍后让同事跟进确认。"
	}
	return "知识库检索状态：知识库检索暂时不可用，当前没有可用的知识库资料。\n" +
		"回复策略：\n" +
		"1. 先判断用户意图，不要因为检索异常就直接输出固定兜底话术。\n" +
		"2. 寒暄、问候、感谢、确认、开玩笑、结束语、简单能力询问，可以自然短句回复。\n" +
		"3. 办入住、要小程序、要定位、问怎么去、转人工、补充房号、确认/取消等服务流程意图，应按当前系统已有工具/上下文继续处理或追问必要信息。\n" +
		"4. 用户表达不清楚或缺少上下文时，先追问具体场景、对象、房号、数量、时间、报错信息或操作步骤。\n" +
		"5. 用户结合图片/语音/文件追问，且上下文已有媒体理解结果，可以围绕确定内容继续问答；无法确定时说明需要确认，不要装作看懂。\n" +
		"6. 只有当用户询问酒店业务事实、规则、价格、流程、配置、时效、承诺、售后、退款、权限或政策，且当前没有资料支持时，才用简短兜底或转人工，例如：" + fallbackReply + "\n" +
		"7. 不得编造，不得输出知识库未提供的具体事实、流程、承诺、价格、时效或政策。"
}
