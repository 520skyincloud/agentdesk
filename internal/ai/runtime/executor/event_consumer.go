package executor

import (
	"fmt"
	"strings"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/toolx"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func consumeAgentEvents(events *adk.AsyncIterator[*adk.AgentEvent], summary *RunResult, collector *callbacks.RuntimeTraceCollector, toolDefsByModelName map[string]string) error {
	if summary == nil {
		return fmt.Errorf("runtime summary is required")
	}
	if collector == nil {
		collector = callbacks.NewRuntimeTraceCollector()
	}
	suppressAssistantReply := false
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
			summary.Status = "error"
			summary.ErrorMessage = "generation_failed"
			return event.Err
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		messageOutput := event.Output.MessageOutput
		collectTokenUsage(messageOutput.Message, summary, collector)
		switch messageOutput.Role {
		case schema.Assistant:
			if suppressAssistantReply {
				continue
			}
			replyText := strings.TrimSpace(messageOutput.Message.Content)
			if looksLikeBareToolCallText(replyText) {
				continue
			}
			if replyText != "" {
				if summary.UseRuntimeV2Generate || summary.UseRuntimeV3Generate {
					summary.RawReplyOutput = replyText
				} else {
					var normalizeErr error
					replyText, normalizeErr = normalizeGeneratedReplyPartsStrict(replyText, collector.Data.Pipeline.ReplyPlan)
					if normalizeErr != nil {
						return normalizeErr
					}
					summary.ReplyText = replyText
				}
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
		case strings.TrimSpace(summary.ReplyText) != "" || strings.TrimSpace(summary.RawReplyOutput) != "":
			summary.Status = "completed"
		case hasInvokedGraphTool(summary.InvokedToolCodes):
			summary.Status = "completed"
		default:
			summary.Status = "fallback"
		}
	}
	summary.ToolCallCount = len(summary.InvokedToolCodes)
	return nil
}

func collectTokenUsage(message *schema.Message, summary *RunResult, collector *callbacks.RuntimeTraceCollector) {
	if message == nil || message.ResponseMeta == nil || message.ResponseMeta.Usage == nil || summary == nil {
		return
	}
	usage := message.ResponseMeta.Usage
	summary.ModelUsageCalls = append(summary.ModelUsageCalls, ModelUsageCall{
		PromptTokens:       usage.PromptTokens,
		CompletionTokens:   usage.CompletionTokens,
		CachedPromptTokens: usage.PromptTokenDetails.CachedTokens,
		ReasoningTokens:    usage.CompletionTokensDetails.ReasoningTokens,
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
