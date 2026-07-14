package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/cloudwego/eino/schema"
)

type knowledgeGuardDecision struct {
	Instructions []*schema.Message
}

func buildKnowledgeUnavailableDecision(_ models.AIAgent, knowledgeBaseIDs []int64) knowledgeGuardDecision {
	if len(knowledgeBaseIDs) == 0 {
		return knowledgeGuardDecision{}
	}
	instruction := buildKnowledgeRetrievalErrorInstruction()
	if instruction == "" {
		return knowledgeGuardDecision{}
	}
	return knowledgeGuardDecision{Instructions: []*schema.Message{schema.SystemMessage(instruction)}}
}

func buildKnowledgeGuardDecision(aiAgent models.AIAgent, retrieveResult *retrievers.KnowledgeRetrieveResult) knowledgeGuardDecision {
	if retrieveResult == nil || len(retrieveResult.KnowledgeBaseIDs) == 0 {
		return knowledgeGuardDecision{}
	}
	if len(retrieveResult.Hits) == 0 || strings.TrimSpace(retrieveResult.ContextText) == "" {
		instruction := buildKnowledgeNoContextInstruction()
		if instruction == "" {
			return knowledgeGuardDecision{}
		}
		return knowledgeGuardDecision{Instructions: []*schema.Message{schema.SystemMessage(instruction)}}
	}
	instruction := buildKnowledgeRuntimeInstruction(retrieveResult.AnswerMode)
	if instruction == "" {
		return knowledgeGuardDecision{}
	}
	return knowledgeGuardDecision{
		Instructions: []*schema.Message{schema.SystemMessage(instruction)},
	}
}

func buildKnowledgeNoContextDecision(_ models.AIAgent, knowledgeBaseIDs []int64) knowledgeGuardDecision {
	if len(knowledgeBaseIDs) == 0 {
		return knowledgeGuardDecision{}
	}
	instruction := buildKnowledgeNoContextInstruction()
	if instruction == "" {
		return knowledgeGuardDecision{}
	}
	return knowledgeGuardDecision{
		Instructions: []*schema.Message{schema.SystemMessage(instruction)},
	}
}

func buildKnowledgeRetrievalErrorDecision(_ models.AIAgent, knowledgeBaseIDs []int64) knowledgeGuardDecision {
	if len(knowledgeBaseIDs) == 0 {
		return knowledgeGuardDecision{}
	}
	instruction := buildKnowledgeRetrievalErrorInstruction()
	if instruction == "" {
		return knowledgeGuardDecision{}
	}
	return knowledgeGuardDecision{
		Instructions: []*schema.Message{schema.SystemMessage(instruction)},
	}
}

func buildKnowledgeRuntimeInstruction(answerMode enums.KnowledgeAnswerMode) string {
	if answerMode == enums.KnowledgeAnswerModeAssist {
		return "知识库回答约束：优先依据后续提供的知识片段回答，可以做轻度归纳，但不要编造片段中未提供的事实。具体事实、步骤、承诺、价格、时效、政策必须能被知识片段直接支持；如果片段不足，按回复运行时决策处理：能追问就只追问一个关键细节，能触发小程序/定位等工具就交给工具，不要输出固定兜底句。"
	}
	return "知识库回答约束：本轮只能依据后续提供的知识片段回答，不得使用模型常识补充未提供的事实，不得输出知识片段外的具体事实、步骤、承诺、价格、时效或政策。片段不足时按回复运行时决策处理：能追问就只追问一个关键细节，能触发小程序/定位等工具就交给工具，不要输出固定兜底句。"
}

func buildKnowledgeNoContextInstruction() string {
	return "知识库检索状态：当前没有从知识库检索到可用资料。\n" +
		"回复策略：\n" +
		"1. 先使用回复运行时决策判断用户意图，不要因为知识库未命中就输出固定兜底话术。\n" +
		"2. 寒暄、问候、感谢、确认、开玩笑、结束语、简单能力询问，可以自然短句回复。\n" +
		"3. 办入住、要小程序、要定位、问怎么去、转人工、补充房号、确认/取消等服务流程意图，应按当前系统已有工具/上下文继续处理或追问必要信息，不要走知识库兜底。\n" +
		"4. 用户表达不清楚或缺少上下文时，像真人一样只追问一个最关键细节，不要一口气列很多字段。\n" +
		"5. 用户结合图片/语音/文件追问，且上下文已有图片/语音/文件等媒体理解结果，可以围绕确定内容继续问答；无法确定时说明需要哪一个具体信息，不要装作看懂。\n" +
		"6. 用户询问酒店业务事实、规则、价格、流程、配置、时效、承诺、售后、退款、权限或政策，且当前没有资料支持时，不能编答案；先判断是否能追问一个关键字段来推进，比如 WiFi 缺房号就问房号，发票缺抬头就让对方按小程序页面填。\n" +
		"7. 不要用含糊确认、代记账、空泛跟进类话术替代真实处理；这些句式只会让客户感觉敷衍。\n" +
		"8. 不得编造，不得输出知识库未提供的具体事实、流程、承诺、价格、时效或政策；回复默认 1 句，像线上酒店接待微信聊天。"
}

func buildKnowledgeRetrievalErrorInstruction() string {
	return "知识库检索状态：知识库检索暂时不可用，当前没有可用的知识库资料。\n" +
		"回复策略：\n" +
		"1. 先使用回复运行时决策判断用户意图，不要因为检索异常就直接输出固定兜底话术。\n" +
		"2. 寒暄、问候、感谢、确认、开玩笑、结束语、简单能力询问，可以自然短句回复。\n" +
		"3. 办入住、要小程序、要定位、问怎么去、转人工、补充房号、确认/取消等服务流程意图，应按当前系统已有工具/上下文继续处理或追问必要信息。\n" +
		"4. 用户表达不清楚或缺少上下文时，像真人一样只追问一个最关键细节，不要一口气列很多字段。\n" +
		"5. 用户结合图片/语音/文件追问，且上下文已有图片/语音/文件等媒体理解结果，可以围绕确定内容继续问答；无法确定时说明需要哪一个具体信息，不要装作看懂。\n" +
		"6. 用户询问酒店业务事实、规则、价格、流程、配置、时效、承诺、售后、退款、权限或政策，且当前没有资料支持时，不能编答案；先判断是否能追问一个关键字段来推进，无法推进才自然说明暂时没有准确资料。\n" +
		"7. 不要用含糊确认、代记账、空泛跟进类话术替代真实处理；这些句式只会让客户感觉敷衍。\n" +
		"8. 不得编造，不得输出知识库未提供的具体事实、流程、承诺、价格、时效或政策；回复默认 1 句，像线上酒店接待微信聊天。"
}
