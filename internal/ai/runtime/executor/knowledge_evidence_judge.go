package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/ai"
	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/services"
)

const (
	knowledgeEvidenceJudgeSchemaVersion = "knowledge_evidence_judge.v2"

	knowledgeEvidenceDecisionDirectSingle    = "direct_single"
	knowledgeEvidenceDecisionDirectCombined  = "direct_combined"
	knowledgeEvidenceDecisionPartial         = "partial"
	knowledgeEvidenceDecisionInsufficient    = "insufficient"
	knowledgeEvidenceDecisionProtocolInvalid = "protocol_invalid"
	knowledgeEvidenceDecisionTimeout         = "timeout"
	knowledgeEvidenceDecisionMalformed       = "malformed"

	knowledgeEvidenceLayerStore   = "store"
	knowledgeEvidenceLayerGeneral = "general"

	knowledgeEvidenceDirectFAQMinimumScore   = float32(0.85)
	knowledgeEvidenceJudgeReviewMinimumScore = float32(0.70)
	knowledgeEvidenceStoreSupplyRescueScore  = float32(0.70)

	knowledgeEvidenceJudgeMinTimeout      = 10 * time.Second
	knowledgeEvidenceJudgeMaxTimeout      = 28 * time.Second
	knowledgeEvidenceJudgeDeadlineReserve = 12 * time.Second
	knowledgeEvidenceJudgeMaxOutputTokens = 4096
)

type knowledgeEvidenceJudge interface {
	JudgeBatch(ctx context.Context, req RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome
}

type knowledgeEvidenceJudgeTask struct {
	TaskID         string
	Intent         string
	Query          string
	RetrievalQuery string
	SubIntent      string
	Objective      string
	Entities       []knowledgeEvidenceJudgeEntity
	SourceContext  []knowledgeEvidenceJudgeSourceMessage
	Candidates     []knowledgeEvidenceJudgeCandidate
	RawCandidates  []knowledgeEvidenceJudgeCandidate
}

type knowledgeEvidenceJudgeEntity struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

type knowledgeEvidenceJudgeSourceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type knowledgeEvidenceJudgeCandidate struct {
	CandidateID string
	Layer       string
	Hit         rag.RetrieveResult
}

type knowledgeEvidenceJudgeOutcome struct {
	Applied    bool
	Selections map[string]map[string]knowledgeEvidenceLayerSelection
	Trace      callbacks.KnowledgeEvidenceJudgeTraceData
}

type knowledgeEvidenceLayerSelection struct {
	Decision             string
	DecisionSource       string
	SelectedCandidateIDs []string
	SupportedFacts       []knowledgeEvidenceFact
	MissingAspects       []string
}

type knowledgeEvidenceFact struct {
	FactID         string   `json:"factId"`
	Aspect         string   `json:"aspect"`
	Statement      string   `json:"statement"`
	CriticalValues []string `json:"criticalValues"`
}

type modelKnowledgeEvidenceJudge struct{}

type knowledgeEvidenceJudgePrompt struct {
	SchemaVersion string                             `json:"schemaVersion"`
	Tasks         []knowledgeEvidenceJudgePromptTask `json:"tasks"`
}

type knowledgeEvidenceJudgePromptTask struct {
	TaskID        string                                  `json:"taskId"`
	Intent        string                                  `json:"intent,omitempty"`
	Question      string                                  `json:"question"`
	SubIntent     string                                  `json:"subIntent,omitempty"`
	Objective     string                                  `json:"objective,omitempty"`
	Entities      []knowledgeEvidenceJudgeEntity          `json:"entities,omitempty"`
	SourceContext []knowledgeEvidenceJudgeSourceMessage   `json:"sourceContext,omitempty"`
	Candidates    []knowledgeEvidenceJudgePromptCandidate `json:"candidates"`
}

type knowledgeEvidenceJudgePromptCandidate struct {
	CandidateID string  `json:"candidateId"`
	Layer       string  `json:"layer"`
	FAQQuestion string  `json:"faqQuestion,omitempty"`
	FAQAnswer   string  `json:"faqAnswer,omitempty"`
	Title       string  `json:"title,omitempty"`
	RawContent  string  `json:"rawContent"`
	Score       float32 `json:"score"`
}

type knowledgeEvidenceJudgeResponse struct {
	SchemaVersion string                               `json:"schemaVersion"`
	Tasks         []knowledgeEvidenceJudgeResponseTask `json:"tasks"`
}

type knowledgeEvidenceJudgeResponseTask struct {
	TaskID string                                `json:"taskId"`
	Layers []knowledgeEvidenceJudgeResponseLayer `json:"layers"`
}

type knowledgeEvidenceJudgeResponseLayer struct {
	Layer                string                  `json:"layer"`
	Decision             string                  `json:"decision"`
	SelectedCandidateIDs []string                `json:"selectedCandidateIds"`
	SupportedFacts       []knowledgeEvidenceFact `json:"supportedFacts"`
	MissingAspects       []string                `json:"missingAspects"`
}

type knowledgeEvidenceJudgeRawResponse struct {
	SchemaVersion string                                  `json:"schemaVersion"`
	Tasks         []knowledgeEvidenceJudgeRawResponseTask `json:"tasks"`
}

type knowledgeEvidenceJudgeRawResponseTask struct {
	TaskID string                                   `json:"taskId"`
	Layers []knowledgeEvidenceJudgeRawResponseLayer `json:"layers"`
}

type knowledgeEvidenceJudgeRawResponseLayer struct {
	Layer                string          `json:"layer"`
	Decision             string          `json:"decision"`
	SelectedCandidateIDs []string        `json:"selectedCandidateIds"`
	SupportedFacts       json.RawMessage `json:"supportedFacts"`
	MissingAspects       json.RawMessage `json:"missingAspects"`
}

type knowledgeEvidenceJudgeParseError struct {
	decision string
	err      error
}

func (e *knowledgeEvidenceJudgeParseError) Error() string {
	if e == nil || e.err == nil {
		return "knowledge judge response is invalid"
	}
	return e.err.Error()
}

func (e *knowledgeEvidenceJudgeParseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func knowledgeEvidenceJudgeResponseError(decision string, err error) error {
	return &knowledgeEvidenceJudgeParseError{decision: decision, err: err}
}

func knowledgeEvidenceJudgeParseFailureDecision(err error) string {
	var parseErr *knowledgeEvidenceJudgeParseError
	if errors.As(err, &parseErr) && parseErr != nil {
		switch parseErr.decision {
		case knowledgeEvidenceDecisionProtocolInvalid, knowledgeEvidenceDecisionMalformed:
			return parseErr.decision
		}
	}
	return knowledgeEvidenceDecisionMalformed
}

func (modelKnowledgeEvidenceJudge) JudgeBatch(ctx context.Context, req RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome {
	prompt := buildKnowledgeEvidenceJudgePrompt(tasks)
	fingerprint := fingerprintKnowledgeEvidenceJudgePrompt(prompt)
	trace := callbacks.KnowledgeEvidenceJudgeTraceData{
		SchemaVersion:        knowledgeEvidenceJudgeSchemaVersion,
		Status:               "fallback",
		Reason:               "judge was not completed; unselected retrieval will be withheld and existing handoff routing will be used",
		CandidateFingerprint: fingerprint,
		TaskCount:            len(prompt.Tasks),
		CandidateCount:       countKnowledgeEvidenceJudgeCandidates(prompt),
	}
	if len(prompt.Tasks) == 0 {
		trace.Status = "skipped"
		trace.Reason = "no retrieved candidate required evidence judging"
		return knowledgeEvidenceJudgeOutcome{Trace: trace}
	}

	resolved, err := services.StoreAIModelSettingService.ResolveForConversation(req.Conversation.ID, services.StoreAIModelUsageKnowledgeJudgeLLM, 0)
	if err != nil || resolved == nil {
		trace.Status = knowledgeEvidenceDecisionMalformed
		trace.Reason = "knowledge judge model is unavailable; retrieval remains intact and the judge protocol must be retried"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(err)
		return failedKnowledgeEvidenceJudgeOutcome(tasks, trace, knowledgeEvidenceDecisionMalformed)
	}
	config := normalizeKnowledgeEvidenceJudgeConfig(resolved.Config, len(prompt.Tasks), trace.CandidateCount)
	configuredJudgeTimeout := time.Duration(config.TimeoutMS) * time.Millisecond
	effectiveJudgeTimeout, deadlineAvailable := knowledgeEvidenceJudgeTimeoutWithinParent(ctx, configuredJudgeTimeout)
	if !deadlineAvailable {
		trace.Status = knowledgeEvidenceDecisionTimeout
		trace.Reason = "knowledge judge was skipped because the parent reply deadline has no remaining stage budget"
		return failedKnowledgeEvidenceJudgeOutcome(tasks, trace, knowledgeEvidenceDecisionTimeout)
	}
	judgeDeadlineTrimmed := effectiveJudgeTimeout < configuredJudgeTimeout
	config.TimeoutMS = int(effectiveJudgeTimeout / time.Millisecond)
	trace.Model = config.ModelName
	if strings.TrimSpace(config.ModelName) == "" || strings.TrimSpace(string(config.Provider)) == "" {
		trace.Status = knowledgeEvidenceDecisionMalformed
		trace.Reason = "knowledge judge model configuration is incomplete; retrieval remains intact and the judge protocol must be retried"
		return failedKnowledgeEvidenceJudgeOutcome(tasks, trace, knowledgeEvidenceDecisionMalformed)
	}

	systemPrompt := knowledgeEvidenceJudgeSystemPrompt()
	userPrompt, err := json.Marshal(prompt)
	if err != nil {
		trace.Status = knowledgeEvidenceDecisionMalformed
		trace.Reason = "knowledge judge prompt could not be encoded; retrieval remains intact and the judge protocol must be retried"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(err)
		return failedKnowledgeEvidenceJudgeOutcome(tasks, trace, knowledgeEvidenceDecisionMalformed)
	}

	callCtx := knowledgeEvidenceJudgeUsageContext(ctx, req, resolved)
	callCtx, capture := usagex.WithCapture(callCtx)
	callCtx, cancel := context.WithTimeout(callCtx, time.Duration(config.TimeoutMS)*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	result, callErr := ai.LLM.ChatWithConfig(callCtx, config, systemPrompt, string(userPrompt))
	trace.LatencyMs = time.Since(startedAt).Milliseconds()
	recordKnowledgeEvidenceJudgeUsage(callCtx, req, config, result, lastKnowledgeEvidenceJudgeReceipt(capture), fingerprint, trace.LatencyMs, callErr)
	if callErr != nil {
		failureDecision := knowledgeEvidenceDecisionMalformed
		if errors.Is(callErr, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			failureDecision = knowledgeEvidenceDecisionTimeout
		}
		trace.Status = failureDecision
		trace.Reason = "knowledge judge model call failed; retrieval remains intact and the judge protocol must be retried"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(callErr)
		return failedKnowledgeEvidenceJudgeOutcome(tasks, trace, failureDecision)
	}

	selections, parseErr := parseKnowledgeEvidenceJudgeRuntimeResponse(result.Content, tasks)
	if parseErr != nil {
		failureDecision := knowledgeEvidenceJudgeParseFailureDecision(parseErr)
		trace.Status = failureDecision
		trace.Reason = "knowledge judge returned an invalid protocol response; retrieval remains intact and the judge protocol must be retried"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(parseErr)
		return failedKnowledgeEvidenceJudgeOutcome(tasks, trace, failureDecision)
	}
	trace.Status = "completed"
	trace.Reason = "knowledge evidence was selected once per task and layer before deterministic store priority"
	if judgeDeadlineTrimmed {
		trace.Reason += "; judge timeout was bounded by the parent reply deadline"
	}
	return knowledgeEvidenceJudgeOutcome{
		Applied:    true,
		Selections: selections,
		Trace:      trace,
	}
}

func failedKnowledgeEvidenceJudgeOutcome(tasks []knowledgeEvidenceJudgeTask, trace callbacks.KnowledgeEvidenceJudgeTraceData, decision string) knowledgeEvidenceJudgeOutcome {
	selections := failedKnowledgeEvidenceLayerSelections(tasks, decision)
	repaired := repairExactFAQFallbackSelections(tasks, selections)
	if repaired > 0 {
		trace.Reason = strings.TrimSpace(trace.Reason + fmt.Sprintf("; recovered %d strict exact-FAQ selection(s)", repaired))
	}
	return knowledgeEvidenceJudgeOutcome{
		Applied:    true,
		Selections: selections,
		Trace:      trace,
	}
}

func failedKnowledgeEvidenceLayerSelections(tasks []knowledgeEvidenceJudgeTask, decision string) map[string]map[string]knowledgeEvidenceLayerSelection {
	selections := make(map[string]map[string]knowledgeEvidenceLayerSelection, len(tasks))
	for _, task := range tasks {
		taskSelections := make(map[string]knowledgeEvidenceLayerSelection, 2)
		for _, candidate := range task.Candidates {
			layer := strings.TrimSpace(candidate.Layer)
			if layer != knowledgeEvidenceLayerStore && layer != knowledgeEvidenceLayerGeneral {
				continue
			}
			taskSelections[layer] = knowledgeEvidenceLayerSelection{Decision: decision, DecisionSource: decision}
		}
		selections[strings.TrimSpace(task.TaskID)] = taskSelections
	}
	return selections
}

func knowledgeEvidenceExpectedCandidatesByLayer(task knowledgeEvidenceJudgeTask) map[string]map[string]struct{} {
	expected := make(map[string]map[string]struct{}, 2)
	for _, candidate := range task.Candidates {
		layer := strings.TrimSpace(candidate.Layer)
		if layer != knowledgeEvidenceLayerStore && layer != knowledgeEvidenceLayerGeneral {
			continue
		}
		if expected[layer] == nil {
			expected[layer] = make(map[string]struct{})
		}
		expected[layer][strings.TrimSpace(candidate.CandidateID)] = struct{}{}
	}
	return expected
}

func buildKnowledgeEvidenceJudgePrompt(tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgePrompt {
	prompt := knowledgeEvidenceJudgePrompt{SchemaVersion: knowledgeEvidenceJudgeSchemaVersion}
	prompt.Tasks = make([]knowledgeEvidenceJudgePromptTask, 0, len(tasks))
	for _, task := range tasks {
		item := knowledgeEvidenceJudgePromptTask{
			TaskID:        strings.TrimSpace(task.TaskID),
			Intent:        canonicalIntentCode(task.Intent),
			Question:      strings.TrimSpace(task.Query),
			SubIntent:     strings.TrimSpace(task.SubIntent),
			Objective:     strings.TrimSpace(task.Objective),
			Entities:      append([]knowledgeEvidenceJudgeEntity(nil), task.Entities...),
			SourceContext: append([]knowledgeEvidenceJudgeSourceMessage(nil), task.SourceContext...),
		}
		item.Candidates = make([]knowledgeEvidenceJudgePromptCandidate, 0, len(task.Candidates))
		for _, candidate := range task.Candidates {
			title := strings.TrimSpace(candidate.Hit.Title)
			if title == "" {
				title = strings.TrimSpace(candidate.Hit.DocumentTitle)
			}
			faqQuestion, faqAnswer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
			rawContent := strings.TrimSpace(candidate.Hit.Content)
			if faqQuestion != "" && faqAnswer != "" {
				rawContent = "问题：" + faqQuestion + "\n答案：" + faqAnswer
			}
			item.Candidates = append(item.Candidates, knowledgeEvidenceJudgePromptCandidate{
				CandidateID: strings.TrimSpace(candidate.CandidateID),
				Layer:       strings.TrimSpace(candidate.Layer),
				FAQQuestion: faqQuestion,
				FAQAnswer:   faqAnswer,
				Title:       title,
				RawContent:  rawContent,
				Score:       candidate.Hit.Score,
			})
		}
		prompt.Tasks = append(prompt.Tasks, item)
	}
	return prompt
}

func splitKnowledgeEvidenceFAQ(hit rag.RetrieveResult) (string, string) {
	return splitKnowledgeEvidenceFAQForQuery(hit, strings.TrimSpace(hit.FaqQuestion))
}

type knowledgeEvidenceFAQUnit struct {
	Question string
	Answer   string
}

func splitKnowledgeEvidenceFAQForQuery(hit rag.RetrieveResult, query string) (string, string) {
	raw := strings.TrimSpace(hit.Content)
	units := parseKnowledgeEvidenceFAQUnits(raw)
	if len(units) > 0 {
		if preferred := strings.TrimSpace(hit.FaqQuestion); preferred != "" {
			if index := bestKnowledgeEvidenceFAQUnitIndex(units, preferred); index >= 0 && knowledgeEvidenceFAQQuestionMatchScore(units[index].Question, preferred) >= 0.82 {
				return units[index].Question, units[index].Answer
			}
		}
		if index := bestKnowledgeEvidenceFAQUnitIndex(units, query); index >= 0 {
			return units[index].Question, units[index].Answer
		}
		return units[0].Question, units[0].Answer
	}
	question := normalizeKnowledgeEvidenceFAQQuestion(strings.TrimSpace(hit.FaqQuestion))
	if question != "" {
		return question, trimKnowledgeEvidenceFAQMetadata(raw)
	}
	return "", ""
}

func knowledgeEvidenceHitForQuery(hit rag.RetrieveResult, query string) rag.RetrieveResult {
	question, answer := splitKnowledgeEvidenceFAQForQuery(hit, query)
	if question == "" || answer == "" {
		return hit
	}
	hit.FaqQuestion = question
	hit.Content = "问题：" + question + "\n答案：" + answer
	return hit
}

func parseQuestionAnswerContent(raw string) (string, string, bool) {
	units := parseKnowledgeEvidenceFAQUnits(raw)
	if len(units) == 0 {
		return "", "", false
	}
	return units[0].Question, units[0].Answer, true
}

var knowledgeEvidenceFAQQuestionMarkerPattern = regexp.MustCompile(`(?m)(?:^|\n)[ \t]*(?:问题|问|Q|q)[ \t]*[:：][ \t]*`)
var knowledgeEvidenceFAQAnswerMarkerPattern = regexp.MustCompile(`(?m)(?:^|\n)?[ \t]*(?:答案|答|A|a)[ \t]*[:：][ \t]*`)
var knowledgeEvidenceFAQMetadataLinePattern = regexp.MustCompile(`(?im)(?:^|\n)[ \t]*(?:#{1,6}[ \t]*)?(?:相似问题|相似问法|训练问题|训练问法|扩展问题|扩展问法|召回问题|命中问题|训练元数据|关键词|标签|metadata)[ \t]*[:：]`)

func trimKnowledgeEvidenceFAQMetadata(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw == "" {
		return ""
	}
	if index := knowledgeEvidenceFAQMetadataLinePattern.FindStringIndex(raw); index != nil {
		raw = raw[:index[0]]
	}
	return strings.TrimSpace(raw)
}

func normalizeKnowledgeEvidenceFAQQuestion(raw string) string {
	question := trimKnowledgeEvidenceFAQMetadata(raw)
	for {
		match := knowledgeEvidenceFAQQuestionMarkerPattern.FindStringIndex(question)
		if match == nil || match[0] != 0 {
			break
		}
		question = strings.TrimSpace(question[match[1]:])
	}
	return question
}

func parseKnowledgeEvidenceFAQUnits(raw string) []knowledgeEvidenceFAQUnit {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	questionMarkers := knowledgeEvidenceFAQQuestionMarkerPattern.FindAllStringIndex(raw, -1)
	units := make([]knowledgeEvidenceFAQUnit, 0, len(questionMarkers))
	for index, marker := range questionMarkers {
		blockEnd := len(raw)
		if index+1 < len(questionMarkers) {
			blockEnd = questionMarkers[index+1][0]
		}
		block := raw[marker[1]:blockEnd]
		answerMarker := knowledgeEvidenceFAQAnswerMarkerPattern.FindStringIndex(block)
		if answerMarker == nil {
			continue
		}
		question := normalizeKnowledgeEvidenceFAQQuestion(block[:answerMarker[0]])
		answer := trimKnowledgeEvidenceFAQMetadata(block[answerMarker[1]:])
		if question != "" && answer != "" {
			units = append(units, knowledgeEvidenceFAQUnit{Question: question, Answer: answer})
		}
	}
	for index := 0; index+1 < len(units); index++ {
		units[index].Answer = trimKnowledgeEvidenceFAQTrailingHeading(units[index].Answer, units[index+1].Question)
	}
	return units
}

func trimKnowledgeEvidenceFAQTrailingHeading(answer string, nextQuestion string) string {
	lines := strings.Split(strings.TrimSpace(answer), "\n")
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "" {
			lines = lines[:len(lines)-1]
			continue
		}
		trimmedHeading := strings.TrimSpace(strings.TrimLeft(last, "#-* "))
		if strings.HasPrefix(last, "#") || knowledgeEvidenceFAQQuestionMatchScore(trimmedHeading, nextQuestion) >= 0.72 {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func bestKnowledgeEvidenceFAQUnitIndex(units []knowledgeEvidenceFAQUnit, query string) int {
	query = strings.TrimSpace(query)
	if len(units) == 0 || query == "" {
		return -1
	}
	bestIndex := -1
	bestScore := 0.0
	for index, unit := range units {
		score := knowledgeEvidenceFAQQuestionMatchScore(unit.Question, query)
		if score > bestScore {
			bestScore = score
			bestIndex = index
		}
	}
	return bestIndex
}

func knowledgeEvidenceFAQQuestionMatchScore(question string, query string) float64 {
	question = normalizeKnowledgeEvidenceQuestionForMatch(question)
	query = normalizeKnowledgeEvidenceQuestionForMatch(query)
	if question == "" || query == "" {
		return 0
	}
	if question == query {
		return 1
	}
	questionLen := len([]rune(question))
	queryLen := len([]rune(query))
	shorter, longer := questionLen, queryLen
	if shorter > longer {
		shorter, longer = longer, shorter
	}
	if shorter >= 4 && (strings.Contains(question, query) || strings.Contains(query, question)) {
		return 0.9 * float64(shorter) / float64(longer)
	}
	return knowledgeEvidenceTextNGramSimilarity(question, query)
}

func normalizeKnowledgeEvidenceQuestionForMatch(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	compact = strings.ReplaceAll(compact, "、", "")
	compact = strings.NewReplacer(
		"wi-fi", "wifi",
		"无线网络", "wifi",
		"无线网", "wifi",
	).Replace(compact)
	compact = strings.NewReplacer(
		"帐号", "账号",
		"用户名", "账号",
		"ssid", "账号",
		"wifi名称", "wifi账号",
		"wifi名字", "wifi账号",
	).Replace(compact)
	for _, prefix := range []string{
		"你们酒店的", "你们酒店", "咱们酒店的", "咱们酒店", "本酒店的", "本酒店", "酒店的", "酒店", "门店的", "门店",
		"你们家的", "你们家", "你们的", "你们", "咱们家的", "咱们家", "咱们的", "咱们",
	} {
		if remainder := strings.TrimPrefix(compact, prefix); remainder != compact && len([]rune(remainder)) >= 4 {
			compact = remainder
			break
		}
	}
	for _, phrase := range []string{"应该怎么填写", "应该怎么填", "要怎么填写", "要怎么填", "如何填写", "如何填", "怎么填写", "怎么填", "填写哪些", "填写什么", "填哪些", "填什么"} {
		compact = strings.ReplaceAll(compact, phrase, "填写内容")
	}
	return compact
}

func knowledgeEvidenceFAQDirectMatchScore(question string, answer string, query string) (float64, bool) {
	const minimumQuestionMatch = 0.82
	questionMatch := knowledgeEvidenceFAQQuestionMatchScore(question, query)
	if questionMatch >= minimumQuestionMatch {
		return questionMatch, true
	}
	return questionMatch, false
}

func knowledgeEvidenceConfigurationAnswerCoversQuery(query string, question string, answer string) bool {
	queryTopic := knowledgeEvidenceConfigurationTopic(query)
	if queryTopic == "" || knowledgeEvidenceConfigurationTopic(question+" "+answer) != queryTopic {
		return false
	}
	requestedFields := knowledgeEvidenceConfigurationFields(query)
	if len(requestedFields) == 0 {
		return false
	}
	coveredValues := knowledgeEvidenceConfigurationValues(answer)
	for _, requested := range requestedFields {
		if len(coveredValues[requested]) == 0 {
			return false
		}
	}
	return true
}

func knowledgeEvidenceConfigurationFAQQuestionHighlyRelated(query string, question string) bool {
	queryTopic := knowledgeEvidenceConfigurationTopic(query)
	if queryTopic == "" || knowledgeEvidenceConfigurationTopic(question) != queryTopic {
		return false
	}
	queryFields := knowledgeEvidenceConfigurationFields(query)
	questionFields := knowledgeEvidenceConfigurationFields(question)
	if len(queryFields) == 0 || len(questionFields) == 0 {
		return false
	}
	for _, field := range questionFields {
		if !knowledgeEvidenceContainsString(queryFields, field) {
			return false
		}
	}
	compact := normalizeKnowledgeEvidenceQuestionForMatch(question)
	return !containsAny(compact, []string{"连不上", "无法连接", "连接失败", "不能用", "用不了", "忘记", "忘了", "修改", "更换", "重置", "故障"})
}

func knowledgeEvidenceConfigurationCandidateCoversTask(task knowledgeEvidenceJudgeTask, question string, answer string) bool {
	return knowledgeEvidenceConfigurationFAQQuestionHighlyRelated(task.Query, question) &&
		knowledgeEvidenceConfigurationAnswerCoversQuery(task.Query, question, answer)
}

func knowledgeEvidenceConfigurationCandidateMatchesTask(task knowledgeEvidenceJudgeTask, candidate knowledgeEvidenceJudgeCandidate, question string, answer string) bool {
	if !knowledgeEvidenceConfigurationCandidateCoversTask(task, question, answer) {
		return false
	}
	return knowledgeEvidenceConfigurationScopeMatches(task.Query, strings.Join([]string{question, answer, candidate.Hit.Title}, " "))
}

func knowledgeEvidenceStrictConfigurationCandidateMatches(task knowledgeEvidenceJudgeTask, candidate knowledgeEvidenceJudgeCandidate, question string, answer string) bool {
	return candidate.Hit.Score >= knowledgeEvidenceDirectFAQMinimumScore &&
		knowledgeEvidenceConfigurationCandidateMatchesTask(task, candidate, question, answer)
}

func knowledgeEvidenceConfigurationScope(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	scopes := make([]string, 0, 3)
	if containsAny(compact, []string{"会议室", "会议厅", "会场"}) {
		scopes = append(scopes, "meeting_room")
	}
	if containsAny(compact, []string{"大堂", "前台区域", "公共区域"}) {
		scopes = append(scopes, "lobby")
	}
	if containsAny(compact, []string{"客房", "房间内", "房内", "房间wifi", "房间wi-fi", "房间无线网"}) {
		scopes = append(scopes, "room")
	}
	if len(scopes) != 1 {
		if len(scopes) > 1 {
			return "mixed"
		}
		return ""
	}
	return scopes[0]
}

func knowledgeEvidenceConfigurationScopeMatches(query string, candidateText string) bool {
	queryScope := knowledgeEvidenceConfigurationScope(query)
	candidateScope := knowledgeEvidenceConfigurationScope(candidateText)
	if queryScope == "" {
		return candidateScope != "mixed"
	}
	return candidateScope == queryScope
}

func knowledgeEvidenceConfigurationLayerHasAmbiguousScope(task knowledgeEvidenceJudgeTask, layer string) bool {
	if knowledgeEvidenceConfigurationTopic(task.Query) == "" || knowledgeEvidenceConfigurationScope(task.Query) != "" {
		return false
	}
	scopes := make(map[string]struct{}, 3)
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if !knowledgeEvidenceConfigurationCandidateCoversTask(task, question, answer) {
			continue
		}
		scope := knowledgeEvidenceConfigurationScope(strings.Join([]string{question, answer, candidate.Hit.Title}, " "))
		if scope == "mixed" {
			return true
		}
		if scope == "" {
			scope = "unspecified"
		}
		scopes[scope] = struct{}{}
		if len(scopes) > 1 {
			return true
		}
	}
	return false
}

func knowledgeEvidenceConfigurationTopic(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	if containsAny(compact, []string{"wifi", "wi-fi", "无线网", "无线网络"}) {
		return "wifi"
	}
	if containsAny(compact, []string{"网络密码", "网络账号", "网络帐号", "网络名称", "网络ssid"}) {
		return "wifi"
	}
	return ""
}

func knowledgeEvidenceConfigurationFields(text string) []string {
	if knowledgeEvidenceConfigurationTopic(text) == "" {
		return nil
	}
	compact := normalizeRuntimeKnowledgeQuery(text)
	fields := make([]string, 0, 2)
	if containsAny(compact, []string{"账号", "帐号", "用户名", "名称", "名字", "ssid", "热点名"}) ||
		containsAny(compact, []string{"wifi是哪个", "wi-fi是哪个", "无线网是哪个", "无线网络是哪个"}) {
		fields = append(fields, "account")
	}
	if containsAny(compact, []string{"密码", "口令"}) {
		fields = append(fields, "password")
	}
	return fields
}

func knowledgeEvidenceConfigurationValues(text string) map[string][]string {
	values := make(map[string][]string, 2)
	matches := knowledgeEvidenceConfigurationFieldMarkerPattern.FindAllStringSubmatchIndex(text, -1)
	for index, match := range matches {
		if len(match) < 4 || match[2] < 0 || match[3] < 0 {
			continue
		}
		valueEnd := len(text)
		if index+1 < len(matches) {
			valueEnd = matches[index+1][0]
		}
		value := strings.TrimSpace(text[match[1]:valueEnd])
		if delimiter := strings.IndexAny(value, "\r\n，,；;。"); delimiter >= 0 {
			value = value[:delimiter]
		}
		value = strings.TrimSpace(strings.Trim(value, "：:"))
		if !knowledgeEvidenceConfigurationValueIsConcrete(value) {
			continue
		}
		field := knowledgeEvidenceConfigurationFieldName(text[match[2]:match[3]])
		if field != "" {
			values[field] = appendIfMissing(values[field], value)
		}
	}
	return values
}

func knowledgeEvidenceConfigurationFieldName(label string) string {
	compact := strings.ToLower(strings.TrimSpace(label))
	if containsAny(compact, []string{"密码", "口令"}) {
		return "password"
	}
	if containsAny(compact, []string{"账号", "帐号", "用户名", "名称", "名字", "ssid"}) {
		return "account"
	}
	return ""
}

func knowledgeEvidenceConfigurationValueIsConcrete(value string) bool {
	compact := normalizeRuntimeKnowledgeQuery(value)
	if len([]rune(compact)) < 2 {
		return false
	}
	return !containsAny(compact, []string{
		"请联系", "联系门店", "联系同事", "联系客服", "咨询门店", "咨询客服", "转接", "人工确认",
		"请确认", "待确认", "不清楚", "不知道", "资料没写", "以门店为准", "以现场为准", "是多少", "是什么",
	})
}

func knowledgeEvidenceConfigurationFactAnswersTask(task knowledgeEvidenceJudgeTask, fact knowledgeEvidenceFact) bool {
	requested := knowledgeEvidenceConfigurationFields(task.Query)
	if len(requested) == 0 {
		return false
	}
	values := knowledgeEvidenceConfigurationValues(fact.Statement)
	for _, field := range requested {
		if len(values[field]) > 0 {
			return true
		}
	}
	return false
}

func knowledgeEvidenceHandoffFAQMatchesQuery(question string, query string) bool {
	const minimumQuestionMatch = 0.78
	if strings.TrimSpace(question) == "" || strings.TrimSpace(query) == "" {
		return false
	}
	if knowledgeEvidenceFAQQuestionMatchScore(question, query) >= minimumQuestionMatch {
		return true
	}
	question = trimKnowledgeEvidenceHandoffQuestionSuffix(question)
	query = trimKnowledgeEvidenceHandoffQuestionSuffix(query)
	return question != "" && query != "" && knowledgeEvidenceFAQQuestionMatchScore(question, query) >= minimumQuestionMatch
}

func trimKnowledgeEvidenceHandoffQuestionSuffix(text string) string {
	text = normalizeKnowledgeEvidenceQuestionForMatch(text)
	for _, suffix := range []string{"应该怎么办", "要怎么办", "怎么办", "怎么处理", "如何处理", "怎么解决", "如何解决", "咋办"} {
		if remainder := strings.TrimSuffix(text, suffix); remainder != text && len([]rune(remainder)) >= 3 {
			return strings.TrimSpace(remainder)
		}
	}
	return strings.TrimSpace(text)
}

func knowledgeEvidenceJudgeSystemPrompt() string {
	return strings.TrimSpace(`你是酒店客服知识证据裁判。你不回答客户，不决定是否转人工，只为每个客户任务在每个知识层选择足以回答的证据。

每个 task 会提供当前原子问题、subIntent、objective、entities、紧邻会话 sourceContext，以及带 layer 的候选。sourceContext 只用于理解“这几个、上面那种、都”等指代，不能当作酒店事实来源。

主体一致性是选择证据的硬约束：候选 FAQ 的问题和答案必须与 task.question、subIntent、objective、entities 指向同一业务主体。客户明确提到早餐时不能选择退房 FAQ，明确提到房型或设施时不能换成其他房型或设施。task.entities 有多个明确实体时，单条候选必须覆盖它声称回答的实体；direct_combined 的全部候选合起来必须逐一覆盖所有明确实体，任何与当前主体无关的候选都不得混入。

事实维度完整性检查是每个 task、每个 layer 的必做步骤：
1. 先把客户当前原子问题拆成内部事实维度清单，并判断维度之间的前提依赖。例如同一句同时询问是否存在、数量、费用、时间、位置、方法、范围或条件时，每个仍然适用的维度都必须单独列入检查；这个内部清单不要作为额外字段输出。只有证据明确否定前提时，依赖该前提的追问才不再适用，不得列入 missingAspects。例如明确不能步行时，步行分钟数不再适用；仅“建议驾车”不能推导“不能步行”，步行可行性和时长未知时仍须保留缺失。独立问题或客户明确追问的其他交通方式、时间仍须检查。
2. 对当前 layer 提供的全部候选逐条检查，每条候选的 faqQuestion、faqAnswer 和 rawContent 都要核对它能支持清单中的哪些维度，不能在看到第一条相关候选后提前停止。
3. 不同候选分别补齐不同事实维度，且属于同一门店、同一对象和同一适用范围时，必须判 direct_combined，并选中所有补齐答案所必需的同层候选。
4. 只有检查完当前 layer 的全部候选，仍有适用的清单维度没有任何候选能够补齐时，才允许判 partial，并且 missingAspects 只能写这些真实缺失的维度。只要同层还有候选能补齐 missingAspects，就不得判 partial。

业务政策答复也可以完整回答问题：候选 FAQ 与客户问题语义一致，答案明确给出该问题的适用政策、条件或选择方式时，不要求强行改写成“是/否”或数值结论，可以判 direct_single。必须保留原政策的主体、限定和建议，不能把相关属性差异改成客户所问属性的确定差异。例如平台价格是否相同的 FAQ 回答“每个客户在不同平台享受的平台权益是不一样的，建议您可以对比价格后选择合适您的”，应完整保留这一答复；不能改成“价格不一样”，也不能仅因没有明确同价或异价判 partial。仅主题相关的背景、含糊回避、转接指令或没有覆盖客户新增具体要求的政策不适用此规则。supportedFacts 不得混入“无法证明、证据不足”等裁决分析，criticalValues 不得把脱离主体的“不一样”等词作为价格结论。

外部代执行任务只在 intent=service_request、subIntent=external_proxy_action、objective=action_request 时适用：
- “酒店能否替客户点外卖、叫车、代买、代订或联系外部商家”不是知识库需要证明的酒店事实维度；你只裁决候选中是否存在能帮助客户自行完成同一目标的地址、电话、入口或操作步骤。
- 如果候选明确提供了上述自助信息，可以按证据完整性判 direct_single/direct_combined，并且 supportedFacts 只能保留知识原文明确写出的事实。
- 不得输出或暗示酒店已经代点、叫车、购买、预订、联系或稍后会执行；仅有“有外卖机器人”等旁支存在性事实，不能证明机器人能配送、酒店能代下单或其他执行能力。
- 酒店内部送物、维修、开门等必须由门店处理的动作不属于 external_proxy_action，仍按原有服务证据规则裁决。

必须分别裁决 store 和 general 两层，每层只能输出一种 decision：
- direct_single：单条候选的完整语义足以回答当前问题，只选择这一条。
- direct_combined：同一层内至少两条候选指向同一门店、同一实体和同一适用范围，合在一起足以回答当前问题，只选择必要的候选。
- partial：同一层内已确认一部分有用事实，但仍缺少当前问题要求的一个或多个事实维度。只选择支持已确认事实的必要候选。
- insufficient：该层没有足够证据，selectedCandidateIds 必须为空。

每层还必须输出 supportedFacts 和 missingAspects：
- supportedFacts 只能写 selectedCandidateIds 原文明示或完整 FAQ 问答明确确认的原子事实。每条必须包含 factId、aspect、statement、criticalValues。
- factId 在同一个 task 的同一知识层内必须唯一；aspect 只能是 existence、quantity、price、time、location、method、scope、condition、other。
- statement 必须是可直接给后续回复使用的完整事实句，不要写推理过程。criticalValues 只列不能自然改写的精确值，例如数量、金额、时间、电话、地址、房型名、账号密码、免费/收费或固定选项；“建议、选择、联系、回复、比较、办理”等普通动作词不得放入 criticalValues，没有精确值则输出空数组。
- missingAspects 只写客户当前问题仍然缺失的事实维度或条件，使用简短中文短语。
- direct_single/direct_combined 必须至少有一条 supportedFacts，且 missingAspects 为空；唯一例外是选中单条“转接/转人工”流程指令时，supportedFacts 和 missingAspects 都必须为空。
- partial 必须同时包含至少一条 supportedFacts 和一条 missingAspects。
- insufficient 的 selectedCandidateIds 和 supportedFacts 必须为空；missingAspects 可以用简短短语说明当前层缺少什么，没有必要时输出空数组。

严禁跨 store/general 拼接证据，也不能把不同门店、不同房型对象、不同时间条件或互相矛盾的内容组合。检索分数和候选顺序不能替代语义判断。

FAQ 必须把 faqQuestion 和 faqAnswer 作为一个完整问答来理解。答案出现“是的、可以、不需要、没有”等省略表达时，可以结合 FAQ 问题还原其中已经被明确确认的对象、数量、条件和结论；不得补出 FAQ 问答没有确认的事实。rawContent 只用于核对原文。
上位类别的存在性问题，可以由明确肯定的具体子类证明存在；但否定某个具体子类，不能证明整个上位类别不存在。不同具体子类的一正一负不是冲突，只有同一主体、同一适用范围和同一条件下互相矛盾的结论才是冲突。候选选择是首要任务；只要候选能够完整回答，supportedFacts 的提取困难不能成为判 insufficient 的理由。

条件不能从事实中消失。若答案是“是的，仅限退房前办理”“可以，但仅适用于指定房型”等带硬限制的肯定，statement 必须同时写出肯定结论和限制条件；不得输出无条件的“可以办理”“所有房型都可以”。

费用事实必须区分绝对状态、相对关系和动态政策：“不免费/需要付费”是收费，不是免费；“不同平台免费政策不一样/权益不同”只说明政策或权益存在差异，不能证明任一平台免费；“不同平台”只是主体组别名，只有“价格不一样/相同、哪家更便宜”等明确谓词才是价格比较结论。

用品补充和自取问题必须结合客户状态与 FAQ 答案中的动作判断完整性。例如客户说“纸巾不够了，怎么补充”，同一用品的门店 FAQ 即使问题写成“纸巾用完了怎么办”，只要答案明确给出“前往某处领取/自取”的地点和动作，就已经完整覆盖 method；不能仅因问题措辞不同判 insufficient，也不能改选通用层的“转接”。

肯定枚举中的精确成员属于明确存在性证据。例如“部分房型配备办公桌，如合柴、麦田和艺林”已经明确支持“麦田房型有办公桌”；不能因为总述使用“部分房型”就把枚举内成员判为 insufficient。只有成员名称、所问设施或能力、肯定关系都在同一条 FAQ 原文中明确出现时才能使用，不能把相似名称、条件性描述或其他事实维度当成枚举成员。

最小完整答案规则：supportedFacts 只保留完整回答当前 task 必需的最小事实集合。必要的事实、适用条件和操作方法不能遗漏；背景介绍、重复总结、礼貌话、未被客户询问的路线/时长/价格/延伸建议不得加入。普通动作语义写在 statement 中，不要求后续逐字复述，也不得把动作词本身放入 criticalValues。
严禁把一条长候选知识逐句全部拆成 supportedFacts。只输出当前问题真正需要的最小事实；一个完整 statement 已覆盖多个维度时可以复用该 statement，不再输出它所包含的摘要句或无关细节。

检查 selectedCandidateIds 的 faqAnswer 时，只拆出当前问题实际要求的独立事实维度。一个答案同时包含否定/能力边界与办理方法、数量与费用等必要维度时不能遗漏；同一完整句已经覆盖多个维度时，各 Fact 可以复用同一个完整 statement，禁止再输出被该完整句包含的摘要或碎片。否定对象、数量、金额、时间、电话、地址等不可遗漏的原文字面值必须进入对应 fact 的 criticalValues。

例如 FAQ 问题“问下房间的两瓶矿泉水是免费的吗？”、答案“是的，房间内的矿泉水都是免费的”，完整语义已经确认“房间内有两瓶矿泉水，并且免费”。它足以回答“房间里有几瓶矿泉水”，应判 direct_single；不能因为数量只写在 faqQuestion 中就丢掉这个已被肯定回答确认的事实。这个规则同样适用于其他 FAQ 中被肯定或否定答案确认的对象、数量与条件。

候选答案如果只是“转接”，它是流程指令，不是酒店事实。只有 FAQ 问题与当前任务语义直接匹配时，才可以把该候选作为 direct_single 单条流程指令选择，此时 supportedFacts 和 missingAspects 都输出空数组；绝不能把 FAQ 问题文字当作已经确认的事实，也不能让“转接”候选参与 direct_combined。

事实维度必须严格隔离：确认“有外卖机器人”只支持 existence，不能生成“能送到房间”的 scope 或 method；确认地点名称只支持 existence/location，不能生成距离、步行时间或路线；确认有充电桩不能推导所有车位都能充电。客户询问了这些仍然适用但未被证据确认的维度时，应判 partial 并把对应维度写入 missingAspects。

同层组合示例：客户问“既有沙发又有办公桌的房型有哪些”，一条候选完整列出有沙发的房型，另一条候选完整列出有办公桌的房型，两条属于同一门店和房型范围时，必须判 direct_combined，并由 Judge 直接计算交集。supportedFacts 只输出交集结论及交集房型 criticalValues，禁止把两组源集合原样交给后续生成阶段。只知道沙发或只知道办公桌时应判 partial，保留已确认事实，同时明确缺少另一项设施事实。

否定答案也可以完整回答问题。例如“早餐几点”对应“酒店不提供早餐”可以判 direct_single。必须区分能力/存在性与故障/执行请求，例如“有空调吗”不能选择“空调不制冷需要处理”。

严格输出 JSON，不要 Markdown、解释或额外字段。必须原样返回每个 taskId；对输入实际包含的每个 layer 恰好返回一次。输出格式：
{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水，都是免费的。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内有两瓶矿泉水，都是免费的。","criticalValues":["免费"]}],"missingAspects":[]},{"layer":"general","decision":"insufficient","selectedCandidateIds":[],"supportedFacts":[],"missingAspects":["没有可用于回答当前问题的通用知识证据"]}]}]}`)
}

func parseKnowledgeEvidenceJudgeResponse(raw string, tasks []knowledgeEvidenceJudgeTask) (map[string]map[string]knowledgeEvidenceLayerSelection, error) {
	return parseKnowledgeEvidenceJudgeResponseWithValidation(raw, tasks, false)
}

// Runtime trusts the Judge's semantic decision and only validates its wire contract.
func parseKnowledgeEvidenceJudgeRuntimeResponse(raw string, tasks []knowledgeEvidenceJudgeTask) (map[string]map[string]knowledgeEvidenceLayerSelection, error) {
	return parseKnowledgeEvidenceJudgeResponseWithValidation(raw, tasks, true)
}

func parseKnowledgeEvidenceJudgeResponseWithValidation(raw string, tasks []knowledgeEvidenceJudgeTask, protocolOnly bool) (map[string]map[string]knowledgeEvidenceLayerSelection, error) {
	normalized, err := normalizeKnowledgeEvidenceJudgeResponseJSON(raw)
	if err != nil {
		return nil, knowledgeEvidenceJudgeResponseError(knowledgeEvidenceDecisionMalformed, err)
	}
	parsed := knowledgeEvidenceJudgeRawResponse{}
	decoder := json.NewDecoder(strings.NewReader(normalized))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return nil, knowledgeEvidenceJudgeResponseError(knowledgeEvidenceDecisionMalformed, fmt.Errorf("decode knowledge judge response: %w", err))
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, knowledgeEvidenceJudgeResponseError(knowledgeEvidenceDecisionMalformed, fmt.Errorf("knowledge judge response contains trailing content"))
	}
	if parsed.SchemaVersion != knowledgeEvidenceJudgeSchemaVersion {
		return nil, knowledgeEvidenceJudgeResponseError(knowledgeEvidenceDecisionProtocolInvalid, fmt.Errorf("unexpected knowledge judge schema version %q", parsed.SchemaVersion))
	}
	expected := make(map[string]map[string]map[string]struct{}, len(tasks))
	expectedTasks := make(map[string]knowledgeEvidenceJudgeTask, len(tasks))
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			return nil, knowledgeEvidenceJudgeResponseError(knowledgeEvidenceDecisionProtocolInvalid, fmt.Errorf("knowledge judge task id is empty"))
		}
		layerCandidates := make(map[string]map[string]struct{}, 2)
		for _, candidate := range task.Candidates {
			candidateID := strings.TrimSpace(candidate.CandidateID)
			if candidateID == "" {
				return nil, knowledgeEvidenceJudgeResponseError(knowledgeEvidenceDecisionProtocolInvalid, fmt.Errorf("knowledge judge candidate id is empty for task %s", taskID))
			}
			layer := strings.TrimSpace(candidate.Layer)
			if layer != knowledgeEvidenceLayerStore && layer != knowledgeEvidenceLayerGeneral {
				return nil, knowledgeEvidenceJudgeResponseError(knowledgeEvidenceDecisionProtocolInvalid, fmt.Errorf("invalid knowledge judge layer %q for candidate %s", layer, candidateID))
			}
			if layerCandidates[layer] == nil {
				layerCandidates[layer] = make(map[string]struct{})
			}
			if _, exists := layerCandidates[layer][candidateID]; exists {
				return nil, knowledgeEvidenceJudgeResponseError(knowledgeEvidenceDecisionProtocolInvalid, fmt.Errorf("duplicate expected candidate id %s", candidateID))
			}
			layerCandidates[layer][candidateID] = struct{}{}
		}
		expected[taskID] = layerCandidates
		expectedTasks[taskID] = task
	}

	ret := make(map[string]map[string]knowledgeEvidenceLayerSelection, len(tasks))
	for taskID, expectedLayers := range expected {
		ret[taskID] = defaultKnowledgeEvidenceLayerSelections(expectedLayers)
	}
	seenTasks := make(map[string]bool, len(parsed.Tasks))
	invalidTasks := make(map[string]bool)
	for _, task := range parsed.Tasks {
		taskID := strings.TrimSpace(task.TaskID)
		expectedLayers, ok := expected[taskID]
		if !ok {
			continue
		}
		if seenTasks[taskID] {
			ret[taskID] = failedKnowledgeEvidenceLayerSelectionsForExpected(expectedLayers, knowledgeEvidenceDecisionProtocolInvalid)
			invalidTasks[taskID] = true
			continue
		}
		seenTasks[taskID] = true
		if invalidTasks[taskID] {
			continue
		}
		selections := ret[taskID]
		seenLayers := make(map[string]bool, len(task.Layers))
		invalidLayers := make(map[string]bool)
		for _, layerResult := range task.Layers {
			layer := strings.TrimSpace(layerResult.Layer)
			expectedCandidates, ok := expectedLayers[layer]
			if !ok {
				continue
			}
			if seenLayers[layer] {
				selections[layer] = protocolInvalidKnowledgeEvidenceLayerSelection()
				invalidLayers[layer] = true
				continue
			}
			seenLayers[layer] = true
			if invalidLayers[layer] {
				continue
			}
			decodedLayer, factsMalformed, missingAspectsMalformed := decodeKnowledgeEvidenceJudgeRawLayer(layerResult)
			if protocolOnly {
				selections[layer] = normalizeParsedKnowledgeEvidenceLayerSelectionProtocolOnly(
					taskID,
					layer,
					decodedLayer,
					expectedCandidates,
					expectedTasks[taskID],
					factsMalformed,
					missingAspectsMalformed,
				)
			} else {
				selections[layer] = normalizeParsedKnowledgeEvidenceLayerSelectionWithMalformedFields(
					taskID,
					layer,
					decodedLayer,
					expectedCandidates,
					expectedTasks[taskID],
					factsMalformed,
					missingAspectsMalformed,
				)
			}
		}
		ret[taskID] = selections
	}
	return ret, nil
}

func decodeKnowledgeEvidenceJudgeRawLayer(raw knowledgeEvidenceJudgeRawResponseLayer) (knowledgeEvidenceJudgeResponseLayer, bool, bool) {
	layer := knowledgeEvidenceJudgeResponseLayer{
		Layer:                raw.Layer,
		Decision:             raw.Decision,
		SelectedCandidateIDs: append([]string(nil), raw.SelectedCandidateIDs...),
	}
	factsMalformed := decodeKnowledgeEvidenceJudgeFacts(raw.SupportedFacts, &layer.SupportedFacts) != nil
	missingAspectsMalformed := decodeKnowledgeEvidenceJudgeMissingAspects(raw.MissingAspects, &layer.MissingAspects) != nil
	if strings.TrimSpace(raw.Decision) == knowledgeEvidenceDecisionInsufficient && len(raw.SelectedCandidateIDs) == 0 {
		if knowledgeEvidenceJudgeRawArrayIsMissingOrNull(raw.SupportedFacts) {
			layer.SupportedFacts = nil
			factsMalformed = false
		}
		if knowledgeEvidenceJudgeRawArrayIsMissingOrNull(raw.MissingAspects) {
			layer.MissingAspects = nil
			missingAspectsMalformed = false
		}
	}
	return layer, factsMalformed, missingAspectsMalformed
}

func knowledgeEvidenceJudgeRawArrayIsMissingOrNull(raw json.RawMessage) bool {
	return len(raw) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null")
}

func decodeKnowledgeEvidenceJudgeFacts(raw json.RawMessage, target *[]knowledgeEvidenceFact) error {
	if len(raw) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return fmt.Errorf("supportedFacts must be an array")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("supportedFacts contains trailing content")
	}
	return nil
}

func decodeKnowledgeEvidenceJudgeMissingAspects(raw json.RawMessage, target *[]string) error {
	if len(raw) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return fmt.Errorf("missingAspects must be an array")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("missingAspects contains trailing content")
	}
	return nil
}

func defaultKnowledgeEvidenceLayerSelections(expectedLayers map[string]map[string]struct{}) map[string]knowledgeEvidenceLayerSelection {
	return failedKnowledgeEvidenceLayerSelectionsForExpected(expectedLayers, knowledgeEvidenceDecisionProtocolInvalid)
}

func failedKnowledgeEvidenceLayerSelectionsForExpected(expectedLayers map[string]map[string]struct{}, decision string) map[string]knowledgeEvidenceLayerSelection {
	selections := make(map[string]knowledgeEvidenceLayerSelection, len(expectedLayers))
	for layer := range expectedLayers {
		selections[layer] = knowledgeEvidenceLayerSelection{Decision: decision, DecisionSource: decision}
	}
	return selections
}

func insufficientKnowledgeEvidenceLayerSelection() knowledgeEvidenceLayerSelection {
	return knowledgeEvidenceLayerSelection{Decision: knowledgeEvidenceDecisionInsufficient, DecisionSource: "model"}
}

func protocolInvalidKnowledgeEvidenceLayerSelection() knowledgeEvidenceLayerSelection {
	return knowledgeEvidenceLayerSelection{Decision: knowledgeEvidenceDecisionProtocolInvalid, DecisionSource: knowledgeEvidenceDecisionProtocolInvalid}
}

func normalizeParsedKnowledgeEvidenceLayerSelection(
	taskID string,
	layer string,
	layerResult knowledgeEvidenceJudgeResponseLayer,
	expectedCandidates map[string]struct{},
	expectedTask knowledgeEvidenceJudgeTask,
) knowledgeEvidenceLayerSelection {
	return normalizeParsedKnowledgeEvidenceLayerSelectionWithMalformedFields(
		taskID,
		layer,
		layerResult,
		expectedCandidates,
		expectedTask,
		false,
		false,
	)
}

func normalizeParsedKnowledgeEvidenceLayerSelectionWithMalformedFields(
	taskID string,
	layer string,
	layerResult knowledgeEvidenceJudgeResponseLayer,
	expectedCandidates map[string]struct{},
	expectedTask knowledgeEvidenceJudgeTask,
	supportedFactsMalformed bool,
	missingAspectsMalformed bool,
) knowledgeEvidenceLayerSelection {
	protocolInvalid := protocolInvalidKnowledgeEvidenceLayerSelection()
	decision := strings.TrimSpace(layerResult.Decision)
	switch decision {
	case knowledgeEvidenceDecisionDirectSingle, knowledgeEvidenceDecisionDirectCombined, knowledgeEvidenceDecisionPartial, knowledgeEvidenceDecisionInsufficient:
	default:
		return protocolInvalid
	}

	selectedIDs := make([]string, 0, len(layerResult.SelectedCandidateIDs))
	seenSelected := make(map[string]struct{}, len(layerResult.SelectedCandidateIDs))
	for _, rawCandidateID := range layerResult.SelectedCandidateIDs {
		candidateID := strings.TrimSpace(rawCandidateID)
		if _, ok := expectedCandidates[candidateID]; !ok {
			return protocolInvalid
		}
		if _, exists := seenSelected[candidateID]; exists {
			return protocolInvalid
		}
		seenSelected[candidateID] = struct{}{}
		selectedIDs = append(selectedIDs, candidateID)
	}
	directDecision := decision == knowledgeEvidenceDecisionDirectSingle || decision == knowledgeEvidenceDecisionDirectCombined
	explicitSubjectGaps := knowledgeEvidenceSelectedCandidateExplicitSubjectGaps(expectedTask, layer, selectedIDs)
	if len(selectedIDs) > 0 &&
		(knowledgeEvidenceSelectedCandidatesHaveExplicitSubjectConflict(expectedTask, layer, selectedIDs) ||
			(directDecision && knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(expectedTask, layer, selectedIDs)) ||
			(directDecision && len(explicitSubjectGaps) > 0) ||
			knowledgeEvidenceSelectedCandidatesHaveConflictingAnswers(expectedTask, layer, selectedIDs)) {
		return protocolInvalid
	}
	selectedContainsHandoff := selectedKnowledgeEvidenceContainsHandoffDirective(expectedTask, layer, selectedIDs)
	selectedHandoff := selectedKnowledgeEvidenceIsHandoffDirective(expectedTask, layer, selectedIDs)
	if selectedContainsHandoff && (!selectedHandoff || decision != knowledgeEvidenceDecisionDirectSingle || len(selectedIDs) != 1) {
		return protocolInvalid
	}
	supportedFacts := []knowledgeEvidenceFact(nil)
	factsMalformed := supportedFactsMalformed
	if !factsMalformed {
		var err error
		supportedFacts, err = normalizeKnowledgeEvidenceFacts(taskID, layer, layerResult.SupportedFacts, make(map[string]struct{}))
		factsMalformed = err != nil
	}
	modelFactsGroundSelectedCandidates := false
	if !factsMalformed {
		modelFactsGroundSelectedCandidates = modelKnowledgeEvidenceFactsGroundEverySelectedCandidate(
			expectedTask,
			layer,
			selectedIDs,
			supportedFacts,
		)
	}
	missingAspects := []string(nil)
	if !missingAspectsMalformed {
		var err error
		missingAspects, err = normalizeKnowledgeEvidenceMissingAspects(taskID, layer, layerResult.MissingAspects)
		missingAspectsMalformed = err != nil
	}
	if decision == knowledgeEvidenceDecisionPartial && len(explicitSubjectGaps) > 0 {
		missingAspects = appendKnowledgeEvidenceMissingAspects(
			missingAspects,
			knowledgeEvidenceExplicitSubjectGapMissingAspects(expectedTask, explicitSubjectGaps),
		)
	}
	intersectionDecision := decision == knowledgeEvidenceDecisionDirectCombined || decision == knowledgeEvidenceDecisionPartial
	if intersectionDecision && len(selectedIDs) >= 2 && knowledgeEvidenceQueryAsksIntersection(expectedTask.Query) {
		intersection, ok := deterministicKnowledgeEvidenceIntersectionSelection(expectedTask, layer, selectedIDs)
		if !ok {
			return protocolInvalid
		}
		intersection.DecisionSource = "model_selected_repair"
		return intersection
	}
	modelFactsRequiredRepair := false
	if !factsMalformed && len(selectedIDs) > 0 {
		modelFactCount := len(supportedFacts)
		supportedFacts = groundedKnowledgeEvidenceFacts(expectedTask, layer, selectedIDs, supportedFacts)
		modelFactsRequiredRepair = len(supportedFacts) != modelFactCount
		groundedFactCount := len(supportedFacts)
		supportedFacts = enrichKnowledgeEvidenceFactsFromSelectedFAQs(expectedTask, layer, selectedIDs, supportedFacts)
		modelFactsRequiredRepair = modelFactsRequiredRepair || len(supportedFacts) != groundedFactCount
	}
	if !factsMalformed {
		supportedFacts = finalizeKnowledgeEvidenceFactsForTask(expectedTask, supportedFacts)
	}
	if decision == knowledgeEvidenceDecisionDirectCombined && len(selectedIDs) == 1 {
		return protocolInvalid
	}
	partialMissingAspectsReconciled := false
	if decision == knowledgeEvidenceDecisionPartial && !factsMalformed && !missingAspectsMalformed && len(selectedIDs) > 0 && !selectedHandoff {
		reconciledMissingAspects := unresolvedModelKnowledgeEvidenceMissingAspects(expectedTask, supportedFacts, missingAspects)
		partialMissingAspectsReconciled = len(reconciledMissingAspects) != len(missingAspects)
	}
	selectedTaskBoundCriticalValues := knowledgeEvidenceSelectedTaskBoundCriticalValues(expectedTask, layer, selectedIDs)
	taskBoundCriticalValuesMissing := (decision == knowledgeEvidenceDecisionDirectSingle || decision == knowledgeEvidenceDecisionDirectCombined || decision == knowledgeEvidenceDecisionPartial) &&
		len(selectedIDs) > 0 && !selectedHandoff &&
		!knowledgeEvidenceFactsCoverCriticalValues(supportedFacts, selectedTaskBoundCriticalValues)
	needsFactRepair := factsMalformed || missingAspectsMalformed || modelFactsRequiredRepair || taskBoundCriticalValuesMissing || partialMissingAspectsReconciled
	mechanicallyMissingAspects := []string(nil)
	if !factsMalformed && !selectedHandoff {
		mechanicallyMissingAspects = strictMechanicalMissingKnowledgeEvidenceAspects(expectedTask, supportedFacts)
	}
	switch decision {
	case knowledgeEvidenceDecisionInsufficient:
		if factsMalformed || missingAspectsMalformed || len(selectedIDs) != 0 || len(supportedFacts) != 0 {
			return protocolInvalid
		}
		return knowledgeEvidenceLayerSelection{
			Decision:       knowledgeEvidenceDecisionInsufficient,
			DecisionSource: "model",
			MissingAspects: missingAspects,
		}
	case knowledgeEvidenceDecisionDirectSingle:
		if len(selectedIDs) != 1 || len(missingAspects) != 0 || len(mechanicallyMissingAspects) != 0 || (!selectedHandoff && len(supportedFacts) == 0) || (selectedHandoff && len(supportedFacts) != 0) {
			needsFactRepair = true
		}
	case knowledgeEvidenceDecisionDirectCombined:
		if len(selectedIDs) < 2 || selectedHandoff || len(supportedFacts) == 0 || len(missingAspects) != 0 || len(mechanicallyMissingAspects) != 0 {
			needsFactRepair = true
		}
	case knowledgeEvidenceDecisionPartial:
		missingAspects = appendKnowledgeEvidenceMissingAspects(missingAspects, mechanicallyMissingAspects)
		if len(selectedIDs) == 0 || selectedHandoff || len(supportedFacts) == 0 || len(missingAspects) == 0 {
			needsFactRepair = true
		}
	}
	if needsFactRepair {
		if repaired, ok := repairModelSelectedKnowledgeEvidenceLayer(
			expectedTask,
			layer,
			decision,
			selectedIDs,
			missingAspects,
			modelFactsGroundSelectedCandidates,
		); ok {
			return repaired
		}
		return protocolInvalid
	}
	selection := knowledgeEvidenceLayerSelection{
		Decision:             decision,
		DecisionSource:       "model",
		SelectedCandidateIDs: selectedIDs,
		SupportedFacts:       supportedFacts,
		MissingAspects:       missingAspects,
	}
	return selection
}

func normalizeParsedKnowledgeEvidenceLayerSelectionProtocolOnly(
	taskID string,
	layer string,
	layerResult knowledgeEvidenceJudgeResponseLayer,
	expectedCandidates map[string]struct{},
	expectedTask knowledgeEvidenceJudgeTask,
	supportedFactsMalformed bool,
	missingAspectsMalformed bool,
) knowledgeEvidenceLayerSelection {
	protocolInvalid := protocolInvalidKnowledgeEvidenceLayerSelection()
	decision := strings.TrimSpace(layerResult.Decision)
	switch decision {
	case knowledgeEvidenceDecisionDirectSingle, knowledgeEvidenceDecisionDirectCombined, knowledgeEvidenceDecisionPartial, knowledgeEvidenceDecisionInsufficient:
	default:
		return protocolInvalid
	}

	selectedIDs := make([]string, 0, len(layerResult.SelectedCandidateIDs))
	seenSelected := make(map[string]struct{}, len(layerResult.SelectedCandidateIDs))
	for _, rawCandidateID := range layerResult.SelectedCandidateIDs {
		candidateID := strings.TrimSpace(rawCandidateID)
		if _, ok := expectedCandidates[candidateID]; !ok {
			return protocolInvalid
		}
		if _, exists := seenSelected[candidateID]; exists {
			return protocolInvalid
		}
		seenSelected[candidateID] = struct{}{}
		selectedIDs = append(selectedIDs, candidateID)
	}
	selectedContainsHandoff := selectedKnowledgeEvidenceContainsHandoffDirective(expectedTask, layer, selectedIDs)
	selectedHandoff := selectedModelKnowledgeEvidenceIsHandoffDirective(expectedTask, layer, selectedIDs)
	if selectedContainsHandoff && (!selectedHandoff || decision != knowledgeEvidenceDecisionDirectSingle || len(selectedIDs) != 1) {
		return protocolInvalid
	}
	if selectedHandoff {
		return knowledgeEvidenceLayerSelection{
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			DecisionSource:       "model",
			SelectedCandidateIDs: selectedIDs,
		}
	}
	if supportedFactsMalformed || missingAspectsMalformed {
		return protocolInvalid
	}
	supportedFacts, err := normalizeKnowledgeEvidenceFacts(taskID, layer, layerResult.SupportedFacts, make(map[string]struct{}))
	if err != nil {
		return protocolInvalid
	}
	missingAspects, err := normalizeKnowledgeEvidenceMissingAspects(taskID, layer, layerResult.MissingAspects)
	if err != nil {
		return protocolInvalid
	}
	switch decision {
	case knowledgeEvidenceDecisionInsufficient:
		if len(selectedIDs) != 0 || len(supportedFacts) != 0 {
			return protocolInvalid
		}
	case knowledgeEvidenceDecisionDirectSingle:
		if len(selectedIDs) != 1 || len(missingAspects) != 0 || (!selectedHandoff && len(supportedFacts) == 0) || (selectedHandoff && len(supportedFacts) != 0) {
			return protocolInvalid
		}
	case knowledgeEvidenceDecisionDirectCombined:
		if len(selectedIDs) < 2 || selectedContainsHandoff || len(supportedFacts) == 0 || len(missingAspects) != 0 {
			return protocolInvalid
		}
	case knowledgeEvidenceDecisionPartial:
		if len(selectedIDs) == 0 || selectedContainsHandoff || len(supportedFacts) == 0 || len(missingAspects) == 0 {
			return protocolInvalid
		}
	}
	return knowledgeEvidenceLayerSelection{
		Decision:             decision,
		DecisionSource:       "model",
		SelectedCandidateIDs: selectedIDs,
		SupportedFacts:       supportedFacts,
		MissingAspects:       missingAspects,
	}
}

func repairModelSelectedKnowledgeEvidenceLayer(
	task knowledgeEvidenceJudgeTask,
	layer string,
	decision string,
	selectedCandidateIDs []string,
	missingAspects []string,
	modelFactsGroundSelectedCandidates bool,
) (knowledgeEvidenceLayerSelection, bool) {
	if len(selectedCandidateIDs) == 0 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	if decision == knowledgeEvidenceDecisionDirectSingle && len(selectedCandidateIDs) != 1 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	if decision == knowledgeEvidenceDecisionDirectCombined && len(selectedCandidateIDs) < 2 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	if decision != knowledgeEvidenceDecisionDirectSingle && decision != knowledgeEvidenceDecisionDirectCombined && decision != knowledgeEvidenceDecisionPartial {
		return knowledgeEvidenceLayerSelection{}, false
	}
	if selectedKnowledgeEvidenceIsHandoffDirective(task, layer, selectedCandidateIDs) {
		if decision != knowledgeEvidenceDecisionDirectSingle || len(selectedCandidateIDs) != 1 || len(missingAspects) != 0 {
			return knowledgeEvidenceLayerSelection{}, false
		}
		return knowledgeEvidenceLayerSelection{
			Decision:             decision,
			DecisionSource:       "model_selected_repair",
			SelectedCandidateIDs: append([]string(nil), selectedCandidateIDs...),
		}, true
	}
	directDecision := decision == knowledgeEvidenceDecisionDirectSingle || decision == knowledgeEvidenceDecisionDirectCombined
	explicitSubjectGaps := knowledgeEvidenceSelectedCandidateExplicitSubjectGaps(task, layer, selectedCandidateIDs)
	if knowledgeEvidenceSelectedCandidatesHaveExplicitSubjectConflict(task, layer, selectedCandidateIDs) ||
		(directDecision && knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task, layer, selectedCandidateIDs)) ||
		(directDecision && len(explicitSubjectGaps) > 0) ||
		knowledgeEvidenceSelectedCandidatesHaveConflictingAnswers(task, layer, selectedCandidateIDs) ||
		(!modelFactsGroundSelectedCandidates &&
			!knowledgeEvidenceSelectedCandidatesMatchTaskSubjects(task, layer, selectedCandidateIDs, decision)) {
		return knowledgeEvidenceLayerSelection{}, false
	}
	if decision == knowledgeEvidenceDecisionPartial && len(explicitSubjectGaps) > 0 {
		missingAspects = appendKnowledgeEvidenceMissingAspects(
			missingAspects,
			knowledgeEvidenceExplicitSubjectGapMissingAspects(task, explicitSubjectGaps),
		)
	}

	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	facts := make([]knowledgeEvidenceFact, 0, len(selectedCandidateIDs))
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
			return knowledgeEvidenceLayerSelection{}, false
		}
		candidateFacts := deterministicKnowledgeEvidenceFactsFromFAQ(task.TaskID, answer)
		candidateFacts = enrichKnowledgeEvidenceFactsFromFAQUnit(task, question, answer, candidateFacts)
		if len(candidateFacts) == 0 {
			candidateFacts = []knowledgeEvidenceFact{{
				Aspect:    "other",
				Statement: strings.TrimSpace(answer),
			}}
		}
		facts = append(facts, candidateFacts...)
	}
	if len(facts) == 0 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	seenFactIDs := make(map[string]struct{}, len(facts))
	for index := range facts {
		facts[index].FactID = nextKnowledgeEvidenceFactID(task.TaskID, seenFactIDs)
		seenFactIDs[facts[index].FactID] = struct{}{}
	}
	facts = groundedKnowledgeEvidenceFacts(task, layer, selectedCandidateIDs, facts)
	// A repair only replaces malformed model facts with facts rebuilt from the
	// already-selected FAQ. Keep every grounded dimension here; completeness is
	// checked immediately below and must not depend on the response-style filter.
	facts = canonicalizeKnowledgeEvidenceFacts(sanitizeKnowledgeEvidenceFacts(facts))
	if len(facts) == 0 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	taskBoundValuesComplete := true
	if decision == knowledgeEvidenceDecisionDirectSingle || decision == knowledgeEvidenceDecisionDirectCombined {
		facts, taskBoundValuesComplete = reconcileSelectedKnowledgeEvidenceTaskBoundCriticalValues(task, layer, selectedCandidateIDs, facts)
		if !taskBoundValuesComplete {
			return knowledgeEvidenceLayerSelection{}, false
		}
	} else {
		// Partial evidence is allowed to leave an explicit quantity unresolved.
		// Reconcile any quantity that is mechanically grounded, but let the
		// missing-aspect pass below preserve what the selected FAQ did prove.
		facts, taskBoundValuesComplete = reconcileSelectedKnowledgeEvidenceTaskBoundCriticalValues(task, layer, selectedCandidateIDs, facts)
	}
	mechanicallyMissingAspects := strictMechanicalMissingKnowledgeEvidenceAspects(task, facts)
	switch decision {
	case knowledgeEvidenceDecisionDirectSingle, knowledgeEvidenceDecisionDirectCombined:
		if len(mechanicallyMissingAspects) != 0 {
			return knowledgeEvidenceLayerSelection{}, false
		}
	case knowledgeEvidenceDecisionPartial:
		missingAspects = unresolvedModelKnowledgeEvidenceMissingAspects(task, facts, missingAspects)
		missingAspects = appendKnowledgeEvidenceMissingAspects(missingAspects, mechanicallyMissingAspects)
		if len(missingAspects) == 0 {
			if !taskBoundValuesComplete {
				return knowledgeEvidenceLayerSelection{}, false
			}
			if len(selectedCandidateIDs) == 1 {
				decision = knowledgeEvidenceDecisionDirectSingle
			} else {
				decision = knowledgeEvidenceDecisionDirectCombined
			}
			break
		}
		// Completeness must be checked against every grounded fact above, but the
		// customer-facing partial answer may contain only facts relevant to the
		// current task. Otherwise an unrelated clause in the same FAQ becomes a
		// mandatory coveredFact in Generate.
		if len(requiredKnowledgeEvidenceAspects(task)) > 0 {
			facts = finalizeKnowledgeEvidenceFactsForTask(task, facts)
			if len(facts) == 0 {
				return knowledgeEvidenceLayerSelection{}, false
			}
		}
	}
	return knowledgeEvidenceLayerSelection{
		Decision:             decision,
		DecisionSource:       "model_selected_repair",
		SelectedCandidateIDs: append([]string(nil), selectedCandidateIDs...),
		SupportedFacts:       facts,
		MissingAspects:       append([]string(nil), missingAspects...),
	}, true
}

func groundedKnowledgeEvidenceFacts(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string, facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	if len(facts) == 0 {
		return nil
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	evidenceUnits := make([][]string, 0, len(selectedCandidateIDs))
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		unit := knowledgeEvidenceCandidateGroundingParts(task, candidate)
		if len(unit) > 0 {
			evidenceUnits = append(evidenceUnits, unit)
		}
	}
	if len(evidenceUnits) == 0 {
		return nil
	}

	grounded := make([]knowledgeEvidenceFact, 0, len(facts))
	for _, fact := range facts {
		for _, evidenceUnit := range evidenceUnits {
			if knowledgeEvidenceFactGroundedForTask(task, fact, evidenceUnit) {
				grounded = append(grounded, fact)
				break
			}
		}
	}
	return grounded
}

func modelKnowledgeEvidenceFactsGroundEverySelectedCandidate(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selectedCandidateIDs []string,
	facts []knowledgeEvidenceFact,
) bool {
	if len(selectedCandidateIDs) == 0 || len(facts) == 0 {
		return false
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	factGrounded := make([]bool, len(facts))
	candidateGrounded := make(map[string]bool, len(selectedCandidateIDs))
	for _, candidate := range task.Candidates {
		candidateID := strings.TrimSpace(candidate.CandidateID)
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[candidateID]; !ok {
			continue
		}
		parts := knowledgeEvidenceCandidateGroundingParts(task, candidate)
		for index, fact := range facts {
			if knowledgeEvidenceFactGroundedForTask(task, fact, parts) {
				factGrounded[index] = true
				candidateGrounded[candidateID] = true
			}
		}
	}
	for _, grounded := range factGrounded {
		if !grounded {
			return false
		}
	}
	for candidateID := range selected {
		if !candidateGrounded[candidateID] {
			return false
		}
	}
	return true
}

func knowledgeEvidenceCandidateGroundingParts(task knowledgeEvidenceJudgeTask, candidate knowledgeEvidenceJudgeCandidate) []string {
	question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
	if !knowledgeEvidenceCandidateGroundingUnitMatchesSingleAspectSubject(task, question, answer) {
		return nil
	}
	parts := []string{answer}
	resolvedQuestion := false
	if statement, ok := resolvedKnowledgeEvidenceFAQQuestionStatement(task, question, answer); ok {
		parts = append(parts, statement)
		resolvedQuestion = true
	}
	if !resolvedQuestion && (knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) ||
		knowledgeEvidenceFAQAnswerCollectivelyCoversTaskSubjects(task, answer)) {
		parts = append(parts, strings.TrimSpace(question+" "+answer))
	}
	if strings.TrimSpace(question) == "" || strings.TrimSpace(answer) == "" {
		parts = []string{candidate.Hit.Content}
	}
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			ret = append(ret, part)
		}
	}
	return ret
}

func knowledgeEvidenceCandidateGroundingUnitMatchesSingleAspectSubject(task knowledgeEvidenceJudgeTask, question string, answer string) bool {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if subject, guarded := knowledgeEvidenceImplicitSingleExistenceSubject(task); guarded {
		requiredSubjects = []string{subject}
	}
	if len(requiredSubjects) != 1 {
		return true
	}
	requiredAspects := requiredKnowledgeEvidenceAspects(task)
	if !requiredKnowledgeEvidenceAspect(requiredAspects, "price") &&
		!requiredKnowledgeEvidenceAspect(requiredAspects, "time") &&
		!requiredKnowledgeEvidenceAspect(requiredAspects, "existence") {
		return true
	}
	if requiredKnowledgeEvidenceAspect(requiredAspects, "price") &&
		!knowledgeEvidenceCandidateMatchesImplicitSinglePriceSubject(task, knowledgeEvidenceJudgeCandidate{}, question, answer) {
		return false
	}
	if requiredKnowledgeEvidenceAspect(requiredAspects, "existence") &&
		!knowledgeEvidenceCandidateMatchesImplicitSingleExistenceSubject(task, knowledgeEvidenceJudgeCandidate{}, question, answer) {
		return false
	}
	unitText := normalizeKnowledgeEvidenceSubjectForMatch(question + " " + answer)
	return strings.Contains(unitText, requiredSubjects[0])
}

func knowledgeEvidenceFAQAnswerCollectivelyCoversTaskSubjects(task knowledgeEvidenceJudgeTask, answer string) bool {
	return knowledgeEvidenceFAQAnswerCollectivelyCoversTaskSubjectsForAspect(task, answer, "")
}

func knowledgeEvidenceFAQAnswerCollectivelyCoversTaskSubjectsForAspect(task knowledgeEvidenceJudgeTask, answer string, requiredAspect string) bool {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) < 2 {
		return false
	}
	if knowledgeEvidenceFAQAnswerUsesCompleteSubjectGroupAlias(task, answer, requiredAspect) {
		return true
	}
	requiredAspects := requiredKnowledgeEvidenceAspects(task)
	questionSubjectScopeAvailable := true
	for _, clause := range splitKnowledgeEvidenceAnswerClauses(answer) {
		compact := normalizeKnowledgeEvidenceSubjectForMatch(clause)
		if !knowledgeEvidenceClauseHasCollectivePredicate(compact) {
			if !knowledgeEvidenceCollectiveAnswerPreambleOnly(compact) {
				questionSubjectScopeAvailable = false
			}
			continue
		}
		clauseSubjects := knowledgeEvidenceContainedSubjects(compact, requiredSubjects)
		switch {
		case len(clauseSubjects) == len(requiredSubjects):
		case len(clauseSubjects) > 0:
			questionSubjectScopeAvailable = false
			continue
		case !questionSubjectScopeAvailable || !knowledgeEvidenceCollectiveClauseCanInheritQuestionSubjects(compact):
			questionSubjectScopeAvailable = false
			continue
		}
		if len(requiredAspects) == 0 {
			return true
		}
		if (requiredAspect == "existence" || (requiredAspect == "" && requiredKnowledgeEvidenceAspect(requiredAspects, "existence"))) &&
			containsAny(compact, []string{"都有", "均有", "均配", "均提供"}) {
			return true
		}
		for _, classified := range knowledgeEvidenceAnswerClauseAspects(clause) {
			if (requiredAspect != "" && classified.Aspect == requiredAspect) ||
				(requiredAspect == "" && requiredKnowledgeEvidenceAspect(requiredAspects, classified.Aspect)) {
				return true
			}
		}
		questionSubjectScopeAvailable = false
	}
	return false
}

func knowledgeEvidenceFAQAnswerUsesCompleteSubjectGroupAlias(task knowledgeEvidenceJudgeTask, answer string, requiredAspect string) bool {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) < 2 {
		return false
	}
	entityType := ""
	for _, entity := range task.Entities {
		subject := normalizeKnowledgeEvidenceSubjectForMatch(normalizeKnowledgeEvidenceEntityText(entity))
		if !knowledgeEvidenceContainsString(requiredSubjects, subject) {
			continue
		}
		currentType := strings.ToLower(strings.TrimSpace(entity.Type))
		if currentType == "" {
			return false
		}
		if entityType == "" {
			entityType = currentType
			continue
		}
		if entityType != currentType {
			return false
		}
	}
	if entityType == "" {
		return false
	}
	for _, clause := range splitKnowledgeEvidenceAnswerClauses(answer) {
		if knowledgeEvidenceCompleteSubjectGroupAliasClauseCoversTask(task, entityType, clause, requiredAspect) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceCompleteSubjectGroupAliasClauseCoversTask(task knowledgeEvidenceJudgeTask, entityType string, clause string, requiredAspect string) bool {
	compact := normalizeKnowledgeEvidenceSubjectForMatch(clause)
	aliases := knowledgeEvidenceCompleteSubjectGroupAliases(entityType)
	if compact == "" || len(aliases) == 0 || !containsAny(compact, aliases) || containsAny(compact, []string{
		"部分平台", "某个平台", "某些平台", "有的平台", "其他平台",
		"部分房型", "某个房型", "某些房型", "有的房型", "其他房型",
		"部分用品", "某个用品", "某些用品", "有的用品", "其他用品",
		"部分设施", "某个设施", "某些设施", "有的设施", "其他设施",
	}) {
		return false
	}

	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	requiredAspects := requiredKnowledgeEvidenceAspects(task)
	for _, classified := range knowledgeEvidenceAnswerClauseAspects(clause) {
		if (requiredAspect != "" && classified.Aspect != requiredAspect) ||
			(requiredAspect == "" && !requiredKnowledgeEvidenceAspect(requiredAspects, classified.Aspect)) {
			continue
		}
		if classified.Aspect == "price" && !knowledgeEvidencePriceAliasClauseMatchesTaskPredicate(task.Query, clause) {
			continue
		}
		withoutAlias := clause
		for _, alias := range aliases {
			withoutAlias = strings.ReplaceAll(withoutAlias, alias, "")
		}
		if !knowledgeEvidenceAspectClauseHasForeignSubject(classified.Aspect, withoutAlias, requiredSubjects) {
			return true
		}
	}
	if requiredAspect == "price" || (requiredAspect == "" && requiredKnowledgeEvidenceAspect(requiredAspects, "price")) {
		claims := knowledgeEvidencePriceClaims(clause)
		for _, classified := range knowledgeEvidenceAnswerClauseAspects(clause) {
			if knowledgeEvidenceQueryAsksComparison(task.Query) {
				if (classified.Aspect == "condition" || classified.Aspect == "scope") &&
					knowledgeEvidencePriceClaimsContain(claims, "dynamic") {
					return true
				}
				if classified.Aspect == "method" && containsAny(compact, []string{"对比", "比较", "选择"}) {
					return true
				}
			}
			if knowledgeEvidenceQueryAsksPriceBoundary(task.Query) {
				if (classified.Aspect == "condition" || classified.Aspect == "scope") &&
					containsAny(compact, []string{"平台", "权益", "调整", "情况", "为准", "而定", "取决于"}) {
					return true
				}
				if classified.Aspect == "method" && containsAny(compact, []string{"对比", "比较", "选择", "联系"}) {
					return true
				}
			}
		}
	}
	return false
}

func knowledgeEvidencePriceAliasClauseMatchesTaskPredicate(query string, clause string) bool {
	claims := knowledgeEvidencePriceClaims(clause)
	if knowledgeEvidenceQueryAsksDirectionalPriceComparison(query) {
		return knowledgeEvidencePriceClaimsContain(claims, "cheaper") ||
			knowledgeEvidencePriceClaimsContain(claims, "dearer") ||
			knowledgeEvidencePriceClaimCount(claims, "amount") >= 2
	}
	if knowledgeEvidenceQueryAsksComparison(query) {
		if knowledgeEvidencePriceClaimsContain(claims, "equal") ||
			knowledgeEvidencePriceClaimsContain(claims, "not_equal") ||
			knowledgeEvidencePriceClaimCount(claims, "amount") >= 2 {
			return true
		}
		return knowledgeEvidencePriceClaimsContain(claims, "free") &&
			knowledgeEvidenceClauseHasCollectivePredicate(normalizeRuntimeKnowledgeQuery(clause))
	}
	if knowledgeEvidenceQueryAsksAbsolutePriceStatus(query) || knowledgeEvidenceQueryAsksPriceAmount(query) {
		return knowledgeEvidencePriceClaimsContain(claims, "free") ||
			knowledgeEvidencePriceClaimsContain(claims, "charged") ||
			knowledgeEvidencePriceClaimsContain(claims, "amount")
	}
	if knowledgeEvidenceQueryAsksPriceBoundary(query) {
		return len(claims) > 0
	}
	return len(claims) > 0
}

func knowledgeEvidenceCompleteSubjectGroupAliases(entityType string) []string {
	switch entityType {
	case "company", "platform":
		return []string{"不同平台", "各个平台", "各平台", "这些平台", "上述平台", "平台之间", "每个平台"}
	case "room_type":
		return []string{"不同房型", "各个房型", "各房型", "这些房型", "上述房型", "房型之间", "每种房型"}
	case "supply":
		return []string{"各类用品", "各项用品", "这些用品", "上述用品", "所有用品"}
	case "facility":
		return []string{"各类设施", "各项设施", "这些设施", "上述设施", "所有设施"}
	default:
		return nil
	}
}

func knowledgeEvidenceClauseHasCollectivePredicate(compact string) bool {
	return containsAny(compact, []string{"都是", "都有", "均有", "均为", "均是", "均配", "均提供", "全部", "两者", "二者", "各自", "分别"})
}

func knowledgeEvidenceCollectiveAnswerPreambleOnly(compact string) bool {
	switch strings.Trim(compact, "，,。.!！?？；;：:") {
	case "", "是", "是的", "对", "对的", "没错", "确实", "可以", "有的", "答案是", "回复是":
		return true
	default:
		return false
	}
}

func knowledgeEvidenceCollectiveClauseCanInheritQuestionSubjects(compact string) bool {
	earliest := -1
	for _, marker := range []string{"都是", "都有", "均有", "均为", "均是", "均配", "均提供", "全部", "两者", "二者", "各自", "分别"} {
		if index := strings.Index(compact, marker); index >= 0 && (earliest < 0 || index < earliest) {
			earliest = index
		}
	}
	if earliest < 0 {
		return false
	}
	prefix := strings.Trim(compact[:earliest], "，,。.!！?？；;：:")
	return knowledgeEvidenceCollectiveAnswerPreambleOnly(prefix)
}

func knowledgeEvidenceFactGroundedForTask(task knowledgeEvidenceJudgeTask, fact knowledgeEvidenceFact, evidenceParts []string) bool {
	evidenceParts = knowledgeEvidenceGroundingPartsCompatibleWithFact(fact, evidenceParts)
	if len(evidenceParts) == 0 {
		return false
	}
	if !knowledgeEvidenceFactSubjectClaimsGroundedByParts(task, fact, evidenceParts) {
		return false
	}
	if !knowledgeEvidenceFactQuantityBindingsGroundedByParts(task, fact, evidenceParts) {
		return false
	}
	if !knowledgeEvidencePriceFactPredicateGroundedByParts(fact, evidenceParts) {
		return false
	}
	if !knowledgeEvidenceFactAspectBindingsGroundedByParts(task, fact, evidenceParts) {
		return false
	}
	return knowledgeEvidenceFactGroundedByText(fact, evidenceParts)
}

func knowledgeEvidenceGroundingPartsCompatibleWithFact(fact knowledgeEvidenceFact, evidenceParts []string) []string {
	ret := make([]string, 0, len(evidenceParts)*2)
	for _, part := range evidenceParts {
		propositions := splitKnowledgeEvidenceGroundingPropositions(part)
		if len(propositions) == 0 && strings.TrimSpace(part) != "" {
			propositions = []string{part}
		}
		for _, proposition := range propositions {
			units := []string{proposition}
			clauses := splitKnowledgeEvidenceAnswerClauses(proposition)
			qualifiedClauses := 0
			for _, clause := range clauses {
				if len(knowledgeEvidenceBindingQualifierSignatures(clause)) > 0 {
					qualifiedClauses++
				}
			}
			// Split only unqualified prose or genuinely parallel qualified clauses.
			// A single trailing qualifier (for example "可以办理，仅限退房前")
			// governs the preceding proposition and must not be stripped away.
			if len(knowledgeEvidenceBindingQualifierSignatures(proposition)) == 0 || qualifiedClauses > 1 {
				units = append(units, clauses...)
			} else if strings.TrimSpace(fact.Aspect) == "time" {
				// A clock period such as "晚上" qualifies only its own time slot in
				// "早餐早上七点开始，晚上九点结束". Keep those concrete clock
				// clauses independently groundable without weakening non-time trailing
				// restrictions such as "仅限退房前".
				for _, clause := range clauses {
					if knowledgeEvidenceIndividualTimePattern.MatchString(clause) {
						units = append(units, clause)
					}
				}
			}
			for _, unit := range units {
				unit = strings.TrimSpace(unit)
				if unit == "" || !knowledgeEvidenceFactPreservesBindingQualifiers(fact, unit) {
					continue
				}
				ret = appendIfMissing(ret, unit)
			}
		}
	}
	return ret
}

func splitKnowledgeEvidenceGroundingPropositions(text string) []string {
	parts := strings.FieldsFunc(strings.TrimSpace(text), func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '.', '！', '!', '？', '?', '；', ';':
			return true
		default:
			return false
		}
	})
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			ret = append(ret, part)
		}
	}
	return ret
}

func knowledgeEvidenceFactPreservesBindingQualifiers(fact knowledgeEvidenceFact, evidence string) bool {
	required := knowledgeEvidenceBindingQualifierSignatures(evidence)
	factText := strings.TrimSpace(fact.Statement + " " + strings.Join(fact.CriticalValues, " "))
	actual := knowledgeEvidenceBindingQualifierSignatures(factText)
	for _, signature := range required {
		if !knowledgeEvidenceContainsString(actual, signature) {
			return false
		}
	}
	for _, signature := range actual {
		if !knowledgeEvidenceContainsString(required, signature) {
			return false
		}
	}
	return true
}

func knowledgeEvidenceBindingQualifierSignatures(text string) []string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	if compact == "" {
		return nil
	}
	ret := make([]string, 0, 3)
	for _, item := range []struct {
		signature string
		markers   []string
	}{
		{signature: "before_checkout", markers: []string{"退房前", "离店前"}},
		{signature: "after_checkout", markers: []string{"退房后", "离店后"}},
		{signature: "before_checkin", markers: []string{"入住前", "到店前"}},
		{signature: "after_checkin", markers: []string{"入住后", "登记后", "办理入住后"}},
		{signature: "during_stay", markers: []string{"入住期间", "住店期间", "在住期间"}},
		{signature: "checkin_day", markers: []string{"入住当天", "入住当日"}},
		{signature: "checkout_day", markers: []string{"退房当天", "退房当日", "离店当天", "离店当日"}},
	} {
		if containsAny(compact, item.markers) {
			ret = appendIfMissing(ret, item.signature)
		}
	}
	if containsAny(compact, []string{"当天", "当日"}) &&
		!knowledgeEvidenceContainsString(ret, "checkin_day") && !knowledgeEvidenceContainsString(ret, "checkout_day") {
		ret = appendIfMissing(ret, "same_day")
	}
	for _, condition := range knowledgeEvidenceConflictConditions(compact) {
		ret = appendIfMissing(ret, "condition:"+condition)
	}
	if target, ok := knowledgeEvidenceBindingRestrictionTarget(compact); ok {
		ret = appendIfMissing(ret, "restricted:"+target)
	}
	return ret
}

func knowledgeEvidenceBindingRestrictionTarget(compact string) (string, bool) {
	markerIndex := -1
	markerLength := 0
	for _, marker := range []string{"仅限", "只限", "限于", "只能", "仅可", "只可", "必须在", "须在", "需在", "仅对", "只对", "只有"} {
		if index := strings.Index(compact, marker); index >= 0 && (markerIndex < 0 || index < markerIndex) {
			markerIndex = index
			markerLength = len(marker)
		}
	}
	if markerIndex < 0 {
		return "", false
	}
	target := strings.Trim(compact[markerIndex+markerLength:], "在于，,。.!！?？；;：:")
	for _, boundary := range []string{"才可以", "才可", "可以", "可使用", "使用", "享受", "办理", "领取", "参加", "兑换", "预订", "提供", "开放", "适用"} {
		if index := strings.Index(target, boundary); index > 0 {
			target = target[:index]
		}
	}
	target = strings.Trim(target, "在于，,。.!！?？；;：:")
	switch {
	case strings.Contains(target, "非会员"):
		return "non_member", true
	case strings.Contains(target, "会员"):
		return "member", true
	case strings.Contains(target, "住店客人") || strings.Contains(target, "住客") || strings.Contains(target, "入住客人"):
		return "staying_guest", true
	}
	if target == "" {
		return normalizeRuntimeKnowledgeQuery(compact), true
	}
	runes := []rune(target)
	if len(runes) > 16 {
		target = string(runes[:16])
	}
	return target, true
}

func knowledgeEvidenceFAQAnswerHasBindingQualifier(answer string) bool {
	return len(knowledgeEvidenceBindingQualifierSignatures(answer)) > 0
}

func knowledgeEvidenceFactSubjectClaimsGroundedByParts(task knowledgeEvidenceJudgeTask, fact knowledgeEvidenceFact, evidenceParts []string) bool {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) < 2 {
		return true
	}
	requiredAspects := requiredKnowledgeEvidenceAspects(task)
	factCoversRequiredAspect := false
	for _, aspect := range requiredAspects {
		if knowledgeEvidenceFactSupportsAspect(fact, aspect) {
			factCoversRequiredAspect = true
			break
		}
	}
	if !factCoversRequiredAspect {
		return true
	}

	factText := normalizeKnowledgeEvidenceSubjectForMatch(fact.Statement)
	evidenceText := normalizeKnowledgeEvidenceSubjectForMatch(strings.Join(evidenceParts, " "))
	mentionedSubjects := 0
	for _, subject := range requiredSubjects {
		if subject == "" || !strings.Contains(factText, subject) {
			continue
		}
		mentionedSubjects++
		if !strings.Contains(evidenceText, subject) {
			return false
		}
	}
	if mentionedSubjects < len(requiredSubjects) && containsAny(factText, []string{"都", "均", "全部", "两者", "二者", "各自", "分别"}) {
		for _, subject := range requiredSubjects {
			if subject != "" && !strings.Contains(evidenceText, subject) {
				return false
			}
		}
	}
	return true
}

func knowledgeEvidenceFactQuantityBindingsGroundedByParts(task knowledgeEvidenceJudgeTask, fact knowledgeEvidenceFact, evidenceParts []string) bool {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) == 0 || len(knowledgeEvidenceStrictQuantityPattern.FindAllString(fact.Statement, -1)) == 0 {
		return true
	}
	bindingsChecked := 0
	for _, subject := range requiredSubjects {
		for _, occurrence := range knowledgeEvidenceQuantityOccurrences(fact.Statement, subject) {
			allowImplicit := len(requiredSubjects) == 1
			if occurrence.SubjectRelation != "required" && !(allowImplicit && occurrence.SubjectRelation == "implicit") {
				continue
			}
			bindingsChecked++
			if !knowledgeEvidencePartsContainSubjectQuantityBinding(evidenceParts, subject, occurrence.Value, allowImplicit) {
				return false
			}
		}
	}
	return bindingsChecked > 0
}

func knowledgeEvidencePartsContainSubjectQuantityBinding(parts []string, subject string, expected string, allowImplicit bool) bool {
	for _, part := range parts {
		units := append([]string{part}, splitKnowledgeEvidenceAnswerClauses(part)...)
		for _, unit := range units {
			for _, occurrence := range knowledgeEvidenceQuantityOccurrences(unit, subject) {
				bindingMatches := occurrence.SubjectRelation == "required" || (allowImplicit && occurrence.SubjectRelation == "implicit")
				if bindingMatches && knowledgeEvidenceCriticalValuesEquivalent(occurrence.Value, expected) {
					return true
				}
			}
		}
	}
	return false
}

type knowledgeEvidenceSubjectAspectBinding struct {
	Subject string
	Values  []string
}

type knowledgeEvidencePriceClaim struct {
	Kind  string
	Value string
}

func knowledgeEvidencePriceClaims(text string) []knowledgeEvidencePriceClaim {
	clauses := splitKnowledgeEvidenceAnswerClauses(text)
	if len(clauses) == 0 {
		clauses = []string{strings.TrimSpace(text)}
	}
	ret := make([]knowledgeEvidencePriceClaim, 0, 4)
	for _, clause := range clauses {
		compact := normalizeRuntimeKnowledgeQuery(clause)
		if compact == "" {
			continue
		}
		dynamic := containsAny(compact, []string{
			"免费政策", "收费政策", "价格政策", "费用政策", "收费标准", "平台权益", "平台的权益", "权益不一样", "权益不同", "价格实时调整",
			"价格自动调整", "价格会调整", "价格会变动", "价格可能调整", "以平台为准", "以当天为准",
		})
		if dynamic {
			ret = appendKnowledgeEvidencePriceClaim(ret, knowledgeEvidencePriceClaim{Kind: "dynamic"})
		}

		switch {
		case containsAny(compact, []string{
			"价格不一样", "费用不一样", "收费不一样", "金额不一样", "价位不一样",
			"价格是不一样", "费用是不一样", "收费是不一样", "金额是不一样", "价位是不一样",
			"价格不同", "费用不同", "收费不同", "金额不同", "价位不同",
			"价格有区别", "费用有区别", "收费有区别", "存在价差",
		}):
			ret = appendKnowledgeEvidencePriceClaim(ret, knowledgeEvidencePriceClaim{Kind: "not_equal"})
		case containsAny(compact, []string{
			"价格一样", "费用一样", "收费一样", "金额一样", "价位一样",
			"价格是一样", "费用是一样", "收费是一样", "金额是一样", "价位是一样",
			"价格相同", "费用相同", "收费相同", "金额相同", "价位相同", "同价",
		}):
			ret = appendKnowledgeEvidencePriceClaim(ret, knowledgeEvidencePriceClaim{Kind: "equal"})
		}
		if containsAny(compact, []string{"更便宜", "价格更低", "费用更低", "收费更低", "更划算"}) {
			ret = appendKnowledgeEvidencePriceClaim(ret, knowledgeEvidencePriceClaim{Kind: "cheaper"})
		}
		if containsAny(compact, []string{"更贵", "价格更高", "费用更高", "收费更高"}) {
			ret = appendKnowledgeEvidencePriceClaim(ret, knowledgeEvidencePriceClaim{Kind: "dearer"})
		}

		for _, value := range knowledgeEvidencePriceValuePattern.FindAllString(clause, -1) {
			ret = appendKnowledgeEvidencePriceClaim(ret, knowledgeEvidencePriceClaim{
				Kind:  "amount",
				Value: normalizeRuntimeKnowledgeQuery(value),
			})
		}

		partialBoundary := containsAny(compact, []string{
			"不一定免费", "不一定收费", "不是都免费", "并非都免费", "并不是都免费", "不全免费",
			"部分免费", "部分收费", "有的平台免费", "有的平台收费", "有的免费", "有的收费",
		})
		if partialBoundary || dynamic {
			continue
		}
		switch {
		case containsAny(compact, []string{
			"不收费", "无需收费", "不需收费", "不用收费", "不需要收费",
			"无需付费", "不需付费", "不用付费", "不需要付费", "不付费", "不要钱",
		}):
			ret = appendKnowledgeEvidencePriceClaim(ret, knowledgeEvidencePriceClaim{Kind: "free"})
		case containsAny(compact, []string{
			"不免费", "并非免费", "并不是免费", "不是免费", "需要付费", "需付费", "要付费", "必须付费", "要收费", "需要收费", "需收费", "要钱",
		}):
			ret = appendKnowledgeEvidencePriceClaim(ret, knowledgeEvidencePriceClaim{Kind: "charged"})
		default:
			if strings.Contains(compact, "免费") {
				ret = appendKnowledgeEvidencePriceClaim(ret, knowledgeEvidencePriceClaim{Kind: "free"})
			}
			if strings.Contains(compact, "收费") || strings.Contains(compact, "付费") {
				ret = appendKnowledgeEvidencePriceClaim(ret, knowledgeEvidencePriceClaim{Kind: "charged"})
			}
		}
	}
	return ret
}

func appendKnowledgeEvidencePriceClaim(values []knowledgeEvidencePriceClaim, claim knowledgeEvidencePriceClaim) []knowledgeEvidencePriceClaim {
	claim.Kind = strings.TrimSpace(claim.Kind)
	claim.Value = strings.TrimSpace(claim.Value)
	if claim.Kind == "" {
		return values
	}
	for _, existing := range values {
		if existing.Kind == claim.Kind && existing.Value == claim.Value {
			return values
		}
	}
	return append(values, claim)
}

func knowledgeEvidencePriceClaimsContain(claims []knowledgeEvidencePriceClaim, kind string) bool {
	for _, claim := range claims {
		if claim.Kind == kind {
			return true
		}
	}
	return false
}

func knowledgeEvidencePriceClaimsContainClaim(claims []knowledgeEvidencePriceClaim, wanted knowledgeEvidencePriceClaim) bool {
	for _, claim := range claims {
		if claim.Kind == wanted.Kind && (wanted.Value == "" || claim.Value == wanted.Value) {
			return true
		}
	}
	return false
}

func knowledgeEvidencePriceCriticalValues(text string) []string {
	ret := make([]string, 0, 3)
	for _, claim := range knowledgeEvidencePriceClaims(text) {
		switch claim.Kind {
		case "free":
			ret = appendIfMissing(ret, "免费")
		case "charged":
			ret = appendIfMissing(ret, "收费")
		case "amount":
			ret = appendIfMissing(ret, claim.Value)
		case "equal":
			ret = appendIfMissing(ret, "价格相同")
		case "not_equal":
			ret = appendIfMissing(ret, "价格不同")
		}
	}
	return ret
}

func knowledgeEvidencePriceFactPredicateGroundedByParts(fact knowledgeEvidenceFact, evidenceParts []string) bool {
	if strings.TrimSpace(fact.Aspect) != "price" {
		return true
	}
	expected := knowledgeEvidencePriceClaims(fact.Statement + " " + strings.Join(fact.CriticalValues, " "))
	if len(expected) == 0 {
		return true
	}
	actual := make([]knowledgeEvidencePriceClaim, 0, len(expected))
	for _, part := range evidenceParts {
		for _, claim := range knowledgeEvidencePriceClaims(part) {
			actual = appendKnowledgeEvidencePriceClaim(actual, claim)
		}
	}
	for _, claim := range expected {
		if !knowledgeEvidencePriceClaimsContainClaim(actual, claim) {
			return false
		}
	}
	return true
}

func knowledgeEvidenceFactAspectBindingsGroundedByParts(task knowledgeEvidenceJudgeTask, fact knowledgeEvidenceFact, evidenceParts []string) bool {
	if fact.Aspect != "price" && fact.Aspect != "time" {
		return true
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) == 0 {
		return true
	}
	expected, ambiguous := knowledgeEvidenceSubjectAspectBindings(
		fact.Statement,
		requiredSubjects,
		fact.Aspect,
		fact.CriticalValues,
	)
	if ambiguous {
		statement := normalizeRuntimeKnowledgeQuery(fact.Statement)
		for _, part := range evidenceParts {
			if statement != "" && strings.Contains(normalizeRuntimeKnowledgeQuery(part), statement) {
				return true
			}
		}
		return false
	}
	if len(expected) == 0 {
		return true
	}
	actual := make(map[string][]string, len(requiredSubjects))
	for _, part := range evidenceParts {
		bindings, partAmbiguous := knowledgeEvidenceSubjectAspectBindings(part, requiredSubjects, fact.Aspect, nil)
		if partAmbiguous {
			continue
		}
		for _, binding := range bindings {
			actual[binding.Subject] = appendKnowledgeEvidenceBindingValues(actual[binding.Subject], binding.Values)
		}
	}
	for _, binding := range expected {
		for _, value := range binding.Values {
			if !knowledgeEvidenceBindingValuesContain(actual[binding.Subject], value) {
				return false
			}
		}
	}
	return true
}

func knowledgeEvidenceSubjectAspectBindings(
	text string,
	requiredSubjects []string,
	aspect string,
	fallbackValues []string,
) ([]knowledgeEvidenceSubjectAspectBinding, bool) {
	if len(requiredSubjects) == 0 {
		return nil, false
	}
	bindings := make(map[string][]string, len(requiredSubjects))
	activeSubjects := []string(nil)
	clauses := splitKnowledgeEvidenceAnswerClauses(text)
	if len(clauses) == 0 {
		clauses = []string{strings.TrimSpace(text)}
	}
	for _, clause := range clauses {
		clauseText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
		clauseSubjects := knowledgeEvidenceContainedSubjects(clauseText, requiredSubjects)
		values := knowledgeEvidenceAspectBindingValues(aspect, clause)
		if len(clauseSubjects) > 0 {
			activeSubjects = append([]string(nil), clauseSubjects...)
		}
		if len(values) == 0 {
			continue
		}
		if len(clauseSubjects) == 0 {
			switch {
			case len(activeSubjects) > 0 && !knowledgeEvidenceAspectClauseHasForeignSubject(aspect, clause, requiredSubjects):
				clauseSubjects = activeSubjects
			case len(requiredSubjects) == 1 && !knowledgeEvidenceAspectClauseHasForeignSubject(aspect, clause, requiredSubjects):
				clauseSubjects = requiredSubjects
			case len(requiredSubjects) > 1 && knowledgeEvidenceClauseHasCollectivePredicate(clauseText):
				clauseSubjects = requiredSubjects
			default:
				continue
			}
		}
		switch {
		case len(clauseSubjects) == 1:
			bindings[clauseSubjects[0]] = appendKnowledgeEvidenceBindingValues(bindings[clauseSubjects[0]], values)
		case len(values) == 1 && knowledgeEvidenceClauseHasCollectivePredicate(clauseText):
			for _, subject := range clauseSubjects {
				bindings[subject] = appendKnowledgeEvidenceBindingValues(bindings[subject], values)
			}
		case len(values) == len(clauseSubjects) && containsAny(clauseText, []string{"分别", "各自"}):
			for index, subject := range knowledgeEvidenceSubjectsInTextOrder(clause, clauseSubjects) {
				bindings[subject] = appendKnowledgeEvidenceBindingValues(bindings[subject], []string{values[index]})
			}
		default:
			return nil, true
		}
	}
	if len(bindings) == 0 && len(fallbackValues) > 0 {
		values := knowledgeEvidenceAspectBindingValues(aspect, strings.Join(fallbackValues, " "))
		switch {
		case len(requiredSubjects) == 1:
			bindings[requiredSubjects[0]] = appendKnowledgeEvidenceBindingValues(bindings[requiredSubjects[0]], values)
		case len(values) == 1 && knowledgeEvidenceClauseHasCollectivePredicate(normalizeKnowledgeEvidenceSubjectForMatch(text)):
			for _, subject := range requiredSubjects {
				bindings[subject] = appendKnowledgeEvidenceBindingValues(bindings[subject], values)
			}
		}
	}
	ret := make([]knowledgeEvidenceSubjectAspectBinding, 0, len(bindings))
	for _, subject := range requiredSubjects {
		if len(bindings[subject]) == 0 {
			continue
		}
		ret = append(ret, knowledgeEvidenceSubjectAspectBinding{Subject: subject, Values: bindings[subject]})
	}
	return ret, false
}

func knowledgeEvidenceSubjectsInTextOrder(text string, subjects []string) []string {
	type positionedSubject struct {
		subject string
		index   int
	}
	compact := normalizeKnowledgeEvidenceSubjectForMatch(text)
	positioned := make([]positionedSubject, 0, len(subjects))
	for _, subject := range subjects {
		if index := strings.Index(compact, subject); index >= 0 {
			positioned = append(positioned, positionedSubject{subject: subject, index: index})
		}
	}
	for left := 0; left < len(positioned); left++ {
		for right := left + 1; right < len(positioned); right++ {
			if positioned[right].index < positioned[left].index {
				positioned[left], positioned[right] = positioned[right], positioned[left]
			}
		}
	}
	ret := make([]string, 0, len(positioned))
	for _, item := range positioned {
		ret = append(ret, item.subject)
	}
	return ret
}

func knowledgeEvidenceAspectBindingValues(aspect string, text string) []string {
	ret := make([]string, 0, 4)
	switch aspect {
	case "price":
		for _, claim := range knowledgeEvidencePriceClaims(text) {
			switch claim.Kind {
			case "amount":
				ret = appendIfMissing(ret, claim.Value)
			case "free", "charged", "equal", "not_equal", "cheaper", "dearer", "dynamic":
				ret = appendIfMissing(ret, claim.Kind)
			}
		}
	case "time":
		for _, value := range knowledgeEvidenceIndividualTimePattern.FindAllString(text, -1) {
			ret = appendIfMissing(ret, normalizeKnowledgeEvidenceClockTime(value))
		}
		for _, value := range knowledgeEvidenceDurationValuePattern.FindAllString(text, -1) {
			ret = appendIfMissing(ret, normalizeRuntimeKnowledgeQuery(value))
		}
	}
	return ret
}

func knowledgeEvidenceAspectClauseHasForeignSubject(aspect string, clause string, requiredSubjects []string) bool {
	clauseText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
	if len(knowledgeEvidenceContainedSubjects(clauseText, requiredSubjects)) > 0 {
		return false
	}
	switch aspect {
	case "time":
		return knowledgeEvidenceTimeFactHasExplicitSubject(clause)
	case "price":
		markerIndex := -1
		for _, marker := range []string{"并不是免费", "并非免费", "不是免费", "不免费", "不收费", "免费", "收费", "价格", "费用", "金额", "元", "块", "折"} {
			if index := strings.Index(clauseText, marker); index >= 0 && (markerIndex < 0 || index < markerIndex) {
				markerIndex = index
			}
		}
		if markerIndex <= 0 {
			return false
		}
		prefix := strings.Trim(clauseText[:markerIndex], "的了都均为是，,。；;！!？?")
		for _, discoursePrefix := range []string{"但是", "不过", "其实", "实际", "是的", "对的", "但"} {
			prefix = strings.TrimPrefix(prefix, discoursePrefix)
		}
		for _, marker := range []string{"工作日", "周末", "节假日", "每天", "每日", "目前", "现在"} {
			prefix = strings.ReplaceAll(prefix, marker, "")
		}
		prefix = knowledgeEvidenceStrictQuantityPattern.ReplaceAllString(prefix, "")
		prefix = strings.NewReplacer(
			"每个房间", "", "每间房", "", "房间内", "", "房内", "", "客房内", "",
			"本房间", "", "酒店内", "", "门店内", "", "本店内", "", "店内", "",
		).Replace(prefix)
		prefix = strings.Trim(prefix, "的了都均为是，,。；;！!？?")
		return len([]rune(strings.TrimSpace(prefix))) >= 2
	default:
		return false
	}
}

func appendKnowledgeEvidenceBindingValues(existing []string, values []string) []string {
	ret := append([]string(nil), existing...)
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !knowledgeEvidenceBindingValuesContain(ret, value) {
			ret = append(ret, value)
		}
	}
	return ret
}

func knowledgeEvidenceBindingValuesContain(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(expected) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFactGroundedByText(fact knowledgeEvidenceFact, evidenceParts []string) bool {
	statement := normalizeRuntimeKnowledgeQuery(fact.Statement)
	if statement == "" {
		return false
	}
	for _, value := range fact.CriticalValues {
		if !knowledgeEvidenceCriticalValueGroundedByParts(fact, value, evidenceParts) {
			return false
		}
	}
	// Price wording has many polarity-preserving equivalents (for example
	// "免费", "不收费" and "无需付费"). Once canonical price claims and
	// critical values agree, generic lexical polarity and n-gram checks would
	// only reject valid synonyms. Task-level subject and scope grounding has
	// already run before this helper on the production path.
	if strings.TrimSpace(fact.Aspect) == "price" &&
		len(knowledgeEvidencePriceClaims(fact.Statement+" "+strings.Join(fact.CriticalValues, " "))) > 0 {
		return knowledgeEvidencePriceFactPredicateGroundedByParts(fact, evidenceParts)
	}

	bestSimilarity := 0.0
	polarityMatched := false
	statementNegative := knowledgeEvidenceTextHasNegativeBoundary(statement)
	for _, part := range evidenceParts {
		for _, unit := range append([]string{part}, splitKnowledgeEvidenceAnswerClauses(part)...) {
			normalizedUnit := normalizeRuntimeKnowledgeQuery(unit)
			if normalizedUnit == "" {
				continue
			}
			if strings.Contains(normalizedUnit, statement) || strings.Contains(statement, normalizedUnit) {
				if statementNegative == knowledgeEvidenceTextHasNegativeBoundary(normalizedUnit) {
					return true
				}
				continue
			}
			similarity := knowledgeEvidenceTextNGramSimilarity(statement, normalizedUnit)
			if similarity > bestSimilarity {
				bestSimilarity = similarity
				polarityMatched = statementNegative == knowledgeEvidenceTextHasNegativeBoundary(normalizedUnit)
			}
		}
	}
	minimumSimilarity := 0.46
	if len(fact.CriticalValues) > 0 {
		minimumSimilarity = 0.28
	}
	return polarityMatched && bestSimilarity >= minimumSimilarity
}

func knowledgeEvidenceCriticalValueGroundedByParts(fact knowledgeEvidenceFact, value string, evidenceParts []string) bool {
	normalizedValue := normalizeRuntimeKnowledgeQuery(value)
	if normalizedValue == "" {
		return false
	}
	if strings.TrimSpace(fact.Aspect) == "price" {
		expected := knowledgeEvidencePriceClaims(value)
		if len(expected) > 0 {
			actual := make([]knowledgeEvidencePriceClaim, 0, len(expected))
			for _, part := range evidenceParts {
				for _, claim := range knowledgeEvidencePriceClaims(part) {
					actual = appendKnowledgeEvidencePriceClaim(actual, claim)
				}
			}
			for _, claim := range expected {
				if !knowledgeEvidencePriceClaimsContainClaim(actual, claim) {
					return false
				}
			}
			return true
		}
	}
	return knowledgeEvidencePartsContainValue(evidenceParts, normalizedValue)
}

func knowledgeEvidencePartsContainValue(parts []string, normalizedValue string) bool {
	for _, part := range parts {
		if strings.Contains(normalizeRuntimeKnowledgeQuery(part), normalizedValue) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFAQAnswerConfirmsQuestion(answer string) bool {
	answer = strings.TrimSpace(answer)
	for _, prefix := range []string{"是的", "对的", "没错", "有的"} {
		if !strings.HasPrefix(answer, prefix) {
			continue
		}
		remainder := strings.TrimSpace(strings.TrimPrefix(answer, prefix))
		if remainder == "" || strings.ContainsRune("，,。.!！；;：:", []rune(remainder)[0]) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task knowledgeEvidenceJudgeTask, question string, answer string) bool {
	if !knowledgeEvidenceFAQAnswerConfirmsQuestion(answer) {
		return false
	}
	if _, guidance := knowledgeEvidenceGuidanceRequirement(answer); guidance != "" {
		return false
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if knowledgeEvidenceFAQAnswerMentionsOnlyPartOfRequiredSubjects(answer, requiredSubjects) {
		return false
	}
	requiredAspects := requiredKnowledgeEvidenceAspects(task)
	queryQuantities := knowledgeEvidenceTaskBoundCriticalValues(question)
	clauses := splitKnowledgeEvidenceAnswerClauses(answer)
	for _, clause := range clauses {
		if knowledgeEvidenceTextHasUncertaintyBoundary(clause) {
			clauseText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
			for _, subject := range requiredSubjects {
				if subject != "" && strings.Contains(clauseText, subject) {
					return false
				}
			}
			if knowledgeEvidenceUncertaintyTouchesRequiredAspect(requiredAspects, clause) {
				return false
			}
		}
		if !knowledgeEvidenceTextHasNegativeBoundary(clause) {
			continue
		}
		clauseText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
		for _, subject := range requiredSubjects {
			if subject != "" && strings.Contains(clauseText, subject) {
				return false
			}
		}
		if knowledgeEvidenceNegativeClauseTouchesRequiredAspect(requiredAspects, clause) {
			return false
		}
		anchor := normalizeKnowledgeEvidenceSubjectForMatch(knowledgeEvidenceNegativeBoundaryAnchor(clause))
		questionText := normalizeKnowledgeEvidenceSubjectForMatch(question)
		if anchor != "" && (strings.Contains(questionText, anchor) || strings.Contains(anchor, questionText)) {
			return false
		}
		if containsAny(anchor, []string{"提供", "服务", "支持", "安排", "办理"}) &&
			!containsAny(clauseText, []string{"预约", "押金", "付费", "收费", "费用", "房卡", "刷脸"}) {
			return false
		}
		for _, expected := range queryQuantities {
			expectedUnit := knowledgeEvidenceQuantityUnit(expected)
			for _, actual := range knowledgeEvidenceTaskBoundCriticalValues(clause) {
				if knowledgeEvidenceQuantityUnit(actual) != expectedUnit {
					continue
				}
				if !knowledgeEvidenceCriticalValuesEquivalent(actual, expected) || strings.Contains(clauseText, normalizeRuntimeKnowledgeQuery(expected)) {
					return false
				}
			}
		}
	}
	return true
}

func knowledgeEvidenceNegativeClauseTouchesRequiredAspect(requiredAspects []string, clause string) bool {
	for _, classified := range knowledgeEvidenceAnswerClauseAspects(clause) {
		if classified.Aspect == "existence" || classified.Aspect == "other" {
			continue
		}
		if requiredKnowledgeEvidenceAspect(requiredAspects, classified.Aspect) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFAQAnswerMentionsOnlyPartOfRequiredSubjects(answer string, requiredSubjects []string) bool {
	if len(requiredSubjects) < 2 {
		return false
	}
	answerText := normalizeKnowledgeEvidenceSubjectForMatch(answer)
	mentioned := 0
	for _, subject := range requiredSubjects {
		if subject != "" && strings.Contains(answerText, subject) {
			mentioned++
		}
	}
	return mentioned > 0 && mentioned < len(requiredSubjects)
}

func knowledgeEvidenceSelectedCandidateExplicitSubjectGaps(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selectedCandidateIDs []string,
) []string {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) < 2 || len(selectedCandidateIDs) == 0 {
		return nil
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	covered := make(map[string]struct{}, len(requiredSubjects))
	explicitlyMentioned := false
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		questionText := normalizeKnowledgeEvidenceSubjectForMatch(question)
		answerText := normalizeKnowledgeEvidenceSubjectForMatch(answer)
		answerSubjects := knowledgeEvidenceContainedSubjects(answerText, requiredSubjects)
		questionSubjects := knowledgeEvidenceContainedSubjects(questionText, requiredSubjects)
		for _, subject := range answerSubjects {
			explicitlyMentioned = true
			covered[subject] = struct{}{}
		}
		inheritQuestionSubjects := knowledgeEvidenceFAQAnswerCollectivelyCoversTaskSubjects(task, answer) ||
			(len(answerSubjects) == 0 && knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer))
		if !inheritQuestionSubjects && len(questionSubjects) > 0 && len(questionSubjects) < len(requiredSubjects) {
			inheritQuestionSubjects = knowledgeEvidenceSubjectSetContainedBy(answerSubjects, questionSubjects)
		}
		if inheritQuestionSubjects {
			for _, subject := range questionSubjects {
				explicitlyMentioned = true
				covered[subject] = struct{}{}
			}
		} else if len(answerSubjects) == 0 {
			for _, classified := range knowledgeEvidenceAnswerClauseAspects(answer) {
				if requiredKnowledgeEvidenceAspect(requiredKnowledgeEvidenceAspects(task), classified.Aspect) {
					explicitlyMentioned = true
					break
				}
			}
		}
	}
	if !explicitlyMentioned || len(covered) == len(requiredSubjects) {
		return nil
	}
	missingSubjects := make([]string, 0, len(requiredSubjects)-len(covered))
	for _, subject := range requiredSubjects {
		if _, ok := covered[subject]; !ok {
			missingSubjects = append(missingSubjects, subject)
		}
	}
	return missingSubjects
}

func knowledgeEvidenceSubjectSetContainedBy(subjects []string, allowed []string) bool {
	if len(subjects) == 0 {
		return false
	}
	for _, subject := range subjects {
		if !knowledgeEvidenceContainsString(allowed, subject) {
			return false
		}
	}
	return true
}

func knowledgeEvidenceExplicitSubjectGapMissingAspects(task knowledgeEvidenceJudgeTask, missingSubjects []string) []string {
	if len(missingSubjects) == 0 {
		return nil
	}
	requiredAspects := requiredKnowledgeEvidenceAspects(task)
	if len(requiredAspects) == 0 {
		requiredAspects = []string{"other"}
	}
	ret := make([]string, 0, len(missingSubjects)*len(requiredAspects))
	for _, subject := range missingSubjects {
		for _, aspect := range requiredAspects {
			ret = appendIfMissing(ret, subject+knowledgeEvidenceAspectLabel(aspect))
		}
	}
	return ret
}

func knowledgeEvidenceUncertaintyTouchesRequiredAspect(requiredAspects []string, clause string) bool {
	for _, clauseAspect := range knowledgeEvidenceAnswerClauseAspects(clause) {
		if clauseAspect.Aspect != "other" && requiredKnowledgeEvidenceAspect(requiredAspects, clauseAspect.Aspect) {
			return true
		}
	}
	compact := normalizeRuntimeKnowledgeQuery(clause)
	checks := []struct {
		aspect  string
		markers []string
	}{
		{aspect: "existence", markers: []string{"是否", "有无", "有没有", "提供", "配备", "存在", "供应"}},
		{aspect: "quantity", markers: []string{"数量", "多少", "几瓶", "几份", "几个", "几间", "几台", "几条", "几套", "几双", "几把", "几包", "几盒", "几袋", "几件", "几支", "几只", "几辆", "几杯", "几桶", "几卷"}},
		{aspect: "price", markers: []string{"费用", "收费", "免费", "价格", "金额", "多少钱"}},
		{aspect: "time", markers: []string{"时间", "几点", "多久", "什么时候", "何时"}},
		{aspect: "location", markers: []string{"位置", "地址", "哪里", "哪儿", "楼层"}},
		{aspect: "method", markers: []string{"方式", "方法", "怎么", "如何", "办理", "操作"}},
		{aspect: "scope", markers: []string{"范围", "全部", "哪些", "送到"}},
		{aspect: "condition", markers: []string{"条件", "限制", "要求"}},
	}
	for _, check := range checks {
		if requiredKnowledgeEvidenceAspect(requiredAspects, check.aspect) && containsAny(compact, check.markers) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFAQAnswerSupportsQuestion(task knowledgeEvidenceJudgeTask, question string, answer string) bool {
	if knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) {
		return true
	}
	if _, ok := resolvedKnowledgeEvidenceFAQQuestionStatement(task, question, answer); ok {
		return true
	}
	if !requiredKnowledgeEvidenceAspect(requiredKnowledgeEvidenceAspects(task), "existence") || knowledgeEvidenceTextHasNegativeBoundary(answer) {
		return false
	}
	compactAnswer := normalizeRuntimeKnowledgeQuery(answer)
	if !containsAny(compactAnswer, []string{"有", "配有", "配备", "提供", "设有", "配置"}) {
		return false
	}
	compactQuestion := normalizeRuntimeKnowledgeQuery(question)
	for _, entity := range task.Entities {
		value := normalizeRuntimeKnowledgeQuery(entity.Text)
		if len([]rune(value)) >= 2 && strings.Contains(compactQuestion, value) && strings.Contains(compactAnswer, value) {
			return true
		}
	}
	return false
}

func enrichKnowledgeEvidenceFactsFromSelectedFAQs(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selectedCandidateIDs []string,
	facts []knowledgeEvidenceFact,
) []knowledgeEvidenceFact {
	required := requiredKnowledgeEvidenceAspects(task)
	if len(required) == 0 || len(selectedCandidateIDs) == 0 {
		return facts
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		facts = enrichKnowledgeEvidenceFactsFromFAQUnit(task, question, answer, facts)
	}
	return facts
}

func enrichKnowledgeEvidenceFactsFromFAQUnit(task knowledgeEvidenceJudgeTask, question string, answer string, facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	facts = bindKnowledgeEvidenceFAQTimeSubject(task, question, answer, facts)
	facts = filterKnowledgeEvidenceFAQTimeFacts(task, question, answer, facts)
	if !knowledgeEvidenceFAQAnswerSupportsQuestion(task, question, answer) {
		return facts
	}
	statement, resolved := resolvedKnowledgeEvidenceFAQQuestionStatement(task, question, answer)
	if !resolved {
		if prefix, _, polarity := knowledgeEvidenceFAQAnswerPolarity(answer); polarity &&
			!knowledgeEvidenceFAQAnswerIsPurePolarity(answer, prefix) {
			return facts
		}
		statement = affirmativeKnowledgeEvidenceQuestionStatement(question)
	}
	if statement == "" {
		return facts
	}
	seenFactIDs := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		seenFactIDs[strings.TrimSpace(fact.FactID)] = struct{}{}
	}
	for _, aspect := range requiredKnowledgeEvidenceAspects(task) {
		if knowledgeEvidenceFactsCoverRequiredAspect(task, facts, aspect) {
			resolvedSubjectsCovered := true
			for _, subject := range requiredKnowledgeEvidenceSubjectEntities(task) {
				if !knowledgeEvidenceFactsCoverSubjectAspect(task, facts, subject, aspect) {
					resolvedSubjectsCovered = false
					break
				}
			}
			if resolvedSubjectsCovered {
				if aspect == "existence" {
					facts = enrichKnowledgeEvidenceExistenceFactSubjects(task, facts)
				}
				continue
			}
		}
		criticalValues := confirmedKnowledgeEvidenceQuestionCriticalValues(task, aspect, question, answer)
		if len(criticalValues) == 0 {
			continue
		}
		fact := knowledgeEvidenceFact{
			FactID:         nextKnowledgeEvidenceFactID(task.TaskID, seenFactIDs),
			Aspect:         aspect,
			Statement:      statement,
			CriticalValues: criticalValues,
		}
		if !knowledgeEvidenceFactSupportsAspect(fact, aspect) {
			continue
		}
		seenFactIDs[fact.FactID] = struct{}{}
		facts = append(facts, fact)
	}
	return facts
}

func enrichKnowledgeEvidenceExistenceFactSubjects(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) == 0 {
		return facts
	}
	for factIndex := range facts {
		if !knowledgeEvidenceFactSupportsAspect(facts[factIndex], "existence") {
			continue
		}
		statement := normalizeKnowledgeEvidenceSubjectForMatch(facts[factIndex].Statement)
		for _, entity := range task.Entities {
			subject := normalizeKnowledgeEvidenceSubjectForMatch(normalizeKnowledgeEvidenceEntityText(entity))
			if subject == "" || !knowledgeEvidenceContainsString(requiredSubjects, subject) || !strings.Contains(statement, subject) {
				continue
			}
			facts[factIndex].CriticalValues = appendIfMissing(facts[factIndex].CriticalValues, strings.TrimSpace(entity.Text))
		}
	}
	return facts
}

func affirmativeKnowledgeEvidenceQuestionStatement(question string) string {
	statement := strings.TrimSpace(question)
	statement = strings.Trim(statement, " 。！!？?")
	for _, prefix := range []string{"请问一下", "请问", "问一下", "问下", "想问一下", "想问"} {
		statement = strings.TrimSpace(strings.TrimPrefix(statement, prefix))
	}
	statement = strings.TrimSuffix(statement, "吗")
	statement = strings.TrimSuffix(statement, "嘛")
	statement = strings.TrimSuffix(statement, "么")
	statement = strings.ReplaceAll(statement, "可不可以", "可以")
	statement = strings.ReplaceAll(statement, "能不能", "能")
	statement = strings.ReplaceAll(statement, "支不支持", "支持")
	statement = strings.ReplaceAll(statement, "需不需要", "需要")
	statement = strings.ReplaceAll(statement, "有没有", "有")
	statement = strings.ReplaceAll(statement, "是不是", "是")
	statement = strings.ReplaceAll(statement, "是否", "")
	statement = strings.TrimSpace(statement)
	if statement == "" {
		return ""
	}
	return statement + "。"
}

func resolvedKnowledgeEvidenceFAQQuestionStatement(task knowledgeEvidenceJudgeTask, question string, answer string) (string, bool) {
	if statement, ok := resolvedKnowledgeEvidenceFAQPriceStatement(task, question, answer); ok {
		return statement, true
	}
	prefix, negative, ok := knowledgeEvidenceFAQAnswerPolarity(answer)
	if !ok {
		return "", false
	}
	if !knowledgeEvidenceFAQAnswerIsPurePolarity(answer, prefix) {
		if negative || !knowledgeEvidenceFAQAnswerHasBindingQualifier(answer) ||
			!knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) {
			return "", false
		}
		statement := strings.TrimSuffix(affirmativeKnowledgeEvidenceQuestionStatement(question), "。")
		qualifier := knowledgeEvidenceFAQAnswerQualifierRemainder(answer, prefix)
		if statement == "" || qualifier == "" {
			return "", false
		}
		return statement + "，" + qualifier + "。", true
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) == 0 {
		return "", false
	}
	if knowledgeEvidenceFAQAnswerMentionsOnlyPartOfRequiredSubjects(answer, requiredSubjects) {
		return "", false
	}
	compactQuestion := normalizeKnowledgeEvidenceSubjectForMatch(question)
	for _, subject := range requiredSubjects {
		if !strings.Contains(compactQuestion, subject) {
			return "", false
		}
	}
	if !negative && !knowledgeEvidenceContainsString([]string{"有的", "可以", "支持"}, prefix) {
		statement := affirmativeKnowledgeEvidenceQuestionStatement(question)
		return statement, statement != ""
	}

	statement := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.Trim(strings.TrimSpace(question), " 。！!？?"), "吗"), "嘛"), "么")
	for _, politePrefix := range []string{"请问一下", "请问", "问一下", "问下", "想问一下", "想问"} {
		statement = strings.TrimSpace(strings.TrimPrefix(statement, politePrefix))
	}
	replaceFirst := func(markers []string, replacement string) bool {
		for _, marker := range markers {
			if strings.Contains(statement, marker) {
				statement = strings.Replace(statement, marker, replacement, 1)
				return true
			}
		}
		return false
	}
	matched := false
	switch prefix {
	case "有的":
		matched = replaceFirst([]string{"有没有", "是否有", "有无", "没有", "有"}, "有")
	case "可以":
		matched = replaceFirst([]string{"可不可以", "是否可以", "能不能", "能否", "不可以", "不能", "可以", "能"}, "可以")
	case "支持":
		matched = replaceFirst([]string{"支不支持", "是否支持", "不支持", "支持"}, "支持")
	case "没有", "没有的":
		matched = replaceFirst([]string{"有没有", "是否有", "有无", "没有", "有"}, "没有")
	case "不可以", "不能":
		matched = replaceFirst([]string{"可不可以", "是否可以", "能不能", "能否", "不可以", "不能", "可以", "能"}, prefix)
	case "不支持":
		matched = replaceFirst([]string{"支不支持", "是否支持", "不支持", "支持"}, "不支持")
	case "不需要", "无需", "不用":
		matched = replaceFirst([]string{"需不需要", "是否需要", "不需要", "无需", "不用", "需要"}, "不需要")
	case "不是":
		matched = replaceFirst([]string{"是不是", "是否是", "不是", "是"}, "不是")
	}
	statement = strings.TrimSpace(statement)
	if !matched || statement == "" {
		return "", false
	}
	return statement + "。", true
}

func resolvedKnowledgeEvidenceFAQPriceStatement(task knowledgeEvidenceJudgeTask, question string, answer string) (string, bool) {
	if !requiredKnowledgeEvidenceAspect(requiredKnowledgeEvidenceAspects(task), "price") ||
		!knowledgeEvidenceQueryAsksAbsolutePriceStatus(question) {
		return "", false
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) != 1 ||
		!strings.Contains(normalizeKnowledgeEvidenceSubjectForMatch(question), requiredSubjects[0]) {
		return "", false
	}
	claims := knowledgeEvidencePriceClaims(answer)
	free := knowledgeEvidencePriceClaimsContain(claims, "free")
	charged := knowledgeEvidencePriceClaimsContain(claims, "charged")
	if free == charged {
		return "", false
	}
	if free {
		return requiredSubjects[0] + "免费。", true
	}
	return requiredSubjects[0] + "不免费。", true
}

func knowledgeEvidenceFAQAnswerQualifierRemainder(answer string, prefix string) string {
	answer = strings.TrimSpace(answer)
	prefix = strings.TrimSpace(prefix)
	if answer == "" || prefix == "" || !strings.HasPrefix(answer, prefix) {
		return ""
	}
	remainder := strings.TrimSpace(strings.TrimPrefix(answer, prefix))
	remainder = strings.TrimLeft(remainder, "，,。.!！；;：:")
	remainder = strings.TrimSpace(strings.Trim(remainder, "。.!！；;"))
	return remainder
}

func confirmedKnowledgeEvidenceQuestionCriticalValues(task knowledgeEvidenceJudgeTask, aspect string, question string, answer string) []string {
	combined := strings.TrimSpace(question + " " + answer)
	values := make([]string, 0, 2)
	switch aspect {
	case "existence":
		compactQuestion := normalizeRuntimeKnowledgeQuery(question)
		compactAnswer := normalizeRuntimeKnowledgeQuery(answer)
		for _, entity := range task.Entities {
			value := normalizeRuntimeKnowledgeQuery(entity.Text)
			if len([]rune(value)) < 2 || !strings.Contains(compactQuestion, value) {
				continue
			}
			_, resolvesQuestion := resolvedKnowledgeEvidenceFAQQuestionStatement(task, question, answer)
			if strings.Contains(compactAnswer, value) || knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) || resolvesQuestion {
				values = appendIfMissing(values, strings.TrimSpace(entity.Text))
			}
		}
	case "quantity":
		for _, match := range knowledgeEvidenceStrictQuantityPattern.FindAllString(question, -1) {
			values = appendIfMissing(values, strings.TrimSpace(match))
		}
	case "price":
		priceSource := answer
		if len(knowledgeEvidencePriceClaims(priceSource)) == 0 && knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) {
			priceSource = question
		}
		for _, value := range knowledgeEvidencePriceCriticalValues(priceSource) {
			values = appendIfMissing(values, value)
		}
	case "time":
		rangeMatches := knowledgeEvidenceAnswerTimePattern.FindAllString(combined, -1)
		for _, match := range rangeMatches {
			values = appendIfMissing(values, strings.TrimSpace(match))
		}
		for _, match := range knowledgeEvidenceIndividualTimePattern.FindAllString(combined, -1) {
			if knowledgeEvidenceNumberIsPartOfTime(match, rangeMatches) {
				continue
			}
			values = appendIfMissing(values, strings.TrimSpace(match))
		}
	}
	return values
}

func bindKnowledgeEvidenceFAQTimeSubject(task knowledgeEvidenceJudgeTask, question string, answer string, facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	if len(facts) == 0 || len(requiredKnowledgeEvidenceTimeSlotsForTask(task)) == 0 ||
		len(knowledgeEvidenceIndividualTimePattern.FindAllString(answer, -1)) == 0 {
		return facts
	}
	subject := ""
	if requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task); len(requiredSubjects) == 1 &&
		strings.Contains(normalizeKnowledgeEvidenceSubjectForMatch(question), requiredSubjects[0]) {
		subject = requiredSubjects[0]
	}
	condition := ""
	if conditions := knowledgeEvidenceCalendarConditions(question); len(conditions) == 1 {
		condition = conditions[0]
	}
	if subject == "" && condition == "" {
		return facts
	}
	for index := range facts {
		if !knowledgeEvidenceFactSupportsAspect(facts[index], "time") {
			continue
		}
		if len(splitKnowledgeEvidenceTimeClauses(facts[index].Statement)) != 1 {
			continue
		}
		factText := normalizeKnowledgeEvidenceSubjectForMatch(facts[index].Statement + " " + strings.Join(facts[index].CriticalValues, " "))
		factSubject := subject
		if knowledgeEvidenceTimeFactHasExplicitSubject(facts[index].Statement) {
			factSubject = ""
		}
		statement := strings.Trim(strings.TrimSpace(facts[index].Statement), "。！!？?")
		if statement == "" {
			continue
		}
		prefix := ""
		if condition != "" && len(knowledgeEvidenceCalendarConditions(factText)) == 0 {
			prefix += knowledgeEvidenceTimeConditionLabel(condition)
		}
		if factSubject != "" && !strings.Contains(factText, factSubject) {
			prefix += factSubject
		}
		if prefix == "" {
			continue
		}
		compact := normalizeRuntimeKnowledgeQuery(statement)
		if containsAny(compact, []string{"时间", "开始", "结束", "截止", "开门", "关门", "入住", "退房", "供应到", "营业到"}) {
			facts[index].Statement = prefix + statement + "。"
		} else {
			facts[index].Statement = prefix + "时间为" + statement + "。"
		}
	}
	return facts
}

func filterKnowledgeEvidenceFAQTimeFacts(task knowledgeEvidenceJudgeTask, question string, answer string, facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	if len(facts) == 0 || len(knowledgeEvidenceIndividualTimePattern.FindAllString(answer, -1)) == 0 {
		return facts
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) != 1 {
		return facts
	}
	subject := requiredSubjects[0]
	if !strings.Contains(normalizeKnowledgeEvidenceSubjectForMatch(question), subject) {
		return facts
	}
	filtered := make([]knowledgeEvidenceFact, 0, len(facts))
	for _, fact := range facts {
		if !knowledgeEvidenceFactSupportsAspect(fact, "time") {
			filtered = append(filtered, fact)
			continue
		}
		clauses := knowledgeEvidenceTimeClausesForSubject(question, fact.Statement, subject)
		if len(clauses) == 0 {
			continue
		}
		fact.Statement = strings.Join(clauses, "，")
		fact.CriticalValues = sanitizeKnowledgeEvidenceCriticalValuesForStatement(fact.CriticalValues, fact.Statement)
		filtered = append(filtered, fact)
	}
	return filtered
}

func knowledgeEvidenceTimeFactHasExplicitSubject(statement string) bool {
	compact := normalizeKnowledgeEvidenceSubjectForMatch(statement)
	if containsAny(compact, []string{"办理入住", "办理退房", "入住", "退房", "开门", "关门"}) {
		return true
	}
	residual := knowledgeEvidenceIndividualTimePattern.ReplaceAllString(statement, "")
	residual = knowledgeEvidenceDurationValuePattern.ReplaceAllString(residual, "")
	residual = normalizeKnowledgeEvidenceSubjectForMatch(residual)
	for _, marker := range []string{
		"法定节假日", "节假日", "工作日", "周末", "每天", "每日",
		"次日", "第二天", "翌日",
		"营业时间为", "营业时间是", "供应时间为", "供应时间是", "开放时间为", "开放时间是",
		"营业时间", "供应时间", "开放时间", "时间为", "时间是", "时间",
		"开始", "结束", "截止", "供应到", "营业到", "开放到", "从", "至", "到", "为", "是",
	} {
		residual = strings.ReplaceAll(residual, marker, "")
	}
	residual = strings.Trim(residual, "-~至到，,。；;！!？?")
	return len([]rune(residual)) >= 2
}

func knowledgeEvidenceFactsCoverCriticalValues(facts []knowledgeEvidenceFact, requiredValues []string) bool {
	for _, requiredValue := range requiredValues {
		if !knowledgeEvidenceFactsContainCriticalValue(facts, requiredValue) {
			return false
		}
	}
	return true
}

func reconcileSelectedKnowledgeEvidenceTaskBoundCriticalValues(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selectedCandidateIDs []string,
	facts []knowledgeEvidenceFact,
) ([]knowledgeEvidenceFact, bool) {
	requiredValues := knowledgeEvidenceSelectedTaskBoundCriticalValues(task, layer, selectedCandidateIDs)
	if len(requiredValues) == 0 {
		return facts, true
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	seenFactIDs := make(map[string]struct{}, len(facts)+len(requiredValues))
	for _, fact := range facts {
		seenFactIDs[strings.TrimSpace(fact.FactID)] = struct{}{}
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	_, combinedTotal := knowledgeEvidenceTaskQuantityTargetsBySubject(task.Query, requiredSubjects)

	for _, requiredValue := range requiredValues {
		if knowledgeEvidenceFactsContainCriticalValue(facts, requiredValue) {
			continue
		}
		if combinedTotal && knowledgeEvidenceSelectedCandidatesSupportCombinedQuantityTotal(task, layer, selectedCandidateIDs, requiredSubjects, requiredValue) {
			factID := nextKnowledgeEvidenceFactID(task.TaskID, seenFactIDs)
			seenFactIDs[factID] = struct{}{}
			facts = append(facts, knowledgeEvidenceFact{
				FactID:         factID,
				Aspect:         "quantity",
				Statement:      strings.Join(requiredSubjects, "和") + "合计" + requiredValue + "。",
				CriticalValues: []string{requiredValue},
			})
			continue
		}
		added := false
		for _, candidate := range task.Candidates {
			if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
				continue
			}
			if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
				continue
			}
			question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
			if strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
				continue
			}
			statement := ""
			groundedValue := ""
			if value, ok := knowledgeEvidenceEquivalentTaskQuantityInText(task, question, answer, requiredValue); ok {
				statement = strings.TrimSpace(answer)
				groundedValue = value
			} else if value, ok := knowledgeEvidenceEquivalentTaskQuantityInText(task, question, question, requiredValue); ok && knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) {
				statement = affirmativeKnowledgeEvidenceQuestionStatement(question)
				groundedValue = value
			}
			fact := knowledgeEvidenceFact{
				FactID:         nextKnowledgeEvidenceFactID(task.TaskID, seenFactIDs),
				Aspect:         "quantity",
				Statement:      statement,
				CriticalValues: []string{groundedValue},
			}
			if statement == "" || !knowledgeEvidenceFactGroundedForTask(task, fact, knowledgeEvidenceCandidateGroundingParts(task, candidate)) {
				continue
			}
			seenFactIDs[fact.FactID] = struct{}{}
			facts = append(facts, fact)
			added = true
			break
		}
		if !added {
			return facts, false
		}
	}
	facts = canonicalizeKnowledgeEvidenceFacts(sanitizeKnowledgeEvidenceFacts(facts))
	return facts, knowledgeEvidenceFactsCoverCriticalValues(facts, requiredValues)
}

func knowledgeEvidenceFactsContainCriticalValue(facts []knowledgeEvidenceFact, requiredValue string) bool {
	for _, fact := range facts {
		for _, value := range sanitizeKnowledgeEvidenceCriticalValuesForStatement(fact.CriticalValues, fact.Statement) {
			if knowledgeEvidenceCriticalValuesEquivalent(value, requiredValue) {
				return true
			}
		}
	}
	return false
}

func knowledgeEvidenceSelectedTaskBoundCriticalValues(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) []string {
	queryValues := knowledgeEvidenceTaskBoundCriticalValues(task.Query)
	if len(queryValues) == 0 || len(selectedCandidateIDs) == 0 {
		return nil
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if _, combinedTotal := knowledgeEvidenceTaskQuantityTargetsBySubject(task.Query, requiredSubjects); combinedTotal &&
		len(queryValues) == 1 && knowledgeEvidenceSelectedCandidatesSupportCombinedQuantityTotal(task, layer, selectedCandidateIDs, requiredSubjects, queryValues[0]) {
		return append([]string(nil), queryValues...)
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	confirmed := make([]string, 0, len(queryValues))
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		for _, value := range queryValues {
			if _, ok := knowledgeEvidenceEquivalentTaskQuantityInText(task, question, answer, value); ok {
				confirmed = appendIfMissing(confirmed, value)
				continue
			}
			if _, ok := knowledgeEvidenceEquivalentTaskQuantityInText(task, question, question, value); ok && knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) {
				confirmed = appendIfMissing(confirmed, value)
			}
		}
	}
	return confirmed
}

func knowledgeEvidenceSelectedCandidatesHaveTaskBoundQuantityConflict(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) bool {
	if len(selectedCandidateIDs) == 0 {
		return false
	}
	queryValues := knowledgeEvidenceTaskBoundCriticalValues(task.Query)
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) > 1 {
		return knowledgeEvidenceSelectedCandidatesHaveMultiSubjectTaskBoundQuantityConflict(task, layer, selectedCandidateIDs, requiredSubjects)
	}
	if len(requiredSubjects) == 0 {
		inferredSubjects := knowledgeEvidenceInferredQuantitySubjects(task.Query)
		if len(inferredSubjects) > 1 {
			return knowledgeEvidenceSelectedCandidatesHaveMultiSubjectTaskBoundQuantityConflict(task, layer, selectedCandidateIDs, inferredSubjects)
		}
		if len(queryValues) > 1 && len(inferredSubjects) == 0 {
			return true
		}
		requiredSubjects = inferredSubjects
	}
	if len(queryValues) == 0 {
		return false
	}
	requiredSubject := ""
	if len(requiredSubjects) == 1 {
		requiredSubject = requiredSubjects[0]
	}
	if requirements := knowledgeEvidenceTaskBoundQuantityRequirements(task, requiredSubject); len(requirements) > 0 {
		complete, conflict := knowledgeEvidenceSelectedCandidatesCoverQuantityRequirements(task, layer, selectedCandidateIDs, requirements)
		return !complete || conflict
	}
	taskScope := knowledgeEvidenceConflictObjectScope(task.Query)
	queryConditionsByValue := knowledgeEvidenceQuantityConditionsByValue(task.Query, requiredSubject)

	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	type quantityObservation struct {
		sameUnit              bool
		equivalent            bool
		conflicting           bool
		otherSubjectUnit      bool
		conditionalEquivalent bool
	}
	observations := make(map[string]quantityObservation, len(queryValues))
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		candidateText := normalizeKnowledgeEvidenceSubjectForMatch(strings.Join([]string{question, answer, candidate.Hit.Title}, " "))
		if requiredSubject != "" && !strings.Contains(candidateText, requiredSubject) {
			continue
		}
		quantityTexts := splitKnowledgeEvidenceAnswerClauses(answer)
		if knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) {
			quantityTexts = append(quantityTexts, splitKnowledgeEvidenceAnswerClauses(question)...)
		}
		for _, quantityText := range quantityTexts {
			textScope := knowledgeEvidenceConflictObjectScope(quantityText)
			if taskScope != "" && taskScope != "hotel" && textScope != "" && textScope != "hotel" && taskScope != textScope {
				continue
			}
			candidateConditions := knowledgeEvidenceConflictConditions(quantityText)
			candidateValues := knowledgeEvidenceQuantityOccurrences(quantityText, requiredSubject)
			for _, queryValue := range queryValues {
				queryUnit := knowledgeEvidenceQuantityUnit(queryValue)
				if queryUnit == "" {
					continue
				}
				queryConditions := queryConditionsByValue[normalizeKnowledgeEvidenceQuantityValue(queryValue)]
				if !knowledgeEvidenceQuantityConditionsComparable(queryConditions, candidateConditions) {
					if len(queryConditions) == 0 && len(candidateConditions) > 0 {
						observation := observations[queryValue]
						for _, candidateValue := range candidateValues {
							if knowledgeEvidenceQuantityUnit(candidateValue.Value) == queryUnit &&
								candidateValue.SubjectRelation != "other" &&
								knowledgeEvidenceCriticalValuesEquivalent(candidateValue.Value, queryValue) {
								observation.conditionalEquivalent = true
							}
						}
						observations[queryValue] = observation
					}
					continue
				}
				observation := observations[queryValue]
				for _, candidateValue := range candidateValues {
					if knowledgeEvidenceQuantityUnit(candidateValue.Value) != queryUnit {
						continue
					}
					if requiredSubject != "" && candidateValue.SubjectRelation == "other" {
						observation.otherSubjectUnit = true
						continue
					}
					observation.sameUnit = true
					if knowledgeEvidenceCriticalValuesEquivalent(candidateValue.Value, queryValue) {
						observation.equivalent = true
					} else {
						observation.conflicting = true
					}
				}
				observations[queryValue] = observation
			}
		}
	}
	for _, queryValue := range queryValues {
		observation := observations[queryValue]
		if observation.sameUnit && (!observation.equivalent || observation.conflicting) {
			return true
		}
		if !observation.sameUnit && observation.otherSubjectUnit {
			return true
		}
		if !observation.sameUnit && observation.conditionalEquivalent {
			return true
		}
	}
	return false
}

func knowledgeEvidenceQuantityConditionsByValue(text string, subject string) map[string][]string {
	ret := make(map[string][]string)
	for _, clause := range splitKnowledgeEvidenceAnswerClauses(text) {
		conditions := knowledgeEvidenceConflictConditions(clause)
		for _, occurrence := range knowledgeEvidenceQuantityOccurrences(clause, subject) {
			value := normalizeKnowledgeEvidenceQuantityValue(occurrence.Value)
			if value == "" {
				continue
			}
			for _, condition := range conditions {
				ret[value] = appendIfMissing(ret[value], condition)
			}
			if _, exists := ret[value]; !exists {
				ret[value] = nil
			}
		}
	}
	return ret
}

type knowledgeEvidenceQuantityRequirement struct {
	Subject    string
	Conditions []string
	Value      string
}

func knowledgeEvidenceTaskBoundQuantityRequirements(task knowledgeEvidenceJudgeTask, requiredSubject string) []knowledgeEvidenceQuantityRequirement {
	if knowledgeEvidenceClauseHasCombinedQuantityTotal(task.Query) {
		return nil
	}
	indexes := knowledgeEvidenceStrictQuantityPattern.FindAllStringIndex(task.Query, -1)
	if len(indexes) == 0 {
		return nil
	}
	ret := make([]knowledgeEvidenceQuantityRequirement, 0, len(indexes))
	seen := make(map[string]struct{}, len(indexes))
	activeSubject := normalizeKnowledgeEvidenceSubjectForMatch(requiredSubject)
	hasExplicitCondition := false
	for index, bounds := range indexes {
		value := strings.TrimSpace(task.Query[bounds[0]:bounds[1]])
		compactValue := normalizeRuntimeKnowledgeQuery(value)
		if !containsAnySuffix(compactValue, []string{
			"瓶", "间", "张", "份", "位", "人", "台", "条", "套", "双", "把", "包", "盒", "袋", "件", "支", "只", "辆", "杯", "桶", "卷", "个",
		}) || knowledgeEvidenceQuantityCounterIsScope(compactValue, task.Query[bounds[1]:]) ||
			knowledgeEvidenceQuantityCounterIsRequestParameter(task.Query, bounds[0], bounds[1]) {
			continue
		}
		leftBoundary, rightBoundary := knowledgeEvidenceQuantityClauseBounds(task.Query, bounds[0], bounds[1])
		if index > 0 && indexes[index-1][1] > leftBoundary {
			leftBoundary = indexes[index-1][1]
		}
		if index+1 < len(indexes) && indexes[index+1][0] < rightBoundary {
			rightBoundary = indexes[index+1][0]
		}
		clause := task.Query[leftBoundary:rightBoundary]
		conditions := knowledgeEvidenceConflictConditions(clause)
		hasExplicitCondition = hasExplicitCondition || len(conditions) > 0
		subject := activeSubject
		if subject == "" {
			leading, trailing := knowledgeEvidenceQuantityBindingSegments(
				task.Query[leftBoundary:bounds[0]],
				task.Query[bounds[1]:rightBoundary],
			)
			subject = knowledgeEvidenceQuantityLeadingObject(leading)
			if subject == "" {
				subject = knowledgeEvidenceQuantityTrailingObject(trailing)
			}
			if subject != "" {
				activeSubject = subject
			}
		}
		conditionSets := knowledgeEvidenceQuantityConditionSets(conditions)
		for _, conditionSet := range conditionSets {
			key := normalizeKnowledgeEvidenceSubjectForMatch(subject) + "|" + strings.Join(conditionSet, ",") + "|" + normalizeKnowledgeEvidenceQuantityValue(value)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			ret = append(ret, knowledgeEvidenceQuantityRequirement{
				Subject:    normalizeKnowledgeEvidenceSubjectForMatch(subject),
				Conditions: append([]string(nil), conditionSet...),
				Value:      value,
			})
		}
	}
	if len(ret) == 0 || !hasExplicitCondition {
		return nil
	}
	return ret
}

func knowledgeEvidenceQuantityConditionSets(conditions []string) [][]string {
	if len(conditions) == 0 {
		return [][]string{nil}
	}
	type conditionGroup struct {
		dimension string
		values    []string
	}
	groups := make([]conditionGroup, 0, len(conditions))
	groupIndex := make(map[string]int, len(conditions))
	for _, condition := range conditions {
		dimension := knowledgeEvidenceQuantityConditionDimension(condition)
		index, exists := groupIndex[dimension]
		if !exists {
			index = len(groups)
			groupIndex[dimension] = index
			groups = append(groups, conditionGroup{dimension: dimension})
		}
		groups[index].values = appendIfMissing(groups[index].values, condition)
	}
	ret := [][]string{{}}
	for _, group := range groups {
		next := make([][]string, 0, len(ret)*len(group.values))
		for _, current := range ret {
			for _, value := range group.values {
				combined := append([]string(nil), current...)
				combined = append(combined, value)
				next = append(next, combined)
			}
		}
		ret = next
	}
	return ret
}

func knowledgeEvidenceQuantityConditionDimension(condition string) string {
	switch condition {
	case "workday", "weekend", "holiday":
		return "calendar_day_type"
	case "night", "daytime":
		return "daypart"
	case "checkin_day", "checkout_day":
		return "stay_day"
	default:
		if strings.HasPrefix(condition, "weekday:") {
			return "weekday"
		}
		return "condition:" + condition
	}
}

func knowledgeEvidenceSelectedCandidatesCoverQuantityRequirements(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selectedCandidateIDs []string,
	requirements []knowledgeEvidenceQuantityRequirement,
) (bool, bool) {
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	taskScope := knowledgeEvidenceConflictObjectScope(task.Query)
	for _, requirement := range requirements {
		covered := false
		conflicting := false
		for _, candidate := range task.Candidates {
			if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
				continue
			}
			if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
				continue
			}
			question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
			if strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
				continue
			}
			questionBindsSubject := requirement.Subject == "" || strings.Contains(
				normalizeKnowledgeEvidenceSubjectForMatch(question),
				normalizeKnowledgeEvidenceSubjectForMatch(requirement.Subject),
			)
			type conditionedQuantityText struct {
				text                string
				conditionSets       [][]string
				universalDimensions map[string]struct{}
			}
			quantityTexts := make([]conditionedQuantityText, 0, 4)
			questionConditions := knowledgeEvidenceConflictConditions(question)
			for _, answerClause := range splitKnowledgeEvidenceAnswerClauses(answer) {
				universalDimensions := knowledgeEvidenceQuantityUniversalConditionDimensions(answerClause)
				effectiveConditions := knowledgeEvidenceMergeFAQQuantityConditions(
					questionConditions,
					knowledgeEvidenceConflictConditions(answerClause),
					answerClause,
				)
				quantityTexts = append(quantityTexts, conditionedQuantityText{
					text:                answerClause,
					conditionSets:       knowledgeEvidenceQuantityConditionSets(effectiveConditions),
					universalDimensions: universalDimensions,
				})
			}
			if knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) {
				for _, questionClause := range splitKnowledgeEvidenceAnswerClauses(question) {
					quantityTexts = append(quantityTexts, conditionedQuantityText{
						text:          questionClause,
						conditionSets: knowledgeEvidenceQuantityConditionSets(knowledgeEvidenceConflictConditions(questionClause)),
					})
				}
			}
			for _, quantityText := range quantityTexts {
				textScope := knowledgeEvidenceConflictObjectScope(quantityText.text)
				if taskScope != "" && taskScope != "hotel" && textScope != "" && textScope != "hotel" && taskScope != textScope {
					continue
				}
				conditionsComparable := false
				for _, candidateConditions := range quantityText.conditionSets {
					if knowledgeEvidenceQuantityConditionsComparableWithUniversal(
						requirement.Conditions,
						candidateConditions,
						quantityText.universalDimensions,
					) {
						conditionsComparable = true
						break
					}
				}
				if !conditionsComparable {
					continue
				}
				for _, occurrence := range knowledgeEvidenceQuantityOccurrences(quantityText.text, requirement.Subject) {
					if knowledgeEvidenceQuantityUnit(occurrence.Value) != knowledgeEvidenceQuantityUnit(requirement.Value) ||
						occurrence.SubjectRelation == "other" || (occurrence.SubjectRelation == "implicit" && !questionBindsSubject) {
						continue
					}
					if knowledgeEvidenceCriticalValuesEquivalent(occurrence.Value, requirement.Value) {
						covered = true
					} else {
						conflicting = true
					}
				}
			}
		}
		if conflicting {
			return false, true
		}
		if !covered {
			return false, false
		}
	}
	return true, false
}

func knowledgeEvidenceMergeFAQQuantityConditions(question []string, answer []string, answerText string) []string {
	universalDimensions := knowledgeEvidenceQuantityUniversalConditionDimensions(answerText)
	if len(answer) == 0 && len(universalDimensions) == 0 {
		return append([]string(nil), question...)
	}
	answerDimensions := make(map[string]struct{}, len(answer)+len(universalDimensions))
	for dimension := range universalDimensions {
		answerDimensions[dimension] = struct{}{}
	}
	for _, condition := range answer {
		answerDimensions[knowledgeEvidenceQuantityConditionDimension(condition)] = struct{}{}
	}
	ret := make([]string, 0, len(question)+len(answer))
	for _, condition := range question {
		if _, overridden := answerDimensions[knowledgeEvidenceQuantityConditionDimension(condition)]; overridden {
			continue
		}
		ret = appendIfMissing(ret, condition)
	}
	for _, condition := range answer {
		if _, universal := universalDimensions[knowledgeEvidenceQuantityConditionDimension(condition)]; universal {
			continue
		}
		ret = appendIfMissing(ret, condition)
	}
	return ret
}

func knowledgeEvidenceQuantityUniversalConditionDimensions(text string) map[string]struct{} {
	compact := normalizeRuntimeKnowledgeQuery(text)
	ret := make(map[string]struct{}, 2)
	if containsAny(compact, []string{
		"每天", "每日", "天天", "任何日期", "任意日期", "所有日期", "全年",
		"不分工作日和周末", "无论工作日还是周末", "无论工作日或周末",
	}) {
		ret["calendar_day_type"] = struct{}{}
		ret["weekday"] = struct{}{}
	}
	if containsAny(compact, []string{
		"全天", "全时段", "任何时间", "任意时间", "所有时段", "全部时段",
		"不分白天晚上", "无论白天还是晚上", "无论白天或晚上",
	}) {
		ret["daypart"] = struct{}{}
	}
	return ret
}

func knowledgeEvidenceQuantityConditionsComparable(required []string, candidate []string) bool {
	if len(required) == 0 {
		return len(candidate) == 0
	}
	if len(candidate) == 0 {
		return true
	}
	if len(required) != len(candidate) {
		return false
	}
	for _, condition := range required {
		if !knowledgeEvidenceContainsString(candidate, condition) {
			return false
		}
	}
	return true
}

func knowledgeEvidenceQuantityConditionsComparableWithUniversal(required []string, candidate []string, universalDimensions map[string]struct{}) bool {
	if len(universalDimensions) == 0 {
		return knowledgeEvidenceQuantityConditionsComparable(required, candidate)
	}
	filteredRequired := make([]string, 0, len(required))
	for _, condition := range required {
		if _, universal := universalDimensions[knowledgeEvidenceQuantityConditionDimension(condition)]; universal {
			continue
		}
		filteredRequired = append(filteredRequired, condition)
	}
	return knowledgeEvidenceQuantityConditionsComparable(filteredRequired, candidate)
}

func knowledgeEvidenceFactsCoverQuantityRequirement(facts []knowledgeEvidenceFact, requirement knowledgeEvidenceQuantityRequirement) bool {
	for _, fact := range facts {
		for _, clause := range splitKnowledgeEvidenceAnswerClauses(fact.Statement) {
			if !knowledgeEvidenceQuantityConditionsComparable(requirement.Conditions, knowledgeEvidenceConflictConditions(clause)) {
				continue
			}
			for _, occurrence := range knowledgeEvidenceQuantityOccurrences(clause, requirement.Subject) {
				if knowledgeEvidenceQuantityUnit(occurrence.Value) != knowledgeEvidenceQuantityUnit(requirement.Value) ||
					occurrence.SubjectRelation == "other" ||
					(requirement.Subject != "" && occurrence.SubjectRelation == "implicit") {
					continue
				}
				if knowledgeEvidenceCriticalValuesEquivalent(occurrence.Value, requirement.Value) {
					return true
				}
			}
		}
	}
	return false
}

func knowledgeEvidenceQuantityRequirementMissingAspect(requirement knowledgeEvidenceQuantityRequirement) string {
	conditionLabels := make([]string, 0, len(requirement.Conditions))
	for _, condition := range requirement.Conditions {
		switch condition {
		case "workday":
			conditionLabels = append(conditionLabels, "工作日")
		case "weekend":
			conditionLabels = append(conditionLabels, "周末")
		case "holiday":
			conditionLabels = append(conditionLabels, "节假日")
		case "night":
			conditionLabels = append(conditionLabels, "夜间")
		case "daytime":
			conditionLabels = append(conditionLabels, "白天")
		case "checkin_day":
			conditionLabels = append(conditionLabels, "入住当天")
		case "checkout_day":
			conditionLabels = append(conditionLabels, "退房当天")
		default:
			conditionLabels = append(conditionLabels, condition)
		}
	}
	return strings.Join(conditionLabels, "") + requirement.Subject + knowledgeEvidenceAspectLabel("quantity")
}

func knowledgeEvidenceSelectedCandidatesHaveMultiSubjectTaskBoundQuantityConflict(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selectedCandidateIDs []string,
	requiredSubjects []string,
) bool {
	targets, combinedTotal := knowledgeEvidenceTaskQuantityTargetsBySubject(task.Query, requiredSubjects)
	if combinedTotal {
		queryValues := knowledgeEvidenceTaskBoundCriticalValues(task.Query)
		return len(queryValues) != 1 || !knowledgeEvidenceSelectedCandidatesSupportCombinedQuantityTotal(task, layer, selectedCandidateIDs, requiredSubjects, queryValues[0])
	}
	openTargets := knowledgeEvidenceTaskOpenQuantityUnitsBySubject(task.Query, requiredSubjects)
	if len(targets) == 0 && len(openTargets) == 0 {
		return false
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	taskScope := knowledgeEvidenceConflictObjectScope(task.Query)
	for subject, expectedValues := range targets {
		for _, expected := range expectedValues {
			expectedUnit := knowledgeEvidenceQuantityUnit(expected)
			sameUnit := false
			equivalent := false
			conflicting := false
			for _, candidate := range task.Candidates {
				if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
					continue
				}
				if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
					continue
				}
				question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
				if strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
					continue
				}
				confirmsQuestion := knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer)
				if confirmsQuestion && knowledgeEvidenceSharedQuantityAppliesToSubjects(question, requiredSubjects) &&
					strings.Contains(normalizeKnowledgeEvidenceSubjectForMatch(question), subject) {
					if _, ok := knowledgeEvidenceEquivalentQuantityInText(question, expected); ok {
						sameUnit = true
						equivalent = true
					}
				}
				quantityTexts := splitKnowledgeEvidenceAnswerClauses(answer)
				for _, quantityText := range quantityTexts {
					textScope := knowledgeEvidenceConflictObjectScope(quantityText)
					if taskScope != "" && taskScope != "hotel" && textScope != "" && textScope != "hotel" && taskScope != textScope {
						continue
					}
					for _, occurrence := range knowledgeEvidenceQuantityOccurrences(quantityText, subject) {
						if occurrence.SubjectRelation != "required" || knowledgeEvidenceQuantityUnit(occurrence.Value) != expectedUnit {
							continue
						}
						sameUnit = true
						if knowledgeEvidenceCriticalValuesEquivalent(occurrence.Value, expected) {
							equivalent = true
						} else {
							conflicting = true
						}
					}
				}
			}
			if !sameUnit || !equivalent || conflicting {
				return true
			}
		}
	}
	for subject, expectedUnits := range openTargets {
		if !knowledgeEvidenceSelectedCandidatesSupportOpenQuantity(task, layer, selectedCandidateIDs, subject, expectedUnits) {
			return true
		}
	}
	return false
}

var knowledgeEvidenceOpenQuantityPattern = regexp.MustCompile(`(?:有|配|放|提供|准备|备有)?(?:几|多少)(瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|个)`)

func knowledgeEvidenceTaskOpenQuantityUnitsBySubject(query string, requiredSubjects []string) map[string][]string {
	if len(requiredSubjects) == 0 {
		return nil
	}
	targets := make(map[string][]string, len(requiredSubjects))
	for _, clause := range splitKnowledgeEvidenceAnswerClauses(query) {
		matches := knowledgeEvidenceOpenQuantityPattern.FindAllStringSubmatchIndex(clause, -1)
		if len(matches) == 0 {
			continue
		}
		clauseText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
		clauseSubjects := knowledgeEvidenceContainedSubjects(clauseText, requiredSubjects)
		if len(clauseSubjects) == 0 {
			continue
		}
		if knowledgeEvidenceSharedQuantityAppliesToSubjects(clause, clauseSubjects) {
			for _, match := range matches {
				unit := clause[match[2]:match[3]]
				for _, subject := range clauseSubjects {
					targets[subject] = appendIfMissing(targets[subject], unit)
				}
			}
			continue
		}
		for _, match := range matches {
			unit := clause[match[2]:match[3]]
			owner := ""
			ownerIndex := -1
			prefix := normalizeKnowledgeEvidenceSubjectForMatch(clause[:match[0]])
			for _, subject := range clauseSubjects {
				if index := strings.LastIndex(prefix, subject); index > ownerIndex {
					owner = subject
					ownerIndex = index
				}
			}
			if owner == "" && len(clauseSubjects) == 1 {
				owner = clauseSubjects[0]
			}
			if owner != "" {
				targets[owner] = appendIfMissing(targets[owner], unit)
			}
		}
	}
	return targets
}

func knowledgeEvidenceSelectedCandidatesSupportOpenQuantity(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selectedCandidateIDs []string,
	subject string,
	expectedUnits []string,
) bool {
	for _, text := range selectedKnowledgeEvidenceQuantityTexts(task, layer, selectedCandidateIDs) {
		for _, clause := range splitKnowledgeEvidenceAnswerClauses(text) {
			for _, occurrence := range knowledgeEvidenceQuantityOccurrences(clause, subject) {
				actualUnit := knowledgeEvidenceQuantityUnit(occurrence.Value)
				unitMatches := knowledgeEvidenceOpenQuantityUnitMatches(expectedUnits, actualUnit)
				if occurrence.SubjectRelation == "required" && unitMatches {
					return true
				}
			}
		}
	}
	return false
}

func knowledgeEvidenceOpenQuantityUnitMatches(expectedUnits []string, actualUnit string) bool {
	if actualUnit == "" {
		return false
	}
	if knowledgeEvidenceContainsString(expectedUnits, actualUnit) {
		return true
	}
	if !knowledgeEvidenceContainsString(expectedUnits, "个") {
		return false
	}
	return knowledgeEvidenceContainsString([]string{
		"瓶", "张", "份", "台", "条", "套", "双", "把", "包", "盒", "袋", "件", "支", "只", "杯", "桶", "卷", "个",
	}, actualUnit)
}

func knowledgeEvidenceSelectedCandidatesSupportCombinedQuantityTotal(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selectedCandidateIDs []string,
	requiredSubjects []string,
	expected string,
) bool {
	expectedNumber, expectedUnit, ok := knowledgeEvidenceQuantityNumericParts(expected)
	if !ok || len(requiredSubjects) < 2 {
		return false
	}
	texts := selectedKnowledgeEvidenceQuantityTexts(task, layer, selectedCandidateIDs)
	if len(texts) == 0 {
		return false
	}
	explicitTotalSeen := false
	explicitTotalMatched := false
	for _, text := range texts {
		for _, clause := range splitKnowledgeEvidenceAnswerClauses(text) {
			for _, value := range knowledgeEvidenceExplicitCombinedQuantityTotals(clause) {
				if knowledgeEvidenceQuantityUnit(value) != expectedUnit {
					continue
				}
				explicitTotalSeen = true
				if knowledgeEvidenceCriticalValuesEquivalent(value, expected) {
					explicitTotalMatched = true
				} else {
					return false
				}
			}
		}
	}
	if explicitTotalSeen {
		return explicitTotalMatched
	}

	total := 0
	for _, subject := range requiredSubjects {
		subjectValue := ""
		for _, text := range texts {
			for _, clause := range splitKnowledgeEvidenceAnswerClauses(text) {
				if knowledgeEvidenceClauseHasCombinedQuantityTotal(clause) {
					continue
				}
				for _, occurrence := range knowledgeEvidenceQuantityOccurrences(clause, subject) {
					if occurrence.SubjectRelation != "required" || knowledgeEvidenceQuantityUnit(occurrence.Value) != expectedUnit {
						continue
					}
					if subjectValue != "" && !knowledgeEvidenceCriticalValuesEquivalent(subjectValue, occurrence.Value) {
						return false
					}
					subjectValue = occurrence.Value
				}
			}
		}
		value, unit, ok := knowledgeEvidenceQuantityNumericParts(subjectValue)
		if !ok || unit != expectedUnit {
			return false
		}
		total += value
	}
	return total == expectedNumber
}

func selectedKnowledgeEvidenceQuantityTexts(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) []string {
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	ret := make([]string, 0, len(selectedCandidateIDs)*2)
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		ret = append(ret, answer)
		if knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) {
			ret = append(ret, question)
		}
	}
	return ret
}

func knowledgeEvidenceClauseHasCombinedQuantityTotal(text string) bool {
	return containsAny(normalizeRuntimeKnowledgeQuery(text), []string{"一共", "总共", "合计", "共计"})
}

func knowledgeEvidenceExplicitCombinedQuantityTotals(text string) []string {
	matches := knowledgeEvidenceCombinedQuantityTotalPattern.FindAllStringSubmatch(text, -1)
	ret := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			ret = appendIfMissing(ret, strings.TrimSpace(match[1]))
		}
	}
	return ret
}

func knowledgeEvidenceQuantityNumericParts(value string) (int, string, bool) {
	match := knowledgeEvidenceTaskBoundQuantityValuePattern.FindStringSubmatch(normalizeRuntimeKnowledgeQuery(value))
	if len(match) != 3 {
		return 0, "", false
	}
	if parsed, err := strconv.Atoi(match[1]); err == nil {
		return parsed, match[2], true
	}
	parsed, ok := parseKnowledgeEvidenceEnumerationCount(match[1])
	return parsed, match[2], ok
}

func knowledgeEvidenceTaskQuantityTargetsBySubject(query string, requiredSubjects []string) (map[string][]string, bool) {
	queryValues := knowledgeEvidenceTaskBoundCriticalValues(query)
	if len(queryValues) == 0 || len(requiredSubjects) < 2 {
		return nil, false
	}
	compact := normalizeRuntimeKnowledgeQuery(query)
	shared := knowledgeEvidenceSharedQuantityAppliesToSubjects(query, requiredSubjects)
	combinedTotal := len(queryValues) == 1 && !shared && containsAny(compact, []string{"一共", "总共", "合计", "共计"})
	if combinedTotal {
		return nil, true
	}
	targets := make(map[string][]string, len(requiredSubjects))
	if len(queryValues) == 1 && shared {
		for _, subject := range requiredSubjects {
			targets[subject] = []string{queryValues[0]}
		}
		return targets, false
	}
	for _, subject := range requiredSubjects {
		for _, occurrence := range knowledgeEvidenceQuantityOccurrences(query, subject) {
			if occurrence.SubjectRelation == "required" {
				targets[subject] = appendIfMissing(targets[subject], occurrence.Value)
			}
		}
	}
	return targets, false
}

func knowledgeEvidenceSharedQuantityAppliesToSubjects(text string, requiredSubjects []string) bool {
	compact := normalizeKnowledgeEvidenceSubjectForMatch(text)
	sharedPredicateText := text
	if quantityBounds := knowledgeEvidenceStrictQuantityPattern.FindStringIndex(text); quantityBounds != nil {
		sharedPredicateText = text[:quantityBounds[0]]
	}
	sharedPredicateCompact := normalizeKnowledgeEvidenceSubjectForMatch(sharedPredicateText)
	sharedPredicate := runtimeIntentClauseHasSharedPredicate(sharedPredicateText) ||
		containsAny(sharedPredicateCompact, []string{"分别", "各有", "各自", "每种", "每个", "均有", "均配", "均提供"})
	if len(requiredSubjects) < 2 || !sharedPredicate {
		return false
	}
	for _, subject := range requiredSubjects {
		if !strings.Contains(compact, subject) {
			return false
		}
	}
	return true
}

func knowledgeEvidenceQuantityClauseSubject(clause string, requiredSubject string) string {
	occurrences := knowledgeEvidenceQuantityOccurrences(clause, requiredSubject)
	if len(occurrences) == 0 {
		return "implicit"
	}
	return occurrences[0].SubjectRelation
}

func knowledgeEvidenceSelectedCandidatesHaveConflictingAnswers(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) bool {
	if len(selectedCandidateIDs) < 2 {
		return false
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	type selectedFAQ struct {
		question  string
		answer    string
		signature string
	}
	selectedFAQs := make([]selectedFAQ, 0, len(selectedCandidateIDs))
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		selectedFAQs = append(selectedFAQs, selectedFAQ{
			question:  question,
			answer:    answer,
			signature: knowledgeEvidenceConflictQuestionSignature(question),
		})
	}
	taskSignature := knowledgeEvidenceConflictQuestionSignature(task.Query)
	for leftIndex := 0; leftIndex < len(selectedFAQs); leftIndex++ {
		left := selectedFAQs[leftIndex]
		leftSubjectClaim := knowledgeEvidenceFAQHasExistenceClaim(left.question, left.answer)
		if left.signature == "" && !leftSubjectClaim {
			continue
		}
		if left.signature != "" && taskSignature != "" && !knowledgeEvidenceConflictQuestionSignaturesCompatible(taskSignature, left.signature) {
			continue
		}
		for rightIndex := leftIndex + 1; rightIndex < len(selectedFAQs); rightIndex++ {
			right := selectedFAQs[rightIndex]
			rightSubjectClaim := knowledgeEvidenceFAQHasExistenceClaim(right.question, right.answer)
			if left.signature == "" || right.signature == "" {
				if left.signature != "" || right.signature != "" || !leftSubjectClaim || !rightSubjectClaim {
					continue
				}
			} else if !knowledgeEvidenceConflictQuestionSignaturesCompatible(left.signature, right.signature) ||
				(taskSignature != "" && !knowledgeEvidenceConflictQuestionSignaturesCompatible(taskSignature, right.signature)) {
				continue
			}
			if !knowledgeEvidenceFAQClaimsComparableForConflict(left.question, left.answer, right.question, right.answer) {
				continue
			}
			if left.signature == "" {
				if knowledgeEvidenceFAQClaimsConflict(left.question, left.answer, right.question, right.answer) {
					return true
				}
				continue
			}
			domain, role, _, ok := parseKnowledgeEvidenceConflictQuestionSignature(left.signature)
			if !ok {
				continue
			}
			switch domain {
			case "time":
				if conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(left.question, left.answer, right.question, right.answer); comparable && conflict {
					return true
				}
			case "identity":
				if knowledgeEvidenceIdentityValuesConflict(left.answer, right.answer) {
					return true
				}
			case "address":
				if role == "delivery" {
					if knowledgeEvidenceDeliveryAddressAnswersConflict(left.answer, right.answer) {
						return true
					}
				} else if knowledgeEvidenceFAQClaimsConflict(left.question, left.answer, right.question, right.answer) {
					return true
				}
			default:
				if knowledgeEvidenceFAQClaimsConflict(left.question, left.answer, right.question, right.answer) ||
					knowledgeEvidenceConfigurationValuesConflict(task.Query, left.answer, right.answer) {
					return true
				}
			}
		}
	}
	return false
}

func knowledgeEvidenceDeliveryAddressAnswersConflict(left string, right string) bool {
	leftPayload, leftKind := knowledgeEvidenceDeliveryAddressPayload(left)
	rightPayload, rightKind := knowledgeEvidenceDeliveryAddressPayload(right)
	if leftPayload == "" || rightPayload == "" || leftKind == "" || rightKind == "" {
		return false
	}
	return leftPayload != rightPayload && !strings.Contains(leftPayload, rightPayload) && !strings.Contains(rightPayload, leftPayload)
}

func knowledgeEvidenceDeliveryAddressPayload(answer string) (string, string) {
	payload := normalizeRuntimeKnowledgeQuery(answer)
	payload = strings.NewReplacer(
		"外卖地址", "", "收货地址", "", "配送地址", "", "跑腿地址", "",
		"应该填写", "", "应该填", "", "请填写", "", "请填", "", "填写", "", "填为", "", "填成", "",
		"应该写", "", "请写", "", "写为", "", "写成", "", "填", "",
	).Replace(payload)
	for _, marker := range []string{
		"对应的楼层房间号", "对应楼层房间号", "对应的楼层和房号", "对应楼层和房号",
		"补充楼层和房号", "加上楼层和房号", "再加楼层和房号", "楼层及房间号", "楼层和房间号", "房间号",
	} {
		if index := strings.Index(payload, marker); index >= 0 {
			payload = payload[:index]
			break
		}
	}
	payload = strings.Trim(payload, "，,。；;！!？?：:+加和及并再")
	for _, suffix := range []string{"就可以了", "就可以", "即可", "就行了", "就行", "就好了", "就好"} {
		payload = strings.TrimSuffix(payload, suffix)
	}
	for _, prefix := range []string{"是", "为"} {
		payload = strings.TrimPrefix(payload, prefix)
	}
	payload = strings.Trim(payload, "，,。；;！!？?：:+加和及并再")
	if len([]rune(payload)) < 2 {
		return "", ""
	}
	switch {
	case containsAny(payload, []string{"酒店", "宾馆", "公寓", "旅馆", "客栈", "民宿"}):
		return payload, "venue"
	case knowledgeEvidenceShortVenuePattern.MatchString(payload) && !knowledgeEvidenceGenericShortVenue(payload):
		return payload, "venue"
	case containsAny(payload, []string{"省", "市", "区", "县", "镇", "路", "街", "大道", "巷", "弄", "号"}):
		return payload, "street"
	default:
		return "", ""
	}
}

func knowledgeEvidenceGenericShortVenue(value string) bool {
	switch normalizeRuntimeKnowledgeQuery(value) {
	case "本店", "门店", "本门店", "该店", "此店", "到店", "店内":
		return true
	default:
		return false
	}
}

func knowledgeEvidenceEquivalentQuantityInText(text string, expected string) (string, bool) {
	expectedKey := normalizeKnowledgeEvidenceQuantityValue(expected)
	if expectedKey == "" {
		return "", false
	}
	for _, value := range knowledgeEvidenceStrictQuantityPattern.FindAllString(text, -1) {
		if normalizeKnowledgeEvidenceQuantityValue(value) == expectedKey {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

type knowledgeEvidenceQuantityOccurrence struct {
	Value           string
	SubjectRelation string
}

func knowledgeEvidenceEquivalentTaskQuantityInText(
	task knowledgeEvidenceJudgeTask,
	faqQuestion string,
	text string,
	expected string,
) (string, bool) {
	expectedKey := normalizeKnowledgeEvidenceQuantityValue(expected)
	if expectedKey == "" {
		return "", false
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) == 0 {
		inferredSubject := knowledgeEvidenceInferredQuantitySubjectForValue(task.Query, expected)
		if inferredSubject == "" {
			if len(knowledgeEvidenceTaskBoundCriticalValues(task.Query)) > 1 {
				return "", false
			}
			return knowledgeEvidenceEquivalentQuantityInText(text, expected)
		}
		requiredSubjects = []string{inferredSubject}
	}
	if len(requiredSubjects) != 1 {
		return "", false
	}
	requiredSubject := requiredSubjects[0]
	questionBindsSubject := strings.Contains(
		normalizeKnowledgeEvidenceSubjectForMatch(faqQuestion),
		normalizeKnowledgeEvidenceSubjectForMatch(requiredSubject),
	)
	for _, occurrence := range knowledgeEvidenceQuantityOccurrences(text, requiredSubject) {
		if normalizeKnowledgeEvidenceQuantityValue(occurrence.Value) != expectedKey {
			continue
		}
		switch occurrence.SubjectRelation {
		case "required":
			return occurrence.Value, true
		case "implicit":
			if questionBindsSubject {
				return occurrence.Value, true
			}
		}
	}
	return "", false
}

type knowledgeEvidenceQuantitySubjectBinding struct {
	Value   string
	Subject string
}

func knowledgeEvidenceInferredQuantitySubjectBindings(query string) []knowledgeEvidenceQuantitySubjectBinding {
	indexes := knowledgeEvidenceStrictQuantityPattern.FindAllStringIndex(query, -1)
	if len(indexes) == 0 {
		return nil
	}
	if len(indexes) == 1 {
		prefix := normalizeKnowledgeEvidenceSubjectForMatch(query[:indexes[0][0]])
		if containsAny(prefix, []string{"分别", "各有", "各自", "以及", "和", "与", "及", "、"}) {
			return nil
		}
	}
	bindings := make([]knowledgeEvidenceQuantitySubjectBinding, 0, len(indexes))
	for index, bounds := range indexes {
		leftBoundary, rightBoundary := knowledgeEvidenceQuantityClauseBounds(query, bounds[0], bounds[1])
		if index > 0 && indexes[index-1][1] > leftBoundary {
			leftBoundary = indexes[index-1][1]
		}
		if index+1 < len(indexes) && indexes[index+1][0] < rightBoundary {
			rightBoundary = indexes[index+1][0]
		}
		leadingSegment, trailingSegment := knowledgeEvidenceQuantityBindingSegments(
			query[leftBoundary:bounds[0]],
			query[bounds[1]:rightBoundary],
		)
		leadingObject := knowledgeEvidenceQuantityLeadingObject(leadingSegment)
		trailingObject := knowledgeEvidenceQuantityTrailingObject(trailingSegment)
		if leadingObject != "" && trailingObject != "" && !knowledgeEvidenceQuantityObjectsOverlap(leadingObject, trailingObject) {
			return nil
		}
		subject := leadingObject
		if subject == "" {
			subject = trailingObject
		}
		if subject == "" {
			return nil
		}
		bindings = append(bindings, knowledgeEvidenceQuantitySubjectBinding{
			Value:   strings.TrimSpace(query[bounds[0]:bounds[1]]),
			Subject: subject,
		})
	}
	return bindings
}

func knowledgeEvidenceQuantityBindingSegments(leading string, trailing string) (string, string) {
	connectors := []string{"以及", "而且", "和", "与", "及", "且", "、"}
	leadingCompact := strings.TrimSpace(leading)
	trailingCompact := strings.TrimSpace(trailing)

	for _, connector := range connectors {
		if strings.HasSuffix(leadingCompact, connector) {
			leadingCompact = ""
			break
		}
	}
	if leadingCompact != "" {
		for _, connector := range connectors {
			if !strings.HasPrefix(leadingCompact, connector) {
				continue
			}
			leadingCompact = strings.TrimSpace(strings.TrimPrefix(leadingCompact, connector))
			break
		}
	}

	for _, connector := range connectors {
		if strings.HasPrefix(trailingCompact, connector) {
			trailingCompact = ""
			break
		}
	}
	if trailingCompact != "" {
		for _, connector := range connectors {
			if !strings.HasSuffix(trailingCompact, connector) {
				continue
			}
			trailingCompact = strings.TrimSpace(strings.TrimSuffix(trailingCompact, connector))
			break
		}
	}
	return leadingCompact, trailingCompact
}

func knowledgeEvidenceInferredQuantitySubjects(query string) []string {
	bindings := knowledgeEvidenceInferredQuantitySubjectBindings(query)
	ret := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		ret = appendIfMissing(ret, binding.Subject)
	}
	return ret
}

func knowledgeEvidenceInferredQuantitySubjectForValue(query string, expected string) string {
	expectedKey := normalizeKnowledgeEvidenceQuantityValue(expected)
	if expectedKey == "" {
		return ""
	}
	matchedSubject := ""
	for _, binding := range knowledgeEvidenceInferredQuantitySubjectBindings(query) {
		if normalizeKnowledgeEvidenceQuantityValue(binding.Value) != expectedKey {
			continue
		}
		if matchedSubject != "" && !knowledgeEvidenceQuantityObjectsOverlap(matchedSubject, binding.Subject) {
			return ""
		}
		matchedSubject = binding.Subject
	}
	return matchedSubject
}

func knowledgeEvidenceQuantityOccurrences(text string, requiredSubject string) []knowledgeEvidenceQuantityOccurrence {
	indexes := knowledgeEvidenceStrictQuantityPattern.FindAllStringIndex(text, -1)
	if len(indexes) == 0 {
		return nil
	}
	requiredSubject = normalizeKnowledgeEvidenceSubjectForMatch(requiredSubject)
	ret := make([]knowledgeEvidenceQuantityOccurrence, 0, len(indexes))
	for index, bounds := range indexes {
		leftBoundary, rightBoundary := knowledgeEvidenceQuantityClauseBounds(text, bounds[0], bounds[1])
		if index > 0 && indexes[index-1][1] > leftBoundary {
			leftBoundary = indexes[index-1][1]
		}
		if index+1 < len(indexes) && indexes[index+1][0] < rightBoundary {
			rightBoundary = indexes[index+1][0]
		}
		leading, trailing := knowledgeEvidenceQuantityBindingSegments(
			text[leftBoundary:bounds[0]],
			text[bounds[1]:rightBoundary],
		)
		ret = append(ret, knowledgeEvidenceQuantityOccurrence{
			Value:           strings.TrimSpace(text[bounds[0]:bounds[1]]),
			SubjectRelation: knowledgeEvidenceQuantityOccurrenceSubject(leading, trailing, requiredSubject),
		})
	}
	return ret
}

func knowledgeEvidenceQuantityClauseBounds(text string, start int, end int) (int, int) {
	leftBoundary := 0
	rightBoundary := len(text)
	for _, separator := range []string{"\n", "\r", "。", "！", "!", "？", "?", "；", ";", "，", ","} {
		if index := strings.LastIndex(text[:start], separator); index >= 0 && index+len(separator) > leftBoundary {
			leftBoundary = index + len(separator)
		}
		if offset := strings.Index(text[end:], separator); offset >= 0 && end+offset < rightBoundary {
			rightBoundary = end + offset
		}
	}
	return leftBoundary, rightBoundary
}

func knowledgeEvidenceQuantityOccurrenceSubject(leading string, trailing string, requiredSubject string) string {
	requiredSubject = normalizeKnowledgeEvidenceSubjectForMatch(requiredSubject)
	if requiredSubject == "" {
		return "implicit"
	}
	leadingCompact := normalizeKnowledgeEvidenceSubjectForMatch(leading)
	if strings.Contains(leadingCompact, requiredSubject) &&
		(runtimeIntentClauseHasSharedPredicate(leading) || containsAny(leadingCompact, []string{"均有", "均配", "均提供"})) {
		return "required"
	}
	leadingObject := knowledgeEvidenceQuantityLeadingObject(leading)
	if leadingObject != "" {
		if knowledgeEvidenceQuantityObjectsOverlap(leadingObject, requiredSubject) {
			return "required"
		}
		return "other"
	}
	trailingObject := knowledgeEvidenceQuantityTrailingObject(trailing)
	if trailingObject != "" {
		if knowledgeEvidenceQuantityObjectsOverlap(trailingObject, requiredSubject) {
			return "required"
		}
		return "other"
	}
	return "implicit"
}

func knowledgeEvidenceQuantityLeadingObject(value string) string {
	value = knowledgeEvidenceQuantityTextWithoutConditionMarkers(value)
	for _, connector := range []string{"、", "和", "与", "及"} {
		if index := strings.LastIndex(value, connector); index > 0 {
			value = value[index+len(connector):]
		}
	}
	value = strings.Trim(value, "的了和与及、，,。；;！!？?")
	if containsAny(value, []string{"又写", "写了", "写着", "标注", "注明", "说明里", "记录为", "记录是"}) {
		return ""
	}
	for {
		before := value
		for _, suffix := range []string{
			"一共", "总共", "共计", "共有", "另有", "另外有", "大约有", "大概有", "约有",
			"最多有", "至少有", "还放", "放有", "放了", "放", "提供", "配备", "包含", "赠送", "供应", "准备", "备有",
			"一共是", "总共是", "一共为", "总共为", "分别", "现在", "目前", "当前", "仍然", "仍", "又", "还", "也", "约", "有", "是", "为", "共",
		} {
			value = strings.TrimSuffix(value, suffix)
		}
		value = strings.Trim(value, "的了和与及、，,。；;！!？?")
		if value == before {
			break
		}
	}
	for _, scope := range []string{"每个房间内", "每个房间里", "每个房间", "每间客房内", "每间客房里", "每间客房", "每间房内", "每间房里", "每间房", "房间内", "房间里", "客房内", "客房里", "房内", "房里", "房间", "客房", "酒店内", "酒店里", "门店内", "门店里", "酒店", "门店", "本店"} {
		if value == scope {
			return ""
		}
	}
	runes := []rune(value)
	if len(runes) < 2 || len(runes) > 16 {
		return ""
	}
	return value
}

func knowledgeEvidenceQuantityTrailingObject(value string) string {
	value = knowledgeEvidenceQuantityTextWithoutConditionMarkers(value)
	value = strings.TrimLeft(value, "的")
	if value == "" {
		return ""
	}
	end := len(value)
	for _, marker := range []string{
		"都是", "都", "左右", "以上", "以下", "以内", "至少", "最多", "大约", "约",
		"免费", "收费", "可以", "已经", "分别", "提供", "配备", "供应", "每",
		"需要", "不能", "不会", "无法", "不再", "另有", "可", "是", "为", "有", "在", "放", "需", "会", "能", "不",
	} {
		if index := strings.Index(value, marker); index >= 0 && index < end {
			end = index
		}
	}
	object := strings.Trim(value[:end], "的了和与及、，,。；;！!？?吗嘛么呢呀啊")
	runes := []rune(object)
	if len(runes) < 2 || len(runes) > 16 {
		return ""
	}
	return object
}

func knowledgeEvidenceQuantityTextWithoutConditionMarkers(value string) string {
	value = normalizeKnowledgeEvidenceSubjectForMatch(value)
	for _, marker := range []string{
		"法定节假日", "节假日", "工作日", "平日", "平时", "周末", "双休日", "双休",
		"夜间", "晚上", "夜里", "白天", "日间", "入住当天", "退房当天", "每天", "每日",
	} {
		value = strings.ReplaceAll(value, marker, "")
	}
	return knowledgeEvidenceStandaloneWeekdayPattern.ReplaceAllString(value, "")
}

func knowledgeEvidenceQuantityObjectsOverlap(left string, right string) bool {
	left = normalizeKnowledgeEvidenceSubjectForMatch(left)
	right = normalizeKnowledgeEvidenceSubjectForMatch(right)
	return left != "" && right != "" && (strings.Contains(left, right) || strings.Contains(right, left))
}

func knowledgeEvidenceCriticalValuesEquivalent(left string, right string) bool {
	leftQuantity := normalizeKnowledgeEvidenceQuantityValue(left)
	rightQuantity := normalizeKnowledgeEvidenceQuantityValue(right)
	if leftQuantity != "" || rightQuantity != "" {
		return leftQuantity != "" && leftQuantity == rightQuantity
	}
	return normalizeRuntimeKnowledgeQuery(left) == normalizeRuntimeKnowledgeQuery(right)
}

func normalizeKnowledgeEvidenceQuantityValue(value string) string {
	compact := normalizeRuntimeKnowledgeQuery(value)
	match := knowledgeEvidenceTaskBoundQuantityValuePattern.FindStringSubmatch(compact)
	if len(match) != 3 {
		return ""
	}
	number := match[1]
	if parsed, err := strconv.Atoi(number); err == nil {
		return strconv.Itoa(parsed) + match[2]
	}
	parsed, ok := parseKnowledgeEvidenceEnumerationCount(number)
	if !ok {
		return compact
	}
	return strconv.Itoa(parsed) + match[2]
}

func knowledgeEvidenceQuantityUnit(value string) string {
	match := knowledgeEvidenceTaskBoundQuantityValuePattern.FindStringSubmatch(normalizeRuntimeKnowledgeQuery(value))
	if len(match) != 3 {
		return ""
	}
	return match[2]
}

func knowledgeEvidenceTaskBoundCriticalValues(query string) []string {
	values := make([]string, 0, 2)
	for _, indexes := range knowledgeEvidenceStrictQuantityPattern.FindAllStringIndex(query, -1) {
		match := query[indexes[0]:indexes[1]]
		compact := normalizeRuntimeKnowledgeQuery(match)
		if !containsAnySuffix(compact, []string{
			"瓶", "间", "张", "份", "位", "人", "台", "条", "套", "双", "把", "包", "盒", "袋", "件", "支", "只", "辆", "杯", "桶", "卷", "个",
		}) {
			continue
		}
		if knowledgeEvidenceQuantityCounterIsScope(compact, query[indexes[1]:]) {
			continue
		}
		if knowledgeEvidenceQuantityCounterIsRequestParameter(query, indexes[0], indexes[1]) {
			continue
		}
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	return values
}

func knowledgeEvidenceQuantityCounterIsRequestParameter(query string, start int, end int) bool {
	leftBoundary, rightBoundary := knowledgeEvidenceQuantityClauseBounds(query, start, end)
	prefix := normalizeRuntimeKnowledgeQuery(query[leftBoundary:start])
	suffix := normalizeRuntimeKnowledgeQuery(query[end:rightBoundary])
	clause := normalizeRuntimeKnowledgeQuery(query[leftBoundary:rightBoundary])
	explicitFactQuestion := containsAny(clause, []string{"免费", "收费", "价格", "费用", "金额", "多少钱"}) ||
		(strings.Contains(clause, "有") && strings.Contains(clause, "吗"))
	if strings.Contains(clause, "吗") && containsAny(clause, []string{
		"每个房间", "每间房", "每间客房", "每个客房", "房间都", "客房都",
	}) {
		explicitFactQuestion = true
	}
	if explicitFactQuestion {
		return false
	}
	for _, action := range []string{
		"推荐", "选择", "选", "挑", "预订", "订", "申请", "安排", "叫", "点",
		"添加", "加", "补充", "补", "送", "拿", "取", "领取", "自取", "更换", "换", "借",
		"我要", "我需要", "我想要", "想要", "给我", "帮我准备", "准备", "要",
	} {
		if strings.HasSuffix(prefix, action) {
			return true
		}
	}
	pickupSuffix := containsAny(suffix, []string{
		"怎么拿", "如何拿", "在哪拿", "哪里拿", "怎么取", "如何取", "在哪取", "哪里取",
		"怎么领取", "如何领取", "怎么自取", "如何自取",
	})
	if !pickupSuffix {
		return false
	}
	return true
}

func knowledgeEvidenceQuantityCounterIsScope(value string, remainder string) bool {
	unit := knowledgeEvidenceQuantityUnit(value)
	remainder = normalizeRuntimeKnowledgeQuery(remainder)
	for _, scope := range []string{"房", "房间", "客房", "房型", "酒店", "门店", "订单", "地址", "地点", "楼层", "客户", "客人"} {
		if strings.HasPrefix(remainder, scope) {
			return true
		}
	}
	if unit == "个" && strings.HasPrefix(remainder, "人") {
		return true
	}
	if unit == "人" || unit == "位" {
		for _, action := range []string{"住", "入住", "同住", "用餐", "办理", "预订", "订房"} {
			if strings.HasPrefix(remainder, action) {
				return true
			}
		}
	}
	return false
}

var knowledgeEvidenceTaskBoundQuantityValuePattern = regexp.MustCompile(`^([0-9]+|[零〇一二三四五六七八九十两]+)(瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|个)$`)

func containsAnySuffix(text string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(text, suffix) {
			return true
		}
	}
	return false
}

func filterKnowledgeEvidenceFactsForTask(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	if len(facts) == 0 {
		return nil
	}
	required := requiredKnowledgeEvidenceAspects(task)
	ret := make([]knowledgeEvidenceFact, 0, len(facts))
	for _, rawFact := range facts {
		fact := narrowKnowledgeEvidenceFactToTask(task, rawFact, required)
		if knowledgeEvidenceFactIsMarketingFiller(fact.Statement) {
			continue
		}
		if len(required) == 0 {
			// A grounded identity or descriptive fact may legitimately normalize to
			// "other" when the task has no fixed fact dimension. Required method,
			// location, existence and other dimensions are still checked below.
			if fact.Aspect == "other" {
				if len(knowledgeEvidenceConfigurationFields(task.Query)) > 0 {
					if knowledgeEvidenceConfigurationFactAnswersTask(task, fact) {
						ret = append(ret, fact)
					}
					continue
				}
				if _, guidance := knowledgeEvidenceGuidanceRequirement(fact.Statement); guidance != "" {
					continue
				}
			}
			ret = append(ret, fact)
			continue
		}
		keep := false
		for _, aspect := range required {
			if knowledgeEvidenceFactSupportsAspect(fact, aspect) {
				keep = true
				break
			}
		}
		if !keep && fact.Aspect == "existence" && knowledgeEvidenceTextHasNegativeBoundary(fact.Statement) && knowledgeEvidenceNegativeFactAnswersTask(task, fact) {
			keep = true
		}
		if !keep && requiredKnowledgeEvidenceAspect(required, "scope") && fact.Aspect == "existence" {
			keep = true
		}
		if !keep && requiredKnowledgeEvidenceAspect(required, "method") && knowledgeEvidenceMethodBoundaryRelevantToTask(task, fact, facts) {
			keep = true
		}
		if !keep && requiredKnowledgeEvidenceAspect(required, "method") && fact.Aspect == "location" && knowledgeEvidenceTextHasMethodCue(fact.Statement) {
			// A self-service method is incomplete without the grounded pickup location
			// stated in the same FAQ clause.
			keep = true
		}
		if !keep && requiredKnowledgeEvidenceAspect(required, "method") && fact.Aspect == "location" && knowledgeEvidenceServiceRequestAllowsActionableLocation(task) {
			keep = true
		}
		if !keep && requiredKnowledgeEvidenceAspect(required, "price") && (knowledgeEvidenceQueryAsksComparison(task.Query) || knowledgeEvidenceQueryAsksPriceBoundary(task.Query)) {
			compact := normalizeRuntimeKnowledgeQuery(fact.Statement)
			if (fact.Aspect == "condition" || fact.Aspect == "scope") && containsAny(compact, []string{"平台", "权益", "不同", "调整"}) {
				keep = true
			}
			if fact.Aspect == "condition" && containsAny(compact, []string{"情况", "为准", "而定", "取决于"}) {
				keep = true
			}
			if fact.Aspect == "method" && containsAny(compact, []string{"对比", "比较", "选择", "联系"}) {
				keep = true
			}
		}
		if keep {
			ret = append(ret, fact)
		}
	}
	return ret
}

func finalizeKnowledgeEvidenceFactsForTask(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	groundedFacts := canonicalizeKnowledgeEvidenceFacts(sanitizeKnowledgeEvidenceFacts(append([]knowledgeEvidenceFact(nil), facts...)))
	filteredFacts := filterKnowledgeEvidenceFactsForTask(task, groundedFacts)
	return canonicalizeKnowledgeEvidenceFacts(sanitizeKnowledgeEvidenceFacts(filteredFacts))
}

func sanitizeKnowledgeEvidenceFacts(facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	for index := range facts {
		facts[index].CriticalValues = sanitizeKnowledgeEvidenceCriticalValuesForStatement(facts[index].CriticalValues, facts[index].Statement)
	}
	return facts
}

func sanitizeKnowledgeEvidenceCriticalValues(values []string) []string {
	return sanitizeKnowledgeEvidenceCriticalValuesForStatement(values, "")
}

func sanitizeKnowledgeEvidenceCriticalValuesForStatement(values []string, statement string) []string {
	ret := make([]string, 0, len(values))
	for _, rawValue := range values {
		value := canonicalKnowledgeEvidenceCriticalValue(strings.TrimSpace(rawValue))
		if value == "" || knowledgeEvidenceCriticalValueIsParaphrasable(value) {
			continue
		}
		if knowledgeEvidenceCriticalValueIsBareSequence(value) && !knowledgeEvidenceBareSequenceIsMeaningful(statement, value) {
			continue
		}
		ret = appendIfMissing(ret, value)
	}
	return ret
}

func canonicalKnowledgeEvidenceCriticalValue(value string) string {
	switch normalizeRuntimeKnowledgeQuery(value) {
	case "扫人脸", "刷人脸", "扫脸", "刷脸":
		return "人脸"
	case "app":
		return "APP"
	case "酒店名称", "门店名称", "酒店名", "门店名":
		return "酒店名"
	case "对应楼层", "所在楼层", "楼层":
		return "楼层"
	case "房间号", "门牌号", "房号":
		return "房号"
	default:
		return value
	}
}

func knowledgeEvidenceCriticalValueIsParaphrasable(value string) bool {
	switch normalizeRuntimeKnowledgeQuery(value) {
	case "建议", "选择", "联系", "回复", "对比", "比较", "通过", "使用", "操作", "办理", "申请", "下载", "点击", "输入":
		return true
	default:
		return false
	}
}

func knowledgeEvidenceCriticalValueIsBareSequence(value string) bool {
	runes := []rune(normalizeCriticalValueText(value))
	if len(runes) == 0 || len(runes) > 2 {
		return false
	}
	for _, character := range runes {
		if !strings.ContainsRune("0123456789一二三四五六七八九十", character) {
			return false
		}
	}
	return true
}

func knowledgeEvidenceBareSequenceIsMeaningful(statement string, value string) bool {
	statement = strings.TrimSpace(statement)
	value = strings.TrimSpace(value)
	if statement == "" || value == "" {
		return false
	}
	for _, quoted := range []string{"“" + value + "”", `"` + value + `"`, "‘" + value + "’", "'" + value + "'", "「" + value + "」", "『" + value + "』"} {
		if strings.Contains(statement, quoted) {
			return true
		}
	}
	for offset := 0; offset < len(statement); {
		index := strings.Index(statement[offset:], value)
		if index < 0 {
			break
		}
		index += offset
		prefixRunes := []rune(statement[:index])
		if len(prefixRunes) > 4 {
			prefixRunes = prefixRunes[len(prefixRunes)-4:]
		}
		prefix := string(prefixRunes)
		suffix := statement[index+len(value):]
		if containsAny(prefix, []string{"回复", "选择", "发送", "输入", "按", "选", "回", "发"}) ||
			strings.HasPrefix(strings.TrimSpace(suffix), "号线") || strings.HasPrefix(strings.TrimSpace(suffix), "号") ||
			strings.HasPrefix(strings.TrimSpace(suffix), "楼") || strings.HasPrefix(strings.TrimSpace(suffix), "层") {
			return true
		}
		offset = index + len(value)
	}
	return false
}

func narrowKnowledgeEvidenceFactToTask(task knowledgeEvidenceJudgeTask, fact knowledgeEvidenceFact, required []string) knowledgeEvidenceFact {
	if len(required) != 1 || required[0] != "existence" || fact.Aspect != "existence" {
		return fact
	}
	clauses := splitKnowledgeEvidenceAnswerClauses(fact.Statement)
	if len(clauses) < 2 {
		return fact
	}
	for _, clause := range clauses {
		candidate := fact
		candidate.Statement = strings.TrimSpace(clause) + "。"
		candidate.CriticalValues = filterKnowledgeEvidenceCriticalValuesForStatement(fact.CriticalValues, candidate.Statement)
		if knowledgeEvidenceFactSupportsAspect(candidate, "existence") {
			return candidate
		}
	}
	return fact
}

func filterKnowledgeEvidenceCriticalValuesForStatement(values []string, statement string) []string {
	ret := make([]string, 0, len(values))
	compact := normalizeRuntimeKnowledgeQuery(statement)
	for _, value := range values {
		if strings.Contains(compact, normalizeRuntimeKnowledgeQuery(value)) {
			ret = appendIfMissing(ret, value)
		}
	}
	return ret
}

func requiredKnowledgeEvidenceAspects(task knowledgeEvidenceJudgeTask) []string {
	query := normalizeRuntimeKnowledgeQuery(task.Query)
	asksPhoneValue := knowledgeEvidenceTaskAsksPhoneValue(task)
	ret := make([]string, 0, 3)
	appendAspect := func(aspect string) {
		if aspect != "" && !knowledgeEvidenceContainsString(ret, aspect) {
			ret = append(ret, aspect)
		}
	}
	if asksPhoneValue {
		appendAspect("phone")
	}
	switch semanticGateNormalizeObjective(task.Objective) {
	case "availability":
		appendAspect("existence")
	case "quantity":
		appendAspect("quantity")
	case "price":
		appendAspect("price")
	case "time":
		appendAspect("time")
	case "location":
		appendAspect("location")
	case "method":
		if asksPhoneValue {
			break
		} else if strings.Contains(query, "怎么填") {
			appendAspect("location")
		} else {
			appendAspect("method")
		}
	case "action_request":
		if canonicalIntentCode(task.Intent) == "service_request" && !asksPhoneValue {
			appendAspect("method")
		}
	}
	if containsAny(query, []string{"几瓶", "几个", "几间", "几台", "几条", "几套", "几双", "几把", "几包", "几盒", "几袋", "几件", "几支", "几只", "几辆", "几杯", "几桶", "几卷", "多少瓶", "多少个", "多少台", "多少条", "多少套", "多少双", "多少把", "多少包", "多少盒", "多少袋", "多少件", "多少支", "多少只", "多少辆", "多少杯", "多少桶", "多少卷", "数量"}) {
		appendAspect("quantity")
	}
	if len(knowledgeEvidenceTaskBoundCriticalValues(task.Query)) > 0 {
		appendAspect("quantity")
	}
	if containsAny(query, []string{
		"免费", "收费", "多少钱", "价格", "费用", "价钱", "房价", "单价", "收费标准", "要不要钱", "需要多少钱", "花多少钱", "付多少钱",
	}) {
		appendAspect("price")
	}
	if containsAny(query, []string{"几点", "多久", "什么时候", "何时", "时间"}) {
		appendAspect("time")
	}
	if containsAny(query, []string{"在哪", "哪里", "地址", "位置", "楼层", "怎么填"}) {
		appendAspect("location")
	}
	if !asksPhoneValue && !strings.Contains(query, "怎么填") && knowledgeEvidenceQueryAsksMethod(query) {
		appendAspect("method")
	}
	if knowledgeEvidenceQueryAsksExistence(task.Query) &&
		!(semanticGateNormalizeObjective(task.Objective) == "recommendation" && knowledgeEvidenceSpatialRecommendationTopic(task.Query) != "") {
		appendAspect("existence")
	}
	if containsAny(query, []string{"送到", "哪些", "全部", "范围"}) ||
		(strings.Contains(query, "都有") && !knowledgeEvidenceTaskNamesFiniteSubjectSet(task)) {
		appendAspect("scope")
	}
	if canonicalIntentCode(task.Intent) == "service_request" && len(ret) == 0 && !asksPhoneValue {
		appendAspect("method")
	}
	return ret
}

var knowledgeEvidencePhoneValuePattern = regexp.MustCompile(`(?:^|[^0-9])(?:(?:\+?86[- ]?)?(?:1[3-9][0-9]{9}|1[3-9][0-9][ -][0-9]{4}[ -][0-9]{4})|0[0-9]{2,3}[- ]?[0-9]{7,8}|\(0[0-9]{2,3}\)[- ]?[0-9]{7,8}|(?:400|800)[- ]?[0-9]{3}[- ]?[0-9]{4})(?:[^0-9]|$)`)

func knowledgeEvidenceFactHasPhoneValue(fact knowledgeEvidenceFact) bool {
	raw := fact.Statement + " " + strings.Join(fact.CriticalValues, " ")
	for _, clause := range splitKnowledgeEvidenceAnswerClauses(raw) {
		if !knowledgeEvidencePhoneValuePattern.MatchString(clause) {
			continue
		}
		compact := normalizeRuntimeKnowledgeQuery(clause)
		phoneLabel := containsAny(compact, []string{"联系电话", "联系号码", "电话号码", "手机号", "座机", "电话", "联系方式"})
		nonPhoneLabel := containsAny(compact, []string{
			"订单号", "工号", "房号", "身份证", "验证码", "会员号", "流水号", "运单号", "单号", "编号",
		})
		if phoneLabel || !nonPhoneLabel {
			return true
		}
	}
	return false
}

func knowledgeEvidenceTaskAsksPhoneValue(task knowledgeEvidenceJudgeTask) bool {
	query := normalizeRuntimeKnowledgeQuery(task.Query)
	if containsAny(query, []string{
		"怎么联系", "如何联系", "联系一下", "电话联系", "打电话", "拨打电话", "通过电话", "用电话",
		"修改", "更改", "更新", "换成", "改成", "绑定", "解绑", "删除", "添加", "设置", "录入", "保存",
	}) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(task.Objective)) {
	case "phone", "contact_phone", "store_phone":
		return true
	}
	if containsAny(query, []string{"联系电话", "联系号码", "电话号码", "手机号"}) {
		return true
	}
	valueCue := containsAny(query, []string{"多少", "是什么", "是啥", "几号", "号码", "给我", "告诉我", "发我"})
	return valueCue && containsAny(query, []string{"电话", "联系方式"})
}

func knowledgeEvidenceQueryAsksMethod(query string) bool {
	if containsAny(query, []string{
		"办理", "操作", "打开", "领取", "自取", "拿取", "取用", "获取", "去拿", "在哪拿", "使用", "申请", "登记", "支付", "付款", "联系", "投屏", "调整", "调节",
	}) {
		return true
	}
	for _, prefix := range []string{"怎么", "如何", "怎样"} {
		for _, action := range []string{
			"办", "开", "关", "用", "拿", "取", "申请", "登记", "入住", "退房", "支付", "付款", "联系", "走", "投屏", "调", "处理", "解决",
		} {
			if strings.Contains(query, prefix+action) {
				return true
			}
		}
	}
	return false
}

func knowledgeEvidenceQueryAsksExistence(text string) bool {
	compactText := normalizeRuntimeKnowledgeQuery(text)
	if containsAny(compactText, []string{"有没有", "是否有", "是不是有", "有无", "提供吗", "配备吗", "是否提供", "是否配备"}) {
		return true
	}
	if !strings.Contains(compactText, "吗") {
		return false
	}
	for _, clause := range splitKnowledgeEvidenceAnswerClauses(text) {
		compact := normalizeRuntimeKnowledgeQuery(clause)
		if strings.Contains(compact, "有") && len(knowledgeEvidenceTaskBoundCriticalValues(clause)) == 0 &&
			!knowledgeEvidenceOpenQuantityPattern.MatchString(compact) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceTaskNamesFiniteSubjectSet(task knowledgeEvidenceJudgeTask) bool {
	query := normalizeKnowledgeEvidenceSubjectForMatch(task.Query)
	entitiesByType := make(map[string]map[string]struct{}, len(task.Entities))
	for _, entity := range task.Entities {
		entityType := strings.ToLower(strings.TrimSpace(entity.Type))
		if entityType == "" {
			continue
		}
		value := normalizeKnowledgeEvidenceSubjectForMatch(normalizeKnowledgeEvidenceEntityText(entity))
		if len([]rune(value)) < 2 || !strings.Contains(query, value) ||
			knowledgeEvidenceContainsString([]string{"酒店", "门店", "房间", "客房", "房型", "客户", "服务", "问题"}, value) {
			continue
		}
		if entitiesByType[entityType] == nil {
			entitiesByType[entityType] = make(map[string]struct{}, 2)
		}
		entitiesByType[entityType][value] = struct{}{}
		if len(entitiesByType[entityType]) >= 2 {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFactSupportsAspect(fact knowledgeEvidenceFact, aspect string) bool {
	raw := fact.Statement + " " + strings.Join(fact.CriticalValues, " ")
	compact := normalizeRuntimeKnowledgeQuery(raw)
	if aspect != "condition" && knowledgeEvidenceTextHasUncertaintyBoundary(raw) {
		return false
	}
	switch aspect {
	case "quantity":
		return fact.Aspect == "quantity" && knowledgeEvidenceStrictQuantityPattern.MatchString(compact)
	case "price":
		return fact.Aspect == "price" && len(knowledgeEvidencePriceClaims(raw)) > 0
	case "time":
		return fact.Aspect == "time" && (knowledgeEvidenceAnswerTimePattern.MatchString(raw) || containsAny(compact, []string{"时间", "工作日", "分钟", "小时", "天", "点"}))
	case "location":
		return fact.Aspect == "location" && knowledgeEvidenceTextHasLocationCue(compact)
	case "method":
		return fact.Aspect == "method" && knowledgeEvidenceTextHasMethodCue(compact)
	case "scope":
		return fact.Aspect == "scope" && containsAny(compact, []string{"范围", "送到", "全部", "所有", "都", "仅限", "适用", "包括", "包含", "分别是", "分别为"})
	case "condition":
		return fact.Aspect == "condition" && containsAny(compact, []string{"如果", "条件", "取决于", "为准", "而定", "具体情况"})
	case "phone":
		return fact.Aspect == "other" && knowledgeEvidenceFactHasPhoneValue(fact)
	case "existence":
		return fact.Aspect == "existence" && containsAny(compact, []string{
			"有", "没有", "提供", "配备", "不提供", "无", "不含",
			"可以", "不可以", "支持", "不支持", "需要", "不需要", "无需", "不用", "不能",
		})
	default:
		return fact.Aspect == aspect
	}
}

func knowledgeEvidenceFactsCoverRequiredAspect(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact, aspect string) bool {
	if aspect == "price" {
		return knowledgeEvidencePriceFactsAnswerTask(task, facts)
	}
	for _, fact := range facts {
		if aspect != "condition" && knowledgeEvidenceTextHasUncertaintyBoundary(fact.Statement+" "+strings.Join(fact.CriticalValues, " ")) {
			continue
		}
		if knowledgeEvidenceFactSupportsAspect(fact, aspect) {
			return true
		}
		if aspect == "method" && fact.Aspect == "location" && knowledgeEvidenceTextHasLocationCue(fact.Statement) && knowledgeEvidenceServiceRequestAllowsActionableLocation(task) {
			return true
		}
		if fact.Aspect == "existence" && knowledgeEvidenceTextHasNegativeBoundary(fact.Statement) && knowledgeEvidenceNegativeFactAnswersTask(task, fact) {
			return true
		}
	}
	return false
}

func knowledgeEvidencePriceFactsAnswerTask(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact) bool {
	claims := make([]knowledgeEvidencePriceClaim, 0, len(facts)*2)
	for _, fact := range facts {
		if strings.TrimSpace(fact.Aspect) != "price" {
			continue
		}
		for _, claim := range knowledgeEvidencePriceClaims(fact.Statement + " " + strings.Join(fact.CriticalValues, " ")) {
			claims = appendKnowledgeEvidencePriceClaim(claims, claim)
		}
	}
	if knowledgeEvidenceQueryAsksDirectionalPriceComparison(task.Query) {
		return knowledgeEvidencePriceClaimsContain(claims, "cheaper") ||
			knowledgeEvidencePriceClaimsContain(claims, "dearer") ||
			knowledgeEvidencePriceClaimCount(claims, "amount") >= 2
	}
	if knowledgeEvidenceQueryAsksComparison(task.Query) {
		if knowledgeEvidencePriceClaimsContain(claims, "equal") ||
			knowledgeEvidencePriceClaimsContain(claims, "not_equal") ||
			knowledgeEvidencePriceClaimCount(claims, "amount") >= 2 ||
			knowledgeEvidenceAllRequiredSubjectsHaveFreePriceFact(task, facts) {
			return true
		}
		for _, fact := range facts {
			compact := normalizeRuntimeKnowledgeQuery(fact.Statement)
			if fact.Aspect == "method" && containsAny(compact, []string{"对比", "比较", "选择"}) {
				return true
			}
			if (fact.Aspect == "condition" || fact.Aspect == "scope") &&
				knowledgeEvidencePriceClaimsContain(knowledgeEvidencePriceClaims(fact.Statement), "dynamic") {
				return true
			}
		}
		return false
	}
	if knowledgeEvidenceQueryAsksPriceAmount(task.Query) {
		return knowledgeEvidencePriceClaimsContain(claims, "amount") || knowledgeEvidencePriceClaimsContain(claims, "free")
	}
	if knowledgeEvidenceQueryAsksAbsolutePriceStatus(task.Query) {
		return knowledgeEvidencePriceClaimsContain(claims, "free") ||
			knowledgeEvidencePriceClaimsContain(claims, "charged") ||
			knowledgeEvidencePriceClaimsContain(claims, "amount")
	}
	if knowledgeEvidenceQueryAsksPriceBoundary(task.Query) {
		if len(claims) > 0 {
			return true
		}
		for _, fact := range facts {
			compact := normalizeRuntimeKnowledgeQuery(fact.Statement)
			if fact.Aspect == "method" && containsAny(compact, []string{"联系", "对比", "比较", "选择"}) {
				return true
			}
			if fact.Aspect == "condition" && containsAny(compact, []string{"情况", "为准", "而定", "取决于"}) {
				return true
			}
		}
		return false
	}
	return knowledgeEvidencePriceClaimsContain(claims, "free") ||
		knowledgeEvidencePriceClaimsContain(claims, "charged") ||
		knowledgeEvidencePriceClaimsContain(claims, "amount") ||
		knowledgeEvidencePriceClaimsContain(claims, "equal") ||
		knowledgeEvidencePriceClaimsContain(claims, "not_equal")
}

func knowledgeEvidenceAllRequiredSubjectsHaveFreePriceFact(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact) bool {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) < 2 {
		return false
	}
	covered := make(map[string]struct{}, len(requiredSubjects))
	for _, fact := range facts {
		if strings.TrimSpace(fact.Aspect) != "price" ||
			!knowledgeEvidencePriceClaimsContain(knowledgeEvidencePriceClaims(fact.Statement+" "+strings.Join(fact.CriticalValues, " ")), "free") {
			continue
		}
		text := normalizeKnowledgeEvidenceSubjectForMatch(fact.Statement)
		for _, subject := range requiredSubjects {
			if strings.Contains(text, subject) {
				covered[subject] = struct{}{}
			}
		}
		if knowledgeEvidenceFAQAnswerCollectivelyCoversTaskSubjectsForAspect(task, fact.Statement, "price") {
			for _, subject := range requiredSubjects {
				covered[subject] = struct{}{}
			}
		}
	}
	return len(covered) == len(requiredSubjects)
}

func knowledgeEvidenceTextHasLocationCue(text string) bool {
	compact := normalizeRuntimeKnowledgeQuery(text)
	return knowledgeEvidenceLocationAnchor(text) != "" || containsAny(compact, []string{
		"地址", "位于", "楼", "层", "对面", "入口", "房间号", "门牌号",
		"洗衣房", "百宝箱", "床头柜", "电视柜", "抽屉", "柜子", "大厅", "前台旁", "旁边", "附近", "电梯口", "楼下", "楼上",
	})
}

func knowledgeEvidenceTextHasMethodCue(text string) bool {
	compact := normalizeRuntimeKnowledgeQuery(text)
	return containsAny(compact, []string{
		"通过", "使用", "扫码", "点击", "输入", "操作", "办理", "联系", "回复", "选择", "对比", "比较", "申请", "下载", "登记",
		"领取", "自取", "拿取", "取用", "获取", "前往", "去拿", "拿到", "取到", "到店办理", "现场办理",
		"刷脸", "扫脸", "扫人脸", "人脸开门", "房卡开门", "密码开门", "入住机办理", "小程序办理", "开门",
	})
}

func knowledgeEvidenceServiceRequestAllowsActionableLocation(task knowledgeEvidenceJudgeTask) bool {
	if canonicalIntentCode(task.Intent) != "service_request" {
		return false
	}
	switch knowledgeEvidenceServiceOperationTarget(task.Query) {
	case "locate", "replenish", "pickup":
		return true
	default:
		return false
	}
}

func knowledgeEvidenceServiceOperationTarget(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	switch {
	case containsAny(compact, []string{"坏了", "坏掉", "故障", "不工作", "用不了", "不能用", "无法使用", "打不开", "堵了", "堵住", "漏水", "不制冷", "不出风", "取不出来", "噪音", "好吵", "太吵"}):
		return "malfunction"
	case containsAny(compact, []string{"调温", "调节温度", "温度怎么调", "风速怎么调", "调风速"}):
		return "adjust"
	case containsAny(compact, []string{"关闭", "关掉", "怎么关", "关空调", "关电视"}):
		return "turn_off"
	case containsAny(compact, []string{"开启", "打开", "怎么开", "开空调", "开电视"}):
		return "turn_on"
	case containsAny(compact, []string{"送到", "送来", "送至", "配送", "送一下", "帮我拿", "拿到房间", "送到房间"}):
		return "delivery"
	case containsAny(compact, []string{"更换", "换一个", "换个", "换新", "替换"}):
		return "replace"
	case containsAny(compact, []string{"领取", "自取", "拿取", "取用", "去拿", "在哪拿", "哪里拿", "怎么拿", "怎么取", "前往领取", "前往自取"}):
		return "pickup"
	case containsAny(compact, []string{"找不到", "没找到", "在哪里", "在哪", "哪里", "位置", "放哪"}):
		return "locate"
	case containsAny(compact, []string{"没了", "没有了", "用完了", "不够", "缺少", "缺了", "需要额外", "再要", "多要"}):
		return "replenish"
	default:
		return ""
	}
}

func knowledgeEvidenceServiceOperationTargetsCompatible(taskTarget string, candidateTarget string) bool {
	if taskTarget == "" || candidateTarget == "" {
		return false
	}
	if taskTarget == candidateTarget {
		return true
	}
	if (taskTarget == "replenish" || taskTarget == "locate" || taskTarget == "pickup") &&
		(candidateTarget == "replenish" || candidateTarget == "locate" || candidateTarget == "pickup") {
		return true
	}
	return false
}

func knowledgeEvidenceNegativeFactAnswersTask(task knowledgeEvidenceJudgeTask, fact knowledgeEvidenceFact) bool {
	query := normalizeRuntimeKnowledgeQuery(task.Query)
	statement := normalizeRuntimeKnowledgeQuery(fact.Statement)
	if query == "" || statement == "" {
		return false
	}
	for _, entity := range task.Entities {
		value := normalizeRuntimeKnowledgeQuery(entity.Text)
		if len([]rune(value)) >= 2 && strings.Contains(query, value) && strings.Contains(statement, value) {
			return true
		}
	}
	statementRunes := []rune(statement)
	for index := 0; index+2 <= len(statementRunes); index++ {
		token := string(statementRunes[index : index+2])
		if containsAny(token, []string{"酒店", "没有", "暂不", "提供", "不提"}) {
			continue
		}
		if strings.Contains(query, token) {
			return true
		}
	}
	return knowledgeEvidenceTextNGramSimilarity(query, statement) >= 0.22
}

func knowledgeEvidenceMethodChannels() []string {
	return []string{
		"传统前台", "前台", "入住机", "自助机", "登记机", "小程序", "短信链接", "二维码",
		"房卡", "门锁密码", "密码", "人脸", "电话", "微信", "支付宝", "银行卡", "APP", "app",
	}
}

func knowledgeEvidenceMethodDomain(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	switch {
	case containsAny(compact, []string{"支付", "付款", "转账", "缴费", "收款", "微信支付", "支付宝", "银行卡"}):
		return "payment"
	case containsAny(compact, []string{"开门", "房门", "门锁", "房卡", "刷脸", "扫脸", "扫人脸", "人脸", "门锁密码"}):
		return "door_access"
	case containsAny(compact, []string{"入住", "登记", "入住机", "登记机", "传统前台", "办理入住"}):
		return "check_in"
	case containsAny(compact, []string{"联系", "电话", "管家", "客服", "同事"}):
		return "contact"
	default:
		return ""
	}
}

func knowledgeEvidenceMethodChannelDomain(channel string) string {
	switch normalizeRuntimeKnowledgeQuery(channel) {
	case "传统前台", "前台", "入住机", "自助机", "登记机":
		return "check_in"
	case "房卡", "门锁密码", "密码", "人脸":
		return "door_access"
	case "微信", "支付宝", "银行卡":
		return "payment"
	case "电话":
		return "contact"
	default:
		return ""
	}
}

func knowledgeEvidenceTaskMethodDomain(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact) string {
	taskDomain := knowledgeEvidenceMethodDomain(task.Query)
	if taskDomain == "" {
		for _, fact := range facts {
			if fact.Aspect == "method" {
				if domain := knowledgeEvidenceMethodDomain(fact.Statement); domain != "" {
					taskDomain = domain
					break
				}
			}
		}
	}
	return taskDomain
}

func knowledgeEvidenceRelevantMethodChannels(task knowledgeEvidenceJudgeTask, clause string, facts []knowledgeEvidenceFact) []string {
	taskDomain := knowledgeEvidenceTaskMethodDomain(task, facts)
	clauseDomain := knowledgeEvidenceMethodDomain(clause)
	if taskDomain != "" && clauseDomain != "" && taskDomain != clauseDomain {
		return nil
	}
	compact := normalizeRuntimeKnowledgeQuery(clause)
	ret := make([]string, 0, 2)
	for _, channel := range knowledgeEvidenceMethodChannels() {
		if !strings.Contains(compact, normalizeRuntimeKnowledgeQuery(channel)) {
			continue
		}
		channelDomain := knowledgeEvidenceMethodChannelDomain(channel)
		if taskDomain != "" && channelDomain != "" && taskDomain != channelDomain {
			continue
		}
		ret = appendIfMissing(ret, channel)
	}
	return ret
}

func knowledgeEvidenceMethodBoundaryRelevantToTask(task knowledgeEvidenceJudgeTask, boundary knowledgeEvidenceFact, facts []knowledgeEvidenceFact) bool {
	if boundary.Aspect != "existence" || !knowledgeEvidenceTextHasNegativeBoundary(boundary.Statement) {
		return false
	}
	taskDomain := knowledgeEvidenceTaskMethodDomain(task, facts)
	boundaryDomain := knowledgeEvidenceMethodDomain(boundary.Statement)
	if taskDomain != "" && boundaryDomain != "" && taskDomain != boundaryDomain {
		return false
	}
	if knowledgeEvidenceNegativeFactAnswersTask(task, boundary) {
		return true
	}
	if len(knowledgeEvidenceRelevantMethodChannels(task, boundary.Statement, facts)) == 0 {
		return false
	}
	for _, fact := range facts {
		if fact.Aspect != "method" || !knowledgeEvidenceFactSupportsAspect(fact, "method") {
			continue
		}
		if len(knowledgeEvidenceRelevantMethodChannels(task, fact.Statement, facts)) > 0 {
			return true
		}
	}
	return false
}

func missingRequiredKnowledgeEvidenceAspects(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact) []string {
	ret := make([]string, 0, 2)
	requiredAspects := requiredKnowledgeEvidenceAspects(task)
	timeRequirements := requiredKnowledgeEvidenceTimeRequirements(task)
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	quantityRequirements := []knowledgeEvidenceQuantityRequirement(nil)
	if len(requiredSubjects) <= 1 {
		quantitySubject := ""
		if len(requiredSubjects) == 1 {
			quantitySubject = requiredSubjects[0]
		}
		quantityRequirements = knowledgeEvidenceTaskBoundQuantityRequirements(task, quantitySubject)
	}
	for _, aspect := range requiredAspects {
		if aspect == "time" && len(timeRequirements) > 0 {
			continue
		}
		if aspect == "quantity" && len(quantityRequirements) > 0 {
			continue
		}
		if knowledgeEvidenceFactsCoverRequiredAspect(task, facts, aspect) {
			continue
		}
		ret = append(ret, knowledgeEvidenceAspectLabel(aspect))
	}
	for _, requirement := range timeRequirements {
		if knowledgeEvidenceFactsCoverSubjectConditionTimeSlot(facts, requirement.Subject, requirement.Condition, requirement.Slot) {
			continue
		}
		ret = appendIfMissing(ret, knowledgeEvidenceTimeConditionLabel(requirement.Condition)+requirement.Subject+knowledgeEvidenceTimeSlotLabel(requirement.Slot))
	}
	for _, requirement := range quantityRequirements {
		if knowledgeEvidenceFactsCoverQuantityRequirement(facts, requirement) {
			continue
		}
		ret = appendIfMissing(ret, knowledgeEvidenceQuantityRequirementMissingAspect(requirement))
	}
	for _, requirement := range requiredKnowledgeEvidenceSubjectAspectPairs(task) {
		if knowledgeEvidenceFactsCoverSubjectAspect(task, facts, requirement.Subject, requirement.Aspect) {
			continue
		}
		ret = appendIfMissing(ret, requirement.Subject+knowledgeEvidenceAspectLabel(requirement.Aspect))
	}
	configurationValues := make(map[string][]string, 2)
	for _, fact := range facts {
		for field, values := range knowledgeEvidenceConfigurationValues(fact.Statement) {
			for _, value := range values {
				configurationValues[field] = appendIfMissing(configurationValues[field], value)
			}
		}
	}
	for _, field := range knowledgeEvidenceConfigurationFields(task.Query) {
		if len(configurationValues[field]) == 0 {
			ret = append(ret, knowledgeEvidenceConfigurationFieldLabel(field))
		}
	}
	return ret
}

func strictMechanicalMissingKnowledgeEvidenceAspects(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact) []string {
	return missingRequiredKnowledgeEvidenceAspects(task, facts)
}

func unresolvedModelKnowledgeEvidenceMissingAspects(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact, missingAspects []string) []string {
	if len(missingAspects) == 0 || len(facts) == 0 {
		return append([]string(nil), missingAspects...)
	}
	requiredAspects := requiredKnowledgeEvidenceAspects(task)
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	ret := make([]string, 0, len(missingAspects))
	for _, missingAspect := range missingAspects {
		aspect := knowledgeEvidenceAspectFromMissingAspect(missingAspect)
		aspectRequired := requiredKnowledgeEvidenceAspect(requiredAspects, aspect)
		if aspect == "quantity" && len(knowledgeEvidenceTaskBoundCriticalValues(task.Query)) > 0 {
			aspectRequired = true
		}
		if aspect == "" || !aspectRequired {
			ret = append(ret, missingAspect)
			continue
		}

		missingText := normalizeKnowledgeEvidenceSubjectForMatch(missingAspect)
		mentionedSubjects := make([]string, 0, len(requiredSubjects))
		for _, subject := range requiredSubjects {
			if strings.Contains(missingText, subject) {
				mentionedSubjects = append(mentionedSubjects, subject)
			}
		}
		if len(mentionedSubjects) == 0 {
			if len(requiredSubjects) > 1 {
				ret = append(ret, missingAspect)
				continue
			}
			if len(requiredSubjects) == 1 && !knowledgeEvidenceFactsCoverMissingAspect(task, facts, requiredSubjects[0], aspect, missingAspect) {
				ret = append(ret, missingAspect)
				continue
			}
			if len(requiredSubjects) == 0 && !knowledgeEvidenceFactsCoverMissingAspect(task, facts, "", aspect, missingAspect) {
				ret = append(ret, missingAspect)
			}
			continue
		}

		resolved := true
		for _, subject := range mentionedSubjects {
			if !knowledgeEvidenceFactsCoverMissingAspect(task, facts, subject, aspect, missingAspect) {
				resolved = false
				break
			}
		}
		if !resolved {
			ret = append(ret, missingAspect)
		}
	}
	return ret
}

func knowledgeEvidenceFactsCoverMissingAspect(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact, subject string, aspect string, missingAspect string) bool {
	if aspect == "time" {
		role := knowledgeEvidenceConflictQuestionFieldRole(normalizeRuntimeKnowledgeQuery(missingAspect), "time")
		if role == "start" || role == "end" || role == "duration" || role == "schedule" {
			condition := ""
			if conditions := knowledgeEvidenceCalendarConditions(missingAspect); len(conditions) == 1 {
				condition = conditions[0]
			}
			return knowledgeEvidenceFactsCoverSubjectConditionTimeSlot(facts, subject, condition, role)
		}
	}
	if strings.TrimSpace(subject) != "" {
		return knowledgeEvidenceFactsCoverSubjectAspect(task, facts, subject, aspect)
	}
	return knowledgeEvidenceFactsCoverRequiredAspect(task, facts, aspect)
}

func requiredKnowledgeEvidenceTimeSlots(query string) []string {
	compact := normalizeRuntimeKnowledgeQuery(query)
	ret := make([]string, 0, 2)
	asksRange := containsAny(compact, []string{
		"几点到几点", "几点开始和结束", "几点开始与结束", "几点开始及结束", "几点开始、结束",
		"开始和结束分别是几点", "开始与结束分别是几点", "开始及结束分别是几点",
		"开始和结束时间", "开始与结束时间", "开始及结束时间",
		"从什么时候到什么时候", "什么时候到什么时候", "从几点到几点", "几点至几点", "从何时到何时",
	})
	if asksRange || containsAny(compact, []string{"几点开始", "什么时候开始", "开始时间", "几点开门", "开门时间", "从几点"}) {
		ret = append(ret, "start")
	}
	if asksRange || containsAny(compact, []string{"几点结束", "什么时候结束", "结束时间", "截止时间", "供应到几点", "营业到几点", "几点关门", "关门时间", "到几点"}) {
		ret = append(ret, "end")
	}
	if containsAny(compact, []string{"多久", "多长时间", "时长"}) {
		ret = append(ret, "duration")
	}
	return ret
}

func requiredKnowledgeEvidenceTimeSlotsForTask(task knowledgeEvidenceJudgeTask) []string {
	if slots := requiredKnowledgeEvidenceTimeSlots(task.Query); len(slots) > 0 {
		return slots
	}
	if !requiredKnowledgeEvidenceAspect(requiredKnowledgeEvidenceAspects(task), "time") {
		return nil
	}
	role := knowledgeEvidenceConflictQuestionFieldRole(normalizeRuntimeKnowledgeQuery(task.Query), "time")
	switch role {
	case "start", "end", "duration", "schedule":
		return []string{role}
	default:
		return []string{"schedule"}
	}
}

type knowledgeEvidenceTimeRequirement struct {
	Subject   string
	Condition string
	Slot      string
}

func requiredKnowledgeEvidenceTimeRequirements(task knowledgeEvidenceJudgeTask) []knowledgeEvidenceTimeRequirement {
	requiredSlots := requiredKnowledgeEvidenceTimeSlotsForTask(task)
	if len(requiredSlots) == 0 {
		return nil
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	requiredConditions := requiredKnowledgeEvidenceTimeConditions(task.Query)
	defaultSubjects := append([]string(nil), requiredSubjects...)
	if len(defaultSubjects) == 0 {
		defaultSubjects = []string{""}
	}
	defaultConditions := append([]string(nil), requiredConditions...)
	if len(defaultConditions) == 0 {
		defaultConditions = []string{""}
	}

	ret := make([]knowledgeEvidenceTimeRequirement, 0, len(defaultSubjects)*len(defaultConditions)*len(requiredSlots))
	seen := make(map[string]struct{}, cap(ret))
	appendRequirements := func(subjects []string, conditions []string, slots []string) {
		for _, subject := range subjects {
			for _, condition := range conditions {
				for _, slot := range slots {
					key := subject + "\x00" + condition + "\x00" + slot
					if _, exists := seen[key]; exists {
						continue
					}
					seen[key] = struct{}{}
					ret = append(ret, knowledgeEvidenceTimeRequirement{Subject: subject, Condition: condition, Slot: slot})
				}
			}
		}
	}

	pendingSubjects := make([]string, 0, len(requiredSubjects))
	pendingConditions := make([]string, 0, len(requiredConditions))
	lastSubjects := []string(nil)
	lastConditions := []string(nil)
	lastSlots := []string(nil)
	for _, clause := range splitKnowledgeEvidenceSubjectClauses(task.Query) {
		clauseText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
		clauseSubjects := knowledgeEvidenceContainedSubjects(clauseText, requiredSubjects)
		clauseConditions := requiredKnowledgeEvidenceTimeConditions(clause)
		clauseSlots := requiredKnowledgeEvidenceTimeSlotsForClause(task, clause)
		if len(clauseSlots) == 0 && len(lastSlots) > 0 && containsAny(normalizeRuntimeKnowledgeQuery(clause), []string{"呢", "那"}) {
			clauseSlots = append([]string(nil), lastSlots...)
		}
		if len(clauseSlots) == 0 {
			pendingSubjects = appendKnowledgeEvidenceStrings(pendingSubjects, clauseSubjects)
			pendingConditions = appendKnowledgeEvidenceStrings(pendingConditions, clauseConditions)
			continue
		}

		activeSubjects := appendKnowledgeEvidenceStrings(append([]string(nil), pendingSubjects...), clauseSubjects)
		pendingSubjects = nil
		if len(activeSubjects) == 0 {
			if len(lastSubjects) > 0 {
				activeSubjects = append([]string(nil), lastSubjects...)
			} else {
				activeSubjects = append([]string(nil), defaultSubjects...)
			}
		} else {
			lastSubjects = append([]string(nil), activeSubjects...)
		}

		activeConditions := appendKnowledgeEvidenceStrings(append([]string(nil), pendingConditions...), clauseConditions)
		pendingConditions = nil
		if len(activeConditions) == 0 {
			if len(lastConditions) > 0 {
				activeConditions = append([]string(nil), lastConditions...)
			} else {
				activeConditions = append([]string(nil), defaultConditions...)
			}
		} else {
			lastConditions = append([]string(nil), activeConditions...)
		}
		lastSlots = append([]string(nil), clauseSlots...)
		appendRequirements(activeSubjects, activeConditions, clauseSlots)
	}
	if len(ret) == 0 {
		appendRequirements(defaultSubjects, defaultConditions, requiredSlots)
	}
	return ret
}

func requiredKnowledgeEvidenceTimeSlotsForClause(task knowledgeEvidenceJudgeTask, clause string) []string {
	if slots := requiredKnowledgeEvidenceTimeSlots(clause); len(slots) > 0 {
		return slots
	}
	clauseTask := task
	clauseTask.Query = clause
	clauseTask.Objective = ""
	clauseTask.Intent = ""
	if !requiredKnowledgeEvidenceAspect(requiredKnowledgeEvidenceAspects(clauseTask), "time") {
		return nil
	}
	return []string{knowledgeEvidenceConflictQuestionFieldRole(normalizeRuntimeKnowledgeQuery(clause), "time")}
}

func knowledgeEvidenceFactsCoverSubjectTimeSlot(facts []knowledgeEvidenceFact, subject string, slot string) bool {
	return knowledgeEvidenceFactsCoverSubjectConditionTimeSlot(facts, subject, "", slot)
}

func knowledgeEvidenceFactsCoverSubjectConditionTimeSlot(facts []knowledgeEvidenceFact, subject string, condition string, slot string) bool {
	subject = normalizeKnowledgeEvidenceSubjectForMatch(subject)
	for _, fact := range facts {
		if !knowledgeEvidenceFactSupportsAspect(fact, "time") {
			continue
		}
		activeSubject := ""
		activeCondition := ""
		slotText := fact.Statement
		if len(knowledgeEvidenceIndividualTimePattern.FindAllString(slotText, -1)) == 0 {
			slotText = strings.TrimSpace(slotText + " " + strings.Join(fact.CriticalValues, " "))
		}
		for _, clause := range splitKnowledgeEvidenceTimeClauses(slotText) {
			clauseSubjectText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
			belongsToSubject := subject == ""
			switch {
			case subject != "" && strings.Contains(clauseSubjectText, subject):
				activeSubject = subject
				belongsToSubject = true
			case subject != "" && knowledgeEvidenceTimeFactHasExplicitSubject(clause):
				activeSubject = ""
				belongsToSubject = false
			case subject != "" && activeSubject == subject:
				belongsToSubject = true
			}
			if !belongsToSubject {
				continue
			}
			belongsToCondition := condition == ""
			if condition != "" {
				clauseConditions := knowledgeEvidenceCalendarConditions(clause)
				switch {
				case containsAny(normalizeRuntimeKnowledgeQuery(clause), []string{"每天", "每日", "天天"}):
					belongsToCondition = true
				case knowledgeEvidenceContainsString(clauseConditions, condition):
					activeCondition = condition
					belongsToCondition = true
				case len(clauseConditions) > 0:
					activeCondition = ""
					belongsToCondition = false
				case activeCondition == condition:
					belongsToCondition = true
				}
			}
			if !belongsToCondition {
				continue
			}
			role := knowledgeEvidenceConflictQuestionFieldRole(normalizeRuntimeKnowledgeQuery(clause), "time")
			if slot == "schedule" &&
				(knowledgeEvidenceIndividualTimePattern.MatchString(clause) || knowledgeEvidenceDurationValuePattern.MatchString(clause)) {
				return true
			}
			if strings.TrimSpace(knowledgeEvidenceTimeSlotValues(role, clause)[slot]) != "" {
				return true
			}
		}
	}
	return false
}

func requiredKnowledgeEvidenceTimeConditions(text string) []string {
	return knowledgeEvidenceCalendarConditions(text)
}

func knowledgeEvidenceCalendarConditions(text string) []string {
	ret := make([]string, 0, 3)
	for _, condition := range knowledgeEvidenceConflictConditions(text) {
		switch condition {
		case "workday", "weekend", "holiday":
			ret = appendIfMissing(ret, condition)
		default:
			if strings.HasPrefix(condition, "weekday:") {
				ret = appendIfMissing(ret, condition)
			}
		}
	}
	return ret
}

func knowledgeEvidenceTimeConditionLabel(condition string) string {
	switch condition {
	case "workday":
		return "工作日"
	case "weekend":
		return "周末"
	case "holiday":
		return "节假日"
	case "weekday:monday":
		return "周一"
	case "weekday:tuesday":
		return "周二"
	case "weekday:wednesday":
		return "周三"
	case "weekday:thursday":
		return "周四"
	case "weekday:friday":
		return "周五"
	case "weekday:saturday":
		return "周六"
	case "weekday:sunday":
		return "周日"
	default:
		return ""
	}
}

func knowledgeEvidenceTimeSlotLabel(slot string) string {
	switch slot {
	case "start":
		return "开始时间"
	case "end":
		return "结束时间"
	case "duration":
		return "时长"
	default:
		return "时间"
	}
}

func knowledgeEvidenceAspectFromMissingAspect(value string) string {
	compact := normalizeRuntimeKnowledgeQuery(value)
	switch {
	case containsAny(compact, []string{"数量", "几瓶", "几个", "几间", "几台", "几条", "几套", "几双", "几把", "几包", "几盒", "几袋", "几件", "几支", "几只", "几辆", "几杯", "几桶", "几卷", "瓶数", "个数"}):
		return "quantity"
	case containsAny(compact, []string{"费用", "收费", "免费", "价格", "金额", "多少钱"}):
		return "price"
	case containsAny(compact, []string{"时间", "几点", "多久", "什么时候"}):
		return "time"
	case containsAny(compact, []string{"位置", "地址", "地点", "楼层", "在哪里", "在哪"}):
		return "location"
	case containsAny(compact, []string{"办理方式", "操作方式", "领取方式", "使用方法", "怎么", "如何"}):
		return "method"
	case containsAny(compact, []string{"适用范围", "配送范围", "送达范围", "服务范围", "范围", "送到"}):
		return "scope"
	case containsAny(compact, []string{"适用条件", "使用条件", "条件", "限制"}):
		return "condition"
	case containsAny(compact, []string{"是否存在", "有没有", "是否有"}):
		return "existence"
	default:
		return ""
	}
}

func selectedKnowledgeEvidenceAnswersMatchSingleExistenceSubject(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) bool {
	if !requiredKnowledgeEvidenceAspect(requiredKnowledgeEvidenceAspects(task), "existence") {
		return true
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if subject, guarded := knowledgeEvidenceImplicitSingleExistenceSubject(task); guarded {
		requiredSubjects = []string{subject}
	}
	if len(requiredSubjects) != 1 {
		return true
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	for _, candidate := range allKnowledgeEvidenceJudgeTaskCandidates(task) {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if knowledgeEvidenceFAQAnswerSupportsSingleSubject(question, answer, requiredSubjects[0]) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFAQAnswerSupportsSingleSubject(question string, answer string, subject string) bool {
	question = strings.TrimSpace(question)
	answer = strings.TrimSpace(answer)
	subject = normalizeKnowledgeEvidenceSubjectForMatch(subject)
	if question == "" || answer == "" || subject == "" || isKnowledgeHandoffDirectiveContent(answer) {
		return false
	}
	candidateSubject := knowledgeEvidenceRoomTypePredicateSubject(question, knowledgeEvidenceConflictRoomTypes(question))
	if candidateSubject == "" {
		candidateSubject = knowledgeEvidenceSingleSubjectForAspects(question, []string{"existence"})
	}
	if candidateSubject != "" {
		if !knowledgeEvidenceExistenceCandidateSupportsTaskSubject(subject, candidateSubject, answer) {
			return false
		}
	} else if !knowledgeEvidenceQuestionDirectlyAsksExistenceOfSubject(question, subject) {
		return false
	}
	if strings.Contains(normalizeKnowledgeEvidenceSubjectForMatch(answer), subject) {
		return true
	}
	if knowledgeEvidenceFAQAnswerResolvesQuestionPolarity(answer) {
		return true
	}
	return knowledgeEvidenceFAQQuestionAsksForList(question) && knowledgeEvidenceFAQAnswerProvidesConcreteList(answer)
}

func knowledgeEvidenceFAQAnswerResolvesQuestionPolarity(answer string) bool {
	_, _, ok := knowledgeEvidenceFAQAnswerPolarity(answer)
	return ok
}

func knowledgeEvidenceFAQAnswerPolarity(answer string) (string, bool, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" || knowledgeEvidenceTextHasUncertaintyBoundary(answer) {
		return "", false, false
	}
	for _, item := range []struct {
		prefix   string
		negative bool
	}{
		{prefix: "没有的", negative: true}, {prefix: "不可以", negative: true}, {prefix: "不支持", negative: true},
		{prefix: "不需要", negative: true}, {prefix: "无需", negative: true}, {prefix: "不用", negative: true},
		{prefix: "不能", negative: true}, {prefix: "不是", negative: true}, {prefix: "没有", negative: true},
		{prefix: "是的"}, {prefix: "对的"}, {prefix: "没错"}, {prefix: "有的"}, {prefix: "可以"}, {prefix: "支持"},
	} {
		if !strings.HasPrefix(answer, item.prefix) {
			continue
		}
		remainder := strings.TrimSpace(strings.TrimPrefix(answer, item.prefix))
		if remainder == "" || strings.ContainsRune("，,。.!！；;：:", []rune(remainder)[0]) {
			return item.prefix, item.negative, true
		}
	}
	return "", false, false
}

func knowledgeEvidenceFAQAnswerIsPurePolarity(answer string, prefix string) bool {
	answer = strings.TrimSpace(answer)
	prefix = strings.TrimSpace(prefix)
	if answer == "" || prefix == "" || !strings.HasPrefix(answer, prefix) {
		return false
	}
	remainder := strings.TrimSpace(strings.TrimPrefix(answer, prefix))
	remainder = strings.TrimSpace(strings.Trim(remainder, "，,。.!！；;：:"))
	return remainder == ""
}

func knowledgeEvidenceFAQQuestionAsksForList(question string) bool {
	compact := normalizeRuntimeKnowledgeQuery(question)
	return containsAny(compact, []string{"哪些", "哪几", "有什么", "有些什么", "包括什么", "包含什么", "分别是什么", "分别有哪些"})
}

func knowledgeEvidenceFAQAnswerProvidesConcreteList(answer string) bool {
	if !knowledgeEvidenceAnswerClauseIsGroundedFact(answer) || knowledgeEvidenceTextHasUncertaintyBoundary(answer) {
		return false
	}
	if _, guidance := knowledgeEvidenceGuidanceRequirement(answer); guidance != "" {
		return false
	}
	return true
}

type knowledgeEvidenceSubjectAspectRequirement struct {
	Subject string
	Aspect  string
}

func requiredKnowledgeEvidenceSubjectAspectPairs(task knowledgeEvidenceJudgeTask) []knowledgeEvidenceSubjectAspectRequirement {
	requiredAspects := requiredKnowledgeEvidenceAspects(task)
	if len(requiredAspects) == 0 {
		return nil
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) < 2 {
		return nil
	}

	type subjectGroup struct {
		entityType string
		subjects   []string
	}
	groups := make([]subjectGroup, 0, 2)
	groupIndex := make(map[string]int, 2)
	for _, entity := range task.Entities {
		entityType := strings.ToLower(strings.TrimSpace(entity.Type))
		if entityType == "room_type" {
			continue
		}
		subject := normalizeKnowledgeEvidenceSubjectForMatch(normalizeKnowledgeEvidenceEntityText(entity))
		if subject == "" || !knowledgeEvidenceContainsString(requiredSubjects, subject) {
			continue
		}
		index, ok := groupIndex[entityType]
		if !ok {
			index = len(groups)
			groupIndex[entityType] = index
			groups = append(groups, subjectGroup{entityType: entityType})
		}
		groups[index].subjects = appendIfMissing(groups[index].subjects, subject)
	}

	ret := make([]knowledgeEvidenceSubjectAspectRequirement, 0, len(requiredSubjects)*len(requiredAspects))
	seen := make(map[string]struct{}, cap(ret))
	for _, group := range groups {
		if len(group.subjects) < 2 {
			continue
		}
		if len(requiredAspects) == 1 && !knowledgeEvidenceSubjectGroupExplicitlyRequestsAspect(task, group.subjects, requiredAspects[0]) {
			continue
		}
		pendingSubjects := make([]string, 0, len(group.subjects))
		lastSubjects := make([]string, 0, len(group.subjects))
		for _, clause := range splitKnowledgeEvidenceSubjectClauses(task.Query) {
			clauseText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
			clauseSubjects := knowledgeEvidenceContainedSubjects(clauseText, group.subjects)
			clauseTask := task
			clauseTask.Query = clause
			clauseTask.Objective = ""
			clauseTask.Intent = ""
			clauseAspects := intersectKnowledgeEvidenceStrings(requiredKnowledgeEvidenceAspects(clauseTask), requiredAspects)
			if len(clauseSubjects) > 0 && len(clauseAspects) == 0 {
				pendingSubjects = appendKnowledgeEvidenceStrings(pendingSubjects, clauseSubjects)
				continue
			}
			activeSubjects := clauseSubjects
			if len(clauseSubjects) > 0 {
				activeSubjects = appendKnowledgeEvidenceStrings(append([]string(nil), pendingSubjects...), clauseSubjects)
				pendingSubjects = nil
				lastSubjects = append([]string(nil), activeSubjects...)
			} else if len(clauseAspects) > 0 {
				activeSubjects = lastSubjects
				if len(activeSubjects) == 0 {
					activeSubjects = group.subjects
				}
			}
			for _, subject := range activeSubjects {
				for _, aspect := range clauseAspects {
					key := knowledgeEvidenceSubjectPairKey(subject, aspect)
					if _, exists := seen[key]; exists {
						continue
					}
					seen[key] = struct{}{}
					ret = append(ret, knowledgeEvidenceSubjectAspectRequirement{Subject: subject, Aspect: aspect})
				}
			}
		}
	}
	return ret
}

func knowledgeEvidenceSubjectGroupExplicitlyRequestsAspect(task knowledgeEvidenceJudgeTask, subjects []string, aspect string) bool {
	if len(subjects) < 2 || strings.TrimSpace(aspect) == "" {
		return false
	}
	if aspect == "price" && knowledgeEvidenceQueryAsksComparison(task.Query) {
		return false
	}
	query := normalizeKnowledgeEvidenceSubjectForMatch(task.Query)
	for _, subject := range subjects {
		if !strings.Contains(query, subject) {
			return false
		}
	}
	covered := make(map[string]struct{}, len(subjects))
	pendingSubjects := make([]string, 0, len(subjects))
	allTaskSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	for _, clause := range splitKnowledgeEvidenceSubjectClauses(task.Query) {
		clauseTask := task
		clauseTask.Query = clause
		clauseTask.Objective = ""
		clauseTask.Intent = ""
		clauseHasAspect := requiredKnowledgeEvidenceAspect(requiredKnowledgeEvidenceAspects(clauseTask), aspect)
		clauseText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
		clauseSubjects := knowledgeEvidenceContainedSubjects(clauseText, subjects)
		if len(clauseSubjects) > 0 && !clauseHasAspect {
			pendingSubjects = appendKnowledgeEvidenceStrings(pendingSubjects, clauseSubjects)
			continue
		}
		if !clauseHasAspect {
			continue
		}
		activeSubjects := clauseSubjects
		if len(clauseSubjects) > 0 {
			activeSubjects = appendKnowledgeEvidenceStrings(append([]string(nil), pendingSubjects...), clauseSubjects)
			pendingSubjects = nil
		} else if len(pendingSubjects) > 0 {
			if len(knowledgeEvidenceContainedSubjects(clauseText, allTaskSubjects)) > 0 {
				pendingSubjects = nil
				continue
			}
			activeSubjects = pendingSubjects
			pendingSubjects = nil
		}
		for _, subject := range activeSubjects {
			covered[subject] = struct{}{}
		}
	}
	return len(covered) == len(subjects)
}

func intersectKnowledgeEvidenceStrings(values []string, allowed []string) []string {
	ret := make([]string, 0, len(values))
	for _, value := range values {
		if knowledgeEvidenceContainsString(allowed, value) {
			ret = appendIfMissing(ret, value)
		}
	}
	return ret
}

func knowledgeEvidenceFactsCoverSubjectAspect(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact, subject string, aspect string) bool {
	subject = normalizeKnowledgeEvidenceSubjectForMatch(subject)
	for _, fact := range facts {
		if !knowledgeEvidenceFactSupportsAspect(fact, aspect) {
			continue
		}
		if aspect == "price" && !knowledgeEvidencePriceFactAnswersSubjectStatusTask(task, fact) {
			continue
		}
		text := normalizeKnowledgeEvidenceSubjectForMatch(fact.Statement + " " + strings.Join(fact.CriticalValues, " "))
		if strings.Contains(text, subject) {
			return true
		}
		if knowledgeEvidenceFAQAnswerCollectivelyCoversTaskSubjectsForAspect(task, fact.Statement, aspect) {
			return true
		}
	}
	return false
}

func knowledgeEvidencePriceFactAnswersSubjectStatusTask(task knowledgeEvidenceJudgeTask, fact knowledgeEvidenceFact) bool {
	claims := knowledgeEvidencePriceClaims(fact.Statement + " " + strings.Join(fact.CriticalValues, " "))
	if knowledgeEvidenceQueryAsksComparison(task.Query) {
		return knowledgeEvidencePriceClaimsContain(claims, "equal") ||
			knowledgeEvidencePriceClaimsContain(claims, "not_equal") ||
			knowledgeEvidencePriceClaimsContain(claims, "cheaper") ||
			knowledgeEvidencePriceClaimsContain(claims, "dearer") ||
			knowledgeEvidencePriceClaimsContain(claims, "amount")
	}
	if knowledgeEvidenceQueryAsksPriceAmount(task.Query) {
		return knowledgeEvidencePriceClaimsContain(claims, "amount") || knowledgeEvidencePriceClaimsContain(claims, "free")
	}
	return knowledgeEvidencePriceClaimsContain(claims, "free") ||
		knowledgeEvidencePriceClaimsContain(claims, "charged") ||
		knowledgeEvidencePriceClaimsContain(claims, "amount")
}

func knowledgeEvidenceConfigurationFieldLabel(field string) string {
	if field == "account" {
		return "WiFi账号"
	}
	if field == "password" {
		return "WiFi密码"
	}
	return field
}

func knowledgeEvidenceAspectLabel(aspect string) string {
	switch aspect {
	case "existence":
		return "是否存在"
	case "quantity":
		return "数量"
	case "price":
		return "费用"
	case "time":
		return "时间"
	case "location":
		return "位置"
	case "method":
		return "办理方式"
	case "scope":
		return "适用范围"
	case "condition":
		return "适用条件"
	case "phone":
		return "联系电话"
	default:
		return aspect
	}
}

func appendKnowledgeEvidenceMissingAspects(existing []string, values []string) []string {
	ret := append([]string(nil), existing...)
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !knowledgeEvidenceMissingAspectCovered(ret, value) {
			ret = append(ret, value)
		}
	}
	return ret
}

func knowledgeEvidenceMissingAspectCovered(existing []string, value string) bool {
	if knowledgeEvidenceContainsString(existing, value) {
		return true
	}
	markers := []string{value}
	switch value {
	case "适用范围":
		markers = []string{"范围", "送到"}
	case "数量":
		markers = []string{"数量", "几瓶", "几个"}
	case "费用":
		markers = []string{"费用", "收费", "免费", "价格"}
	}
	for _, item := range existing {
		compact := normalizeRuntimeKnowledgeQuery(item)
		if containsAny(compact, markers) {
			return true
		}
	}
	return false
}

func requiredKnowledgeEvidenceAspect(required []string, aspect string) bool {
	return knowledgeEvidenceContainsString(required, aspect)
}

func knowledgeEvidenceContainsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func knowledgeEvidenceQueryAsksComparison(query string) bool {
	compact := normalizeRuntimeKnowledgeQuery(query)
	compact = strings.NewReplacer(
		"不同平台", "平台",
		"不同房型", "房型",
		"不同用品", "用品",
		"不同设施", "设施",
		"不同渠道", "渠道",
	).Replace(compact)
	return containsAny(compact, []string{
		"价格一样", "费用一样", "收费一样", "金额一样", "价位一样", "价格是一样", "费用是一样", "收费是一样", "金额是一样", "价位是一样",
		"价格相同", "费用相同", "收费相同", "金额相同", "价位相同", "同价",
		"价格不一样", "费用不一样", "收费不一样", "金额不一样", "价位不一样", "价格是不一样", "费用是不一样", "收费是不一样", "金额是不一样", "价位是不一样",
		"价格不同", "费用不同", "收费不同", "金额不同", "价位不同",
		"是否不同", "有何不同", "有什么不同", "区别", "差别", "价差", "差多少", "对比", "比较",
		"哪个更", "哪家更", "哪个便宜", "哪家便宜", "哪个贵", "哪家贵", "哪个划算", "哪家划算",
	})
}

func knowledgeEvidenceQueryAsksDirectionalPriceComparison(query string) bool {
	compact := normalizeRuntimeKnowledgeQuery(query)
	return containsAny(compact, []string{
		"哪个更", "哪家更", "哪个便宜", "哪家便宜", "哪个贵", "哪家贵", "哪个划算", "哪家划算",
		"谁更便宜", "谁更贵", "价格更低", "费用更低", "收费更低", "价格更高", "费用更高", "收费更高",
	})
}

func knowledgeEvidenceQueryAsksAbsolutePriceStatus(query string) bool {
	compact := normalizeRuntimeKnowledgeQuery(query)
	return containsAny(compact, []string{
		"免费", "不免费", "收费", "不收费", "付费", "不付费", "要钱", "不要钱", "需不需要付费", "要不要收费",
	})
}

func knowledgeEvidenceQueryAsksPriceAmount(query string) bool {
	compact := normalizeRuntimeKnowledgeQuery(query)
	return containsAny(compact, []string{"多少钱", "多少元", "多少块", "具体价格", "具体费用", "金额", "价钱"})
}

func knowledgeEvidencePriceClaimCount(claims []knowledgeEvidencePriceClaim, kind string) int {
	count := 0
	for _, claim := range claims {
		if claim.Kind == kind {
			count++
		}
	}
	return count
}

func knowledgeEvidenceQueryAsksPriceBoundary(query string) bool {
	compact := normalizeRuntimeKnowledgeQuery(query)
	return containsAny(compact, []string{"优惠", "折扣", "优惠价", "老客户", "会员价"})
}

func knowledgeEvidenceFactIsMarketingFiller(statement string) bool {
	compact := normalizeRuntimeKnowledgeQuery(statement)
	if compact == "" {
		return true
	}
	for _, phrase := range []string{"帮您解放双手", "解放双手", "祝您入住愉快", "祝您旅途愉快", "宾至如归", "更舒适的体验", "更便捷的体验"} {
		if strings.Contains(compact, normalizeRuntimeKnowledgeQuery(phrase)) {
			return true
		}
	}
	return false
}

// The broad score-based repair below remains legacy-only. Runtime model
// decisions are authoritative and must not be reclassified locally.
func repairHighConfidenceInsufficientKnowledgeSelections(tasks []knowledgeEvidenceJudgeTask, selections map[string]map[string]knowledgeEvidenceLayerSelection) int {
	repaired := 0
	for _, task := range tasks {
		requiredEntities := normalizedKnowledgeEvidenceEntities(task.Entities)
		for _, layer := range []string{knowledgeEvidenceLayerStore, knowledgeEvidenceLayerGeneral} {
			selection, ok := selections[task.TaskID][layer]
			if !ok || (selection.Decision != knowledgeEvidenceDecisionInsufficient && selection.Decision != knowledgeEvidenceDecisionPartial) {
				continue
			}
			if layer == knowledgeEvidenceLayerStore {
				if repairedSelection, ok := deterministicKnowledgeEvidenceHandoffSelection(task, layer); ok {
					selections[task.TaskID][layer] = repairedSelection
					repaired++
					continue
				}
			}
			if repairedSelection, ok := deterministicKnowledgeEvidenceIntersectionSelection(task, layer, selection.SelectedCandidateIDs); ok {
				selections[task.TaskID][layer] = repairedSelection
				repaired++
				continue
			}
			if semanticGateNormalizeObjective(task.Objective) == "availability" && len(requiredEntities) >= 2 {
				if repairedSelection, ok := highConfidenceKnowledgeConsensusSelection(task, layer, requiredEntities); ok {
					selections[task.TaskID][layer] = repairedSelection
					repaired++
					continue
				}
			}
			if repairedSelection, ok := highConfidenceDirectFAQSelection(task, layer); ok {
				selections[task.TaskID][layer] = repairedSelection
				repaired++
			}
		}
	}
	return repaired
}

func repairExactFAQFallbackSelections(tasks []knowledgeEvidenceJudgeTask, selections map[string]map[string]knowledgeEvidenceLayerSelection) int {
	repaired := 0
	for _, task := range tasks {
		taskSelections := selections[task.TaskID]
		if taskSelections == nil {
			continue
		}
		for _, layer := range []string{knowledgeEvidenceLayerStore, knowledgeEvidenceLayerGeneral} {
			selection, ok := taskSelections[layer]
			if !ok || strings.TrimSpace(selection.DecisionSource) == "model" || !knowledgeEvidenceDecisionAllowsExactFAQFallback(selection.Decision) {
				continue
			}
			repairedSelection, ok := strictExactKnowledgeEvidenceFAQSelection(task, layer)
			if !ok {
				continue
			}
			taskSelections[layer] = repairedSelection
			repaired++
		}
	}
	return repaired
}

func repairStoreServiceSupplyInsufficientFAQSelections(tasks []knowledgeEvidenceJudgeTask, selections map[string]map[string]knowledgeEvidenceLayerSelection) int {
	repaired := 0
	for _, task := range tasks {
		if !knowledgeEvidenceTaskAllowsStoreSupplyFAQRescue(task) {
			continue
		}
		taskSelections := selections[task.TaskID]
		selection, ok := taskSelections[knowledgeEvidenceLayerStore]
		if !ok || selection.Decision != knowledgeEvidenceDecisionInsufficient {
			continue
		}
		repairedSelection, ok := highConfidenceDirectFAQSelectionAtMinimum(task, knowledgeEvidenceLayerStore, knowledgeEvidenceStoreSupplyRescueScore)
		if !ok {
			continue
		}
		if len(task.RawCandidates) > 0 {
			fullTask := task
			fullTask.Candidates = task.RawCandidates
			fullTask.RawCandidates = nil
			fullSelection, fullOK := highConfidenceDirectFAQSelectionAtMinimum(fullTask, knowledgeEvidenceLayerStore, knowledgeEvidenceStoreSupplyRescueScore)
			if !fullOK || len(repairedSelection.SelectedCandidateIDs) != 1 || len(fullSelection.SelectedCandidateIDs) != 1 ||
				repairedSelection.SelectedCandidateIDs[0] != fullSelection.SelectedCandidateIDs[0] {
				continue
			}
			repairedSelection = fullSelection
		}
		repairedSelection.DecisionSource = "store_service_faq_rescue"
		taskSelections[knowledgeEvidenceLayerStore] = repairedSelection
		repaired++
	}
	return repaired
}

func knowledgeEvidenceTaskAllowsStoreSupplyFAQRescue(task knowledgeEvidenceJudgeTask) bool {
	if !knowledgeEvidenceTaskHasSupplySubject(task) {
		return false
	}
	switch canonicalIntentCode(task.Intent) {
	case "service_request":
		return true
	case "hotel_info":
		return semanticGateNormalizeObjective(task.Objective) == "availability"
	default:
		return false
	}
}

func knowledgeEvidenceDecisionAllowsExactFAQFallback(decision string) bool {
	switch strings.TrimSpace(decision) {
	case knowledgeEvidenceDecisionInsufficient, knowledgeEvidenceDecisionProtocolInvalid, knowledgeEvidenceDecisionTimeout, knowledgeEvidenceDecisionMalformed:
		return true
	default:
		return false
	}
}

func strictExactKnowledgeEvidenceFAQSelection(task knowledgeEvidenceJudgeTask, layer string) (knowledgeEvidenceLayerSelection, bool) {
	type exactMatch struct {
		candidate knowledgeEvidenceJudgeCandidate
		question  string
		answer    string
	}
	exactQuery := strings.TrimSpace(task.RetrievalQuery)
	if exactQuery == "" {
		exactQuery = strings.TrimSpace(task.Query)
	}
	matches := make([]exactMatch, 0, 2)
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		question, answer, ok := exactKnowledgeEvidenceFAQMatch(candidate.Hit, exactQuery)
		if !ok || strings.TrimSpace(answer) == "" {
			continue
		}
		matches = append(matches, exactMatch{candidate: candidate, question: question, answer: answer})
	}
	if len(matches) == 0 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	answerKey := normalizeStrictKnowledgeEvidenceFAQAnswerText(matches[0].answer)
	for _, match := range matches[1:] {
		if normalizeStrictKnowledgeEvidenceFAQAnswerText(match.answer) != answerKey {
			return knowledgeEvidenceLayerSelection{}, false
		}
	}
	for _, candidate := range allKnowledgeEvidenceJudgeTaskCandidates(task) {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		_, answer, ok := exactKnowledgeEvidenceFAQMatch(candidate.Hit, exactQuery)
		if !ok || strings.TrimSpace(answer) == "" {
			continue
		}
		if normalizeStrictKnowledgeEvidenceFAQAnswerText(answer) != answerKey {
			return knowledgeEvidenceLayerSelection{}, false
		}
	}
	selected := matches[0]
	selectedCandidateIDs := []string{selected.candidate.CandidateID}
	if knowledgeEvidenceSelectedCandidatesHaveExplicitSubjectConflict(task, layer, selectedCandidateIDs) {
		return knowledgeEvidenceLayerSelection{}, false
	}
	if isKnowledgeHandoffDirectiveContent(selected.answer) {
		if knowledgeEvidenceLayerHasCompetingCompleteAnswer(task, layer, selectedCandidateIDs, selected.question, selected.answer) {
			return knowledgeEvidenceLayerSelection{}, false
		}
		return knowledgeEvidenceLayerSelection{
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			DecisionSource:       "deterministic_handoff",
			SelectedCandidateIDs: selectedCandidateIDs,
		}, true
	}
	if knowledgeEvidenceLayerHasConflictingCompleteAnswer(task, layer, selectedCandidateIDs, selected.question, selected.answer) {
		return knowledgeEvidenceLayerSelection{}, false
	}
	repaired, ok := repairModelSelectedKnowledgeEvidenceLayer(
		task,
		layer,
		knowledgeEvidenceDecisionDirectSingle,
		selectedCandidateIDs,
		nil,
		false,
	)
	if !ok {
		return knowledgeEvidenceLayerSelection{}, false
	}
	repaired.DecisionSource = "exact_faq_fallback"
	return repaired, true
}

func knowledgeEvidenceLayerHasCompetingCompleteAnswer(task knowledgeEvidenceJudgeTask, layer string, excludedCandidateIDs []string, selectedQuestion string, selectedAnswer string) bool {
	excluded := make(map[string]struct{}, len(excludedCandidateIDs))
	for _, candidateID := range excludedCandidateIDs {
		excluded[strings.TrimSpace(candidateID)] = struct{}{}
	}
	for _, candidate := range allKnowledgeEvidenceJudgeTaskCandidates(task) {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, skip := excluded[strings.TrimSpace(candidate.CandidateID)]; skip {
			continue
		}
		if knowledgeEvidenceJudgeReviewWorthyBodyPeer(task, candidate, selectedQuestion, selectedAnswer) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceLayerHasCompetingReviewBodyOutsideJudge(task knowledgeEvidenceJudgeTask, layer string, excludedCandidateIDs []string, selectedQuestion string, selectedAnswer string) bool {
	visible := make(map[string]struct{}, len(task.Candidates))
	visibleUnits := make(map[string]struct{}, len(task.Candidates))
	for _, candidate := range task.Candidates {
		visible[strings.TrimSpace(candidate.CandidateID)] = struct{}{}
		if unitKey := knowledgeEvidenceJudgeCandidateDedupKey(candidate.Hit); unitKey != "" {
			visibleUnits[unitKey] = struct{}{}
		}
	}
	excluded := make(map[string]struct{}, len(excludedCandidateIDs))
	for _, candidateID := range excludedCandidateIDs {
		excluded[strings.TrimSpace(candidateID)] = struct{}{}
	}
	for _, candidate := range allKnowledgeEvidenceJudgeTaskCandidates(task) {
		candidateID := strings.TrimSpace(candidate.CandidateID)
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, skip := excluded[candidateID]; skip {
			continue
		}
		if _, seenByJudge := visible[candidateID]; seenByJudge {
			continue
		}
		if unitKey := knowledgeEvidenceJudgeCandidateDedupKey(candidate.Hit); unitKey != "" {
			if _, equivalentSeenByJudge := visibleUnits[unitKey]; equivalentSeenByJudge {
				continue
			}
		}
		if knowledgeEvidenceJudgeReviewWorthyBodyPeer(task, candidate, selectedQuestion, selectedAnswer) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceJudgeReviewWorthyBodyPeer(task knowledgeEvidenceJudgeTask, candidate knowledgeEvidenceJudgeCandidate, selectedQuestion string, selectedAnswer string) bool {
	if candidate.Hit.Score < knowledgeEvidenceJudgeReviewMinimumScore {
		return false
	}
	question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
	if strings.TrimSpace(question) == "" || strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
		return false
	}
	if knowledgeEvidenceTextHasUncertaintyBoundary(answer) || !knowledgeEvidenceAnswerClauseIsGroundedFact(answer) {
		return false
	}
	return knowledgeEvidenceStrictExactConflictPeer(task, candidate, selectedQuestion, selectedAnswer, question, answer)
}

func knowledgeEvidenceLayerHasConflictingCompleteAnswer(task knowledgeEvidenceJudgeTask, layer string, excludedCandidateIDs []string, selectedQuestion string, selectedAnswer string) bool {
	excluded := make(map[string]struct{}, len(excludedCandidateIDs))
	for _, candidateID := range excludedCandidateIDs {
		excluded[strings.TrimSpace(candidateID)] = struct{}{}
	}
	for _, candidate := range allKnowledgeEvidenceJudgeTaskCandidates(task) {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, skip := excluded[strings.TrimSpace(candidate.CandidateID)]; skip {
			continue
		}
		if candidate.Hit.Score < knowledgeEvidenceJudgeReviewMinimumScore {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		if !knowledgeEvidenceStrictExactConflictPeer(task, candidate, selectedQuestion, selectedAnswer, question, answer) {
			continue
		}
		domain := knowledgeEvidenceConflictQuestionDomain(task.Query)
		if domain == "time" && candidate.Hit.Score >= knowledgeEvidenceJudgeReviewMinimumScore &&
			!knowledgeEvidenceTextHasUncertaintyBoundary(answer) && knowledgeEvidenceAnswerClauseIsGroundedFact(answer) {
			if conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(task.Query, selectedAnswer, question, answer); comparable {
				if conflict {
					return true
				}
				continue
			}
		}
		candidateTask := task
		candidateTask.Candidates = []knowledgeEvidenceJudgeCandidate{candidate}
		candidateTask.RawCandidates = []knowledgeEvidenceJudgeCandidate{candidate}
		if _, complete := repairModelSelectedKnowledgeEvidenceLayer(
			candidateTask,
			layer,
			knowledgeEvidenceDecisionDirectSingle,
			[]string{candidate.CandidateID},
			nil,
			false,
		); !complete {
			continue
		}
		if domain == "time" {
			if conflict, comparable := knowledgeEvidenceTimeSlotAnswersConflict(task.Query, selectedAnswer, question, answer); comparable && conflict {
				return true
			}
			continue
		}
		if domain == "identity" {
			if knowledgeEvidenceIdentityValuesConflict(selectedAnswer, answer) {
				return true
			}
			continue
		}
		if !knowledgeEvidenceFAQClaimsComparableForConflict(selectedQuestion, selectedAnswer, question, answer) {
			continue
		}
		if knowledgeEvidenceFAQClaimsConflict(selectedQuestion, selectedAnswer, question, answer) ||
			knowledgeEvidenceConfigurationValuesConflict(task.Query, selectedAnswer, answer) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceStrictExactConflictPeer(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
	selectedQuestion string,
	selectedAnswer string,
	question string,
	answer string,
) bool {
	if _, _, exact := exactKnowledgeEvidenceFAQMatch(candidate.Hit, task.Query); exact {
		return true
	}
	if knowledgeEvidenceCandidateHasExplicitTaskConflict(task, candidate, question, answer) {
		return false
	}
	if !knowledgeEvidenceFAQClaimsComparableForConflict(selectedQuestion, selectedAnswer, question, answer) {
		return false
	}
	taskSignature := knowledgeEvidenceConflictQuestionSignature(task.Query)
	candidateSignature := knowledgeEvidenceConflictQuestionSignature(question)
	if taskSignature != "" || candidateSignature != "" {
		return taskSignature != "" && candidateSignature != "" &&
			knowledgeEvidenceConflictQuestionSignaturesCompatible(taskSignature, candidateSignature)
	}
	if knowledgeEvidenceConfigurationTopic(task.Query) != "" {
		return knowledgeEvidenceConfigurationCandidateMatchesTask(task, candidate, question, answer)
	}
	taskMethodDomain := knowledgeEvidenceMethodDomain(task.Query)
	candidateMethodDomain := knowledgeEvidenceMethodDomain(question)
	if taskMethodDomain != "" || candidateMethodDomain != "" {
		return taskMethodDomain != "" && taskMethodDomain == candidateMethodDomain
	}
	taskTarget := knowledgeEvidenceServiceOperationTarget(task.Query)
	candidateTarget := knowledgeEvidenceServiceOperationTarget(strings.Join([]string{question, answer}, " "))
	if taskTarget != "" || candidateTarget != "" {
		if taskTarget == "" || candidateTarget == "" || !knowledgeEvidenceServiceOperationTargetsCompatible(taskTarget, candidateTarget) {
			return false
		}
		return knowledgeEvidenceTaskHasSupplySubject(task) ||
			knowledgeEvidenceFAQSharesTaskSubject(task.Query, strings.Join([]string{question, answer, candidate.Hit.Title}, " "))
	}
	taskShape := knowledgeEvidenceReviewQuestionShape(task.Query)
	candidateShape := knowledgeEvidenceReviewQuestionShape(question)
	if taskShape != "" || candidateShape != "" {
		if taskShape == "" || taskShape != candidateShape {
			return false
		}
		if taskShape == "existence" {
			return true
		}
	}
	return knowledgeEvidenceFAQSharesTaskSubject(task.Query, strings.Join([]string{question, answer, candidate.Hit.Title}, " "))
}

func knowledgeEvidenceReviewQuestionShape(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	switch {
	case containsAny(compact, []string{"哪些", "哪几", "有什么", "包括什么", "包含什么", "分别有哪些"}):
		return "list"
	case containsAny(compact, []string{"几瓶", "多少瓶", "几个", "多少个", "有多少", "数量"}):
		return "quantity"
	case containsAny(compact, []string{"免费", "收费", "多少钱", "价格", "费用"}):
		return "price"
	case containsAny(compact, []string{"几点", "什么时候", "何时", "多久", "时间"}):
		return "time"
	case containsAny(compact, []string{"在哪", "哪里", "地址", "位置", "楼层"}):
		return "location"
	case containsAny(compact, []string{"有没有", "是否有", "是不是有", "配备", "配有", "设有", "提供吗", "供应吗", "是否供应"}) ||
		(strings.Contains(compact, "有") && strings.Contains(compact, "吗")):
		return "existence"
	default:
		return ""
	}
}

func knowledgeEvidenceConflictObjectScope(text string) string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	switch {
	case containsAny(compact, []string{"会议室", "会议厅", "会场"}):
		return "meeting_room"
	case containsAny(compact, []string{"停车场", "车库", "停车位", "车位"}):
		return "parking"
	case containsAny(compact, []string{"老板", "董事长", "负责人"}):
		return "owner"
	case containsAny(compact, []string{"管家", "值班经理"}):
		return "steward"
	case containsAny(compact, []string{"大堂", "前台区域", "公共区域"}):
		return "lobby"
	case containsAny(compact, []string{"客房", "房间内", "房内", "房间", "房型"}):
		return "room"
	case containsAny(compact, []string{"酒店", "门店", "本店"}):
		return "hotel"
	default:
		return ""
	}
}

func knowledgeEvidenceConflictRoomTypes(text string) []string {
	matches := knowledgeEvidenceConflictRoomTypePattern.FindAllStringSubmatch(text, -1)
	ret := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		value := normalizeStrictKnowledgeEvidenceFAQText(match[1])
		for _, prefix := range []string{"问题", "答案", "酒店", "门店", "本店", "这个", "那个"} {
			value = strings.TrimPrefix(value, prefix)
		}
		if knowledgeEvidenceConflictRoomTypeIsGeneric(value) {
			continue
		}
		ret = appendIfMissing(ret, value)
	}
	return ret
}

func knowledgeEvidenceConflictRoomTypeIsGeneric(value string) bool {
	value = normalizeStrictKnowledgeEvidenceFAQText(value)
	return value == "" || strings.HasSuffix(value, "的") || knowledgeEvidenceTextHasExplicitQuestionForm(value) ||
		knowledgeEvidenceContainsString([]string{"所有", "全部", "部分", "不同", "每个", "每种", "房间", "客房", "房型"}, value)
}

func explicitKnowledgeEvidenceTaskRoomTypes(task knowledgeEvidenceJudgeTask) []string {
	query := normalizeRuntimeKnowledgeQuery(task.Query)
	ret := make([]string, 0, len(task.Entities))
	for _, entity := range task.Entities {
		if !strings.EqualFold(strings.TrimSpace(entity.Type), "room_type") {
			continue
		}
		value := normalizeKnowledgeEvidenceEntityText(entity)
		if value == "" || !strings.Contains(query, value) {
			continue
		}
		// Generic attributive or interrogative entities are room-type concepts,
		// not explicit room names. Semantic selection remains owned by Judge.
		if strings.HasSuffix(value, "的") || knowledgeEvidenceTextHasExplicitQuestionForm(value) {
			continue
		}
		ret = appendIfMissing(ret, value)
	}
	if len(ret) == 0 {
		ret = append(ret, knowledgeEvidenceConflictRoomTypes(task.Query)...)
	}
	return ret
}

func knowledgeEvidenceTextContainsAnyRoomType(text string, roomTypes []string) bool {
	compact := normalizeRuntimeKnowledgeQuery(text)
	for _, roomType := range roomTypes {
		roomType = normalizeRuntimeKnowledgeQuery(roomType)
		if roomType != "" && strings.Contains(compact, roomType) {
			return true
		}
	}
	return false
}

var knowledgeEvidenceStandaloneWeekdayPattern = regexp.MustCompile(`(?:周|星期|礼拜)([一二三四五六日天])`)

func knowledgeEvidenceConflictConditions(text string) []string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	ret := make([]string, 0, 3)
	workdayMarkers := []string{
		"工作日", "平日", "平时", "周一到周五", "周一至周五", "周一到五", "周一至五",
		"星期一到星期五", "星期一至星期五", "星期一到五", "星期一至五",
		"礼拜一到礼拜五", "礼拜一至礼拜五",
	}
	weekendMarkers := []string{
		"周末", "双休日", "双休", "周六日", "周六和周日", "周六与周日", "周六、周日", "周六到周日", "周六至周日",
		"星期六和星期日", "星期六与星期日", "星期六、星期日", "星期六到星期日", "星期六至星期日",
		"礼拜六和礼拜日", "礼拜六与礼拜日", "礼拜六到礼拜日", "礼拜六至礼拜日",
	}
	for _, item := range []struct {
		canonical string
		markers   []string
	}{
		{canonical: "workday", markers: workdayMarkers},
		{canonical: "weekend", markers: weekendMarkers},
		{canonical: "holiday", markers: []string{"节假日", "法定假日"}},
		{canonical: "night", markers: []string{"夜间", "晚上", "夜里"}},
		{canonical: "daytime", markers: []string{"白天", "日间"}},
		{canonical: "checkin_day", markers: []string{"入住当天"}},
		{canonical: "checkout_day", markers: []string{"退房当天"}},
	} {
		if containsAny(compact, item.markers) {
			ret = appendIfMissing(ret, item.canonical)
		}
	}
	standaloneText := compact
	for _, marker := range append(append([]string(nil), workdayMarkers...), weekendMarkers...) {
		standaloneText = strings.ReplaceAll(standaloneText, marker, "")
	}
	weekdayNames := map[string]string{
		"一": "monday", "二": "tuesday", "三": "wednesday", "四": "thursday",
		"五": "friday", "六": "saturday", "日": "sunday", "天": "sunday",
	}
	for _, match := range knowledgeEvidenceStandaloneWeekdayPattern.FindAllStringSubmatch(standaloneText, -1) {
		if len(match) != 2 {
			continue
		}
		if weekday := weekdayNames[match[1]]; weekday != "" {
			ret = appendIfMissing(ret, "weekday:"+weekday)
		}
	}
	return ret
}

func knowledgeEvidenceConflictQuestionSignature(text string) string {
	compact := normalizeStrictKnowledgeEvidenceFAQText(text)
	if compact == "" {
		return ""
	}
	if topic := knowledgeEvidenceSpatialRecommendationTopic(compact); topic != "" {
		return "spatial_recommendation:value|" + topic
	}
	domain := ""
	switch {
	case containsAny(compact, []string{"联系电话", "联系方式", "联系号码", "电话号码", "手机号", "电话"}):
		domain = "phone"
	case containsAny(compact, []string{"具体地址", "外卖地址", "地址", "位置在哪里", "位置在哪"}):
		domain = "address"
	case containsAny(compact, []string{"几瓶", "多少瓶", "几个", "多少个", "有多少", "数量"}):
		domain = "quantity"
	case containsAny(compact, []string{"几点", "什么时候", "时间", "多久", "多长时间", "时长", "多晚", "多早"}):
		domain = "time"
	case containsAny(compact, []string{"老板", "董事长", "创始人", "负责人"}) && containsAny(compact, []string{"是谁", "叫什么", "姓名", "名字", "哪位"}):
		domain = "identity"
	default:
		return ""
	}
	fieldRole := knowledgeEvidenceConflictQuestionFieldRole(compact, domain)
	if domain == "phone" {
		scope := knowledgeEvidenceConflictObjectScope(compact)
		if scope == "" {
			// In the hotel reply domain, an unqualified phone question means the
			// store contact. Explicit people such as the owner or steward retain
			// their own scope so one contact cannot be substituted for another.
			scope = "hotel"
		}
		return domain + ":" + fieldRole + "|" + scope
	}
	if domain == "address" && fieldRole == "delivery" {
		// "怎么填"、"填哪些"、"应该写什么" all ask for the same
		// delivery-address value. Their wording is not an object conflict; any
		// real conflict must come from the selected address facts themselves.
		return domain + ":" + fieldRole + "|"
	}
	subject := strings.NewReplacer(
		"你们酒店", "酒店", "咱们酒店", "酒店", "本酒店", "酒店", "门店", "酒店", "本店", "酒店",
		"会议室", "", "会议厅", "", "会场", "", "停车场", "", "车库", "", "停车位", "", "车位", "",
		"客房", "", "房间内", "", "房内", "", "房间", "", "房型", "", "大堂", "", "前台区域", "", "公共区域", "",
		"工作日", "", "平日", "", "周末", "", "节假日", "", "法定假日", "", "夜间", "", "晚上", "", "夜里", "", "白天", "", "日间", "",
		"联系电话", "", "联系方式", "", "联系号码", "", "电话号码", "", "手机号", "", "电话", "", "号码", "",
		"具体地址", "", "外卖地址", "", "收货地址", "", "配送地址", "", "跑腿地址", "", "地址", "", "位置在哪里", "", "位置在哪", "", "位置", "",
		"有几瓶", "", "几瓶", "", "多少瓶", "", "有多少个", "", "多少个", "", "有多少", "", "几个", "", "数量", "",
		"营业到多晚", "", "供应到多晚", "", "开放到多晚", "", "开到多晚", "", "到多晚", "", "多晚", "",
		"最早多早", "", "多早", "",
		"几点开始", "", "几点结束", "", "几点", "", "什么时候", "", "时间", "", "开始", "", "结束", "",
		"多久", "", "多长时间", "", "时长", "",
		"分别", "", "各自", "", "逐项", "",
		"老板", "", "董事长", "", "创始人", "", "负责人", "", "姓名", "", "名字", "", "哪位", "", "叫什么", "", "是谁", "",
		"在哪里", "", "在哪", "", "是什么", "", "是多少", "", "多少", "", "怎么填", "", "如何填", "", "填写", "",
		"有没有", "", "是否", "", "是不是", "", "有", "", "的", "", "吗", "", "呢", "", "是", "",
	).Replace(compact)
	subject = strings.TrimSpace(subject)
	if domain == "time" {
		subject = strings.Trim(subject, "从到和与及")
	}
	if subject == "酒店" {
		subject = ""
	}
	return domain + ":" + fieldRole + "|" + subject
}

func knowledgeEvidenceSpatialRecommendationTopic(text string) string {
	compact := normalizeKnowledgeEvidenceSubjectForMatch(text)
	if !containsAny(compact, []string{"附近", "周围"}) {
		return ""
	}
	switch {
	case containsAny(compact, []string{"好玩", "游玩", "玩的地方", "去哪玩", "去哪里玩", "景点", "景区", "娱乐", "休闲"}):
		return "attraction"
	case containsAny(compact, []string{"好吃", "吃的", "吃饭", "餐馆", "饭店", "餐饮", "小吃", "咖啡"}):
		return "dining"
	case containsAny(compact, []string{"商场", "购物", "超市", "便利店"}):
		return "shopping"
	case containsAny(compact, []string{"医院", "诊所", "药店", "看病", "买药"}):
		return "medical"
	default:
		return ""
	}
}

func knowledgeEvidenceConflictQuestionFieldRole(compact string, domain string) string {
	switch domain {
	case "time":
		switch {
		case containsAny(compact, []string{"几点开始", "什么时候开始", "开始时间", "最早几点", "几点开门", "开门时间", "多早"}):
			return "start"
		case containsAny(compact, []string{"几点结束", "什么时候结束", "结束时间", "截止时间", "最晚几点", "供应到几点", "营业到几点", "几点关门", "关门时间", "多晚"}):
			return "end"
		case containsAny(compact, []string{"多久", "多长时间", "时长"}):
			return "duration"
		case strings.Contains(compact, "入住"):
			return "start"
		case strings.Contains(compact, "退房"):
			return "end"
		default:
			return "schedule"
		}
	case "address":
		switch {
		case containsAny(compact, []string{"外卖地址", "收货地址", "配送地址", "跑腿地址"}) ||
			(containsAny(compact, []string{"地址"}) && containsAny(compact, []string{"怎么填", "如何填", "填写", "填什么", "填哪些"})):
			return "delivery"
		case containsAny(compact, []string{"停车场入口", "停车入口", "车库入口"}):
			return "parking_entrance"
		case containsAny(compact, []string{"酒店地址", "门店地址", "本店地址", "具体地址", "地址在哪里", "地址在哪", "位置在哪里", "位置在哪"}):
			return "physical"
		default:
			return "physical"
		}
	default:
		return "value"
	}
}

func knowledgeEvidenceConflictQuestionDomain(text string) string {
	signature := knowledgeEvidenceConflictQuestionSignature(text)
	domainRole, _, ok := strings.Cut(signature, "|")
	if !ok {
		return ""
	}
	domain, _, ok := strings.Cut(domainRole, ":")
	if !ok {
		return ""
	}
	return domain
}

func knowledgeEvidenceConflictQuestionSignaturesCompatible(left string, right string) bool {
	leftDomain, leftRole, leftSubject, leftOK := parseKnowledgeEvidenceConflictQuestionSignature(left)
	rightDomain, rightRole, rightSubject, rightOK := parseKnowledgeEvidenceConflictQuestionSignature(right)
	if !leftOK || !rightOK || leftDomain != rightDomain || leftSubject != rightSubject {
		return false
	}
	if leftDomain != "time" {
		return leftRole == rightRole
	}
	return knowledgeEvidenceTimeRolesOverlap(leftRole, rightRole)
}

func parseKnowledgeEvidenceConflictQuestionSignature(signature string) (string, string, string, bool) {
	domainRole, subject, ok := strings.Cut(strings.TrimSpace(signature), "|")
	if !ok {
		return "", "", "", false
	}
	domain, role, ok := strings.Cut(domainRole, ":")
	if !ok || domain == "" || role == "" {
		return "", "", "", false
	}
	return domain, role, subject, true
}

func knowledgeEvidenceTimeRolesOverlap(left string, right string) bool {
	if left == right {
		return true
	}
	if left == "schedule" {
		return right == "start" || right == "end"
	}
	if right == "schedule" {
		return left == "start" || left == "end"
	}
	return false
}

func knowledgeEvidenceTimeSlotAnswersConflict(leftQuestion string, leftAnswer string, rightQuestion string, rightAnswer string) (bool, bool) {
	leftRole := knowledgeEvidenceConflictQuestionFieldRole(normalizeStrictKnowledgeEvidenceFAQText(leftQuestion), "time")
	rightRole := knowledgeEvidenceConflictQuestionFieldRole(normalizeStrictKnowledgeEvidenceFAQText(rightQuestion), "time")
	leftValuesByCondition := knowledgeEvidenceTimeSlotValuesByConditionForQuestion(leftQuestion, leftRole, leftAnswer)
	rightValuesByCondition := knowledgeEvidenceTimeSlotValuesByConditionForQuestion(rightQuestion, rightRole, rightAnswer)
	compared := false
	for leftCondition, leftValues := range leftValuesByCondition {
		for rightCondition, rightValues := range rightValuesByCondition {
			if leftCondition != "" && rightCondition != "" && leftCondition != rightCondition {
				continue
			}
			for _, slot := range []string{"start", "end", "duration", "schedule"} {
				leftValue := strings.TrimSpace(leftValues[slot])
				rightValue := strings.TrimSpace(rightValues[slot])
				if leftValue == "" || rightValue == "" {
					continue
				}
				compared = true
				if normalizeRuntimeKnowledgeQuery(leftValue) != normalizeRuntimeKnowledgeQuery(rightValue) {
					return true, true
				}
			}
		}
	}
	return false, compared
}

func knowledgeEvidenceTimeSlotValuesByConditionForQuestion(question string, role string, answer string) map[string]map[string]string {
	_, _, subject, ok := parseKnowledgeEvidenceConflictQuestionSignature(knowledgeEvidenceConflictQuestionSignature(question))
	subject = normalizeKnowledgeEvidenceSubjectForMatch(subject)
	clauses := splitKnowledgeEvidenceTimeClauses(answer)
	if ok && subject != "" {
		clauses = knowledgeEvidenceTimeClausesForSubject(question, answer, subject)
	}
	if len(clauses) == 0 {
		return map[string]map[string]string{"": knowledgeEvidenceTimeSlotValues(role, answer)}
	}
	questionConditions := knowledgeEvidenceCalendarConditions(question)
	defaultCondition := ""
	if len(questionConditions) == 1 {
		defaultCondition = questionConditions[0]
	}
	grouped := make(map[string][]string, len(questionConditions)+1)
	activeConditions := []string(nil)
	for _, clause := range clauses {
		conditions := knowledgeEvidenceCalendarConditions(clause)
		if containsAny(normalizeRuntimeKnowledgeQuery(clause), []string{"每天", "每日", "天天"}) {
			conditions = []string{""}
		}
		if len(conditions) > 0 {
			activeConditions = append([]string(nil), conditions...)
		} else if len(activeConditions) > 0 {
			conditions = activeConditions
		} else {
			conditions = []string{defaultCondition}
		}
		for _, condition := range conditions {
			grouped[condition] = append(grouped[condition], clause)
		}
	}
	ret := make(map[string]map[string]string, len(grouped))
	for condition, conditionClauses := range grouped {
		ret[condition] = knowledgeEvidenceTimeSlotValues(role, strings.Join(conditionClauses, "，"))
	}
	return ret
}

func knowledgeEvidenceTimeSlotValuesForQuestion(question string, role string, answer string) map[string]string {
	_, _, subject, ok := parseKnowledgeEvidenceConflictQuestionSignature(knowledgeEvidenceConflictQuestionSignature(question))
	subject = normalizeKnowledgeEvidenceSubjectForMatch(subject)
	if !ok || subject == "" {
		return knowledgeEvidenceTimeSlotValues(role, answer)
	}
	selected := knowledgeEvidenceTimeClausesForSubject(question, answer, subject)
	if len(selected) == 0 {
		return map[string]string{}
	}
	return knowledgeEvidenceTimeSlotValues(role, strings.Join(selected, "，"))
}

func knowledgeEvidenceTimeClausesForSubject(question string, answer string, subject string) []string {
	clauses := splitKnowledgeEvidenceTimeClauses(answer)
	if len(clauses) == 0 {
		return nil
	}
	subject = normalizeKnowledgeEvidenceSubjectForMatch(subject)
	if subject == "" || !strings.Contains(normalizeKnowledgeEvidenceSubjectForMatch(question), subject) {
		return clauses
	}
	selected := make([]string, 0, len(clauses))
	activeSubject := subject
	for _, clause := range clauses {
		clauseSubjectText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
		switch {
		case strings.Contains(clauseSubjectText, subject):
			activeSubject = subject
			selected = append(selected, clause)
		case knowledgeEvidenceTimeFactHasExplicitSubject(clause):
			activeSubject = ""
		case activeSubject == subject:
			selected = append(selected, clause)
		}
	}
	return selected
}

func knowledgeEvidenceTimeSlotValues(role string, answer string) map[string]string {
	ret := make(map[string]string, 3)
	type clockMatch struct {
		start          int
		end            int
		raw            string
		value          string
		period         string
		explicitPeriod bool
		role           string
	}
	indexes := knowledgeEvidenceIndividualTimePattern.FindAllStringIndex(answer, -1)
	matches := make([]clockMatch, 0, len(indexes))
	for index, bounds := range indexes {
		raw := answer[bounds[0]:bounds[1]]
		period := knowledgeEvidenceClockPeriod(raw)
		explicitPeriod := period != ""
		if period == "" && index > 0 {
			connector := answer[indexes[index-1][1]:bounds[0]]
			if knowledgeEvidenceTimeRangeConnector(connector) && !containsAny(normalizeRuntimeKnowledgeQuery(connector), []string{"次日", "第二天", "翌日"}) {
				period = matches[index-1].period
			}
		}
		matchRole := knowledgeEvidenceTimeStatementRole(knowledgeEvidenceTimeClauseForMatch(answer, bounds[0], bounds[1]))
		matches = append(matches, clockMatch{
			start:          bounds[0],
			end:            bounds[1],
			raw:            raw,
			value:          normalizeKnowledgeEvidenceClockTimeWithPeriod(raw, period),
			period:         period,
			explicitPeriod: explicitPeriod,
			role:           matchRole,
		})
	}
	if len(matches) == 2 && knowledgeEvidenceTimeRangeConnector(answer[matches[0].end:matches[1].start]) {
		matches[1].value = normalizeKnowledgeEvidenceInheritedNightRangeEnd(
			matches[0].raw,
			matches[0].period,
			matches[1].raw,
			matches[1].period,
			matches[1].explicitPeriod,
			matches[1].value,
		)
	}
	rangeAssigned := false
	if len(matches) == 2 &&
		knowledgeEvidenceTimeRangeConnector(answer[matches[0].end:matches[1].start]) &&
		knowledgeEvidenceTimeStatementRole(knowledgeEvidenceTimeSuffixForMatch(answer, matches[1].end)) != "start" {
		ret["start"] = matches[0].value
		ret["end"] = matches[1].value
		rangeAssigned = true
	}
	if !rangeAssigned {
		explicitRole := false
		for _, match := range matches {
			if match.role == "" || match.value == "" {
				continue
			}
			explicitRole = true
			ret[match.role] = match.value
		}
		if !explicitRole && len(matches) == 2 && knowledgeEvidenceTimeRangeConnector(answer[matches[0].end:matches[1].start]) {
			ret["start"] = matches[0].value
			ret["end"] = matches[1].value
		} else if !explicitRole && len(matches) > 0 && (role == "start" || role == "end" || role == "schedule") {
			ret[role] = matches[0].value
		}
	}
	if duration := knowledgeEvidenceDurationValuePattern.FindString(answer); duration != "" {
		ret["duration"] = strings.TrimSpace(duration)
	}
	return ret
}

func normalizeKnowledgeEvidenceInheritedNightRangeEnd(startRaw string, startPeriod string, endRaw string, endPeriod string, endExplicitPeriod bool, currentValue string) string {
	if endExplicitPeriod || !containsAny(startPeriod, []string{"晚上", "夜里", "夜间"}) || endPeriod != startPeriod {
		return currentValue
	}
	startHour, startOK := knowledgeEvidenceClockRawHour(startRaw)
	endHour, endOK := knowledgeEvidenceClockRawHour(endRaw)
	if !startOK || !endOK || endHour == 12 || endHour < 1 || endHour > 11 || endHour >= startHour {
		return currentValue
	}
	return normalizeKnowledgeEvidenceClockTimeWithPeriod(endRaw, "")
}

func knowledgeEvidenceClockRawHour(value string) (int, bool) {
	raw := strings.Trim(strings.TrimSpace(value), "，,。；;！!？?")
	if period := knowledgeEvidenceClockPeriod(raw); period != "" {
		raw = strings.TrimPrefix(raw, period)
	}
	normalized := normalizeKnowledgeEvidenceClockTimeWithPeriod(raw, "")
	parts := strings.SplitN(normalized, ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(parts[0])
	return hour, err == nil
}

func knowledgeEvidenceTimeSuffixForMatch(answer string, end int) string {
	clauseEnd := len(answer)
	for _, separator := range []string{"，", ",", "；", ";", "。", "！", "!", "？", "?"} {
		if offset := strings.Index(answer[end:], separator); offset >= 0 && end+offset < clauseEnd {
			clauseEnd = end + offset
		}
	}
	return strings.TrimSpace(answer[end:clauseEnd])
}

func normalizeKnowledgeEvidenceClockTime(value string) string {
	return normalizeKnowledgeEvidenceClockTimeWithPeriod(value, "")
}

func normalizeKnowledgeEvidenceClockTimeWithPeriod(value string, inheritedPeriod string) string {
	raw := strings.Trim(strings.TrimSpace(value), "，,。；;！!？?")
	period := knowledgeEvidenceClockPeriod(raw)
	if period == "" {
		period = inheritedPeriod
	} else {
		raw = strings.TrimPrefix(raw, period)
	}
	if parts := strings.SplitN(raw, ":", 2); len(parts) == 2 {
		hour, hourErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		minute, minuteErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if hourErr == nil && minuteErr == nil && hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59 {
			hour = knowledgeEvidenceClockHourWithPeriod(hour, period)
			return fmt.Sprintf("%02d:%02d", hour, minute)
		}
		return normalizeRuntimeKnowledgeQuery(value)
	}
	compact := normalizeRuntimeKnowledgeQuery(raw)
	if compact == "" {
		return ""
	}

	pointIndex := strings.Index(compact, "点")
	if pointIndex <= 0 {
		return normalizeRuntimeKnowledgeQuery(value)
	}
	hour, ok := parseKnowledgeEvidenceClockNumber(compact[:pointIndex])
	if !ok || hour < 0 || hour > 23 {
		return normalizeRuntimeKnowledgeQuery(value)
	}
	minuteText := strings.TrimSpace(compact[pointIndex+len("点"):])
	minute := 0
	switch minuteText {
	case "", "整":
	case "半":
		minute = 30
	default:
		minuteText = strings.TrimSuffix(minuteText, "分")
		var minuteOK bool
		minute, minuteOK = parseKnowledgeEvidenceClockNumber(minuteText)
		if !minuteOK || minute < 0 || minute > 59 {
			return normalizeRuntimeKnowledgeQuery(value)
		}
	}
	hour = knowledgeEvidenceClockHourWithPeriod(hour, period)
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

func knowledgeEvidenceClockPeriod(value string) string {
	value = strings.TrimSpace(value)
	for _, marker := range []string{"凌晨", "早上", "上午", "中午", "下午", "傍晚", "晚上", "夜里", "夜间"} {
		if strings.HasPrefix(value, marker) {
			return marker
		}
	}
	return ""
}

func knowledgeEvidenceClockHourWithPeriod(hour int, period string) int {
	if containsAny(period, []string{"晚上", "夜里", "夜间"}) && hour == 12 {
		return 0
	}
	if containsAny(period, []string{"下午", "傍晚", "晚上", "夜里", "夜间"}) && hour < 12 {
		return hour + 12
	}
	if period == "凌晨" && hour == 12 {
		return 0
	}
	if period == "中午" && hour > 0 && hour < 11 {
		return hour + 12
	}
	return hour
}

func knowledgeEvidenceTimeRangeConnector(value string) bool {
	compact := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "").Replace(strings.TrimSpace(value))
	compact = strings.Trim(compact, "，,。；;：:")
	switch compact {
	case "-", "~", "～", "至", "到",
		"开始到", "开始至", "营业到", "营业至", "供应到", "供应至", "开放到", "开放至",
		"到次日", "至次日", "到第二天", "至第二天", "到翌日", "至翌日":
		return true
	default:
		return false
	}
}

func knowledgeEvidenceTimeStatementRole(value string) string {
	compact := normalizeRuntimeKnowledgeQuery(value)
	hasStart := containsAny(compact, []string{"开始", "开门", "入住", "起"})
	hasEnd := containsAny(compact, []string{"结束", "截止", "关门", "退房", "供应到", "营业到"})
	if hasStart == hasEnd {
		return ""
	}
	if hasStart {
		return "start"
	}
	return "end"
}

func knowledgeEvidenceTimeClauseForMatch(answer string, start int, end int) string {
	clauseStart := 0
	for _, separator := range []string{"，", ",", "；", ";", "。", "！", "!", "？", "?"} {
		if index := strings.LastIndex(answer[:start], separator); index >= 0 && index+len(separator) > clauseStart {
			clauseStart = index + len(separator)
		}
	}
	clauseEnd := len(answer)
	for _, separator := range []string{"，", ",", "；", ";", "。", "！", "!", "？", "?"} {
		if offset := strings.Index(answer[end:], separator); offset >= 0 && end+offset < clauseEnd {
			clauseEnd = end + offset
		}
	}
	return strings.TrimSpace(answer[clauseStart:clauseEnd])
}

func parseKnowledgeEvidenceClockNumber(value string) (int, bool) {
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed, true
	}
	if value == "零" || value == "〇" {
		return 0, true
	}
	return parseKnowledgeEvidenceEnumerationCount(value)
}

func knowledgeEvidenceIdentityValuesConflict(left string, right string) bool {
	leftValue := normalizeKnowledgeEvidenceIdentityValue(knowledgeEvidenceIdentityValue(left))
	rightValue := normalizeKnowledgeEvidenceIdentityValue(knowledgeEvidenceIdentityValue(right))
	return leftValue != "" && rightValue != "" && leftValue != rightValue
}

func normalizeKnowledgeEvidenceIdentityValue(value string) string {
	value = normalizeRuntimeKnowledgeQuery(value)
	for _, suffix := range []string{"先生", "女士", "老师"} {
		value = strings.TrimSuffix(value, suffix)
	}
	return value
}

func knowledgeEvidenceIdentityValue(answer string) string {
	trimmed := strings.Trim(strings.TrimSpace(answer), "，,。；;！!？?：:")
	clauses := splitKnowledgeEvidenceAnswerClauses(trimmed)
	if len(clauses) == 0 {
		clauses = []string{trimmed}
	}
	for _, clause := range clauses {
		for _, pattern := range []*regexp.Regexp{knowledgeEvidenceRoleFirstIdentityPattern, knowledgeEvidencePersonFirstIdentityPattern} {
			match := pattern.FindStringSubmatch(strings.TrimSpace(clause))
			if len(match) < 2 {
				continue
			}
			value := trimKnowledgeEvidenceIdentityCandidate(match[1])
			if knowledgeEvidenceIdentityValueUnavailable(value) || knowledgeEvidenceIdentityValueIsRoleOrContext(value) {
				continue
			}
			return value
		}
	}
	trimmed = trimKnowledgeEvidenceIdentityCandidate(trimmed)
	if knowledgeEvidenceIdentityValueUnavailable(trimmed) || !knowledgeEvidenceBareIdentityValuePattern.MatchString(trimmed) || knowledgeEvidenceIdentityValueIsRoleOrContext(trimmed) {
		return ""
	}
	return trimmed
}

func trimKnowledgeEvidenceIdentityCandidate(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "，,。；;！!？?：:")
	for {
		before := value
		for _, prefix := range []string{"本店由", "门店由", "目前", "现任", "现在", "由"} {
			if strings.HasPrefix(value, prefix) {
				value = strings.TrimPrefix(value, prefix)
				break
			}
		}
		if value == before {
			break
		}
	}
	for _, boundary := range []string{
		"同时担任", "并且担任", "并担任", "且担任", "主要负责", "联系方式", "联系电话",
		"负责", "主管", "管理", "分管", "兼任", "任职", "从事", "担任", "电话", "微信",
	} {
		index := strings.Index(value, boundary)
		if index <= 0 || len([]rune(value[:index])) < 2 {
			continue
		}
		value = value[:index]
		break
	}
	return strings.Trim(strings.TrimSpace(value), "，,。；;！!？?：:")
}

func knowledgeEvidenceIdentityValueIsRoleOrContext(value string) bool {
	return containsAny(normalizeRuntimeKnowledgeQuery(value), []string{
		"老板", "董事长", "创始人", "负责人", "经理", "管家", "酒店", "公司", "集团", "门店",
	})
}

func knowledgeEvidenceIdentityValueUnavailable(value string) bool {
	compact := normalizeRuntimeKnowledgeQuery(value)
	return knowledgeEvidenceTextHasUncertaintyBoundary(value) || containsAny(compact, []string{
		"暂无", "未公开", "不详", "保密", "没有资料", "无资料", "没有信息", "无相关信息",
		"不知道", "不清楚", "无法提供", "不能提供", "不便提供", "无法确认", "不能确认",
		"请联系前台", "联系前台", "请咨询前台", "咨询前台", "请联系门店", "联系门店", "请咨询门店", "咨询门店",
	})
}

func knowledgeEvidenceJudgeTaskContainsCandidate(candidates []knowledgeEvidenceJudgeCandidate, candidateID string) bool {
	candidateID = strings.TrimSpace(candidateID)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.CandidateID) == candidateID {
			return true
		}
	}
	return false
}

func allKnowledgeEvidenceJudgeTaskCandidates(task knowledgeEvidenceJudgeTask) []knowledgeEvidenceJudgeCandidate {
	if len(task.RawCandidates) > 0 {
		return task.RawCandidates
	}
	return task.Candidates
}

type exactKnowledgeEvidenceFAQUnit struct {
	Question string
	Answer   string
	Aliases  []string
}

var knowledgeEvidenceFAQAliasLinePattern = regexp.MustCompile(`(?im)^[ \t]*(?:相似问题|相似问法|训练问题|训练问法|扩展问题|扩展问法|召回问题|命中问题)[ \t]*[:：][ \t]*(.+)$`)
var knowledgeEvidenceConflictRoomTypePattern = regexp.MustCompile(`([\p{Han}A-Za-z0-9]{1,12})房型`)

func exactKnowledgeEvidenceFAQMatch(hit rag.RetrieveResult, query string) (string, string, bool) {
	queryKey := normalizeStrictKnowledgeEvidenceFAQText(query)
	if queryKey == "" {
		return "", "", false
	}
	units := exactKnowledgeEvidenceFAQUnits(hit)
	for _, unit := range units {
		if normalizeStrictKnowledgeEvidenceFAQText(unit.Question) == queryKey {
			return unit.Question, unit.Answer, true
		}
		for _, alias := range unit.Aliases {
			if normalizeStrictKnowledgeEvidenceFAQText(alias) == queryKey {
				return unit.Question, unit.Answer, true
			}
		}
	}
	return "", "", false
}

func exactKnowledgeEvidenceFAQUnits(hit rag.RetrieveResult) []exactKnowledgeEvidenceFAQUnit {
	raw := strings.TrimSpace(strings.ReplaceAll(hit.Content, "\r\n", "\n"))
	markers := knowledgeEvidenceFAQQuestionMarkerPattern.FindAllStringIndex(raw, -1)
	units := make([]exactKnowledgeEvidenceFAQUnit, 0, len(markers)+1)
	for index, marker := range markers {
		blockEnd := len(raw)
		if index+1 < len(markers) {
			blockEnd = markers[index+1][0]
		}
		block := raw[marker[0]:blockEnd]
		parsed := parseKnowledgeEvidenceFAQUnits(block)
		if len(parsed) == 0 {
			continue
		}
		units = append(units, exactKnowledgeEvidenceFAQUnit{
			Question: parsed[0].Question,
			Answer:   parsed[0].Answer,
			Aliases:  exactKnowledgeEvidenceFAQAliases(block),
		})
	}
	if len(units) > 0 {
		return units
	}
	question := normalizeKnowledgeEvidenceFAQQuestion(hit.FaqQuestion)
	answer := trimKnowledgeEvidenceFAQMetadata(raw)
	if question == "" || answer == "" {
		return nil
	}
	return []exactKnowledgeEvidenceFAQUnit{{
		Question: question,
		Answer:   answer,
		Aliases:  exactKnowledgeEvidenceFAQAliases(raw),
	}}
}

func exactKnowledgeEvidenceFAQAliases(raw string) []string {
	matches := knowledgeEvidenceFAQAliasLinePattern.FindAllStringSubmatch(raw, -1)
	aliases := make([]string, 0, len(matches)*2)
	seen := make(map[string]struct{})
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		for _, alias := range strings.FieldsFunc(match[1], func(r rune) bool {
			switch r {
			case '、', ',', '，', ';', '；', '|':
				return true
			default:
				return false
			}
		}) {
			alias = strings.TrimSpace(alias)
			key := normalizeStrictKnowledgeEvidenceFAQText(alias)
			if alias == "" || key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

func normalizeStrictKnowledgeEvidenceFAQText(text string) string {
	return normalizeStrictKnowledgeEvidenceFAQTextWithTerminalParticles(text, true)
}

func normalizeStrictKnowledgeEvidenceFAQTextWithTerminalParticles(text string, stripQuestionParticles bool) string {
	compact := strings.ToLower(strings.TrimSpace(text))
	compact = strings.NewReplacer(
		" ", "", "\t", "", "\r", "", "\n", "",
		"，", "", ",", "", "。", "", ".", "", "？", "", "?", "",
		"！", "", "!", "", "：", "", ":", "", "；", "", ";", "",
		"“", "", "”", "", "‘", "", "’", "", "\"", "", "'", "",
	).Replace(compact)
	for {
		before := compact
		for _, prefix := range []string{"您好", "你好", "请问一下", "请问", "麻烦问一下", "麻烦问下", "麻烦", "我想问一下", "我想问下", "想问一下", "想问下", "问一下", "问下"} {
			compact = strings.TrimPrefix(compact, prefix)
		}
		if compact == before {
			break
		}
	}
	for _, suffix := range []string{"谢谢啦", "谢谢你", "谢谢"} {
		compact = strings.TrimSuffix(compact, suffix)
	}
	if stripQuestionParticles && knowledgeEvidenceTextHasExplicitQuestionForm(compact) {
		compact = strings.TrimRight(compact, "呀啊呢啦哈")
	}
	return strings.TrimSpace(compact)
}

func knowledgeEvidenceTextHasExplicitQuestionForm(text string) bool {
	return containsAny(text, []string{
		"吗", "嘛", "么", "什么", "啥", "谁", "哪", "几", "多少", "怎么", "如何",
		"是否", "有没有", "能不能", "可不可以", "为什么", "为何", "何时", "几点",
		"多久", "多远", "在哪", "哪里", "哪儿", "是不是", "对不对", "行不行",
		"可以不", "会不会", "要不要", "用不用",
	})
}

func normalizeStrictKnowledgeEvidenceFAQAnswerText(answer string) string {
	if isKnowledgeHandoffDirectiveContent(answer) {
		return "__handoff__"
	}
	return normalizeStrictKnowledgeEvidenceFAQTextWithTerminalParticles(answer, false)
}

func deterministicKnowledgeEvidenceJudgeFallbackSelections(tasks []knowledgeEvidenceJudgeTask) (map[string]map[string]knowledgeEvidenceLayerSelection, int, int) {
	selections := make(map[string]map[string]knowledgeEvidenceLayerSelection, len(tasks))
	handoffs := 0
	for _, task := range tasks {
		layers := make(map[string]map[string]struct{}, 2)
		for _, candidate := range task.Candidates {
			layer := strings.TrimSpace(candidate.Layer)
			if layer == knowledgeEvidenceLayerStore || layer == knowledgeEvidenceLayerGeneral {
				if layers[layer] == nil {
					layers[layer] = make(map[string]struct{})
				}
				layers[layer][strings.TrimSpace(candidate.CandidateID)] = struct{}{}
			}
		}
		taskSelections := failedKnowledgeEvidenceLayerSelectionsForExpected(layers, knowledgeEvidenceDecisionInsufficient)
		for layer := range layers {
			if selection, ok := deterministicKnowledgeEvidenceHandoffSelection(task, layer); ok {
				taskSelections[layer] = selection
				handoffs++
			}
		}
		selections[task.TaskID] = taskSelections
	}
	repairHighConfidenceInsufficientKnowledgeSelections(tasks, selections)
	groundedAnswers := 0
	for _, task := range tasks {
		taskSelections := selections[task.TaskID]
		for _, selection := range taskSelections {
			if selectionHasCompleteEvidence(selection) && len(selection.SupportedFacts) > 0 {
				groundedAnswers++
			}
		}
	}
	return selections, groundedAnswers, handoffs
}

func knowledgeEvidenceTaskHasLayerCandidates(task knowledgeEvidenceJudgeTask, layer string) bool {
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) == strings.TrimSpace(layer) {
			return true
		}
	}
	return false
}

func deterministicKnowledgeEvidenceHandoffSelection(task knowledgeEvidenceJudgeTask, layer string) (knowledgeEvidenceLayerSelection, bool) {
	selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, layer)
	if !ok || len(selection.SelectedCandidateIDs) != 1 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	selectedID := selection.SelectedCandidateIDs[0]
	for _, candidate := range task.Candidates {
		if candidate.CandidateID != selectedID || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		_, answer, matched := exactKnowledgeEvidenceFAQMatch(candidate.Hit, task.Query)
		if matched && isKnowledgeHandoffDirectiveContent(answer) {
			selection.DecisionSource = "deterministic_handoff"
			return selection, true
		}
	}
	return knowledgeEvidenceLayerSelection{}, false
}

func highConfidenceDirectFAQSelection(task knowledgeEvidenceJudgeTask, layer string) (knowledgeEvidenceLayerSelection, bool) {
	return highConfidenceDirectFAQSelectionAtMinimum(task, layer, knowledgeEvidenceDirectFAQMinimumScore)
}

func highConfidenceDirectFAQSelectionAtMinimum(task knowledgeEvidenceJudgeTask, layer string, minimumScore float32) (knowledgeEvidenceLayerSelection, bool) {
	if knowledgeEvidenceConfigurationLayerHasAmbiguousScope(task, layer) {
		return knowledgeEvidenceLayerSelection{}, false
	}
	type matchedFAQ struct {
		candidate knowledgeEvidenceJudgeCandidate
		question  string
		answer    string
		match     float64
	}
	matches := make([]matchedFAQ, 0, 2)
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		question, answer, questionMatch, _, ok := knowledgeEvidenceDirectFAQCandidateEligibilityAtMinimum(task, candidate, minimumScore)
		if !ok {
			continue
		}
		matches = append(matches, matchedFAQ{candidate: candidate, question: question, answer: answer, match: questionMatch})
	}
	if len(matches) == 0 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	best := matches[0]
	for _, match := range matches[1:] {
		if match.match > best.match+0.02 || (match.match >= best.match-0.02 && match.candidate.Hit.Score > best.candidate.Hit.Score) {
			best = match
		}
	}
	if knowledgeEvidenceDirectFAQHasConflict(task, layer, best.candidate.CandidateID, best.question, best.answer, best.match, minimumScore) {
		return knowledgeEvidenceLayerSelection{}, false
	}
	facts := deterministicKnowledgeEvidenceFactsFromFAQ(task.TaskID, best.answer)
	facts = enrichKnowledgeEvidenceFactsFromFAQUnit(task, best.question, best.answer, facts)
	facts = groundedKnowledgeEvidenceFacts(task, layer, []string{best.candidate.CandidateID}, facts)
	facts = finalizeKnowledgeEvidenceFactsForTask(task, facts)
	if len(facts) == 0 || len(strictMechanicalMissingKnowledgeEvidenceAspects(task, facts)) > 0 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	return knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		DecisionSource:       deterministicKnowledgeEvidenceFAQDecisionSource(layer),
		SelectedCandidateIDs: []string{best.candidate.CandidateID},
		SupportedFacts:       facts,
	}, true
}

func knowledgeEvidenceJudgeCandidateCompletesTask(task knowledgeEvidenceJudgeTask, candidate knowledgeEvidenceJudgeCandidate) (float64, bool) {
	_, answer, questionMatch, facts, ok := knowledgeEvidenceDirectFAQCandidateEligibility(task, candidate)
	if !ok {
		_, exactAnswer, exact := exactKnowledgeEvidenceFAQMatch(candidate.Hit, task.Query)
		if !exact || strings.TrimSpace(exactAnswer) == "" || isKnowledgeHandoffDirectiveContent(exactAnswer) {
			return questionMatch, false
		}
		repaired, repairedOK := repairModelSelectedKnowledgeEvidenceLayer(
			task,
			candidate.Layer,
			knowledgeEvidenceDecisionDirectSingle,
			[]string{candidate.CandidateID},
			nil,
			false,
		)
		if !repairedOK || len(repaired.MissingAspects) > 0 || len(repaired.SupportedFacts) == 0 {
			return questionMatch, false
		}
		return 1, true
	}
	if len(strictMechanicalMissingKnowledgeEvidenceAspects(task, facts)) > 0 {
		return questionMatch, false
	}
	if len(facts) > 0 {
		return questionMatch, true
	}
	// An exact FAQ can still be worth preserving for the model when its answer
	// is a grounded yes/no statement that does not map to one of the fixed fact
	// aspects. This only affects candidate retention; it never bypasses Judge.
	return questionMatch, questionMatch >= 0.94 && knowledgeEvidenceAnswerClauseIsGroundedFact(answer)
}

func knowledgeEvidenceJudgeReviewCandidateCompletesTask(task knowledgeEvidenceJudgeTask, candidate knowledgeEvidenceJudgeCandidate) (float64, bool) {
	if candidate.Hit.Score < knowledgeEvidenceJudgeReviewMinimumScore {
		return 0, false
	}
	question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
	questionMatch, matched := knowledgeEvidenceFAQDirectMatchScore(question, answer, task.Query)
	configurationTask := knowledgeEvidenceConfigurationTopic(task.Query) != ""
	configurationMatch := configurationTask && knowledgeEvidenceConfigurationCandidateMatchesTask(task, candidate, question, answer)
	storeSemanticMatch := knowledgeEvidenceStoreServiceSemanticFAQMatchesAtMinimum(
		task,
		candidate,
		question,
		answer,
		knowledgeEvidenceJudgeReviewMinimumScore,
	)
	if question == "" || answer == "" || isKnowledgeHandoffDirectiveContent(answer) ||
		knowledgeEvidenceTextHasUncertaintyBoundary(answer) ||
		(!matched && !configurationMatch && !storeSemanticMatch) ||
		!knowledgeEvidenceCandidateMatchesTaskSubjects(task, candidate, question, answer) ||
		len(knowledgeEvidenceSelectedCandidateExplicitSubjectGaps(task, candidate.Layer, []string{candidate.CandidateID})) > 0 ||
		!selectedKnowledgeEvidenceAnswersMatchSingleExistenceSubject(task, candidate.Layer, []string{candidate.CandidateID}) {
		return questionMatch, false
	}
	repaired, ok := repairModelSelectedKnowledgeEvidenceLayer(
		task,
		candidate.Layer,
		knowledgeEvidenceDecisionDirectSingle,
		[]string{candidate.CandidateID},
		nil,
		false,
	)
	return questionMatch, ok && len(repaired.MissingAspects) == 0 && len(repaired.SupportedFacts) > 0
}

func knowledgeEvidenceDirectFAQCandidateEligibility(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
) (string, string, float64, []knowledgeEvidenceFact, bool) {
	return knowledgeEvidenceDirectFAQCandidateEligibilityAtMinimum(task, candidate, knowledgeEvidenceDirectFAQMinimumScore)
}

func knowledgeEvidenceDirectFAQCandidateEligibilityAtMinimum(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
	minimumScore float32,
) (string, string, float64, []knowledgeEvidenceFact, bool) {
	const (
		minimumRescueScore         = float32(0.65)
		minimumRescueQuestionMatch = 0.94
	)
	question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
	questionMatch, matched := knowledgeEvidenceFAQDirectMatchScore(question, answer, task.Query)
	configurationTask := knowledgeEvidenceConfigurationTopic(task.Query) != ""
	strictConfigurationMatch := configurationTask && knowledgeEvidenceStrictConfigurationCandidateMatches(task, candidate, question, answer)
	questionRescueMinimumScore := minimumRescueScore
	if minimumScore < knowledgeEvidenceDirectFAQMinimumScore {
		questionRescueMinimumScore = minimumScore
	}
	rescuedByQuestion := !configurationTask && candidate.Hit.Score >= questionRescueMinimumScore && questionMatch >= minimumRescueQuestionMatch
	storeSemanticMatch := knowledgeEvidenceStoreServiceSemanticFAQMatchesAtMinimum(task, candidate, question, answer, minimumScore)
	if (candidate.Hit.Score < minimumScore && !rescuedByQuestion) ||
		question == "" || answer == "" || isKnowledgeHandoffDirectiveContent(answer) ||
		(!matched && !strictConfigurationMatch && !storeSemanticMatch) {
		return question, answer, questionMatch, nil, false
	}
	if configurationTask &&
		(!knowledgeEvidenceConfigurationAnswerCoversQuery(task.Query, question, answer) ||
			!knowledgeEvidenceConfigurationScopeMatches(task.Query, strings.Join([]string{question, answer, candidate.Hit.Title}, " "))) {
		return question, answer, questionMatch, nil, false
	}
	if minimumScore < knowledgeEvidenceDirectFAQMinimumScore && knowledgeEvidenceCandidateHasExplicitTaskConflict(task, candidate, question, answer) {
		return question, answer, questionMatch, nil, false
	}
	if !knowledgeEvidenceCandidateMatchesTaskSubjects(task, candidate, question, answer) {
		return question, answer, questionMatch, nil, false
	}
	if len(knowledgeEvidenceSelectedCandidateExplicitSubjectGaps(task, candidate.Layer, []string{candidate.CandidateID})) > 0 {
		return question, answer, questionMatch, nil, false
	}
	if !selectedKnowledgeEvidenceAnswersMatchSingleExistenceSubject(task, candidate.Layer, []string{candidate.CandidateID}) {
		return question, answer, questionMatch, nil, false
	}
	facts := deterministicKnowledgeEvidenceFactsFromFAQ(task.TaskID, answer)
	facts = enrichKnowledgeEvidenceFactsFromFAQUnit(task, question, answer, facts)
	facts = groundedKnowledgeEvidenceFacts(task, candidate.Layer, []string{candidate.CandidateID}, facts)
	facts = finalizeKnowledgeEvidenceFactsForTask(task, facts)
	return question, answer, questionMatch, facts, true
}

func knowledgeEvidenceStoreServiceSemanticFAQMatches(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
	question string,
	answer string,
) bool {
	return knowledgeEvidenceStoreServiceSemanticFAQMatchesAtMinimum(
		task,
		candidate,
		question,
		answer,
		knowledgeEvidenceDirectFAQMinimumScore,
	)
}

func knowledgeEvidenceStoreServiceSemanticFAQMatchesAtMinimum(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
	question string,
	answer string,
	minimumScore float32,
) bool {
	if strings.TrimSpace(candidate.Layer) != knowledgeEvidenceLayerStore ||
		!knowledgeEvidenceTaskAllowsStoreServiceSemanticFAQ(task) ||
		candidate.Hit.Score < minimumScore {
		return false
	}
	requiredEntities := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredEntities) == 0 || !knowledgeEvidenceCandidateMatchesTaskSubjects(task, candidate, question, answer) {
		return false
	}
	candidateText := strings.Join([]string{question, answer, candidate.Hit.Title}, " ")
	if !knowledgeEvidenceFAQSharesTaskSubject(task.Query, candidateText) {
		return false
	}
	if semanticGateNormalizeObjective(task.Objective) == "availability" {
		return len(requiredEntities) == 1 && knowledgeEvidenceFAQAnswerSupportsSingleSubject(question, answer, requiredEntities[0])
	}
	return knowledgeEvidenceServiceOperationTargetsCompatible(
		knowledgeEvidenceServiceOperationTarget(task.Query),
		knowledgeEvidenceServiceOperationTarget(candidateText),
	)
}

func knowledgeEvidenceTaskAllowsStoreServiceSemanticFAQ(task knowledgeEvidenceJudgeTask) bool {
	if canonicalIntentCode(task.Intent) == "service_request" {
		return true
	}
	if canonicalIntentCode(task.Intent) != "hotel_info" || isSpatialFactSubIntent(task.SubIntent) {
		return false
	}
	switch semanticGateNormalizeObjective(task.Objective) {
	case "availability", "location", "method", "action_request":
		return knowledgeEvidenceTaskHasSupplySubject(task)
	default:
		return false
	}
}

func knowledgeEvidenceTaskHasSupplySubject(task knowledgeEvidenceJudgeTask) bool {
	for _, entity := range task.Entities {
		if strings.EqualFold(strings.TrimSpace(entity.Type), "supply") {
			return true
		}
	}
	subIntent := strings.ToLower(strings.TrimSpace(task.SubIntent))
	return subIntent == "supplies_self_help" || strings.HasPrefix(subIntent, "supplies_") || strings.HasPrefix(subIntent, "supply_")
}

func knowledgeEvidenceDirectFAQHasConflict(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateID string, selectedQuestion string, selectedAnswer string, selectedQuestionMatch float64, selectionMinimumScore float32) bool {
	configurationTopic := knowledgeEvidenceConfigurationTopic(task.Query)
	selectedConfigurationScope := knowledgeEvidenceConfigurationScope(selectedQuestion + " " + selectedAnswer)
	conflictMinimumScore := knowledgeEvidenceJudgeReviewMinimumScore
	if selectionMinimumScore < conflictMinimumScore {
		conflictMinimumScore = selectionMinimumScore
	}
	for _, candidate := range task.Candidates {
		if candidate.CandidateID == selectedCandidateID || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		questionMatch := knowledgeEvidenceFAQQuestionMatchScore(question, task.Query)
		if configurationTopic != "" && knowledgeEvidenceConfigurationCandidateMatchesTask(task, candidate, question, answer) {
			candidateScope := knowledgeEvidenceConfigurationScope(strings.Join([]string{question, answer, candidate.Hit.Title}, " "))
			if knowledgeEvidenceConfigurationScope(task.Query) == "" && candidateScope != selectedConfigurationScope {
				return true
			}
			if knowledgeEvidenceConfigurationValuesConflict(task.Query, selectedAnswer, answer) {
				return true
			}
		}
		if candidate.Hit.Score < conflictMinimumScore {
			continue
		}
		semanticPeer := knowledgeEvidenceStoreServiceSemanticFAQMatchesAtMinimum(task, candidate, question, answer, conflictMinimumScore)
		sameFAQQuestionPeer := knowledgeEvidenceFAQQuestionMatchScore(question, selectedQuestion) >= 0.82 &&
			knowledgeEvidenceCandidateMatchesTaskSubjects(task, candidate, question, answer)
		existenceClaimPair := knowledgeEvidenceFAQPairUsesExistenceSubject(selectedQuestion, selectedAnswer, question, answer)
		existencePeer := existenceClaimPair && knowledgeEvidenceFAQClaimsComparableForConflict(selectedQuestion, selectedAnswer, question, answer)
		if !semanticPeer && !sameFAQQuestionPeer && !existencePeer && (questionMatch < 0.78 || questionMatch+0.08 < selectedQuestionMatch) {
			continue
		}
		if existenceClaimPair && !existencePeer {
			continue
		}
		if isKnowledgeHandoffDirectiveContent(answer) || knowledgeEvidenceFAQClaimsConflict(selectedQuestion, selectedAnswer, question, answer) {
			return true
		}
	}
	return false
}

func deterministicKnowledgeEvidenceFAQDecisionSource(layer string) string {
	if strings.TrimSpace(layer) == knowledgeEvidenceLayerStore {
		return "store_exact_faq_rescue"
	}
	return "deterministic_faq_rescue"
}

func knowledgeEvidenceFAQSharesTaskSubject(query string, candidateText string) bool {
	query = normalizeKnowledgeEvidenceSubjectForMatch(query)
	candidateText = normalizeKnowledgeEvidenceSubjectForMatch(candidateText)
	if query == "" || candidateText == "" {
		return false
	}
	queryRunes := []rune(query)
	maxWidth := 4
	if len(queryRunes) < maxWidth {
		maxWidth = len(queryRunes)
	}
	for width := maxWidth; width >= 2; width-- {
		for start := 0; start+width <= len(queryRunes); start++ {
			token := string(queryRunes[start : start+width])
			if knowledgeEvidenceGenericFAQSubjectToken(token) || !strings.Contains(candidateText, token) {
				continue
			}
			return true
		}
	}
	return false
}

func knowledgeEvidenceGenericFAQSubjectToken(token string) bool {
	token = normalizeKnowledgeEvidenceSubjectForMatch(token)
	if token == "" {
		return true
	}
	return containsAny(token, []string{
		"酒店", "门店", "房间", "客房", "问题", "服务", "客户", "这个", "那个", "一下", "请问", "帮我", "我要", "我想",
		"有没有", "能不能", "可不可以", "是不是", "是否", "怎么", "如何", "哪里", "在哪", "什么", "多少", "几个", "几点",
		"没了", "没有", "不够", "坏了", "需要", "可以", "怎么办", "咋办", "的吗", "了吗", "一下",
	})
}

func knowledgeEvidenceConfigurationValuesConflict(query string, left string, right string) bool {
	leftValues := knowledgeEvidenceConfigurationValues(left)
	rightValues := knowledgeEvidenceConfigurationValues(right)
	for _, field := range knowledgeEvidenceConfigurationFields(query) {
		if len(leftValues[field]) == 0 || len(rightValues[field]) == 0 {
			continue
		}
		leftValue := normalizeRuntimeKnowledgeQuery(strings.Join(leftValues[field], "|"))
		rightValue := normalizeRuntimeKnowledgeQuery(strings.Join(rightValues[field], "|"))
		if leftValue != rightValue {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFAQAnswersConflict(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if knowledgeEvidenceTextHasNegativeBoundary(left) != knowledgeEvidenceTextHasNegativeBoundary(right) {
		return true
	}
	return knowledgeEvidenceFAQAnswersConflictWithoutPolarity(left, right)
}

func knowledgeEvidenceFAQClaimsConflict(leftQuestion string, leftAnswer string, rightQuestion string, rightAnswer string) bool {
	if knowledgeEvidenceFAQPairUsesExistenceSubject(leftQuestion, leftAnswer, rightQuestion, rightAnswer) &&
		knowledgeEvidenceFAQClaimsComparableForConflict(leftQuestion, leftAnswer, rightQuestion, rightAnswer) {
		leftSubject := knowledgeEvidenceFAQExistenceSubject(leftQuestion, leftAnswer)
		rightSubject := knowledgeEvidenceFAQExistenceSubject(rightQuestion, rightAnswer)
		if leftSubject != "" && rightSubject != "" &&
			knowledgeEvidenceFAQAnswerNegatesSubject(leftAnswer, leftSubject) != knowledgeEvidenceFAQAnswerNegatesSubject(rightAnswer, rightSubject) {
			return true
		}
		return knowledgeEvidenceFAQAnswersConflictWithoutPolarity(leftAnswer, rightAnswer)
	}
	return knowledgeEvidenceFAQAnswersConflict(leftAnswer, rightAnswer)
}

func knowledgeEvidenceFAQAnswersConflictWithoutPolarity(left string, right string) bool {
	leftNumbers := knowledgeEvidenceAnswerNumberPattern.FindAllString(normalizeRuntimeKnowledgeQuery(left), -1)
	rightNumbers := knowledgeEvidenceAnswerNumberPattern.FindAllString(normalizeRuntimeKnowledgeQuery(right), -1)
	if len(leftNumbers) > 0 && len(rightNumbers) > 0 && strings.Join(leftNumbers, "|") != strings.Join(rightNumbers, "|") {
		return true
	}
	if knowledgeEvidenceStringSetsConflict(knowledgeEvidenceLocationSignatures(left), knowledgeEvidenceLocationSignatures(right)) {
		return true
	}
	if knowledgeEvidenceMethodSignaturesConflict(knowledgeEvidenceMethodSignatures(left), knowledgeEvidenceMethodSignatures(right)) {
		return true
	}
	if knowledgeEvidenceScopeSignaturesConflict(knowledgeEvidenceScopeSignatures(left), knowledgeEvidenceScopeSignatures(right)) {
		return true
	}
	return knowledgeEvidenceConditionSignaturesConflict(left, right)
}

func knowledgeEvidenceLocationSignatures(text string) []string {
	if explicit := knowledgeEvidenceExplicitPickupLocationSignatures(text); len(explicit) > 0 {
		return explicit
	}
	compact := normalizeRuntimeKnowledgeQuery(text)
	ret := make([]string, 0, 3)
	for _, item := range []struct {
		value     string
		canonical string
	}{
		{"洗衣房", "洗衣房"}, {"百宝箱", "百宝箱"}, {"床头柜", "床头柜"}, {"电视柜", "电视柜"},
		{"电梯口", "电梯口"}, {"地下车库", "地下车库"}, {"停车场", "停车场"}, {"餐厅", "餐厅"},
		{"大堂", "大堂"}, {"前台", "前台"}, {"一楼", "一楼"}, {"楼下", "楼下"}, {"楼上", "楼上"},
		{"房门口", "房门口"}, {"房间门口", "房门口"}, {"房间内", "房间"}, {"房内", "房间"}, {"客房", "房间"},
	} {
		if strings.Contains(compact, item.value) {
			ret = appendIfMissing(ret, item.canonical)
		}
	}
	return ret
}

func knowledgeEvidenceExplicitPickupLocationSignatures(text string) []string {
	ret := make([]string, 0, 2)
	for _, clause := range splitKnowledgeEvidenceAnswerClauses(text) {
		compact := normalizeRuntimeKnowledgeQuery(clause)
		pickupIndex := firstKnowledgeEvidenceTextMarkerIndex(compact, []string{"领取", "自取", "拿取", "取用", "去拿"})
		if pickupIndex <= 0 {
			continue
		}
		prefix := compact[:pickupIndex]
		locationStart := -1
		locationMarkerLength := 0
		for _, marker := range []string{"前往", "到", "在"} {
			if index := strings.LastIndex(prefix, marker); index >= 0 {
				locationStart = index
				locationMarkerLength = len(marker)
				break
			}
		}
		if locationStart < 0 {
			continue
		}
		location := strings.TrimSpace(prefix[locationStart+locationMarkerLength:])
		location = strings.Trim(location, "，,。；;！!？?：:")
		for _, suffix := range []string{"可以", "可", "自行", "直接", "去"} {
			location = strings.TrimSuffix(location, suffix)
		}
		location = strings.TrimSpace(location)
		if length := len([]rune(location)); length < 2 || length > 48 {
			continue
		}
		ret = appendIfMissing(ret, location)
	}
	return ret
}

func firstKnowledgeEvidenceTextMarkerIndex(text string, markers []string) int {
	first := -1
	for _, marker := range markers {
		if index := strings.Index(text, marker); index >= 0 && (first < 0 || index < first) {
			first = index
		}
	}
	return first
}

func knowledgeEvidenceStringSetsConflict(left []string, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, leftValue := range left {
		for _, rightValue := range right {
			if leftValue == rightValue || strings.Contains(leftValue, rightValue) || strings.Contains(rightValue, leftValue) {
				return false
			}
		}
	}
	return true
}

func knowledgeEvidenceMethodSignatures(text string) []string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	ret := make([]string, 0, 2)
	if containsAny(compact, []string{"领取", "自取", "拿取", "取用", "前往", "去拿", "自行拿"}) {
		ret = append(ret, "pickup")
	}
	if containsAny(compact, []string{"送到", "送来", "送至", "配送", "送房", "送进房间"}) {
		ret = append(ret, "delivery")
	}
	if containsAny(compact, []string{"联系", "电话", "微信"}) {
		ret = append(ret, "contact")
	}
	return ret
}

func knowledgeEvidenceMethodSignaturesConflict(left []string, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, leftValue := range left {
		for _, rightValue := range right {
			if leftValue == rightValue {
				return false
			}
		}
	}
	return (knowledgeEvidenceContainsString(left, "pickup") && knowledgeEvidenceContainsString(right, "delivery")) ||
		(knowledgeEvidenceContainsString(left, "delivery") && knowledgeEvidenceContainsString(right, "pickup"))
}

func knowledgeEvidenceScopeSignatures(text string) []string {
	compact := normalizeRuntimeKnowledgeQuery(text)
	ret := make([]string, 0, 2)
	switch {
	case containsAny(compact, []string{"所有房间", "全部房间", "每个房间", "每间房", "所有房型", "全部房型"}):
		ret = append(ret, "room_coverage=all")
	case containsAny(compact, []string{"部分房间", "部分房型", "视房型", "不同房型"}):
		ret = append(ret, "room_coverage=some")
	}
	if containsAny(compact, []string{"只能放一楼", "仅限一楼", "只送一楼"}) {
		ret = append(ret, "delivery_scope=first_floor")
	}
	if containsAny(compact, []string{"送到房间", "送至房间", "送到房门口", "送至房门口"}) {
		ret = append(ret, "delivery_scope=room")
	}
	return ret
}

func knowledgeEvidenceScopeSignaturesConflict(left []string, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	leftByDimension := make(map[string]string, len(left))
	for _, signature := range left {
		parts := strings.SplitN(signature, "=", 2)
		if len(parts) == 2 {
			leftByDimension[parts[0]] = parts[1]
		}
	}
	for _, signature := range right {
		parts := strings.SplitN(signature, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if leftValue, ok := leftByDimension[parts[0]]; ok && leftValue != parts[1] {
			return true
		}
	}
	return false
}

func knowledgeEvidenceConditionSignaturesConflict(left string, right string) bool {
	leftCompact := normalizeRuntimeKnowledgeQuery(left)
	rightCompact := normalizeRuntimeKnowledgeQuery(right)
	leftDaily := containsAny(leftCompact, []string{"每天", "每日", "全天"})
	rightDaily := containsAny(rightCompact, []string{"每天", "每日", "全天"})
	leftLimited := containsAny(leftCompact, []string{"仅工作日", "只限工作日", "仅周末", "只限周末", "仅节假日", "只限节假日"})
	rightLimited := containsAny(rightCompact, []string{"仅工作日", "只限工作日", "仅周末", "只限周末", "仅节假日", "只限节假日"})
	if (leftDaily && rightLimited) || (rightDaily && leftLimited) {
		return true
	}
	if !leftLimited || !rightLimited {
		return false
	}
	return knowledgeEvidenceExclusiveCondition(leftCompact) != knowledgeEvidenceExclusiveCondition(rightCompact)
}

func knowledgeEvidenceExclusiveCondition(text string) string {
	for _, condition := range []string{"工作日", "周末", "节假日"} {
		if strings.Contains(text, condition) {
			return condition
		}
	}
	return ""
}

func deterministicKnowledgeEvidenceFactsFromFAQ(taskID string, answer string) []knowledgeEvidenceFact {
	clauses := splitKnowledgeEvidenceAnswerClauses(answer)
	facts := make([]knowledgeEvidenceFact, 0, len(clauses)*2)
	seen := make(map[string]struct{}, len(clauses))
	for _, clause := range clauses {
		if !knowledgeEvidenceAnswerClauseIsGroundedFact(clause) {
			continue
		}
		for _, classified := range knowledgeEvidenceAnswerClauseAspects(clause) {
			criticalValues := knowledgeEvidenceAnswerClauseCriticalValues(clause, classified.CriticalValue)
			if classified.Aspect == "method" {
				for _, channel := range []string{"小程序", "入住机", "短信链接", "二维码", "房卡", "人脸", "电话", "微信", "支付宝", "银行卡", "APP", "app"} {
					if strings.Contains(clause, channel) {
						criticalValues = appendIfMissing(criticalValues, channel)
					}
				}
			}
			factID := nextKnowledgeEvidenceFactID(taskID, seen)
			seen[factID] = struct{}{}
			statement := strings.TrimSpace(clause)
			if !strings.HasSuffix(statement, "。") && !strings.HasSuffix(statement, "！") && !strings.HasSuffix(statement, "？") {
				statement += "。"
			}
			facts = append(facts, knowledgeEvidenceFact{FactID: factID, Aspect: classified.Aspect, Statement: statement, CriticalValues: criticalValues})
		}
	}
	return facts
}

func normalizedKnowledgeEvidenceEntities(entities []knowledgeEvidenceJudgeEntity) []string {
	ret := make([]string, 0, len(entities))
	seen := make(map[string]struct{}, len(entities))
	for _, entity := range entities {
		value := normalizeKnowledgeEvidenceEntityText(entity)
		if len([]rune(value)) < 2 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}

func normalizeKnowledgeEvidenceEntityText(entity knowledgeEvidenceJudgeEntity) string {
	value := normalizeRuntimeKnowledgeQuery(entity.Text)
	if strings.TrimSpace(entity.Type) == "room_type" {
		for _, suffix := range []string{"房型", "客房"} {
			value = strings.TrimSuffix(value, suffix)
		}
	}
	return value
}

func requiredKnowledgeEvidenceSubjectEntities(task knowledgeEvidenceJudgeTask) []string {
	query := normalizeKnowledgeEvidenceSubjectForMatch(task.Query)
	ret := make([]string, 0, len(task.Entities))
	for _, entity := range task.Entities {
		value := normalizeKnowledgeEvidenceSubjectForMatch(normalizeKnowledgeEvidenceEntityText(entity))
		if len([]rune(value)) < 2 || !strings.Contains(query, value) || knowledgeEvidenceContainsString([]string{"酒店", "门店", "房间", "客房", "房型", "客户", "服务", "问题", "地址", "位置", "附近"}, value) {
			continue
		}
		ret = appendIfMissing(ret, value)
	}
	return ret
}

func normalizeKnowledgeEvidenceSubjectForMatch(text string) string {
	return strings.NewReplacer(
		"wi-fi", "wifi",
		"无线网络", "wifi",
		"无线网", "wifi",
		"开发票", "发票",
		"董事长", "老板",
		"书桌", "办公桌",
		"卫生间", "洗手间",
		"周边", "附近",
	).Replace(normalizeRuntimeKnowledgeQuery(text))
}

func canonicalKnowledgeEvidenceSemanticSubject(text string) string {
	value := normalizeKnowledgeEvidenceSubjectForMatch(text)
	for {
		before := value
		for _, suffix := range []string{"服务", "设施", "设备", "用品"} {
			base := strings.TrimSuffix(value, suffix)
			if base != value && len([]rune(base)) >= 2 {
				value = base
				break
			}
		}
		if value == before {
			break
		}
	}
	return strings.Trim(value, "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
}

func knowledgeEvidenceSemanticSubjectsEquivalent(left string, right string) bool {
	left = canonicalKnowledgeEvidenceSemanticSubject(left)
	right = canonicalKnowledgeEvidenceSemanticSubject(right)
	return left != "" && right != "" && left == right
}

func knowledgeEvidenceExistenceSubjectIsNarrower(specific string, generic string) bool {
	specific = canonicalKnowledgeEvidenceSemanticSubject(specific)
	generic = canonicalKnowledgeEvidenceSemanticSubject(generic)
	if specific == "" || generic == "" || specific == generic || len([]rune(generic)) < 2 {
		return false
	}
	prefix := strings.TrimSuffix(specific, generic)
	if prefix == specific || prefix == "" {
		return false
	}
	for _, negativePrefix := range []string{
		"免", "无", "零", "非", "不含", "无需", "没有", "取消", "免收", "不收", "未收",
		"未配", "未提供", "未供应", "不配", "不带", "不提供", "不供应",
	} {
		if strings.HasSuffix(prefix, negativePrefix) {
			return false
		}
	}
	return true
}

func knowledgeEvidenceExistenceCandidateSupportsTaskSubject(taskSubject string, candidateSubject string, answer string) bool {
	if knowledgeEvidenceSemanticSubjectsEquivalent(taskSubject, candidateSubject) {
		return true
	}
	return knowledgeEvidenceExistenceSubjectIsNarrower(candidateSubject, taskSubject) &&
		!knowledgeEvidenceFAQAnswerNegatesSubject(answer, candidateSubject) &&
		!isKnowledgeHandoffDirectiveContent(answer)
}

func knowledgeEvidenceFAQAnswerNegatesSubject(answer string, subject string) bool {
	subject = canonicalKnowledgeEvidenceSemanticSubject(subject)
	if answer == "" || subject == "" {
		return false
	}
	if _, negative, ok := knowledgeEvidenceFAQAnswerPolarity(answer); ok && negative {
		return true
	}
	clauses := splitKnowledgeEvidenceAnswerClauses(answer)
	if len(clauses) == 0 {
		clauses = []string{answer}
	}
	for _, clause := range clauses {
		if !knowledgeEvidenceTextHasNegativeBoundary(clause) {
			continue
		}
		anchor := canonicalKnowledgeEvidenceSemanticSubject(knowledgeEvidenceNegativeBoundaryAnchor(clause))
		if anchor != "" && (strings.Contains(anchor, subject) || strings.Contains(subject, anchor)) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceQuestionDirectlyAsksExistenceOfSubject(question string, subject string) bool {
	candidateSubject := knowledgeEvidenceSingleCompanionSubject(question, []string{
		"是否配备", "是否配有", "是否设有", "是否提供", "是否供应", "是不是有", "有没有", "是否有", "有无",
		"可不可以", "能不能", "是否可以", "不可以", "能否", "不能", "可以", "支持", "配备", "配有", "设有", "提供", "供应", "没有", "有", "能", "吗",
	})
	return knowledgeEvidenceSemanticSubjectsEquivalent(candidateSubject, subject)
}

func knowledgeEvidenceFAQExistenceSubject(question string, answer string) string {
	subject := knowledgeEvidenceRoomTypePredicateSubject(question, knowledgeEvidenceConflictRoomTypes(question))
	if subject == "" {
		subject = knowledgeEvidenceSingleSubjectForAspects(question, []string{"existence"})
	}
	if subject == "" {
		subject = knowledgeEvidenceSingleSubjectForAspects(answer, []string{"existence"})
	}
	return canonicalKnowledgeEvidenceSemanticSubject(subject)
}

func knowledgeEvidenceExistenceFAQSubjectsComparable(leftQuestion string, leftAnswer string, rightQuestion string, rightAnswer string) bool {
	leftSubject := knowledgeEvidenceFAQExistenceSubject(leftQuestion, leftAnswer)
	rightSubject := knowledgeEvidenceFAQExistenceSubject(rightQuestion, rightAnswer)
	if leftSubject == "" || rightSubject == "" || knowledgeEvidenceSemanticSubjectsEquivalent(leftSubject, rightSubject) {
		return true
	}

	leftNarrower := knowledgeEvidenceExistenceSubjectIsNarrower(leftSubject, rightSubject)
	rightNarrower := knowledgeEvidenceExistenceSubjectIsNarrower(rightSubject, leftSubject)
	if !leftNarrower && !rightNarrower {
		return false
	}

	leftNegative := knowledgeEvidenceFAQAnswerNegatesSubject(leftAnswer, leftSubject)
	rightNegative := knowledgeEvidenceFAQAnswerNegatesSubject(rightAnswer, rightSubject)
	if leftNarrower {
		if isKnowledgeHandoffDirectiveContent(rightAnswer) {
			return true
		}
		return !leftNegative && rightNegative
	}
	if isKnowledgeHandoffDirectiveContent(leftAnswer) {
		return true
	}
	return leftNegative && !rightNegative
}

func knowledgeEvidenceFAQClaimsComparableForConflict(leftQuestion string, leftAnswer string, rightQuestion string, rightAnswer string) bool {
	leftSubjectClaim := knowledgeEvidenceFAQHasExistenceClaim(leftQuestion, leftAnswer)
	rightSubjectClaim := knowledgeEvidenceFAQHasExistenceClaim(rightQuestion, rightAnswer)
	if leftSubjectClaim || rightSubjectClaim {
		return leftSubjectClaim && rightSubjectClaim &&
			knowledgeEvidenceExistenceFAQSubjectsComparable(leftQuestion, leftAnswer, rightQuestion, rightAnswer)
	}
	return true
}

func knowledgeEvidenceFAQPairUsesExistenceSubject(leftQuestion string, leftAnswer string, rightQuestion string, rightAnswer string) bool {
	return knowledgeEvidenceFAQHasExistenceClaim(leftQuestion, leftAnswer) ||
		knowledgeEvidenceFAQHasExistenceClaim(rightQuestion, rightAnswer)
}

func knowledgeEvidenceFAQHasExistenceClaim(question string, answer string) bool {
	if knowledgeEvidenceQuestionShapeHasExistenceSubject(question, knowledgeEvidenceReviewQuestionShape(question)) {
		return true
	}
	subject := knowledgeEvidenceFAQExistenceSubject(question, answer)
	return subject != "" && knowledgeEvidenceFAQAnswerNegatesSubject(answer, subject)
}

func knowledgeEvidenceQuestionShapeHasExistenceSubject(question string, shape string) bool {
	if shape == "existence" {
		return true
	}
	return shape == "list" && knowledgeEvidenceSpatialRecommendationTopic(question) == "" && knowledgeEvidenceFAQExistenceSubject(question, "") != ""
}

func knowledgeEvidenceSingleSubjectForAspects(text string, aspects []string) string {
	clauses := splitKnowledgeEvidenceAnswerClauses(text)
	if len(clauses) == 0 {
		clauses = []string{text}
	}
	subjects := make([]string, 0, 2)
	appendSubject := func(subject string) bool {
		subject = canonicalKnowledgeEvidenceSemanticSubject(subject)
		if len([]rune(subject)) < 2 {
			return true
		}
		for _, existing := range subjects {
			if knowledgeEvidenceSemanticSubjectsEquivalent(existing, subject) {
				return true
			}
		}
		subjects = append(subjects, subject)
		return len(subjects) == 1
	}
	for _, clause := range clauses {
		compact := normalizeRuntimeKnowledgeQuery(clause)
		if requiredKnowledgeEvidenceAspect(aspects, "existence") && knowledgeEvidenceClauseExpressesExistence(compact) {
			if !appendSubject(knowledgeEvidenceSingleExistenceSubject(clause)) {
				return ""
			}
		}
		if requiredKnowledgeEvidenceAspect(aspects, "price") && containsAny(compact, []string{
			"免费", "收费", "多少钱", "价格", "费用", "金额", "价钱", "价位", "要钱", "付费", "付钱",
		}) {
			if !appendSubject(knowledgeEvidenceSinglePriceSubject(clause)) {
				return ""
			}
		}
		if requiredKnowledgeEvidenceAspect(aspects, "time") && containsAny(compact, []string{"几点", "什么时候", "何时", "时间", "多久"}) {
			if !appendSubject(knowledgeEvidenceSingleCompanionSubject(clause, []string{
				"几点开始", "几点结束", "几点", "什么时候", "何时", "时间是多少", "时间", "多久",
			})) {
				return ""
			}
		}
		if requiredKnowledgeEvidenceAspect(aspects, "location") && containsAny(compact, []string{"在哪里", "在哪", "哪里", "地址", "位置", "楼层"}) {
			if !appendSubject(knowledgeEvidenceSingleCompanionSubject(clause, []string{
				"在哪里吃", "在哪吃", "哪里吃", "在哪里拿", "在哪拿", "哪里拿", "在哪里", "在哪", "哪里", "地址", "位置", "楼层",
			})) {
				return ""
			}
		}
	}
	if len(subjects) != 1 {
		return ""
	}
	return subjects[0]
}

func knowledgeEvidenceClauseExpressesExistence(compact string) bool {
	compact = normalizeRuntimeKnowledgeQuery(compact)
	return containsAny(compact, []string{
		"有没有", "是否有", "是不是有", "有无", "有吗", "提供吗", "配备吗", "是否提供", "是否配备", "有哪些", "有什么",
		"提供", "配备", "配有", "设有", "存在", "供应", "可不可以", "能不能", "能否", "可以", "支持",
	}) || strings.Contains(compact, "有")
}

func knowledgeEvidenceSingleCompanionSubject(text string, markers []string) string {
	compact := normalizeKnowledgeEvidenceSubjectForMatch(text)
	compact = strings.NewReplacer(
		"麻烦请问一下", "", "麻烦请问", "", "麻烦问一下", "", "麻烦问下", "",
		"我想请问一下", "", "我想请问", "", "我想问一下", "", "我想问", "",
		"请问一下", "", "请问", "", "想问一下", "", "想问", "", "麻烦", "",
		"你们酒店", "", "咱们酒店", "", "本酒店", "", "你们门店", "", "本门店", "", "你们家", "", "咱们家", "", "你们", "", "咱们", "", "本店", "", "酒店", "", "门店", "",
		"每个房间", "", "房间里面", "", "房间内", "", "房间里", "", "客房内", "", "客房里", "", "房内", "", "客房", "", "房间", "",
	).Replace(compact)
	for _, marker := range markers {
		compact = strings.ReplaceAll(compact, normalizeRuntimeKnowledgeQuery(marker), "")
	}
	compact = strings.Trim(compact, "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	if len([]rune(compact)) < 2 || len([]rune(compact)) > 24 || containsAny(compact, []string{"什么", "哪些", "哪个", "哪种", "啥"}) {
		return ""
	}
	return compact
}

func knowledgeEvidenceCandidateMatchesTaskSubjects(task knowledgeEvidenceJudgeTask, candidate knowledgeEvidenceJudgeCandidate, question string, answer string) bool {
	if knowledgeEvidenceConfigurationTopic(task.Query) != "" &&
		!knowledgeEvidenceConfigurationScopeMatches(task.Query, strings.Join([]string{question, answer, candidate.Hit.Title}, " ")) {
		return false
	}
	if !knowledgeEvidenceCandidateMatchesImplicitSinglePriceSubject(task, candidate, question, answer) {
		return false
	}
	if !knowledgeEvidenceCandidateMatchesImplicitSingleExistenceSubject(task, candidate, question, answer) {
		return false
	}
	required := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(required) == 0 {
		return true
	}
	text := normalizeKnowledgeEvidenceSubjectForMatch(strings.Join([]string{question, answer, candidate.Hit.Content}, " "))
	for _, subject := range required {
		if !strings.Contains(text, subject) {
			return false
		}
	}
	return true
}

func knowledgeEvidenceCandidateMatchesImplicitSinglePriceSubject(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
	question string,
	answer string,
) bool {
	taskSubject, guarded := knowledgeEvidenceImplicitSinglePriceSubject(task)
	if !guarded {
		return true
	}
	candidateSubject := ""
	if inferred := knowledgeEvidenceInferredQuantitySubjects(question); len(inferred) == 1 {
		candidateSubject = inferred[0]
	}
	if candidateSubject == "" {
		candidateSubject = knowledgeEvidenceSingleSubjectForAspects(question, requiredKnowledgeEvidenceAspects(task))
	}
	if candidateSubject == "" && strings.Contains(normalizeKnowledgeEvidenceSubjectForMatch(question), canonicalKnowledgeEvidenceSemanticSubject(taskSubject)) {
		return true
	}
	if candidateSubject == "" {
		candidateSubject = knowledgeEvidenceSingleSubjectForAspects(strings.Join([]string{candidate.Hit.Title, answer, candidate.Hit.Content}, " "), requiredKnowledgeEvidenceAspects(task))
	}
	if knowledgeEvidenceSemanticSubjectsEquivalent(taskSubject, candidateSubject) {
		return true
	}
	taskScope := knowledgeEvidenceConflictObjectScope(task.Query)
	candidateScope := knowledgeEvidenceCandidateApplicabilityScope(task, candidate, question, answer)
	if taskScope == "" || candidateScope == "" || taskScope != candidateScope {
		return false
	}
	return knowledgeEvidenceSemanticSubjectsEquivalent(
		knowledgeEvidenceSubjectWithoutGenericScope(taskSubject),
		knowledgeEvidenceSubjectWithoutGenericScope(candidateSubject),
	)
}

func knowledgeEvidenceCandidateMatchesImplicitSingleExistenceSubject(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
	question string,
	answer string,
) bool {
	taskSubject, guarded := knowledgeEvidenceImplicitSingleExistenceSubject(task)
	if !guarded {
		return true
	}
	anchors := explicitKnowledgeEvidenceTaskRoomTypes(task)
	candidateSubject := knowledgeEvidenceRoomTypePredicateSubject(question, anchors)
	if candidateSubject == "" {
		candidateSubject = knowledgeEvidenceSingleSubjectForAspects(question, requiredKnowledgeEvidenceAspects(task))
	}
	if candidateSubject == "" && knowledgeEvidenceQuestionDirectlyAsksExistenceOfSubject(question, taskSubject) {
		return true
	}
	if candidateSubject == "" {
		candidateSubject = knowledgeEvidenceSingleSubjectForAspects(candidate.Hit.Title, requiredKnowledgeEvidenceAspects(task))
	}
	if candidateSubject == "" {
		candidateSubject = knowledgeEvidenceSingleSubjectForAspects(answer, requiredKnowledgeEvidenceAspects(task))
	}
	return knowledgeEvidenceExistenceCandidateSupportsTaskSubject(taskSubject, candidateSubject, answer)
}

func knowledgeEvidenceImplicitSingleExistenceSubject(task knowledgeEvidenceJudgeTask) (string, bool) {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	aspects := requiredKnowledgeEvidenceAspects(task)
	if !requiredKnowledgeEvidenceAspect(aspects, "existence") || knowledgeEvidenceQueryAsksComparison(task.Query) {
		return "", false
	}
	anchors := explicitKnowledgeEvidenceTaskRoomTypes(task)
	if len(anchors) > 0 {
		predicates := make([]string, 0, len(requiredSubjects))
		for _, subject := range requiredSubjects {
			if !knowledgeEvidenceContainsString(anchors, subject) {
				predicates = appendIfMissing(predicates, subject)
			}
		}
		switch len(predicates) {
		case 1:
			return predicates[0], true
		case 0:
			if subject := knowledgeEvidenceRoomTypePredicateSubject(task.Query, anchors); subject != "" {
				return subject, true
			}
		}
		return "", false
	}
	if len(requiredSubjects) > 1 {
		return "", false
	}
	if len(requiredSubjects) == 1 {
		return requiredSubjects[0], true
	}
	if inferredSubjects := knowledgeEvidenceInferredQuantitySubjects(task.Query); len(inferredSubjects) == 1 {
		return inferredSubjects[0], true
	}
	subject := knowledgeEvidenceSingleSubjectForAspects(task.Query, aspects)
	if len([]rune(subject)) < 2 || containsAny(subject, []string{"和", "与", "及", "、", "分别", "各自"}) {
		return "", false
	}
	return subject, true
}

func knowledgeEvidenceRoomTypePredicateSubject(text string, anchors []string) string {
	compact := normalizeKnowledgeEvidenceSubjectForMatch(text)
	if compact == "" {
		return ""
	}
	hasRoomTypeContext := len(anchors) > 0 || containsAny(compact, []string{
		"房型", "哪种客房", "哪些客房", "哪几种客房", "什么客房",
	})
	if !hasRoomTypeContext {
		return ""
	}
	for _, anchor := range anchors {
		compact = strings.ReplaceAll(compact, normalizeKnowledgeEvidenceSubjectForMatch(anchor), "")
	}
	compact = strings.NewReplacer(
		"有该设施的房型", "", "配备该设施的房型", "", "提供该设施的房型", "",
		"哪些房型", "", "哪几种房型", "", "哪种房型", "", "什么房型", "", "所有房型", "", "全部房型", "", "各个房型", "", "各房型", "", "房型", "",
		"哪些客房", "", "哪几种客房", "", "哪种客房", "", "什么客房", "", "所有客房", "", "全部客房", "", "各个客房", "", "各客房", "",
		"都有", "有", "均有", "有", "都", "", "均", "",
	).Replace(compact)
	compact = strings.Trim(compact, "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了和与及、")
	return knowledgeEvidenceSingleExistenceSubject(compact)
}

func knowledgeEvidenceSubjectWithoutGenericScope(subject string) string {
	return strings.Trim(strings.NewReplacer(
		"每个房间", "", "每间房", "", "房间里面", "", "房间内", "", "房间里", "", "客房里面", "", "客房内", "", "客房里", "", "房内", "", "客房", "", "房间", "",
		"酒店里面", "", "酒店内", "", "门店里面", "", "门店内", "", "本店内", "", "店内", "", "酒店", "", "门店", "", "本店", "",
	).Replace(canonicalKnowledgeEvidenceSemanticSubject(subject)), "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
}

func knowledgeEvidenceSingleExistenceSubject(text string) string {
	compact := normalizeKnowledgeEvidenceSubjectForMatch(text)
	if compact == "" || containsAny(compact, []string{"和", "与", "及", "、", "分别", "各自"}) {
		return ""
	}
	compact = strings.NewReplacer(
		"麻烦请问一下", "", "麻烦请问", "", "麻烦问一下", "", "麻烦问下", "",
		"我想请问一下", "", "我想请问", "", "我想问一下", "", "我想问", "",
		"请问一下", "", "请问", "", "想问一下", "", "想问", "", "麻烦", "",
		"你们酒店", "", "咱们酒店", "", "本酒店", "", "你们门店", "", "本门店", "", "你们家", "", "咱们家", "", "你们", "", "咱们", "", "本店", "", "酒店", "", "门店", "",
		"每个房间", "", "房间里面", "", "房间内", "", "房间里", "", "客房内", "", "客房里", "", "房内", "", "客房", "", "房间", "",
		"是否配备", "", "是否配有", "", "是否设有", "", "是否提供", "", "是否存在", "", "是不是有", "", "有没有", "", "是否有", "", "有无", "", "是否", "",
		"都有哪些", "", "有哪些", "", "有什么", "",
		"可不可以", "", "能不能", "", "能否", "", "是否可以", "", "可以", "", "支持", "",
		"不提供", "", "未提供", "", "没有", "", "提供", "", "配备", "", "配有", "", "设有", "", "存在", "", "供应", "", "有", "",
		"的吗", "", "的么", "", "的吗", "", "吗", "", "呢", "", "么", "", "嘛", "",
	).Replace(compact)
	compact = strings.Trim(compact, "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
	if len([]rune(compact)) < 2 || len([]rune(compact)) > 24 || containsAny(compact, []string{"什么", "哪些", "哪个", "哪种", "啥"}) {
		return ""
	}
	return compact
}

func knowledgeEvidenceImplicitSinglePriceSubject(task knowledgeEvidenceJudgeTask) (string, bool) {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) > 1 {
		return "", false
	}
	aspects := requiredKnowledgeEvidenceAspects(task)
	if !requiredKnowledgeEvidenceAspect(aspects, "price") || knowledgeEvidenceQueryAsksComparison(task.Query) {
		return "", false
	}
	if len(requiredSubjects) == 1 {
		return requiredSubjects[0], true
	}
	if inferredSubjects := knowledgeEvidenceInferredQuantitySubjects(task.Query); len(inferredSubjects) == 1 {
		return inferredSubjects[0], true
	}
	subject := knowledgeEvidenceSingleSubjectForAspects(task.Query, aspects)
	if len([]rune(subject)) < 2 || containsAny(subject, []string{"和", "与", "及", "、", "还是", "分别", "各自"}) {
		return "", false
	}
	return subject, true
}

func knowledgeEvidenceSinglePriceSubject(text string) string {
	compact := normalizeKnowledgeEvidenceSubjectForMatch(text)
	subject := strings.NewReplacer(
		"你们酒店", "", "咱们酒店", "", "本酒店", "", "你们门店", "", "本门店", "", "你们家", "", "咱们家", "", "你们", "", "咱们", "", "本店", "",
		"怎么收费", "", "如何收费", "", "是否收费", "", "是不是免费", "", "是否免费", "", "有没有收费", "",
		"多少钱", "", "价格", "", "费用", "", "金额", "", "价钱", "", "价位", "", "免费", "", "收费", "", "付费", "", "要钱", "", "付钱", "", "花钱", "",
		"是否", "", "是不是", "", "有没有", "", "需要", "", "的", "", "吗", "", "呢", "", "是", "", "都", "",
	).Replace(compact)
	return strings.Trim(subject, "，,。.!！?？；;：:啊呀呢吧哈啦哦嘛么的了")
}

// Model-selected evidence owns semantic synonym resolution. The local guard
// only rejects contradictions that can be proven without another semantic
// judgement, such as a different explicit room type or configuration scope.
func knowledgeEvidenceSelectedCandidatesHaveExplicitSubjectConflict(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) bool {
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	for _, candidate := range allKnowledgeEvidenceJudgeTaskCandidates(task) {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if knowledgeEvidenceCandidateHasExplicitTaskConflict(task, candidate, question, answer) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceCandidateHasExplicitTaskConflict(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
	question string,
	answer string,
) bool {
	if !knowledgeEvidenceCandidateMatchesImplicitSinglePriceSubject(task, candidate, question, answer) {
		return true
	}
	if !knowledgeEvidenceCandidateMatchesImplicitSingleExistenceSubject(task, candidate, question, answer) {
		return true
	}
	candidateText := strings.Join([]string{question, answer, candidate.Hit.Title}, " ")
	if knowledgeEvidenceConfigurationTopic(task.Query) != "" &&
		!knowledgeEvidenceConfigurationScopeMatches(task.Query, candidateText) {
		return true
	}
	taskScope := knowledgeEvidenceConflictObjectScope(task.Query)
	candidateScope := knowledgeEvidenceCandidateApplicabilityScope(task, candidate, question, answer)
	if taskScope != "" && taskScope != "hotel" && candidateScope != "" && candidateScope != "hotel" && taskScope != candidateScope {
		return true
	}
	taskRoomTypes := explicitKnowledgeEvidenceTaskRoomTypes(task)
	if len(taskRoomTypes) > 0 && !knowledgeEvidenceTextContainsAnyRoomType(candidateText, taskRoomTypes) {
		candidateRoomTypes := knowledgeEvidenceConflictRoomTypes(candidateText)
		if len(candidateRoomTypes) > 0 && knowledgeEvidenceStringSetsConflict(taskRoomTypes, candidateRoomTypes) {
			return true
		}
	}
	taskConditions := knowledgeEvidenceConflictConditions(task.Query)
	candidateConditions := knowledgeEvidenceConflictConditions(candidateText)
	if len(taskConditions) > 0 && len(candidateConditions) > 0 && knowledgeEvidenceStringSetsConflict(taskConditions, candidateConditions) {
		return true
	}
	taskSignature := knowledgeEvidenceConflictQuestionSignature(task.Query)
	candidateSignature := knowledgeEvidenceConflictQuestionSignature(question)
	if taskSignature != "" && candidateSignature != "" && !knowledgeEvidenceConflictQuestionSignaturesCompatible(taskSignature, candidateSignature) {
		return true
	}
	taskMethodDomain := knowledgeEvidenceMethodDomain(task.Query)
	candidateMethodDomain := knowledgeEvidenceMethodDomain(question)
	if taskMethodDomain != "" && candidateMethodDomain != "" && taskMethodDomain != candidateMethodDomain {
		return true
	}
	taskTarget := knowledgeEvidenceServiceOperationTarget(task.Query)
	candidateTarget := knowledgeEvidenceServiceOperationTarget(strings.Join([]string{question, answer}, " "))
	return taskTarget != "" && candidateTarget != "" &&
		!knowledgeEvidenceServiceOperationTargetsCompatible(taskTarget, candidateTarget)
}

func knowledgeEvidenceCandidateApplicabilityScope(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
	question string,
	answer string,
) string {
	if canonicalIntentCode(task.Intent) == "service_request" || knowledgeEvidenceServiceOperationTarget(task.Query) != "" {
		// A service FAQ title or answer often names the destination of an
		// action (for example, "大堂用品领取"). That destination is not the
		// applicability scope of the customer's room request. Only the FAQ
		// question can prove an explicit, conflicting service scope here.
		return knowledgeEvidenceConflictObjectScope(question)
	}
	if scope := knowledgeEvidenceConflictObjectScope(strings.Join([]string{question, candidate.Hit.Title}, " ")); scope != "" {
		return scope
	}
	return knowledgeEvidenceConflictObjectScope(answer)
}

func explicitKnowledgeEvidenceTaskAnchorEntities(task knowledgeEvidenceJudgeTask, required []string) []string {
	query := normalizeKnowledgeEvidenceSubjectForMatch(task.Query)
	anchors := make([]string, 0, 1)
	for _, entity := range task.Entities {
		if !strings.EqualFold(strings.TrimSpace(entity.Type), "room_type") {
			continue
		}
		value := normalizeKnowledgeEvidenceSubjectForMatch(normalizeKnowledgeEvidenceEntityText(entity))
		if value == "" || !strings.Contains(query, value) || !knowledgeEvidenceContainsString(required, value) {
			continue
		}
		anchors = appendIfMissing(anchors, value)
	}
	return anchors
}

func knowledgeEvidenceSelectedCandidatesMatchTaskSubjects(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string, decision string) bool {
	required := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(required) == 0 {
		return true
	}
	anchors := explicitKnowledgeEvidenceTaskAnchorEntities(task, required)
	requested := make([]string, 0, len(required))
	for _, subject := range required {
		if !knowledgeEvidenceContainsString(anchors, subject) {
			requested = append(requested, subject)
		}
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	if len(anchors) > 0 {
		return knowledgeEvidenceSelectedCandidatesCoverAnchoredSubjects(task, layer, selected, anchors, requested, decision)
	}
	covered := make(map[string]bool, len(required))
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		text := normalizeKnowledgeEvidenceSubjectForMatch(strings.Join([]string{question, answer, candidate.Hit.Content}, " "))
		matched := false
		for _, subject := range required {
			if strings.Contains(text, subject) {
				covered[subject] = true
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	if strings.TrimSpace(decision) == knowledgeEvidenceDecisionPartial {
		for _, subject := range required {
			if covered[subject] {
				return true
			}
		}
		return false
	}
	for _, subject := range required {
		if !covered[subject] {
			return false
		}
	}
	return true
}

func knowledgeEvidenceSelectedCandidatesCoverAnchoredSubjects(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selected map[string]struct{},
	anchors []string,
	requested []string,
	decision string,
) bool {
	anchorCovered := make(map[string]bool, len(anchors))
	pairCovered := make(map[string]bool, len(anchors)*len(requested))
	matchedCandidates := 0
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		matchedCandidates++
		candidateAnchors, candidatePairs := knowledgeEvidenceCandidateAnchoredSubjectCoverage(task, candidate, anchors, requested)
		if len(candidateAnchors) == 0 || (len(requested) > 0 && len(candidatePairs) == 0) {
			return false
		}
		for _, anchor := range candidateAnchors {
			anchorCovered[anchor] = true
		}
		for _, pair := range candidatePairs {
			pairCovered[pair] = true
		}
	}
	if matchedCandidates != len(selected) {
		return false
	}
	if strings.TrimSpace(decision) == knowledgeEvidenceDecisionPartial {
		if len(requested) == 0 {
			return len(anchorCovered) > 0
		}
		return len(pairCovered) > 0
	}
	if len(requested) == 0 {
		for _, anchor := range anchors {
			if !anchorCovered[anchor] {
				return false
			}
		}
		return true
	}
	for _, anchor := range anchors {
		for _, subject := range requested {
			if !pairCovered[knowledgeEvidenceSubjectPairKey(anchor, subject)] {
				return false
			}
		}
	}
	return true
}

func knowledgeEvidenceCandidateAnchoredSubjectCoverage(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
	anchors []string,
	requested []string,
) ([]string, []string) {
	question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
	questionAnchors, questionPairs := knowledgeEvidenceAnchoredSubjectCoverageFromText(question, anchors, requested)
	clauses := splitKnowledgeEvidenceAnswerClauses(answer)
	if len(clauses) == 0 && strings.TrimSpace(answer) != "" {
		clauses = []string{answer}
	}
	if len(clauses) == 0 {
		clauses = splitKnowledgeEvidenceAnswerClauses(candidate.Hit.Content)
	}
	coveredAnchors := make([]string, 0, len(anchors))
	coveredPairs := make([]string, 0, len(anchors)*len(requested))
	for _, clause := range clauses {
		if knowledgeEvidenceTextHasUncertaintyBoundary(clause) {
			continue
		}
		text := normalizeKnowledgeEvidenceSubjectForMatch(clause)
		clauseAnchors := knowledgeEvidenceContainedSubjects(text, anchors)
		clauseRequested := knowledgeEvidenceContainedSubjects(text, requested)
		if len(clauseAnchors) > 0 && len(clauseRequested) > 0 {
			_, explicitPairs := knowledgeEvidenceAnchoredSubjectCoverageFromText(clause, anchors, requested)
			coveredAnchors = appendKnowledgeEvidenceStrings(coveredAnchors, clauseAnchors)
			coveredPairs = appendKnowledgeEvidenceStrings(coveredPairs, explicitPairs)
			continue
		}
		if knowledgeEvidenceFAQAnswerConfirmsTaskQuestion(task, question, answer) && knowledgeEvidenceFAQAnswerConfirmsQuestion(clause) {
			coveredAnchors = appendKnowledgeEvidenceStrings(coveredAnchors, questionAnchors)
			coveredPairs = appendKnowledgeEvidenceStrings(coveredPairs, questionPairs)
			continue
		}
		for _, pair := range questionPairs {
			anchor, subject := splitKnowledgeEvidenceSubjectPairKey(pair)
			if len(clauseAnchors) > 0 && !knowledgeEvidenceContainsString(clauseAnchors, anchor) {
				continue
			}
			if len(clauseRequested) > 0 && !knowledgeEvidenceContainsString(clauseRequested, subject) {
				continue
			}
			coveredAnchors = appendIfMissing(coveredAnchors, anchor)
			coveredPairs = appendIfMissing(coveredPairs, pair)
		}
	}
	return coveredAnchors, coveredPairs
}

func knowledgeEvidenceAnchoredSubjectCoverageFromText(text string, anchors []string, requested []string) ([]string, []string) {
	normalized := normalizeKnowledgeEvidenceSubjectForMatch(text)
	containedAnchors := knowledgeEvidenceContainedSubjects(normalized, anchors)
	containedRequested := knowledgeEvidenceContainedSubjects(normalized, requested)
	if len(containedAnchors) == 0 {
		return nil, nil
	}
	if len(containedRequested) == 0 {
		return containedAnchors, nil
	}
	clauses := splitKnowledgeEvidenceSubjectClauses(text)
	if len(clauses) <= 1 || len(containedAnchors) == 1 {
		return containedAnchors, crossKnowledgeEvidenceSubjectPairs(containedAnchors, containedRequested)
	}

	// Across multiple clauses, only carry an anchor through a subject-only list
	// into the immediately following predicate. Independent clauses remain
	// independent, so "A has X, B has Y" never becomes A/B × X/Y.
	pairs := make([]string, 0, len(containedAnchors)*len(containedRequested))
	pendingAnchors := make([]string, 0, len(containedAnchors))
	lastAnchors := make([]string, 0, len(containedAnchors))
	for _, clause := range clauses {
		clauseText := normalizeKnowledgeEvidenceSubjectForMatch(clause)
		clauseAnchors := knowledgeEvidenceContainedSubjects(clauseText, containedAnchors)
		clauseRequested := knowledgeEvidenceContainedSubjects(clauseText, containedRequested)
		if len(clauseAnchors) > 0 && len(clauseRequested) == 0 {
			pendingAnchors = appendKnowledgeEvidenceStrings(pendingAnchors, clauseAnchors)
			continue
		}
		activeAnchors := clauseAnchors
		if len(clauseAnchors) > 0 {
			activeAnchors = appendKnowledgeEvidenceStrings(append([]string(nil), pendingAnchors...), clauseAnchors)
			pendingAnchors = nil
			lastAnchors = append([]string(nil), activeAnchors...)
		} else if len(clauseRequested) > 0 {
			activeAnchors = lastAnchors
		}
		pairs = appendKnowledgeEvidenceStrings(pairs, crossKnowledgeEvidenceSubjectPairs(activeAnchors, clauseRequested))
	}
	return containedAnchors, pairs
}

func splitKnowledgeEvidenceSubjectClauses(text string) []string {
	parts := strings.FieldsFunc(strings.TrimSpace(text), func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '！', '!', '？', '?', '；', ';', '，', ',', '、':
			return true
		default:
			return false
		}
	})
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			ret = append(ret, part)
		}
	}
	return ret
}

func crossKnowledgeEvidenceSubjectPairs(anchors []string, requested []string) []string {
	pairs := make([]string, 0, len(anchors)*len(requested))
	for _, anchor := range anchors {
		for _, subject := range requested {
			pairs = appendIfMissing(pairs, knowledgeEvidenceSubjectPairKey(anchor, subject))
		}
	}
	return pairs
}

func appendKnowledgeEvidenceStrings(existing []string, values []string) []string {
	ret := existing
	for _, value := range values {
		ret = appendIfMissing(ret, value)
	}
	return ret
}

func knowledgeEvidenceContainedSubjects(text string, subjects []string) []string {
	ret := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		if strings.Contains(text, subject) {
			ret = appendIfMissing(ret, subject)
		}
	}
	return ret
}

func knowledgeEvidenceSubjectPairKey(anchor string, subject string) string {
	return strings.TrimSpace(anchor) + "\x00" + strings.TrimSpace(subject)
}

func splitKnowledgeEvidenceSubjectPairKey(pair string) (string, string) {
	parts := strings.SplitN(pair, "\x00", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func highConfidenceKnowledgeConsensusSelection(task knowledgeEvidenceJudgeTask, layer string, requiredEntities []string) (knowledgeEvidenceLayerSelection, bool) {
	minimumScore := knowledgeEvidenceDirectFAQMinimumScore
	var best *knowledgeEvidenceJudgeCandidate
	bestTarget := ""
	bestPredicate := ""
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) || candidate.Hit.Score < minimumScore {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		if strings.TrimSpace(question) == "" || strings.TrimSpace(answer) == "" {
			continue
		}
		answer = strings.TrimSpace(answer)
		target, predicate, ok := knowledgeEvidenceAffirmativeEnumerationMatch(answer, requiredEntities)
		if !ok {
			continue
		}
		copy := candidate
		if best == nil || candidate.Hit.Score > best.Hit.Score {
			best = &copy
			bestTarget = target
			bestPredicate = predicate
		}
	}
	if best == nil || knowledgeEvidenceEnumerationHasConflict(task, layer, bestTarget, bestPredicate, minimumScore) {
		return knowledgeEvidenceLayerSelection{}, false
	}

	criticalValues := make([]string, 0, 2)
	targetText := bestTarget
	predicateText := bestPredicate
	for _, entity := range task.Entities {
		value := normalizeKnowledgeEvidenceEntityText(entity)
		if value == bestTarget {
			targetText = strings.TrimSpace(entity.Text)
		}
		if value == bestPredicate {
			predicateText = strings.TrimSpace(entity.Text)
		}
	}
	criticalValues = appendIfMissing(criticalValues, targetText)
	criticalValues = appendIfMissing(criticalValues, predicateText)
	return knowledgeEvidenceLayerSelection{
		Decision:             knowledgeEvidenceDecisionDirectSingle,
		DecisionSource:       deterministicKnowledgeEvidenceFAQDecisionSource(layer),
		SelectedCandidateIDs: []string{best.CandidateID},
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID:         strings.TrimSpace(task.TaskID) + "F1",
			Aspect:         "existence",
			Statement:      targetText + "有" + predicateText + "。",
			CriticalValues: criticalValues,
		}},
	}, true
}

func knowledgeEvidenceAffirmativeEnumerationMatch(answer string, entities []string) (string, string, bool) {
	compact := normalizeRuntimeKnowledgeQuery(answer)
	if knowledgeEvidenceTextHasNegativeBoundary(compact) || containsAny(compact, []string{"可能", "也许", "不一定", "为准", "取决于", "视情况", "具体情况"}) {
		return "", "", false
	}
	prefix, members, ok := splitKnowledgeEvidenceEnumeration(answer)
	if !ok {
		return "", "", false
	}
	for _, target := range entities {
		if !knowledgeEvidenceEnumerationContainsMember(members, target) {
			continue
		}
		for _, predicate := range entities {
			if predicate != target && strings.Contains(prefix, predicate) {
				return target, predicate, true
			}
		}
	}
	return "", "", false
}

func splitKnowledgeEvidenceEnumeration(answer string) (string, []string, bool) {
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(strings.ToLower(strings.TrimSpace(answer)))
	markerIndex := -1
	markerLength := 0
	for _, marker := range []string{"例如", "比如", "包括", "包含", "分别是", "分别为", "如"} {
		if index := strings.Index(compact, marker); index >= 0 && (markerIndex < 0 || index < markerIndex) {
			if marker == "如" && strings.HasPrefix(compact[index:], "如果") {
				continue
			}
			markerIndex = index
			markerLength = len(marker)
		}
	}
	if markerIndex < 0 {
		return "", nil, false
	}
	prefix := normalizeRuntimeKnowledgeQuery(compact[:markerIndex])
	tail := compact[markerIndex+markerLength:]
	if index := strings.IndexAny(tail, "。；;！？!?"); index >= 0 {
		tail = tail[:index]
	}
	tail = strings.NewReplacer("以及", "、", "或者", "、", "和", "、", "及", "、", "与", "、", "，", "、", ",", "、", "/", "、").Replace(tail)
	return prefix, strings.Split(tail, "、"), true
}

func knowledgeEvidenceEnumerationContainsMember(members []string, entity string) bool {
	for _, member := range members {
		member = strings.TrimSuffix(normalizeRuntimeKnowledgeQuery(member), "等")
		for _, suffix := range []string{"房型", "客房"} {
			member = strings.TrimSuffix(member, suffix)
		}
		if member == entity {
			return true
		}
	}
	return false
}

func knowledgeEvidenceEnumerationHasConflict(task knowledgeEvidenceJudgeTask, layer string, target string, predicate string, minimumScore float32) bool {
	for _, candidate := range task.Candidates {
		if candidate.Layer != layer || candidate.Hit.Score < minimumScore {
			continue
		}
		_, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		compact := normalizeRuntimeKnowledgeQuery(answer)
		if strings.Contains(compact, target) && strings.Contains(compact, predicate) && knowledgeEvidenceTextHasNegativeBoundary(compact) {
			return true
		}
	}
	return false
}

func deterministicKnowledgeEvidenceIntersectionSelection(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) (knowledgeEvidenceLayerSelection, bool) {
	query := normalizeRuntimeKnowledgeQuery(task.Query)
	if len(selectedCandidateIDs) < 2 || !knowledgeEvidenceQueryAsksIntersection(query) {
		return knowledgeEvidenceLayerSelection{}, false
	}
	queryObject := knowledgeEvidenceIntersectionObject(query)
	if queryObject == "" {
		return knowledgeEvidenceLayerSelection{}, false
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	type operand struct {
		label        string
		candidateIDs []string
		enumeration  knowledgeEvidenceEnumeration
	}
	operands := make([]operand, 0, 2)
	operandByLabel := make(map[string]int, 2)
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) || !hasStringKey(selected, candidate.CandidateID) {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if answer == "" {
			answer = strings.TrimSpace(candidate.Hit.Content)
		}
		if knowledgeEvidenceTextHasNegativeBoundary(question + " " + answer) {
			return knowledgeEvidenceLayerSelection{}, false
		}
		text := normalizeRuntimeKnowledgeQuery(question + " " + answer + " " + candidate.Hit.Title)
		if knowledgeEvidenceIntersectionObject(text) != queryObject {
			return knowledgeEvidenceLayerSelection{}, false
		}
		label := ""
		for _, entity := range task.Entities {
			value := normalizeRuntimeKnowledgeQuery(entity.Text)
			if len([]rune(value)) >= 2 && strings.Contains(query, value) && strings.Contains(text, value) && !knowledgeEvidenceContainsString([]string{"房间", "客房", "房型", "酒店", "门店"}, value) {
				if label != "" && label != value {
					label = ""
					break
				}
				label = value
			}
		}
		if label == "" {
			return knowledgeEvidenceLayerSelection{}, false
		}
		enumeration := parseKnowledgeEvidenceIntersectionEnumeration(answer, label)
		if enumeration.Completeness == knowledgeEvidenceEnumerationInvalid {
			return knowledgeEvidenceLayerSelection{}, false
		}
		if existingIndex, exists := operandByLabel[label]; exists {
			if !knowledgeEvidenceEnumerationsEquivalent(operands[existingIndex].enumeration, enumeration) {
				return knowledgeEvidenceLayerSelection{}, false
			}
			operands[existingIndex].candidateIDs = appendIfMissing(operands[existingIndex].candidateIDs, strings.TrimSpace(candidate.CandidateID))
			continue
		}
		operandByLabel[label] = len(operands)
		operands = append(operands, operand{
			label:        label,
			candidateIDs: []string{strings.TrimSpace(candidate.CandidateID)},
			enumeration:  enumeration,
		})
	}
	if len(operands) < 2 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	intersection := append([]string(nil), operands[0].enumeration.Members...)
	usedIDs := append([]string(nil), operands[0].candidateIDs...)
	labels := []string{operands[0].label}
	complete := operands[0].enumeration.Completeness == knowledgeEvidenceEnumerationComplete
	for _, item := range operands[1:] {
		labels = append(labels, item.label)
		for _, candidateID := range item.candidateIDs {
			usedIDs = appendIfMissing(usedIDs, candidateID)
		}
		complete = complete && item.enumeration.Completeness == knowledgeEvidenceEnumerationComplete
		memberSet := make(map[string]struct{}, len(item.enumeration.Members))
		for _, member := range item.enumeration.Members {
			memberSet[member] = struct{}{}
		}
		filtered := intersection[:0]
		for _, member := range intersection {
			if _, ok := memberSet[member]; ok {
				filtered = append(filtered, member)
			}
		}
		intersection = filtered
	}
	if len(labels) < 2 || len(intersection) == 0 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	decision := knowledgeEvidenceDecisionDirectCombined
	statement := "同时有" + strings.Join(labels, "和") + "的房型是" + strings.Join(intersection, "、") + "。"
	missingAspects := []string(nil)
	if !complete {
		decision = knowledgeEvidenceDecisionPartial
		statement = "当前资料能确认同时有" + strings.Join(labels, "和") + "的房型包括" + strings.Join(intersection, "、") + "。"
		missingAspects = []string{"完整房型范围"}
	}
	return knowledgeEvidenceLayerSelection{
		Decision:             decision,
		DecisionSource:       deterministicKnowledgeEvidenceFAQDecisionSource(layer),
		SelectedCandidateIDs: usedIDs,
		SupportedFacts: []knowledgeEvidenceFact{{
			FactID:         strings.TrimSpace(task.TaskID) + "F1",
			Aspect:         "scope",
			Statement:      statement,
			CriticalValues: append([]string(nil), intersection...),
		}},
		MissingAspects: missingAspects,
	}, true
}

func knowledgeEvidenceEnumerationsEquivalent(left knowledgeEvidenceEnumeration, right knowledgeEvidenceEnumeration) bool {
	if left.Completeness != right.Completeness || len(left.Members) != len(right.Members) {
		return false
	}
	for _, member := range left.Members {
		if !knowledgeEvidenceContainsString(right.Members, member) {
			return false
		}
	}
	return true
}

func knowledgeEvidenceQueryAsksIntersection(query string) bool {
	query = normalizeRuntimeKnowledgeQuery(query)
	return strings.Contains(query, "同时") || (strings.Contains(query, "既") && strings.Contains(query, "又"))
}

func knowledgeEvidenceIntersectionObject(text string) string {
	text = normalizeRuntimeKnowledgeQuery(text)
	if containsAny(text, []string{"会议室", "会议厅", "会场"}) {
		return "meeting_room"
	}
	if containsAny(text, []string{"房型", "客房", "房间"}) {
		return "room_type"
	}
	return ""
}

func hasStringKey(values map[string]struct{}, key string) bool {
	_, ok := values[strings.TrimSpace(key)]
	return ok
}

type knowledgeEvidenceEnumerationCompleteness string

const (
	knowledgeEvidenceEnumerationInvalid  knowledgeEvidenceEnumerationCompleteness = "invalid"
	knowledgeEvidenceEnumerationPartial  knowledgeEvidenceEnumerationCompleteness = "partial"
	knowledgeEvidenceEnumerationComplete knowledgeEvidenceEnumerationCompleteness = "complete"
)

type knowledgeEvidenceEnumeration struct {
	Members      []string
	Completeness knowledgeEvidenceEnumerationCompleteness
}

var knowledgeEvidenceRoomTypeCountPattern = regexp.MustCompile(`(?:共|合计)?([一二三四五六七八九十两0-9]+)种(?:房型)?`)
var knowledgeEvidenceRoomTypeCountSuffixPattern = regexp.MustCompile(`[一二三四五六七八九十两0-9]+种(?:房型)?$`)

var knowledgeEvidenceEnumerationEtcMarkers = []string{
	"等其他房型", "等其它房型", "等常见房型", "等更多房型", "等多个房型", "等若干房型", "等相关房型", "等房型", "等等",
}

func splitKnowledgeEvidenceCompleteEnumeration(answer string) ([]string, bool) {
	enumeration := parseKnowledgeEvidenceIntersectionEnumeration(answer, "")
	return enumeration.Members, enumeration.Completeness == knowledgeEvidenceEnumerationComplete
}

func parseKnowledgeEvidenceIntersectionEnumeration(answer string, label string) knowledgeEvidenceEnumeration {
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(strings.ToLower(strings.TrimSpace(answer)))
	label = normalizeRuntimeKnowledgeQuery(label)
	if compact == "" {
		return knowledgeEvidenceEnumeration{Completeness: knowledgeEvidenceEnumerationInvalid}
	}

	declaredCount, countStart, countEnd, hasDeclaredCount := knowledgeEvidenceDeclaredRoomTypeCount(compact)
	partial := knowledgeEvidenceEnumerationHasNonExhaustiveMarker(compact)
	rawMembers := ""
	explicitComplete := false

	markerIndex, markerLength := knowledgeEvidenceEnumerationListMarker(compact)
	if markerIndex >= 0 {
		rawMembers = compact[markerIndex+markerLength:]
		explicitComplete = true
	} else if exampleIndex, exampleLength := knowledgeEvidenceEnumerationExampleMarker(compact); exampleIndex >= 0 {
		rawMembers = compact[exampleIndex+exampleLength:]
		partial = true
	} else if hasDeclaredCount {
		explicitComplete = true
		afterCount := compact[countEnd:]
		if separator := strings.IndexAny(afterCount, "：:"); separator >= 0 {
			rawMembers = afterCount[separator+1:]
		} else {
			rawMembers = compact[:countStart]
			if boundary := strings.LastIndexAny(rawMembers, "。；;！？!?：:"); boundary >= 0 {
				rawMembers = rawMembers[boundary+1:]
			}
		}
	} else if label != "" {
		labelIndex := strings.LastIndex(compact, label)
		if labelIndex <= 0 {
			return knowledgeEvidenceEnumeration{Completeness: knowledgeEvidenceEnumerationInvalid}
		}
		rawMembers = compact[:labelIndex]
		if boundary := strings.LastIndexAny(rawMembers, "。；;！？!?：:"); boundary >= 0 {
			rawMembers = rawMembers[boundary+1:]
		}
		for {
			before := rawMembers
			for _, suffix := range []string{"均配备", "都配备", "配备", "配置", "设有", "带有", "都有", "均有", "带", "有", "的房型", "房型"} {
				rawMembers = strings.TrimSuffix(rawMembers, suffix)
			}
			if rawMembers == before {
				break
			}
		}
	} else {
		return knowledgeEvidenceEnumeration{Completeness: knowledgeEvidenceEnumerationInvalid}
	}

	members, trailingPartial, ok := splitKnowledgeEvidenceEnumerationMembers(rawMembers, label)
	if !ok || (hasDeclaredCount && len(members) != declaredCount) {
		return knowledgeEvidenceEnumeration{Completeness: knowledgeEvidenceEnumerationInvalid}
	}
	if partial || trailingPartial || !explicitComplete {
		return knowledgeEvidenceEnumeration{Members: members, Completeness: knowledgeEvidenceEnumerationPartial}
	}
	return knowledgeEvidenceEnumeration{Members: members, Completeness: knowledgeEvidenceEnumerationComplete}
}

func knowledgeEvidenceEnumerationListMarker(text string) (int, int) {
	markerIndex := -1
	markerLength := 0
	for _, marker := range []string{"分别是", "分别为", "包括", "包含", "如下"} {
		if index := strings.Index(text, marker); index >= 0 && (markerIndex < 0 || index < markerIndex) {
			markerIndex = index
			markerLength = len(marker)
		}
	}
	return markerIndex, markerLength
}

func knowledgeEvidenceEnumerationHasNonExhaustiveMarker(text string) bool {
	if containsAny(text, []string{"部分", "例如", "比如", "诸如", "但不限于"}) || containsAny(text, knowledgeEvidenceEnumerationEtcMarkers) {
		return true
	}
	trimmed := strings.TrimRight(text, "。；;！？!?，,")
	return strings.HasSuffix(trimmed, "等")
}

func knowledgeEvidenceEnumerationExampleMarker(text string) (int, int) {
	for _, marker := range []string{"例如", "比如", "诸如"} {
		if index := strings.LastIndex(text, marker); index >= 0 {
			return index, len(marker)
		}
	}
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], "如")
		if index < 0 {
			break
		}
		index += offset
		previousIsSeparator := index == 0
		if index > 0 {
			prefix := []rune(text[:index])
			previousIsSeparator = strings.ContainsRune("，,：:；;。", prefix[len(prefix)-1])
		}
		if previousIsSeparator && !strings.HasPrefix(text[index:], "如下") && !strings.HasPrefix(text[index:], "如果") {
			return index, len("如")
		}
		offset = index + len("如")
	}
	return -1, 0
}

func knowledgeEvidenceDeclaredRoomTypeCount(text string) (int, int, int, bool) {
	match := knowledgeEvidenceRoomTypeCountPattern.FindStringSubmatchIndex(text)
	if len(match) < 4 || match[2] < 0 || match[3] < 0 {
		return 0, 0, 0, false
	}
	count, ok := parseKnowledgeEvidenceEnumerationCount(text[match[2]:match[3]])
	if !ok || count <= 0 {
		return 0, 0, 0, false
	}
	return count, match[0], match[1], true
}

func parseKnowledgeEvidenceEnumerationCount(raw string) (int, bool) {
	if value, err := strconv.Atoi(raw); err == nil {
		return value, value > 0
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	if raw == "十" {
		return 10, true
	}
	runes := []rune(raw)
	if len(runes) == 2 && runes[0] == '十' {
		value, ok := digits[runes[1]]
		return 10 + value, ok
	}
	if len(runes) >= 2 && runes[1] == '十' {
		tens, ok := digits[runes[0]]
		if !ok || tens == 0 {
			return 0, false
		}
		ones := 0
		if len(runes) == 3 {
			var exists bool
			ones, exists = digits[runes[2]]
			if !exists {
				return 0, false
			}
		} else if len(runes) != 2 {
			return 0, false
		}
		return tens*10 + ones, true
	}
	if len(runes) == 1 {
		value, ok := digits[runes[0]]
		return value, ok && value > 0
	}
	return 0, false
}

func splitKnowledgeEvidenceEnumerationMembers(raw string, label string) ([]string, bool, bool) {
	raw = strings.TrimSpace(strings.Trim(raw, "：:，,"))
	if index := strings.IndexAny(raw, "。；;！？!?"); index >= 0 {
		raw = raw[:index]
	}
	raw, trailingPartial := trimKnowledgeEvidenceEnumerationNonExhaustiveMarkers(raw)
	raw = strings.NewReplacer("以及", "、", "和", "、", "及", "、", "与", "、", "，", "、", ",", "、", "/", "、").Replace(raw)
	ret := make([]string, 0, 4)
	for index, rawMember := range strings.Split(raw, "、") {
		member := strings.TrimSpace(normalizeRuntimeKnowledgeQuery(rawMember))
		if index == 0 {
			member = trimKnowledgeEvidenceEnumerationLead(member)
		}
		member = knowledgeEvidenceRoomTypeCountSuffixPattern.ReplaceAllString(member, "")
		member = strings.TrimSuffix(member, "房型")
		if member == "" || strings.HasSuffix(member, "等") || len([]rune(member)) > 10 ||
			containsAny(member, []string{"酒店", "门店", "部分房型", "全部房型", "所有房型", "房间"}) ||
			(label != "" && strings.Contains(member, label)) {
			return nil, trailingPartial, false
		}
		ret = appendIfMissing(ret, member)
	}
	return ret, trailingPartial, len(ret) >= 2
}

func trimKnowledgeEvidenceEnumerationLead(member string) string {
	for {
		before := member
		for _, prefix := range []string{
			"目前我们酒店的", "目前我们酒店", "目前我们店的", "目前我们店", "目前我们的", "目前我们", "目前本酒店的", "目前本酒店", "目前本店的", "目前本店",
			"我们酒店的", "我们酒店", "我们店的", "我们店", "我们的", "我们", "咱们酒店的", "咱们酒店", "咱们店的", "咱们店", "咱们的", "咱们",
			"本酒店的", "本酒店", "本门店的", "本门店", "本店的", "本店", "酒店的", "酒店", "门店的", "门店", "现有的", "现有", "目前的", "目前",
		} {
			member = strings.TrimPrefix(member, prefix)
		}
		member = strings.TrimLeft(member, "：:，,")
		if member == before {
			return member
		}
	}
}

func trimKnowledgeEvidenceEnumerationNonExhaustiveMarkers(raw string) (string, bool) {
	partial := false
	if index := strings.Index(raw, "但不限于"); index >= 0 {
		partial = true
		before := strings.Trim(strings.TrimSpace(raw[:index]), "：:，,")
		after := strings.Trim(strings.TrimSpace(raw[index+len("但不限于"):]), "：:，,")
		if before == "" {
			raw = after
		} else {
			raw = before
		}
	}
	for _, marker := range knowledgeEvidenceEnumerationEtcMarkers {
		if index := strings.Index(raw, marker); index >= 0 {
			raw = raw[:index]
			partial = true
			break
		}
	}
	raw = strings.Trim(strings.TrimSpace(raw), "：:，,")
	if strings.HasSuffix(raw, "等") {
		raw = strings.TrimSuffix(raw, "等")
		partial = true
	}
	return strings.Trim(strings.TrimSpace(raw), "：:，,"), partial
}

func knowledgeEvidenceTextContainsAll(text string, required []string) bool {
	text = normalizeRuntimeKnowledgeQuery(text)
	if text == "" {
		return false
	}
	for _, value := range required {
		if value == "" || !strings.Contains(text, value) {
			return false
		}
	}
	return true
}

func normalizeKnowledgeEvidenceJudgeResponseJSON(raw string) (string, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return "", fmt.Errorf("knowledge judge response is empty")
	}

	if strings.HasPrefix(normalized, `"`) {
		var unwrapped string
		decoder := json.NewDecoder(strings.NewReader(normalized))
		if err := decoder.Decode(&unwrapped); err != nil {
			return "", fmt.Errorf("unwrap string-encoded knowledge judge response: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return "", fmt.Errorf("string-encoded knowledge judge response contains trailing content")
		}
		normalized = strings.TrimSpace(unwrapped)
		if normalized == "" {
			return "", fmt.Errorf("string-encoded knowledge judge response is empty")
		}
	}

	unwrapped, err := unwrapKnowledgeEvidenceJudgeJSONFence(normalized)
	if err != nil {
		return "", err
	}
	return unwrapped, nil
}

func unwrapKnowledgeEvidenceJudgeJSONFence(raw string) (string, error) {
	if !strings.HasPrefix(raw, "```") {
		return raw, nil
	}
	lines := strings.Split(raw, "\n")
	if len(lines) < 3 {
		return "", fmt.Errorf("knowledge judge response contains an incomplete JSON code block")
	}
	header := strings.TrimSpace(lines[0])
	if header != "```" && !strings.EqualFold(header, "```json") {
		return "", fmt.Errorf("knowledge judge response contains an unsupported code block")
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "```" {
		return "", fmt.Errorf("knowledge judge JSON code block contains trailing content")
	}
	body := strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	if body == "" {
		return "", fmt.Errorf("knowledge judge JSON code block is empty")
	}
	return body, nil
}

func normalizeKnowledgeEvidenceFacts(taskID string, layer string, facts []knowledgeEvidenceFact, seenFactIDs map[string]struct{}) ([]knowledgeEvidenceFact, error) {
	ret := make([]knowledgeEvidenceFact, 0, len(facts))
	for _, fact := range facts {
		factID := strings.TrimSpace(fact.FactID)
		aspect := strings.TrimSpace(fact.Aspect)
		statement := strings.TrimSpace(fact.Statement)
		if !isKnowledgeEvidenceFactAspect(aspect) {
			aspect = "other"
		}
		if factID == "" || statement == "" {
			return nil, fmt.Errorf("invalid supported fact for task %s layer %s", taskID, layer)
		}
		if _, exists := seenFactIDs[factID]; exists {
			return nil, fmt.Errorf("duplicate supported fact id %s for task %s", factID, taskID)
		}
		seenFactIDs[factID] = struct{}{}
		criticalValues := make([]string, 0, len(fact.CriticalValues))
		seenValues := make(map[string]struct{}, len(fact.CriticalValues))
		for _, rawValue := range fact.CriticalValues {
			value := strings.TrimSpace(rawValue)
			if value == "" {
				return nil, fmt.Errorf("empty critical value for fact %s", factID)
			}
			if _, exists := seenValues[value]; exists {
				continue
			}
			seenValues[value] = struct{}{}
			criticalValues = append(criticalValues, value)
		}
		ret = append(ret, knowledgeEvidenceFact{
			FactID:         factID,
			Aspect:         aspect,
			Statement:      statement,
			CriticalValues: criticalValues,
		})
	}
	return ret, nil
}

func canonicalizeKnowledgeEvidenceFacts(facts []knowledgeEvidenceFact) []knowledgeEvidenceFact {
	// A FactID is part of the Judge/Generate coverage contract. Two facts may
	// deliberately share the same short statement while their subjects live in
	// different FAQ questions, so this stage must never collapse FactIDs. The
	// Generate prompt groups identical or containing statements for presentation
	// while retaining every FactID and its own critical values.
	return append([]knowledgeEvidenceFact(nil), facts...)
}

func knowledgeEvidenceFactsSemanticallyOverlap(left knowledgeEvidenceFact, right knowledgeEvidenceFact) bool {
	if strings.TrimSpace(left.Aspect) == "" || strings.TrimSpace(left.Aspect) != strings.TrimSpace(right.Aspect) {
		return false
	}
	leftText := normalizeRuntimeKnowledgeQuery(left.Statement)
	rightText := normalizeRuntimeKnowledgeQuery(right.Statement)
	if leftText == "" || rightText == "" {
		return false
	}
	if knowledgeEvidenceTextHasNegativeBoundary(leftText) != knowledgeEvidenceTextHasNegativeBoundary(rightText) ||
		knowledgeEvidenceCriticalValuesConflict(left.CriticalValues, right.CriticalValues) {
		return false
	}
	if (strings.Contains(leftText, "免费") && strings.Contains(rightText, "收费")) || (strings.Contains(leftText, "收费") && strings.Contains(rightText, "免费")) {
		return false
	}
	if leftText == rightText {
		return true
	}
	// Similar wording and a shared value such as the same pickup location do not
	// prove that two facts have the same subject. Keep non-identical statements
	// separate so facts about different supplies, room types or scopes cannot be
	// collapsed before Generate sees them.
	return false
}

func knowledgeEvidenceFactContextSignature(text string) string {
	parts := make([]string, 0, 3)
	if scope := knowledgeEvidenceConfigurationScope(text); scope != "" {
		parts = append(parts, "scope="+scope)
	}
	if index := strings.Index(text, "房型"); index > 0 {
		prefix := []rune(text[:index])
		if len(prefix) > 6 {
			prefix = prefix[len(prefix)-6:]
		}
		parts = append(parts, "room_type="+string(prefix))
	}
	for _, condition := range []string{"工作日", "周末", "节假日", "平日", "每天", "每日", "夜间", "白天", "入住当天", "退房当天"} {
		if strings.Contains(text, condition) {
			parts = append(parts, "condition="+condition)
		}
	}
	return strings.Join(parts, "|")
}

func knowledgeEvidenceCriticalValueSetsEqual(left []string, right []string) bool {
	leftValues := make(map[string]struct{}, len(left))
	for _, value := range left {
		if value = normalizeCriticalValueText(value); value != "" {
			leftValues[value] = struct{}{}
		}
	}
	rightValues := make(map[string]struct{}, len(right))
	for _, value := range right {
		if value = normalizeCriticalValueText(value); value != "" {
			rightValues[value] = struct{}{}
		}
	}
	if len(leftValues) != len(rightValues) {
		return false
	}
	for value := range leftValues {
		if _, ok := rightValues[value]; !ok {
			return false
		}
	}
	return true
}

func knowledgeEvidenceCriticalValuesConflict(left []string, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	leftValues := make(map[string]struct{}, len(left))
	for _, value := range left {
		value = normalizeCriticalValueText(value)
		if value != "" {
			leftValues[value] = struct{}{}
		}
	}
	if len(leftValues) == 0 {
		return false
	}
	rightValues := make(map[string]struct{}, len(right))
	for _, value := range right {
		value = normalizeCriticalValueText(value)
		if value != "" {
			rightValues[value] = struct{}{}
		}
	}
	if len(rightValues) == 0 {
		return false
	}
	leftSubset := true
	for value := range leftValues {
		if _, ok := rightValues[value]; !ok {
			leftSubset = false
			break
		}
	}
	rightSubset := true
	for value := range rightValues {
		if _, ok := leftValues[value]; !ok {
			rightSubset = false
			break
		}
	}
	return !leftSubset && !rightSubset
}

func normalizeKnowledgeEvidenceMissingAspects(taskID string, layer string, aspects []string) ([]string, error) {
	ret := make([]string, 0, len(aspects))
	seen := make(map[string]struct{}, len(aspects))
	for _, rawAspect := range aspects {
		aspect := strings.TrimSpace(rawAspect)
		if aspect == "" {
			return nil, fmt.Errorf("empty missing aspect for task %s layer %s", taskID, layer)
		}
		if _, exists := seen[aspect]; exists {
			return nil, fmt.Errorf("duplicate missing aspect %q for task %s layer %s", aspect, taskID, layer)
		}
		seen[aspect] = struct{}{}
		ret = append(ret, aspect)
	}
	return ret, nil
}

func isKnowledgeEvidenceFactAspect(aspect string) bool {
	switch aspect {
	case "existence", "quantity", "price", "time", "location", "method", "scope", "condition", "other":
		return true
	default:
		return false
	}
}

func reconcileSelectedFAQGuidanceFacts(
	taskID string,
	layer string,
	selection knowledgeEvidenceLayerSelection,
	candidates map[string]knowledgeEvidenceJudgeCandidate,
) knowledgeEvidenceLayerSelection {
	return reconcileSelectedFAQGuidanceFactsForQuery(taskID, "", layer, selection, candidates)
}

func reconcileSelectedFAQGuidanceFactsForQuery(
	taskID string,
	query string,
	layer string,
	selection knowledgeEvidenceLayerSelection,
	candidates map[string]knowledgeEvidenceJudgeCandidate,
) knowledgeEvidenceLayerSelection {
	return reconcileSelectedFAQGuidanceFactsForTask(
		knowledgeEvidenceJudgeTask{TaskID: taskID, Query: query},
		layer,
		selection,
		candidates,
	)
}

func reconcileSelectedFAQGuidanceFactsForTask(
	task knowledgeEvidenceJudgeTask,
	layer string,
	selection knowledgeEvidenceLayerSelection,
	candidates map[string]knowledgeEvidenceJudgeCandidate,
) knowledgeEvidenceLayerSelection {
	if strings.TrimSpace(selection.DecisionSource) == "model" {
		return selection
	}
	if selection.Decision != knowledgeEvidenceDecisionDirectSingle && selection.Decision != knowledgeEvidenceDecisionDirectCombined {
		return selection
	}
	if intersection, ok := deterministicKnowledgeEvidenceIntersectionSelection(task, layer, selection.SelectedCandidateIDs); ok {
		if strings.TrimSpace(selection.DecisionSource) != "" {
			intersection.DecisionSource = selection.DecisionSource
		}
		intersection.SupportedFacts = canonicalizeKnowledgeEvidenceFacts(sanitizeKnowledgeEvidenceFacts(intersection.SupportedFacts))
		return intersection
	}
	taskID := task.TaskID
	seenFactIDs := make(map[string]struct{}, len(selection.SupportedFacts))
	for _, fact := range selection.SupportedFacts {
		seenFactIDs[strings.TrimSpace(fact.FactID)] = struct{}{}
	}
	for _, candidateID := range selection.SelectedCandidateIDs {
		candidate, ok := candidates[strings.TrimSpace(candidateID)]
		if !ok || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		allowedTimeClauses, filterTimeClauses := knowledgeEvidenceAllowedTimeClauseSet(task, question, answer)
		for _, clause := range splitKnowledgeEvidenceAnswerClauses(answer) {
			if !knowledgeEvidenceAnswerClauseIsGroundedFact(clause) {
				continue
			}
			for _, classified := range knowledgeEvidenceAnswerClauseAspects(clause) {
				aspect := classified.Aspect
				if aspect == "time" && filterTimeClauses {
					if _, ok := allowedTimeClauses[normalizeRuntimeKnowledgeQuery(clause)]; !ok {
						continue
					}
				}
				criticalValue := classified.CriticalValue
				criticalValues := knowledgeEvidenceAnswerClauseCriticalValues(clause, criticalValue)
				criticalValues = appendKnowledgeEvidenceCriticalValues(
					criticalValues,
					knowledgeEvidenceFAQClauseConcreteValues(task, aspect, clause, selection.SupportedFacts),
				)
				if factIndex := knowledgeEvidenceAnswerClauseFactIndex(clause, aspect, criticalValue, criticalValues, selection.SupportedFacts); factIndex >= 0 {
					selection.SupportedFacts[factIndex].CriticalValues = appendKnowledgeEvidenceCriticalValues(
						selection.SupportedFacts[factIndex].CriticalValues,
						criticalValues,
					)
					continue
				}
				if !knowledgeEvidenceFAQClauseRequiredForTask(task, aspect, clause, criticalValues, selection.SupportedFacts) {
					continue
				}
				factID := nextKnowledgeEvidenceFactID(taskID, seenFactIDs)
				seenFactIDs[factID] = struct{}{}
				statement := strings.TrimSpace(clause)
				if !strings.HasSuffix(statement, "。") && !strings.HasSuffix(statement, "！") && !strings.HasSuffix(statement, "？") {
					statement += "。"
				}
				selection.SupportedFacts = append(selection.SupportedFacts, knowledgeEvidenceFact{
					FactID:         factID,
					Aspect:         aspect,
					Statement:      statement,
					CriticalValues: criticalValues,
				})
			}
		}
	}
	selection.SupportedFacts = finalizeKnowledgeEvidenceFactsForTask(task, selection.SupportedFacts)
	return selection
}

func knowledgeEvidenceAllowedTimeClauseSet(task knowledgeEvidenceJudgeTask, question string, answer string) (map[string]struct{}, bool) {
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	if len(requiredSubjects) != 1 {
		return nil, false
	}
	subject := requiredSubjects[0]
	if !strings.Contains(normalizeKnowledgeEvidenceSubjectForMatch(question), subject) {
		return nil, false
	}
	allowed := make(map[string]struct{})
	for _, clause := range knowledgeEvidenceTimeClausesForSubject(question, answer, subject) {
		if normalized := normalizeRuntimeKnowledgeQuery(clause); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	return allowed, true
}

func knowledgeEvidenceFAQClauseRequiredForTask(task knowledgeEvidenceJudgeTask, aspect string, clause string, criticalValues []string, facts []knowledgeEvidenceFact) bool {
	if knowledgeEvidenceFAQClauseAddsMissingConfiguration(task, clause, facts) {
		return true
	}
	required := requiredKnowledgeEvidenceAspects(task)
	if requiredKnowledgeEvidenceAspect(required, aspect) {
		if !knowledgeEvidenceFactsCoverRequiredAspect(task, facts, aspect) {
			return true
		}
		if knowledgeEvidenceFAQClauseAddsConcreteValue(aspect, clause, criticalValues, facts) {
			return true
		}
	}
	if knowledgeEvidenceTextHasNegativeBoundary(clause) {
		if requiredKnowledgeEvidenceAspect(required, "existence") && knowledgeEvidenceNegativeFactAnswersTask(task, knowledgeEvidenceFact{Aspect: "existence", Statement: clause}) {
			return true
		}
		if requiredKnowledgeEvidenceAspect(required, "method") && knowledgeEvidenceMethodBoundaryRelevantToTask(task, knowledgeEvidenceFact{Aspect: "existence", Statement: clause}, facts) {
			return true
		}
	}
	if requiredKnowledgeEvidenceAspect(required, "price") && (knowledgeEvidenceQueryAsksComparison(task.Query) || knowledgeEvidenceQueryAsksPriceBoundary(task.Query)) {
		compact := normalizeRuntimeKnowledgeQuery(clause)
		if (aspect == "condition" || aspect == "scope") && containsAny(compact, []string{"平台", "权益", "不同", "调整", "情况", "为准", "而定", "取决于"}) {
			return true
		}
		return aspect == "method" && containsAny(compact, []string{"对比", "比较", "选择", "联系"})
	}
	return false
}

func knowledgeEvidenceFAQClauseAddsMissingConfiguration(task knowledgeEvidenceJudgeTask, clause string, facts []knowledgeEvidenceFact) bool {
	requested := knowledgeEvidenceConfigurationFields(task.Query)
	if len(requested) == 0 {
		return false
	}
	existing := make(map[string][]string, len(requested))
	for _, fact := range facts {
		for field, values := range knowledgeEvidenceConfigurationValues(fact.Statement) {
			for _, value := range values {
				existing[field] = appendIfMissing(existing[field], normalizeRuntimeKnowledgeQuery(value))
			}
		}
	}
	clauseValues := knowledgeEvidenceConfigurationValues(clause)
	for _, field := range requested {
		for _, value := range clauseValues[field] {
			if !knowledgeEvidenceContainsString(existing[field], normalizeRuntimeKnowledgeQuery(value)) {
				return true
			}
		}
	}
	return false
}

func knowledgeEvidenceFAQClauseAddsConcreteValue(aspect string, clause string, criticalValues []string, facts []knowledgeEvidenceFact) bool {
	values := sanitizeKnowledgeEvidenceCriticalValuesForStatement(criticalValues, clause)
	for _, value := range values {
		if !knowledgeEvidenceFactsContainConcreteValue(facts, value) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceFAQClauseConcreteValues(task knowledgeEvidenceJudgeTask, aspect string, clause string, facts []knowledgeEvidenceFact) []string {
	values := make([]string, 0, 4)
	compact := normalizeRuntimeKnowledgeQuery(clause)
	if aspect == "method" {
		for _, channel := range knowledgeEvidenceRelevantMethodChannels(task, clause, facts) {
			values = appendIfMissing(values, canonicalKnowledgeEvidenceCriticalValue(channel))
		}
	}
	if aspect == "location" {
		for _, slot := range []struct {
			value   string
			markers []string
		}{
			{value: "酒店名", markers: []string{"酒店名称", "门店名称", "酒店名", "门店名"}},
			{value: "楼层", markers: []string{"对应楼层", "所在楼层", "楼层"}},
			{value: "房号", markers: []string{"房间号", "门牌号", "房号"}},
		} {
			for _, marker := range slot.markers {
				if strings.Contains(compact, normalizeRuntimeKnowledgeQuery(marker)) {
					values = appendIfMissing(values, slot.value)
					break
				}
			}
		}
	}
	return values
}

func knowledgeEvidenceFactsContainConcreteValue(facts []knowledgeEvidenceFact, value string) bool {
	value = canonicalKnowledgeEvidenceCriticalValue(strings.TrimSpace(value))
	if value == "" {
		return true
	}
	for _, fact := range facts {
		if containsCriticalValue(fact.Statement, value) {
			return true
		}
		for _, existing := range sanitizeKnowledgeEvidenceCriticalValuesForStatement(fact.CriticalValues, fact.Statement) {
			if normalizeCriticalValueText(existing) == normalizeCriticalValueText(value) {
				return true
			}
		}
	}
	return false
}

func knowledgeEvidenceAnswerClauseIsGroundedFact(clause string) bool {
	compact := normalizeRuntimeKnowledgeQuery(clause)
	if len([]rune(compact)) < 3 {
		return false
	}
	if knowledgeEvidenceFactIsMarketingFiller(clause) {
		return false
	}
	for _, filler := range []string{"好的", "好哒", "收到", "谢谢", "谢谢您", "感谢", "不客气", "您好", "你好", "抱歉", "不好意思", "没问题", "希望能帮到您", "祝您愉快"} {
		if compact == normalizeRuntimeKnowledgeQuery(filler) {
			return false
		}
	}
	compactLength := len([]rune(compact))
	for _, prefix := range []string{"感谢", "谢谢", "祝您", "很高兴为您", "希望能帮到"} {
		if strings.HasPrefix(compact, prefix) && compactLength <= 16 {
			return false
		}
	}
	for _, prefix := range []string{"另外", "同时", "此外", "然后", "以及", "并且"} {
		if compact == prefix {
			return false
		}
	}
	return true
}

func knowledgeEvidenceTextHasUncertaintyBoundary(text string) bool {
	compact := normalizeRuntimeKnowledgeQuery(text)
	return containsAny(compact, []string{
		"不确定", "暂不确定", "无法确认", "不能确认", "待确认", "需要确认", "需确认",
		"资料未写", "资料没写", "未写明", "没有写明", "不清楚", "未知",
		"可能", "也许", "不一定", "视情况", "视当天情况", "具体情况为准",
	})
}

type knowledgeEvidenceClauseAspect struct {
	Aspect        string
	CriticalValue string
}

func knowledgeEvidenceAnswerClauseAspects(clause string) []knowledgeEvidenceClauseAspect {
	ret := make([]knowledgeEvidenceClauseAspect, 0, 3)
	appendAspect := func(aspect string, criticalValue string) {
		if aspect == "" {
			return
		}
		for _, existing := range ret {
			if existing.Aspect == aspect {
				return
			}
		}
		ret = append(ret, knowledgeEvidenceClauseAspect{Aspect: aspect, CriticalValue: criticalValue})
	}
	if aspect, criticalValue := knowledgeEvidenceGuidanceRequirement(clause); criticalValue != "" {
		appendAspect(aspect, criticalValue)
	}
	compact := normalizeRuntimeKnowledgeQuery(clause)
	if knowledgeEvidenceNegativeBoundaryAnchor(clause) != "" {
		appendAspect("existence", "")
	}
	if knowledgeEvidenceIndividualTimePattern.MatchString(clause) || strings.Contains(compact, "时间") || strings.Contains(compact, "几点") {
		appendAspect("time", "")
	}
	if containsAny(compact, []string{"免费", "收费", "价格", "费用", "金额"}) {
		appendAspect("price", "")
	}
	if knowledgeEvidenceStrictQuantityPattern.MatchString(compact) {
		appendAspect("quantity", "")
	}
	if knowledgeEvidenceTextHasLocationCue(clause) {
		appendAspect("location", "")
	}
	if knowledgeEvidenceTextHasMethodCue(clause) {
		appendAspect("method", "")
	}
	if containsAny(compact, []string{"不同平台", "平台权益", "实时调整", "自动调整"}) {
		appendAspect("condition", "")
	}
	if containsAny(compact, []string{"仅限", "适用", "范围", "全部", "均可"}) {
		appendAspect("scope", "")
	}
	if containsAny(compact, []string{"如果", "需要", "取决于", "为准", "而定"}) {
		appendAspect("condition", "")
	}
	if knowledgeEvidenceClauseHasPositiveExistencePredicate(clause) {
		appendAspect("existence", "")
	}
	if len(ret) == 0 {
		appendAspect("other", "")
	}
	return ret

}

func knowledgeEvidenceClauseHasPositiveExistencePredicate(clause string) bool {
	compact := normalizeRuntimeKnowledgeQuery(clause)
	if compact == "" || knowledgeEvidenceTextHasNegativeBoundary(compact) || knowledgeEvidenceTextHasUncertaintyBoundary(compact) {
		return false
	}
	if containsAny(compact, []string{"配备", "配有", "设有", "提供", "存在", "供应", "装有", "带有"}) {
		return true
	}
	plain := strings.NewReplacer(
		"所有", "", "如有需要", "", "若有需要", "", "如果有需要", "", "有需要", "",
		"如有问题", "", "若有问题", "", "如果有问题", "", "有问题", "",
		"如有疑问", "", "若有疑问", "", "如果有疑问", "", "有疑问", "",
	).Replace(compact)
	return strings.Contains(plain, "有")
}

func knowledgeEvidenceAnswerClauseAspect(clause string) (string, string) {
	aspects := knowledgeEvidenceAnswerClauseAspects(clause)
	if len(aspects) == 0 {
		return "other", ""
	}
	return aspects[0].Aspect, aspects[0].CriticalValue
}

func knowledgeEvidenceAnswerClauseFactIndex(clause string, aspect string, criticalValue string, criticalValues []string, facts []knowledgeEvidenceFact) int {
	if criticalValue != "" {
		if index := knowledgeEvidenceGuidanceFactIndex(clause, aspect, criticalValue, criticalValues, facts); index >= 0 {
			return index
		}
	}
	clauseText := normalizeRuntimeKnowledgeQuery(clause)
	clauseNegative := knowledgeEvidenceTextHasNegativeBoundary(clauseText)
	bestIndex := -1
	bestSimilarity := 0.0
	for index := range facts {
		if strings.TrimSpace(facts[index].Aspect) != strings.TrimSpace(aspect) {
			continue
		}
		statement := normalizeRuntimeKnowledgeQuery(facts[index].Statement)
		if statement == "" || clauseNegative != knowledgeEvidenceTextHasNegativeBoundary(statement) {
			continue
		}
		if strings.Contains(statement, clauseText) || strings.Contains(clauseText, statement) {
			return index
		}
		similarity := knowledgeEvidenceTextNGramSimilarity(clauseText, statement)
		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestIndex = index
		}
	}
	if bestSimilarity >= 0.58 {
		return bestIndex
	}
	return -1
}

func knowledgeEvidenceAnswerClauseCriticalValues(clause string, criticalValue string) []string {
	values := make([]string, 0, 4)
	if criticalValue != "" {
		values = appendKnowledgeEvidenceCriticalValues(values, knowledgeEvidenceGuidanceCriticalValues(clause, criticalValue))
	}
	if anchor := knowledgeEvidenceNegativeBoundaryAnchor(clause); anchor != "" {
		values = appendIfMissing(values, anchor)
	}
	for _, match := range knowledgeEvidenceGuidanceNumberPattern.FindAllString(clause, -1) {
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	rangeTimeMatches := knowledgeEvidenceAnswerTimePattern.FindAllString(clause, -1)
	timeMatches := append([]string(nil), rangeTimeMatches...)
	for _, match := range rangeTimeMatches {
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	for _, match := range knowledgeEvidenceIndividualTimePattern.FindAllString(clause, -1) {
		timeMatches = appendIfMissing(timeMatches, match)
		if knowledgeEvidenceNumberIsPartOfTime(match, rangeTimeMatches) {
			continue
		}
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	for _, matchIndex := range knowledgeEvidenceAnswerNumberPattern.FindAllStringIndex(clause, -1) {
		match := clause[matchIndex[0]:matchIndex[1]]
		if knowledgeEvidenceNumberIsPartOfTime(match, timeMatches) {
			continue
		}
		if knowledgeEvidenceCriticalValueIsBareSequence(match) && knowledgeEvidenceNumberIsListMarker(clause, matchIndex[0], matchIndex[1]) {
			continue
		}
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	for _, match := range knowledgeEvidenceAnswerChineseQuantityPattern.FindAllString(clause, -1) {
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	if anchor := knowledgeEvidenceLocationAnchor(clause); anchor != "" {
		values = appendIfMissing(values, anchor)
	}
	for _, value := range knowledgeEvidencePriceCriticalValues(clause) {
		values = appendIfMissing(values, value)
	}
	for _, fieldValues := range knowledgeEvidenceConfigurationValues(clause) {
		for _, value := range fieldValues {
			values = appendIfMissing(values, value)
		}
	}
	return values
}

func knowledgeEvidenceNumberIsListMarker(clause string, start int, end int) bool {
	if start < 0 || end <= start || end > len(clause) {
		return false
	}
	prefix := strings.TrimSpace(clause[:start])
	prefix = strings.TrimSpace(strings.Trim(prefix, "-*•"))
	if prefix != "" {
		return false
	}
	suffix := strings.TrimLeft(clause[end:], " \t")
	return strings.HasPrefix(suffix, ".") || strings.HasPrefix(suffix, "、") || strings.HasPrefix(suffix, ")") ||
		strings.HasPrefix(suffix, "）") || strings.HasPrefix(suffix, ":") || strings.HasPrefix(suffix, "：")
}

func knowledgeEvidenceNumberIsPartOfTime(number string, timeMatches []string) bool {
	number = strings.TrimSpace(number)
	if number == "" {
		return false
	}
	for _, timeMatch := range timeMatches {
		if strings.Contains(strings.TrimSpace(timeMatch), number) {
			return true
		}
	}
	return false
}

func knowledgeEvidenceTextHasNegativeBoundary(text string) bool {
	compact := normalizeRuntimeKnowledgeQuery(text)
	return containsAny(compact, []string{
		"不免费", "并非免费", "并不是免费", "不是免费",
		"并不是", "并非", "没有", "不是", "不能", "不会", "无法", "不可", "不含", "不提供", "未提供", "不供应", "未供应",
		"不配备", "不配有", "未配备", "未配有", "不支持", "不需要", "无需", "不用", "暂不",
	})
}

func knowledgeEvidenceNegativeBoundaryAnchor(clause string) string {
	for _, marker := range []string{
		"并不是", "不提供", "未提供", "不供应", "未供应", "不配备", "不配有", "未配备", "未配有",
		"不支持", "不需要", "没有", "并非", "不是", "不能", "不会", "无法", "不可", "不含", "无需", "暂不",
	} {
		index := strings.Index(clause, marker)
		if index < 0 {
			continue
		}
		anchor := strings.TrimSpace(clause[index+len(marker):])
		for _, connector := range []string{"但是", "不过", "而是", "但"} {
			if connectorIndex := strings.Index(anchor, connector); connectorIndex >= 0 {
				anchor = strings.TrimSpace(anchor[:connectorIndex])
			}
		}
		anchor = strings.Trim(anchor, " ，,。；;！!？?：:")
		runes := []rune(anchor)
		if len(runes) > 20 {
			anchor = string(runes[:20])
		}
		if len([]rune(normalizeRuntimeKnowledgeQuery(anchor))) >= 2 {
			return anchor
		}
	}
	return ""
}

func knowledgeEvidenceLocationAnchor(clause string) string {
	for _, marker := range []string{"地址为", "地址是", "地址：", "地址:", "位于"} {
		index := strings.Index(clause, marker)
		if index < 0 {
			continue
		}
		anchor := strings.Trim(strings.TrimSpace(clause[index+len(marker):]), " ，,。；;！!？?")
		length := len([]rune(normalizeRuntimeKnowledgeQuery(anchor)))
		if length >= 3 && length <= 48 {
			return anchor
		}
	}
	return ""
}

func splitKnowledgeEvidenceAnswerClauses(answer string) []string {
	parts := strings.FieldsFunc(strings.TrimSpace(answer), func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '！', '!', '？', '?', '；', ';', '，', ',':
			return true
		default:
			return false
		}
	})
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len([]rune(normalizeRuntimeKnowledgeQuery(part))) < 3 {
			continue
		}
		ret = append(ret, part)
	}
	return ret
}

func splitKnowledgeEvidenceTimeClauses(answer string) []string {
	parts := strings.FieldsFunc(strings.TrimSpace(answer), func(r rune) bool {
		switch r {
		case '\n', '\r', '。', '！', '!', '？', '?', '；', ';', '，', ',':
			return true
		default:
			return false
		}
	})
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ret = append(ret, part)
	}
	return ret
}

func knowledgeEvidenceGuidanceRequirement(clause string) (string, string) {
	compact := normalizeRuntimeKnowledgeQuery(clause)
	for _, cue := range []string{"对比", "比较", "联系", "回复", "选择"} {
		if strings.Contains(compact, cue) {
			return "method", cue
		}
	}
	if strings.Contains(compact, "建议") {
		return "method", "建议"
	}
	for _, cue := range []string{"取决于", "具体情况", "当天情况", "实际情况"} {
		if strings.Contains(compact, cue) {
			return "condition", cue
		}
	}
	if strings.Contains(compact, "为准") {
		return "condition", "为准"
	}
	if strings.Contains(compact, "而定") {
		return "condition", "而定"
	}
	return "", ""
}

func knowledgeEvidenceGuidanceFactIndex(clause string, aspect string, criticalValue string, criticalValues []string, facts []knowledgeEvidenceFact) int {
	clauseText := normalizeRuntimeKnowledgeQuery(clause)
	for index := range facts {
		if strings.TrimSpace(facts[index].Aspect) != strings.TrimSpace(aspect) {
			continue
		}
		statement := normalizeRuntimeKnowledgeQuery(facts[index].Statement)
		if statement != "" && (strings.Contains(statement, clauseText) || strings.Contains(clauseText, statement)) {
			return index
		}
	}
	if criticalValue == "对比" || criticalValue == "比较" {
		for index := range facts {
			if strings.TrimSpace(facts[index].Aspect) != strings.TrimSpace(aspect) {
				continue
			}
			if knowledgeEvidenceGuidanceCueCovered(facts[index].Statement, criticalValue) {
				return index
			}
			for _, value := range facts[index].CriticalValues {
				if knowledgeEvidenceGuidanceCueCovered(value, criticalValue) {
					return index
				}
			}
		}
	}
	for _, literal := range criticalValues {
		for index := range facts {
			if strings.TrimSpace(facts[index].Aspect) != strings.TrimSpace(aspect) {
				continue
			}
			if strings.Contains(normalizeRuntimeKnowledgeQuery(facts[index].Statement), normalizeRuntimeKnowledgeQuery(literal)) || stringSliceContains(facts[index].CriticalValues, literal) {
				return index
			}
		}
	}
	return -1
}

var knowledgeEvidenceGuidanceNumberPattern = regexp.MustCompile(`[0-9][0-9-]{5,}[0-9]`)
var knowledgeEvidenceGuidanceQuotedPattern = regexp.MustCompile(`[“\"]([^”\"]{1,20})[”\"]`)
var knowledgeEvidenceAnswerTimePattern = regexp.MustCompile(`[0-9]{1,2}:[0-9]{2}(?:\s*(?:-|~|至|到)\s*[0-9]{1,2}:[0-9]{2})?`)
var knowledgeEvidenceIndividualTimePattern = regexp.MustCompile(`(?:(?:凌晨|早上|上午|中午|下午|傍晚|晚上|夜里|夜间)?(?:[0-9]{1,2}:[0-9]{2}|(?:[0-9]{1,2}|[零〇一二三四五六七八九十两]{1,3})点(?:半|(?:[0-9]{1,2}|[零〇一二三四五六七八九十两]{1,3})分)?))`)
var knowledgeEvidenceDurationValuePattern = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?\s*(?:分钟|小时|天)`)
var knowledgeEvidenceRoleFirstIdentityPattern = regexp.MustCompile(`(?:老板|董事长|创始人|负责人|经理|管家)(?:姓名|名字)?(?:是|为|叫)([\p{Han}A-Za-z·]{2,20}(?:先生|女士|老师)?)`)
var knowledgeEvidencePersonFirstIdentityPattern = regexp.MustCompile(`([\p{Han}A-Za-z·]{2,20}(?:先生|女士|老师)?)(?:是|为|担任)(?:老板|董事长|创始人|负责人|经理|管家)`)
var knowledgeEvidenceBareIdentityValuePattern = regexp.MustCompile(`^[\p{Han}A-Za-z·]{2,20}(?:先生|女士|老师)?$`)
var knowledgeEvidenceShortVenuePattern = regexp.MustCompile(`^[\p{Han}A-Za-z0-9·]{2,20}店$`)
var knowledgeEvidenceAnswerNumberPattern = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?(?:\s*(?:-|~|至|到)\s*[0-9]+(?:\.[0-9]+)?)?(?:个|瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|天|晚|小时|分钟|分|秒|元|块|折|层|楼|号|公里|米|工作日)?`)
var knowledgeEvidenceAnswerChineseQuantityPattern = regexp.MustCompile(`[零〇一二三四五六七八九十百千万两]+(?:个|瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|天|晚|小时|分钟|元|块|折|层|楼|号|公里|米|工作日)`)
var knowledgeEvidenceStrictQuantityPattern = regexp.MustCompile(`(?:[0-9]+(?:\.[0-9]+)?|[零〇一二三四五六七八九十百千万两]+)(?:个|瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|天|晚|小时|分钟|元|块|折|公里|米|工作日)`)
var knowledgeEvidenceCombinedQuantityTotalPattern = regexp.MustCompile(`(?:一共|总共|合计|共计)(?:是|为|有|共有)?\s*((?:[0-9]+|[零〇一二三四五六七八九十百千万两]+)(?:个|瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷))`)
var knowledgeEvidencePriceValuePattern = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?(?:元|块|折)`)
var knowledgeEvidenceConfigurationFieldMarkerPattern = regexp.MustCompile(`(?i)(?:wifi|wi-fi|无线网|无线网络)?\s*(账号|帐号|用户名|名称|名字|ssid|密码|口令)\s*(?:是|为|[:：])?\s*`)

func knowledgeEvidenceGuidanceCriticalValues(clause string, criticalValue string) []string {
	values := make([]string, 0, 3)
	for _, match := range knowledgeEvidenceGuidanceNumberPattern.FindAllString(clause, -1) {
		values = appendIfMissing(values, strings.TrimSpace(match))
	}
	for _, match := range knowledgeEvidenceGuidanceQuotedPattern.FindAllStringSubmatch(clause, -1) {
		if len(match) > 1 {
			values = appendIfMissing(values, strings.TrimSpace(match[1]))
		}
	}
	return values
}

func appendKnowledgeEvidenceCriticalValues(existing []string, values []string) []string {
	ret := append([]string(nil), existing...)
	for _, value := range values {
		ret = appendIfMissing(ret, value)
	}
	return ret
}

func knowledgeEvidenceGuidanceCueCovered(text string, criticalValue string) bool {
	criticalValue = normalizeRuntimeKnowledgeQuery(criticalValue)
	if criticalValue == "对比" || criticalValue == "比较" {
		return strings.Contains(text, "对比") || strings.Contains(text, "比较")
	}
	return criticalValue != "" && strings.Contains(text, criticalValue)
}

func nextKnowledgeEvidenceFactID(taskID string, seen map[string]struct{}) string {
	prefix := strings.TrimSpace(taskID) + "F"
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s%d", prefix, index)
		if _, exists := seen[candidate]; !exists {
			return candidate
		}
	}
}

func selectedKnowledgeEvidenceIsHandoffDirective(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) bool {
	if len(selectedCandidateIDs) != 1 {
		return false
	}
	visibleCandidates := make(map[string]knowledgeEvidenceJudgeCandidate, len(task.Candidates))
	for _, candidate := range task.Candidates {
		visibleCandidates[candidate.CandidateID] = candidate
	}
	if !selectedExactKnowledgeEvidenceHandoffCandidateMatches(task.Query, layer, selectedCandidateIDs, visibleCandidates) {
		return false
	}
	selectedID := strings.TrimSpace(selectedCandidateIDs[0])
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.CandidateID) != selectedID || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		question, answer, exact := exactKnowledgeEvidenceFAQMatch(candidate.Hit, task.Query)
		if !exact || !isKnowledgeHandoffDirectiveContent(answer) {
			return false
		}
		return !knowledgeEvidenceLayerHasCompetingReviewBodyOutsideJudge(task, layer, selectedCandidateIDs, question, answer)
	}
	return false
}

func selectedModelKnowledgeEvidenceIsHandoffDirective(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) bool {
	if len(selectedCandidateIDs) != 1 {
		return false
	}
	visibleCandidates := make(map[string]knowledgeEvidenceJudgeCandidate, len(task.Candidates))
	for _, candidate := range task.Candidates {
		visibleCandidates[candidate.CandidateID] = candidate
	}
	if !selectedKnowledgeEvidenceHandoffCandidateMatches(task.Query, layer, selectedCandidateIDs, visibleCandidates) {
		return false
	}
	selectedID := strings.TrimSpace(selectedCandidateIDs[0])
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.CandidateID) != selectedID || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		return !knowledgeEvidenceLayerHasCompetingReviewBodyOutsideJudge(task, layer, selectedCandidateIDs, question, answer)
	}
	return false
}

func selectedKnowledgeEvidenceHandoffCandidateMatches(
	query string,
	layer string,
	selectedCandidateIDs []string,
	candidates map[string]knowledgeEvidenceJudgeCandidate,
) bool {
	if len(selectedCandidateIDs) != 1 {
		return false
	}
	candidate, ok := candidates[strings.TrimSpace(selectedCandidateIDs[0])]
	if !ok || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
		return false
	}
	_, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, query)
	return isKnowledgeHandoffDirectiveContent(answer)
}

func selectedExactKnowledgeEvidenceHandoffCandidateMatches(
	query string,
	layer string,
	selectedCandidateIDs []string,
	candidates map[string]knowledgeEvidenceJudgeCandidate,
) bool {
	if len(selectedCandidateIDs) != 1 {
		return false
	}
	candidate, ok := candidates[strings.TrimSpace(selectedCandidateIDs[0])]
	if !ok || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
		return false
	}
	_, answer, exact := exactKnowledgeEvidenceFAQMatch(candidate.Hit, query)
	return exact && isKnowledgeHandoffDirectiveContent(answer)
}

func selectedKnowledgeEvidenceContainsHandoffDirective(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) bool {
	if len(selectedCandidateIDs) == 0 {
		return false
	}
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	for _, candidate := range allKnowledgeEvidenceJudgeTaskCandidates(task) {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if _, ok := selected[strings.TrimSpace(candidate.CandidateID)]; !ok {
			continue
		}
		_, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if isKnowledgeHandoffDirectiveContent(answer) {
			return true
		}
	}
	return false
}

func strictExactKnowledgeEvidenceHandoffSelectionMatches(
	query string,
	layer string,
	selectedCandidateIDs []string,
	candidates map[string]knowledgeEvidenceJudgeCandidate,
) bool {
	selected := make(map[string]struct{}, len(selectedCandidateIDs))
	for _, candidateID := range selectedCandidateIDs {
		selected[strings.TrimSpace(candidateID)] = struct{}{}
	}
	selectedDirective := false
	matchedAnswer := ""
	for candidateID, candidate := range candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		_, answer, matched := exactKnowledgeEvidenceFAQMatch(candidate.Hit, query)
		if !matched {
			continue
		}
		answerKey := normalizeStrictKnowledgeEvidenceFAQAnswerText(answer)
		if matchedAnswer == "" {
			matchedAnswer = answerKey
		} else if matchedAnswer != answerKey {
			return false
		}
		if !isKnowledgeHandoffDirectiveContent(answer) {
			return false
		}
		if _, ok := selected[strings.TrimSpace(candidateID)]; ok {
			selectedDirective = true
		}
	}
	return selectedDirective
}

func normalizeKnowledgeEvidenceJudgeConfig(config models.AIConfig, taskCount int, candidateCount int) models.AIConfig {
	if taskCount < 1 {
		taskCount = 1
	}
	legacyRequiredTimeout := 4*time.Second + time.Duration(taskCount)*time.Second
	if legacyRequiredTimeout > 15*time.Second {
		legacyRequiredTimeout = 15 * time.Second
	}
	configuredTimeout := time.Duration(config.TimeoutMS) * time.Millisecond
	if config.TimeoutMS <= 0 {
		configuredTimeout = 15 * time.Second
	} else if configuredTimeout < legacyRequiredTimeout {
		configuredTimeout = legacyRequiredTimeout
	} else if configuredTimeout > 15*time.Second {
		configuredTimeout = 15 * time.Second
	}
	dynamicTimeout := knowledgeEvidenceJudgeTimeoutBudget(taskCount, candidateCount)
	if dynamicTimeout > configuredTimeout {
		configuredTimeout = dynamicTimeout
	}
	if configuredTimeout > knowledgeEvidenceJudgeMaxTimeout {
		configuredTimeout = knowledgeEvidenceJudgeMaxTimeout
	}
	config.TimeoutMS = int(configuredTimeout / time.Millisecond)

	requiredOutputTokens := 512 + taskCount*256
	if requiredOutputTokens < 1024 {
		requiredOutputTokens = 1024
	}
	if requiredOutputTokens > knowledgeEvidenceJudgeMaxOutputTokens {
		requiredOutputTokens = knowledgeEvidenceJudgeMaxOutputTokens
	}
	if config.MaxOutputTokens <= 0 {
		config.MaxOutputTokens = requiredOutputTokens
	} else if config.MaxOutputTokens < requiredOutputTokens {
		config.MaxOutputTokens = requiredOutputTokens
	} else if config.MaxOutputTokens > knowledgeEvidenceJudgeMaxOutputTokens {
		config.MaxOutputTokens = knowledgeEvidenceJudgeMaxOutputTokens
	}
	config.MaxRetryCount = 0
	return config
}

func knowledgeEvidenceJudgeTimeoutWithinParent(ctx context.Context, configured time.Duration) (time.Duration, bool) {
	if configured <= 0 {
		return 0, false
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return configured, true
	}
	return knowledgeEvidenceJudgeTimeoutWithinRemaining(configured, time.Until(deadline))
}

func knowledgeEvidenceJudgeTimeoutWithinRemaining(configured time.Duration, remaining time.Duration) (time.Duration, bool) {
	available := remaining - knowledgeEvidenceJudgeDeadlineReserve
	if configured <= 0 || available < time.Millisecond {
		return 0, false
	}
	if configured < available {
		return configured, true
	}
	return available, true
}

func knowledgeEvidenceJudgeTimeoutBudget(taskCount int, candidateCount int) time.Duration {
	if taskCount < 1 {
		taskCount = 1
	}
	if candidateCount < 0 {
		candidateCount = 0
	}
	budget := 8*time.Second + time.Duration(taskCount)*1250*time.Millisecond + time.Duration(candidateCount)*350*time.Millisecond
	budget = ((budget + time.Second - 1) / time.Second) * time.Second
	if budget < knowledgeEvidenceJudgeMinTimeout {
		return knowledgeEvidenceJudgeMinTimeout
	}
	if budget > knowledgeEvidenceJudgeMaxTimeout {
		return knowledgeEvidenceJudgeMaxTimeout
	}
	return budget
}

func knowledgeEvidenceJudgeUsageContext(ctx context.Context, req RunInput, resolved *services.ResolvedAIConfig) context.Context {
	scope := usagex.ScopeFromContext(ctx)
	runtimeScope := resolveRuntimeIntentScope(req)
	if scope.CompanyID <= 0 {
		scope.CompanyID = runtimeScope.CompanyID
	}
	if scope.StoreID <= 0 {
		scope.StoreID = runtimeScope.StoreID
	}
	if scope.WxWorkInstanceID <= 0 {
		scope.WxWorkInstanceID = runtimeScope.WxWorkInstanceID
	}
	if scope.ConversationID <= 0 {
		scope.ConversationID = req.Conversation.ID
	}
	if scope.MessageID <= 0 {
		scope.MessageID = req.UserMessage.ID
	}
	if strings.TrimSpace(scope.RequestID) == "" {
		scope.RequestID = strings.TrimSpace(req.UserMessage.RequestID)
	}
	if resolved != nil {
		scope.CredentialRevision = resolved.CredentialRevision
		scope.ModelSource = resolved.Source
	}
	return usagex.WithScope(ctx, scope)
}

func recordKnowledgeEvidenceJudgeUsage(ctx context.Context, req RunInput, config models.AIConfig, result *ai.ChatCompletionResult, receipt *usagex.Receipt, fingerprint string, latencyMS int64, callErr error) {
	status := "completed"
	errorClass := ""
	if callErr != nil {
		status = "failed"
		errorClass = "model_call_failed"
	}
	record := ai.ModelUsageRecord{
		Stage:            "knowledge_evidence_judge",
		OperationType:    "batch_select",
		Config:           config,
		LatencyMS:        latencyMS,
		Status:           status,
		ErrorClass:       errorClass,
		Receipt:          receipt,
		ExternalEventKey: knowledgeEvidenceJudgeUsageEventKey(req, fingerprint),
	}
	if result != nil {
		record.PromptTokens = int64(result.PromptTokens)
		record.CompletionTokens = int64(result.CompletionTokens)
	}
	ai.RecordModelUsage(ctx, record)
}

func knowledgeEvidenceJudgeUsageEventKey(req RunInput, fingerprint string) string {
	requestID := strings.TrimSpace(req.UserMessage.RequestID)
	if requestID == "" {
		requestID = fmt.Sprintf("conversation-%d-message-%d", req.Conversation.ID, req.UserMessage.ID)
	}
	return requestID + ":knowledge_evidence_judge:" + fingerprint
}

func lastKnowledgeEvidenceJudgeReceipt(capture *usagex.Capture) *usagex.Receipt {
	if capture == nil {
		return nil
	}
	receipts := capture.Receipts()
	if len(receipts) == 0 {
		return nil
	}
	receipt := receipts[len(receipts)-1]
	return &receipt
}

func fingerprintKnowledgeEvidenceJudgePrompt(prompt knowledgeEvidenceJudgePrompt) string {
	raw, _ := json.Marshal(prompt)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:12])
}

func countKnowledgeEvidenceJudgeCandidates(prompt knowledgeEvidenceJudgePrompt) int {
	count := 0
	for _, task := range prompt.Tasks {
		count += len(task.Candidates)
	}
	return count
}

func compactKnowledgeEvidenceJudgeError(err error) string {
	if err == nil {
		return ""
	}
	return preview(strings.Join(strings.Fields(err.Error()), " "), 240)
}
