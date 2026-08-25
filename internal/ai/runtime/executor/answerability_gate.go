package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/mlogclub/simple/sqls"
)

const (
	answerabilityNodeRetrieve = "retrieve_knowledge"
	answerabilityNodeAllow    = "allow_agent"
	answerabilityNodeFallback = "fallback"

	answerabilityStatusSkipped      = "skipped"
	answerabilityStatusNoContext    = "no_context"
	answerabilityStatusHasContext   = "has_context"
	answerabilityStatusUnanswerable = "unanswerable"
)

type knowledgeContextRetriever interface {
	KnowledgeBaseIDs() []int64
	RetrieveContextByOptions(ctx context.Context, opts retrievers.KnowledgeRetrieveOptions, query string) (*retrievers.KnowledgeRetrieveResult, error)
}

type answerabilityRetrieverFactory func(aiAgent models.AIAgent) knowledgeContextRetriever

type KnowledgeAnswerabilityGate struct {
	newRetriever answerabilityRetrieverFactory
	judge        knowledgeEvidenceJudge
}

type runtimeKnowledgeQuestionResult struct {
	TaskID string
	Query  string
	Result *retrievers.KnowledgeRetrieveResult
}

type runtimeKnowledgeRetrieveBatch struct {
	Questions []runtimeKnowledgeQuestionResult
	Merged    *retrievers.KnowledgeRetrieveResult
}

type runtimeKnowledgeQuestionDisposition struct {
	TaskID       string
	Query        string
	HasAnswer    bool
	NeedsHandoff bool
	HandoffHit   rag.RetrieveResult
}

type answerabilityGateInput struct {
	Request   RunInput
	Summary   *RunResult
	Collector *callbacks.RuntimeTraceCollector
	Messages  []*schema.Message
	Intent    callbacks.IntentTraceData
}

type answerabilityGateState struct {
	Input               answerabilityGateInput
	KnowledgeIDs        []int64
	RetrieveResult      *retrievers.KnowledgeRetrieveResult
	Decision            knowledgeGuardDecision
	SkipGate            bool
	AnswerabilityStatus string
	AnswerabilityReason string
	ErrorMessage        string
}

func NewKnowledgeAnswerabilityGate() *KnowledgeAnswerabilityGate {
	return &KnowledgeAnswerabilityGate{
		newRetriever: func(aiAgent models.AIAgent) knowledgeContextRetriever {
			return retrievers.NewKnowledgeRetriever(aiAgent)
		},
		judge: modelKnowledgeEvidenceJudge{},
	}
}

func (g *KnowledgeAnswerabilityGate) withDefaults() *KnowledgeAnswerabilityGate {
	if g == nil {
		return NewKnowledgeAnswerabilityGate()
	}
	ret := *g
	defaults := NewKnowledgeAnswerabilityGate()
	if ret.newRetriever == nil {
		ret.newRetriever = defaults.newRetriever
	}
	if ret.judge == nil {
		ret.judge = defaults.judge
	}
	return &ret
}

func (g *KnowledgeAnswerabilityGate) Evaluate(ctx context.Context, input answerabilityGateInput) (*answerabilityGateState, error) {
	gate := g.withDefaults()
	graph := compose.NewGraph[*answerabilityGateState, *answerabilityGateState]()
	if err := graph.AddLambdaNode(answerabilityNodeRetrieve, compose.InvokableLambda(gate.retrieveKnowledge)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(answerabilityNodeAllow, compose.InvokableLambda(allowAnswerabilityPassThrough)); err != nil {
		return nil, err
	}
	if err := graph.AddLambdaNode(answerabilityNodeFallback, compose.InvokableLambda(fallbackAnswerabilityPassThrough)); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(compose.START, answerabilityNodeRetrieve); err != nil {
		return nil, err
	}
	if err := graph.AddBranch(answerabilityNodeRetrieve, compose.NewGraphBranch(routeAnswerabilityGate, map[string]bool{
		answerabilityNodeAllow:    true,
		answerabilityNodeFallback: true,
	})); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(answerabilityNodeAllow, compose.END); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(answerabilityNodeFallback, compose.END); err != nil {
		return nil, err
	}
	runnable, err := graph.Compile(ctx)
	if err != nil {
		return nil, err
	}
	return runnable.Invoke(ctx, &answerabilityGateState{Input: input})
}

func routeAnswerabilityGate(ctx context.Context, state *answerabilityGateState) (string, error) {
	if state == nil {
		return answerabilityNodeFallback, nil
	}
	return answerabilityNodeAllow, nil
}

func allowAnswerabilityPassThrough(ctx context.Context, state *answerabilityGateState) (*answerabilityGateState, error) {
	if state == nil {
		return &answerabilityGateState{}, nil
	}
	if len(state.Decision.Instructions) > 0 {
		state.Input.Messages = append(state.Input.Messages, state.Decision.Instructions...)
	}
	if state.RetrieveResult != nil {
		if contextText := strings.TrimSpace(state.RetrieveResult.ContextText); contextText != "" {
			state.Input.Messages = append(state.Input.Messages, schema.SystemMessage(contextText))
		}
	}
	return state, nil
}

func fallbackAnswerabilityPassThrough(ctx context.Context, state *answerabilityGateState) (*answerabilityGateState, error) {
	if state == nil {
		return &answerabilityGateState{}, nil
	}
	return state, nil
}

func retrieveContextForRuntimeQuestions(ctx context.Context, retriever knowledgeContextRetriever, opts retrievers.KnowledgeRetrieveOptions, query string, intent callbacks.IntentTraceData) (*runtimeKnowledgeRetrieveBatch, error) {
	nonKnowledgeQueries := nonKnowledgeQueriesFromIntentTasks(intent)
	queries := mergeRuntimeKnowledgeQueries(
		query,
		knowledgeQueriesFromIntentTasks(intent),
		nonKnowledgeQueries,
	)
	if len(queries) == 0 && len(nonKnowledgeQueries) == 0 && strings.TrimSpace(query) != "" {
		queries = []string{strings.TrimSpace(query)}
	}
	return retrieveContextForRuntimeQuestionList(ctx, retriever, opts, query, queries)
}

func mergeRuntimeKnowledgeQueries(query string, taskQueries []string, nonKnowledgeQueries []string) []string {
	burstQueries := splitRuntimeKnowledgeQueries(query)
	if len(taskQueries) == 0 && len(nonKnowledgeQueries) == 0 {
		return burstQueries
	}
	if len(burstQueries) <= 1 {
		if len(taskQueries) > 0 {
			return append([]string(nil), taskQueries...)
		}
		if len(burstQueries) == 1 && runtimeKnowledgeQueryMatchesAny(nonKnowledgeQueries, burstQueries[0]) {
			if residual := runtimeKnowledgeResidualQueries(burstQueries[0], nonKnowledgeQueries); len(residual) > 0 {
				return residual
			}
			return nil
		}
		return burstQueries
	}

	ret := make([]string, 0, len(burstQueries)+len(taskQueries))
	usedTasks := make([]bool, len(taskQueries))
	for _, burstQuery := range burstQueries {
		matched := -1
		for index, taskQuery := range taskQueries {
			if usedTasks[index] || !runtimeKnowledgeQueryCovers(taskQuery, burstQuery) {
				continue
			}
			matched = index
			break
		}
		if matched >= 0 {
			ret = appendRuntimeKnowledgeQuery(ret, taskQueries[matched])
			usedTasks[matched] = true
			continue
		}
		if runtimeKnowledgeQueryMatchesAny(nonKnowledgeQueries, burstQuery) {
			continue
		}
		if runtimeBurstLineLooksLikeTask(burstQuery) {
			ret = appendRuntimeKnowledgeQuery(ret, burstQuery)
		}
	}
	for index, taskQuery := range taskQueries {
		if !usedTasks[index] {
			ret = appendRuntimeKnowledgeQuery(ret, taskQuery)
		}
	}
	return ret
}

func runtimeKnowledgeQueryMatchesAny(queries []string, query string) bool {
	for _, candidate := range queries {
		if runtimeKnowledgeQueryCovers(candidate, query) {
			return true
		}
	}
	return false
}

func runtimeKnowledgeResidualQueries(query string, nonKnowledgeQueries []string) []string {
	parts := strings.FieldsFunc(strings.TrimSpace(currentTurnDisplayText(query)), func(r rune) bool {
		switch r {
		case '\n', '\r', ',', '，', '.', '。', ';', '；', '?', '？', '!', '！':
			return true
		default:
			return false
		}
	})
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		part = cleanRuntimeQuestionLine(part)
		if part == "" || isRuntimeBurstStructureLine(part) || runtimeKnowledgeQueryMatchesAny(nonKnowledgeQueries, part) || !runtimeBurstLineLooksLikeTask(part) {
			continue
		}
		ret = appendRuntimeKnowledgeQuery(ret, part)
	}
	return ret
}

func appendRuntimeKnowledgeQuery(items []string, query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return items
	}
	for _, existing := range items {
		if runtimeKnowledgeQueryCovers(existing, query) && runtimeKnowledgeQueryCovers(query, existing) {
			return items
		}
	}
	return append(items, query)
}

func runtimeKnowledgeQueryCovers(taskQuery string, burstQuery string) bool {
	task := normalizeRuntimeKnowledgeQuery(taskQuery)
	burst := normalizeRuntimeKnowledgeQuery(burstQuery)
	if task == "" || burst == "" {
		return false
	}
	return task == burst || strings.Contains(task, burst) || strings.Contains(burst, task)
}

func normalizeRuntimeKnowledgeQuery(query string) string {
	return strings.NewReplacer(
		" ", "", "\t", "", "\r", "", "\n", "",
		"，", "", ",", "", "。", "", "？", "", "?", "",
		"！", "", "!", "", "：", "", ":", "", "；", "", ";", "",
	).Replace(strings.ToLower(strings.TrimSpace(cleanRuntimeQuestionLine(query))))
}

func runtimeBurstLineLooksLikeTask(query string) bool {
	compact := normalizeRuntimeKnowledgeQuery(query)
	if compact == "" {
		return false
	}
	return strings.ContainsAny(query, "?？") || containsAny(compact, []string{
		"吗", "么", "呢", "哪", "什么", "怎么", "如何", "谁", "多少", "几", "多久", "几点", "为什么", "有没有", "能不能", "可不可以", "是否",
		"帮我", "给我", "发我", "我要", "我想", "请问", "麻烦", "转人工", "投诉",
		"没了", "没有", "坏了", "堵了", "打不开", "不制冷", "失败", "拿不出", "太吵", "很吵",
	})
}

func knowledgeQueriesFromIntentTasks(intent callbacks.IntentTraceData) []string {
	ret := make([]string, 0, len(intent.IntentTasks))
	seen := map[string]bool{}
	for _, task := range intent.IntentTasks {
		if task.Intent != "hotel_info" && !task.NeedsKnowledge {
			continue
		}
		query := strings.TrimSpace(task.Text)
		if query == "" {
			query = strings.TrimSpace(task.SubIntent)
		}
		if query == "" || seen[query] {
			continue
		}
		seen[query] = true
		ret = append(ret, query)
	}
	return ret
}

func nonKnowledgeQueriesFromIntentTasks(intent callbacks.IntentTraceData) []string {
	ret := make([]string, 0, len(intent.IntentTasks))
	seen := map[string]bool{}
	for _, task := range intent.IntentTasks {
		if task.NeedsKnowledge || task.Intent == "hotel_info" {
			continue
		}
		if !task.NeedsResource && !task.NeedsTool && !task.NeedsHumanRoute && task.Intent != "hotel_variable" && task.Intent != "human_complaint_risk" {
			continue
		}
		query := strings.TrimSpace(task.Text)
		if query == "" {
			continue
		}
		normalized := normalizeRuntimeKnowledgeQuery(query)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		ret = append(ret, query)
	}
	return ret
}

func splitRuntimeKnowledgeQueries(query string) []string {
	display := strings.TrimSpace(currentTurnDisplayText(query))
	if display == "" || !isMultiQuestionCurrentTurn(display) {
		if query = strings.TrimSpace(query); query != "" {
			return []string{query}
		}
		return nil
	}
	lines := strings.Split(display, "\n")
	ret := make([]string, 0, len(lines))
	seen := map[string]bool{}
	for _, line := range lines {
		line = cleanRuntimeQuestionLine(line)
		if line == "" || isRuntimeBurstStructureLine(line) || seen[line] {
			continue
		}
		seen[line] = true
		ret = append(ret, line)
	}
	if len(ret) <= 1 {
		if query = strings.TrimSpace(query); query != "" {
			return []string{query}
		}
	}
	return ret
}

func cleanRuntimeQuestionLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	line = trimRuntimeQuestionOrdinal(line)
	for strings.HasPrefix(line, "[") {
		end := strings.Index(line, "]")
		if end <= 0 || end >= len(line)-1 {
			break
		}
		line = strings.TrimSpace(line[end+1:])
	}
	line = strings.TrimPrefix(line, "-")
	line = strings.TrimPrefix(line, "•")
	return strings.TrimSpace(line)
}

func trimRuntimeQuestionOrdinal(line string) string {
	runes := []rune(strings.TrimSpace(line))
	index := 0
	for index < len(runes) && runes[index] >= '0' && runes[index] <= '9' {
		index++
	}
	if index == 0 || index >= len(runes) {
		return strings.TrimSpace(line)
	}
	switch runes[index] {
	case '.', '．', '、':
		return strings.TrimSpace(string(runes[index+1:]))
	default:
		return strings.TrimSpace(line)
	}
}

func isRuntimeBurstStructureLine(line string) bool {
	return strings.Contains(line, "本轮客户连续消息") || strings.Contains(line, "按时间顺序")
}

func retrieveContextForRuntimeQuestionList(ctx context.Context, retriever knowledgeContextRetriever, opts retrievers.KnowledgeRetrieveOptions, originalQuery string, queries []string) (*runtimeKnowledgeRetrieveBatch, error) {
	batch := &runtimeKnowledgeRetrieveBatch{}
	if len(queries) == 0 {
		batch.Merged = &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: append([]int64(nil), retriever.KnowledgeBaseIDs()...),
			Query:            strings.TrimSpace(originalQuery),
			Options:          opts,
		}
		return batch, nil
	}
	results := make([]*retrievers.KnowledgeRetrieveResult, len(queries))
	errs := make(chan error, len(queries))
	var wg sync.WaitGroup
	for i, question := range queries {
		wg.Add(1)
		go func(index int, query string) {
			defer wg.Done()
			questionOpts := opts
			questionOpts.QueryPreview = preview(query, 120)
			if len(queries) > 1 {
				if questionOpts.MaxContextItems <= 0 || questionOpts.MaxContextItems > 2 {
					questionOpts.MaxContextItems = 2
				}
				if questionOpts.TopK <= 0 || questionOpts.TopK > 4 {
					questionOpts.TopK = 4
				}
			}
			result, err := retriever.RetrieveContextByOptions(ctx, questionOpts, query)
			if err != nil {
				errs <- err
				return
			}
			results[index] = result
		}(i, question)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return nil, err
		}
	}
	batch.Questions = make([]runtimeKnowledgeQuestionResult, 0, len(queries))
	for index, question := range queries {
		batch.Questions = append(batch.Questions, runtimeKnowledgeQuestionResult{
			TaskID: fmt.Sprintf("T%d", index+1),
			Query:  strings.TrimSpace(question),
			Result: results[index],
		})
	}
	batch.Merged = mergeRuntimeKnowledgeQuestionResults(retriever.KnowledgeBaseIDs(), opts, originalQuery, batch.Questions)
	return batch, nil
}

func mergeRuntimeKnowledgeQuestionResults(knowledgeBaseIDs []int64, opts retrievers.KnowledgeRetrieveOptions, originalQuery string, questions []runtimeKnowledgeQuestionResult) *retrievers.KnowledgeRetrieveResult {
	merged := &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: append([]int64(nil), knowledgeBaseIDs...),
		Query:            strings.TrimSpace(originalQuery),
		Options:          opts,
	}
	seenRawHits := map[string]bool{}
	seenHits := map[string]bool{}
	seenContext := map[string]bool{}
	contextSections := make([]string, 0, len(questions))
	for _, question := range questions {
		result := question.Result
		if result == nil {
			continue
		}
		if len(merged.KnowledgeBaseIDs) == 0 {
			merged.KnowledgeBaseIDs = append([]int64(nil), result.KnowledgeBaseIDs...)
		}
		if len(merged.Policies) == 0 {
			merged.Policies = append([]retrievers.KnowledgeBaseRetrievePolicy(nil), result.Policies...)
		}
		if result.AnswerMode != 0 {
			merged.AnswerMode = result.AnswerMode
		}
		if result.TopScore > merged.TopScore {
			merged.TopScore = result.TopScore
		}
		merged.RawHits = appendUniqueRuntimeRetrieveResults(merged.RawHits, result.RawHits, seenRawHits)
		merged.Hits = appendUniqueRuntimeRetrieveResults(merged.Hits, result.Hits, seenHits)
		merged.ContextResults = appendUniqueRuntimeRetrieveResults(merged.ContextResults, result.ContextResults, seenContext)
		merged.TraceItems = append(merged.TraceItems, result.TraceItems...)
		if strings.TrimSpace(result.ContextText) != "" {
			if len(questions) == 1 {
				contextSections = append(contextSections, strings.TrimSpace(result.ContextText))
			} else {
				contextSections = append(contextSections, "【问题："+question.Query+"】\n"+strings.TrimSpace(result.ContextText))
			}
		}
		if merged.TraceSummary.TopK == 0 && merged.TraceSummary.ContextMaxTokens == 0 {
			merged.TraceSummary = result.TraceSummary
		}
	}
	merged.ContextText = strings.TrimSpace(strings.Join(contextSections, "\n\n"))
	if merged.ContextText == "" && len(merged.ContextResults) > 0 {
		merged.ContextText = strings.TrimSpace(rag.Retrieve.BuildContext(context.Background(), merged.ContextResults, 1<<30))
	}
	merged.TraceSummary.HitCount = len(merged.Hits)
	merged.TraceSummary.ContextCount = len(merged.ContextResults)
	return merged
}

func buildKnowledgeEvidenceJudgeTasks(batch *runtimeKnowledgeRetrieveBatch, storeKnowledgeBaseIDs []int64, knowledgeBaseIDs []int64) []knowledgeEvidenceJudgeTask {
	if batch == nil || len(batch.Questions) == 0 {
		return nil
	}
	storeSet := make(map[int64]struct{}, len(storeKnowledgeBaseIDs))
	for _, knowledgeBaseID := range storeKnowledgeBaseIDs {
		if knowledgeBaseID > 0 {
			storeSet[knowledgeBaseID] = struct{}{}
		}
	}
	generalSet := make(map[int64]struct{}, len(knowledgeBaseIDs))
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		if knowledgeBaseID <= 0 {
			continue
		}
		if _, isStore := storeSet[knowledgeBaseID]; !isStore {
			generalSet[knowledgeBaseID] = struct{}{}
		}
	}

	tasks := make([]knowledgeEvidenceJudgeTask, 0, len(batch.Questions))
	for _, question := range batch.Questions {
		if question.Result == nil {
			continue
		}
		rawHits := question.Result.RawHits
		if len(rawHits) == 0 {
			rawHits = question.Result.Hits
		}
		item := knowledgeEvidenceJudgeTask{TaskID: question.TaskID, Query: question.Query}
		for _, hit := range rawHits {
			layer := ""
			if _, ok := storeSet[hit.KnowledgeBaseID]; ok {
				layer = knowledgeEvidenceLayerStore
			} else if _, ok := generalSet[hit.KnowledgeBaseID]; ok {
				layer = knowledgeEvidenceLayerGeneral
			}
			if layer == "" {
				continue
			}
			item.Candidates = append(item.Candidates, knowledgeEvidenceJudgeCandidate{
				CandidateID: fmt.Sprintf("%sC%d", question.TaskID, len(item.Candidates)+1),
				Layer:       layer,
				Hit:         hit,
			})
		}
		if len(item.Candidates) > 0 {
			tasks = append(tasks, item)
		}
	}
	return tasks
}

func applyKnowledgeEvidenceJudgeOutcome(batch *runtimeKnowledgeRetrieveBatch, tasks []knowledgeEvidenceJudgeTask, outcome knowledgeEvidenceJudgeOutcome) callbacks.KnowledgeEvidenceJudgeTraceData {
	trace := outcome.Trace
	if batch == nil || !outcome.Applied {
		return trace
	}
	questionByTaskID := make(map[string]*runtimeKnowledgeQuestionResult, len(batch.Questions))
	for index := range batch.Questions {
		questionByTaskID[batch.Questions[index].TaskID] = &batch.Questions[index]
	}
	trace.Tasks = make([]callbacks.KnowledgeEvidenceJudgeTaskTraceData, 0, len(tasks))
	for _, task := range tasks {
		question := questionByTaskID[task.TaskID]
		if question == nil || question.Result == nil {
			continue
		}
		classifications := outcome.Classifications[task.TaskID]
		directByLayer := map[string][]knowledgeEvidenceJudgeCandidate{}
		supportingByLayer := map[string][]knowledgeEvidenceJudgeCandidate{}
		taskTrace := callbacks.KnowledgeEvidenceJudgeTaskTraceData{
			TaskID:       task.TaskID,
			QueryPreview: preview(task.Query, 120),
		}
		for _, candidate := range task.Candidates {
			switch classifications[candidate.CandidateID] {
			case knowledgeEvidenceClassificationDirect:
				directByLayer[candidate.Layer] = append(directByLayer[candidate.Layer], candidate)
				taskTrace.DirectCandidateIDs = append(taskTrace.DirectCandidateIDs, candidate.CandidateID)
			case knowledgeEvidenceClassificationSupporting:
				supportingByLayer[candidate.Layer] = append(supportingByLayer[candidate.Layer], candidate)
				taskTrace.SupportingCandidateIDs = append(taskTrace.SupportingCandidateIDs, candidate.CandidateID)
			}
		}
		selectedLayer := ""
		if len(directByLayer[knowledgeEvidenceLayerStore]) > 0 {
			selectedLayer = knowledgeEvidenceLayerStore
		} else if len(directByLayer[knowledgeEvidenceLayerGeneral]) > 0 {
			selectedLayer = knowledgeEvidenceLayerGeneral
		}
		selectedCandidates := make([]knowledgeEvidenceJudgeCandidate, 0, len(task.Candidates))
		if selectedLayer != "" {
			selectedCandidates = append(selectedCandidates, directByLayer[selectedLayer]...)
			selectedCandidates = append(selectedCandidates, supportingByLayer[selectedLayer]...)
		}
		selectedHits := make([]rag.RetrieveResult, 0, len(selectedCandidates))
		for _, candidate := range selectedCandidates {
			selectedHits = append(selectedHits, candidate.Hit)
			taskTrace.SelectedCandidateIDs = append(taskTrace.SelectedCandidateIDs, candidate.CandidateID)
		}
		taskTrace.SelectedLayer = selectedLayer
		if selectedLayer == "" {
			taskTrace.Decision = "no_direct_evidence"
		} else {
			taskTrace.Decision = "selected_" + selectedLayer
		}
		retrievers.RebuildKnowledgeRetrieveSelection(question.Result, selectedHits)
		trace.Tasks = append(trace.Tasks, taskTrace)
	}
	batch.Merged = mergeRuntimeKnowledgeQuestionResults(batch.Merged.KnowledgeBaseIDs, batch.Merged.Options, batch.Merged.Query, batch.Questions)
	return trace
}

func runtimeKnowledgeQuestionDispositions(batch *runtimeKnowledgeRetrieveBatch) []runtimeKnowledgeQuestionDisposition {
	if batch == nil {
		return nil
	}
	items := make([]runtimeKnowledgeQuestionDisposition, 0, len(batch.Questions))
	for _, question := range batch.Questions {
		item := runtimeKnowledgeQuestionDisposition{TaskID: question.TaskID, Query: question.Query}
		result := question.Result
		if result == nil || len(result.Hits) == 0 {
			item.NeedsHandoff = true
			items = append(items, item)
			continue
		}
		if hit, ok := topKnowledgeHandoffDirective(result); ok {
			item.NeedsHandoff = true
			item.HandoffHit = hit
			items = append(items, item)
			continue
		}
		if strings.TrimSpace(result.ContextText) == "" {
			item.NeedsHandoff = true
			items = append(items, item)
			continue
		}
		removeKnowledgeHandoffDirectiveSelection(result)
		if len(result.Hits) == 0 || strings.TrimSpace(result.ContextText) == "" {
			item.NeedsHandoff = true
			items = append(items, item)
			continue
		}
		item.HasAnswer = true
		items = append(items, item)
	}
	return items
}

func splitRuntimeKnowledgeQuestionDispositions(items []runtimeKnowledgeQuestionDisposition) (answered int, pending []runtimeKnowledgeQuestionDisposition) {
	for _, item := range items {
		if item.HasAnswer {
			answered++
		}
		if item.NeedsHandoff {
			pending = append(pending, item)
		}
	}
	return answered, pending
}

func rebuildRuntimeKnowledgeReplyPlan(
	plan callbacks.ReplyPlanTraceData,
	questions []runtimeKnowledgeQuestionResult,
	pending []runtimeKnowledgeQuestionDisposition,
	excludePending bool,
) callbacks.ReplyPlanTraceData {
	pendingTaskIDs := make(map[string]bool, len(pending))
	if excludePending {
		for _, item := range pending {
			pendingTaskIDs[strings.TrimSpace(item.TaskID)] = true
		}
	}

	usedPlanTasks := make([]bool, len(plan.TaskPlans))
	activeKnowledgeTasks := make([]callbacks.ReplyTaskPlanTraceData, 0, len(questions))
	for _, question := range questions {
		if pendingTaskIDs[strings.TrimSpace(question.TaskID)] {
			continue
		}
		query := strings.TrimSpace(question.Query)
		if query == "" {
			continue
		}
		task := callbacks.ReplyTaskPlanTraceData{
			Intent: "hotel_info",
			Text:   query,
			Output: "knowledge_text_reply",
		}
		matchedPlanIndex := -1
		for index, candidate := range plan.TaskPlans {
			if usedPlanTasks[index] || !runtimeReplyTaskUsesKnowledge(candidate) || !runtimeKnowledgeQueryCovers(candidate.Text, query) {
				continue
			}
			matchedPlanIndex = index
			break
		}
		if matchedPlanIndex < 0 {
			for index, candidate := range plan.TaskPlans {
				if usedPlanTasks[index] || !runtimeReplyTaskUsesKnowledge(candidate) || strings.TrimSpace(candidate.Text) != "" {
					continue
				}
				matchedPlanIndex = index
				break
			}
		}
		if matchedPlanIndex >= 0 {
			usedPlanTasks[matchedPlanIndex] = true
			task = plan.TaskPlans[matchedPlanIndex]
			task.Text = query
			task.Output = "knowledge_text_reply"
			if strings.TrimSpace(task.Intent) == "" {
				task.Intent = "hotel_info"
			}
		}
		activeKnowledgeTasks = append(activeKnowledgeTasks, task)
	}

	rebuilt := make([]callbacks.ReplyTaskPlanTraceData, 0, len(plan.TaskPlans)+len(activeKnowledgeTasks))
	insertedKnowledgeTasks := false
	for _, task := range plan.TaskPlans {
		if runtimeReplyTaskUsesKnowledge(task) {
			if !insertedKnowledgeTasks {
				rebuilt = append(rebuilt, activeKnowledgeTasks...)
				insertedKnowledgeTasks = true
			}
			continue
		}
		rebuilt = append(rebuilt, task)
	}
	if !insertedKnowledgeTasks && len(activeKnowledgeTasks) > 0 {
		rebuilt = append(activeKnowledgeTasks, rebuilt...)
	}
	plan.TaskPlans = rebuilt
	return plan
}

func runtimeReplyTaskUsesKnowledge(task callbacks.ReplyTaskPlanTraceData) bool {
	output := strings.TrimSpace(task.Output)
	intent := strings.TrimSpace(task.Intent)
	if output == "structured_resource_commit" || output == "human_route_confirmation_or_dispatch" || intent == "hotel_variable" {
		return false
	}
	return output == "knowledge_text_reply" || intent == "hotel_info"
}

func clearDeferredRuntimeKnowledgeQuestions(batch *runtimeKnowledgeRetrieveBatch, pending []runtimeKnowledgeQuestionDisposition) {
	if batch == nil || len(pending) == 0 {
		return
	}
	pendingSet := make(map[string]struct{}, len(pending))
	for _, item := range pending {
		pendingSet[item.TaskID] = struct{}{}
	}
	for index := range batch.Questions {
		if _, ok := pendingSet[batch.Questions[index].TaskID]; !ok || batch.Questions[index].Result == nil {
			continue
		}
		retrievers.RebuildKnowledgeRetrieveSelection(batch.Questions[index].Result, nil)
	}
	batch.Merged = mergeRuntimeKnowledgeQuestionResults(batch.Merged.KnowledgeBaseIDs, batch.Merged.Options, batch.Merged.Query, batch.Questions)
}

func deferredRuntimeKnowledgeHandoffReason(pending []runtimeKnowledgeQuestionDisposition) string {
	labels := make([]string, 0, len(pending))
	for _, item := range pending {
		if label := preview(strings.TrimSpace(item.Query), 80); label != "" {
			labels = append(labels, label)
		}
	}
	reason := "部分酒店业务问题需要门店同事接手"
	if len(labels) > 0 {
		reason += "；待处理问题：" + strings.Join(labels, "；")
	}
	return reason
}

func buildDeferredRuntimeKnowledgeInstruction(pending []runtimeKnowledgeQuestionDisposition, willRequestHandoff bool) string {
	labels := make([]string, 0, len(pending))
	for _, item := range pending {
		if label := preview(strings.TrimSpace(item.Query), 80); label != "" {
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		return ""
	}
	if willRequestHandoff {
		return strings.Join([]string{
			"【部分问题处理边界】部分原子问题已从当前 active ReplyPlan 移除，并由系统单独执行转接；若缺少必要房号，系统会先追问房号。",
			"本次只回答 active ReplyPlan 中仍保留、且已经提供直接知识证据的问题；不得猜测、复述、概括或提及任何已移除任务，也不得声称已经记录、登记、受理、处理、联系、安排或转接。",
			"系统会在本条知识答案提交成功后单独执行直接转接或必要的房号追问，本次 Generate 不要重复输出转接、房号追问或成功话术。",
		}, "\n")
	}
	parts := []string{
		"【部分问题处理边界】以下问题当前没有可靠直接知识，或胜出知识明确要求门店同事接手：" + strings.Join(labels, "；") + "。",
		"本次只回答已经提供直接知识证据的其他问题；不得猜测这些待处理问题，不得把其他问题的答案挪过来，也不得声称已经记录、登记、受理、处理、联系、安排或转接。",
	}
	parts = append(parts, "当前会话不允许自动转人工；对这些问题只可自然说明暂时无法确认，不要承诺后续动作。")
	return strings.Join(parts, "\n")
}

func appendUniqueRuntimeRetrieveResults(dst []rag.RetrieveResult, src []rag.RetrieveResult, seen map[string]bool) []rag.RetrieveResult {
	for _, item := range src {
		key := runtimeRetrieveResultKey(item)
		if seen[key] {
			continue
		}
		seen[key] = true
		dst = append(dst, item)
	}
	return dst
}

func runtimeRetrieveResultKey(item rag.RetrieveResult) string {
	return fmt.Sprintf("%d:%d:%d:%d:%s:%s", item.KnowledgeBaseID, item.DocumentID, item.ChunkID, item.FaqID, strings.TrimSpace(item.SourceRecordID), strings.TrimSpace(item.Content))
}

func resolveRuntimeKnowledgeResources(req RunInput, result *retrievers.KnowledgeRetrieveResult) []callbacks.KnowledgeResourceTraceData {
	if result == nil || len(result.ContextResults) == 0 {
		return nil
	}
	scope := resolveRuntimeIntentScope(req)
	if scope.WxWorkInstanceID <= 0 || scope.IntentProfileID <= 0 {
		return nil
	}
	sources := make([]services.KnowledgeResourceSourceRef, 0, len(result.ContextResults))
	for _, item := range result.ContextResults {
		if item.KnowledgeBaseID <= 0 || strings.TrimSpace(item.SourceRecordID) == "" {
			continue
		}
		sources = append(sources, services.KnowledgeResourceSourceRef{
			KnowledgeBaseID: item.KnowledgeBaseID,
			SourceRecordID:  item.SourceRecordID,
		})
	}
	resources := services.KnowledgeResourceService.ResolveForRuntime(scope.WxWorkInstanceID, scope.CompanyID, scope.IntentProfileID, sources)
	ret := make([]callbacks.KnowledgeResourceTraceData, 0, len(resources))
	for _, item := range resources {
		ret = append(ret, callbacks.KnowledgeResourceTraceData{
			GroupID:         item.GroupID,
			ItemID:          item.ItemID,
			KnowledgeBaseID: item.KnowledgeBaseID,
			SourceRecordID:  item.SourceRecordID,
			AssetID:         item.AssetID,
			Title:           item.Title,
			Description:     item.Description,
			SortNo:          item.SortNo,
		})
	}
	return ret
}

func (g *KnowledgeAnswerabilityGate) retrieveKnowledge(ctx context.Context, state *answerabilityGateState) (*answerabilityGateState, error) {
	if state == nil {
		state = &answerabilityGateState{}
	}
	gate := g.withDefaults()
	req := state.Input.Request
	intent := state.Input.Intent
	knowledgeActionInstruction := buildKnowledgePathActionInstruction(req, intent)
	if instruction := buildMissingMediaContextInstruction(req, state.Input.Messages, intent); strings.TrimSpace(instruction) != "" {
		state.Decision.Instructions = append(state.Decision.Instructions, schema.SystemMessage(instruction))
		state.SkipGate = true
		state.recordAnswerability(answerabilityStatusSkipped, "missing media context instruction", nil)
		return state, nil
	}
	if !intent.NeedsKnowledge {
		if instruction := buildIntentActionInstruction(req, intent); strings.TrimSpace(instruction) != "" {
			state.Decision.Instructions = append(state.Decision.Instructions, schema.SystemMessage(instruction))
		}
		state.SkipGate = true
		state.recordAnswerability(answerabilityStatusSkipped, "intent does not require knowledge", nil)
		return state, nil
	}
	configuredKnowledgeIDs := utils.SplitInt64s(req.AIAgent.KnowledgeIDs)
	if len(configuredKnowledgeIDs) == 0 {
		markKnowledgeNoContextHandoffDirective(state.Input, "当前酒店业务问题没有配置可用知识库")
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, configuredKnowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		state.recordAnswerability(answerabilityStatusNoContext, "intent requires knowledge but no knowledge configured", nil)
		return state, nil
	}
	retriever := gate.newRetriever(req.AIAgent)
	state.KnowledgeIDs = append([]int64(nil), configuredKnowledgeIDs...)
	if retriever == nil {
		state.Decision = buildKnowledgeRetrievalErrorDecision(req.AIAgent, configuredKnowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		state.recordAnswerability(answerabilityStatusUnanswerable, "knowledge retriever unavailable", nil)
		return state, nil
	}
	knowledgeIDs := retriever.KnowledgeBaseIDs()
	state.KnowledgeIDs = append([]int64(nil), knowledgeIDs...)
	if len(knowledgeIDs) == 0 {
		markKnowledgeNoContextHandoffDirective(state.Input, "当前酒店业务问题没有可用知识库")
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, configuredKnowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		state.recordAnswerability(answerabilityStatusNoContext, "intent requires knowledge but retriever has no knowledge", nil)
		return state, nil
	}
	query := strings.TrimSpace(req.UserMessage.Content)
	if query == "" {
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, knowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		state.recordAnswerability(answerabilityStatusNoContext, "empty user question", nil)
		return state, nil
	}
	retrieveOptions := retrievers.DefaultKnowledgeRetrieveOptions()
	retrieveOptions.QueryPreview = preview(req.UserMessage.Content, 120)
	batch, err := retrieveContextForRuntimeQuestions(ctx, retriever, retrieveOptions, query, intent)
	if err != nil {
		state.Decision = buildKnowledgeRetrievalErrorDecision(req.AIAgent, knowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		state.ErrorMessage = err.Error()
		state.recordAnswerability(answerabilityStatusUnanswerable, "knowledge retrieval failed", err)
		return state, nil
	}
	result := batch.Merged
	storeKnowledgeBaseIDs := utils.SplitInt64s(req.AIAgent.KnowledgeIDs)
	judgeTasks := buildKnowledgeEvidenceJudgeTasks(batch, storeKnowledgeBaseIDs, knowledgeIDs)
	judgeTrace := callbacks.KnowledgeEvidenceJudgeTraceData{
		SchemaVersion: knowledgeEvidenceJudgeSchemaVersion,
		Status:        "skipped",
		Reason:        "no retrieved candidates required evidence judging",
	}
	if len(judgeTasks) > 0 {
		judgeOutcome := gate.judge.JudgeBatch(ctx, req, judgeTasks)
		judgeTrace = applyKnowledgeEvidenceJudgeOutcome(batch, judgeTasks, judgeOutcome)
		result = batch.Merged
	}
	dispositions := runtimeKnowledgeQuestionDispositions(batch)
	batch.Merged = mergeRuntimeKnowledgeQuestionResults(batch.Merged.KnowledgeBaseIDs, batch.Merged.Options, batch.Merged.Query, batch.Questions)
	result = batch.Merged
	answeredQuestionCount, pendingQuestions := splitRuntimeKnowledgeQuestionDispositions(dispositions)
	deferredInstruction := ""
	if len(pendingQuestions) > 0 && answeredQuestionCount == 0 {
		if state.Input.Collector != nil {
			state.Input.Collector.SetKnowledgeEvidenceJudge(judgeTrace)
		}
		clearDeferredRuntimeKnowledgeQuestions(batch, pendingQuestions)
		result = batch.Merged
		state.RetrieveResult = result
		if state.Input.Summary != nil && result != nil {
			state.Input.Summary.RetrieverCount = len(result.Hits)
		}
		for _, pending := range pendingQuestions {
			if pending.HandoffHit.Content != "" {
				markKnowledgeHandoffDirective(state.Input, pending.HandoffHit)
				state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, knowledgeIDs)
				state.recordAnswerability(answerabilityStatusSkipped, "selected knowledge answer requested human handoff", nil)
				return state, nil
			}
		}
		markKnowledgeNoContextHandoffDirective(state.Input, "当前酒店业务问题知识库没有可用答案")
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, knowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		state.recordAnswerability(answerabilityStatusNoContext, "no retrieved context", nil)
		return state, nil
	}
	willRequestHandoff := len(pendingQuestions) > 0 && services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(req.Conversation.ID)
	if state.Input.Collector != nil {
		activePlan := rebuildRuntimeKnowledgeReplyPlan(
			state.Input.Collector.Data.Pipeline.ReplyPlan,
			batch.Questions,
			pendingQuestions,
			willRequestHandoff,
		)
		state.Input.Collector.SetReplyPlan(activePlan)
	}
	if len(pendingQuestions) > 0 {
		clearDeferredRuntimeKnowledgeQuestions(batch, pendingQuestions)
		result = batch.Merged
		deferredInstruction = buildDeferredRuntimeKnowledgeInstruction(pendingQuestions, willRequestHandoff)
		if willRequestHandoff {
			judgeTrace.DeferredHandoff = true
			judgeTrace.DeferredHandoffReason = deferredRuntimeKnowledgeHandoffReason(pendingQuestions)
			for _, item := range pendingQuestions {
				judgeTrace.DeferredTaskIDs = append(judgeTrace.DeferredTaskIDs, item.TaskID)
			}
		}
	}
	if state.Input.Collector != nil {
		state.Input.Collector.SetKnowledgeEvidenceJudge(judgeTrace)
	}
	state.RetrieveResult = result
	if state.Input.Summary != nil && result != nil {
		state.Input.Summary.RetrieverCount = len(result.Hits)
	}
	if state.Input.Collector != nil && result != nil {
		state.Input.Collector.SetRetrieverSummary(result.TraceSummary)
		state.Input.Collector.AddRetrieverItems(result.TraceItems)
		state.Input.Collector.SetKnowledgeResources(resolveRuntimeKnowledgeResources(state.Input.Request, result))
	}
	if result == nil || len(result.Hits) == 0 || strings.TrimSpace(result.ContextText) == "" {
		markKnowledgeNoContextHandoffDirective(state.Input, "当前酒店业务问题知识库没有可用答案")
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, knowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		state.recordAnswerability(answerabilityStatusNoContext, "no retrieved context", nil)
		return state, nil
	}
	state.Decision = buildKnowledgeGuardDecision(req.AIAgent, result)
	state.prependDecisionInstruction(knowledgeActionInstruction)
	if deferredInstruction != "" {
		state.Decision.Instructions = append(state.Decision.Instructions, schema.SystemMessage(deferredInstruction))
	}
	state.recordAnswerability(answerabilityStatusHasContext, "retrieved context injected", nil)
	return state, nil
}

func markKnowledgeNoContextHandoffDirective(input answerabilityGateInput, reason string) {
	if current := strings.TrimSpace(currentTurnDisplayText(input.Request.UserMessage.Content)); current != "" {
		reason += "；客户消息：" + preview(current, 180)
	}
	if input.Summary != nil {
		input.Summary.handoffDirective = true
		input.Summary.handoffDirectiveReason = reason
		input.Summary.handoffDirectiveSource = "knowledge_no_context"
	}
	if input.Collector == nil {
		return
	}
	ledger := input.Collector.Data.ActionLedger
	ledger.RequestedActions = appendIfMissingActionLedgerItem(ledger.RequestedActions, callbacks.ActionLedgerItem{
		Action: "human_route",
		Status: "requested",
		Reason: reason,
	})
	input.Collector.SetActionLedger(ledger)
}

func topKnowledgeHandoffDirective(result *retrievers.KnowledgeRetrieveResult) (rag.RetrieveResult, bool) {
	if result == nil || len(result.Hits) == 0 {
		return rag.RetrieveResult{}, false
	}
	top := result.Hits[0]
	return top, isKnowledgeHandoffDirectiveContent(top.Content)
}

func isKnowledgeHandoffDirectiveContent(content string) bool {
	answer := normalizeKnowledgeDirectiveAnswer(content)
	return answer == "转接" || answer == "转人工"
}

func normalizeKnowledgeDirectiveAnswer(content string) string {
	answer := strings.TrimSpace(content)
	for _, marker := range []string{"\n答案：", "\n回答：", "\n答案:", "\n回答:"} {
		if index := strings.LastIndex(answer, marker); index >= 0 {
			answer = strings.TrimSpace(answer[index+len(marker):])
			break
		}
	}
	for _, prefix := range []string{"答案：", "回答：", "答案:", "回答:"} {
		if strings.HasPrefix(answer, prefix) {
			answer = strings.TrimSpace(strings.TrimPrefix(answer, prefix))
			break
		}
	}
	answer = strings.Trim(answer, " \t\r\n，,。.!！；;")
	return strings.Join(strings.Fields(answer), "")
}

func markKnowledgeHandoffDirective(input answerabilityGateInput, hit rag.RetrieveResult) {
	reason := "知识库规则要求门店同事接手"
	if topic := strings.TrimSpace(hit.Title); topic != "" {
		reason += "：" + preview(topic, 80)
	}
	if current := strings.TrimSpace(currentTurnDisplayText(input.Request.UserMessage.Content)); current != "" {
		reason += "；客户消息：" + preview(current, 180)
	}
	if input.Summary != nil {
		input.Summary.handoffDirective = true
		input.Summary.handoffDirectiveReason = reason
		input.Summary.handoffDirectiveSource = "knowledge_top_answer"
	}
	if input.Collector == nil {
		return
	}
	ledger := input.Collector.Data.ActionLedger
	ledger.RequestedActions = appendIfMissingActionLedgerItem(ledger.RequestedActions, callbacks.ActionLedgerItem{
		Action: "human_route",
		Status: "requested",
		Reason: reason,
	})
	input.Collector.SetActionLedger(ledger)
}

func removeKnowledgeHandoffDirectiveContexts(result *retrievers.KnowledgeRetrieveResult) {
	if result == nil {
		return
	}
	kept := make([]rag.RetrieveResult, 0, len(result.ContextResults))
	removed := false
	for _, item := range result.ContextResults {
		if isKnowledgeHandoffDirectiveContent(item.Content) {
			removed = true
			continue
		}
		kept = append(kept, item)
	}
	if !removed {
		return
	}
	result.ContextResults = kept
	result.ContextText = strings.TrimSpace(rag.Retrieve.BuildContext(context.Background(), kept, 1<<30))
}

func removeKnowledgeHandoffDirectiveSelection(result *retrievers.KnowledgeRetrieveResult) {
	if result == nil || len(result.Hits) == 0 {
		return
	}
	kept := make([]rag.RetrieveResult, 0, len(result.Hits))
	for _, item := range result.Hits {
		if isKnowledgeHandoffDirectiveContent(item.Content) {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == len(result.Hits) {
		removeKnowledgeHandoffDirectiveContexts(result)
		return
	}
	retrievers.RebuildKnowledgeRetrieveSelection(result, kept)
}

func buildKnowledgePathActionInstruction(req RunInput, intent callbacks.IntentTraceData) string {
	if intent.PrimaryIntent != "hotel_variable" && !intent.NeedsResource {
		return ""
	}
	return buildIntentActionInstruction(req, intent)
}

func buildIntentActionInstruction(req RunInput, intent callbacks.IntentTraceData) string {
	parts := []string{"运行时动作约束：本轮必须由模型生成自然回复，禁止使用固定短答；只允许使用统一意图识别阶段给出的分类、子意图、资源动作和上下文，不得重新发明业务流程。"}
	if intent.PrimaryIntent != "hotel_variable" && (intent.NeedsResource || len(intent.ResourceActions) > 0) {
		parts = append(parts, buildHotelVariableInstruction(req, intent))
	}
	switch intent.PrimaryIntent {
	case "hotel_variable":
		parts = append(parts, buildHotelVariableInstruction(req, intent))
	case "human_complaint_risk":
		if intent.SubIntent == "emergency_safety" {
			parts = append(parts, "人工/投诉/风险-突发安全：这是受伤/摔倒/流血/报警等高风险场景，必须进入接待路由；先安抚并提醒用户不要移动，必要时拨打 120/报警。缺房号/位置时只追问当前位置，同时不得等待知识库。")
		} else {
			parts = append(parts, "人工/投诉/风险：按当前门店托管模式和排班处理；没有工具或路由结果时，不得表达人工动作、通知安排或处理结果已经发生。普通设施/设备问题若知识库命中，知识库优先于人工。")
		}
	case "service_request":
		parts = append(parts, "服务请求：按当前分类提示词处理；普通设施/设备/用品问题先使用知识库。没有知识库或工具结果时，不得承诺派人、送物、维修、叫醒或记录完成。")
	case "interaction":
		if strings.TrimSpace(intent.SubIntent) == "media_context_follow_up" {
			parts = append(parts, "图片/文件上下文：围绕当前问题使用最近图片/文件解析文本，不机械复述 OCR，不说系统识别。语音仍按既有语转文文本链路处理。")
		} else if strings.TrimSpace(intent.SubIntent) == "clarify" || intent.NeedsClarification {
			parts = append(parts, "互动/澄清：只追问一个关键点或给安全短答，不调用知识、变量或人工路由。")
		} else {
			parts = append(parts, "互动：所有闲聊、感谢、确认、表情和非业务互动都自然短句回应，结合最近上下文，不使用固定话术表。")
		}
	}
	return strings.Join(nonEmptyStrings(parts), "\n")
}

func buildHotelVariableInstruction(req RunInput, intent callbacks.IntentTraceData) string {
	return buildHotelVariableInstructionFromInstance(findRuntimeWxWorkInstance(req), req.UserMessage.Content, intent)
}

func buildHotelVariableInstructionFromInstance(instance *models.WxWorkProtocolInstance, currentText string, intent callbacks.IntentTraceData) string {
	resourceTypes := requestedHotelVariableResourceTypes(currentText, intent)
	if len(resourceTypes) == 0 {
		return "酒店变量：当前请求需要门店账号变量，但未识别到具体变量动作。模型只能追问一个关键点，不能编造电话、定位或小程序入口。"
	}
	if intent.NeedsKnowledge {
		return "酒店变量：本轮同时有酒店信息问题和变量请求。电话、定位、小程序等变量由 Commit 阶段按 resourceActions 单独发送真实消息；本阶段只回答停车、早餐、发票、入住流程等知识问题。不要写“定位发你/小程序发你/我这边发你/已经发了/点开就能”，也不要复述变量详情。"
	}
	parts := make([]string, 0, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		switch resourceType {
		case "location":
			parts = append(parts, "酒店变量-定位/地址："+buildLocationResourceContext(instance))
		case "mini_program":
			parts = append(parts, "酒店变量-入住小程序："+buildMiniProgramResourceContext(instance))
		case "phone":
			parts = append(parts, "酒店变量-门店电话："+buildPhoneResourceContext(instance))
		default:
			parts = append(parts, "酒店变量："+resourceType+" 未识别到可用变量动作，不能编造具体值。")
		}
	}
	return strings.Join(parts, "\n")
}

func requestedHotelVariableResourceTypes(currentText string, intent callbacks.IntentTraceData) []string {
	ret := make([]string, 0, 3)
	add := func(resourceType string) {
		resourceType = strings.TrimSpace(resourceType)
		if resourceType == "" || resourceType == "store_variable" || resourceType == "store_group" {
			return
		}
		for _, existing := range ret {
			if existing == resourceType {
				return
			}
		}
		ret = append(ret, resourceType)
	}
	_ = currentText
	for _, action := range intent.ResourceActions {
		add(hotelVariableResourceTypeFromAction(action))
	}
	if len(ret) == 0 {
		switch strings.TrimSpace(intent.ResourceAction) {
		case "provide_location":
			add("location")
		case "send_miniprogram", "provide_mini_program":
			add("mini_program")
		case "provide_phone":
			add("phone")
		default:
			resourceType := strings.TrimSpace(intent.ResourceType)
			add(resourceType)
		}
	}
	return ret
}

func buildMissingMediaContextInstruction(req RunInput, messages []*schema.Message, intent callbacks.IntentTraceData) string {
	if strings.TrimSpace(intent.SubIntent) != "media_context_follow_up" {
		return ""
	}
	text := normalizeGateText(req.UserMessage.Content)
	if text == "" || !isMediaFollowUpIntent(text) || hasUsableMediaUnderstanding(messages) || hasRecentUsableMediaUnderstanding(req) {
		return ""
	}
	if isMediaCorrectionOrComplaint(text) {
		return ""
	}
	switch {
	case containsAny(text, []string{"语音", "听下", "听懂"}):
		return "媒体上下文状态：统一意图识别判定为媒体追问，但当前没有可用语音理解内容。由模型自然说明需要用户用文字补充一句；不要使用固定短答，不要假装听到了。"
	case containsAny(text, []string{"图片", "照片", "图里", "图上", "截图", "看图"}):
		return "媒体上下文状态：统一意图识别判定为媒体追问，但当前没有可用图片理解内容。由模型自然说明需要补发或说明要看的点；不要使用固定短答，不要假装看到了。"
	case containsAny(text, []string{"文件", "附件"}):
		return "媒体上下文状态：统一意图识别判定为媒体追问，但当前没有可用文件理解内容。由模型自然说明需要关键页或具体问题；不要使用固定短答，不要假装读到了。"
	default:
		return "媒体上下文状态：统一意图识别判定为媒体追问，但当前没有可用媒体理解内容。由模型自然说明需要补充具体内容；不要使用固定短答，不要假装看懂。"
	}
}

func hasRecentUsableMediaUnderstanding(req RunInput) bool {
	return findRecentUsableMediaUnderstanding(req) != nil
}

type recentMediaUnderstanding struct {
	MessageType  enums.IMMessageType
	MediaText    string
	MediaSummary string
}

func findRecentUsableMediaUnderstanding(req RunInput) *recentMediaUnderstanding {
	if req.Conversation.ID <= 0 || req.UserMessage.ID <= 0 {
		return nil
	}
	db := sqls.DB()
	if db == nil {
		return nil
	}
	createdAfter := req.UserMessage.CreatedAt.Add(-2 * time.Minute)
	messages := repositories.MessageRepository.Find(db, sqls.NewCnd().
		Eq("conversation_id", req.Conversation.ID).
		Eq("sender_type", string(enums.IMSenderTypeCustomer)).
		In("message_type", []string{string(enums.IMMessageTypeImage), string(enums.IMMessageTypeVoice), string(enums.IMMessageTypeAttachment), string(enums.IMMessageTypeGIF)}).
		Where("id < ? AND created_at >= ?", req.UserMessage.ID, createdAfter).
		Desc("id").
		Limit(8))
	for i := range messages {
		mediaText, mediaSummary, mediaStatus := utils.RuntimeMediaUnderstandingFromPayload(messages[i].Payload)
		if strings.TrimSpace(mediaStatus) == "understood" && (strings.TrimSpace(mediaText) != "" || strings.TrimSpace(mediaSummary) != "") {
			return &recentMediaUnderstanding{
				MessageType:  messages[i].MessageType,
				MediaText:    strings.TrimSpace(mediaText),
				MediaSummary: strings.TrimSpace(mediaSummary),
			}
		}
	}
	return nil
}

func isMediaCorrectionOrComplaint(text string) bool {
	return containsAny(text, []string{"没发语音", "不是语音", "胡乱回", "乱回", "看不到", "看不见", "神经病", "有病", "你在说什么", "什么鬼"})
}

func hasUsableMediaUnderstanding(messages []*schema.Message) bool {
	for _, message := range messages {
		if message == nil {
			continue
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		if strings.Contains(content, "图片内容是") || strings.Contains(content, "图片摘要是") || strings.Contains(content, "语音内容是") || strings.Contains(content, "语音摘要是") || strings.Contains(content, "文件内容是") || strings.Contains(content, "文件摘要是") || strings.Contains(content, "视频理解结果是") || strings.Contains(content, "动画表情理解结果是") || strings.Contains(content, "媒体理解：") {
			return true
		}
	}
	return false
}

func buildLocationResourceContext(instance *models.WxWorkProtocolInstance) string {
	if instance == nil {
		return "当前门店没有绑定定位/地址变量。请直接说明当前账号暂未配置定位，不能编造地址或坐标，不能说让同事发送或稍后处理。"
	}
	name := firstNonEmpty(instance.StoreNavigationName, instance.EmployeeName, "当前门店")
	address := strings.TrimSpace(instance.StoreAddress)
	lng := strings.TrimSpace(instance.StoreLongitude)
	lat := strings.TrimSpace(instance.StoreLatitude)
	if lng == "" || lat == "" {
		if address != "" {
			return "可用地址变量：" + name + "，地址：" + address + "。必须直接用这个地址回答，不能说让同事发送或稍后处理。"
		}
		return "当前门店没有绑定定位/地址变量。请直接说明当前账号暂未配置定位，不能编造地址或坐标，不能说让同事发送或稍后处理。"
	}
	if address != "" {
		return "可用定位变量：" + name + "，地址：" + address + "，坐标：" + lat + ", " + lng + "，地图URI：" + buildAmapMarkerURI(name, lng, lat) + "。必须直接使用这个定位变量回答，不能说发不了链接、让同事发送或稍后处理。"
	}
	return "可用定位变量：" + name + "，坐标：" + lat + ", " + lng + "，地图URI：" + buildAmapMarkerURI(name, lng, lat) + "。必须直接使用这个定位变量回答，不能说发不了链接、让同事发送或稍后处理。"
}

func buildHotelVariableDirectReply(instance *models.WxWorkProtocolInstance, intent callbacks.IntentTraceData, currentText string) string {
	resourceTypes := requestedHotelVariableResourceTypes(currentText, intent)
	if len(resourceTypes) == 0 {
		if fallback := hotelVariableResourceTypeFromIntent(intent); fallback != "" {
			resourceTypes = []string{fallback}
		}
	}
	parts := make([]string, 0, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		switch resourceType {
		case "location":
			parts = append(parts, buildLocationDirectReply(instance))
		case "mini_program":
			parts = append(parts, buildMiniProgramDirectReply(instance))
		case "phone":
			parts = append(parts, buildPhoneDirectReply(instance))
		}
	}
	return strings.Join(nonEmptyStrings(parts), "\n")
}

func hotelVariableResourceTypeFromIntent(intent callbacks.IntentTraceData) string {
	for _, action := range intent.ResourceActions {
		if resourceType := hotelVariableResourceTypeFromAction(action); resourceType != "" {
			return resourceType
		}
	}
	switch strings.TrimSpace(intent.ResourceAction) {
	case "provide_location":
		return "location"
	case "send_miniprogram", "provide_mini_program":
		return "mini_program"
	case "provide_phone":
		return "phone"
	}
	for _, value := range []string{intent.ResourceType, intent.SubIntent} {
		switch strings.TrimSpace(value) {
		case "location":
			return "location"
		case "mini_program":
			return "mini_program"
		case "phone":
			return "phone"
		}
	}
	return ""
}

func hotelVariableResourceTypeFromAction(action string) string {
	switch strings.TrimSpace(action) {
	case "provide_location":
		return "location"
	case "send_miniprogram", "provide_mini_program":
		return "mini_program"
	case "provide_phone":
		return "phone"
	default:
		return ""
	}
}

func buildLocationDirectReply(instance *models.WxWorkProtocolInstance) string {
	if instance == nil {
		return "当前账号暂未配置酒店定位。"
	}
	name := firstNonEmpty(instance.StoreNavigationName, instance.EmployeeName, "酒店")
	address := strings.TrimSpace(instance.StoreAddress)
	lng := strings.TrimSpace(instance.StoreLongitude)
	lat := strings.TrimSpace(instance.StoreLatitude)
	parts := make([]string, 0, 3)
	if address != "" {
		parts = append(parts, name+"地址："+address)
	}
	if lng != "" && lat != "" {
		parts = append(parts, "酒店定位："+buildAmapMarkerURI(name, lng, lat))
	}
	if len(parts) == 0 {
		return "当前账号暂未配置酒店定位。"
	}
	return strings.Join(parts, "。") + "。"
}

func buildMiniProgramResourceContext(instance *models.WxWorkProtocolInstance) string {
	if instance == nil || strings.TrimSpace(instance.DefaultMiniProgramPayload) == "" {
		return "当前门店没有绑定入住小程序变量。请直接说明当前账号暂未配置入住小程序，不能编造入口，不能说让同事发送或稍后处理。"
	}
	return "当前门店已绑定入住小程序变量" + miniProgramPayloadSummary(instance.DefaultMiniProgramPayload) + "。必须围绕这个变量回复，不能说小程序未配置；没有工具发送结果时不能说已经发给你、点开就能用、到前台扫码、微信搜门店名，也不能说让同事发送或稍后处理。"
}

func buildMiniProgramDirectReply(instance *models.WxWorkProtocolInstance) string {
	if instance == nil || strings.TrimSpace(instance.DefaultMiniProgramPayload) == "" {
		return "当前账号暂未配置入住小程序。"
	}
	title := miniProgramPayloadDisplayName(instance.DefaultMiniProgramPayload)
	if title == "" {
		title = "入住小程序"
	}
	return "入住小程序入口：" + title + "。"
}

func buildPhoneResourceContext(instance *models.WxWorkProtocolInstance) string {
	phone := extractRuntimeStorePhone(instance)
	if phone == "" {
		return "当前门店没有配置联系电话变量。请直接说明当前账号暂未配置联系电话，不能编造号码，不能说让同事发送或稍后处理。"
	}
	return "可用联系电话变量：" + phone + "。必须直接回复这个电话，不能说让同事发送或稍后处理。"
}

func buildPhoneDirectReply(instance *models.WxWorkProtocolInstance) string {
	phone := extractRuntimeStorePhone(instance)
	if phone == "" {
		return "当前账号暂未配置联系电话。"
	}
	return "酒店电话：" + phone + "。"
}

func findRuntimeWxWorkInstance(req RunInput) *models.WxWorkProtocolInstance {
	if req.Conversation.ID <= 0 {
		return nil
	}
	db := sqls.DB()
	if db == nil {
		return nil
	}
	route := repositories.ConversationRouteStateRepository.Take(db, "conversation_id = ?", req.Conversation.ID)
	if route == nil || route.WxWorkInstanceID <= 0 {
		return nil
	}
	return repositories.WxWorkProtocolInstanceRepository.Get(db, route.WxWorkInstanceID)
}

func extractRuntimeStorePhone(instance *models.WxWorkProtocolInstance) string {
	if instance == nil {
		return ""
	}
	return utils.RepairMojibakeText(strings.TrimSpace(instance.StoreContactPhone))
}

func buildAmapMarkerURI(name string, lng string, lat string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "当前门店"
	}
	return "https://uri.amap.com/marker?position=" + strings.TrimSpace(lng) + "," + strings.TrimSpace(lat) + "&name=" + url.QueryEscape(name)
}

func miniProgramPayloadSummary(payload string) string {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return ""
	}
	parts := make([]string, 0, 4)
	for _, key := range []string{"title", "appname", "appid", "page_path", "username"} {
		if value, ok := data[key].(string); ok && strings.TrimSpace(value) != "" {
			parts = append(parts, key+"="+strings.TrimSpace(value))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "（" + strings.Join(parts, "，") + "）"
}

func miniProgramPayloadDisplayName(payload string) string {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return ""
	}
	for _, key := range []string{"title", "appname", "username", "appid"} {
		if value, ok := data[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func nonEmptyStrings(values []string) []string {
	ret := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			ret = append(ret, value)
		}
	}
	return ret
}

func isMediaFollowUpIntent(text string) bool {
	return containsAny(text, []string{"图片", "照片", "图里", "图上", "这个", "这是啥", "这是什么", "看下", "帮我看", "识别", "语音", "听下", "文件", "附件", "截图", "表情"})
}

func containsAny(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func normalizeGateText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	return strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "，", "", "。", "", "！", "", "!", "", "？", "", "?", "", "：", "", ":", "").Replace(text)
}

func (s *answerabilityGateState) recordAnswerability(status string, reason string, err error) {
	s.recordAnswerabilityWithLatency(status, reason, err, time.Time{})
}

func (s *answerabilityGateState) prependDecisionInstruction(instruction string) {
	if s == nil {
		return
	}
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return
	}
	s.Decision.Instructions = append([]*schema.Message{schema.SystemMessage(instruction)}, s.Decision.Instructions...)
}

func (s *answerabilityGateState) recordAnswerabilityWithLatency(status string, reason string, err error, started time.Time) {
	if s == nil {
		return
	}
	s.AnswerabilityStatus = strings.TrimSpace(status)
	s.AnswerabilityReason = strings.TrimSpace(reason)
	if s.Input.Collector == nil {
		return
	}
	errorMessage := strings.TrimSpace(s.ErrorMessage)
	if err != nil {
		errorMessage = err.Error()
	}
	data := callbacks.AnswerabilityTraceData{
		Status:       s.AnswerabilityStatus,
		Reason:       s.AnswerabilityReason,
		ErrorMessage: errorMessage,
	}
	if !started.IsZero() {
		data.LatencyMs = time.Since(started).Milliseconds()
	}
	s.Input.Collector.SetAnswerability(data)
}
