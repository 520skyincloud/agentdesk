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
	SourceRef string
	Start     int
	End       int
	Quote     string
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
	QuestionKey            string // Q1..Qn，规范化产物
	Sequence               int
	PrimarySourceMessageID int64
	AnalysisRevision       int
	SourceSpans            []IntentSourceSpan
	DependsOnObs           []string
	Intent                 string
	SubIntent              string
	RequestMode            string
	Text                   string // 检索/表达用规范化文本（有限改写）
	Requirements           []RequirementSeed
	CanonicalQuestionHash  string
	Relation               string
}

// RequirementSeed 是模型建议的答案义务（契约 10.8），ID 与状态机由服务端负责。
type RequirementSeed struct {
	Kind     string
	Required bool
	Sequence int
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
		unit.Requirements = task.Requirements
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
