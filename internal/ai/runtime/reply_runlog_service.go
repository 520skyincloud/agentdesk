package runtime

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/toolx"
	svc "agent-desk/internal/services"
)

func newReplyRunLogService() *replyRunLogService {
	return &replyRunLogService{}
}

type replyRunLogService struct{}

type replyRunLogInput struct {
	StartedAt    time.Time
	Message      models.Message
	Conversation models.Conversation
	AIAgent      models.AIAgent
	Question     string
	RunErr       error
	Trace        *aiReplyTraceData
	Summary      *applicationruntime.Summary
}

func (s *replyRunLogService) Write(input replyRunLogInput) {
	errorMessage := ""
	if input.RunErr != nil {
		errorMessage = input.RunErr.Error()
	} else if input.Summary != nil && strings.TrimSpace(input.Summary.ErrorMessage) != "" {
		errorMessage = strings.TrimSpace(input.Summary.ErrorMessage)
	}
	traceData := buildAIReplyTraceData(input.Trace)
	plannedAction, plannedToolCode, planReason := buildRunLogPlan(input.Summary)
	logItem := &models.AgentRunLog{
		ConversationID:   input.Conversation.ID,
		MessageID:        input.Message.ID,
		RequestID:        input.Message.RequestID,
		AIAgentID:        input.AIAgent.ID,
		AIConfigID:       input.AIAgent.AIConfigID,
		UserMessage:      strings.TrimSpace(input.Question),
		PlannedAction:    plannedAction,
		PlannedSkillCode: strings.TrimSpace(summaryPlannedSkillCode(input.Summary)),
		PlannedSkillName: strings.TrimSpace(summaryPlannedSkillName(input.Summary)),
		SkillRouteTrace:  strings.TrimSpace(summarySkillRouteTrace(input.Summary)),
		ToolSearchTrace:  extractToolSearchTrace(input.Summary),
		GraphToolTrace:   extractGraphToolTrace(input.Summary),
		GraphToolCode:    firstGraphToolCode(input.Summary),
		HandoffReason:    extractHandoffReason(input.Summary),
		PlannedToolCode:  plannedToolCode,
		PlanReason:       planReason,
		InterruptType:    firstInterruptType(input.Summary),
		ResumeSource:     runLogResumeSource(input.Trace),
		FinalAction:      toRunLogFinalAction(input.Summary),
		FinalStatus:      runLogFinalStatus(input.Summary),
		ReplyText:        buildRunLogReplyText(input.Summary),
		ErrorMessage:     errorMessage,
		LatencyMs:        time.Since(input.StartedAt).Milliseconds(),
		TraceData:        traceData,
		CreatedAt:        time.Now(),
	}
	if err := svc.AgentRunLogService.Create(logItem); err != nil {
		slog.Warn("create agent run log failed",
			"requestId", input.Message.RequestID,
			"message_id", input.Message.ID,
			"conversation_id", logItem.ConversationID,
			"ai_agent_id", input.AIAgent.ID,
			"error", err)
	}
}

func buildAIReplyTraceData(trace *aiReplyTraceData) string {
	if trace == nil {
		return ""
	}
	data, err := json.Marshal(trace)
	if err != nil {
		return ""
	}
	return string(data)
}

func buildRunLogPlan(summary *applicationruntime.Summary) (plannedAction, plannedToolCode, planReason string) {
	if summary == nil {
		return "", "", ""
	}
	if skillCode := strings.TrimSpace(summaryPlannedSkillCode(summary)); skillCode != "" {
		reason := strings.TrimSpace(summary.PlanReason)
		if reason == "" {
			reason = "skill_selected"
		}
		return "skill", "", reason
	}
	if strings.TrimSpace(summary.Status) == "expired" {
		return "interrupt", "", "pending interrupt checkpoint expired"
	}
	if summary.Interrupted {
		if graphToolCode := firstGraphToolCode(summary); graphToolCode != "" {
			reason := graphPlanReason(summary)
			if reason == "" {
				reason = "graph tool interrupted and is waiting for user confirmation"
			}
			return "graph", graphToolCode, reason
		}
		return "tool", summaryPrimaryToolCode(summary), "agent interrupted and is waiting for user confirmation"
	}
	if len(summary.InvokedToolCodes) > 0 {
		if graphToolCode := firstGraphToolCode(summary); graphToolCode != "" {
			reason := graphPlanReason(summary)
			if reason == "" {
				reason = "agent invoked graph tool"
			}
			return "graph", graphToolCode, reason
		}
		toolCode := summaryPrimaryToolCode(summary)
		reason := "agent invoked MCP tool"
		if toolCode != "" && toolCode != firstInvokedToolCode(summary) {
			reason = "agent invoked dynamic tool via tool_search"
		}
		return "tool", toolCode, reason
	}
	if strings.TrimSpace(summary.ReplyText) != "" {
		return "reply", "", "agent replied directly"
	}
	if strings.TrimSpace(summary.ErrorMessage) != "" {
		return "error", "", "runtime execution failed"
	}
	return "fallback", "", "runtime produced empty reply"
}

func toRunLogFinalAction(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	if skillCode := strings.TrimSpace(summaryPlannedSkillCode(summary)); skillCode != "" && strings.TrimSpace(summary.ReplyText) != "" {
		return "skill"
	}
	if graphToolCode := firstGraphToolCode(summary); graphToolCode != "" && strings.TrimSpace(summary.ReplyText) != "" {
		return "graph"
	}
	switch strings.TrimSpace(summary.Status) {
	case "completed":
		return "reply"
	case "fallback":
		return "fallback"
	case "error":
		return "error"
	case "interrupted":
		return "interrupted"
	case "expired":
		return "expired"
	default:
		return strings.TrimSpace(summary.Status)
	}
}

func buildRunLogReplyText(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	return strings.TrimSpace(summary.ReplyText)
}

func summaryPlannedSkillCode(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	return strings.TrimSpace(summary.PlannedSkillCode)
}

func summaryPlannedSkillName(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	return strings.TrimSpace(summary.PlannedSkillName)
}

func summarySkillRouteTrace(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	return strings.TrimSpace(summary.SkillRouteTrace)
}

func runLogResumeSource(trace *aiReplyTraceData) string {
	if trace == nil {
		return ""
	}
	return strings.TrimSpace(trace.ResumeSource)
}

func runLogFinalStatus(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	return strings.TrimSpace(summary.Status)
}

func summaryPrimaryToolCode(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	toolCode := firstInvokedToolCode(summary)
	if toolCode != toolx.BuiltinToolSearch.Code {
		return toolCode
	}
	if targetToolCode := firstToolSearchTargetToolCode(summary); targetToolCode != "" {
		return targetToolCode
	}
	return toolCode
}

func extractToolSearchTrace(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	trace := parseRuntimeTraceData(summary.TraceData)
	if len(trace.ToolSearch.Items) == 0 {
		return ""
	}
	buf, err := json.Marshal(trace.ToolSearch)
	if err != nil {
		return ""
	}
	return string(buf)
}

func extractGraphToolTrace(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	trace := parseRuntimeTraceData(summary.TraceData)
	if len(trace.GraphTools.Items) == 0 {
		return ""
	}
	buf, err := json.Marshal(trace.GraphTools)
	if err != nil {
		return ""
	}
	return string(buf)
}

func firstToolSearchTargetToolCode(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	trace := parseRuntimeTraceData(summary.TraceData)
	for _, item := range trace.ToolSearch.Items {
		toolCode := strings.TrimSpace(item.TargetToolCode)
		if toolCode != "" {
			return toolCode
		}
		if len(item.CandidateToolCodes) == 1 {
			toolCode = strings.TrimSpace(item.CandidateToolCodes[0])
			if toolCode != "" {
				return toolCode
			}
		}
	}
	return ""
}

func firstGraphToolCode(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	trace := parseRuntimeTraceData(summary.TraceData)
	for _, item := range trace.GraphTools.Items {
		toolCode := strings.TrimSpace(item.ToolCode)
		if toolCode != "" {
			return toolCode
		}
	}
	return ""
}

func extractHandoffReason(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	trace := parseRuntimeTraceData(summary.TraceData)
	for _, item := range trace.GraphTools.Items {
		if strings.TrimSpace(item.ToolCode) != toolx.GraphHandoffConversation.Code {
			continue
		}
		if len(item.Arguments) == 0 {
			return ""
		}
		var args runtimeTraceHandoffArguments
		if err := json.Unmarshal(item.Arguments, &args); err != nil {
			return ""
		}
		return strings.TrimSpace(args.Reason)
	}
	return ""
}

func graphPlanReason(summary *applicationruntime.Summary) string {
	if summary == nil {
		return ""
	}
	trace := parseRuntimeTraceData(summary.TraceData)
	for _, item := range trace.GraphTools.Items {
		toolCode := strings.TrimSpace(item.ToolCode)
		switch toolCode {
		case toolx.GraphTriageServiceRequest.Code:
			recommendedAction := strings.TrimSpace(item.RecommendedAction)
			if recommendedAction == "" {
				return "graph tool triaged service request"
			}
			if item.TicketDraftReady {
				return "graph tool triaged service request: " + recommendedAction + " with ready ticket draft"
			}
			return "graph tool triaged service request: " + recommendedAction
		case toolx.GraphAnalyzeConversation.Code:
			recommendedAction := strings.TrimSpace(item.RecommendedAction)
			riskLevel := strings.TrimSpace(item.RiskLevel)
			switch {
			case recommendedAction != "" && riskLevel != "":
				return "graph tool analyzed conversation: " + recommendedAction + " (" + riskLevel + " risk)"
			case recommendedAction != "":
				return "graph tool analyzed conversation: " + recommendedAction
			case riskLevel != "":
				return "graph tool analyzed conversation (" + riskLevel + " risk)"
			default:
				return "graph tool analyzed conversation"
			}
		}
	}
	return ""
}

type runtimeTraceProjection struct {
	ToolSearch struct {
		Items []struct {
			TargetToolCode     string   `json:"targetToolCode"`
			CandidateToolCodes []string `json:"candidateToolCodes"`
		} `json:"items"`
	} `json:"toolSearch"`
	GraphTools struct {
		Items []struct {
			ToolCode          string          `json:"toolCode"`
			Arguments         json.RawMessage `json:"arguments"`
			RecommendedAction string          `json:"recommendedAction"`
			RiskLevel         string          `json:"riskLevel"`
			TicketDraftReady  bool            `json:"ticketDraftReady"`
		} `json:"items"`
	} `json:"graphTools"`
}

type runtimeTraceHandoffArguments struct {
	Reason string `json:"reason"`
}

func parseRuntimeTraceData(raw string) runtimeTraceProjection {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return runtimeTraceProjection{}
	}
	var trace runtimeTraceProjection
	if err := json.Unmarshal([]byte(raw), &trace); err != nil {
		return runtimeTraceProjection{}
	}
	return trace
}
