package executor

import (
	"context"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/services"
)

func executeIntentHumanRoute(ctx context.Context, req RunInput, summary *RunResult, collector *callbacks.RuntimeTraceCollector) (bool, error) {
	if strings.HasPrefix(strings.TrimSpace(req.UserMessage.RequestID), "manual_resume_") {
		return false, nil
	}
	if summary == nil || collector == nil {
		return false, nil
	}
	intent := collector.Data.Pipeline.Intent
	if !intent.NeedsHumanRoute {
		return false, nil
	}
	if !isHandoffIntentCategory(intent) {
		collector.AddGraphToolItem(callbacks.GraphToolTraceItem{
			ToolCode: toolx.GraphHandoffConversation.Code,
			ToolName: toolx.GraphHandoffConversation.Name,
			Arguments: map[string]any{
				"intent":    intent.PrimaryIntent,
				"subIntent": intent.SubIntent,
			},
			Status:            "skipped",
			RecommendedAction: "ignore_non_handoff_intent_human_route_flag",
			ResultPreview:     "二次确认只属于 human_complaint_risk 意图分类；当前分类继续按自身链路处理",
		})
		return false, nil
	}
	if runtimeReplyPlanHasNonHandoffTask(summary.ReplyPlanV2) {
		collector.AddGraphToolItem(callbacks.GraphToolTraceItem{
			ToolCode: toolx.GraphHandoffConversation.Code,
			ToolName: toolx.GraphHandoffConversation.Name,
			Arguments: map[string]any{
				"intent":    intent.PrimaryIntent,
				"subIntent": intent.SubIntent,
			},
			Status:            "skipped",
			RecommendedAction: "defer_handoff_until_other_tasks_commit",
			ResultPreview:     "当前轮还有可直接回答或发送的任务，先提交成功项，再发人工确认",
		})
		return false, nil
	}
	if !services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(req.Conversation.ID) {
		collector.AddGraphToolItem(callbacks.GraphToolTraceItem{
			ToolCode: toolx.GraphHandoffConversation.Code,
			ToolName: toolx.GraphHandoffConversation.Name,
			Arguments: map[string]any{
				"intent":    intent.PrimaryIntent,
				"subIntent": intent.SubIntent,
			},
			Status:            "skipped",
			RecommendedAction: "customer_auto_handoff_disabled",
			ResultPreview:     "当前客户在此企微员工号下已关闭自动转人工；继续由 AI 直接回复",
		})
		return false, nil
	}
	reason := buildIntentHumanRouteReason(intent, runtimeUserMessageText(req.UserMessage))
	started := time.Now()
	promptSent, err := services.ConversationHandoffConfirmationService.RequestByAIWithOriginMessage(req.Conversation.ID, req.AIAgent, reason, strings.TrimSpace(req.UserMessage.RequestID), req.UserMessage.ID)
	item := callbacks.GraphToolTraceItem{
		ToolCode: toolx.GraphHandoffConversation.Code,
		ToolName: toolx.GraphHandoffConversation.Name,
		Arguments: map[string]any{
			"reason":         reason,
			"intent":         intent.PrimaryIntent,
			"subIntent":      intent.SubIntent,
			"routePolicy":    intent.HumanRoutePolicy,
			"conversationId": req.Conversation.ID,
		},
		LatencyMs: time.Since(started).Milliseconds(),
	}
	if err != nil {
		item.Status = "error"
		item.ErrorMessage = err.Error()
		collector.AddGraphToolItem(item)
		return true, err
	}
	item.Status = "success"
	item.RecommendedAction = "request_handoff_confirmation"
	if promptSent {
		item.ResultPreview = "pending customer confirmation"
	} else {
		item.ResultPreview = "human route already active"
	}
	collector.AddGraphToolItem(item)
	summary.InvokedToolCodes = appendIfMissing(summary.InvokedToolCodes, toolx.GraphHandoffConversation.Code)
	summary.ToolCallCount = len(summary.InvokedToolCodes)
	return true, nil
}

func runtimeReplyPlanHasNonHandoffTask(plan *contracts.ReplyPlanV2) bool {
	if plan == nil {
		return false
	}
	for _, task := range plan.Tasks {
		if task.OutputMode != "handoff" && task.OutputMode != "skip" {
			return true
		}
	}
	return false
}

func isHandoffIntentCategory(intent callbacks.IntentTraceData) bool {
	return canonicalIntentCode(intent.PrimaryIntent) == "human_complaint_risk" ||
		canonicalIntentCode(intent.MatchedIntentCode) == "human_complaint_risk"
}

func isEmergencySafetyHandoff(intent callbacks.IntentTraceData) bool {
	return isHandoffIntentCategory(intent) && strings.TrimSpace(intent.SubIntent) == "emergency_safety"
}

func buildIntentHumanRouteReason(intent callbacks.IntentTraceData, currentText string) string {
	parts := make([]string, 0, 3)
	parts = append(parts, humanRouteReasonLabel(intent))
	if strings.TrimSpace(currentText) != "" {
		parts = append(parts, "客户消息："+preview(currentText, 180))
	} else if reason := sanitizeIntentHumanRouteReason(intent.Reason); reason != "" {
		parts = append(parts, reason)
	}
	reason := strings.Join(parts, "；")
	if strings.TrimSpace(reason) == "" {
		reason = "用户需要人工接待"
	}
	return reason
}

func humanRouteReasonLabel(intent callbacks.IntentTraceData) string {
	switch {
	case strings.TrimSpace(intent.SubIntent) == "emergency_safety":
		return "客人遇到安全或突发情况，需要门店同事尽快关注"
	case strings.TrimSpace(intent.PrimaryIntent) == "human_complaint_risk":
		return "客人有投诉、风险或明确人工诉求，需要人工关注"
	case strings.TrimSpace(intent.PrimaryIntent) == "service_request":
		return "客人有服务请求，需要人工跟进"
	default:
		return "客人需要人工接待"
	}
}

func sanitizeIntentHumanRouteReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "model IntentDetect JSON:")
	value = strings.TrimSpace(value)
	for _, token := range []string{
		"human_complaint_risk",
		"service_request",
		"hotel_info",
		"hotel_variable",
		"interaction",
		"interaction",
		"emergency_safety",
		"NeedsHumanRoute",
		"needsHumanRoute",
	} {
		value = strings.ReplaceAll(value, token, "")
	}
	value = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(value)
	return strings.TrimSpace(strings.Join(strings.Fields(value), " "))
}
