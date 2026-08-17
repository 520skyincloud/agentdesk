package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode"

	"agent-desk/internal/ai/runtime/channelbreaker"
	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/factory"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/modelconfig"
	"agent-desk/internal/pkg/strictjson"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/mlogclub/simple/sqls"
)

// 多模态契约 9/10/12：IntentTasksV3 仅作为显式实验模式使用；生产主链保持
// V2 的轻量任务协议，避免模型计算 span 造成整轮失败。
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
	Normalized   string            `json:"normalizedText"`
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
- textOrigin=media_analysis 表示文字来自媒体分析，不是客户原话；只能结合对应 Observation 理解可见问题或发起中性澄清。

输出 intent_tasks.v3。

强制规则：
1. 先为每个非空 utterance 输出一条 utteranceCoverage；不得遗漏 URef。
2. covered 必须列出 Task sequence；ignored 只允许用于与另一条 covered 输入完全等价的重复消息，ignoredReason 必须为 duplicate_equivalent。
3. 每个 Task 必须引用真实 sourceRef。
4. sourceSpan.quote 必须是对应 utterance.text 的连续原文片段。
5. 多问题必须使用各自原文片段；禁止把完整原文复制到多个 Task。
6. 图片、附件和历史内容默认是 Observation，不自动创建业务 Task。
7. 当前文字明确引用图片时，在 dependsOnObservationRefs 中引用对应 Observation。
8. 语音 transcript 属于当前客户输入，可拆多个问题，但每题必须绑定自己的 Span。
9. 不得输出 taskKey、needsKnowledge、needsResource、needsHumanRoute、action 或执行结果。
10. intent 必须来自 ALLOWED_INTENT_CATALOG，并严格遵守该项的能力边界。
11. interaction、social requestMode、greeting/thanks/closing 不得伪装成酒店知识问题。
12. answerRequirements 描述本 Task 必须完成的答案义务；每个 Task 至少一项。
13. 寒暄、感谢、表情、问号、闲聊也是需要处理的输入，必须建立 interaction Task，不能标记 ignored。
14. 纯表情、拟声、寒暄或闲聊时 dialogueAct 使用 social/greeting/thanks/closing，Task 使用 intent=interaction、requestMode=social；不得使用 hotel_info。
15. 只输出严格 JSON Object，不输出 Markdown 或解释。`

const intentV3MaxUtterancesPerBatch = 12

type intentV3ProtocolDiagnostic struct {
	Code            string
	Path            string
	RepairAttempted bool
	RepairSucceeded bool
	Degraded        bool
}

func newIntentV3ProtocolDiagnostic(err error) intentV3ProtocolDiagnostic {
	code, _ := strictjson.CodeOf(err)
	if strings.TrimSpace(code) == "" {
		code = "unknown"
	}
	path, _ := strictjson.PathOf(err)
	if strings.TrimSpace(path) == "" {
		path = "$"
	}
	return intentV3ProtocolDiagnostic{Code: code, Path: path}
}

func mergeIntentV3ProtocolDiagnostic(current, incoming intentV3ProtocolDiagnostic) intentV3ProtocolDiagnostic {
	if current.Code == "" {
		current.Code = incoming.Code
	}
	if current.Path == "" {
		current.Path = incoming.Path
	}
	current.RepairAttempted = current.RepairAttempted || incoming.RepairAttempted
	current.RepairSucceeded = current.RepairSucceeded || incoming.RepairSucceeded
	current.Degraded = current.Degraded || incoming.Degraded
	return current
}

func applyIntentV3ProtocolDiagnostic(trace *callbacks.IntentTraceData, diagnostic intentV3ProtocolDiagnostic) {
	if trace == nil || diagnostic.Code == "" {
		return
	}
	trace.ProtocolErrorCode = diagnostic.Code
	trace.ProtocolErrorPath = diagnostic.Path
	trace.RepairAttempted = diagnostic.RepairAttempted
	trace.RepairSucceeded = diagnostic.RepairSucceeded
	trace.ProtocolDegraded = diagnostic.Degraded
}

// buildIntentV3Envelope constructs the envelope from the authoritative current
// turn when a persisted turn is available. History is only a compatibility
// fallback for non-coordinated calls. This prevents history window truncation
// from permanently omitting an older unanswered source message.
func buildIntentV3Envelope(req RunInput, history adapter.HistoryBuildResult) (contextcompiler.TurnInputEnvelope, error) {
	messages := authoritativeIntentTurnMessages(req)
	if len(messages) == 0 {
		messages = recentIntentHistoryMessages(req, history)
	}
	scope := contextcompiler.EnvelopeScope{
		TenantID:       req.Conversation.TenantID,
		StoreID:        req.Conversation.StoreID,
		ConversationID: req.Conversation.ID,
		SessionNo:      req.UserMessage.SessionNo,
		TurnID:         req.UserMessage.AIReplyTurnID,
		TurnVersion:    req.UserMessage.AIReplyTurnVersion,
	}
	analyses, err := services.MessageAnalysisService.ReadyV2ForMessages(messages)
	if err != nil {
		return contextcompiler.TurnInputEnvelope{}, err
	}
	envelope := contextcompiler.BuildTurnInputEnvelopeWithAnalyses(scope, messages, analyses)
	populateIntentV3UnresolvedTasks(&envelope, req)
	return envelope, nil
}

// populateIntentV3UnresolvedTasks projects only durable task metadata into the
// current-turn envelope. Customer text remains source-bound and is never copied
// from AIReplyTurnTask into the Intent prompt.
func populateIntentV3UnresolvedTasks(envelope *contextcompiler.TurnInputEnvelope, req RunInput) {
	if envelope == nil || req.UserMessage.AIReplyTurnID <= 0 || req.Conversation.TenantID <= 0 || sqls.DB() == nil {
		return
	}
	all := repositories.AIReplyTurnTaskRepository.FindByTurnInTenant(
		sqls.DB(), req.Conversation.TenantID, req.UserMessage.AIReplyTurnID,
	)
	tasks := make([]models.AIReplyTurnTask, 0, len(all))
	questionTextByTaskID := make(map[int64]string, len(all))
	for _, task := range all {
		if !intentV3TaskCarriesFollowUpContext(task) {
			continue
		}
		source := repositories.MessageRepository.GetInTenant(sqls.DB(), task.SourceMessageID, req.Conversation.TenantID)
		if source == nil || source.ConversationID != req.Conversation.ID || source.SessionNo != req.UserMessage.SessionNo ||
			source.AIReplyTurnID != req.UserMessage.AIReplyTurnID || source.SenderType != enums.IMSenderTypeCustomer {
			continue
		}
		questionTextByTaskID[task.ID] = runtimeTaskSourceSpanText(
			runtimeTaskSourceText(*source), task.SourceSpanStart, task.SourceSpanEnd,
		)
		tasks = append(tasks, task)
	}
	if len(tasks) > intentV3MaxUtterancesPerBatch*2 {
		tasks = tasks[len(tasks)-intentV3MaxUtterancesPerBatch*2:]
	}
	appendIntentV3UnresolvedTasksWithQuestionText(envelope, tasks, questionTextByTaskID)
}

func intentV3TaskCarriesFollowUpContext(task models.AIReplyTurnTask) bool {
	switch task.Status {
	case enums.AIReplyTurnTaskStatusPending,
		enums.AIReplyTurnTaskStatusRunning,
		enums.AIReplyTurnTaskStatusReady,
		enums.AIReplyTurnTaskStatusWaitingCoverage,
		enums.AIReplyTurnTaskStatusCommitted,
		enums.AIReplyTurnTaskStatusFailed:
		return true
	default:
		return false
	}
}

func appendIntentV3UnresolvedTasks(envelope *contextcompiler.TurnInputEnvelope, tasks []models.AIReplyTurnTask) {
	appendIntentV3UnresolvedTasksWithQuestionText(envelope, tasks, nil)
}

func appendIntentV3UnresolvedTasksWithQuestionText(
	envelope *contextcompiler.TurnInputEnvelope,
	tasks []models.AIReplyTurnTask,
	questionTextByTaskID map[int64]string,
) {
	if envelope == nil || len(tasks) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(envelope.UnresolvedTasks)+len(tasks))
	for _, task := range envelope.UnresolvedTasks {
		if key := strings.TrimSpace(task.TaskKey); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, task := range tasks {
		key := strings.TrimSpace(task.TaskKey)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		questionText := strings.TrimSpace(questionTextByTaskID[task.ID])
		item := contextcompiler.EnvelopeUnresolvedTask{
			TaskKey: key, SourceMessageID: task.SourceMessageID, SequenceNo: task.SequenceNo,
			Intent: strings.TrimSpace(task.Intent), SubIntent: strings.TrimSpace(task.SubIntent),
			Status: string(task.Status), CanonicalQuestionHash: strings.TrimSpace(task.CanonicalQuestionHash),
			QuestionText:  questionText,
			ResolvedTopic: firstNonEmptyQuestionTopic(questionText, task.SubIntent, task.Intent),
		}
		if raw := strings.TrimSpace(task.AnswerRequirementsJSON); raw != "" {
			if set, err := contracts.DecodeAnswerRequirementSetV1([]byte(raw)); err == nil {
				for _, requirement := range set.Requirements {
					item.Requirements = append(item.Requirements, contextcompiler.EnvelopeRequirement{
						Kind: requirement.Kind, Required: requirement.Required, Sequence: requirement.Sequence,
					})
				}
			}
		}
		envelope.UnresolvedTasks = append(envelope.UnresolvedTasks, item)
		seen[key] = struct{}{}
	}
}

func authoritativeIntentTurnMessages(req RunInput) []models.Message {
	if req.UserMessage.AIReplyTurnID <= 0 || req.Conversation.TenantID <= 0 || req.Conversation.ID <= 0 || sqls.DB() == nil {
		return nil
	}
	messages := repositories.MessageRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", req.Conversation.TenantID).
		Eq("conversation_id", req.Conversation.ID).
		Eq("session_no", req.UserMessage.SessionNo).
		Eq("ai_reply_turn_id", req.UserMessage.AIReplyTurnID).
		Eq("sender_type", enums.IMSenderTypeCustomer).
		Where("recalled_at IS NULL AND send_status NOT IN (?, ?)", enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Asc("ai_reply_turn_version").
		Asc("id"))
	return uniqueIntentMessages(messages, req.UserMessage)
}

func recentIntentHistoryMessages(req RunInput, history adapter.HistoryBuildResult) []models.Message {
	messages := make([]models.Message, 0, 8)
	for index := len(history.RawItems) - 1; index >= 0; index-- {
		item := history.RawItems[index]
		if item.SenderType != req.UserMessage.SenderType || item.SessionNo != req.UserMessage.SessionNo {
			break
		}
		if req.UserMessage.AIReplyTurnID > 0 && item.AIReplyTurnID > 0 && item.AIReplyTurnID != req.UserMessage.AIReplyTurnID {
			break
		}
		messages = append([]models.Message{item}, messages...)
	}
	return uniqueIntentMessages(messages, req.UserMessage)
}

func uniqueIntentMessages(messages []models.Message, current models.Message) []models.Message {
	ret := make([]models.Message, 0, len(messages)+1)
	seen := make(map[int64]struct{}, len(messages)+1)
	for _, item := range messages {
		if item.ID <= 0 {
			continue
		}
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		ret = append(ret, item)
	}
	if current.ID > 0 {
		if _, exists := seen[current.ID]; !exists {
			ret = append(ret, current)
		}
	}
	sort.SliceStable(ret, func(i, j int) bool {
		if ret[i].AIReplyTurnVersion != ret[j].AIReplyTurnVersion {
			return ret[i].AIReplyTurnVersion < ret[j].AIReplyTurnVersion
		}
		return ret[i].ID < ret[j].ID
	})
	return ret
}

// parseIntentTasksV3Wire 严格解码 + 业务校验（Schema、版本、覆盖率集合等式）。
func parseIntentTasksV3Wire(content string) (intentTasksV3Wire, error) {
	normalized, _ := normalizeStructuredModelObject(content)
	parsed, err := strictjson.DecodeObject[intentTasksV3Wire]([]byte(normalized), strictjson.DecodeOptions{
		MaxBytes: 32 * 1024, Schema: contracts.MustSchema(contracts.SchemaIntentTasksV3),
	})
	if err != nil {
		return intentTasksV3Wire{}, err
	}
	if parsed.SchemaVersion != contracts.SchemaIntentTasksV3 {
		return intentTasksV3Wire{}, &strictjson.ProtocolError{
			Code: strictjson.ErrorJSONBusinessInvariant, Path: "$.schemaVersion",
			Message: "intent v3 schema version mismatch",
		}
	}
	return parsed, nil
}

// validateV3UtteranceCoverage 契约 10.7：非空 URef 集合等式。
func validateV3UtteranceCoverage(envelope contextcompiler.TurnInputEnvelope, coverage []intentCoverageItemWire, tasks []intentTaskV3Wire) []string {
	issues := make([]string, 0)
	nonEmpty := map[string]struct{}{}
	for _, utterance := range envelope.Utterances {
		if strings.TrimSpace(utterance.Text) != "" {
			nonEmpty[utterance.Ref] = struct{}{}
		}
	}
	taskBySequence := make(map[int]intentTaskV3Wire, len(tasks))
	for _, task := range tasks {
		if _, exists := taskBySequence[task.Sequence]; exists {
			issues = append(issues, fmt.Sprintf("task sequence %d appears more than once", task.Sequence))
			continue
		}
		taskBySequence[task.Sequence] = task
	}
	seen := map[string]string{}
	normalizedByRef := make(map[string]string, len(envelope.Utterances))
	for _, utterance := range envelope.Utterances {
		if value := normalizeQuestionText(utterance.Text); value != "" {
			normalizedByRef[utterance.Ref] = value
		}
	}
	coveredNormalized := make(map[string]struct{}, len(coverage))
	for _, item := range coverage {
		if item.Status != "covered" {
			continue
		}
		if value := normalizedByRef[item.SourceRef]; value != "" {
			coveredNormalized[value] = struct{}{}
		}
	}
	coveredTaskSequences := make(map[int]map[string]struct{}, len(tasks))
	for _, item := range coverage {
		if previous, dup := seen[item.SourceRef]; dup {
			issues = append(issues, fmt.Sprintf("utterance %s covered twice (%s/%s)", item.SourceRef, previous, item.Status))
			continue
		}
		seen[item.SourceRef] = item.Status
		if _, known := nonEmpty[item.SourceRef]; !known {
			issues = append(issues, fmt.Sprintf("coverage references unknown or empty utterance %s", item.SourceRef))
		}
		if (item.Status == "covered") != (len(item.TaskSequences) > 0) {
			issues = append(issues, fmt.Sprintf("utterance %s coverage status mismatch", item.SourceRef))
		}
		if item.Status == "ignored" {
			if strings.TrimSpace(item.IgnoredReason) != "duplicate_equivalent" {
				issues = append(issues, fmt.Sprintf("utterance %s ignored with unsupported reason", item.SourceRef))
			} else if normalized := normalizedByRef[item.SourceRef]; normalized == "" {
				issues = append(issues, fmt.Sprintf("utterance %s ignored without comparable text", item.SourceRef))
			} else if _, duplicated := coveredNormalized[normalized]; !duplicated {
				issues = append(issues, fmt.Sprintf("utterance %s ignored without an equivalent covered utterance", item.SourceRef))
			}
		}
		if item.Status == "covered" && strings.TrimSpace(item.IgnoredReason) != "" {
			issues = append(issues, fmt.Sprintf("utterance %s covered with ignored reason", item.SourceRef))
		}
		for _, sequence := range item.TaskSequences {
			task, exists := taskBySequence[sequence]
			if !exists {
				issues = append(issues, fmt.Sprintf("utterance %s references unknown task sequence %d", item.SourceRef, sequence))
				continue
			}
			if !stringInExactSet(item.SourceRef, task.SourceRefs) {
				issues = append(issues, fmt.Sprintf("utterance %s does not belong to task sequence %d", item.SourceRef, sequence))
			}
			if coveredTaskSequences[sequence] == nil {
				coveredTaskSequences[sequence] = make(map[string]struct{})
			}
			coveredTaskSequences[sequence][item.SourceRef] = struct{}{}
		}
		delete(nonEmpty, item.SourceRef)
	}
	for _, task := range tasks {
		spanRefs := make(map[string]struct{}, len(task.SourceSpans))
		for _, span := range task.SourceSpans {
			spanRefs[span.SourceRef] = struct{}{}
		}
		if !sameStringSet(task.SourceRefs, mapKeys(spanRefs)) {
			issues = append(issues, fmt.Sprintf("task sequence %d sourceRefs do not match sourceSpans", task.Sequence))
		}
		for _, ref := range task.SourceRefs {
			if _, covered := coveredTaskSequences[task.Sequence][ref]; !covered {
				issues = append(issues, fmt.Sprintf("task sequence %d is not linked from coverage for %s", task.Sequence, ref))
			}
		}
	}
	for ref := range nonEmpty {
		issues = append(issues, fmt.Sprintf("utterance %s missing from coverage", ref))
	}
	return issues
}

// validateV3SemanticFragmentCoverage closes the gap between message-level
// utteranceCoverage and the actual questions inside one long text/voice
// utterance. Every deterministic punctuation-delimited fragment must overlap
// at least one valid source span owned by a task linked from that utterance's
// coverage entry. This is a protocol check only; it does not infer intent or
// perform keyword classification.
func validateV3SemanticFragmentCoverage(envelope contextcompiler.TurnInputEnvelope, coverage []intentCoverageItemWire, tasks []intentTaskV3Wire) []string {
	utteranceByRef := make(map[string]contextcompiler.EnvelopeUtterance, len(envelope.Utterances))
	for _, utterance := range envelope.Utterances {
		utteranceByRef[utterance.Ref] = utterance
	}
	taskBySequence := make(map[int]intentTaskV3Wire, len(tasks))
	for _, task := range tasks {
		taskBySequence[task.Sequence] = task
	}

	issues := make([]string, 0)
	for _, item := range coverage {
		if item.Status != "covered" {
			continue
		}
		utterance, ok := utteranceByRef[item.SourceRef]
		if !ok || strings.TrimSpace(utterance.Text) == "" {
			continue
		}
		spans := make([]intentSpanWire, 0)
		for _, sequence := range item.TaskSequences {
			task, exists := taskBySequence[sequence]
			if !exists {
				continue
			}
			for _, span := range task.SourceSpans {
				if span.SourceRef == item.SourceRef {
					spans = append(spans, span)
				}
			}
		}
		for _, fragment := range splitFallbackUtteranceClauses(utterance.Text, len([]rune(utterance.Text))+1) {
			covered := false
			for _, span := range spans {
				if span.Start < fragment.End && span.End > fragment.Start {
					covered = true
					break
				}
			}
			if !covered {
				issues = append(issues, fmt.Sprintf(
					"utterance %s fragment [%d,%d) is not covered by any linked task span: %q",
					item.SourceRef, fragment.Start, fragment.End, fragment.Quote,
				))
			}
		}
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
	tasks = normalizeIntentTasksForDialogueAct(parsed.DialogueAct, tasks)
	if issues := validateV3UtteranceCoverage(envelope, parsed.UtteranceCoverage, parsed.Tasks); len(issues) > 0 {
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
	if issues := validateV3SemanticFragmentCoverage(envelope, parsed.UtteranceCoverage, parsed.Tasks); len(issues) > 0 {
		return callbacks.IntentTraceData{}, false, &strictjson.ProtocolError{
			Code: strictjson.ErrorJSONBusinessInvariant, Path: "tasks.sourceSpans",
			Message: strings.Join(issues, "; "),
		}
	}
	normalized := NormalizeIntentTasksWithDialogueAct(envelope, tasks, parsed.DialogueAct)
	units := normalized.AcceptedUnits
	degraded := strings.HasPrefix(normalized.Status, "degraded_")
	if len(units) == 0 {
		return callbacks.IntentTraceData{}, false, &strictjson.ProtocolError{
			Code: strictjson.ErrorJSONBusinessInvariant, Path: "$.tasks",
			Message: "intent v3 produced no question units",
		}
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
		// 能力目录与模型输出不一致属于协议/配置故障。安全降级只能澄清，
		// 不能把未知输入强制送进酒店知识库，更不能转人工。
		trace = callbacks.IntentTraceData{ShouldReply: true, DialogueAct: v2.DialogueAct,
			PrimaryIntent: "interaction", MatchedIntentCode: "interaction",
			SubIntent: "clarify", NeedsClarification: true,
			Reason: "intent_tasks.v3 capability_unavailable_safe_clarify"}
		trace.IntentTasks = make([]callbacks.IntentTaskTraceData, 0, len(v2Tasks))
		for _, task := range v2Tasks {
			trace.IntentTasks = append(trace.IntentTasks, callbacks.IntentTaskTraceData{
				Sequence: task.Sequence, Intent: "interaction", SubIntent: "clarify",
				Text: task.Text, RequestMode: "clarify_previous", Confidence: task.Confidence,
			})
		}
	} else {
		trace = AdaptIntentV2ToLegacyTrace(v2, derived)
	}
	unitsBySequence := make(map[int]QuestionUnit, len(units))
	for _, unit := range units {
		unitsBySequence[unit.Sequence] = unit
	}
	for index := range trace.IntentTasks {
		unit, ok := unitsBySequence[trace.IntentTasks[index].Sequence]
		if !ok {
			return callbacks.IntentTraceData{}, false, &strictjson.ProtocolError{
				Code: strictjson.ErrorJSONReferenceInvalid, Path: "tasks.sequence",
				Message: fmt.Sprintf("normalized question unit missing for task sequence %d", trace.IntentTasks[index].Sequence),
			}
		}
		trace.IntentTasks[index].QuestionUnitKey = unit.QuestionKey
		trace.IntentTasks[index].RelationType = unit.Relation
		trace.IntentTasks[index].ParentTaskKey = unit.ParentTaskKey
		trace.IntentTasks[index].ResolvedTopic = unit.ResolvedTopic
		trace.IntentTasks[index].InheritedRequirements = encodeRequirementSeeds(unit.InheritedRequirements)
		trace.IntentTasks[index].SourceMessageID = unit.PrimarySourceMessageID
		trace.IntentTasks[index].CanonicalQuestionHash = unit.CanonicalQuestionHash
		trace.IntentTasks[index].SourceBindings, trace.IntentTasks[index].SourceSetFingerprint,
			trace.IntentTasks[index].AnalysisRevision = buildQuestionUnitSourceTrace(envelope, unit)
		trace.IntentTasks[index].ObservationBindings = buildQuestionUnitObservationTrace(envelope, unit)
		if len(unit.SourceSpans) > 0 {
			trace.IntentTasks[index].SourceSpanStart = unit.SourceSpans[0].Start
			trace.IntentTasks[index].SourceSpanEnd = unit.SourceSpans[0].End
		}
		seeds := unitRequirements[trace.IntentTasks[index].Sequence]
		if len(seeds) == 0 {
			seeds = defaultRequirementSeeds(trace.IntentTasks[index])
		}
		for _, seed := range seeds {
			trace.IntentTasks[index].Requirements = append(trace.IntentTasks[index].Requirements,
				fmt.Sprintf("%s|%t", seed.Kind, seed.Required))
		}
	}
	trace.MatchMode = "intent_tasks.v3"
	trace.UtteranceCoverage = buildIntentCoverageTrace(envelope, parsed.UtteranceCoverage)
	trace.Reason = "intent_tasks.v3 envelope+span normalized"
	if degraded {
		trace.Reason = "intent_tasks.v3 " + normalized.Status
	}
	return trace, degraded, nil
}

func encodeRequirementSeeds(seeds []RequirementSeed) []string {
	if len(seeds) == 0 {
		return nil
	}
	ret := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		ret = append(ret, fmt.Sprintf("%s|%t", strings.TrimSpace(seed.Kind), seed.Required))
	}
	return ret
}

func buildIntentCoverageTrace(envelope contextcompiler.TurnInputEnvelope, coverage []intentCoverageItemWire) []callbacks.IntentCoverageTraceData {
	messageIDByRef := make(map[string]int64, len(envelope.Utterances))
	for _, utterance := range envelope.Utterances {
		messageIDByRef[utterance.Ref] = utterance.MessageID
	}
	ret := make([]callbacks.IntentCoverageTraceData, 0, len(coverage))
	for _, item := range coverage {
		messageID := messageIDByRef[item.SourceRef]
		if messageID <= 0 {
			continue
		}
		ret = append(ret, callbacks.IntentCoverageTraceData{
			MessageID: messageID, Status: item.Status, ReasonCode: strings.TrimSpace(item.IgnoredReason),
			TaskSequences: append([]int(nil), item.TaskSequences...),
		})
	}
	return ret
}

func mapKeys(values map[string]struct{}) []string {
	ret := make([]string, 0, len(values))
	for value := range values {
		ret = append(ret, value)
	}
	return ret
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
	Intent             string   `json:"intent"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	NeedsKnowledge     bool     `json:"needsKnowledge"`
	NeedsResource      bool     `json:"needsResource"`
	ResourceType       string   `json:"resourceType"`
	NeedsTool          bool     `json:"needsTool"`
	NeedsHumanRoute    bool     `json:"needsHumanRoute"`
	NoReplyWhenMatched bool     `json:"noReplyWhenMatched"`
	PositiveExamples   []string `json:"positiveExamples"`
	NegativeExamples   []string `json:"negativeExamples"`
}

func renderIntentCatalogEntries(configs []models.ReplyIntentConfig) []intentCatalogEntry {
	entries := make([]intentCatalogEntry, 0, len(configs))
	for _, config := range configs {
		entries = append(entries, intentCatalogEntry{
			Intent: config.Code, Name: config.Name, Description: boundedIntentCatalogText(config.Description, 600),
			NeedsKnowledge: config.NeedsKnowledge, NeedsResource: config.NeedsResource,
			ResourceType: config.ResourceType, NeedsTool: config.NeedsTool,
			NeedsHumanRoute: config.NeedsHumanRoute, NoReplyWhenMatched: config.NoReplyWhenMatched,
			PositiveExamples: splitIntentCatalogExamples(config.PositiveExamples, 6),
			NegativeExamples: splitIntentCatalogExamples(config.NegativeExamples, 6),
		})
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
	intentConfig, err := withRuntimeIntentStructuredOutputSchema(resolved.RuntimeConfig(), contracts.SchemaIntentTasksV3, contracts.MustSchema(contracts.SchemaIntentTasksV3))
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
	envelope, err := buildIntentV3Envelope(req, history)
	if err != nil {
		return callbacks.IntentTraceData{}, err
	}
	if open, retryAt := channelbreaker.IsOpen("intent_detect_v3", resolved.ModelName, time.Now()); open {
		return callbacks.IntentTraceData{}, fmt.Errorf("intent v3 channel breaker open until %s", retryAt.Format(time.RFC3339))
	}
	batches := splitIntentV3Envelope(envelope, intentV3MaxUtterancesPerBatch)
	if len(batches) == 0 {
		return callbacks.IntentTraceData{}, fmt.Errorf("intent v3 envelope has no non-empty utterance")
	}
	outputs := make([]intentTasksV3Wire, 0, len(batches))
	diagnostic := intentV3ProtocolDiagnostic{}
	callNo := 0
	for _, batch := range batches {
		parsed, calls, batchDiagnostic, batchErr := detectRuntimeIntentV3Batch(
			intentCtx, req, batch, configs, resolved, intentConfig, chatModel, usageCapture, callNo,
		)
		callNo += calls
		diagnostic = mergeIntentV3ProtocolDiagnostic(diagnostic, batchDiagnostic)
		if batchErr != nil {
			return callbacks.IntentTraceData{}, batchErr
		}
		outputs = append(outputs, parsed)
	}
	merged := mergeIntentV3BatchOutputs(outputs)
	trace, adaptErr := adaptDone(envelope, merged, configs)
	if adaptErr != nil {
		mergedDiagnostic := newIntentV3ProtocolDiagnostic(adaptErr)
		mergedDiagnostic.Degraded = true
		diagnostic = mergeIntentV3ProtocolDiagnostic(diagnostic, mergedDiagnostic)
		logIntentV3ProtocolDiagnostic(adaptErr, 0, callNo)
		trace, adaptErr = degradeIntentV3(envelope, merged, configs, adaptErr)
		if adaptErr != nil {
			return callbacks.IntentTraceData{}, adaptErr
		}
	}
	applyIntentV3ProtocolDiagnostic(&trace, diagnostic)
	return trace, nil
}

func detectRuntimeIntentV3Batch(
	ctx context.Context,
	req RunInput,
	envelope contextcompiler.TurnInputEnvelope,
	configs []models.ReplyIntentConfig,
	resolved *services.ModelCallConfig,
	intentConfig modelconfig.Config,
	chatModel model.ToolCallingChatModel,
	usageCapture *usagex.Capture,
	callOffset int,
) (intentTasksV3Wire, int, intentV3ProtocolDiagnostic, error) {
	userBlock, err := renderIntentV3EnvelopeBlock(envelope, configs)
	if err != nil {
		return intentTasksV3Wire{}, 0, intentV3ProtocolDiagnostic{}, err
	}
	messages := []*schema.Message{schema.SystemMessage(intentV3SystemPrompt), schema.UserMessage(userBlock)}
	startedAt := time.Now()
	receiptOffset := len(usageCapture.Receipts())
	callCtx, cancel := context.WithTimeout(ctx, runtimeIntentModelInvocationTimeout(intentConfig.TimeoutMS, intentConfig.MaxRetryCount))
	result, err := chatModel.Generate(callCtx, messages)
	cancel()
	if err != nil {
		channelbreaker.RecordFailure("intent_detect_v3", resolved.ModelName, time.Now())
		recordIntentModelUsage(req, intentConfig, resolved, nil, gatewayReceiptsSince(usageCapture, receiptOffset), callOffset+1, time.Since(startedAt).Milliseconds(), err)
		return intentTasksV3Wire{}, 1, intentV3ProtocolDiagnostic{}, err
	}
	channelbreaker.RecordSuccess("intent_detect_v3", resolved.ModelName)
	recordIntentModelUsage(req, intentConfig, resolved, result, gatewayReceiptsSince(usageCapture, receiptOffset), callOffset+1, time.Since(startedAt).Milliseconds(), nil)
	parsed, protocolErr := parseIntentTasksV3Wire(result.Content)
	if protocolErr == nil {
		if _, adaptErr := adaptDone(envelope, parsed, configs); adaptErr == nil {
			return parsed, 1, intentV3ProtocolDiagnostic{}, nil
		} else {
			protocolErr = adaptErr
		}
	}
	diagnostic := newIntentV3ProtocolDiagnostic(protocolErr)
	logIntentV3ProtocolDiagnostic(protocolErr, len(result.Content), callOffset+1)
	if protocolErr == nil || !runtimeIntentV3RepairAllowed(protocolErr) {
		return intentTasksV3Wire{}, 1, diagnostic, protocolErr
	}
	diagnostic.RepairAttempted = true
	repairMessages := []*schema.Message{
		schema.SystemMessage(intentV3SystemPrompt + "\n\n" + buildIntentV3RepairInstruction(protocolErr)),
		schema.UserMessage(userBlock),
	}
	retryStartedAt := time.Now()
	retryOffset := len(usageCapture.Receipts())
	retryCtx, retryCancel := context.WithTimeout(ctx, runtimeIntentModelInvocationTimeout(intentConfig.TimeoutMS, intentConfig.MaxRetryCount))
	retry, retryErr := chatModel.Generate(retryCtx, repairMessages)
	retryCancel()
	recordIntentModelUsage(req, intentConfig, resolved, retry, gatewayReceiptsSince(usageCapture, retryOffset), callOffset+2, time.Since(retryStartedAt).Milliseconds(), retryErr)
	if retryErr == nil {
		retried, retryProtocolErr := parseIntentTasksV3Wire(retry.Content)
		if retryProtocolErr == nil {
			_, retryProtocolErr = adaptDone(envelope, retried, configs)
		}
		if retryProtocolErr == nil {
			diagnostic.RepairSucceeded = true
			return retried, 2, diagnostic, nil
		}
		logIntentV3ProtocolDiagnostic(retryProtocolErr, len(retry.Content), callOffset+2)
	}
	degraded, degradeErr := degradeIntentV3Wire(envelope)
	diagnostic.Degraded = true
	return degraded, 2, diagnostic, degradeErr
}

func splitIntentV3Envelope(envelope contextcompiler.TurnInputEnvelope, maxUtterances int) []contextcompiler.TurnInputEnvelope {
	if maxUtterances <= 0 {
		maxUtterances = intentV3MaxUtterancesPerBatch
	}
	ret := make([]contextcompiler.TurnInputEnvelope, 0, (len(envelope.Utterances)+maxUtterances-1)/maxUtterances)
	for start := 0; start < len(envelope.Utterances); {
		end := min(start+maxUtterances, len(envelope.Utterances))
		if end < len(envelope.Utterances) && end-start > 1 && strings.TrimSpace(envelope.Utterances[end-1].Text) == "" && strings.TrimSpace(envelope.Utterances[end].Text) != "" {
			end--
		}
		batch := sliceIntentV3Envelope(envelope, start, end)
		if intentEnvelopeHasNonEmptyUtterance(batch) {
			ret = append(ret, batch)
		}
		start = end
	}
	return ret
}

func sliceIntentV3Envelope(envelope contextcompiler.TurnInputEnvelope, start, end int) contextcompiler.TurnInputEnvelope {
	batch := contextcompiler.TurnInputEnvelope{
		SchemaVersion: envelope.SchemaVersion, Scope: envelope.Scope, PriorAssistant: envelope.PriorAssistant,
		UnresolvedTasks: append([]contextcompiler.EnvelopeUnresolvedTask(nil), envelope.UnresolvedTasks...),
		Utterances:      append([]contextcompiler.EnvelopeUtterance(nil), envelope.Utterances[start:end]...),
	}
	observationRefs := make(map[string]struct{})
	for _, utterance := range batch.Utterances {
		for _, ref := range utterance.ObservationRefs {
			observationRefs[ref] = struct{}{}
		}
	}
	for _, observation := range envelope.Observations {
		if _, ok := observationRefs[observation.Ref]; ok {
			batch.Observations = append(batch.Observations, observation)
		}
	}
	return batch
}

func intentEnvelopeHasNonEmptyUtterance(envelope contextcompiler.TurnInputEnvelope) bool {
	for _, utterance := range envelope.Utterances {
		if strings.TrimSpace(utterance.Text) != "" {
			return true
		}
	}
	return false
}

func mergeIntentV3BatchOutputs(outputs []intentTasksV3Wire) intentTasksV3Wire {
	merged := intentTasksV3Wire{SchemaVersion: contracts.SchemaIntentTasksV3, DialogueAct: "unknown"}
	sequenceOffset := 0
	for _, output := range outputs {
		if merged.DialogueAct == "unknown" {
			merged.DialogueAct = output.DialogueAct
		} else if output.DialogueAct != "" && output.DialogueAct != merged.DialogueAct {
			merged.DialogueAct = "new_topic"
		}
		for _, task := range output.Tasks {
			task.Sequence += sequenceOffset
			merged.Tasks = append(merged.Tasks, task)
		}
		for _, item := range output.UtteranceCoverage {
			for index := range item.TaskSequences {
				item.TaskSequences[index] += sequenceOffset
			}
			merged.UtteranceCoverage = append(merged.UtteranceCoverage, item)
		}
		sequenceOffset = len(merged.Tasks)
	}
	return merged
}

func normalizeIntentTasksForDialogueAct(dialogueAct string, tasks []IntentTaskV3) []IntentTaskV3 {
	for index := range tasks {
		act := classifyIntentTaskDialogueAct(tasks[index])
		if act == "" && len(tasks) == 1 {
			switch strings.TrimSpace(dialogueAct) {
			case "greeting", "thanks", "closing", "social":
				act = strings.TrimSpace(dialogueAct)
			}
		}
		switch act {
		case "greeting", "thanks", "closing", "social":
			tasks[index].Intent = "interaction"
			tasks[index].SubIntent = act
			tasks[index].RequestMode = "social"
			tasks[index].Requirements = []RequirementSeed{{Sequence: 1, Kind: "social_reply", Required: true}}
		case "clarify":
			tasks[index].Intent = "interaction"
			tasks[index].SubIntent = "clarify"
			tasks[index].RequestMode = "clarify_previous"
			tasks[index].Requirements = []RequirementSeed{{Sequence: 1, Kind: "clarification", Required: true}}
		}
	}
	return tasks
}

// classifyIntentTaskDialogueAct 是模型后的逐 Task 确定性保护。它只修正纯社交、
// 纯语气和无唯一指代的短追问，不识别任何酒店业务关键词。
func classifyIntentTaskDialogueAct(task IntentTaskV3) string {
	parts := make([]string, 0, len(task.SourceSpans))
	for _, span := range task.SourceSpans {
		if value := strings.TrimSpace(span.Quote); value != "" {
			parts = append(parts, value)
		}
	}
	text := strings.Join(parts, " ")
	if strings.TrimSpace(text) == "" {
		text = task.NormalizedText
	}
	if questionPunctuationOnly(text) {
		return "clarify"
	}
	compact := compactDialogueText(text)
	if compact == "" {
		return "social"
	}
	if symbolOnlyDialogueText(compact) {
		return "social"
	}
	if stringInExactSet(compact, []string{"你好", "您好", "在吗", "在不", "哈喽", "嗨", "hello", "hi", "早上好", "下午好", "晚上好"}) {
		return "greeting"
	}
	if stringInExactSet(compact, []string{"谢谢", "谢谢你", "谢谢您", "多谢", "感谢", "辛苦了", "麻烦了", "谢啦"}) {
		return "thanks"
	}
	if stringInExactSet(compact, []string{"再见", "拜拜", "晚安", "不用了", "没事了", "先这样", "回头聊"}) {
		return "closing"
	}
	if stringInExactSet(compact, []string{"怎么说", "什么意思", "然后呢", "这个呢", "你说啥", "没听懂", "没明白", "?", "??"}) {
		return "clarify"
	}
	if stringInExactSet(compact, []string{"哈哈", "哈哈哈", "嘿嘿", "嘻嘻", "嘻嘻嘻", "略略略", "好无聊", "好无聊啊", "无聊", "开心", "好玩", "哎呀", "哦吼吼"}) {
		return "social"
	}
	if act := reduplicatedDialogueFragmentAct(compact); act != "" {
		return act
	}
	if strings.TrimSpace(task.RequestMode) == "social" {
		return "social"
	}
	return ""
}

func questionPunctuationOnly(text string) bool {
	seenQuestion := false
	for _, r := range strings.TrimSpace(text) {
		if unicode.IsSpace(r) {
			continue
		}
		switch r {
		case '?', '？', '﹖', '⁇', '⁈', '⁉':
			seenQuestion = true
			continue
		}
		if unicode.IsPunct(r) {
			continue
		}
		return false
	}
	return seenQuestion
}

func symbolOnlyDialogueText(text string) bool {
	seen := false
	for _, r := range strings.TrimSpace(text) {
		if unicode.IsSpace(r) {
			continue
		}
		seen = true
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return seen
}

// reduplicatedDialogueFragmentAct recognizes a linguistic shape, not a hotel
// keyword. Short repeated interjections are social; other short reduplicated
// fragments are incomplete enough to require clarification instead of RAG.
func reduplicatedDialogueFragmentAct(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) < 3 || len(runes) > 8 {
		return ""
	}
	unique := make(map[rune]struct{}, len(runes))
	hasAdjacentRepeat := false
	allVocalization := true
	for index, r := range runes {
		if !unicode.IsLetter(r) || unicode.IsDigit(r) {
			return ""
		}
		unique[r] = struct{}{}
		if index > 0 && runes[index-1] == r {
			hasAdjacentRepeat = true
		}
		if !strings.ContainsRune("哈嘿嘻呵咦呀哎哦噢嗯呃额吼略哼", r) {
			allVocalization = false
		}
	}
	if !hasAdjacentRepeat || len(unique) > (len(runes)+1)/2 {
		return ""
	}
	if allVocalization {
		return "social"
	}
	return "clarify"
}

func compactDialogueText(text string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '，', ',', '。', '！', '!', '；', ';', '：', ':', '“', '”', '"', '\'', '（', '）', '(', ')':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(text)))
}

func stringInExactSet(value string, options []string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func defaultRequirementSeeds(task callbacks.IntentTaskTraceData) []RequirementSeed {
	kind := "answer"
	switch task.Intent {
	case "hotel_variable":
		kind = "resource_delivery"
	case "human_complaint_risk":
		kind = "handoff_decision"
	case "interaction":
		if task.SubIntent == "clarify" || task.RequestMode == "clarify_previous" {
			kind = "clarification"
		} else {
			kind = "social_reply"
		}
	case "service_request":
		kind = "service_boundary"
	case "hotel_info":
		kind = "knowledge_answer"
	}
	return []RequirementSeed{{Sequence: 1, Kind: kind, Required: true}}
}

func buildQuestionUnitSourceTrace(envelope contextcompiler.TurnInputEnvelope, unit QuestionUnit) ([]callbacks.TaskSourceBindingTraceData, string, int) {
	messageByRef := make(map[string]contextcompiler.EnvelopeUtterance, len(envelope.Utterances))
	for _, utterance := range envelope.Utterances {
		messageByRef[utterance.Ref] = utterance
	}
	bindings := make([]callbacks.TaskSourceBindingTraceData, 0, len(unit.SourceSpans))
	analysisRevision := 0
	for _, span := range unit.SourceSpans {
		utterance, ok := messageByRef[span.SourceRef]
		if !ok || utterance.MessageID <= 0 {
			continue
		}
		bindings = append(bindings, callbacks.TaskSourceBindingTraceData{
			MessageID: utterance.MessageID, SpanStart: span.Start, SpanEnd: span.End,
		})
		if utterance.MessageType != "text" && utterance.MessageType != "html" {
			analysisRevision = 1
		}
	}
	sort.SliceStable(bindings, func(i, j int) bool {
		if bindings[i].MessageID != bindings[j].MessageID {
			return bindings[i].MessageID < bindings[j].MessageID
		}
		if bindings[i].SpanStart != bindings[j].SpanStart {
			return bindings[i].SpanStart < bindings[j].SpanStart
		}
		return bindings[i].SpanEnd < bindings[j].SpanEnd
	})
	raw, _ := json.Marshal(bindings)
	sum := sha256.Sum256(raw)
	return bindings, hex.EncodeToString(sum[:]), analysisRevision
}

func buildQuestionUnitObservationTrace(envelope contextcompiler.TurnInputEnvelope, unit QuestionUnit) []callbacks.TaskObservationBindingTraceData {
	observationByRef := make(map[string]contextcompiler.EnvelopeObservation, len(envelope.Observations))
	for _, observation := range envelope.Observations {
		observationByRef[observation.Ref] = observation
	}
	bindings := make([]callbacks.TaskObservationBindingTraceData, 0, len(unit.DependsOnObs))
	seen := make(map[string]struct{}, len(unit.DependsOnObs))
	for _, ref := range unit.DependsOnObs {
		observation, ok := observationByRef[ref]
		if !ok || observation.MessageID <= 0 {
			continue
		}
		revision := observation.SourceRevision
		if revision <= 0 {
			revision = 1
		}
		key := fmt.Sprintf("%d/%d", observation.MessageID, revision)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		bindings = append(bindings, callbacks.TaskObservationBindingTraceData{
			MessageID: observation.MessageID, SourceRevision: revision,
		})
	}
	sort.SliceStable(bindings, func(i, j int) bool {
		if bindings[i].MessageID == bindings[j].MessageID {
			return bindings[i].SourceRevision < bindings[j].SourceRevision
		}
		return bindings[i].MessageID < bindings[j].MessageID
	})
	return bindings
}

func splitIntentCatalogExamples(raw string, limit int) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || r == '；' || r == ';' })
	ret := make([]string, 0, min(limit, len(parts)))
	for _, part := range parts {
		part = boundedIntentCatalogText(part, 120)
		if part == "" {
			continue
		}
		ret = append(ret, part)
		if len(ret) >= limit {
			break
		}
	}
	return ret
}

func boundedIntentCatalogText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

func adaptDone(envelope contextcompiler.TurnInputEnvelope, parsed intentTasksV3Wire, configs []models.ReplyIntentConfig) (callbacks.IntentTraceData, error) {
	trace, _, err := adaptIntentV3ToTrace(envelope, parsed, configs)
	return trace, err
}

// degradeIntentV3 契约 10.5.2：协议失败时按唯一非空 utterance 收敛，
// 不复制多个 Task，不转人工。
func degradeIntentV3(envelope contextcompiler.TurnInputEnvelope, parsed intentTasksV3Wire, configs []models.ReplyIntentConfig, cause error) (callbacks.IntentTraceData, error) {
	fallback, err := degradeIntentV3Wire(envelope)
	if err != nil {
		return callbacks.IntentTraceData{}, fmt.Errorf("intent v3 protocol failure without fallback: %w", cause)
	}
	if dialogueAct := strings.TrimSpace(parsed.DialogueAct); dialogueAct != "" {
		fallback.DialogueAct = dialogueAct
	}
	trace, _, err := adaptIntentV3ToTrace(envelope, fallback, configs)
	return trace, err
}

func degradeIntentV3Wire(envelope contextcompiler.TurnInputEnvelope) (intentTasksV3Wire, error) {
	fallback := intentTasksV3Wire{SchemaVersion: contracts.SchemaIntentTasksV3, DialogueAct: "unknown"}
	seq := 0
	nonEmptyRemaining := countNonEmptyEnvelopeUtterances(envelope.Utterances)
	for _, utterance := range envelope.Utterances {
		if strings.TrimSpace(utterance.Text) == "" {
			continue
		}
		maxClauses := 12 - seq - (nonEmptyRemaining - 1)
		if maxClauses < 1 {
			maxClauses = 1
		}
		sequences := make([]int, 0, maxClauses)
		for _, clause := range splitFallbackUtteranceClauses(utterance.Text, maxClauses) {
			seq++
			sequences = append(sequences, seq)
			fallback.Tasks = append(fallback.Tasks, intentTaskV3Wire{
				Sequence: seq, Intent: "interaction", SubIntent: "clarify", SourceRefs: []string{utterance.Ref},
				SourceSpans: []intentSpanWire{{SourceRef: utterance.Ref, Start: clause.Start, End: clause.End, Quote: clause.Quote}},
				Normalized:  clause.Quote, Requirements: []requirementWire{{Sequence: 1, Kind: "clarification", Required: true}},
				RequestMode: "clarify_previous", Confidence: 0.5,
			})
		}
		fallback.UtteranceCoverage = append(fallback.UtteranceCoverage, intentCoverageItemWire{
			SourceRef: utterance.Ref, Status: "covered", TaskSequences: sequences,
		})
		nonEmptyRemaining--
	}
	if len(fallback.Tasks) == 0 {
		return intentTasksV3Wire{}, fmt.Errorf("intent v3 protocol failure without fallback")
	}
	return fallback, nil
}

func buildIntentV3RepairInstruction(err error) string {
	code, _ := strictjson.CodeOf(err)
	path, _ := strictjson.PathOf(err)
	if path == "" {
		path = "$"
	}
	return "【协议修复，仅此一次】\n上一次输出违反 intent_tasks.v3 协议。" +
		"\nerrorCode=" + firstNonEmpty(code, "unknown") +
		"\njsonPath=" + path +
		"\n请根据同一份 CURRENT_TURN_ENVELOPE 重新输出完整严格 JSON；不得补写或猜测上一版输出。" +
		"\n每个非空 URef 恰好一条 utteranceCoverage；" +
		"sourceSpan.quote 必须是对应 utterance.text 的连续原文片段（0-based rune offset，end exclusive）；" +
		"ignored 只允许 duplicate_equivalent，且必须存在归一化后完全相同的 covered 输入。"
}

func runtimeIntentV3RepairAllowed(err error) bool {
	code, ok := strictjson.CodeOf(err)
	if !ok {
		return false
	}
	switch code {
	case strictjson.ErrorJSONRootNotObject, strictjson.ErrorJSONSyntaxInvalid,
		strictjson.ErrorJSONDuplicateKey, strictjson.ErrorJSONUnknownField,
		strictjson.ErrorJSONTrailingContent, strictjson.ErrorJSONSchemaInvalid,
		strictjson.ErrorJSONReferenceInvalid, strictjson.ErrorJSONBusinessInvariant:
		return true
	default:
		return false
	}
}

func logIntentV3ProtocolDiagnostic(err error, rawBytes, attempt int) {
	path, _ := strictjson.PathOf(err)
	if path == "" {
		path = "$"
	}
	slog.Warn("intent v3 protocol diagnostic",
		"stage", "intent", "contract", contracts.SchemaIntentTasksV3,
		"error_code", intentV3ProtocolErrorCode(err), "json_path", path,
		"raw_bytes", rawBytes, "attempt", attempt, "provider_status", 200)
}

// intentV3ProtocolErrorCode 从 strictjson/协议错误中提取确定性错误码。
func intentV3ProtocolErrorCode(err error) string {
	if code, ok := strictjson.CodeOf(err); ok {
		return code
	}
	return "unknown"
}
