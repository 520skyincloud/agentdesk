package runtime

import (
	"errors"
	"strings"

	"agent-desk/internal/ai/runtime/actions"
	svc "agent-desk/internal/services"
)

// init 给动作目录绑定执行器。执行器依赖 services，因此放在 runtime 层注入，
// 避免 actions 包与 services 形成循环依赖。
func init() {
	actions.RegisterExecutor("human_handoff", humanHandoffActionExecutor{})

	// 资源动作与工具动作由 Runtime 的 ActionLedger / 工具链结构化提交，
	// 目录执行器只做声明，不在此处直接发送。
	actions.RegisterExecutor("provide_location", declarativeActionExecutor{})
	actions.RegisterExecutor("provide_mini_program", declarativeActionExecutor{})
	actions.RegisterExecutor("provide_phone", declarativeActionExecutor{})
	actions.RegisterExecutor("create_ticket", declarativeActionExecutor{})
}

// humanHandoffActionExecutor 把“知识命中 → 转人工”变成确定性二次确认动作。
type humanHandoffActionExecutor struct{}

func (humanHandoffActionExecutor) Run(input actions.Input) (actions.Output, error) {
	conversationID := input.ConversationID
	if conversationID <= 0 {
		return actions.Output{}, errActionMissingConversation
	}
	aiAgent, ok := svc.WxWorkProtocolInstanceService.BuildRuntimeAIAgentForConversation(conversationID)
	if !ok {
		if conversation := svc.ConversationService.Get(conversationID); conversation != nil {
			if resolved := svc.AIAgentService.GetByTenantID(conversation.AIAgentID, conversation.TenantID); resolved != nil {
				aiAgent = *resolved
				ok = true
			}
		}
	}
	if !ok {
		return actions.Output{}, errActionMissingAIAgent
	}
	reason := strings.TrimSpace(paramString(input.Parameters, "reason"))
	if reason == "" {
		reason = "知识库要求转人工"
	}
	_, err := svc.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(
		conversationID, aiAgent, reason, input.RequestID, input.OriginMessageID,
	)
	if err != nil {
		return actions.Output{}, err
	}
	return actions.Output{Handled: true, NeedConfirmation: true}, nil
}

// declarativeActionExecutor 表示该动作由 ActionLedger 或工具链结构化落地。
type declarativeActionExecutor struct{}

func (declarativeActionExecutor) Run(actions.Input) (actions.Output, error) {
	return actions.Output{Handled: false}, nil
}

func paramString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	switch value := params[key].(type) {
	case string:
		return value
	default:
		return ""
	}
}

var (
	errActionMissingConversation = errors.New("action requires conversation id")
	errActionMissingAIAgent      = errors.New("action requires runtime AI agent")
)
