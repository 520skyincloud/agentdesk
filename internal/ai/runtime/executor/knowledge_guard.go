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
		return "知识库回答约束：沿用回复运行时决策，知识部分依据本轮已选事实和适用条件自然回答，可以归纳，不扩展未确认能力。PMS实时事实及资源结果按各自来源使用。missingAspects只表示对应方面未知，不能把未知写成肯定或否定；已答事实不能覆盖或补出未知方面。先回应当前所求，再补必要方法或下一步，不复制整条知识。"
	}
	return "知识库回答约束：沿用回复运行时决策，知识部分只能依据本轮已选事实和适用条件回答，不用模型常识补充具体事实、步骤、承诺、价格、时效或政策。PMS实时事实及资源结果按各自来源使用。missingAspects只表示对应方面未知，不能把未知写成肯定或否定；已答事实不能覆盖或补出未知方面。先回应当前所求，再补必要方法或下一步，不复制整条知识。"
}

func buildKnowledgeNoContextInstruction() string {
	return knowledgeUnavailableInstruction("当前没有从知识库检索到可用资料")
}

func buildKnowledgeRetrievalErrorInstruction() string {
	return knowledgeUnavailableInstruction("知识库检索暂时不可用，当前没有可用的知识库资料")
}

func knowledgeUnavailableInstruction(status string) string {
	return "知识库检索状态：" + status + "。\n" +
		"沿用本轮回复计划和当前有效上下文，不重新分类，不因知识未命中或检索异常自动转人工。\n" +
		"PMS只读查询、资源发送和其他独立任务继续处理；已有工具事实可以直接回答，不因缺少FAQ而废弃。\n" +
		"当前问题已明确的资料继续使用，只追问一个真正阻断处理的关键字段；已经说过的手机号、日期或对象不得重复询问。\n" +
		"不得编造，缺少资料不等于不存在。保留已知部分，包括已确认的图片/语音/文件等媒体理解结果；用客户能理解的语言说清尚不能确认的部分，不暴露知识库、PMS或内部错误。\n" +
		"人工仅按运行时已授权来源执行：客户明确要求人工、适用知识明确转接或既定严重安全风险。话术不能授予操作权限。\n" +
		"寒暄、感谢或闲聊自然回应，不要因为知识库未命中就输出固定兜底话术；不要用含糊确认、代记账、空泛跟进类话术替代真实处理。简单问题简洁，复杂问题答全，不硬限制句数。"
}
