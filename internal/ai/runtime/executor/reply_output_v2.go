package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/channelbreaker"
	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	"agent-desk/internal/pkg/strictjson"
	svc "agent-desk/internal/services"

	"github.com/cloudwego/eino/schema"
)

type replyOutputProtocolError struct {
	RawResponse string
	Reason      string
	Cause       error
}

func (e *replyOutputProtocolError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return "reply_output.v2 protocol invalid"
	}
	return "reply_output.v2 protocol invalid: " + strings.TrimSpace(e.Reason)
}

func (e *replyOutputProtocolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (s *Service) executeRuntimeV2DirectGeneration(
	ctx context.Context,
	req RunInput,
	messages []*schema.Message,
	summary *RunResult,
	collector *callbacks.RuntimeTraceCollector,
) error {
	chatModel, err := factory.NewChatModelFactory().Build(ctx, req.ModelConfig)
	if err != nil {
		return markRuntimeGenerationError(summary, collector, time.Now(), err)
	}
	startedAt := time.Now()

	// 阶段一（取数）：若有可用工具，先用不带结构化输出的模型跑工具循环，
	// 把工具调用与结果以 Tool 消息追加进上下文；无工具则跳过，保持单次 Generate。
	messages = runRuntimeToolCollection(ctx, req, messages, summary, collector)

	generate := func(input []*schema.Message) error {
		summary.ReplyModelAttempted = true
		if open, retryAt := channelbreaker.IsOpen("reply_generate", req.ModelConfig.ModelName, time.Now()); open {
			return fmt.Errorf("reply channel breaker open until %s", retryAt.Format(time.RFC3339))
		}
		response, generateErr := chatModel.Generate(ctx, input)
		if generateErr != nil {
			channelbreaker.RecordFailure("reply_generate", req.ModelConfig.ModelName, time.Now())
			return generateErr
		}
		channelbreaker.RecordSuccess("reply_generate", req.ModelConfig.ModelName)
		collectTokenUsage(response, summary, collector)
		if response == nil {
			summary.RawReplyOutput = ""
		} else {
			summary.RawReplyOutput = strings.TrimSpace(response.Content)
		}
		return applyRuntimeReplyOutputV2(summary.RawReplyOutput, summary, collector)
	}

	err = generate(messages)
	var protocolErr *replyOutputProtocolError
	if errors.As(err, &protocolErr) {
		resetRuntimeGenerationForProtocolRepair(summary, collector, "reply_output_v2_protocol_repair")
		repairMessages, compileErr := compileRuntimeReplyOutputRepairMessages(ctx, summary, protocolErr)
		if compileErr != nil {
			err = compileErr
		} else {
			err = generate(repairMessages)
		}
	}
	collector.Data.Pipeline.Generate.LatencyMs = time.Since(startedAt).Milliseconds()
	if err != nil {
		var repeatedProtocolErr *replyOutputProtocolError
		if errors.As(err, &repeatedProtocolErr) {
			err = fmt.Errorf("reply_output.v2 protocol repair failed: %w", err)
		}
		return markRuntimeGenerationError(summary, collector, startedAt, err)
	}
	return completeRuntimeGeneration(summary, collector, req.ModelConfig.ModelName, startedAt)
}

// runRuntimeToolCollection 是两阶段生成里的「取数」阶段。只在存在可用工具时执行，
// 最多 runtimeToolLoopMaxRounds 轮；无工具或无工具调用时返回原 messages。
func runRuntimeToolCollection(
	ctx context.Context,
	req RunInput,
	messages []*schema.Message,
	summary *RunResult,
	collector *callbacks.RuntimeTraceCollector,
) []*schema.Message {
	tools := collectRuntimeBaseTools(ctx, req)
	if len(tools) == 0 {
		return messages
	}
	toolInfos := buildRuntimeToolInfos(ctx, tools)
	if len(toolInfos) == 0 {
		return messages
	}

	// 取数阶段不带结构化输出，模型自由在「调工具 / 输出取数意图」间切换。
	toolConfig := req.ModelConfig
	toolConfig.StructuredOutput = nil
	chatModel, err := factory.NewChatModelFactory().Build(ctx, toolConfig)
	if err != nil {
		return messages
	}
	toolModel, err := chatModel.WithTools(toolInfos)
	if err != nil {
		return messages
	}

	nameToCode := runtimeToolNameToCode(req, toolInfos)
	loopMessages := append([]*schema.Message(nil), messages...)
	invoked := false
	for round := 0; round < runtimeToolLoopMaxRounds; round++ {
		if open, _ := channelbreaker.IsOpen("reply_generate", req.ModelConfig.ModelName, time.Now()); open {
			break
		}
		response, generateErr := toolModel.Generate(ctx, loopMessages)
		if generateErr != nil {
			channelbreaker.RecordFailure("reply_generate", req.ModelConfig.ModelName, time.Now())
			break
		}
		channelbreaker.RecordSuccess("reply_generate", req.ModelConfig.ModelName)
		collectTokenUsage(response, summary, collector)
		if response == nil || len(response.ToolCalls) == 0 {
			break
		}
		invoked = true
		loopMessages = append(loopMessages, response)
		for _, toolCall := range response.ToolCalls {
			if code := nameToCode[strings.TrimSpace(toolCall.Function.Name)]; code != "" {
				summary.InvokedToolCodes = appendIfMissing(summary.InvokedToolCodes, code)
			}
			result := executeRuntimeToolCall(ctx, tools, toolCall)
			loopMessages = append(loopMessages, schema.ToolMessage(result, toolCall.ID, schema.WithToolName(toolCall.Function.Name)))
		}
	}
	if invoked {
		summary.ToolCallCount = len(summary.InvokedToolCodes)
	}
	return loopMessages
}

func applyRuntimeReplyOutputV2(raw string, summary *RunResult, collector *callbacks.RuntimeTraceCollector) error {
	if summary == nil || summary.ReplyPlanV2 == nil || summary.EvidenceBundle == nil || summary.ActionLedgerV2 == nil {
		return fmt.Errorf("reply_output.v2 validation context is incomplete")
	}
	parsed, err := parseRuntimeReplyOutputV2(raw)
	if err != nil {
		if collector != nil {
			collector.Data.Pipeline.Validate.Status = "failed"
			collector.Data.Pipeline.Validate.Reason = replyProtocolErrorReason(err)
		}
		if runtimeProtocolRepairAllowed(err) {
			return &replyOutputProtocolError{RawResponse: raw, Reason: replyProtocolErrorReason(err), Cause: err}
		}
		return err
	}
	validation := NewReplyValidatorForMode(summary.RuntimeValidatorMode).Validate(ReplyValidationInput{
		Output: parsed, Plan: *summary.ReplyPlanV2, Evidence: *summary.EvidenceBundle, ActionLedger: *summary.ActionLedgerV2,
	})
	summary.ValidationResult = &validation
	if collector != nil {
		collector.Data.Pipeline.Validate.Status = validation.Status
		collector.Data.Pipeline.Validate.Reason = validationResultReason(validation)
	}
	switch validation.Status {
	case "passed":
		summary.ReplyParts = append([]contracts.ReplyPartV2(nil), validation.NormalizedParts...)
		summary.ReplyText = joinValidatedReplyParts(validation.NormalizedParts)
		return nil
	case "repairable_protocol_error":
		return &replyOutputProtocolError{RawResponse: raw, Reason: validationResultReason(validation)}
	default:
		return fmt.Errorf("reply_output.v2 rejected: %s", validationResultReason(validation))
	}
}

func compileRuntimeReplyOutputRepairMessages(ctx context.Context, summary *RunResult, protocolErr *replyOutputProtocolError) ([]*schema.Message, error) {
	if summary == nil || summary.GenerateCompileInput == nil || summary.CompiledContext == nil {
		return nil, fmt.Errorf("reply_output.v2 repair context is unavailable")
	}
	input := *summary.GenerateCompileInput
	input.RepairInstruction = buildRuntimeReplyOutputRepairInstruction(protocolErr)
	compiled, err := contextcompiler.New(nil).Compile(ctx, input)
	if err != nil {
		return nil, err
	}
	if compiled.Fingerprint != summary.CompiledContext.Fingerprint {
		return nil, fmt.Errorf("reply_output.v2 repair context fingerprint changed")
	}
	return compiled.Messages, nil
}

func buildRuntimeReplyOutputRepairInstruction(protocolErr *replyOutputProtocolError) string {
	reason := "invalid_protocol"
	raw := ""
	if protocolErr != nil {
		reason = strings.TrimSpace(protocolErr.Reason)
		raw = boundedRuntimeRepairText(protocolErr.RawResponse, 8*1024)
	}
	return strings.Join([]string{
		"上一版输出存在可修复的 reply_output.v2 协议错误。只修复 JSON 结构和任务覆盖，不新增、删除或改写业务任务。",
		"error=" + reason,
		"第一次输出=" + raw,
		"重新输出唯一一个严格 JSON Object；不得输出 Markdown、解释、注释或额外文本。",
	}, "\n")
}

func replyProtocolErrorReason(err error) string {
	if code, ok := strictjson.CodeOf(err); ok {
		return code
	}
	return "invalid_reply_output"
}

func validationResultReason(result contracts.ValidationResultV1) string {
	if len(result.Errors) == 0 {
		return strings.TrimSpace(result.Status)
	}
	codes := make([]string, 0, len(result.Errors))
	for _, issue := range result.Errors {
		code := strings.TrimSpace(issue.Code)
		if code != "" {
			codes = append(codes, code)
		}
	}
	if len(codes) == 0 {
		return strings.TrimSpace(result.Status)
	}
	return strings.Join(uniqueTrimmedStrings(codes), ",")
}

func joinValidatedReplyParts(parts []contracts.ReplyPartV2) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if content := strings.TrimSpace(part.Content); content != "" {
			texts = append(texts, content)
		}
	}
	return strings.Join(texts, "\n<<NEXT_MESSAGE>>\n")
}

func markRuntimeGenerationError(summary *RunResult, collector *callbacks.RuntimeTraceCollector, startedAt time.Time, cause error) error {
	if summary == nil || collector == nil {
		return svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorGenerationFailed, cause)
	}
	err := cause
	if _, controlled := svc.AIReplyExecutionErrorCodeOf(err); !controlled {
		err = svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorGenerationFailed, cause)
	}
	summary.Status = "error"
	summary.ErrorMessage = err.Error()
	collector.Data.Status = summary.Status
	collector.Data.Error.Message = summary.ErrorMessage
	collector.Data.Error.Stage = "generate"
	collector.Data.Pipeline.Generate.Status = "failed"
	collector.Data.Pipeline.Generate.Reason = summary.ErrorMessage
	if collector.Data.Pipeline.Validate.Status != "rejected" {
		collector.Data.Pipeline.Validate.Status = "failed"
	}
	if strings.TrimSpace(collector.Data.Pipeline.Validate.Reason) == "" {
		collector.Data.Pipeline.Validate.Reason = summary.ErrorMessage
	}
	collector.Data.Pipeline.Generate.LatencyMs = time.Since(startedAt).Milliseconds()
	return err
}

func completeRuntimeGeneration(summary *RunResult, collector *callbacks.RuntimeTraceCollector, modelName string, startedAt time.Time) error {
	if summary == nil || collector == nil {
		return svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorGenerationFailed, fmt.Errorf("runtime generation state is required"))
	}
	collector.Data.Pipeline.Generate.LatencyMs = time.Since(startedAt).Milliseconds()
	summary.ModelName = modelName
	if !summary.Interrupted && len(summary.ReplyParts) == 0 && strings.TrimSpace(summary.ReplyText) == "" && !hasInvokedGraphTool(summary.InvokedToolCodes) {
		err := svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorEmptyOutput, fmt.Errorf("runtime produced no reply or action"))
		return markRuntimeGenerationError(summary, collector, startedAt, err)
	}
	if summary.Status == "started" || summary.Status == "fallback" {
		summary.Status = "completed"
	}
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = summary.ReplyText
	collector.Data.Output.FinishReason = summary.Status
	collector.Data.Pipeline.Generate.Status = summary.Status
	collector.Data.Pipeline.Generate.Reason = "reply_output.v2 generated and validated"
	if strings.TrimSpace(collector.Data.Pipeline.Validate.Status) == "" {
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "reply_output.v2 passed deterministic validation"
	}
	return nil
}
