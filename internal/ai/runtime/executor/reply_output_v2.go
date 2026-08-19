package executor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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

type runtimeTaskValidationFailure struct {
	Reason string
}

func (e *runtimeTaskValidationFailure) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return "reply_output.v2 task validation failed"
	}
	return "reply_output.v2 task validation failed: " + strings.TrimSpace(e.Reason)
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
	hadTaskRepairState := len(summary.replyRepairState.PreservedParts) > 0 || len(summary.replyRepairState.PendingTaskKeys) > 0
	if len(summary.replyRepairState.PreservedParts) > 0 {
		parsed.Parts = mergeRuntimeReplyParts(summary.replyRepairState.PreservedParts, parsed.Parts, summary.ReplyPlanV2)
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
		summary.replyRepairState = runtimeReplyRepairState{}
		return nil
	case "repairable_protocol_error":
		preserveRuntimeValidReplyParts(summary, parsed, req, true)
		if summary.ValidationResult != nil && summary.ValidationResult.Status == "passed" && len(summary.ReplyParts) > 0 {
			if collector != nil {
				collector.Data.Pipeline.Validate.Status = "passed"
				collector.Data.Pipeline.Validate.Reason = "valid task answers preserved after dropping invalid extra output"
			}
			return nil
		}
		if hadTaskRepairState {
			cause := &runtimeTaskValidationFailure{Reason: validationResultReason(validation)}
			if applySafeRuntimeDegraded(summary, collector, req, cause) {
				return nil
			}
		}
		return &replyOutputProtocolError{RawResponse: raw, Reason: validationResultReason(validation)}
	default:
		preserveRuntimeValidReplyParts(summary, parsed, req, false)
		cause := &runtimeTaskValidationFailure{Reason: validationResultReason(validation)}
		if applySafeRuntimeDegraded(summary, collector, req, cause) {
			return nil
		}
		return fmt.Errorf("reply_output.v2 rejected: %s", validationResultReason(validation))
	}
}

func preserveRuntimeValidReplyParts(summary *RunResult, output contracts.ReplyOutputV2, req RunInput, allowComplete bool) bool {
	if summary == nil || summary.ReplyPlanV2 == nil || summary.EvidenceBundle == nil || summary.ActionLedgerV2 == nil {
		return false
	}
	plan := *summary.ReplyPlanV2
	expected := runtimeTextTaskKeys(plan)
	planByTask := make(map[string]contracts.ReplyPlanTaskV2, len(plan.Tasks))
	for _, task := range plan.Tasks {
		planByTask[task.TaskKey] = task
	}
	preserved := make([]contracts.ReplyPartV2, 0, len(output.Parts))
	covered := make(map[string]struct{}, len(expected))
	for _, original := range normalizeReplyParts(output.Parts, &plan) {
		part := original
		part.TaskKeys = runtimeUncoveredTaskKeys(part.TaskKeys, expected, covered)
		if len(part.TaskKeys) == 0 {
			continue
		}
		if normalized, ok := validateRuntimeReplyPart(summary, req, plan, part); ok {
			preserved = append(preserved, normalized)
			markRuntimeTaskKeysCovered(covered, normalized.TaskKeys)
			continue
		}
		for _, candidate := range isolateRuntimeReplyPartByTask(part, planByTask) {
			taskKey := candidate.TaskKeys[0]
			if _, exists := covered[taskKey]; exists {
				continue
			}
			normalized, ok := validateRuntimeReplyPart(summary, req, plan, candidate)
			if !ok {
				continue
			}
			preserved = append(preserved, normalized)
			markRuntimeTaskKeysCovered(covered, normalized.TaskKeys)
		}
	}
	pending := make([]string, 0, len(expected)-len(covered))
	for _, taskKey := range expected {
		if _, ok := covered[taskKey]; !ok {
			pending = append(pending, taskKey)
		}
	}
	summary.replyRepairState = runtimeReplyRepairState{
		PreservedParts: append([]contracts.ReplyPartV2(nil), preserved...), PendingTaskKeys: append([]string(nil), pending...),
	}
	if len(preserved) == 0 {
		return len(pending) > 0
	}
	if allowComplete && len(pending) == 0 {
		compacted := mergeRuntimeReplyParts(nil, preserved, &plan)
		result := NewReplyValidatorForMode(summary.RuntimeValidatorMode).Validate(ReplyValidationInput{
			Req: req, Output: contracts.ReplyOutputV2{SchemaVersion: contracts.ReplyOutputV2SchemaVersion, Parts: compacted},
			Plan: plan, Evidence: *summary.EvidenceBundle, ActionLedger: *summary.ActionLedgerV2, Gates: summary.ValidationGates,
			ServerValidatedTaskBindings: true,
		})
		if result.Status == "passed" {
			summary.ValidationResult = &result
			summary.ReplyParts = append([]contracts.ReplyPartV2(nil), result.NormalizedParts...)
			summary.ReplyText = joinValidatedReplyParts(result.NormalizedParts)
			summary.replyRepairState = runtimeReplyRepairState{}
			return false
		}
	}
	return len(pending) > 0
}

func validateRuntimeReplyPart(summary *RunResult, req RunInput, plan contracts.ReplyPlanV2, part contracts.ReplyPartV2) (contracts.ReplyPartV2, bool) {
	partPlan := runtimeReplyPlanForTaskKeys(plan, part.TaskKeys)
	result := NewReplyValidatorForMode(summary.RuntimeValidatorMode).Validate(ReplyValidationInput{
		Req: req, Output: contracts.ReplyOutputV2{SchemaVersion: contracts.ReplyOutputV2SchemaVersion, Parts: []contracts.ReplyPartV2{part}},
		Plan: partPlan, Evidence: *summary.EvidenceBundle, ActionLedger: *summary.ActionLedgerV2, Gates: summary.ValidationGates,
	})
	if result.Status != "passed" || len(result.NormalizedParts) != 1 {
		return contracts.ReplyPartV2{}, false
	}
	return result.NormalizedParts[0], true
}

func runtimeUncoveredTaskKeys(taskKeys, expected []string, covered map[string]struct{}) []string {
	ret := make([]string, 0, len(taskKeys))
	for _, taskKey := range taskKeys {
		if !stringInSlice(taskKey, expected) {
			continue
		}
		if _, exists := covered[taskKey]; exists {
			continue
		}
		ret = append(ret, taskKey)
	}
	return ret
}

func markRuntimeTaskKeysCovered(covered map[string]struct{}, taskKeys []string) {
	for _, taskKey := range taskKeys {
		covered[taskKey] = struct{}{}
	}
}

func isolateRuntimeReplyPartByTask(part contracts.ReplyPartV2, planByTask map[string]contracts.ReplyPlanTaskV2) []contracts.ReplyPartV2 {
	units := splitReplyAnswerUnits(part.Content)
	if len(units) == 0 {
		return nil
	}
	tasks := make([]contracts.ReplyPlanTaskV2, 0, len(part.TaskKeys))
	for _, taskKey := range part.TaskKeys {
		if task, ok := planByTask[taskKey]; ok {
			tasks = append(tasks, task)
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Sequence < tasks[j].Sequence })
	usedImplicit := make([]bool, len(units))
	ret := make([]contracts.ReplyPartV2, 0, len(tasks))
	for _, task := range tasks {
		selected := make([]string, 0, 2)
		for _, unit := range units {
			if replyAnswerUnitExplicitlyNamesTask(unit, task) {
				selected = append(selected, unit)
			}
		}
		if len(selected) == 0 {
			for index, unit := range units {
				if usedImplicit[index] || !replyAnswerUnitImplicitlySupportsTask(unit, task) {
					continue
				}
				usedImplicit[index] = true
				selected = append(selected, unit)
				break
			}
		}
		content := joinRuntimeReplyAnswerUnits(selected)
		if content == "" {
			continue
		}
		ret = append(ret, contracts.ReplyPartV2{
			TaskKeys: []string{task.TaskKey}, Content: content,
			EvidenceRefs: intersectRuntimeReplyRefs(part.EvidenceRefs, task.EvidenceRefs),
			ActionRefs:   intersectRuntimeReplyRefs(part.ActionRefs, task.ActionRefs),
		})
	}
	return ret
}

func joinRuntimeReplyAnswerUnits(units []string) string {
	cleaned := make([]string, 0, len(units))
	for _, unit := range units {
		if unit = strings.TrimSpace(unit); unit != "" {
			cleaned = append(cleaned, strings.TrimRight(unit, "。！!？?；;，,"))
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	return strings.Join(cleaned, "；") + "。"
}

func intersectRuntimeReplyRefs(values, allowed []string) []string {
	ret := make([]string, 0, len(values))
	for _, value := range values {
		if stringInSlice(value, allowed) {
			ret = append(ret, value)
		}
	}
	return uniqueTrimmedStrings(ret)
}

func runtimeTextTaskKeys(plan contracts.ReplyPlanV2) []string {
	ret := make([]string, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if task.OutputMode == "text" || task.OutputMode == "text_and_resource" || task.OutputMode == "clarification" {
			ret = append(ret, task.TaskKey)
		}
	}
	return ret
}

func runtimePartHasUnknownOrCoveredTask(part contracts.ReplyPartV2, expected []string, covered map[string]struct{}) bool {
	for _, taskKey := range part.TaskKeys {
		if !stringInSlice(taskKey, expected) {
			return true
		}
		if _, exists := covered[taskKey]; exists {
			return true
		}
	}
	return false
}

func runtimeReplyPlanForTaskKeys(plan contracts.ReplyPlanV2, taskKeys []string) contracts.ReplyPlanV2 {
	selected := make(map[string]struct{}, len(taskKeys))
	for _, taskKey := range taskKeys {
		selected[strings.TrimSpace(taskKey)] = struct{}{}
	}
	tasks := make([]contracts.ReplyPlanTaskV2, 0, len(selected))
	for _, task := range plan.Tasks {
		if _, ok := selected[task.TaskKey]; ok {
			tasks = append(tasks, task)
		}
	}
	plan.Tasks = tasks
	plan.ShouldGenerate = len(tasks) > 0
	return plan
}

func mergeRuntimeReplyParts(preserved, generated []contracts.ReplyPartV2, plan *contracts.ReplyPlanV2) []contracts.ReplyPartV2 {
	parts := append(append([]contracts.ReplyPartV2(nil), preserved...), generated...)
	parts = normalizeReplyParts(parts, plan)
	if plan == nil || len(parts) <= plan.GlobalConstraints.MaxReplyParts || plan.GlobalConstraints.MaxReplyParts <= 0 {
		return parts
	}
	maxQuestions := plan.GlobalConstraints.MaxQuestionsPerPart
	if maxQuestions <= 0 {
		maxQuestions = 4
	}
	packed := make([]contracts.ReplyPartV2, 0, plan.GlobalConstraints.MaxReplyParts)
	for _, part := range parts {
		if len(packed) == 0 || len(packed[len(packed)-1].TaskKeys)+len(part.TaskKeys) > maxQuestions {
			packed = append(packed, part)
			continue
		}
		last := &packed[len(packed)-1]
		last.TaskKeys = appendUniqueStrings(last.TaskKeys, part.TaskKeys...)
		last.EvidenceRefs = appendUniqueStrings(last.EvidenceRefs, part.EvidenceRefs...)
		last.ActionRefs = appendUniqueStrings(last.ActionRefs, part.ActionRefs...)
		last.Content = strings.TrimSpace(last.Content + "\n" + part.Content)
	}
	return packed
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
	if len(summary.replyRepairState.PendingTaskKeys) > 0 {
		pending := append([]string(nil), summary.replyRepairState.PendingTaskKeys...)
		plan := runtimeReplyPlanForTaskKeys(*summary.ReplyPlanV2, pending)
		evidence := runtimeEvidenceForTaskKeys(*summary.EvidenceBundle, pending)
		input.ReplyPlan = &plan
		input.Evidence = &evidence
		input.PreparedActions = runtimePreparedActionsForTaskKeys(input.PreparedActions, pending)
		input.RepairInstruction += "\n已通过校验的其他答案由服务器保留，不得重复。只输出这些仍需修复的 taskKeys：" + strings.Join(pending, ",")
	}
	compiled, err := contextcompiler.New(nil).Compile(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(summary.replyRepairState.PendingTaskKeys) == 0 && compiled.Fingerprint != summary.CompiledContext.Fingerprint {
		return nil, fmt.Errorf("reply_output.v2 repair context fingerprint changed")
	}
	return compiled.Messages, nil
}

func runtimeEvidenceForTaskKeys(evidence contracts.EvidenceBundleV1, taskKeys []string) contracts.EvidenceBundleV1 {
	items := make([]contracts.EvidenceItemV1, 0, len(evidence.Items))
	for _, item := range evidence.Items {
		if stringsIntersect(item.TaskKeys, taskKeys) {
			items = append(items, item)
		}
	}
	evidence.Items = items
	return evidence
}

func runtimePreparedActionsForTaskKeys(actions []contracts.ActionLedgerItemV1, taskKeys []string) []contracts.ActionLedgerItemV1 {
	ret := make([]contracts.ActionLedgerItemV1, 0, len(actions))
	for _, action := range actions {
		if stringInSlice(action.TaskKey, taskKeys) {
			ret = append(ret, action)
		}
	}
	return ret
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

func isRuntimeTaskFailureNotice(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "关于") &&
		(strings.HasSuffix(content, "，当前资料没写明，不能乱答。") ||
			strings.HasSuffix(content, "，当前没能确认，不能乱答。") ||
			strings.HasSuffix(content, "，当前无法确认，不能乱答。"))
}

func runtimeTaskFailureNotice(task contracts.ReplyPlanTaskV2) string {
	return "关于" + runtimeTaskFailureLabel(task) + "，当前无法确认，不能乱答。"
}

func runtimeTaskFailureLabel(task contracts.ReplyPlanTaskV2) string {
	switch replyTaskTopicClass(task) {
	case "checkin":
		return "入住"
	case "checkout":
		return "退房"
	case "breakfast":
		return "早餐"
	case "address":
		return "门店地址"
	case "parking":
		return "停车"
	case "coffee":
		return "咖啡"
	case "invoice":
		return "发票"
	case "wifi":
		return "WiFi"
	case "laundry":
		return "洗衣"
	case "luggage":
		return "行李寄存"
	case "takeaway":
		return "外卖"
	case "room_change":
		return "换房"
	}
	switch strings.TrimSpace(task.SubIntent) {
	case "discount", "promotion":
		return "优惠"
	case "store_phone", "phone":
		return "门店电话"
	case "store_name", "identity":
		return "门店名称"
	}
	objective := strings.Trim(strings.TrimSpace(task.Objective), "，,。.!！?？:：;；")
	if objective != "" && utf8.RuneCountInString(objective) <= 24 && validCustomerVisibleText(objective) {
		return objective
	}
	return "您刚才问的这项"
}

func buildRuntimeTaskFailureNoticeParts(plan contracts.ReplyPlanV2) []contracts.ReplyPartV2 {
	parts := make([]contracts.ReplyPartV2, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if task.OutputMode != "text" && task.OutputMode != "text_and_resource" && task.OutputMode != "clarification" {
			continue
		}
		parts = append(parts, contracts.ReplyPartV2{
			TaskKeys: []string{task.TaskKey}, Content: runtimeTaskFailureNotice(task),
			EvidenceRefs: []string{}, ActionRefs: []string{},
		})
	}
	return parts
}

func runtimeTaskFailureNoticeAllowed(cause error) bool {
	var validationFailure *runtimeTaskValidationFailure
	if errors.As(cause, &validationFailure) {
		return true
	}
	var protocolFailure *replyOutputProtocolError
	return errors.As(cause, &protocolFailure)
}

// applySafeRuntimeDegraded may only expose server-owned scalar facts. It is not
// a second answer engine and must never render FastGPT text, process templates,
// cached answers, generic acknowledgements, or no-hit business replies.
func applySafeRuntimeDegraded(summary *RunResult, collector *callbacks.RuntimeTraceCollector, req RunInput, cause error) bool {
	if summary == nil || summary.ReplyPlanV2 == nil || summary.EvidenceBundle == nil || summary.ActionLedgerV2 == nil {
		return false
	}
	parts := append([]contracts.ReplyPartV2(nil), summary.replyRepairState.PreservedParts...)
	covered := make([]string, 0)
	for _, part := range parts {
		covered = appendUniqueStrings(covered, part.TaskKeys...)
	}
	remainingPlan := runtimeReplyPlanWithoutTaskKeys(*summary.ReplyPlanV2, covered)
	parts = mergeRuntimeReplyParts(parts, buildSafeRuntimeDegradedParts(remainingPlan, *summary.EvidenceBundle), summary.ReplyPlanV2)

	covered = covered[:0]
	for _, part := range parts {
		covered = appendUniqueStrings(covered, part.TaskKeys...)
	}
	usedTaskFailureNotice := false
	if runtimeTaskFailureNoticeAllowed(cause) {
		pendingPlan := runtimeReplyPlanWithoutTaskKeys(*summary.ReplyPlanV2, covered)
		failureParts := buildRuntimeTaskFailureNoticeParts(pendingPlan)
		if len(failureParts) > 0 {
			parts = mergeRuntimeReplyParts(parts, failureParts, summary.ReplyPlanV2)
			usedTaskFailureNotice = true
		}
	}
	if len(parts) == 0 {
		return false
	}
	validationPlan := safeDegradedValidationPlan(*summary.ReplyPlanV2, parts)
	if usedTaskFailureNotice {
		validationPlan = *summary.ReplyPlanV2
	}
	validation := NewReplyValidatorForMode(summary.RuntimeValidatorMode).Validate(ReplyValidationInput{
		Req: req, Output: contracts.ReplyOutputV2{SchemaVersion: contracts.ReplyOutputV2SchemaVersion, Parts: parts},
		Plan: validationPlan, Evidence: *summary.EvidenceBundle, ActionLedger: *summary.ActionLedgerV2,
		Gates: summary.ValidationGates, ServerValidatedTaskBindings: true,
	})
	if validation.Status != "passed" {
		return false
	}
	summary.ValidationResult = &validation
	summary.ReplyParts = append([]contracts.ReplyPartV2(nil), validation.NormalizedParts...)
	summary.ReplyText = joinValidatedReplyParts(validation.NormalizedParts)
	summary.replyRepairState = runtimeReplyRepairState{}
	summary.Status = string(GenerationOutcomeSafeDegraded)
	summary.ErrorMessage = ""
	setGenerationOutcome(summary, collector, GenerationOutcomeSafeDegraded)
	collector.Data.Status = summary.Status
	collector.Data.Error.Message = runtimeGenerationFailureCode(collector, cause)
	collector.Data.Error.Stage = "generate_safe_degraded"
	collector.Data.Pipeline.Generate.Status = string(GenerationOutcomeSafeDegraded)
	collector.Data.Pipeline.Generate.Mode = string(GenerationOutcomeSafeDegraded)
	if usedTaskFailureNotice {
		collector.Data.Pipeline.Generate.Reason = "invalid task answers were isolated; valid answers were preserved and remaining tasks received deterministic safe results"
		collector.Data.Pipeline.Validate.Reason = "task-isolated reply and deterministic safe results passed final validation"
	} else {
		collector.Data.Pipeline.Generate.Reason = "generation failed; only authoritative scalar facts were allowed through safe degraded mode"
		collector.Data.Pipeline.Validate.Reason = "safe degraded scalar facts passed deterministic validation"
	}
	collector.Data.Pipeline.Validate.Status = "passed"
	return strings.TrimSpace(summary.ReplyText) != ""
}

func runtimeReplyPlanWithoutTaskKeys(plan contracts.ReplyPlanV2, excluded []string) contracts.ReplyPlanV2 {
	tasks := make([]contracts.ReplyPlanTaskV2, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if !stringInSlice(task.TaskKey, excluded) {
			tasks = append(tasks, task)
		}
	}
	plan.Tasks = tasks
	plan.ShouldGenerate = len(tasks) > 0
	return plan
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
