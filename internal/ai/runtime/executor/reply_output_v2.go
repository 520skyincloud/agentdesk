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
	startedAt := time.Now()
	// P9 门禁快照：按当前请求范围计算，Validator 按此启用/跳过（默认全开）。
	summary.ValidationGates = ReplyValidationGates{
		FactSourceBoundary: gateEnabled(gateFactSourceBoundary, req),
		UnsupportedDomain:  gateEnabled(gateEvidenceQuality, req),
	}
	chatModel, err := factory.NewChatModelFactory().Build(ctx, req.ModelConfig)
	if err != nil {
		return finishRuntimeV2GenerationFailure(ctx, req, summary, collector, startedAt, err)
	}

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
		return applyRuntimeReplyOutputV2(summary.RawReplyOutput, summary, collector, req)
	}

	err = generate(messages)
	if err != nil {
		recordRuntimeGenerationFailure(collector, false, err)
	}
	var protocolErr *replyOutputProtocolError
	if errors.As(err, &protocolErr) {
		resetRuntimeGenerationForProtocolRepair(summary, collector, "reply_output_v2_protocol_repair")
		repairMessages, compileErr := compileRuntimeReplyOutputRepairMessages(ctx, summary, protocolErr)
		if compileErr != nil {
			err = compileErr
			recordRuntimeGenerationFailure(collector, true, err)
		} else {
			err = generate(repairMessages)
			if err != nil {
				recordRuntimeGenerationFailure(collector, true, err)
			}
		}
	}
	if err != nil {
		return finishRuntimeV2GenerationFailure(ctx, req, summary, collector, startedAt, err)
	}
	collector.Data.Pipeline.Generate.LatencyMs = time.Since(startedAt).Milliseconds()
	return completeRuntimeGeneration(summary, collector, req.ModelConfig.ModelName, startedAt)
}

func finishRuntimeV2GenerationFailure(
	ctx context.Context,
	req RunInput,
	summary *RunResult,
	collector *callbacks.RuntimeTraceCollector,
	startedAt time.Time,
	err error,
) error {
	collector.Data.Pipeline.Generate.LatencyMs = time.Since(startedAt).Milliseconds()
	if collector.Data.Pipeline.Generate.InitialErrorCode == "" {
		recordRuntimeGenerationFailure(collector, false, err)
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		collector.Data.Pipeline.Generate.Status = "cancelled"
		collector.Data.Pipeline.Generate.Reason = "superseded runtime generation cancelled"
		collector.Data.Pipeline.Validate.Status = "cancelled"
		collector.Data.Pipeline.Validate.Reason = "newer turn owns reply generation"
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return context.Canceled
	}
	if applySafeRuntimeDegraded(summary, collector, req, err) {
		return completeRuntimeGeneration(summary, collector, req.ModelConfig.ModelName, startedAt)
	}
	var repeatedProtocolErr *replyOutputProtocolError
	if errors.As(err, &repeatedProtocolErr) {
		err = fmt.Errorf("reply_output.v2 protocol repair failed: %w", err)
	}
	return markRuntimeGenerationError(summary, collector, startedAt, err)
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

func applyRuntimeReplyOutputV2(raw string, summary *RunResult, collector *callbacks.RuntimeTraceCollector, req RunInput) error {
	if summary == nil || summary.ReplyPlanV2 == nil || summary.EvidenceBundle == nil || summary.ActionLedgerV2 == nil {
		return fmt.Errorf("reply_output.v2 validation context is incomplete")
	}
	parsed, err := parseRuntimeReplyOutputV2(raw)
	if err != nil {
		// 生产回归 2026-08-18：模型偶发在 JSON 前后夹带说明文字或思考前缀
		// （行李类问题连续触发 json_root_not_object → 技术失败提示）。先做
		// 宽松提取（首个 { 到最后一个 }）再解码一次；仍失败才走协议修复。
		if extracted := extractLooseJSONObject(raw); extracted != "" && extracted != strings.TrimSpace(raw) {
			retryParsed, retryErr := parseRuntimeReplyOutputV2(extracted)
			if retryErr == nil {
				parsed, err = retryParsed, nil
			}
		}
	}
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
		Req: req, Output: parsed, Plan: *summary.ReplyPlanV2, Evidence: *summary.EvidenceBundle, ActionLedger: *summary.ActionLedgerV2,
		Gates: summary.ValidationGates,
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

// parseRuntimeReplyOutputV3AsV2 解码 reply_output.v3 并映射到 V2 校验输入；
// groupKey 仅审计不作为权威（分组由服务端知识层证据集合决定）。
func parseRuntimeReplyOutputV3AsV2(raw string) (contracts.ReplyOutputV2, error) {
	parsed, err := strictjson.DecodeObject[contracts.ReplyOutputV3]([]byte(raw), strictjson.DecodeOptions{
		MaxBytes: 64 * 1024, Schema: contracts.MustSchema(contracts.SchemaReplyOutputV3),
	})
	if err != nil {
		return contracts.ReplyOutputV2{}, err
	}
	if parsed.SchemaVersion != contracts.ReplyOutputV3SchemaVersion {
		return contracts.ReplyOutputV2{}, fmt.Errorf("reply_output.v3 schema version mismatch")
	}
	ret := contracts.ReplyOutputV2{Parts: make([]contracts.ReplyPartV2, 0, len(parsed.Parts))}
	seenGroups := make(map[string]struct{}, len(parsed.Parts))
	for _, part := range parsed.Parts {
		if _, dup := seenGroups[part.GroupKey]; dup {
			return contracts.ReplyOutputV2{}, fmt.Errorf("reply_output.v3 group %s split across parts", part.GroupKey)
		}
		seenGroups[part.GroupKey] = struct{}{}
		ret.Parts = append(ret.Parts, contracts.ReplyPartV2{
			TaskKeys: part.TaskKeys, Content: strings.TrimSpace(part.Content),
			EvidenceRefs: nil, ActionRefs: nil,
		})
	}
	return ret, nil
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
	if protocolErr != nil {
		reason = strings.TrimSpace(protocolErr.Reason)
	}
	return strings.Join([]string{
		"上一版输出存在可修复的 reply_output.v2 协议错误。只修复 JSON 结构和任务覆盖，不新增、删除或改写业务任务。",
		"error=" + reason,
		"重新输出唯一一个严格 JSON Object；不得输出 Markdown、解释、注释或额外文本。",
	}, "\n")
}

const (
	authoritativeStoreNameEvidenceTitle    = "当前门店名称（系统权威）"
	authoritativeStoreAddressEvidenceTitle = "当前门店地址（系统权威）"
	authoritativeStorePhoneEvidenceTitle   = "当前门店电话（系统权威）"
)

// applySafeRuntimeDegraded may only expose server-owned scalar facts. It is not
// a second answer engine and must never render FastGPT text, process templates,
// cached answers, generic acknowledgements, or no-hit business replies.
func applySafeRuntimeDegraded(summary *RunResult, collector *callbacks.RuntimeTraceCollector, req RunInput, cause error) bool {
	if summary == nil || summary.ReplyPlanV2 == nil || summary.EvidenceBundle == nil || summary.ActionLedgerV2 == nil {
		return false
	}
	parts := buildSafeRuntimeDegradedParts(*summary.ReplyPlanV2, *summary.EvidenceBundle)
	if len(parts) == 0 {
		return false
	}
	validationPlan := safeDegradedValidationPlan(*summary.ReplyPlanV2, parts)
	validation := NewReplyValidatorForMode(summary.RuntimeValidatorMode).Validate(ReplyValidationInput{
		Req: req, Output: contracts.ReplyOutputV2{SchemaVersion: contracts.ReplyOutputV2SchemaVersion, Parts: parts},
		Plan: validationPlan, Evidence: *summary.EvidenceBundle, ActionLedger: *summary.ActionLedgerV2,
		Gates: summary.ValidationGates,
	})
	if validation.Status != "passed" {
		return false
	}
	summary.ValidationResult = &validation
	summary.ReplyParts = append([]contracts.ReplyPartV2(nil), validation.NormalizedParts...)
	summary.ReplyText = joinValidatedReplyParts(validation.NormalizedParts)
	summary.Status = string(GenerationOutcomeSafeDegraded)
	summary.ErrorMessage = ""
	setGenerationOutcome(summary, collector, GenerationOutcomeSafeDegraded)
	collector.Data.Status = summary.Status
	collector.Data.Error.Message = runtimeGenerationFailureCode(collector, cause)
	collector.Data.Error.Stage = "generate_safe_degraded"
	collector.Data.Pipeline.Generate.Status = string(GenerationOutcomeSafeDegraded)
	collector.Data.Pipeline.Generate.Mode = string(GenerationOutcomeSafeDegraded)
	collector.Data.Pipeline.Generate.Reason = "generation failed; only authoritative scalar facts were allowed through safe degraded mode"
	collector.Data.Pipeline.Validate.Status = "passed"
	collector.Data.Pipeline.Validate.Reason = "safe degraded scalar facts passed deterministic validation"
	return strings.TrimSpace(summary.ReplyText) != ""
}

func buildSafeRuntimeDegradedParts(plan contracts.ReplyPlanV2, evidence contracts.EvidenceBundleV1) []contracts.ReplyPartV2 {
	parts := make([]contracts.ReplyPartV2, 0, min(plan.GlobalConstraints.MaxReplyParts, 3))
	for _, task := range plan.Tasks {
		if task.OutputMode != "text" && task.OutputMode != "text_and_resource" && task.OutputMode != "clarification" {
			continue
		}
		content, evidenceRef := safeRuntimeScalarFact(task, evidence)
		if content == "" || evidenceRef == "" {
			continue
		}
		part := contracts.ReplyPartV2{
			TaskKeys: []string{task.TaskKey}, Content: content,
			EvidenceRefs: []string{evidenceRef}, ActionRefs: []string{},
		}
		if len(parts) < 3 {
			parts = append(parts, part)
			continue
		}
		last := &parts[len(parts)-1]
		last.TaskKeys = appendUniqueStrings(last.TaskKeys, task.TaskKey)
		last.EvidenceRefs = appendUniqueStrings(last.EvidenceRefs, evidenceRef)
		last.Content = strings.TrimSpace(last.Content + "\n" + content)
	}
	return parts
}

func safeDegradedValidationPlan(plan contracts.ReplyPlanV2, parts []contracts.ReplyPartV2) contracts.ReplyPlanV2 {
	covered := make(map[string]struct{})
	for _, part := range parts {
		for _, taskKey := range part.TaskKeys {
			covered[taskKey] = struct{}{}
		}
	}
	filtered := make([]contracts.ReplyPlanTaskV2, 0, len(covered))
	for _, task := range plan.Tasks {
		if _, ok := covered[task.TaskKey]; ok {
			filtered = append(filtered, task)
		}
	}
	plan.Tasks = filtered
	plan.ShouldGenerate = len(filtered) > 0
	if plan.GlobalConstraints.MaxReplyParts < len(parts) {
		plan.GlobalConstraints.MaxReplyParts = len(parts)
	}
	return plan
}

func safeRuntimeScalarFact(task contracts.ReplyPlanTaskV2, evidence contracts.EvidenceBundleV1) (string, string) {
	for _, item := range evidence.Items {
		if item.SourceType != "store_fact" || item.Answerability != "supporting" ||
			!stringInSlice(item.Ref, task.EvidenceRefs) || !stringInSlice(task.TaskKey, item.TaskKeys) ||
			strings.TrimSpace(item.Content) == "" {
			continue
		}
		content := strings.TrimSpace(item.Content)
		switch {
		case item.Title == authoritativeStoreNameEvidenceTitle && isStoreIdentitySubIntent(task.SubIntent):
			return "这里是" + strings.TrimRight(content, "。！!？?") + "。", item.Ref
		case item.Title == authoritativeStoreAddressEvidenceTitle && runtimeTaskUsesOnlyAuthoritativeStoreAddress(task.SubIntent):
			return "当前门店地址是：" + strings.TrimRight(content, "。！!？?") + "。", item.Ref
		case item.Title == authoritativeStorePhoneEvidenceTitle && isStorePhoneSubIntent(task.SubIntent):
			return "当前门店联系电话是：" + strings.TrimRight(content, "。！!？?") + "。", item.Ref
		}
	}
	return "", ""
}

func isStorePhoneSubIntent(subIntent string) bool {
	switch strings.TrimSpace(subIntent) {
	case "phone", "contact_phone", "store_phone":
		return true
	default:
		return false
	}
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

func recordRuntimeGenerationFailure(collector *callbacks.RuntimeTraceCollector, repair bool, err error) {
	if collector == nil || err == nil {
		return
	}
	code := runtimeGenerationFailureCode(collector, err)
	if repair {
		if collector.Data.Pipeline.Generate.RepairErrorCode == "" {
			collector.Data.Pipeline.Generate.RepairErrorCode = code
		}
		return
	}
	if collector.Data.Pipeline.Generate.InitialErrorCode == "" {
		collector.Data.Pipeline.Generate.InitialErrorCode = code
	}
}

func runtimeGenerationFailureCode(collector *callbacks.RuntimeTraceCollector, err error) string {
	var protocolErr *replyOutputProtocolError
	if errors.As(err, &protocolErr) && strings.TrimSpace(protocolErr.Reason) != "" {
		return strings.TrimSpace(protocolErr.Reason)
	}
	if code, ok := strictjson.CodeOf(err); ok {
		return code
	}
	if collector != nil {
		if reason := strings.TrimSpace(collector.Data.Pipeline.Validate.Reason); reason != "" && reason != "pending" {
			return boundedEvidenceText(reason, 160)
		}
	}
	if code, ok := svc.AIReplyExecutionErrorCodeOf(err); ok {
		return string(code)
	}
	return "generation_failed"
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
	setGenerationOutcome(summary, collector, GenerationOutcomeGenerationFailed)
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
	if err := applyCustomerVisibleBoundary(summary, collector); err != nil {
		return markRuntimeGenerationError(summary, collector, startedAt, err)
	}
	if !summary.Interrupted && len(summary.ReplyParts) == 0 && strings.TrimSpace(summary.ReplyText) == "" && !hasInvokedGraphTool(summary.InvokedToolCodes) {
		err := svc.NewAIReplyExecutionError(svc.AIReplyExecutionErrorEmptyOutput, fmt.Errorf("runtime produced no reply or action"))
		return markRuntimeGenerationError(summary, collector, startedAt, err)
	}
	safeDegraded := summary.GenerationOutcome == GenerationOutcomeSafeDegraded
	if summary.Status == "started" {
		summary.Status = "completed"
	}
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = summary.ReplyText
	if safeDegraded {
		setGenerationOutcome(summary, collector, GenerationOutcomeSafeDegraded)
		collector.Data.Output.FinishReason = string(GenerationOutcomeSafeDegraded)
		collector.Data.Pipeline.Generate.Status = string(GenerationOutcomeSafeDegraded)
		collector.Data.Pipeline.Generate.Mode = string(GenerationOutcomeSafeDegraded)
		if strings.TrimSpace(collector.Data.Error.Message) == "" {
			collector.Data.Error.Message = firstNonEmpty(
				collector.Data.Pipeline.Generate.RepairErrorCode,
				collector.Data.Pipeline.Generate.InitialErrorCode,
				"generation_failed",
			)
			collector.Data.Error.Stage = "generate_safe_degraded"
		}
		if strings.TrimSpace(collector.Data.Pipeline.Generate.Reason) == "" {
			collector.Data.Pipeline.Generate.Reason = "generation failed; only authoritative scalar facts were allowed through safe degraded mode"
		}
		return nil
	}
	collector.Data.Output.FinishReason = summary.Status
	collector.Data.Pipeline.Generate.Status = summary.Status
	if collector.Data.Pipeline.Generate.InitialErrorCode != "" {
		setGenerationOutcome(summary, collector, GenerationOutcomeRepaired)
		collector.Data.Pipeline.Generate.Mode = "repaired"
		collector.Data.Pipeline.Generate.Reason = "reply_output.v2 repaired and validated"
	} else {
		setGenerationOutcome(summary, collector, GenerationOutcomeGenerated)
		collector.Data.Pipeline.Generate.Mode = "generated"
		collector.Data.Pipeline.Generate.Reason = "reply_output.v2 generated and validated"
	}
	if strings.TrimSpace(collector.Data.Pipeline.Validate.Status) == "" {
		collector.Data.Pipeline.Validate.Status = "passed"
		collector.Data.Pipeline.Validate.Reason = "reply_output.v2 passed deterministic validation"
	}
	return nil
}

// extractLooseJSONObject 从夹杂说明文字/思考前缀的输出中提取 JSON 对象主体。
func extractLooseJSONObject(raw string) string {
	trimmed := strings.TrimSpace(raw)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return ""
	}
	return trimmed[start : end+1]
}
