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
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"
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

const runtimeReplyMaxOutputTokens = 512

func newRuntimeReplyExecutor() *runtimeReplyExecutor {
	return &runtimeReplyExecutor{}
}

func (e *runtimeReplyExecutor) Run(ctx context.Context, input runtimeReplyRunInput) (*applicationruntime.Summary, error) {
	resolved, err := svc.ModelCallResolverService.ResolveForConversation(input.Conversation.ID, enums.ModelUsageSlotReplyLLM)
	if err != nil {
		return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorGenerationFailed, err)
	}
	modelConfig := normalizeRuntimeReplyModelConfig(resolved.RuntimeConfig())
	e.applyResolvedModelTrace(input.Trace, resolved, modelConfig)
	runtimeStartedAt := time.Now()
	runCtx, usageCapture := usagex.WithCapture(ctx)
	runCtx = usagex.WithScope(runCtx, buildModelUsageScope(resolved, input.Conversation.ID, input.Message.ID, input.Message.RequestID))
	summary, err := Service.Run(runCtx, applicationruntime.Request{
		Conversation: input.Conversation,
		UserMessage:  input.Message,
		AIAgent:      input.AIAgent,
		ModelConfig:  modelConfig,
	})
	if err != nil {
		if _, controlled := svc.AIReplyExecutionErrorCodeOf(err); !controlled {
			err = svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorGenerationFailed, err)
		}
	}
	e.recordReplyModelUsage(input.Conversation, input.Message, modelConfig, resolved, summary, usageCapture.Receipts(), err, time.Since(runtimeStartedAt).Milliseconds())
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
	resolved, err := svc.ModelCallResolverService.ResolveForConversation(input.Conversation.ID, enums.ModelUsageSlotReplyLLM)
	if err != nil {
		return nil, svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorGenerationFailed, err)
	}
	modelConfig := normalizeRuntimeReplyModelConfig(resolved.RuntimeConfig())
	e.applyResolvedModelTrace(input.Trace, resolved, modelConfig)
	runtimeStartedAt := time.Now()
	if input.Trace != nil {
		input.Trace.ResumeSource = "pending_interrupt"
	}
	resumeCtx, usageCapture := usagex.WithCapture(ctx)
	resumeCtx = usagex.WithScope(resumeCtx, buildModelUsageScope(resolved, input.Conversation.ID, input.Message.ID, input.Message.RequestID))
	summary, err := Service.Resume(resumeCtx, applicationruntime.ResumeRequest{
		Conversation: input.Conversation,
		AIAgent:      input.AIAgent,
		ModelConfig:  modelConfig,
		CheckPointID: strings.TrimSpace(input.PendingInterrupt.CheckPointID),
		ResumeData: map[string]string{
			strings.TrimSpace(input.PendingInterrupt.InterruptID): e.resumeMessageText(input.Message),
		},
	})
	if err != nil {
		if _, controlled := svc.AIReplyExecutionErrorCodeOf(err); !controlled {
			err = svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorGenerationFailed, err)
		}
	}
	e.recordReplyModelUsage(input.Conversation, input.Message, modelConfig, resolved, summary, usageCapture.Receipts(), err, time.Since(runtimeStartedAt).Milliseconds())
	if input.Trace != nil {
		input.Trace.RuntimeLatencyMs = time.Since(runtimeStartedAt).Milliseconds()
		e.fillTraceFromSummary(input.Trace, summary, err)
	}
	return summary, err
}

func (e *runtimeReplyExecutor) recordReplyModelUsage(conversation models.Conversation, message models.Message, modelConfig modelconfig.Config, resolved *svc.ModelCallConfig, summary *applicationruntime.Summary, receipts []usagex.Receipt, runErr error, latencyMS int64) {
	requestID := strings.TrimSpace(message.RequestID)
	if requestID == "" {
		return
	}
	errorMessage := ""
	if runErr != nil {
		errorMessage = "model_call_failed"
	}
	runID := ""
	if summary != nil {
		runID = strings.TrimSpace(summary.RunID)
	}
	if runID == "" {
		runID = "no-run-id"
	}
	if summary == nil {
		if runErr != nil {
			event := models.AIUsageEvent{
				EventKey:       fmt.Sprintf("%s:reply_generate:%s:failed", requestID, runID),
				ConversationID: conversation.ID, MessageID: message.ID, RequestID: requestID,
				Stage: "reply_generate", Provider: string(modelConfig.Provider), Model: modelConfig.ModelName,
				MetricSource: svc.AIUsageMetricSourceProviderOperation,
				LatencyMS:    latencyMS, Status: "failed", ErrorClass: errorMessage, ErrorMessage: errorMessage,
			}
			svc.AIUsageEventService.ApplyModelCallAttribution(&event, resolved)
			applyReplyGatewayReceipt(&event, receiptAt(receipts, 0))
			_ = svc.AIUsageEventService.Record(event)
		}
		return
	}
	for index, usage := range summary.ModelUsageCalls {
		event := models.AIUsageEvent{
			EventKey:       fmt.Sprintf("%s:reply_generate:%s:%d", requestID, runID, index+1),
			ConversationID: conversation.ID, MessageID: message.ID, RequestID: requestID,
			Stage: "reply_generate", Provider: string(modelConfig.Provider), Model: modelConfig.ModelName,
			PromptTokens: int64(usage.PromptTokens), CompletionTokens: int64(usage.CompletionTokens),
			CachedPromptTokens: int64(usage.CachedPromptTokens), ReasoningTokens: int64(usage.ReasoningTokens),
			MetricSource: svc.AIUsageMetricSourceUpstreamActual,
			Status:       "completed",
		}
		svc.AIUsageEventService.ApplyModelCallAttribution(&event, resolved)
		applyReplyGatewayReceipt(&event, receiptAt(receipts, index))
		_ = svc.AIUsageEventService.Record(event)
	}
	if len(summary.ModelUsageCalls) == 0 && runErr != nil {
		event := models.AIUsageEvent{
			EventKey:       fmt.Sprintf("%s:reply_generate:%s:failed", requestID, runID),
			ConversationID: conversation.ID, MessageID: message.ID, RequestID: requestID,
			Stage: "reply_generate", Provider: string(modelConfig.Provider), Model: modelConfig.ModelName,
			MetricSource: svc.AIUsageMetricSourceProviderOperation,
			LatencyMS:    latencyMS, Status: "failed", ErrorClass: errorMessage, ErrorMessage: errorMessage,
		}
		svc.AIUsageEventService.ApplyModelCallAttribution(&event, resolved)
		applyReplyGatewayReceipt(&event, receiptAt(receipts, 0))
		_ = svc.AIUsageEventService.Record(event)
	}
}

func receiptAt(receipts []usagex.Receipt, index int) *usagex.Receipt {
	if index < 0 || index >= len(receipts) {
		return nil
	}
	return &receipts[index]
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

func normalizeRuntimeReplyModelConfig(config modelconfig.Config) modelconfig.Config {
	if config.MaxOutputTokens <= 0 || config.MaxOutputTokens > runtimeReplyMaxOutputTokens {
		config.MaxOutputTokens = runtimeReplyMaxOutputTokens
	}
	return config
}

func (e *runtimeReplyExecutor) applyResolvedModelTrace(trace *aiReplyTraceData, resolved *svc.ModelCallConfig, config modelconfig.Config) {
	if trace == nil || resolved == nil {
		return
	}
	trace.StoreID = resolved.StoreID
	trace.ModelProfileID = resolved.ProfileID
	trace.ModelProfileRevision = resolved.ProfileRevision
	trace.ModelSlotID = resolved.SlotID
	trace.UsageSlot = string(resolved.UsageCode)
	trace.CredentialRevision = resolved.CredentialRevision
	trace.ModelSource = svc.AIModelSourceStoreProfile
	trace.ConfiguredMaxOutputTokens = resolved.MaxOutputTokens
	trace.EffectiveMaxOutputTokens = config.MaxOutputTokens
}

func buildModelUsageScope(resolved *svc.ModelCallConfig, conversationID, messageID int64, requestID string) usagex.Scope {
	return svc.ModelCallUsageScope(resolved, conversationID, messageID, requestID)
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
