package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

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
			ResultPreview:     "直接转人工只属于 human_complaint_risk 意图分类；当前分类继续按自身链路处理",
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
	reason := buildIntentHumanRouteReason(intent, req.UserMessage.Content)
	started := time.Now()
	dispatch := services.ConversationHandoffConfirmationService.DispatchByAIWithOriginMessage
	if isEmergencySafetyHandoff(intent) {
		dispatch = services.ConversationHandoffConfirmationService.DispatchEmergencyByAIWithOriginMessage
	}
	result, err := dispatch(req.Conversation.ID, req.AIAgent, reason, strings.TrimSpace(req.UserMessage.RequestID), req.UserMessage.ID)
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
	if err := applyHandoffDispatchResult(summary, &item, result, isEmergencySafetyHandoff(intent)); err != nil {
		item.Status = "error"
		item.ErrorMessage = err.Error()
		collector.AddGraphToolItem(item)
		return true, err
	}
	collector.AddGraphToolItem(item)
	summary.InvokedToolCodes = appendIfMissing(summary.InvokedToolCodes, toolx.GraphHandoffConversation.Code)
	summary.ToolCallCount = len(summary.InvokedToolCodes)
	return true, nil
}

func deferMixedExplicitIntentHumanRoute(req RunInput, collector *callbacks.RuntimeTraceCollector) bool {
	if collector == nil {
		return false
	}
	intent := collector.Data.Pipeline.Intent
	if !intent.NeedsHumanRoute || strings.TrimSpace(intent.SubIntent) != "explicit_handoff" || isEmergencySafetyHandoff(intent) {
		return false
	}

	plan := collector.Data.Pipeline.ReplyPlan
	hasNonHandoffTask := false
	handoffTaskIDs := make([]string, 0)
	for _, task := range plan.TaskPlans {
		switch strings.TrimSpace(task.OutputKind) {
		case "text", "resource":
			hasNonHandoffTask = true
		case "handoff":
			if strings.TrimSpace(task.Output) == runtimeKnowledgeDeferredHandoffOutput {
				continue
			}
			if strings.TrimSpace(task.SubIntent) != "explicit_handoff" {
				return false
			}
			handoffTaskIDs = appendIfMissing(handoffTaskIDs, strings.TrimSpace(task.TaskID))
		}
	}
	if !hasNonHandoffTask {
		return false
	}

	reason := buildIntentHumanRouteReason(intent, req.UserMessage.Content)
	judgeTrace := collector.Data.Pipeline.EvidenceJudge
	judgeTrace.DeferredHandoff = true
	if strings.TrimSpace(judgeTrace.DeferredHandoffReason) == "" {
		judgeTrace.DeferredHandoffReason = reason
	}
	for _, taskID := range handoffTaskIDs {
		if taskID != "" {
			judgeTrace.DeferredTaskIDs = appendIfMissing(judgeTrace.DeferredTaskIDs, taskID)
		}
	}
	collector.SetKnowledgeEvidenceJudge(judgeTrace)

	ledger := collector.Data.ActionLedger
	ledger.RequestedActions = appendIfMissingActionLedgerItem(ledger.RequestedActions, callbacks.ActionLedgerItem{
		Action: "human_route",
		Status: "deferred",
		Reason: reason,
	})
	collector.SetActionLedger(ledger)
	return true
}

func executeRuntimeHandoffDirective(req RunInput, summary *RunResult, collector *callbacks.RuntimeTraceCollector) (bool, error) {
	if strings.HasPrefix(strings.TrimSpace(req.UserMessage.RequestID), "manual_resume_") {
		return false, nil
	}
	if summary == nil || collector == nil || !summary.handoffDirective {
		return false, nil
	}
	if !services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(req.Conversation.ID) {
		collector.AddGraphToolItem(callbacks.GraphToolTraceItem{
			ToolCode: toolx.GraphHandoffConversation.Code,
			ToolName: toolx.GraphHandoffConversation.Name,
			Arguments: map[string]any{
				"source":         summary.handoffDirectiveSource,
				"conversationId": req.Conversation.ID,
			},
			Status:            "skipped",
			RecommendedAction: "customer_auto_handoff_disabled",
			ResultPreview:     "当前客户在此企微员工号下已关闭自动转人工；继续由 AI 直接回复",
		})
		return false, nil
	}
	reason := strings.TrimSpace(summary.handoffDirectiveReason)
	if reason == "" {
		reason = "当前问题需要门店同事接手"
	}
	started := time.Now()
	applyRoomNumberPolicy, roomNumberText := runtimeHandoffRoomNumberPolicy(collector)
	result, err := services.ConversationHandoffConfirmationService.DispatchByAIWithRoomNumberPolicy(
		req.Conversation.ID,
		req.AIAgent,
		reason,
		strings.TrimSpace(req.UserMessage.RequestID),
		req.UserMessage.ID,
		applyRoomNumberPolicy,
		roomNumberText,
	)
	item := callbacks.GraphToolTraceItem{
		ToolCode: toolx.GraphHandoffConversation.Code,
		ToolName: toolx.GraphHandoffConversation.Name,
		Arguments: map[string]any{
			"reason":                reason,
			"source":                summary.handoffDirectiveSource,
			"conversationId":        req.Conversation.ID,
			"applyRoomNumberPolicy": applyRoomNumberPolicy,
		},
		LatencyMs: time.Since(started).Milliseconds(),
	}
	if err != nil {
		item.Status = "error"
		item.ErrorMessage = err.Error()
		collector.AddGraphToolItem(item)
		return true, err
	}
	if err := applyHandoffDispatchResult(summary, &item, result, false); err != nil {
		item.Status = "error"
		item.ErrorMessage = err.Error()
		collector.AddGraphToolItem(item)
		return true, err
	}
	collector.AddGraphToolItem(item)
	ledger := collector.Data.ActionLedger
	ledger.CommittedActions = appendIfMissingActionLedgerItem(ledger.CommittedActions, callbacks.ActionLedgerItem{
		Action: "human_route",
		Status: summary.handoffDispatchStatus,
		Reason: reason,
	})
	collector.SetActionLedger(ledger)
	summary.ReplyText = ""
	summary.InvokedToolCodes = appendIfMissing(summary.InvokedToolCodes, toolx.GraphHandoffConversation.Code)
	summary.ToolCallCount = len(summary.InvokedToolCodes)
	return true, nil
}

func runtimeHandoffRoomNumberPolicy(collector *callbacks.RuntimeTraceCollector) (bool, string) {
	if collector == nil {
		return true, ""
	}
	pending := collector.Data.Pipeline.EvidenceJudge.DeferredTaskIDs
	if len(pending) == 0 {
		return true, ""
	}
	roomTasks := make([]string, 0, len(pending))
	for _, taskID := range pending {
		matched := false
		for _, task := range collector.Data.Pipeline.ReplyPlan.TaskPlans {
			if task.TaskID != taskID {
				continue
			}
			matched = true
			if task.Intent == "hotel_info" {
				switch task.Objective {
				case "availability", "quantity", "price", "time", "location", "method", "policy", "recommendation", "compound_information":
					continue
				}
			}
			text := activeGenerationTaskText(task)
			if strings.TrimSpace(text) == "" {
				return true, ""
			}
			roomTasks = append(roomTasks, text)
		}
		if !matched {
			return true, ""
		}
	}
	return len(roomTasks) > 0, strings.Join(roomTasks, "；")
}

func applyHandoffDispatchResult(summary *RunResult, item *callbacks.GraphToolTraceItem, result *services.HandoffDispatchResult, emergency bool) error {
	if summary == nil || item == nil || result == nil {
		return fmt.Errorf("转人工结果为空")
	}
	summary.handoffDispatchStatus = string(result.Status)
	item.Status = "success"
	switch result.Status {
	case services.HandoffDispatchStatusAwaitingRoomNumber:
		item.RecommendedAction = "collect_handoff_room_number"
		item.ResultPreview = "room number requested before direct human route"
	case services.HandoffDispatchStatusDispatched:
		item.RecommendedAction = "dispatch_human_route"
		item.ResultPreview = "routed directly to human reception"
		if emergency {
			item.RecommendedAction = "dispatch_emergency_handoff"
			item.ResultPreview = "emergency safety routed directly to human reception"
		}
	case services.HandoffDispatchStatusAlreadyActive:
		item.RecommendedAction = "human_route_already_active"
		item.ResultPreview = "human route already active"
	case services.HandoffDispatchStatusOffHours:
		item.RecommendedAction = "handoff_off_hours"
		item.ResultPreview = "human service is currently outside service hours"
	default:
		return fmt.Errorf("未知转人工状态: %s", result.Status)
	}
	return nil
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
