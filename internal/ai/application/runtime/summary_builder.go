package runtime

import (
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/executor"
	"strings"
)

func toSummary(summary *executor.RunResult) *Summary {
	if summary == nil {
		return nil
	}
	ret := &Summary{
		RunID:                     summary.RunID,
		Status:                    summary.Status,
		GenerationOutcome:         string(summary.GenerationOutcome),
		ReplyText:                 summary.ReplyText,
		PlannedSkillCode:          strings.TrimSpace(summary.SelectedSkillCode),
		PlannedSkillName:          strings.TrimSpace(summary.SelectedSkillName),
		PlanReason:                strings.TrimSpace(summary.SkillRouteReason),
		SkillRouteTrace:           strings.TrimSpace(summary.SkillRouteTrace),
		SkillAllowedToolCodes:     append([]string(nil), summary.SkillAllowedToolCodes...),
		ModelName:                 summary.ModelName,
		PromptTokens:              summary.PromptTokens,
		CompletionTokens:          summary.CompletionTokens,
		TotalTokens:               summary.TotalTokens,
		CachedPromptTokens:        summary.CachedPromptTokens,
		ReasoningTokens:           summary.ReasoningTokens,
		HistoryMessageCount:       summary.HistoryMessageCount,
		RetrieverCount:            summary.RetrieverCount,
		ToolCallCount:             summary.ToolCallCount,
		ToolCodes:                 append([]string(nil), summary.ToolCodes...),
		InvokedToolCodes:          append([]string(nil), summary.InvokedToolCodes...),
		CheckPointID:              summary.CheckPointID,
		Interrupted:               summary.Interrupted,
		TraceData:                 summary.TraceData,
		ErrorMessage:              summary.ErrorMessage,
		PolicySkipped:             summary.SkipReply,
		ReplyModelAttempted:       summary.ReplyModelAttempted,
		TaskLedgerEnabled:         summary.TaskLedgerEnabled,
		TaskKeys:                  append([]string(nil), summary.TaskKeys...),
		FailedTaskKeys:            append([]string(nil), summary.FailedTaskKeys...),
		HumanTaskKeys:             append([]string(nil), summary.HumanTaskKeys...),
		HasRemainingTasks:         summary.HasRemainingTasks,
		NeedsHumanDispatch:        summary.NeedsHumanDispatch,
		CoveredByTaskID:           summary.CoveredByTaskID,
		ReplyParts:                append([]contracts.ReplyPartV2(nil), summary.ReplyParts...),
		PreparedActions:           append([]contracts.PreparedActionV1(nil), summary.PreparedActions...),
		ActionLedgerAuthoritative: summary.ActionLedgerAuthoritative,
	}
	if summary.ActionLedgerV2 != nil {
		ledger := *summary.ActionLedgerV2
		ledger.Actions = append([]contracts.ActionLedgerItemV1(nil), summary.ActionLedgerV2.Actions...)
		ret.ActionLedgerV2 = &ledger
	}
	if len(summary.ModelUsageCalls) > 0 {
		ret.ModelUsageCalls = make([]ModelUsageCall, 0, len(summary.ModelUsageCalls))
		for _, usage := range summary.ModelUsageCalls {
			ret.ModelUsageCalls = append(ret.ModelUsageCalls, ModelUsageCall{
				PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
				CachedPromptTokens: usage.CachedPromptTokens, ReasoningTokens: usage.ReasoningTokens,
			})
		}
	}
	if len(summary.Interrupts) > 0 {
		ret.Interrupts = make([]InterruptContextSummary, 0, len(summary.Interrupts))
		for _, item := range summary.Interrupts {
			ret.Interrupts = append(ret.Interrupts, InterruptContextSummary{
				Type:        item.Type,
				ID:          item.ID,
				InfoPreview: item.InfoPreview,
			})
		}
	}
	return ret
}
