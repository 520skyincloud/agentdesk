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

	knowledgeEvidenceDirectFAQMinimumScore = float32(0.85)

	knowledgeEvidenceJudgeMaxTimeout      = 15 * time.Second
	knowledgeEvidenceJudgeMaxOutputTokens = 4096
)

type knowledgeEvidenceJudge interface {
	JudgeBatch(ctx context.Context, req RunInput, tasks []knowledgeEvidenceJudgeTask) knowledgeEvidenceJudgeOutcome
}

type knowledgeEvidenceJudgeTask struct {
	TaskID        string
	Intent        string
	Query         string
	SubIntent     string
	Objective     string
	Entities      []knowledgeEvidenceJudgeEntity
	SourceContext []knowledgeEvidenceJudgeSourceMessage
	Candidates    []knowledgeEvidenceJudgeCandidate
	RawCandidates []knowledgeEvidenceJudgeCandidate
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
	config := normalizeKnowledgeEvidenceJudgeConfig(resolved.Config, len(prompt.Tasks))
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

	selections, parseErr := parseKnowledgeEvidenceJudgeResponse(result.Content, tasks)
	if parseErr != nil {
		failureDecision := knowledgeEvidenceJudgeParseFailureDecision(parseErr)
		trace.Status = failureDecision
		trace.Reason = "knowledge judge returned an invalid protocol response; retrieval remains intact and the judge protocol must be retried"
		trace.ErrorMessage = compactKnowledgeEvidenceJudgeError(parseErr)
		return failedKnowledgeEvidenceJudgeOutcome(tasks, trace, failureDecision)
	}
	repaired := repairExactFAQFallbackSelections(tasks, selections)
	trace.Status = "completed"
	trace.Reason = "knowledge evidence was selected once per task and layer before deterministic store priority"
	if repaired > 0 {
		trace.Reason += fmt.Sprintf("; repaired %d strict exact-FAQ selection(s) without using retrieval scores or semantic similarity", repaired)
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
1. 先把客户当前原子问题拆成内部事实维度清单。例如同一句同时询问是否存在、数量、费用、时间、位置、方法、范围或条件时，每个维度都必须单独列入检查；这个内部清单不要作为额外字段输出。
2. 对当前 layer 提供的全部候选逐条检查，每条候选的 faqQuestion、faqAnswer 和 rawContent 都要核对它能支持清单中的哪些维度，不能在看到第一条相关候选后提前停止。
3. 不同候选分别补齐不同事实维度，且属于同一门店、同一对象和同一适用范围时，必须判 direct_combined，并选中所有补齐答案所必需的同层候选。
4. 只有检查完当前 layer 的全部候选，仍有清单维度没有任何候选能够补齐时，才允许判 partial，并且 missingAspects 只能写这些真实缺失的维度。只要同层还有候选能补齐 missingAspects，就不得判 partial。

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

用品补充和自取问题必须结合客户状态与 FAQ 答案中的动作判断完整性。例如客户说“纸巾不够了，怎么补充”，同一用品的门店 FAQ 即使问题写成“纸巾用完了怎么办”，只要答案明确给出“前往某处领取/自取”的地点和动作，就已经完整覆盖 method；不能仅因问题措辞不同判 insufficient，也不能改选通用层的“转接”。

肯定枚举中的精确成员属于明确存在性证据。例如“部分房型配备办公桌，如合柴、麦田和艺林”已经明确支持“麦田房型有办公桌”；不能因为总述使用“部分房型”就把枚举内成员判为 insufficient。只有成员名称、所问设施或能力、肯定关系都在同一条 FAQ 原文中明确出现时才能使用，不能把相似名称、条件性描述或其他事实维度当成枚举成员。

最小完整答案规则：supportedFacts 只保留完整回答当前 task 必需的最小事实集合。必要的事实、适用条件和操作方法不能遗漏；背景介绍、重复总结、礼貌话、未被客户询问的路线/时长/价格/延伸建议不得加入。普通动作语义写在 statement 中，不要求后续逐字复述，也不得把动作词本身放入 criticalValues。

检查 selectedCandidateIds 的 faqAnswer 时，只拆出当前问题实际要求的独立事实维度。一个答案同时包含否定/能力边界与办理方法、数量与费用等必要维度时不能遗漏；同一完整句已经覆盖多个维度时，各 Fact 可以复用同一个完整 statement，禁止再输出被该完整句包含的摘要或碎片。否定对象、数量、金额、时间、电话、地址等不可遗漏的原文字面值必须进入对应 fact 的 criticalValues。

例如 FAQ 问题“问下房间的两瓶矿泉水是免费的吗？”、答案“是的，房间内的矿泉水都是免费的”，完整语义已经确认“房间内有两瓶矿泉水，并且免费”。它足以回答“房间里有几瓶矿泉水”，应判 direct_single；不能因为数量只写在 faqQuestion 中就丢掉这个已被肯定回答确认的事实。这个规则同样适用于其他 FAQ 中被肯定或否定答案确认的对象、数量与条件。

候选答案如果只是“转接”，它是流程指令，不是酒店事实。只有 FAQ 问题与当前任务语义直接匹配时，才可以把该候选作为 direct_single 单条流程指令选择，此时 supportedFacts 和 missingAspects 都输出空数组；绝不能把 FAQ 问题文字当作已经确认的事实，也不能让“转接”候选参与 direct_combined。

事实维度必须严格隔离：确认“有外卖机器人”只支持 existence，不能生成“能送到房间”的 scope 或 method；确认地点名称只支持 existence/location，不能生成距离、步行时间或路线；确认有充电桩不能推导所有车位都能充电。客户询问了这些未被证据确认的维度时，应判 partial 并把对应维度写入 missingAspects。

同层组合示例：客户问“既有沙发又有办公桌的房型有哪些”，一条候选完整列出有沙发的房型，另一条候选完整列出有办公桌的房型，两条属于同一门店和房型范围时，必须判 direct_combined，并由 Judge 直接计算交集。supportedFacts 只输出交集结论及交集房型 criticalValues，禁止把两组源集合原样交给后续生成阶段。只知道沙发或只知道办公桌时应判 partial，保留已确认事实，同时明确缺少另一项设施事实。

否定答案也可以完整回答问题。例如“早餐几点”对应“酒店不提供早餐”可以判 direct_single。必须区分能力/存在性与故障/执行请求，例如“有空调吗”不能选择“空调不制冷需要处理”。

严格输出 JSON，不要 Markdown、解释或额外字段。必须原样返回每个 taskId；对输入实际包含的每个 layer 恰好返回一次。输出格式：
{"schemaVersion":"knowledge_evidence_judge.v2","tasks":[{"taskId":"T1","layers":[{"layer":"store","decision":"direct_combined","selectedCandidateIds":["T1C1","T1C2"],"supportedFacts":[{"factId":"T1F1","aspect":"quantity","statement":"房间内有两瓶矿泉水，都是免费的。","criticalValues":["两瓶"]},{"factId":"T1F2","aspect":"price","statement":"房间内有两瓶矿泉水，都是免费的。","criticalValues":["免费"]}],"missingAspects":[]},{"layer":"general","decision":"insufficient","selectedCandidateIds":[],"supportedFacts":[],"missingAspects":["没有可用于回答当前问题的通用知识证据"]}]}]}`)
}

func parseKnowledgeEvidenceJudgeResponse(raw string, tasks []knowledgeEvidenceJudgeTask) (map[string]map[string]knowledgeEvidenceLayerSelection, error) {
	normalized, err := normalizeKnowledgeEvidenceJudgeResponseJSON(raw)
	if err != nil {
		return nil, knowledgeEvidenceJudgeResponseError(knowledgeEvidenceDecisionMalformed, err)
	}
	parsed := knowledgeEvidenceJudgeResponse{}
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
			selections[layer] = normalizeParsedKnowledgeEvidenceLayerSelection(
				taskID,
				layer,
				layerResult,
				expectedCandidates,
				expectedTasks[taskID],
			)
		}
		ret[taskID] = selections
	}
	return ret, nil
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
	if len(selectedIDs) > 0 && !knowledgeEvidenceSelectedCandidatesMatchTaskSubjects(expectedTask, layer, selectedIDs, decision) {
		return protocolInvalid
	}
	selectedContainsHandoff := selectedKnowledgeEvidenceContainsHandoffDirective(expectedTask, layer, selectedIDs)
	selectedHandoff := selectedKnowledgeEvidenceIsHandoffDirective(expectedTask, layer, selectedIDs)
	if selectedContainsHandoff && (!selectedHandoff || decision != knowledgeEvidenceDecisionDirectSingle || len(selectedIDs) != 1) {
		return protocolInvalid
	}
	if !selectedHandoff && decision != knowledgeEvidenceDecisionPartial && !selectedKnowledgeEvidenceAnswersMatchSingleExistenceSubject(expectedTask, layer, selectedIDs) {
		return protocolInvalid
	}
	supportedFacts, err := normalizeKnowledgeEvidenceFacts(taskID, layer, layerResult.SupportedFacts, make(map[string]struct{}))
	factsMalformed := err != nil
	missingAspects, err := normalizeKnowledgeEvidenceMissingAspects(taskID, layer, layerResult.MissingAspects)
	if err != nil {
		return protocolInvalid
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
	if !factsMalformed && len(selectedIDs) > 0 {
		supportedFacts = groundedKnowledgeEvidenceFacts(expectedTask, layer, selectedIDs, supportedFacts)
		supportedFacts = enrichKnowledgeEvidenceFactsFromSelectedFAQs(expectedTask, layer, selectedIDs, supportedFacts)
	}
	if !factsMalformed {
		supportedFacts = finalizeKnowledgeEvidenceFactsForTask(expectedTask, supportedFacts)
	}
	if decision == knowledgeEvidenceDecisionDirectCombined && len(selectedIDs) == 1 {
		return protocolInvalid
	}
	needsFactRepair := factsMalformed
	mechanicallyMissingAspects := []string(nil)
	if !factsMalformed && !selectedHandoff {
		mechanicallyMissingAspects = strictMechanicalMissingKnowledgeEvidenceAspects(expectedTask, supportedFacts)
	}
	switch decision {
	case knowledgeEvidenceDecisionInsufficient:
		if factsMalformed || len(selectedIDs) != 0 || len(supportedFacts) != 0 {
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
		if repaired, ok := repairModelSelectedKnowledgeEvidenceLayer(expectedTask, layer, decision, selectedIDs, missingAspects); ok {
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

func repairModelSelectedKnowledgeEvidenceLayer(
	task knowledgeEvidenceJudgeTask,
	layer string,
	decision string,
	selectedCandidateIDs []string,
	missingAspects []string,
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
	if decision != knowledgeEvidenceDecisionPartial && !selectedKnowledgeEvidenceAnswersMatchSingleExistenceSubject(task, layer, selectedCandidateIDs) {
		return knowledgeEvidenceLayerSelection{}, false
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
	mechanicallyMissingAspects := strictMechanicalMissingKnowledgeEvidenceAspects(task, facts)
	switch decision {
	case knowledgeEvidenceDecisionDirectSingle, knowledgeEvidenceDecisionDirectCombined:
		if len(mechanicallyMissingAspects) != 0 {
			return knowledgeEvidenceLayerSelection{}, false
		}
	case knowledgeEvidenceDecisionPartial:
		missingAspects = appendKnowledgeEvidenceMissingAspects(missingAspects, mechanicallyMissingAspects)
		if len(missingAspects) == 0 {
			return knowledgeEvidenceLayerSelection{}, false
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
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		parts := []string{answer}
		if knowledgeEvidenceFAQAnswerConfirmsQuestion(answer) {
			parts = append(parts, strings.TrimSpace(question+" "+answer))
		}
		if strings.TrimSpace(question) == "" || strings.TrimSpace(answer) == "" {
			parts = []string{candidate.Hit.Content}
		}
		unit := make([]string, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				unit = append(unit, part)
			}
		}
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
			if knowledgeEvidenceFactGroundedByText(fact, evidenceUnit) {
				grounded = append(grounded, fact)
				break
			}
		}
	}
	return grounded
}

func knowledgeEvidenceFactGroundedByText(fact knowledgeEvidenceFact, evidenceParts []string) bool {
	statement := normalizeRuntimeKnowledgeQuery(fact.Statement)
	if statement == "" {
		return false
	}
	for _, value := range fact.CriticalValues {
		normalizedValue := normalizeRuntimeKnowledgeQuery(value)
		if normalizedValue == "" || !knowledgeEvidencePartsContainValue(evidenceParts, normalizedValue) {
			return false
		}
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

func knowledgeEvidenceFAQAnswerSupportsQuestion(task knowledgeEvidenceJudgeTask, question string, answer string) bool {
	if knowledgeEvidenceFAQAnswerConfirmsQuestion(answer) {
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
	if !knowledgeEvidenceFAQAnswerSupportsQuestion(task, question, answer) {
		return facts
	}
	statement := affirmativeKnowledgeEvidenceQuestionStatement(question)
	if statement == "" {
		return facts
	}
	seenFactIDs := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		seenFactIDs[strings.TrimSpace(fact.FactID)] = struct{}{}
	}
	for _, aspect := range requiredKnowledgeEvidenceAspects(task) {
		if knowledgeEvidenceFactsCoverRequiredAspect(task, facts, aspect) {
			continue
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

func affirmativeKnowledgeEvidenceQuestionStatement(question string) string {
	statement := strings.TrimSpace(question)
	statement = strings.Trim(statement, " 。！!？?")
	for _, prefix := range []string{"请问一下", "请问", "问一下", "问下", "想问一下", "想问"} {
		statement = strings.TrimSpace(strings.TrimPrefix(statement, prefix))
	}
	statement = strings.TrimSuffix(statement, "吗")
	statement = strings.TrimSuffix(statement, "嘛")
	statement = strings.TrimSuffix(statement, "么")
	statement = strings.ReplaceAll(statement, "有没有", "有")
	statement = strings.ReplaceAll(statement, "是不是", "是")
	statement = strings.ReplaceAll(statement, "是否", "")
	statement = strings.TrimSpace(statement)
	if statement == "" {
		return ""
	}
	return statement + "。"
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
			if strings.Contains(compactAnswer, value) || knowledgeEvidenceFAQAnswerConfirmsQuestion(answer) {
				values = appendIfMissing(values, strings.TrimSpace(entity.Text))
			}
		}
	case "quantity":
		for _, match := range knowledgeEvidenceStrictQuantityPattern.FindAllString(question, -1) {
			values = appendIfMissing(values, strings.TrimSpace(match))
		}
	case "price":
		compact := normalizeRuntimeKnowledgeQuery(combined)
		for _, value := range []string{"免费", "收费"} {
			if strings.Contains(compact, value) {
				values = appendIfMissing(values, value)
			}
		}
		for _, match := range knowledgeEvidencePriceValuePattern.FindAllString(combined, -1) {
			values = appendIfMissing(values, strings.TrimSpace(match))
		}
	case "time":
		for _, match := range knowledgeEvidenceAnswerTimePattern.FindAllString(combined, -1) {
			values = appendIfMissing(values, strings.TrimSpace(match))
		}
	}
	return values
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
	ret := make([]string, 0, 3)
	appendAspect := func(aspect string) {
		if aspect != "" && !knowledgeEvidenceContainsString(ret, aspect) {
			ret = append(ret, aspect)
		}
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
		if strings.Contains(query, "怎么填") {
			appendAspect("location")
		} else {
			appendAspect("method")
		}
	case "action_request":
		if canonicalIntentCode(task.Intent) == "service_request" {
			appendAspect("method")
		}
	}
	if containsAny(query, []string{"几瓶", "几个", "几间", "几台", "几条", "几套", "几双", "几把", "几包", "几盒", "几袋", "几件", "几支", "几只", "几辆", "几杯", "几桶", "几卷", "多少瓶", "多少个", "多少台", "多少条", "多少套", "多少双", "多少把", "多少包", "多少盒", "多少袋", "多少件", "多少支", "多少只", "多少辆", "多少杯", "多少桶", "多少卷", "数量"}) {
		appendAspect("quantity")
	}
	if containsAny(query, []string{"免费", "收费", "多少钱", "价格", "费用", "钱", "价"}) {
		appendAspect("price")
	}
	if containsAny(query, []string{"几点", "多久", "什么时候", "何时", "时间"}) {
		appendAspect("time")
	}
	if containsAny(query, []string{"在哪", "哪里", "地址", "位置", "楼层", "怎么填"}) {
		appendAspect("location")
	}
	if !strings.Contains(query, "怎么填") && containsAny(query, []string{
		"怎么", "如何", "怎样", "办理", "操作", "打开", "领取", "自取", "拿取", "取用", "获取", "去拿", "在哪拿", "怎么拿", "怎么取",
	}) {
		appendAspect("method")
	}
	if containsAny(query, []string{"有没有", "是否有", "有吗", "配备", "提供吗"}) {
		appendAspect("existence")
	}
	if containsAny(query, []string{"送到", "哪些", "全部", "范围"}) ||
		(strings.Contains(query, "都有") && !knowledgeEvidenceTaskNamesFiniteRoomTypeSet(task)) {
		appendAspect("scope")
	}
	if canonicalIntentCode(task.Intent) == "service_request" && len(ret) == 0 {
		appendAspect("method")
	}
	return ret
}

func knowledgeEvidenceTaskNamesFiniteRoomTypeSet(task knowledgeEvidenceJudgeTask) bool {
	query := normalizeKnowledgeEvidenceSubjectForMatch(task.Query)
	roomTypes := 0
	for _, entity := range task.Entities {
		if !strings.EqualFold(strings.TrimSpace(entity.Type), "room_type") {
			continue
		}
		value := normalizeKnowledgeEvidenceSubjectForMatch(normalizeKnowledgeEvidenceEntityText(entity))
		if value == "" || !strings.Contains(query, value) {
			continue
		}
		roomTypes++
		if roomTypes >= 2 {
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
		return fact.Aspect == "price" && (containsAny(compact, []string{"免费", "收费", "价格", "费用", "金额"}) || knowledgeEvidencePriceValuePattern.MatchString(compact))
	case "time":
		return fact.Aspect == "time" && (knowledgeEvidenceAnswerTimePattern.MatchString(raw) || containsAny(compact, []string{"时间", "工作日", "分钟", "小时", "天", "点"}))
	case "location":
		return fact.Aspect == "location" && knowledgeEvidenceTextHasLocationCue(compact)
	case "method":
		return fact.Aspect == "method" && knowledgeEvidenceTextHasMethodCue(compact)
	case "scope":
		return fact.Aspect == "scope" && containsAny(compact, []string{"范围", "送到", "全部", "所有", "都", "仅限", "适用"})
	case "condition":
		return fact.Aspect == "condition" && containsAny(compact, []string{"如果", "条件", "取决于", "为准", "而定", "具体情况"})
	case "existence":
		return fact.Aspect == "existence" && containsAny(compact, []string{"有", "没有", "提供", "配备", "不提供", "无", "不含"})
	default:
		return fact.Aspect == aspect
	}
}

func knowledgeEvidenceFactsCoverRequiredAspect(task knowledgeEvidenceJudgeTask, facts []knowledgeEvidenceFact, aspect string) bool {
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
	if aspect == "price" && (knowledgeEvidenceQueryAsksComparison(task.Query) || knowledgeEvidenceQueryAsksPriceBoundary(task.Query)) {
		for _, fact := range facts {
			compact := normalizeRuntimeKnowledgeQuery(fact.Statement)
			if fact.Aspect == "method" && containsAny(compact, []string{"对比", "比较", "选择"}) {
				return true
			}
			if (fact.Aspect == "condition" || fact.Aspect == "scope") && containsAny(compact, []string{"平台", "权益", "不同", "调整"}) {
				return true
			}
			if fact.Aspect == "condition" && containsAny(compact, []string{"情况", "为准", "而定", "取决于"}) {
				return true
			}
		}
	}
	return false
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
	for _, aspect := range requiredKnowledgeEvidenceAspects(task) {
		if knowledgeEvidenceFactsCoverRequiredAspect(task, facts, aspect) {
			continue
		}
		ret = append(ret, knowledgeEvidenceAspectLabel(aspect))
	}
	for _, requirement := range requiredKnowledgeEvidenceSubjectAspectPairs(task) {
		if knowledgeEvidenceFactsCoverSubjectAspect(facts, requirement.Subject, requirement.Aspect) {
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

func selectedKnowledgeEvidenceAnswersMatchSingleExistenceSubject(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateIDs []string) bool {
	if !requiredKnowledgeEvidenceAspect(requiredKnowledgeEvidenceAspects(task), "existence") {
		return true
	}
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
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
	if !strings.Contains(normalizeKnowledgeEvidenceSubjectForMatch(question), subject) {
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
	answer = strings.TrimSpace(answer)
	if answer == "" || knowledgeEvidenceTextHasUncertaintyBoundary(answer) {
		return false
	}
	for _, prefix := range []string{
		"是的", "对的", "没错", "有的", "可以", "支持",
		"没有", "没有的", "不可以", "不支持", "不需要", "无需", "不用", "不能", "不是",
	} {
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
	if len(requiredAspects) < 2 {
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

func intersectKnowledgeEvidenceStrings(values []string, allowed []string) []string {
	ret := make([]string, 0, len(values))
	for _, value := range values {
		if knowledgeEvidenceContainsString(allowed, value) {
			ret = appendIfMissing(ret, value)
		}
	}
	return ret
}

func knowledgeEvidenceFactsCoverSubjectAspect(facts []knowledgeEvidenceFact, subject string, aspect string) bool {
	subject = normalizeKnowledgeEvidenceSubjectForMatch(subject)
	for _, fact := range facts {
		if !knowledgeEvidenceFactSupportsAspect(fact, aspect) {
			continue
		}
		text := normalizeKnowledgeEvidenceSubjectForMatch(fact.Statement + " " + strings.Join(fact.CriticalValues, " "))
		if strings.Contains(text, subject) {
			return true
		}
	}
	return false
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
	return containsAny(compact, []string{"一样", "不同", "区别", "对比", "比较", "哪个", "哪家"})
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

// Historical score-based rescue helpers below remain only for isolated legacy
// tests. The production JudgeBatch/apply path must use
// repairExactFAQFallbackSelections and must never call these helpers.
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
			if !ok || !knowledgeEvidenceDecisionAllowsExactFAQFallback(selection.Decision) {
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
	matches := make([]exactMatch, 0, 2)
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		question, answer, ok := exactKnowledgeEvidenceFAQMatch(candidate.Hit, task.Query)
		if !ok || strings.TrimSpace(answer) == "" {
			continue
		}
		matches = append(matches, exactMatch{candidate: candidate, question: question, answer: answer})
	}
	if len(matches) == 0 {
		return knowledgeEvidenceLayerSelection{}, false
	}
	answerKey := normalizeStrictKnowledgeEvidenceFAQText(matches[0].answer)
	for _, match := range matches[1:] {
		if normalizeStrictKnowledgeEvidenceFAQText(match.answer) != answerKey {
			return knowledgeEvidenceLayerSelection{}, false
		}
	}
	for _, candidate := range allKnowledgeEvidenceJudgeTaskCandidates(task) {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		_, answer, ok := exactKnowledgeEvidenceFAQMatch(candidate.Hit, task.Query)
		if !ok || strings.TrimSpace(answer) == "" {
			continue
		}
		if normalizeStrictKnowledgeEvidenceFAQText(answer) != answerKey {
			return knowledgeEvidenceLayerSelection{}, false
		}
	}
	selected := matches[0]
	selectedCandidateIDs := []string{selected.candidate.CandidateID}
	if !knowledgeEvidenceSelectedCandidatesMatchTaskSubjects(
		task,
		layer,
		selectedCandidateIDs,
		knowledgeEvidenceDecisionDirectSingle,
	) {
		return knowledgeEvidenceLayerSelection{}, false
	}
	if isKnowledgeHandoffDirectiveContent(selected.answer) {
		if knowledgeEvidenceLayerHasCompetingCompleteAnswer(task, layer, selectedCandidateIDs) {
			return knowledgeEvidenceLayerSelection{}, false
		}
		return knowledgeEvidenceLayerSelection{
			Decision:             knowledgeEvidenceDecisionDirectSingle,
			DecisionSource:       "deterministic_handoff",
			SelectedCandidateIDs: selectedCandidateIDs,
		}, true
	}
	repaired, ok := repairModelSelectedKnowledgeEvidenceLayer(
		task,
		layer,
		knowledgeEvidenceDecisionDirectSingle,
		selectedCandidateIDs,
		nil,
	)
	if !ok {
		return knowledgeEvidenceLayerSelection{}, false
	}
	repaired.DecisionSource = "exact_faq_fallback"
	return repaired, true
}

func knowledgeEvidenceLayerHasCompetingCompleteAnswer(task knowledgeEvidenceJudgeTask, layer string, excludedCandidateIDs []string) bool {
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
		_, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		candidateTask := task
		candidateTask.Candidates = append([]knowledgeEvidenceJudgeCandidate(nil), task.Candidates...)
		if !knowledgeEvidenceJudgeTaskContainsCandidate(candidateTask.Candidates, candidate.CandidateID) {
			candidateTask.Candidates = append(candidateTask.Candidates, candidate)
		}
		if _, complete := knowledgeEvidenceJudgeCandidateCompletesTask(candidateTask, candidate); complete {
			return true
		}
	}
	return false
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
	return strings.TrimSpace(compact)
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
		question, answer, questionMatch, _, ok := knowledgeEvidenceDirectFAQCandidateEligibility(task, candidate)
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
	if knowledgeEvidenceDirectFAQHasConflict(task, layer, best.candidate.CandidateID, best.question, best.answer, best.match) {
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
		return questionMatch, false
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

func knowledgeEvidenceDirectFAQCandidateEligibility(
	task knowledgeEvidenceJudgeTask,
	candidate knowledgeEvidenceJudgeCandidate,
) (string, string, float64, []knowledgeEvidenceFact, bool) {
	const (
		minimumRescueScore         = float32(0.65)
		minimumRescueQuestionMatch = 0.94
	)
	question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
	questionMatch, matched := knowledgeEvidenceFAQDirectMatchScore(question, answer, task.Query)
	configurationTask := knowledgeEvidenceConfigurationTopic(task.Query) != ""
	strictConfigurationMatch := configurationTask && knowledgeEvidenceStrictConfigurationCandidateMatches(task, candidate, question, answer)
	rescuedByQuestion := !configurationTask && candidate.Hit.Score >= minimumRescueScore && questionMatch >= minimumRescueQuestionMatch
	storeSemanticMatch := knowledgeEvidenceStoreServiceSemanticFAQMatches(task, candidate, question, answer)
	if (candidate.Hit.Score < knowledgeEvidenceDirectFAQMinimumScore && !rescuedByQuestion) ||
		question == "" || answer == "" || isKnowledgeHandoffDirectiveContent(answer) ||
		(!matched && !strictConfigurationMatch && !storeSemanticMatch) {
		return question, answer, questionMatch, nil, false
	}
	if configurationTask &&
		(!knowledgeEvidenceConfigurationAnswerCoversQuery(task.Query, question, answer) ||
			!knowledgeEvidenceConfigurationScopeMatches(task.Query, strings.Join([]string{question, answer, candidate.Hit.Title}, " "))) {
		return question, answer, questionMatch, nil, false
	}
	if !knowledgeEvidenceCandidateMatchesTaskSubjects(task, candidate, question, answer) {
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
	if strings.TrimSpace(candidate.Layer) != knowledgeEvidenceLayerStore ||
		!knowledgeEvidenceTaskAllowsStoreServiceSemanticFAQ(task) ||
		candidate.Hit.Score < knowledgeEvidenceDirectFAQMinimumScore {
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
	case "location", "method", "action_request":
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

func knowledgeEvidenceDirectFAQHasConflict(task knowledgeEvidenceJudgeTask, layer string, selectedCandidateID string, selectedQuestion string, selectedAnswer string, selectedQuestionMatch float64) bool {
	configurationTopic := knowledgeEvidenceConfigurationTopic(task.Query)
	selectedConfigurationScope := knowledgeEvidenceConfigurationScope(selectedQuestion + " " + selectedAnswer)
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
		if candidate.Hit.Score < 0.7 {
			continue
		}
		semanticPeer := knowledgeEvidenceStoreServiceSemanticFAQMatches(task, candidate, question, answer)
		sameFAQQuestionPeer := knowledgeEvidenceFAQQuestionMatchScore(question, selectedQuestion) >= 0.82 &&
			knowledgeEvidenceCandidateMatchesTaskSubjects(task, candidate, question, answer)
		if !semanticPeer && !sameFAQQuestionPeer && (questionMatch < 0.78 || questionMatch+0.08 < selectedQuestionMatch) {
			continue
		}
		if isKnowledgeHandoffDirectiveContent(answer) || knowledgeEvidenceFAQAnswersConflict(selectedAnswer, answer) {
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
		if len([]rune(value)) < 2 || !strings.Contains(query, value) || knowledgeEvidenceContainsString([]string{"酒店", "门店", "房间", "客房", "房型", "客户", "服务", "问题", "地址", "位置"}, value) {
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
	).Replace(normalizeRuntimeKnowledgeQuery(text))
}

func knowledgeEvidenceCandidateMatchesTaskSubjects(task knowledgeEvidenceJudgeTask, candidate knowledgeEvidenceJudgeCandidate, question string, answer string) bool {
	if knowledgeEvidenceConfigurationTopic(task.Query) != "" &&
		!knowledgeEvidenceConfigurationScopeMatches(task.Query, strings.Join([]string{question, answer, candidate.Hit.Title}, " ")) {
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
		if knowledgeEvidenceFAQAnswerConfirmsQuestion(clause) {
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
	const minimumScore = float32(0.85)
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
				return nil, fmt.Errorf("duplicate critical value %q for fact %s", value, factID)
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
	if len(facts) < 2 {
		return facts
	}
	parent := make([]int, len(facts))
	for index := range parent {
		parent[index] = index
	}
	var find func(int) int
	find = func(index int) int {
		if parent[index] != index {
			parent[index] = find(parent[index])
		}
		return parent[index]
	}
	union := func(left int, right int) {
		leftRoot, rightRoot := find(left), find(right)
		if leftRoot != rightRoot {
			parent[rightRoot] = leftRoot
		}
	}
	for left := 0; left < len(facts); left++ {
		for right := left + 1; right < len(facts); right++ {
			if knowledgeEvidenceFactsSemanticallyOverlap(facts[left], facts[right]) {
				union(left, right)
			}
		}
	}
	groups := make(map[int][]int, len(facts))
	for index := range facts {
		root := find(index)
		groups[root] = append(groups[root], index)
	}
	for _, indexes := range groups {
		if len(indexes) < 2 {
			continue
		}
		canonicalIndex := indexes[0]
		allValues := make([]string, 0)
		for _, index := range indexes {
			allValues = appendKnowledgeEvidenceCriticalValues(allValues, facts[index].CriticalValues)
			if len([]rune(normalizeRuntimeKnowledgeQuery(facts[index].Statement))) > len([]rune(normalizeRuntimeKnowledgeQuery(facts[canonicalIndex].Statement))) {
				canonicalIndex = index
			}
		}
		statement := facts[canonicalIndex].Statement
		for _, index := range indexes {
			facts[index].Statement = statement
			facts[index].CriticalValues = appendKnowledgeEvidenceCriticalValues(facts[index].CriticalValues, allValues)
		}
	}
	ret := make([]knowledgeEvidenceFact, 0, len(facts))
	seen := make(map[string]int, len(facts))
	for _, fact := range facts {
		key := strings.TrimSpace(fact.Aspect) + "|" + normalizeRuntimeKnowledgeQuery(fact.Statement)
		if index, ok := seen[key]; ok {
			ret[index].CriticalValues = appendKnowledgeEvidenceCriticalValues(ret[index].CriticalValues, fact.CriticalValues)
			continue
		}
		seen[key] = len(ret)
		ret = append(ret, fact)
	}
	return ret
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
	if !knowledgeEvidenceCriticalValueSetsEqual(left.CriticalValues, right.CriticalValues) {
		return false
	}
	if knowledgeEvidenceFactContextSignature(leftText) != knowledgeEvidenceFactContextSignature(rightText) {
		return false
	}
	if strings.Contains(leftText, rightText) || strings.Contains(rightText, leftText) {
		return true
	}
	return len(left.CriticalValues) > 0 && knowledgeEvidenceTextNGramSimilarity(leftText, rightText) >= 0.58
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
		_, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		for _, clause := range splitKnowledgeEvidenceAnswerClauses(answer) {
			if !knowledgeEvidenceAnswerClauseIsGroundedFact(clause) {
				continue
			}
			for _, classified := range knowledgeEvidenceAnswerClauseAspects(clause) {
				aspect := classified.Aspect
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
	if knowledgeEvidenceAnswerTimePattern.MatchString(clause) || strings.Contains(compact, "时间") || strings.Contains(compact, "几点") {
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
	if len(ret) == 0 {
		appendAspect("other", "")
	}
	return ret

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
	timeMatches := knowledgeEvidenceAnswerTimePattern.FindAllString(clause, -1)
	for _, match := range timeMatches {
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
	compact := normalizeRuntimeKnowledgeQuery(clause)
	for _, value := range []string{"免费", "收费"} {
		if strings.Contains(compact, value) {
			values = appendIfMissing(values, value)
		}
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
	return containsAny(compact, []string{"并不是", "并非", "没有", "不是", "不能", "不会", "无法", "不可", "不含", "不提供", "不支持", "不需要", "无需", "未配备", "暂不"})
}

func knowledgeEvidenceNegativeBoundaryAnchor(clause string) string {
	for _, marker := range []string{"并不是", "不提供", "不支持", "不需要", "未配备", "没有", "并非", "不是", "不能", "不会", "无法", "不可", "不含", "无需", "暂不"} {
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
var knowledgeEvidenceAnswerNumberPattern = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?(?:\s*(?:-|~|至|到)\s*[0-9]+(?:\.[0-9]+)?)?(?:个|瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|天|晚|小时|分钟|分|秒|元|块|折|层|楼|号|公里|米|工作日)?`)
var knowledgeEvidenceAnswerChineseQuantityPattern = regexp.MustCompile(`[零〇一二三四五六七八九十百千万两]+(?:个|瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|天|晚|小时|分钟|元|块|折|层|楼|号|公里|米|工作日)`)
var knowledgeEvidenceStrictQuantityPattern = regexp.MustCompile(`(?:[0-9]+(?:\.[0-9]+)?|[零〇一二三四五六七八九十百千万两]+)(?:个|瓶|间|张|份|位|人|台|条|套|双|把|包|盒|袋|件|支|只|辆|杯|桶|卷|天|晚|小时|分钟|元|块|折|公里|米|工作日)`)
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
	if len(selectedCandidateIDs) == 0 {
		return false
	}
	allCandidates := allKnowledgeEvidenceJudgeTaskCandidates(task)
	candidates := make(map[string]knowledgeEvidenceJudgeCandidate, len(allCandidates))
	for _, candidate := range allCandidates {
		candidates[candidate.CandidateID] = candidate
	}
	return strictExactKnowledgeEvidenceHandoffSelectionMatches(task.Query, layer, selectedCandidateIDs, candidates)
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
		answerKey := normalizeStrictKnowledgeEvidenceFAQText(answer)
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

func normalizeKnowledgeEvidenceJudgeConfig(config models.AIConfig, taskCount int) models.AIConfig {
	if taskCount < 1 {
		taskCount = 1
	}
	requiredTimeout := 4*time.Second + time.Duration(taskCount)*time.Second
	if requiredTimeout > knowledgeEvidenceJudgeMaxTimeout {
		requiredTimeout = knowledgeEvidenceJudgeMaxTimeout
	}
	configuredTimeout := time.Duration(config.TimeoutMS) * time.Millisecond
	if config.TimeoutMS <= 0 {
		configuredTimeout = knowledgeEvidenceJudgeMaxTimeout
	} else if configuredTimeout < requiredTimeout {
		configuredTimeout = requiredTimeout
	} else if configuredTimeout > knowledgeEvidenceJudgeMaxTimeout {
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
