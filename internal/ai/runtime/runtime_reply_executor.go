package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	applicationruntime "agent-desk/internal/ai/application/runtime"
	"agent-desk/internal/ai/runtime/graphs"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/pkg/utils"
	svc "agent-desk/internal/services"
)

type runtimeReplyExecutor struct{}

type runtimeReplyRunInput struct {
	Conversation models.Conversation
	Message      models.Message
	AIAgent      models.AIAgent
	Trace        *aiReplyTraceData
}

type runtimeReplyResumeInput struct {
	Conversation     models.Conversation
	Message          models.Message
	AIAgent          models.AIAgent
	PendingInterrupt *models.ConversationInterrupt
	Trace            *aiReplyTraceData
}

const (
	runtimeReplyDefaultMaxOutputTokens = 512
	runtimeReplyMaxOutputTokens        = 1024
)

func newRuntimeReplyExecutor() *runtimeReplyExecutor {
	return &runtimeReplyExecutor{}
}

func (e *runtimeReplyExecutor) Run(ctx context.Context, input runtimeReplyRunInput) (*applicationruntime.Summary, error) {
	resolved, err := svc.StoreAIModelSettingService.ResolveForConversation(input.Conversation.ID, svc.StoreAIModelUsageReplyLLM, input.AIAgent.AIConfigID)
	if err != nil {
		return nil, err
	}
	aiConfig := normalizeRuntimeReplyAIConfig(resolved.Config)
	if input.Trace != nil {
		input.Trace.AIConfigID = aiConfig.ID
		input.Trace.ModelSource = resolved.Source
		input.Trace.ModelSettingID = resolved.ModelSettingID
		input.Trace.ConfiguredMaxOutputTokens = resolved.Config.MaxOutputTokens
		input.Trace.EffectiveMaxOutputTokens = aiConfig.MaxOutputTokens
	}
	input.AIAgent.AIConfigID = aiConfig.ID
	runtimeStartedAt := time.Now()
	runCtx, usageCapture := usagex.WithCapture(ctx)
	runCtx = usagex.WithScope(runCtx, usagex.Scope{
		ConversationID: input.Conversation.ID, MessageID: input.Message.ID,
		RequestID: input.Message.RequestID, CredentialRevision: resolved.CredentialRevision,
		ModelSource: resolved.Source,
	})
	summary, err := Service.Run(runCtx, applicationruntime.Request{
		Conversation: input.Conversation,
		UserMessage:  input.Message,
		AIAgent:      input.AIAgent,
		AIConfig:     aiConfig,
	})
	e.recordReplyModelUsage(input.Conversation, input.Message, aiConfig, resolved.Source, resolved.CredentialRevision, summary, usageCapture.Receipts(), err, time.Since(runtimeStartedAt).Milliseconds())
	if input.Trace != nil {
		input.Trace.RuntimeLatencyMs = time.Since(runtimeStartedAt).Milliseconds()
		e.fillTraceFromSummary(input.Trace, summary, err)
	}
	return summary, err
}

func (e *runtimeReplyExecutor) ResumePendingInterrupt(ctx context.Context, input runtimeReplyResumeInput) (*applicationruntime.Summary, error) {
	if input.PendingInterrupt == nil {
		return nil, fmt.Errorf("pending interrupt is required")
	}
	resolved, err := svc.StoreAIModelSettingService.ResolveForConversation(input.Conversation.ID, svc.StoreAIModelUsageReplyLLM, input.AIAgent.AIConfigID)
	if err != nil {
		return nil, err
	}
	aiConfig := normalizeRuntimeReplyAIConfig(resolved.Config)
	if input.Trace != nil {
		input.Trace.AIConfigID = aiConfig.ID
		input.Trace.ModelSource = resolved.Source
		input.Trace.ModelSettingID = resolved.ModelSettingID
		input.Trace.ConfiguredMaxOutputTokens = resolved.Config.MaxOutputTokens
		input.Trace.EffectiveMaxOutputTokens = aiConfig.MaxOutputTokens
	}
	input.AIAgent.AIConfigID = aiConfig.ID
	runtimeStartedAt := time.Now()
	if input.Trace != nil {
		input.Trace.ResumeSource = "pending_interrupt"
	}
	resumeCtx, usageCapture := usagex.WithCapture(ctx)
	resumeCtx = usagex.WithScope(resumeCtx, usagex.Scope{
		ConversationID: input.Conversation.ID, MessageID: input.Message.ID,
		RequestID: input.Message.RequestID, CredentialRevision: resolved.CredentialRevision,
		ModelSource: resolved.Source,
	})
	summary, err := Service.Resume(resumeCtx, applicationruntime.ResumeRequest{
		Conversation: input.Conversation,
		AIAgent:      input.AIAgent,
		AIConfig:     aiConfig,
		CheckPointID: strings.TrimSpace(input.PendingInterrupt.CheckPointID),
		ResumeData: map[string]string{
			strings.TrimSpace(input.PendingInterrupt.InterruptID): e.resumeMessageText(input.Message),
		},
	})
	e.recordReplyModelUsage(input.Conversation, input.Message, aiConfig, resolved.Source, resolved.CredentialRevision, summary, usageCapture.Receipts(), err, time.Since(runtimeStartedAt).Milliseconds())
	if input.Trace != nil {
		input.Trace.RuntimeLatencyMs = time.Since(runtimeStartedAt).Milliseconds()
		e.fillTraceFromSummary(input.Trace, summary, err)
	}
	return summary, err
}

func (e *runtimeReplyExecutor) recordReplyModelUsage(conversation models.Conversation, message models.Message, aiConfig models.AIConfig, modelSource string, credentialRevision int64, summary *applicationruntime.Summary, receipts []usagex.Receipt, runErr error, latencyMS int64) {
	for _, event := range buildReplyModelUsageEvents(conversation, message, aiConfig, modelSource, credentialRevision, summary, receipts, runErr, latencyMS) {
		_ = svc.AIUsageEventService.Record(event)
	}
}

func buildReplyModelUsageEvents(conversation models.Conversation, message models.Message, aiConfig models.AIConfig, modelSource string, credentialRevision int64, summary *applicationruntime.Summary, receipts []usagex.Receipt, runErr error, latencyMS int64) []models.AIUsageEvent {
	requestID := strings.TrimSpace(message.RequestID)
	if requestID == "" {
		return nil
	}
	runID := ""
	var calls []applicationruntime.ModelUsageCall
	if summary != nil {
		runID = strings.TrimSpace(summary.RunID)
		calls = append([]applicationruntime.ModelUsageCall(nil), summary.ModelUsageCalls...)
	}
	if runID == "" {
		runID = "no-run-id"
	}

	usedReceipts := make(map[int]struct{}, len(receipts))
	for index := range calls {
		ordinal := calls[index].GatewayReceiptOrdinal
		if ordinal <= 0 || ordinal > len(receipts) {
			continue
		}
		if _, exists := usedReceipts[ordinal]; exists {
			calls[index].GatewayReceiptOrdinal = 0
			continue
		}
		usedReceipts[ordinal] = struct{}{}
	}
	for index := range calls {
		if calls[index].GatewayReceiptOrdinal > 0 || !calls[index].HasUsage {
			continue
		}
		for receiptIndex, receipt := range receipts {
			ordinal := receiptIndex + 1
			if receipt.StatusCode >= 400 {
				continue
			}
			if _, used := usedReceipts[ordinal]; used {
				continue
			}
			calls[index].GatewayReceiptOrdinal = ordinal
			usedReceipts[ordinal] = struct{}{}
			break
		}
	}

	events := make([]models.AIUsageEvent, 0, len(calls)+len(receipts))
	eventIndex := 0
	usedReceipts = make(map[int]struct{}, len(receipts))
	for _, call := range calls {
		eventIndex++
		event := newReplyModelUsageEvent(
			fmt.Sprintf("%s:reply_generate:%s:%d", requestID, runID, eventIndex),
			conversation,
			message,
			aiConfig,
			modelSource,
			credentialRevision,
		)
		applyReplyModelUsageCall(&event, call, runErr)
		ordinal := call.GatewayReceiptOrdinal
		if ordinal > 0 && ordinal <= len(receipts) {
			receipt := &receipts[ordinal-1]
			usedReceipts[ordinal] = struct{}{}
			applyReplyGatewayReceipt(&event, receipt)
			if receipt.StatusCode >= 400 {
				event.Status = "failed"
				event.ErrorMessage = "model_call_failed"
			}
		} else if event.Status == "failed" {
			event.LatencyMS = latencyMS
		}
		events = append(events, event)
	}
	for receiptIndex := range receipts {
		ordinal := receiptIndex + 1
		if _, used := usedReceipts[ordinal]; used {
			continue
		}
		eventIndex++
		event := newReplyModelUsageEvent(
			fmt.Sprintf("%s:reply_generate:%s:%d", requestID, runID, eventIndex),
			conversation,
			message,
			aiConfig,
			modelSource,
			credentialRevision,
		)
		event.MetricSource = svc.AIUsageMetricSourceProviderOperation
		event.Status = "completed"
		if receipts[receiptIndex].StatusCode >= 400 {
			event.Status = "failed"
			event.ErrorMessage = "model_call_failed"
		}
		applyReplyGatewayReceipt(&event, &receipts[receiptIndex])
		events = append(events, event)
	}
	if len(events) == 0 && runErr != nil {
		event := newReplyModelUsageEvent(
			fmt.Sprintf("%s:reply_generate:%s:failed", requestID, runID),
			conversation,
			message,
			aiConfig,
			modelSource,
			credentialRevision,
		)
		event.MetricSource = svc.AIUsageMetricSourceProviderOperation
		event.LatencyMS = latencyMS
		event.Status = "failed"
		event.ErrorMessage = "model_call_failed"
		events = append(events, event)
	}
	return events
}

func newReplyModelUsageEvent(eventKey string, conversation models.Conversation, message models.Message, aiConfig models.AIConfig, modelSource string, credentialRevision int64) models.AIUsageEvent {
	return models.AIUsageEvent{
		EventKey:       eventKey,
		ConversationID: conversation.ID, MessageID: message.ID, RequestID: strings.TrimSpace(message.RequestID),
		Stage: "reply_generate", Provider: string(aiConfig.Provider), Model: aiConfig.ModelName,
		AIConfigID: aiConfig.ID, ModelSource: modelSource, CredentialRevision: credentialRevision,
	}
}

func applyReplyModelUsageCall(event *models.AIUsageEvent, call applicationruntime.ModelUsageCall, runErr error) {
	if event == nil {
		return
	}
	event.MetricSource = svc.AIUsageMetricSourceProviderOperation
	if call.HasUsage {
		event.PromptTokens = int64(call.PromptTokens)
		event.CompletionTokens = int64(call.CompletionTokens)
		event.CachedPromptTokens = int64(call.CachedPromptTokens)
		event.ReasoningTokens = int64(call.ReasoningTokens)
		event.MetricSource = svc.AIUsageMetricSourceUpstreamActual
	}
	event.Status = strings.TrimSpace(call.Status)
	if event.Status == "" {
		switch {
		case call.HasUsage:
			event.Status = "completed"
		case runErr != nil:
			event.Status = "failed"
		default:
			event.Status = "completed"
		}
	}
	event.ErrorMessage = strings.TrimSpace(call.ErrorMessage)
	if event.Status == "failed" && event.ErrorMessage == "" {
		event.ErrorMessage = "model_call_failed"
	}
}

func applyReplyGatewayReceipt(event *models.AIUsageEvent, receipt *usagex.Receipt) {
	if event == nil || receipt == nil {
		return
	}
	event.Gateway = receipt.Gateway
	event.GatewayRequestID = receipt.RequestID
	event.GatewayUpstreamID = receipt.UpstreamRequestID
	event.CallStartedAt = &receipt.StartedAt
	event.CallFinishedAt = &receipt.FinishedAt
	if receipt.LatencyMS() > 0 {
		event.LatencyMS = receipt.LatencyMS()
	}
}

func normalizeRuntimeReplyAIConfig(config models.AIConfig) models.AIConfig {
	if config.MaxOutputTokens <= 0 {
		config.MaxOutputTokens = runtimeReplyDefaultMaxOutputTokens
	} else if config.MaxOutputTokens > runtimeReplyMaxOutputTokens {
		config.MaxOutputTokens = runtimeReplyMaxOutputTokens
	}
	return config
}

func (e *runtimeReplyExecutor) fillTraceFromSummary(trace *aiReplyTraceData, summary *applicationruntime.Summary, runErr error) {
	if trace == nil {
		return
	}
	if runErr != nil {
		trace.Status = "runtime_error"
		trace.FinalAction = "error"
		if summary != nil {
			trace.Runtime = json.RawMessage(summary.TraceData)
		}
		return
	}
	trace.Status = "runtime_prepared"
	trace.FinalAction = toRunLogFinalAction(summary)
	if summary != nil && strings.TrimSpace(summary.TraceData) != "" {
		trace.Runtime = json.RawMessage(summary.TraceData)
	}
}

func (e *runtimeReplyExecutor) resumeMessageText(message models.Message) string {
	return strings.TrimSpace(utils.BuildRuntimeMessageTextWithPayload(message.MessageType, message.Content, message.Payload))
}

func expiredInterruptSummary() *applicationruntime.Summary {
	return &applicationruntime.Summary{
		Status:    "expired",
		ReplyText: graphs.ConfirmationExpiredReply,
	}
}
