package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/schema"
)

const (
	generatedReplyMaxAttempts = 2
	generatedReplyRetryDelay  = 300 * time.Millisecond
)

type generatedReplyAttemptFunc func(context.Context, []*schema.Message) error

type generatedReplyRecoveryResult struct {
	AttemptCount int
	FallbackMode string
}

const (
	resumeGeneratedReplyFallbackMode = "resume_reinterrupt"
	resumeGeneratedReplyPrompt       = "我需要你的明确确认，请直接回复“确认”或“取消”。"
)

type resumeGeneratedReplyInterruptInfo struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func runGeneratedReplyWithRecovery(
	ctx context.Context,
	messages []*schema.Message,
	summary *RunResult,
	collector *callbacks.RuntimeTraceCollector,
	stillEligible func() bool,
	attempt generatedReplyAttemptFunc,
) (generatedReplyRecoveryResult, error) {
	result := generatedReplyRecoveryResult{}
	if attempt == nil {
		return result, fmt.Errorf("generated reply attempt is required")
	}
	runInvokedToolCodes := append([]string(nil), summaryInvokedToolCodes(summary)...)
	defer func() {
		restoreGeneratedReplyRunToolState(summary, runInvokedToolCodes)
	}()
	var lastErr error
	for attemptIndex := 1; attemptIndex <= generatedReplyMaxAttempts; attemptIndex++ {
		if attemptIndex > 1 {
			if stillEligible != nil && !stillEligible() {
				markGeneratedReplyCancelled(summary, collector)
				result.FallbackMode = "cancelled_before_retry"
				return result, nil
			}
			if !sleepGeneratedReplyRetry(ctx, generatedReplyRetryDelay) {
				return result, ctx.Err()
			}
		}

		resetGeneratedReplyAttemptState(summary)
		attemptMessages := messages
		if attemptIndex > 1 {
			attemptMessages = appendGeneratedReplyRepairInstruction(messages, lastErr)
		}
		receiptOffset := generatedReplyGatewayReceiptCount(ctx)
		usageOffset := generatedReplyModelUsageCount(summary)
		toolTraceOffset := generatedReplyToolTraceCount(collector)
		result.AttemptCount = attemptIndex
		attemptErr := attempt(ctx, attemptMessages)
		runInvokedToolCodes = mergeGeneratedReplyToolCodes(runInvokedToolCodes, summaryInvokedToolCodes(summary))
		runInvokedToolCodes = mergeGeneratedReplyToolCodes(runInvokedToolCodes, generatedReplyToolCodesSince(collector, toolTraceOffset))
		attemptInvokedTool := generatedReplyToolTraceCount(collector) > toolTraceOffset
		if attemptErr == nil && generatedReplyAttemptHasOutcome(summary) {
			recordGeneratedReplyAttemptModelCalls(ctx, summary, receiptOffset, usageOffset, nil)
			markGeneratedReplyAttemptTrace(collector, result.AttemptCount, "")
			return result, nil
		}
		if attemptErr == nil {
			attemptErr = fmt.Errorf("%w: generate completed without a customer-visible reply", ErrGeneratedReplyProtocol)
		}
		recordGeneratedReplyAttemptModelCalls(ctx, summary, receiptOffset, usageOffset, attemptErr)
		lastErr = attemptErr
		recordGeneratedReplyProtocolError(collector, lastErr)

		if attemptIndex == generatedReplyMaxAttempts ||
			!isRetryableGeneratedReplyError(lastErr) ||
			len(runInvokedToolCodes) > 0 || attemptInvokedTool ||
			summary.Interrupted {
			break
		}
	}

	fallback := deterministicGeneratedReplyFallback(collector)
	if fallback != "" {
		summary.Status = "completed"
		summary.ReplyText = fallback
		summary.ErrorMessage = ""
		if collector != nil {
			collector.Data.Pipeline.Generate.ComposedMessageCount = generatedReplyMessageCount(fallback)
		}
		result.FallbackMode = "supported_facts"
		markGeneratedReplyAttemptTrace(collector, result.AttemptCount, result.FallbackMode)
		return result, nil
	}
	markGeneratedReplyAttemptTrace(collector, result.AttemptCount, "unavailable")
	return result, lastErr
}

func runResumedGeneratedReplyWithRecovery(
	ctx context.Context,
	summary *RunResult,
	collector *callbacks.RuntimeTraceCollector,
	interruptID string,
	stillEligible func() bool,
	attempt generatedReplyAttemptFunc,
) (generatedReplyRecoveryResult, error) {
	result, err := runGeneratedReplyWithRecovery(ctx, nil, summary, collector, stillEligible, attempt)
	if err == nil || (summary != nil && summary.SkipReply) {
		return result, err
	}
	if !IsGeneratedReplyProtocolError(err) && !isRetryableGeneratedReplyError(err) {
		return result, err
	}
	if !applyDeterministicResumeInterruptFallback(summary, collector, interruptID) {
		return result, err
	}
	result.FallbackMode = resumeGeneratedReplyFallbackMode
	return result, nil
}

func applyDeterministicResumeInterruptFallback(summary *RunResult, collector *callbacks.RuntimeTraceCollector, interruptID string) bool {
	interruptID = strings.TrimSpace(interruptID)
	if summary == nil || interruptID == "" {
		return false
	}
	info, err := json.Marshal(resumeGeneratedReplyInterruptInfo{
		Type:    "ticket_creation_confirmation",
		Message: resumeGeneratedReplyPrompt,
	})
	if err != nil {
		return false
	}

	summary.Status = "interrupted"
	summary.ReplyText = ""
	summary.ErrorMessage = ""
	summary.Interrupted = true
	summary.Interrupts = []InterruptContextSummary{{
		Type:        "ticket_creation_confirmation",
		ID:          interruptID,
		InfoPreview: string(info),
	}}
	if collector != nil {
		collector.Data.Status = summary.Status
		collector.Data.Output.ReplyText = ""
		collector.Data.Output.FinishReason = "resume_generate_fallback_reinterrupt"
		collector.Data.Error.Message = ""
		collector.Data.Error.Stage = ""
		collector.Data.Pipeline.Generate.Status = "interrupted"
		collector.Data.Pipeline.Generate.FallbackMode = resumeGeneratedReplyFallbackMode
		collector.Data.Pipeline.Generate.Reason = "resume generate recovery preserved the pending confirmation interrupt"
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "resume failure was replaced with a deterministic confirmation re-prompt"
	}
	return true
}

func resolveResumeInterruptID(req ResumeInput) string {
	keys := make([]string, 0, len(req.ResumeData))
	for key := range req.ResumeData {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) > 0 {
		sort.Strings(keys)
		return keys[0]
	}
	interrupt := services.ConversationInterruptService.GetByCheckPointID(req.CheckPointID)
	if interrupt == nil {
		return ""
	}
	return strings.TrimSpace(interrupt.InterruptID)
}

func recordGeneratedReplyProtocolError(collector *callbacks.RuntimeTraceCollector, err error) {
	if collector == nil || !IsGeneratedReplyProtocolError(err) {
		return
	}
	collector.Data.Pipeline.Generate.LastProtocolError = compactGeneratedReplyRecoveryError(err)
}

func generatedReplyAttemptHasOutcome(summary *RunResult) bool {
	if summary == nil {
		return false
	}
	return strings.TrimSpace(summary.ReplyText) != "" ||
		hasInvokedGraphTool(summary.InvokedToolCodes) ||
		summary.Interrupted ||
		summary.SkipReply
}

func resetGeneratedReplyAttemptState(summary *RunResult) {
	if summary == nil {
		return
	}
	summary.Status = "started"
	summary.ReplyText = ""
	summary.ErrorMessage = ""
	summary.SkipReply = false
	summary.Interrupted = false
	summary.Interrupts = nil
	summary.InvokedToolCodes = nil
	summary.ToolCallCount = 0
}

func summaryInvokedToolCodes(summary *RunResult) []string {
	if summary == nil {
		return nil
	}
	return summary.InvokedToolCodes
}

func mergeGeneratedReplyToolCodes(existing []string, additions []string) []string {
	ret := append([]string(nil), existing...)
	for _, toolCode := range additions {
		ret = appendIfMissing(ret, strings.TrimSpace(toolCode))
	}
	return ret
}

func restoreGeneratedReplyRunToolState(summary *RunResult, invokedToolCodes []string) {
	if summary == nil {
		return
	}
	summary.InvokedToolCodes = append([]string(nil), invokedToolCodes...)
	summary.ToolCallCount = len(summary.InvokedToolCodes)
}

func generatedReplyGatewayReceiptCount(ctx context.Context) int {
	capture := usagex.CaptureFromContext(ctx)
	if capture == nil {
		return 0
	}
	return len(capture.Receipts())
}

func generatedReplyModelUsageCount(summary *RunResult) int {
	if summary == nil {
		return 0
	}
	return len(summary.ModelUsageCalls)
}

func generatedReplyToolTraceCount(collector *callbacks.RuntimeTraceCollector) int {
	if collector == nil {
		return 0
	}
	return len(collector.Data.Tools.Items)
}

func generatedReplyToolCodesSince(collector *callbacks.RuntimeTraceCollector, offset int) []string {
	if collector == nil {
		return nil
	}
	items := collector.Data.Tools.Items
	if offset < 0 || offset > len(items) {
		offset = len(items)
	}
	ret := make([]string, 0, len(items)-offset)
	for _, item := range items[offset:] {
		toolCode := strings.TrimSpace(item.ToolCode)
		if toolCode == "" {
			toolCode = strings.TrimSpace(item.ToolName)
		}
		ret = appendIfMissing(ret, toolCode)
	}
	return ret
}

func recordGeneratedReplyAttemptModelCalls(ctx context.Context, summary *RunResult, receiptOffset int, usageOffset int, attemptErr error) {
	if summary == nil {
		return
	}
	if usageOffset < 0 || usageOffset > len(summary.ModelUsageCalls) {
		usageOffset = len(summary.ModelUsageCalls)
	}
	prefix := append([]ModelUsageCall(nil), summary.ModelUsageCalls[:usageOffset]...)
	attemptUsage := append([]ModelUsageCall(nil), summary.ModelUsageCalls[usageOffset:]...)
	usageByReceipt := make(map[int]int)
	unboundUsageIndexes := make([]int, 0, len(attemptUsage))
	for index := range attemptUsage {
		if strings.TrimSpace(attemptUsage[index].Status) == "" {
			attemptUsage[index].Status = "completed"
		}
		ordinal := attemptUsage[index].GatewayReceiptOrdinal
		if ordinal > 0 {
			if _, exists := usageByReceipt[ordinal]; !exists {
				usageByReceipt[ordinal] = index
				continue
			}
		}
		unboundUsageIndexes = append(unboundUsageIndexes, index)
	}

	capture := usagex.CaptureFromContext(ctx)
	var receipts []usagex.Receipt
	if capture != nil {
		receipts = capture.Receipts()
	}
	if receiptOffset < 0 || receiptOffset > len(receipts) {
		receiptOffset = len(receipts)
	}
	attemptCalls := make([]ModelUsageCall, 0, len(receipts)-receiptOffset+len(attemptUsage))
	usedUsageIndexes := make(map[int]struct{}, len(attemptUsage))
	unboundIndex := 0
	for index := receiptOffset; index < len(receipts); index++ {
		ordinal := index + 1
		if usageIndex, exists := usageByReceipt[ordinal]; exists {
			attemptCalls = append(attemptCalls, attemptUsage[usageIndex])
			usedUsageIndexes[usageIndex] = struct{}{}
			continue
		}
		if receipts[index].StatusCode < 400 && unboundIndex < len(unboundUsageIndexes) {
			usageIndex := unboundUsageIndexes[unboundIndex]
			attemptUsage[usageIndex].GatewayReceiptOrdinal = ordinal
			attemptCalls = append(attemptCalls, attemptUsage[usageIndex])
			usedUsageIndexes[usageIndex] = struct{}{}
			unboundIndex++
			continue
		}
		status := "completed"
		errorMessage := ""
		if receipts[index].StatusCode >= 400 || (attemptErr != nil && index == len(receipts)-1) {
			status = "failed"
			errorMessage = "model_call_failed"
		}
		attemptCalls = append(attemptCalls, ModelUsageCall{
			GatewayReceiptOrdinal: ordinal,
			Status:                status,
			ErrorMessage:          errorMessage,
		})
	}
	for index, call := range attemptUsage {
		if _, used := usedUsageIndexes[index]; used {
			continue
		}
		attemptCalls = append(attemptCalls, call)
	}
	if len(attemptCalls) == 0 && attemptErr != nil {
		attemptCalls = append(attemptCalls, ModelUsageCall{
			Status:       "failed",
			ErrorMessage: "model_call_failed",
		})
	}
	nextSequence := generatedReplyNextModelCallSequence(prefix)
	for index := range attemptCalls {
		if attemptCalls[index].CallSequence <= 0 {
			nextSequence++
			attemptCalls[index].CallSequence = nextSequence
			continue
		}
		if attemptCalls[index].CallSequence > nextSequence {
			nextSequence = attemptCalls[index].CallSequence
		}
	}
	summary.ModelUsageCalls = append(prefix, attemptCalls...)
}

func generatedReplyNextModelCallSequence(calls []ModelUsageCall) int {
	sequence := 0
	for _, call := range calls {
		if call.CallSequence > sequence {
			sequence = call.CallSequence
		}
	}
	return sequence
}

func appendGeneratedReplyRepairInstruction(messages []*schema.Message, previousErr error) []*schema.Message {
	ret := append([]*schema.Message(nil), messages...)
	reason := compactGeneratedReplyRecoveryError(previousErr)
	instruction := "【输出协议修复】上一版回复未通过本地完整性校验。任务、来源和知识证据已经冻结，禁止重新解释问题或增加事实。请严格按任务输出契约重新输出全部 replyParts，并补齐所有缺少的 taskId、coveredFactIds 和关键值。"
	if reason != "" {
		instruction += " 上一版缺陷：" + reason + "。"
	}
	ret = append(ret, schema.SystemMessage(instruction))
	return ret
}

func compactGeneratedReplyRecoveryError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if len([]rune(text)) <= 180 {
		return text
	}
	return string([]rune(text)[:180])
}

func isRetryableGeneratedReplyError(err error) bool {
	if err == nil {
		return false
	}
	if IsGeneratedReplyProtocolError(err) {
		return true
	}
	if !IsGeneratedReplyExecutionError(err) {
		return false
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "context canceled") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden") || strings.Contains(lower, "status 400") || strings.Contains(lower, "status 401") || strings.Contains(lower, "status 403") {
		return false
	}
	for _, marker := range []string{
		"status 429", "too many requests", "rate limit",
		"connection reset", "connection refused", "broken pipe", "unexpected eof",
		"timeout", "timed out", "temporarily unavailable", "temporary failure",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return generatedReplyServerStatusPattern.MatchString(lower)
}

var generatedReplyServerStatusPattern = regexp.MustCompile(`status\s+5\d\d`)

func deterministicGeneratedReplyFallback(collector *callbacks.RuntimeTraceCollector) string {
	if collector == nil {
		return ""
	}
	plan := collector.Data.Pipeline.ReplyPlan
	groups := buildTextReplyTaskGroups(plan)
	if len(groups) == 0 {
		return ""
	}
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		statements := make([]string, 0, len(group.Facts))
		for _, fact := range group.Facts {
			statement := strings.TrimSpace(fact.Statement)
			if statement != "" {
				statements = appendIfMissing(statements, statement)
			}
		}
		if len(statements) == 0 {
			if text := deterministicInteractionFallback(plan, group.TaskID); text != "" {
				parts = append(parts, text)
				continue
			}
			if text := deterministicKnowledgeFallback(plan, group.TaskID); text != "" {
				parts = append(parts, text)
				continue
			}
			return ""
		}
		parts = append(parts, joinGeneratedReplyFactStatements(statements))
	}
	return composeGeneratedReplyContents(parts, 3)
}

func deterministicKnowledgeFallback(plan callbacks.ReplyPlanTraceData, taskID string) string {
	for _, task := range plan.TaskPlans {
		if strings.TrimSpace(task.TaskID) != strings.TrimSpace(taskID) {
			continue
		}
		if strings.TrimSpace(task.Intent) == "hotel_info" || strings.TrimSpace(task.Output) == "knowledge_text_reply" {
			return "不好意思，这个我暂时没法准确回答。"
		}
	}
	return ""
}

func deterministicInteractionFallback(plan callbacks.ReplyPlanTraceData, taskID string) string {
	for _, task := range plan.TaskPlans {
		if strings.TrimSpace(task.TaskID) != strings.TrimSpace(taskID) || strings.TrimSpace(task.Intent) != "interaction" {
			continue
		}
		switch strings.TrimSpace(task.SubIntent) {
		case "greeting":
			return "在的呀，您说。"
		case "thanks", "gratitude":
			return "不客气呀。"
		case "acknowledgement", "accepted", "confirmation":
			return "好的。"
		case "social":
			return "嗯嗯，在的呀。"
		case "correction":
			return "不好意思，是我理解错了。"
		case "clarify":
			return "您具体想问哪方面呀？"
		case "frustration":
			return "不好意思，您把当前问题发我，我继续帮您处理。"
		case "media_context_follow_up", "actionable_media_context":
			return "您具体想问图片或文件里的哪一部分呀？"
		case "chat", "small_talk":
			return "在的呀，您说。"
		default:
			return "在的呀，您说。"
		}
	}
	return ""
}

func joinGeneratedReplyFactStatements(statements []string) string {
	parts := make([]string, 0, len(statements))
	for _, statement := range statements {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		parts = append(parts, statement)
	}
	return strings.Join(parts, " ")
}

func sleepGeneratedReplyRetry(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func markGeneratedReplyCancelled(summary *RunResult, collector *callbacks.RuntimeTraceCollector) {
	if summary != nil {
		summary.Status = "completed"
		summary.ReplyText = ""
		summary.ErrorMessage = ""
		summary.SkipReply = true
	}
	if collector != nil {
		collector.Data.Pipeline.Generate.Status = "skipped"
		collector.Data.Pipeline.Generate.Reason = "reply became ineligible before generate retry"
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "stale generate retry was cancelled"
	}
}

func markGeneratedReplyAttemptTrace(collector *callbacks.RuntimeTraceCollector, attempts int, fallbackMode string) {
	if collector == nil {
		return
	}
	collector.Data.Pipeline.Generate.AttemptCount = attempts
	collector.Data.Pipeline.Generate.FallbackMode = strings.TrimSpace(fallbackMode)
	if attempts > 1 {
		collector.Data.Pipeline.Generate.Reason = fmt.Sprintf("generate completed after %d attempts", attempts)
	}
	if fallbackMode != "" {
		collector.Data.Pipeline.Generate.Reason = fmt.Sprintf("generate recovery used %s after %d attempts", fallbackMode, attempts)
	}
}

func canContinueGeneratedReply(req RunInput) bool {
	if req.Conversation.ID <= 0 || req.UserMessage.ID <= 0 {
		return true
	}
	return services.MessageService.CanSendAIReply(req.Conversation.ID, req.UserMessage.RequestID, req.UserMessage.ID)
}

func canContinueResumedGeneratedReply(req ResumeInput) bool {
	if req.Conversation.ID <= 0 {
		return true
	}
	interrupt := services.ConversationInterruptService.GetByCheckPointID(req.CheckPointID)
	if interrupt == nil {
		return services.MessageService.CanSendAIReply(req.Conversation.ID, "", 0)
	}
	if interrupt.ConversationID > 0 && interrupt.ConversationID != req.Conversation.ID {
		return false
	}
	if interrupt.SourceMessageID <= 0 {
		return services.MessageService.CanSendAIReply(req.Conversation.ID, "", 0)
	}
	source := services.MessageService.Get(interrupt.SourceMessageID)
	if source == nil || source.ConversationID != req.Conversation.ID {
		return false
	}
	return services.MessageService.CanSendAIReply(req.Conversation.ID, source.RequestID, source.ID)
}
