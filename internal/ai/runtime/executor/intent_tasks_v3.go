package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/channelbreaker"
	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/strictjson"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/services"
	"github.com/cloudwego/eino/schema"
)

// 多模态契约 9/10/12：IntentTasksV3 主链接线（成组灰度 AI_RUNTIME_MULTIMODAL_V3=on）。
// Intent 收到 TurnInputEnvelope，输出 intent_tasks.v3（来源引用 + rune span +
// utteranceCoverage）；服务端校验 span、做集合等式覆盖校验，再经 QuestionUnit
// Normalize 收敛（同源去重 / degraded_single_task），最后适配到既有 V2 下游。

// intentTasksV3Wire 是 intent_tasks.v3 的传输形态。
type intentTasksV3Wire struct {
	SchemaVersion     string                   `json:"schemaVersion"`
	DialogueAct       string                   `json:"dialogueAct"`
	UtteranceCoverage []intentCoverageItemWire `json:"utteranceCoverage"`
	Tasks             []intentTaskV3Wire       `json:"tasks"`
}

type intentCoverageItemWire struct {
	SourceRef     string `json:"sourceRef"`
	Status        string `json:"status"`
	TaskSequences []int  `json:"taskSequences"`
	IgnoredReason string `json:"ignoredReason"`
}

type intentTaskV3Wire struct {
	Sequence     int               `json:"sequence"`
	Intent       string            `json:"intent"`
	SubIntent    string            `json:"subIntent"`
	SourceRefs   []string          `json:"sourceRefs"`
	SourceSpans  []intentSpanWire  `json:"sourceSpans"`
	DependsOnObs []string          `json:"dependsOnObservationRefs"`
	Normalized   string            `json:"normalizedQuestion"`
	Requirements []requirementWire `json:"answerRequirements"`
	RequestMode  string            `json:"requestMode"`
	Confidence   float64           `json:"confidence"`
}

type requirementWire struct {
	Sequence int    `json:"sequence"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
}

type intentSpanWire struct {
	SourceRef string `json:"sourceRef"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Quote     string `json:"quote"`
}

// intentV3SystemPrompt 契约 9.3 的 Intent System Prompt。
const intentV3SystemPrompt = `你是酒店无人化客服系统的 IntentDetect 阶段，不负责回复客户。

你收到的是 turn_input_envelope.v1：
- utterances 是当前 Turn 的客户输入；
- observations 是媒体观察，不是门店事实；
- unresolvedTasks 只用于判断重复、补充、纠正或取消。

输出 intent_tasks.v3。

强制规则：
1. 先为每个非空 utterance 输出一条 utteranceCoverage；不得遗漏 URef。
2. covered 必须列出 Task sequence；ignored 必须使用允许的稳定 reasonCode。
3. 每个 Task 必须引用真实 sourceRef。
4. sourceSpan.quote 必须是对应 utterance.text 的连续原文片段。
5. 多问题必须使用各自原文片段；禁止把完整原文复制到多个 Task。
6. 图片、附件和历史内容默认是 Observation，不自动创建业务 Task。
7. 当前文字明确引用图片时，在 dependsOnObservationRefs 中引用对应 Observation。
8. 语音 transcript 属于当前客户输入，可拆多个问题，但每题必须绑定自己的 Span。
9. 不得输出 taskKey、needsKnowledge、needsResource、needsHumanRoute、action 或执行结果。
10. 只输出严格 JSON Object，不输出 Markdown 或解释。`

// buildIntentV3Envelope 构建当前 Turn 的 Envelope：当前消息 + 紧邻未答复
// 的客户连续消息（无 AI/人工回复分隔）。
func buildIntentV3Envelope(req RunInput, history adapter.HistoryBuildResult) contextcompiler.TurnInputEnvelope {
	messages := make([]models.Message, 0, 4)
	for index := len(history.RawItems) - 1; index >= 0; index-- {
		item := history.RawItems[index]
		if item.SenderType != req.UserMessage.SenderType {
			break // 一旦出现非客户消息即停止（媒体/文字连续段）
		}
		messages = append([]models.Message{item}, messages...)
		if len(messages) >= 6 {
			break
		}
	}
	messages = append(messages, req.UserMessage)
	scope := contextcompiler.EnvelopeScope{
		TenantID:       req.Conversation.TenantID,
		StoreID:        req.Conversation.StoreID,
		ConversationID: req.Conversation.ID,
		SessionNo:      req.UserMessage.SessionNo,
	}
	return contextcompiler.BuildTurnInputEnvelope(scope, messages)
}

// parseIntentTasksV3Wire 严格解码 + 业务校验（Schema、版本、覆盖率集合等式）。
func parseIntentTasksV3Wire(content string) (intentTasksV3Wire, error) {
	parsed, err := strictjson.DecodeObject[intentTasksV3Wire]([]byte(content), strictjson.DecodeOptions{
		MaxBytes: 32 * 1024, Schema: contracts.MustSchema(contracts.SchemaIntentTasksV3),
	})
	if err != nil {
		return intentTasksV3Wire{}, err
	}
	if parsed.SchemaVersion != contracts.SchemaIntentTasksV3 {
		return intentTasksV3Wire{}, fmt.Errorf("intent v3 schema version mismatch")
	}
	return parsed, nil
}

// validateV3UtteranceCoverage 契约 10.7：非空 URef 集合等式。
func validateV3UtteranceCoverage(envelope contextcompiler.TurnInputEnvelope, coverage []intentCoverageItemWire) []string {
	issues := make([]string, 0)
	nonEmpty := map[string]struct{}{}
	for _, utterance := range envelope.Utterances {
		if strings.TrimSpace(utterance.Text) != "" {
			nonEmpty[utterance.Ref] = struct{}{}
		}
	}
	seen := map[string]string{}
	for _, item := range coverage {
		if previous, dup := seen[item.SourceRef]; dup {
			issues = append(issues, fmt.Sprintf("utterance %s covered twice (%s/%s)", item.SourceRef, previous, item.Status))
			continue
		}
		seen[item.SourceRef] = item.Status
		if (item.Status == "covered") != (len(item.TaskSequences) > 0) {
			issues = append(issues, fmt.Sprintf("utterance %s coverage status mismatch", item.SourceRef))
		}
		if item.Status == "ignored" && item.IgnoredReason == "none" {
			issues = append(issues, fmt.Sprintf("utterance %s ignored without reason", item.SourceRef))
		}
		delete(nonEmpty, item.SourceRef)
	}
	for ref := range nonEmpty {
		issues = append(issues, fmt.Sprintf("utterance %s missing from coverage", ref))
	}
	return issues
}

// adaptIntentV3ToTrace 把 V3 输出校验、规范化后适配到既有 V2 下游。
// 返回 degraded=true 时表示走了降级路径（仍继续，不转人工）。
func adaptIntentV3ToTrace(envelope contextcompiler.TurnInputEnvelope, parsed intentTasksV3Wire, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, bool, error) {
	tasks := make([]IntentTaskV3, 0, len(parsed.Tasks))
	for _, task := range parsed.Tasks {
		spans := make([]IntentSourceSpan, 0, len(task.SourceSpans))
		for _, span := range task.SourceSpans {
			spans = append(spans, IntentSourceSpan{SourceRef: span.SourceRef, Start: span.Start, End: span.End, Quote: span.Quote})
		}
		tasks = append(tasks, IntentTaskV3{
			Sequence: task.Sequence, Intent: task.Intent, SubIntent: task.SubIntent,
			SourceRefs: task.SourceRefs, SourceSpans: spans, DependsOnObs: task.DependsOnObs,
			NormalizedText: task.Normalized, RequestMode: task.RequestMode, Confidence: task.Confidence,
		})
		for _, requirement := range task.Requirements {
			tasks[len(tasks)-1].Requirements = append(tasks[len(tasks)-1].Requirements, RequirementSeed{
				Kind: requirement.Kind, Required: requirement.Required, Sequence: requirement.Sequence,
			})
		}
	}
	if issues := validateV3UtteranceCoverage(envelope, parsed.UtteranceCoverage); len(issues) > 0 {
		return callbacks.IntentTraceData{}, false, &strictjson.ProtocolError{
			Code: strictjson.ErrorJSONBusinessInvariant, Path: "utteranceCoverage",
			Message: strings.Join(issues, "; "),
		}
	}
	if issues := ValidateIntentTaskSources(envelope, tasks); len(issues) > 0 {
		return callbacks.IntentTraceData{}, false, &strictjson.ProtocolError{
			Code: strictjson.ErrorJSONBusinessInvariant, Path: "tasks",
			Message: fmt.Sprintf("source span invalid: %s", issues[0].Message),
		}
	}
	normalized := NormalizeIntentTasks(envelope, tasks)
	units := normalized.AcceptedUnits
	degraded := normalized.Status == "degraded_single_task"
	if len(units) == 0 {
		return callbacks.IntentTraceData{}, false, fmt.Errorf("intent v3 produced no question units")
	}
	// 复用 V2 适配：QuestionUnit -> IntentTaskV2 -> 能力派生 -> legacy trace。
	v2Tasks := make([]contracts.IntentTaskV2, 0, len(units))
	unitRequirements := make(map[int][]RequirementSeed, len(units))
	for _, unit := range units {
		v2Tasks = append(v2Tasks, contracts.IntentTaskV2{
			Sequence: unit.Sequence, Intent: unit.Intent, SubIntent: unit.SubIntent,
			Text: unit.Text, RequestMode: unit.RequestMode, Confidence: 0.9,
		})
		unitRequirements[unit.Sequence] = unit.Requirements
	}
	v2 := contracts.IntentTasksV2{SchemaVersion: contracts.SchemaIntentTasksV2, DialogueAct: parsed.DialogueAct, Tasks: v2Tasks}
	derived, deriveErr := DeriveRuntimeIntentCapabilities(v2, configs)
	var trace callbacks.IntentTraceData
	if deriveErr != nil {
		// 能力目录缺该意图时降级仍要产出可回复 trace（信息问答路径），
		// 不让降级协议失败升级为整次 Intent 失败。
		trace = callbacks.IntentTraceData{ShouldReply: true, DialogueAct: v2.DialogueAct,
			PrimaryIntent: "hotel_info", Reason: "intent_tasks.v3 degraded_single_task"}
		trace.IntentTasks = make([]callbacks.IntentTaskTraceData, 0, len(v2Tasks))
		for _, task := range v2Tasks {
			trace.IntentTasks = append(trace.IntentTasks, callbacks.IntentTaskTraceData{
				Sequence: task.Sequence, Intent: task.Intent, SubIntent: task.SubIntent,
				Text: task.Text, RequestMode: task.RequestMode, Confidence: task.Confidence,
				NeedsKnowledge: true,
			})
		}
	} else {
		trace = AdaptIntentV2ToLegacyTrace(v2, derived)
	}
	for index := range trace.IntentTasks {
		for _, seed := range unitRequirements[trace.IntentTasks[index].Sequence] {
			trace.IntentTasks[index].Requirements = append(trace.IntentTasks[index].Requirements,
				fmt.Sprintf("%s|%t", seed.Kind, seed.Required))
		}
	}
	trace.MatchMode = "intent_tasks.v3"
	trace.Reason = "intent_tasks.v3 envelope+span normalized"
	if degraded {
		trace.Reason = "intent_tasks.v3 degraded_single_task"
	}
	return trace, degraded, nil
}

// renderIntentV3EnvelopeBlock 渲染 Intent User Prompt（契约 9.4）。
func renderIntentV3EnvelopeBlock(envelope contextcompiler.TurnInputEnvelope, catalog []models.ReplyIntentConfig) (string, error) {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	catalogJSON, err := json.Marshal(renderIntentCatalogEntries(catalog))
	if err != nil {
		return "", err
	}
	_ = catalogJSON
	return "[CURRENT_TURN_ENVELOPE]\n" + string(raw) + "\n\n[ALLOWED_INTENT_CATALOG]\n" + string(catalogJSON), nil
}

type intentCatalogEntry struct {
	Intent    string `json:"intent"`
	SubIntent string `json:"subIntent"`
}

func renderIntentCatalogEntries(configs []models.ReplyIntentConfig) []intentCatalogEntry {
	entries := make([]intentCatalogEntry, 0, len(configs))
	for _, config := range configs {
		entries = append(entries, intentCatalogEntry{Intent: config.Code, SubIntent: config.Name})
	}
	return entries
}

// detectRuntimeIntentV3 是 IntentTasksV3 的模型调用入口（成组灰度）：
// Envelope -> V3 Prompt -> 一次模型调用 + 至多一次协议修复 -> 校验/规范化
// -> 适配 V2 下游。失败不转人工，按 Intent 技术失败走短重试。
func detectRuntimeIntentV3(ctx context.Context, req RunInput, history adapter.HistoryBuildResult, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error) {
	resolved, err := resolveRuntimeIntentDetectModelCall(req)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	intentConfig, err := withRuntimeIntentStructuredOutputSchema(resolved.RuntimeConfig(), contracts.MustSchema(contracts.SchemaIntentTasksV3))
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	if strings.TrimSpace(intentConfig.ModelName) == "" || strings.TrimSpace(string(intentConfig.Provider)) == "" {
		return callbacks.IntentTraceData{}, fmt.Errorf("intent model unavailable")
	}
	intentCtx, usageCapture := usagex.WithCapture(ctx)
	intentCtx = usagex.WithScope(intentCtx, services.ModelCallUsageScope(
		resolved, req.Conversation.ID, req.UserMessage.ID, req.UserMessage.RequestID))
	chatModel, err := factory.NewChatModelFactory().Build(intentCtx, intentConfig)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	envelope := buildIntentV3Envelope(req, history)
	userBlock, err := renderIntentV3EnvelopeBlock(envelope, configs)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	messages := []*schema.Message{
		schema.SystemMessage(intentV3SystemPrompt),
		schema.UserMessage(userBlock),
	}
	if open, retryAt := channelbreaker.IsOpen("intent_detect_v3", resolved.ModelName, time.Now()); open {
		return callbacks.IntentTraceData{}, fmt.Errorf("intent v3 channel breaker open until %s", retryAt.Format(time.RFC3339))
	}
	startedAt := time.Now()
	receiptOffset := len(usageCapture.Receipts())
	callCtx, cancel := context.WithTimeout(intentCtx, runtimeIntentModelInvocationTimeout(intentConfig.TimeoutMS, intentConfig.MaxRetryCount))
	result, err := chatModel.Generate(callCtx, messages)
	cancel()
	if err != nil {
		channelbreaker.RecordFailure("intent_detect_v3", resolved.ModelName, time.Now())
		recordIntentModelUsage(req, intentConfig, resolved, nil, gatewayReceiptsSince(usageCapture, receiptOffset), 1, time.Since(startedAt).Milliseconds(), err)
		return callbacks.IntentTraceData{}, err
	}
	channelbreaker.RecordSuccess("intent_detect_v3", resolved.ModelName)
	recordIntentModelUsage(req, intentConfig, resolved, result, gatewayReceiptsSince(usageCapture, receiptOffset), 1, time.Since(startedAt).Milliseconds(), nil)
	parsed, err := parseIntentTasksV3Wire(result.Content)
	if err != nil && runtimeProtocolRepairAllowed(err) {
		// 契约 10.5：一次协议修复；仍失败时由 Normalize 降级，不转人工。
		trace, degraded, adaptErr := adaptIntentV3ToTrace(envelope, parsed, configs)
		_ = trace
		_ = degraded
		if adaptErr == nil {
			return adaptDone(envelope, parsed, configs)
		}
		repairMessages := append(append([]*schema.Message{}, messages...),
			schema.AssistantMessage(result.Content, nil),
			schema.UserMessage(buildIntentV3RepairInstruction(err)))
		retryStartedAt := time.Now()
		retryOffset := len(usageCapture.Receipts())
		retryCtx, retryCancel := context.WithTimeout(intentCtx, runtimeIntentModelInvocationTimeout(intentConfig.TimeoutMS, intentConfig.MaxRetryCount))
		retry, retryErr := chatModel.Generate(retryCtx, repairMessages)
		retryCancel()
		if retryErr == nil {
			recordIntentModelUsage(req, intentConfig, resolved, retry, gatewayReceiptsSince(usageCapture, retryOffset), 2, time.Since(retryStartedAt).Milliseconds(), nil)
			if retried, retryParseErr := parseIntentTasksV3Wire(retry.Content); retryParseErr == nil {
				return adaptDone(envelope, retried, configs)
			}
		}
		// 修复失败：降级为逐 utterance 全文 QuestionUnit（不转人工）。
		return degradeIntentV3(envelope, parsed, configs, err)
	}
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	return adaptDone(envelope, parsed, configs)
}

func adaptDone(envelope contextcompiler.TurnInputEnvelope, parsed intentTasksV3Wire, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error) {
	trace, _, err := adaptIntentV3ToTrace(envelope, parsed, configs)
	return trace, err
}

// degradeIntentV3 契约 10.5.2：协议失败时按唯一非空 utterance 收敛，
// 不复制多个 Task，不转人工。
func degradeIntentV3(envelope contextcompiler.TurnInputEnvelope, parsed intentTasksV3Wire, configs []models.ReplyIntentConfig, cause error) (callbacks.IntentTraceData, error) {
	fallback := intentTasksV3Wire{SchemaVersion: contracts.SchemaIntentTasksV3, DialogueAct: parsed.DialogueAct}
	seq := 0
	for _, utterance := range envelope.Utterances {
		if strings.TrimSpace(utterance.Text) == "" {
			continue
		}
		seq++
		runes := []rune(utterance.Text)
		fallback.Tasks = append(fallback.Tasks, intentTaskV3Wire{
			Sequence: seq, Intent: "hotel_info", SourceRefs: []string{utterance.Ref},
			SourceSpans: []intentSpanWire{{SourceRef: utterance.Ref, Start: 0, End: len(runes), Quote: utterance.Text}},
			Normalized:  utterance.Text, RequestMode: "answer", Confidence: 0.5,
		})
		fallback.UtteranceCoverage = append(fallback.UtteranceCoverage, intentCoverageItemWire{
			SourceRef: utterance.Ref, Status: "covered", TaskSequences: []int{seq},
		})
	}
	if len(fallback.Tasks) == 0 {
		return callbacks.IntentTraceData{}, fmt.Errorf("intent v3 protocol failure without fallback: %w", cause)
	}
	trace, _, err := adaptIntentV3ToTrace(envelope, fallback, configs)
	return trace, err
}

func buildIntentV3RepairInstruction(err error) string {
	return "上一次输出违反 intent_tasks.v3 协议：" + err.Error() +
		"\n请重新输出严格 JSON：每个非空 URef 恰好一条 utteranceCoverage；" +
		"sourceSpan.quote 必须是对应 utterance.text 的连续原文片段（0-based rune offset，end exclusive）。"
}
