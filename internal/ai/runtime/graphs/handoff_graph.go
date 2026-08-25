package graphs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/services"
)

type handoffGraphArgs struct {
	Reason string `json:"reason"`
}

var dispatchHandoffByAI = func(conversationID int64, aiAgent models.AIAgent, reason string, requestID string) (*services.HandoffDispatchResult, error) {
	return services.ConversationHandoffConfirmationService.DispatchByAI(conversationID, aiAgent, reason, requestID)
}

var isAutoHandoffEnabledForConversation = func(conversationID int64) bool {
	return services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(conversationID)
}

type HandoffGraph struct {
	conversation models.Conversation
	aiAgent      models.AIAgent
}

func NewHandoffGraph(conversation models.Conversation, aiAgent models.AIAgent) *HandoffGraph {
	return &HandoffGraph{
		conversation: conversation,
		aiAgent:      aiAgent,
	}
}

func (g *HandoffGraph) Run(ctx context.Context, argumentsInJSON string) (string, error) {
	if !isAutoHandoffEnabledForConversation(g.conversation.ID) {
		return tooling.MarshalToolResult(tooling.ToolResult{
			Handled:     false,
			Terminal:    false,
			Action:      "auto_handoff_disabled",
			ReplySent:   false,
			ShouldRetry: false,
		}), nil
	}
	reason, err := g.buildReason(argumentsInJSON)
	if err != nil {
		return "", err
	}
	result, err := dispatchHandoffByAI(g.conversation.ID, g.aiAgent, reason, tracex.RequestIDFromContext(ctx))
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", fmt.Errorf("handoff dispatch result missing")
	}
	return tooling.MarshalToolResult(tooling.ToolResult{
		Handled:     true,
		Terminal:    true,
		Action:      string(result.Status),
		ReplySent:   true,
		ShouldRetry: false,
	}), nil
}

func (g *HandoffGraph) buildReason(argumentsInJSON string) (string, error) {
	reason := "用户需要转人工支持"
	var args handoffGraphArgs
	if strings.TrimSpace(argumentsInJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", fmt.Errorf("invalid handoff arguments: %w", err)
		}
	}
	if parsed := strings.TrimSpace(args.Reason); parsed != "" {
		reason = parsed
	}
	return reason, nil
}
