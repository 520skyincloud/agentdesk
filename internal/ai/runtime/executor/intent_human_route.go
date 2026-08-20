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
	autoHandoffEnabled := services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(req.Conversation.ID)
	decision := decideRuntimeIntentHandoff(req, summary, intent, autoHandoffEnabled)
	if decision.Mode == contracts.HandoffModeNone {
		recommendedAction := "ignore_ineligible_handoff"
		resultPreview := "当前任务不满足人工确认门禁，继续按原任务链路处理"
		if decision.ReasonCode == "customer_auto_handoff_disabled" {
			recommendedAction = "customer_auto_handoff_disabled"
			resultPreview = "当前客户在此企微员工号下已关闭自动转人工；继续由 AI 直接回复"
		}
		collector.AddGraphToolItem(callbacks.GraphToolTraceItem{
			ToolCode: toolx.GraphHandoffConversation.Code,
			ToolName: toolx.GraphHandoffConversation.Name,
			Arguments: map[string]any{
				"intent":     intent.PrimaryIntent,
				"subIntent":  intent.SubIntent,
				"reasonCode": decision.ReasonCode,
			},
			Status:            "skipped",
			RecommendedAction: recommendedAction,
			ResultPreview:     resultPreview,
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
	reason := buildIntentHumanRouteReason(intent, runtimeUserMessageText(req.UserMessage))
	started := time.Now()
	promptSent, err := services.ConversationHandoffConfirmationService.RequestByAIForTasksWithOriginMessage(
		req.Conversation.ID,
		req.AIAgent,
		reason,
		strings.TrimSpace(req.UserMessage.RequestID),
		req.UserMessage.ID,
		decision.TurnID,
		decision.TaskKeys,
	)
	item := callbacks.GraphToolTraceItem{
		ToolCode: toolx.GraphHandoffConversation.Code,
		ToolName: toolx.GraphHandoffConversation.Name,
		Arguments: map[string]any{
			"reason":         reason,
			"intent":         intent.PrimaryIntent,
			"subIntent":      intent.SubIntent,
			"routePolicy":    intent.HumanRoutePolicy,
			"conversationId": req.Conversation.ID,
			"turnId":         decision.TurnID,
			"taskKeys":       decision.TaskKeys,
			"decisionMode":   decision.Mode,
			"reasonCode":     decision.ReasonCode,
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

func decideRuntimeIntentHandoff(req RunInput, summary *RunResult, intent callbacks.IntentTraceData, autoHandoffEnabled bool) contracts.HandoffDecisionV2 {
	taskKeys := runtimeHandoffTaskKeys(summary)
	turnVersion := req.UserMessage.AIReplyTurnVersion
	if summary != nil && summary.ReplyPlanV2 != nil && summary.ReplyPlanV2.TurnVersion > 0 {
		turnVersion = summary.ReplyPlanV2.TurnVersion
	}
	instanceID := int64(0)
	if instance := findRuntimeWxWorkInstance(req); instance != nil {
		instanceID = instance.ID
	}
	task := HandoffTaskView{
		TaskKeys: taskKeys, TurnID: req.UserMessage.AIReplyTurnID, TurnVersion: turnVersion,
		TenantID: req.Conversation.TenantID, StoreID: req.Conversation.StoreID,
		StoreStaffBindingID: req.Conversation.StoreStaffBindingID, ProtocolInstanceID: instanceID,
		ConversationID: req.Conversation.ID, SessionNo: req.UserMessage.SessionNo,
	}
	if len(taskKeys) > 0 {
		task.TaskKey = taskKeys[0]
	}
	capability := CapabilityDecisionV1{}
	switch strings.TrimSpace(intent.SubIntent) {
	case "explicit_handoff":
		if !runtimeTextExplicitlyRequestsHuman(runtimeUserMessageText(req.UserMessage)) {
			decision := DecideHandoff(task, capability, HandoffFailureNone)
			decision.ReasonCode = "explicit_handoff_not_present"
			return decision
		}
		task.ExplicitHumanRequest = true
	case "complaint_escalation", "refund_compensation", "order_price_dispute":
		capability.Route = "business_handoff"
		capability.ExecutionMode = "human"
	case "emergency_safety":
		task.SafetyCritical = true
	default:
		decision := DecideHandoff(task, capability, HandoffFailureNone)
		decision.ReasonCode = "handoff_category_not_eligible"
		return decision
	}
	if !isHandoffIntentCategory(intent) {
		decision := DecideHandoff(task, CapabilityDecisionV1{}, HandoffFailureNone)
		decision.ReasonCode = "non_handoff_intent_category"
		return decision
	}
	decision := DecideHandoff(task, capability, HandoffFailureNone)
	if !autoHandoffEnabled {
		decision.Mode = contracts.HandoffModeNone
		decision.ReasonCode = "customer_auto_handoff_disabled"
	}
	return decision
}

func runtimeTextExplicitlyRequestsHuman(text string) bool {
	compact := compactRuntimeProtocolText(text)
	return containsAny(compact, []string{
		"人工", "转人工", "找人工", "人工客服", "真人客服", "转客服", "找客服", "换个人", "叫个人", "别机器人", "不要机器人",
	})
}

func runtimeHandoffTaskKeys(summary *RunResult) []string {
	if summary == nil {
		return nil
	}
	keys := make([]string, 0)
	if summary.ReplyPlanV2 != nil {
		for _, task := range summary.ReplyPlanV2.Tasks {
			if task.OutputMode == "handoff" {
				keys = appendUniqueStrings(keys, task.TaskKey)
			}
		}
	}
	if len(keys) == 0 {
		keys = appendUniqueStrings(keys, summary.HumanTaskKeys...)
	}
	return keys
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
