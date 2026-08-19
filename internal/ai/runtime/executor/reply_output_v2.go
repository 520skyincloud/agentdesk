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
	if applyControlledRuntimeReplyFallback(summary, collector, req, err) {
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
	var parsed contracts.ReplyOutputV2
	var err error
	if multimodalV3Enabled() {
		// 契约 22.14/15：成组开关下模型只输出 groupKey+taskKeys+content；
		// Evidence/Action 引用由服务端 deterministic autofix 派生。
		// 模型仍按 v2 形态输出时兼容解析（引用一律由服务端覆盖派生），
		// 避免协议切换期回复中断。
		parsed, err = parseRuntimeReplyOutputV3AsV2(raw)
		if err != nil {
			parsed, err = parseRuntimeReplyOutputV2(raw)
		}
	} else {
		parsed, err = parseRuntimeReplyOutputV2(raw)
	}
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

// applyControlledRuntimeReplyFallback 是 Generate 本身和唯一一次协议修复都失败后的
// 最后发送边界。它只使用已经通过任务相关性裁决的 Evidence/Store Fact，不重新检索、
// 不调用第二个模型，也不把知识全文倾倒给客户。取消中的旧 Job 不会进入这里。
func applyControlledRuntimeReplyFallback(summary *RunResult, collector *callbacks.RuntimeTraceCollector, req RunInput, cause error) bool {
	if summary == nil || summary.ReplyPlanV2 == nil || summary.EvidenceBundle == nil || summary.ActionLedgerV2 == nil {
		return false
	}
	parts := buildControlledRuntimeFallbackParts(*summary.ReplyPlanV2, *summary.EvidenceBundle)
	if len(parts) == 0 {
		return false
	}
	validation := NewReplyValidatorForMode(summary.RuntimeValidatorMode).Validate(ReplyValidationInput{
		Req: req, Output: contracts.ReplyOutputV2{SchemaVersion: contracts.ReplyOutputV2SchemaVersion, Parts: parts},
		Plan: *summary.ReplyPlanV2, Evidence: *summary.EvidenceBundle, ActionLedger: *summary.ActionLedgerV2,
		Gates: summary.ValidationGates,
	})
	if validation.Status != "passed" {
		return false
	}
	summary.ValidationResult = &validation
	summary.ReplyParts = append([]contracts.ReplyPartV2(nil), validation.NormalizedParts...)
	summary.ReplyText = joinValidatedReplyParts(validation.NormalizedParts)
	summary.Status = "fallback"
	summary.ErrorMessage = ""
	collector.Data.Status = "fallback"
	collector.Data.Error.Message = runtimeGenerationFailureCode(collector, cause)
	collector.Data.Error.Stage = "generate_fallback"
	collector.Data.Pipeline.Generate.Status = "fallback"
	collector.Data.Pipeline.Generate.Mode = "controlled_fallback"
	collector.Data.Pipeline.Generate.Reason = "controlled evidence fallback after generate failure"
	collector.Data.Pipeline.Validate.Status = "passed"
	collector.Data.Pipeline.Validate.Reason = "controlled fallback passed deterministic validation"
	_ = cause
	return strings.TrimSpace(summary.ReplyText) != ""
}

func buildControlledRuntimeFallbackParts(plan contracts.ReplyPlanV2, evidence contracts.EvidenceBundleV1) []contracts.ReplyPartV2 {
	parts := make([]contracts.ReplyPartV2, 0, min(plan.GlobalConstraints.MaxReplyParts, 3))
	for _, task := range plan.Tasks {
		if task.OutputMode != "text" && task.OutputMode != "text_and_resource" && task.OutputMode != "clarification" {
			continue
		}
		content := controlledRuntimeTaskFallbackText(task, evidence)
		if content == "" {
			continue
		}
		part := contracts.ReplyPartV2{
			TaskKeys: []string{task.TaskKey}, Content: content,
			EvidenceRefs: append([]string(nil), task.EvidenceRefs...), ActionRefs: append([]string(nil), task.ActionRefs...),
		}
		if len(parts) < 3 {
			parts = append(parts, part)
			continue
		}
		last := &parts[len(parts)-1]
		last.TaskKeys = appendUniqueStrings(last.TaskKeys, task.TaskKey)
		last.EvidenceRefs = appendUniqueStrings(last.EvidenceRefs, task.EvidenceRefs...)
		last.ActionRefs = appendUniqueStrings(last.ActionRefs, task.ActionRefs...)
		last.Content = strings.TrimSpace(last.Content + "\n" + content)
	}
	return parts
}

func controlledRuntimeTaskFallbackText(task contracts.ReplyPlanTaskV2, evidence contracts.EvidenceBundleV1) string {
	if runtimeFallbackNeedsProcessCoverage(task) {
		if content := controlledRuntimeProcessFallbackText(task, evidence); content != "" {
			return content
		}
	}
	for _, item := range evidence.Items {
		if !stringInSlice(item.Ref, task.EvidenceRefs) || strings.TrimSpace(item.Content) == "" {
			continue
		}
		content := strings.TrimSpace(item.Content)
		if item.SourceType == "store_fact" {
			switch {
			case taskRequestsStoreAddress(task.SubIntent, task.Objective):
				return "外卖或收货地址请填写：" + strings.TrimRight(content, "。！!？?") + "。"
			case isStoreIdentitySubIntent(task.SubIntent):
				return "这里是" + strings.TrimRight(content, "。！!？?") + "。"
			}
		}
		if snippet := conciseRuntimeEvidenceSnippet(content, task); snippet != "" {
			return snippet
		}
	}
	if task.Knowledge.Status == "unavailable" {
		return "这项门店信息我暂时没查准，为避免说错，您可以稍后再问我一次。"
	}
	if task.Knowledge.Policy == "required" || task.OutputMode == "clarification" {
		return "当前门店资料里暂时没有写明这项信息，我先不乱回答。"
	}
	if task.Intent == "interaction" {
		return "收到。"
	}
	return "收到，您可以再具体说一下需要了解什么。"
}

func controlledRuntimeProcessFallbackText(task contracts.ReplyPlanTaskV2, evidence contracts.EvidenceBundleV1) string {
	items := make([]contracts.EvidenceItemV1, 0, len(task.EvidenceRefs))
	hasAuthoritativeCheckinFact := false
	for _, sourceType := range []string{"store_fact", "fastgpt"} {
		for _, item := range evidence.Items {
			if item.SourceType != sourceType || !stringInSlice(item.Ref, task.EvidenceRefs) || strings.TrimSpace(item.Content) == "" {
				continue
			}
			items = append(items, item)
			if item.SourceType == "store_fact" {
				facts := runtimeProcessFactMask(item.Content)
				if facts&runtimeProcessFactRegistration != 0 && facts&runtimeProcessFactAccess != 0 {
					hasAuthoritativeCheckinFact = true
				}
			}
		}
	}
	segments := make([]string, 0, 6)
	seen := make(map[string]struct{})
	coveredProcessFacts := uint8(0)
	for _, item := range items {
		content := cleanRuntimeEvidenceAnswer(item.Content)
		for _, segment := range splitRuntimeEvidenceSegments(content) {
			key := compactRuntimeProtocolText(segment)
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			facts := runtimeProcessFactMask(segment)
			if isCheckinProcessSubIntent(task.SubIntent) && facts == runtimeProcessFactRoute && !runtimeTaskRequestsRoute(task) {
				continue
			}
			if facts != 0 && facts&^coveredProcessFacts == 0 {
				continue
			}
			seen[key] = struct{}{}
			coveredProcessFacts |= facts
			segments = append(segments, segment)
			if len(segments) >= 6 {
				break
			}
		}
		if len(segments) >= 6 {
			break
		}
	}
	if len(segments) == 0 {
		return ""
	}
	if isCheckinProcessSubIntent(task.SubIntent) && hasAuthoritativeCheckinFact && coveredProcessFacts&runtimeProcessFactRegistration != 0 && coveredProcessFacts&runtimeProcessFactAccess != 0 {
		ret := "我们这边是无人值守自助入住，没有传统前台和房卡。请先在下面的入住小程序按提示完成入住登记，登记成功后到店直接刷脸开门，不需要密码。"
		if runtimeTaskRequestsRoute(task) {
			for _, segment := range segments {
				if runtimeProcessFactMask(segment)&runtimeProcessFactRoute != 0 {
					ret += segment + "。"
					break
				}
			}
		}
		return boundedEvidenceText(ret, 600)
	}
	return boundedEvidenceText(strings.Join(segments, "。")+"。", 600)
}

func cleanRuntimeEvidenceAnswer(content string) string {
	content = strings.TrimSpace(strings.NewReplacer("\r", "\n", "\t", " ").Replace(content))
	if index := strings.LastIndex(content, "答案："); index >= 0 {
		content = strings.TrimSpace(content[index+len("答案："):])
	} else if index := strings.LastIndex(content, "答案:"); index >= 0 {
		content = strings.TrimSpace(content[index+len("答案:"):])
	}
	return strings.TrimLeft(content, "-#*• ")
}

func splitRuntimeEvidenceSegments(content string) []string {
	raw := strings.FieldsFunc(content, func(r rune) bool {
		return r == '。' || r == '！' || r == '!' || r == '\n'
	})
	ret := make([]string, 0, len(raw))
	for _, segment := range raw {
		segment = strings.TrimSpace(strings.TrimLeft(segment, "-#*•0123456789.、 "))
		if segment == "" || strings.HasSuffix(segment, "？") || strings.HasSuffix(segment, "?") {
			continue
		}
		ret = append(ret, segment)
	}
	return ret
}

func runtimeProcessFactMask(text string) uint8 {
	compact := compactRuntimeProtocolText(text)
	var mask uint8
	if containsAny(compact, []string{"小程序", "登记", "实名", "证件", "订单", "入住信息"}) {
		mask |= runtimeProcessFactRegistration
	}
	if containsAny(compact, []string{"刷脸", "开门", "门禁", "房卡", "密码"}) {
		mask |= runtimeProcessFactAccess
	}
	if containsAny(compact, []string{"入口", "大楼", "大厅", "电梯", "楼层", "停车场"}) {
		mask |= runtimeProcessFactRoute
	}
	return mask
}

const (
	runtimeProcessFactRegistration uint8 = 1 << iota
	runtimeProcessFactAccess
	runtimeProcessFactRoute
)

func runtimeTaskRequestsRoute(task contracts.ReplyPlanTaskV2) bool {
	subIntent := strings.ToLower(strings.TrimSpace(task.SubIntent))
	if containsAny(subIntent, []string{"entrance", "navigation", "route", "address", "location"}) {
		return true
	}
	return runtimeTextRequestsEntranceRoute(task.Objective)
}

func conciseRuntimeEvidenceSnippet(content string, task contracts.ReplyPlanTaskV2) string {
	content = cleanRuntimeEvidenceAnswer(content)
	if content == "" {
		return ""
	}
	segments := splitRuntimeEvidenceSegments(content)
	maxSegments := 2
	maxRunes := 260
	if runtimeFallbackNeedsProcessCoverage(task) {
		maxSegments = 6
		maxRunes = 600
	}
	selected := make([]string, 0, maxSegments)
	for _, segment := range segments {
		selected = append(selected, segment)
		if len(selected) == maxSegments {
			break
		}
	}
	if len(selected) == 0 {
		return ""
	}
	ret := strings.Join(selected, "。") + "。"
	return boundedEvidenceText(ret, maxRunes)
}

func runtimeFallbackNeedsProcessCoverage(task contracts.ReplyPlanTaskV2) bool {
	subIntent := strings.ToLower(strings.TrimSpace(task.SubIntent))
	return strings.Contains(subIntent, "process") || strings.Contains(subIntent, "steps") ||
		strings.Contains(subIntent, "guide") || isCheckinProcessSubIntent(subIntent) ||
		subIntent == "checkout" || subIntent == "check_out"
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
	fallback := summary.Status == "fallback" || collector.Data.Pipeline.Generate.Mode == "controlled_fallback"
	if summary.Status == "started" || fallback {
		summary.Status = "completed"
	}
	collector.Data.Status = summary.Status
	collector.Data.Output.ReplyText = summary.ReplyText
	if fallback {
		collector.Data.Output.FinishReason = "controlled_fallback"
		collector.Data.Pipeline.Generate.Status = "fallback"
		collector.Data.Pipeline.Generate.Mode = "controlled_fallback"
		if strings.TrimSpace(collector.Data.Error.Message) == "" {
			collector.Data.Error.Message = firstNonEmpty(
				collector.Data.Pipeline.Generate.RepairErrorCode,
				collector.Data.Pipeline.Generate.InitialErrorCode,
				"generation_failed",
			)
			collector.Data.Error.Stage = "generate_fallback"
		}
		if strings.TrimSpace(collector.Data.Pipeline.Generate.Reason) == "" {
			collector.Data.Pipeline.Generate.Reason = "controlled evidence fallback after generate failure"
		}
		return nil
	}
	collector.Data.Output.FinishReason = summary.Status
	collector.Data.Pipeline.Generate.Status = summary.Status
	if collector.Data.Pipeline.Generate.InitialErrorCode != "" {
		collector.Data.Pipeline.Generate.Mode = "repaired"
		collector.Data.Pipeline.Generate.Reason = "reply_output.v2 repaired and validated"
	} else {
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
