package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"agent-desk/internal/ai/runtime/contextcompiler"
	"golang.org/x/text/unicode/norm"
)

// 多模态契约 9-12：IntentTasksV3 来源片段校验、QuestionUnit 规范化与同源去重。
// 模型输出 sourceRefs + sourceSpans(quote)；服务端用 rune offset 精确证明
// task.text 来自真实来源子句，禁止把完整语音转写复制到多个 Task。

// IntentTaskV3 是 Intent 模型的语义任务（含来源引用）。
type IntentTaskV3 struct {
	Sequence       int
	Intent         string
	SubIntent      string
	SourceRefs     []string
	SourceSpans    []IntentSourceSpan
	DependsOnObs   []string
	NormalizedText string
	RequestMode    string
	Confidence     float64
	Requirements   []RequirementSeed
}

// IntentSourceSpan 是一个来源片段：sourceRef + 0-based rune offset（end exclusive）+ 原文 quote。
type IntentSourceSpan struct {
	SourceRef string `json:"sourceRef"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Quote     string `json:"-"`
}

// SourceSpanIssue 是来源校验失败项。
type SourceSpanIssue struct {
	Sequence int
	Code     string
	Message  string
}

// ValidateIntentTaskSources 校验 Intent 输出与 Envelope 的一致性（多模态契约 10）：
// 1. sourceRef 存在且属于当前 Turn；2. span 在 rune 范围内；3. quote 与原文切片完全一致；
// 4. ObservationRef 属于当前 Turn；5. 多个 Task 不得拥有完全相同的来源 spans。
func ValidateIntentTaskSources(envelope contextcompiler.TurnInputEnvelope, tasks []IntentTaskV3) []SourceSpanIssue {
	issues := make([]SourceSpanIssue, 0)
	utteranceByRef := make(map[string]contextcompiler.EnvelopeUtterance, len(envelope.Utterances))
	for _, utterance := range envelope.Utterances {
		utteranceByRef[utterance.Ref] = utterance
	}
	observationByRef := make(map[string]contextcompiler.EnvelopeObservation, len(envelope.Observations))
	for _, obs := range envelope.Observations {
		observationByRef[obs.Ref] = obs
	}
	seenIdentity := make(map[string]int, len(tasks))
	for _, task := range tasks {
		for _, span := range task.SourceSpans {
			utterance, ok := utteranceByRef[span.SourceRef]
			if !ok {
				issues = append(issues, SourceSpanIssue{Sequence: task.Sequence, Code: "intent_source_ref_invalid",
					Message: fmt.Sprintf("unknown sourceRef %s", span.SourceRef)})
				continue
			}
			runes := []rune(utterance.Text)
			if span.Start < 0 || span.End <= span.Start || span.End > len(runes) {
				issues = append(issues, SourceSpanIssue{Sequence: task.Sequence, Code: "intent_source_span_invalid",
					Message: fmt.Sprintf("span [%d,%d) out of range len=%d", span.Start, span.End, len(runes))})
				continue
			}
			if string(runes[span.Start:span.End]) != span.Quote {
				issues = append(issues, SourceSpanIssue{Sequence: task.Sequence, Code: "intent_source_span_invalid",
					Message: "quote does not match source text slice"})
			}
		}
		for _, ref := range task.DependsOnObs {
			if _, ok := observationByRef[ref]; !ok {
				issues = append(issues, SourceSpanIssue{Sequence: task.Sequence, Code: "intent_source_ref_invalid",
					Message: fmt.Sprintf("unknown observationRef %s", ref)})
			}
		}
		identity := taskSourceIdentity(task)
		if prev, exists := seenIdentity[identity]; exists {
			issues = append(issues, SourceSpanIssue{Sequence: task.Sequence, Code: "intent_duplicate_full_span",
				Message: fmt.Sprintf("task %d duplicates task %d source spans", task.Sequence, prev)})
		} else {
			seenIdentity[identity] = task.Sequence
		}
	}
	return issues
}

func taskSourceIdentity(task IntentTaskV3) string {
	parts := make([]string, 0, len(task.SourceSpans))
	for _, span := range task.SourceSpans {
		parts = append(parts, fmt.Sprintf("%s[%d:%d]", span.SourceRef, span.Start, span.End))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// QuestionUnit 是规范化后的独立问题（多模态契约 11）：带真实来源、canonical hash 和关系。
type QuestionUnit struct {
	QuestionKey            string             `json:"questionKey"` // Q1..Qn，规范化产物
	Sequence               int                `json:"sequence"`
	PrimarySourceMessageID int64              `json:"primarySourceMessageId"`
	AnalysisRevision       int                `json:"analysisRevision"`
	SourceSpans            []IntentSourceSpan `json:"sourceSpans"`
	DependsOnObs           []string           `json:"dependsOnObservationRefs"`
	Intent                 string             `json:"intent"`
	SubIntent              string             `json:"subIntent"`
	RequestMode            string             `json:"requestMode"`
	Text                   string             `json:"text"` // 检索/表达用规范化文本（有限改写）
	Requirements           []RequirementSeed  `json:"requirements,omitempty"`
	CanonicalQuestionHash  string             `json:"canonicalQuestionHash"`
	Relation               string             `json:"relation"`
	ParentTaskKey          string             `json:"parentTaskKey,omitempty"`
	ResolvedTopic          string             `json:"resolvedTopic,omitempty"`
	InheritedRequirements  []RequirementSeed  `json:"inheritedRequirements,omitempty"`
}

// RequirementSeed 是模型建议的答案义务（契约 10.8），ID 与状态机由服务端负责。
type RequirementSeed struct {
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
	Sequence int    `json:"sequence"`
}

// TaskNormalizationResult 记录规范化结果：接受的 QuestionUnit 与被抑制的重复项。
type TaskNormalizationResult struct {
	AcceptedUnits   []QuestionUnit
	SuppressedUnits []SuppressedUnit
	Status          string // accepted/repaired/degraded_clause_tasks
}

// SuppressedUnit 是被去重抑制的模型任务。
type SuppressedUnit struct {
	Sequence             int
	ReasonCode           string
	CoveredByQuestionKey string
}

// NormalizeIntentTasks 是「多模态契约 12」的本地规范化：
// 校验 -> 构建 QuestionUnit -> 同源精确去重。任务身份只由真实来源决定，
// 不允许模型在重试时通过改变 intent/subIntent/requestMode 创建第二个任务。
// Span 全部非法时按通用句末边界重建来源明确的澄清 QuestionUnit，
// 不复制全文到多个 Task，也不转人工。
func NormalizeIntentTasks(envelope contextcompiler.TurnInputEnvelope, tasks []IntentTaskV3) TaskNormalizationResult {
	return NormalizeIntentTasksWithDialogueAct(envelope, tasks, "")
}

// NormalizeIntentTasksWithDialogueAct keeps the V3 wire contract model-free:
// relation is supplied by the server from dialogueAct plus the durable task
// context, then projected onto QuestionUnit for downstream persistence.
func NormalizeIntentTasksWithDialogueAct(envelope contextcompiler.TurnInputEnvelope, tasks []IntentTaskV3, dialogueAct string) TaskNormalizationResult {
	result := TaskNormalizationResult{AcceptedUnits: make([]QuestionUnit, 0, len(tasks)), SuppressedUnits: []SuppressedUnit{}}

	utteranceByRef := make(map[string]contextcompiler.EnvelopeUtterance, len(envelope.Utterances))
	for _, utterance := range envelope.Utterances {
		utteranceByRef[utterance.Ref] = utterance
	}

	// 第一步：相同来源 spans 先收敛为 same_source_duplicate。
	// 这是文档 10.5 与 12.3 的同源重复：即使 Span 本身合法，也只允许一个 Task 存活。
	seenIdentity := make(map[string]IntentTaskV3, len(tasks))
	deduped := make([]IntentTaskV3, 0, len(tasks))
	for _, task := range tasks {
		identity := taskSourceIdentity(task)
		if keeper, exists := seenIdentity[identity]; exists {
			result.SuppressedUnits = append(result.SuppressedUnits, SuppressedUnit{
				Sequence: task.Sequence, ReasonCode: "same_source_duplicate",
			})
			_ = keeper
			continue
		}
		seenIdentity[identity] = task
		deduped = append(deduped, task)
	}

	// 第二步：Span 校验。全部非法时降级为全文单 QuestionUnit；部分非法剔除后继续。
	issues := ValidateIntentTaskSources(envelope, deduped)
	valid := len(issues) == 0
	if !valid {
		badSeq := make(map[int]struct{}, len(issues))
		for _, issue := range issues {
			badSeq[issue.Sequence] = struct{}{}
		}
		kept := make([]IntentTaskV3, 0, len(deduped))
		for _, task := range deduped {
			if _, bad := badSeq[task.Sequence]; !bad {
				kept = append(kept, task)
			}
		}
		if len(kept) == 0 && len(deduped) > 0 {
			degraded := degradedSingleTaskResult(envelope)
			degraded.SuppressedUnits = append(degraded.SuppressedUnits, result.SuppressedUnits...)
			return degraded
		}
		deduped = kept
	}
	if valid {
		result.Status = "accepted"
	} else {
		result.Status = "repaired"
	}

	// 第三步：构建 QuestionUnit 并按 canonicalQuestionHash 二次去重（改写文本不同的同义重复）。
	seenHash := make(map[string]string, len(deduped))
	for index, task := range deduped {
		unit := buildQuestionUnit(index+1, task, utteranceByRef)
		unit.Requirements = cloneRequirementSeeds(task.Requirements)
		applyQuestionUnitRelation(&unit, envelope, dialogueAct, result.AcceptedUnits)
		if covered, exists := seenHash[unit.CanonicalQuestionHash]; exists {
			result.SuppressedUnits = append(result.SuppressedUnits, SuppressedUnit{
				Sequence: task.Sequence, ReasonCode: "same_source_duplicate", CoveredByQuestionKey: covered,
			})
			continue
		}
		seenHash[unit.CanonicalQuestionHash] = unit.QuestionKey
		result.AcceptedUnits = append(result.AcceptedUnits, unit)
	}
	return result
}

func applyQuestionUnitRelation(unit *QuestionUnit, envelope contextcompiler.TurnInputEnvelope, dialogueAct string, priorUnits []QuestionUnit) {
	if unit == nil {
		return
	}
	unit.Relation = normalizedQuestionRelation(dialogueAct, unit.RequestMode)
	unit.ResolvedTopic = questionUnitTopic(*unit)

	if unit.Relation == "new_topic" {
		// Intent models occasionally classify an elliptical follow-up as a new
		// topic (for example a predicate-only question with no explicit subject).
		// Override that label only when the sentence is structurally dependent on
		// context and exactly one same-scope parent exists. This is deliberately
		// grammar-based rather than a hotel keyword whitelist.
		if !looksContextDependentQuestionUnit(*unit) {
			return
		}
		if parent, ok := findContextualTaskParent(*unit, envelope.UnresolvedTasks); ok {
			unit.Relation = "follow_up"
			bindQuestionUnitToEnvelopeParent(unit, parent)
			return
		}
		if parent, ok := findContextualPriorUnit(*unit, priorUnits); ok {
			unit.Relation = "follow_up"
			bindQuestionUnitToPriorUnit(unit, parent)
		}
		return
	}

	if parent, ok := findUnresolvedTaskParent(*unit, envelope.UnresolvedTasks); ok {
		bindQuestionUnitToEnvelopeParent(unit, parent)
		return
	}
	if unit.RequestMode == "clarify_previous" {
		unit.ResolvedTopic = ""
		unit.InheritedRequirements = nil
	}

	// A same-turn short follow-up may refer to the immediately preceding unit.
	// Only a single compatible candidate is accepted; ambiguity remains an
	// explicit clarification instead of inheriting an arbitrary topic.
	if isShortQuestionUnit(*unit) {
		candidates := make([]QuestionUnit, 0, 2)
		for _, prior := range priorUnits {
			if questionUnitsCompatible(*unit, prior) {
				candidates = append(candidates, prior)
			}
		}
		if len(candidates) == 1 {
			bindQuestionUnitToPriorUnit(unit, candidates[0])
			return
		}
		if len(candidates) > 1 {
			unit.ParentTaskKey = ""
			unit.ResolvedTopic = ""
			unit.InheritedRequirements = nil
		}
	}
}

func bindQuestionUnitToEnvelopeParent(unit *QuestionUnit, parent contextcompiler.EnvelopeUnresolvedTask) {
	unit.ParentTaskKey = strings.TrimSpace(parent.TaskKey)
	unit.ResolvedTopic = firstNonEmptyQuestionTopic(parent.ResolvedTopic, parent.QuestionText, parent.SubIntent, parent.Intent)
	unit.InheritedRequirements = cloneEnvelopeRequirements(parent.Requirements)
}

func bindQuestionUnitToPriorUnit(unit *QuestionUnit, parent QuestionUnit) {
	unit.ParentTaskKey = parent.QuestionKey
	unit.ResolvedTopic = firstNonEmptyQuestionTopic(parent.ResolvedTopic, parent.Text, parent.SubIntent, parent.Intent)
	unit.InheritedRequirements = cloneRequirementSeeds(parent.Requirements)
}

func normalizedQuestionRelation(dialogueAct, requestMode string) string {
	switch strings.TrimSpace(requestMode) {
	case "correct_previous":
		return "correction"
	case "confirm_previous":
		return "confirmation"
	case "cancel_previous":
		return "cancellation"
	}
	switch strings.TrimSpace(dialogueAct) {
	case "follow_up", "repeat", "refinement", "correction", "confirmation", "cancellation":
		return strings.TrimSpace(dialogueAct)
	default:
		return "new_topic"
	}
}

func findUnresolvedTaskParent(unit QuestionUnit, tasks []contextcompiler.EnvelopeUnresolvedTask) (contextcompiler.EnvelopeUnresolvedTask, bool) {
	candidates := make([]contextcompiler.EnvelopeUnresolvedTask, 0, 2)
	for _, task := range tasks {
		if !isUnresolvedTaskActive(task.Status) || !questionTaskCompatible(unit, task) {
			continue
		}
		if unit.Relation == "repeat" && !questionTaskRepeats(unit, task) {
			continue
		}
		candidates = append(candidates, task)
	}
	if len(candidates) != 1 {
		return contextcompiler.EnvelopeUnresolvedTask{}, false
	}
	return candidates[0], true
}

func findContextualTaskParent(unit QuestionUnit, tasks []contextcompiler.EnvelopeUnresolvedTask) (contextcompiler.EnvelopeUnresolvedTask, bool) {
	candidates := make([]contextcompiler.EnvelopeUnresolvedTask, 0, 2)
	for _, task := range tasks {
		if !isUnresolvedTaskActive(task.Status) || !questionTaskBroadlyCompatible(unit, task) {
			continue
		}
		candidates = append(candidates, task)
	}
	if len(candidates) == 0 {
		return contextcompiler.EnvelopeUnresolvedTask{}, false
	}
	// Prefer the nearest preceding source event. If two candidate tasks came
	// from the same source event and have the same order, the relation is
	// genuinely ambiguous and must remain unbound.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].SourceMessageID != candidates[j].SourceMessageID {
			return candidates[i].SourceMessageID > candidates[j].SourceMessageID
		}
		return candidates[i].SequenceNo > candidates[j].SequenceNo
	})
	if len(candidates) > 1 && candidates[0].SourceMessageID > 0 &&
		candidates[0].SourceMessageID == candidates[1].SourceMessageID {
		return contextcompiler.EnvelopeUnresolvedTask{}, false
	}
	return candidates[0], true
}

func findContextualPriorUnit(unit QuestionUnit, priorUnits []QuestionUnit) (QuestionUnit, bool) {
	candidates := make([]QuestionUnit, 0, 2)
	for _, prior := range priorUnits {
		if questionUnitsBroadlyCompatible(unit, prior) {
			candidates = append(candidates, prior)
		}
	}
	if len(candidates) != 1 {
		return QuestionUnit{}, false
	}
	return candidates[0], true
}

func isUnresolvedTaskActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "delivered", "answered", "resolved", "cancelled", "canceled", "closed", "superseded", "failed_terminal", "skipped":
		return false
	default:
		return true
	}
}

func questionTaskCompatible(unit QuestionUnit, task contextcompiler.EnvelopeUnresolvedTask) bool {
	if strings.TrimSpace(unit.Intent) != "" && strings.TrimSpace(task.Intent) != "" &&
		strings.TrimSpace(unit.Intent) != strings.TrimSpace(task.Intent) {
		return unit.RequestMode == "clarify_previous"
	}
	if strings.TrimSpace(unit.SubIntent) != "" && strings.TrimSpace(task.SubIntent) != "" &&
		strings.TrimSpace(unit.SubIntent) != strings.TrimSpace(task.SubIntent) {
		return unit.RequestMode == "clarify_previous"
	}
	return true
}

func questionTaskBroadlyCompatible(unit QuestionUnit, task contextcompiler.EnvelopeUnresolvedTask) bool {
	unitIntent := strings.TrimSpace(unit.Intent)
	taskIntent := strings.TrimSpace(task.Intent)
	if unitIntent == "interaction" && strings.TrimSpace(unit.SubIntent) == "clarify" {
		return true
	}
	return unitIntent != "" && taskIntent != "" && unitIntent == taskIntent
}

func questionTaskRepeats(unit QuestionUnit, task contextcompiler.EnvelopeUnresolvedTask) bool {
	if hash := strings.TrimSpace(task.CanonicalQuestionHash); hash != "" {
		return hash == unit.CanonicalQuestionHash
	}
	text := strings.TrimSpace(task.QuestionText)
	return text != "" && normalizeQuestionText(text) == normalizeQuestionText(unit.Text)
}

func isShortQuestionUnit(unit QuestionUnit) bool {
	text := strings.TrimSpace(unit.Text)
	return text != "" && len([]rune(normalizeQuestionText(text))) <= 24
}

func looksContextDependentQuestionUnit(unit QuestionUnit) bool {
	if !isShortQuestionUnit(unit) {
		return false
	}
	switch strings.TrimSpace(unit.RequestMode) {
	case "clarify_previous", "correct_previous", "confirm_previous", "cancel_previous":
		return true
	}
	text := strings.ToLower(strings.TrimSpace(norm.NFKC.String(unit.Text)))
	text = strings.TrimLeftFunc(text, unicode.IsPunct)
	if text == "" {
		return false
	}
	// These are language-level interrogative, deictic and predicate openings.
	// They signal a missing subject without encoding any hotel/business topic.
	prefixes := []string{
		"怎么", "如何", "哪里", "哪儿", "哪边", "在哪", "几点", "多久", "多少", "什么时候",
		"能否", "能不能", "可以", "可不可以", "是否", "要不要", "有没有", "还有", "然后",
		"那", "这个", "那个", "它", "送到", "放到", "放哪", "给谁", "找谁",
		"where ", "when ", "how ", "can ", "could ", "does ", "do ", "is it", "what about", "and ", "then ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func questionUnitsCompatible(current, prior QuestionUnit) bool {
	if current.RequestMode == "clarify_previous" {
		return true
	}
	return strings.TrimSpace(current.Intent) == strings.TrimSpace(prior.Intent) &&
		strings.TrimSpace(current.SubIntent) == strings.TrimSpace(prior.SubIntent)
}

func questionUnitsBroadlyCompatible(current, prior QuestionUnit) bool {
	if current.Intent == "interaction" && current.SubIntent == "clarify" {
		return true
	}
	return strings.TrimSpace(current.Intent) != "" && strings.TrimSpace(current.Intent) == strings.TrimSpace(prior.Intent)
}

func questionUnitTopic(unit QuestionUnit) string {
	if strings.TrimSpace(unit.Intent) == "interaction" {
		return strings.TrimSpace(unit.SubIntent)
	}
	return firstNonEmptyQuestionTopic(unit.SubIntent, unit.Intent)
}

func firstNonEmptyQuestionTopic(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cloneRequirementSeeds(values []RequirementSeed) []RequirementSeed {
	if len(values) == 0 {
		return nil
	}
	return append([]RequirementSeed(nil), values...)
}

func cloneEnvelopeRequirements(values []contextcompiler.EnvelopeRequirement) []RequirementSeed {
	if len(values) == 0 {
		return nil
	}
	ret := make([]RequirementSeed, 0, len(values))
	for _, value := range values {
		ret = append(ret, RequirementSeed{Kind: value.Kind, Required: value.Required, Sequence: value.Sequence})
	}
	return ret
}

// degradedSingleTaskResult 保留旧函数名以减少调用面变更；实际按通用句末边界
// 生成独立 QuestionUnit。来源协议失效时只允许澄清，不得猜成酒店知识。
func degradedSingleTaskResult(envelope contextcompiler.TurnInputEnvelope) TaskNormalizationResult {
	result := TaskNormalizationResult{AcceptedUnits: []QuestionUnit{}, SuppressedUnits: []SuppressedUnit{}, Status: "degraded_clause_tasks"}
	seq := 0
	nonEmptyRemaining := countNonEmptyEnvelopeUtterances(envelope.Utterances)
	for _, utterance := range envelope.Utterances {
		text := strings.TrimSpace(utterance.Text)
		if text == "" {
			continue
		}
		maxClauses := 12 - seq - (nonEmptyRemaining - 1)
		if maxClauses < 1 {
			maxClauses = 1
		}
		for _, clause := range splitFallbackUtteranceClauses(utterance.Text, maxClauses) {
			seq++
			span := IntentSourceSpan{SourceRef: utterance.Ref, Start: clause.Start, End: clause.End, Quote: clause.Quote}
			result.AcceptedUnits = append(result.AcceptedUnits, QuestionUnit{
				QuestionKey: envelopeRefQ(seq), Sequence: seq,
				PrimarySourceMessageID: utterance.MessageID,
				SourceSpans:            []IntentSourceSpan{span},
				Intent:                 "interaction", SubIntent: "clarify",
				RequestMode: "clarify_previous", Text: clause.Quote,
				Requirements:          []RequirementSeed{{Sequence: 1, Kind: "clarification", Required: true}},
				CanonicalQuestionHash: CanonicalQuestionHash("interaction", "clarify", []string{clause.Quote}, "clarify_previous"),
				Relation:              "new_topic",
				ResolvedTopic:         "clarify",
			})
		}
		nonEmptyRemaining--
	}
	return result
}

type fallbackClauseSpan struct {
	Start int
	End   int
	Quote string
}

// splitFallbackUtteranceClauses is domain-agnostic. It uses punctuation and
// newlines that commonly separate independent requests, preserves exact rune
// offsets, and merges overflow into the final span so intent_tasks.v3 stays
// within its 12-task limit.
func splitFallbackUtteranceClauses(text string, maxClauses int) []fallbackClauseSpan {
	runes := []rune(text)
	if maxClauses <= 0 {
		maxClauses = 1
	}
	spans := make([]fallbackClauseSpan, 0, min(maxClauses, 4))
	start := 0
	flush := func(end int) {
		trimmedStart, trimmedEnd := start, end
		for trimmedStart < trimmedEnd && unicode.IsSpace(runes[trimmedStart]) {
			trimmedStart++
		}
		for trimmedEnd > trimmedStart && unicode.IsSpace(runes[trimmedEnd-1]) {
			trimmedEnd--
		}
		if trimmedEnd > trimmedStart {
			spans = append(spans, fallbackClauseSpan{
				Start: trimmedStart, End: trimmedEnd, Quote: string(runes[trimmedStart:trimmedEnd]),
			})
		}
		start = end
	}
	for index := 0; index < len(runes); index++ {
		if !isFallbackClauseTerminal(runes[index]) {
			continue
		}
		end := index + 1
		for end < len(runes) && isFallbackClauseTerminal(runes[end]) {
			end++
		}
		flush(end)
		index = end - 1
	}
	flush(len(runes))
	if len(spans) <= maxClauses {
		return spans
	}
	mergedStart := spans[maxClauses-1].Start
	mergedEnd := spans[len(spans)-1].End
	ret := append([]fallbackClauseSpan(nil), spans[:maxClauses-1]...)
	ret = append(ret, fallbackClauseSpan{Start: mergedStart, End: mergedEnd, Quote: string(runes[mergedStart:mergedEnd])})
	return ret
}

func isFallbackClauseTerminal(value rune) bool {
	switch value {
	case '。', '！', '!', '？', '?', '；', ';', '，', ',', '\n', '\r', '…':
		return true
	default:
		return false
	}
}

func countNonEmptyEnvelopeUtterances(utterances []contextcompiler.EnvelopeUtterance) int {
	count := 0
	for _, utterance := range utterances {
		if strings.TrimSpace(utterance.Text) != "" {
			count++
		}
	}
	return count
}

func buildQuestionUnit(seq int, task IntentTaskV3, utteranceByRef map[string]contextcompiler.EnvelopeUtterance) QuestionUnit {
	primaryMessageID := int64(0)
	quotes := make([]string, 0, len(task.SourceSpans))
	for _, span := range task.SourceSpans {
		if utterance, ok := utteranceByRef[span.SourceRef]; ok && primaryMessageID == 0 {
			primaryMessageID = utterance.MessageID
		}
		quotes = append(quotes, span.Quote)
	}
	// The model-provided normalizedText is descriptive metadata only. Retrieval
	// and reply objectives must be derived from the exact, server-verified source
	// quotes so a model rewrite cannot merge topics or inject a new question.
	text := strings.TrimSpace(strings.Join(quotes, " "))
	return QuestionUnit{
		QuestionKey: envelopeRefQ(seq), Sequence: seq,
		PrimarySourceMessageID: primaryMessageID,
		SourceSpans:            task.SourceSpans, DependsOnObs: task.DependsOnObs,
		Intent: task.Intent, SubIntent: task.SubIntent, RequestMode: task.RequestMode,
		Text:                  text,
		CanonicalQuestionHash: CanonicalQuestionHash(task.Intent, task.SubIntent, quotes, task.RequestMode),
		Relation:              "new_topic",
		ResolvedTopic:         firstNonEmptyQuestionTopic(task.SubIntent, task.Intent),
	}
}

func envelopeRefQ(seq int) string {
	return fmt.Sprintf("Q%d", seq)
}

// CanonicalQuestionHash 按「多模态契约 11.2」计算：只使用真实 source quotes，
// 不使用模型自由改写文本或模型判断出的 intent/subIntent/requestMode。
// 规范化：NFKC 等价（小写英文/合并空格/去尾标点），保留数字与否定词。
func CanonicalQuestionHash(_ string, _ string, exactSourceQuotes []string, _ string) string {
	joined := normalizeQuestionText(strings.Join(exactSourceQuotes, "\x1e"))
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:])
}

func normalizeQuestionText(text string) string {
	text = norm.NFKC.String(strings.ToLower(strings.TrimSpace(text)))
	var b strings.Builder
	lastSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		lastSpace = false
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	return strings.TrimRight(out, "。！？.!?,，；;：:")
}
