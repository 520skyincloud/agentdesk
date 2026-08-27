package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pkg/usagex"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// ErrGeneratedReplyExecution marks an upstream Generate event failure that is
// safe for the caller to retry without rerunning Intent or knowledge stages.
var ErrGeneratedReplyExecution = errors.New("generated reply execution failed")

func IsGeneratedReplyExecutionError(err error) bool {
	return errors.Is(err, ErrGeneratedReplyExecution)
}

func consumeAgentEvents(ctx context.Context, events *adk.AsyncIterator[*adk.AgentEvent], summary *RunResult, collector *callbacks.RuntimeTraceCollector, toolDefsByModelName map[string]string) error {
	if summary == nil {
		return nil
	}
	if collector == nil {
		collector = callbacks.NewRuntimeTraceCollector()
	}
	suppressAssistantReply := false
	var protocolErr error
	var executionErr error
	for {
		event, ok := events.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			summary.Status = "interrupted"
			summary.Interrupted = true
			summary.Interrupts = buildInterruptSummaries(event)
		}
		if event.Err != nil {
			errMsg := strings.TrimSpace(event.Err.Error())
			if errMsg != "" {
				summary.Status = "error"
				summary.ErrorMessage = errMsg
				if executionErr == nil {
					executionErr = fmt.Errorf("%w: %v", ErrGeneratedReplyExecution, event.Err)
				}
			}
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		messageOutput := event.Output.MessageOutput
		collectTokenUsage(ctx, messageOutput.Message, summary, collector)
		switch messageOutput.Role {
		case schema.Assistant:
			if suppressAssistantReply {
				continue
			}
			replyText := strings.TrimSpace(messageOutput.Message.Content)
			replyText, err := normalizeGeneratedReplyPartsResult(
				replyText,
				collector.Data.Pipeline.ReplyPlan,
				collector.Data.Pipeline.EvidenceJudge.DeferredHandoff,
			)
			if err == nil {
				replyText, err = SanitizeGeneratedReplyText(replyText)
			}
			if err != nil {
				if isBlockedInternalReplyMarkerError(err) {
					collector.Data.Pipeline.Generate.BlockedInternalMarker = true
				}
				if errors.Is(err, errGeneratedReplyProtocol) && protocolErr == nil {
					protocolErr = err
				}
				summary.Status = "error"
				summary.ErrorMessage = err.Error()
				continue
			}
			if looksLikeBareToolCallText(replyText) {
				continue
			}
			if replyText != "" {
				summary.ReplyText = replyText
				collector.Data.Pipeline.Generate.ComposedMessageCount = generatedReplyMessageCount(replyText)
			}
		case schema.Tool:
			toolName := strings.TrimSpace(messageOutput.ToolName)
			if toolName == "" {
				continue
			}
			toolCode := toolName
			if mappedCode, ok := toolDefsByModelName[toolName]; ok && strings.TrimSpace(mappedCode) != "" {
				toolCode = strings.TrimSpace(mappedCode)
			}
			summary.InvokedToolCodes = appendIfMissing(summary.InvokedToolCodes, toolCode)
			if strings.TrimSpace(summary.ReplyText) == "" && toolx.ResolveToolSourceType(toolCode) == enums.ToolSourceTypeGraph {
				toolReplyText := strings.TrimSpace(messageOutput.Message.Content)
				if result, ok := tooling.ParseToolResult(toolReplyText); ok {
					if result.ReplyText != "" && !result.ReplySent {
						summary.ReplyText = result.ReplyText
					}
					if result.Terminal && !result.ShouldRetry {
						suppressAssistantReply = true
					}
				}
			}
		}
	}
	if summary.Status == "started" {
		switch {
		case strings.TrimSpace(summary.ErrorMessage) != "":
			summary.Status = "error"
		case summary.Interrupted:
			summary.Status = "interrupted"
		case strings.TrimSpace(summary.ReplyText) != "":
			summary.Status = "completed"
		case hasInvokedGraphTool(summary.InvokedToolCodes):
			summary.Status = "completed"
		default:
			summary.Status = "fallback"
		}
	}
	summary.ToolCallCount = len(summary.InvokedToolCodes)
	if protocolErr != nil {
		return protocolErr
	}
	return executionErr
}

func isBlockedInternalReplyMarkerError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "internal reply header")
}

func generatedReplyMessageCount(text string) int {
	parts, _ := splitGeneratedReplyMessages(text)
	count := 0
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			count++
		}
	}
	return count
}

func collectTokenUsage(ctx context.Context, message *schema.Message, summary *RunResult, collector *callbacks.RuntimeTraceCollector) {
	if message == nil || message.ResponseMeta == nil || message.ResponseMeta.Usage == nil || summary == nil {
		return
	}
	usage := message.ResponseMeta.Usage
	summary.ModelUsageCalls = append(summary.ModelUsageCalls, ModelUsageCall{
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CachedPromptTokens:    usage.PromptTokenDetails.CachedTokens,
		ReasoningTokens:       usage.CompletionTokensDetails.ReasoningTokens,
		HasUsage:              true,
		GatewayReceiptOrdinal: currentGatewayReceiptOrdinal(ctx),
		Status:                "completed",
	})
	summary.PromptTokens += usage.PromptTokens
	summary.CompletionTokens += usage.CompletionTokens
	summary.TotalTokens += usage.TotalTokens
	summary.CachedPromptTokens += usage.PromptTokenDetails.CachedTokens
	summary.ReasoningTokens += usage.CompletionTokensDetails.ReasoningTokens
	if collector != nil {
		collector.Data.Model.Usage.PromptTokens += usage.PromptTokens
		collector.Data.Model.Usage.CompletionTokens += usage.CompletionTokens
		collector.Data.Model.Usage.TotalTokens += usage.TotalTokens
		collector.Data.Model.Usage.CachedPromptTokens += usage.PromptTokenDetails.CachedTokens
		collector.Data.Model.Usage.ReasoningTokens += usage.CompletionTokensDetails.ReasoningTokens
	}
}

func currentGatewayReceiptOrdinal(ctx context.Context) int {
	capture := usagex.CaptureFromContext(ctx)
	if capture == nil {
		return 0
	}
	return len(capture.Receipts())
}

func hasInvokedGraphTool(toolCodes []string) bool {
	for _, toolCode := range toolCodes {
		if toolx.ResolveToolSourceType(toolCode) == enums.ToolSourceTypeGraph {
			return true
		}
	}
	return false
}

func looksLikeBareToolCallText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "handoff_to_human(") || strings.Contains(lower, "graph/handoff_to_human") {
		return true
	}
	if strings.HasPrefix(lower, "handoff_to_human") || strings.HasPrefix(lower, "tool_call") || strings.HasPrefix(lower, "function_call") {
		return true
	}
	return false
}

func buildInterruptSummaries(event *adk.AgentEvent) []InterruptContextSummary {
	if event == nil || event.Action == nil || event.Action.Interrupted == nil {
		return nil
	}
	interrupts := event.Action.Interrupted.InterruptContexts
	result := make([]InterruptContextSummary, 0, len(interrupts))
	for _, item := range interrupts {
		if item == nil {
			continue
		}
		result = append(result, InterruptContextSummary{
			ID:          strings.TrimSpace(item.ID),
			InfoPreview: previewInterruptInfo(item.Info),
		})
	}
	return result
}
