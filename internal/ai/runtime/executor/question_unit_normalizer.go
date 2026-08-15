package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"agent-desk/internal/ai/runtime/contextcompiler"
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
// 4. ObservationRef 属于当前 Turn；5. 多个 Task 不得拥有完全相同的 spans+intent/subIntent。
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
	return strings.Join([]string{task.Intent, task.SubIntent, task.RequestMode, strings.Join(parts, ",")}, "|")
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
	Status          string // accepted/repaired/degraded_single_task
}

// SuppressedUnit 是被去重抑制的模型任务。
type SuppressedUnit struct {
	Sequence             int
	ReasonCode           string
	CoveredByQuestionKey string
}

// NormalizeIntentTasks 是「多模态契约 12」的本地规范化：
// 校验 -> 构建 QuestionUnit -> 同源精确去重（相同 canonicalQuestionHash + intent/subIntent 只留一个）。
// Span 全部非法时降级为每个唯一非空 utterance 一个全文 QuestionUnit（degraded_single_task），
// 不复制多个 Task，不转人工。
func NormalizeIntentTasks(envelope contextcompiler.TurnInputEnvelope, tasks []IntentTaskV3) TaskNormalizationResult {
	result := TaskNormalizationResult{AcceptedUnits: make([]QuestionUnit, 0, len(tasks)), SuppressedUnits: []SuppressedUnit{}}

	utteranceByRef := make(map[string]contextcompiler.EnvelopeUtterance, len(envelope.Utterances))
	for _, utterance := range envelope.Utterances {
		utteranceByRef[utterance.Ref] = utterance
	}

	// 第一步：同身份（相同 spans + intent/subIntent/requestMode）先收敛为 same_source_duplicate。
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

// degradedSingleTaskResult 按「每个唯一非空文字/语音 utterance 一个全文 QuestionUnit」降级。
func degradedSingleTaskResult(envelope contextcompiler.TurnInputEnvelope) TaskNormalizationResult {
	result := TaskNormalizationResult{AcceptedUnits: []QuestionUnit{}, SuppressedUnits: []SuppressedUnit{}, Status: "degraded_single_task"}
	seq := 0
	for _, utterance := range envelope.Utterances {
		text := strings.TrimSpace(utterance.Text)
		if text == "" {
			continue
		}
		seq++
		span := IntentSourceSpan{SourceRef: utterance.Ref, Start: 0, End: len([]rune(text)), Quote: text}
		result.AcceptedUnits = append(result.AcceptedUnits, QuestionUnit{
			QuestionKey: envelopeRefQ(seq), Sequence: seq,
			PrimarySourceMessageID: utterance.MessageID,
			SourceSpans:            []IntentSourceSpan{span},
			Intent:                 "hotel_info", SubIntent: "store_knowledge",
			RequestMode: "answer", Text: text,
			CanonicalQuestionHash: CanonicalQuestionHash("hotel_info", "store_knowledge", []string{text}, "answer"),
			Relation:              "new_topic",
		})
	}
	return result
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
	text := strings.TrimSpace(task.NormalizedText)
	if text == "" && len(quotes) > 0 {
		text = strings.Join(quotes, " ")
	}
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

// CanonicalQuestionHash 按「多模态契约 11.2」计算：使用真实 source quotes，
// 不使用模型自由改写文本。规范化：NFKC 等价（小写英文/合并空格/去尾标点），保留数字与否定词。
func CanonicalQuestionHash(intent, subIntent string, exactSourceQuotes []string, requestMode string) string {
	joined := normalizeQuestionText(strings.Join(exactSourceQuotes, "\x1e"))
	parts := []string{strings.TrimSpace(intent), strings.TrimSpace(subIntent), joined, strings.TrimSpace(requestMode)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

func normalizeQuestionText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
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
