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
	"agent-desk/internal/ai/runtime/internal/impl/adapter"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyruntime"
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

	knowledgeEvidenceJudgeBatchCandidateBudget   = 28
	knowledgeEvidenceJudgeDefaultTaskCandidates  = 3
	knowledgeEvidenceJudgeCompoundTaskCandidates = 4

	answerabilityStatusSkipped      = "skipped"
	answerabilityStatusNoContext    = "no_context"
	answerabilityStatusHasContext   = "has_context"
	answerabilityStatusUnanswerable = "unanswerable"

	runtimeKnowledgeDispositionAnswer             = "answer"
	runtimeKnowledgeDispositionAnswerThenHandoff  = "answer_then_handoff"
	runtimeKnowledgeDispositionDirectHandoff      = "knowledge_direct_handoff"
	runtimeKnowledgeDispositionNoEvidenceHandoff  = "no_evidence_handoff"
	runtimeKnowledgeDispositionJudgeProtocolRetry = "judge_protocol_retry"
	runtimeKnowledgeDeferredHandoffOutput         = replyruntime.ManualResumeDeferredKnowledgeOutput
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
	TaskID             string
	Intent             string
	Query              string
	OriginalText       string
	EvidenceQuery      string
	SubIntent          string
	Objective          string
	RelationToPrevious string
	ResolutionState    string
	SourceRefs         []string
	Entities           []callbacks.IntentEntityTraceData
	Result             *retrievers.KnowledgeRetrieveResult
	RetrieveError      error
	Decision           string
	Disposition        string
	MissingAspects     []string
}

type runtimeKnowledgeQuestionSpec struct {
	TaskID             string
	Intent             string
	Query              string
	OriginalText       string
	SubIntent          string
	Objective          string
	RelationToPrevious string
	ResolutionState    string
	SourceRefs         []string
	Entities           []callbacks.IntentEntityTraceData
}

type runtimeKnowledgeRetrieveBatch struct {
	Questions []runtimeKnowledgeQuestionResult
	Merged    *retrievers.KnowledgeRetrieveResult
}

type runtimeKnowledgeQuestionDisposition struct {
	TaskID         string
	Query          string
	HasAnswer      bool
	NeedsHandoff   bool
	NeedsRetry     bool
	Disposition    string
	MissingAspects []string
	HandoffHit     rag.RetrieveResult
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

func retrieveContextForRuntimeQuestions(ctx context.Context, retriever knowledgeContextRetriever, opts retrievers.KnowledgeRetrieveOptions, query string, intent callbacks.IntentTraceData, plans ...callbacks.ReplyPlanTraceData) (*runtimeKnowledgeRetrieveBatch, error) {
	if len(plans) > 0 {
		if questions, ok := runtimeKnowledgeQuestionsFromReplyPlan(plans[0], intent); ok {
			return retrieveContextForRuntimeQuestionList(ctx, retriever, opts, query, questions)
		}
	}
	nonKnowledgeQueries := nonKnowledgeQueriesFromIntentTasks(intent)
	excludedBurstQueries := append([]string(nil), nonKnowledgeQueries...)
	for _, sourceQuery := range knowledgeSourceQueriesFromIntentTasks(intent) {
		excludedBurstQueries = appendRuntimeKnowledgeQuery(excludedBurstQueries, sourceQuery)
	}
	queries := mergeRuntimeKnowledgeQueries(
		query,
		knowledgeQueriesFromIntentTasks(intent),
		excludedBurstQueries,
	)
	if len(queries) == 0 && len(nonKnowledgeQueries) == 0 && strings.TrimSpace(query) != "" {
		queries = []string{strings.TrimSpace(query)}
	}
	questions := make([]runtimeKnowledgeQuestionSpec, 0, len(queries))
	for index, item := range queries {
		spec := runtimeKnowledgeQuestionSpec{
			TaskID: fmt.Sprintf("T%d", index+1),
			Query:  item,
		}
		if intentTask := runtimeKnowledgeIntentTaskForQuery(intent, item); intentTask != nil {
			spec.Intent = canonicalIntentCode(intentTask.Intent)
			spec.OriginalText = strings.TrimSpace(intentTask.Text)
			spec.SubIntent = strings.TrimSpace(intentTask.SubIntent)
			spec.Objective = semanticGateNormalizeObjective(intentTask.Objective)
			spec.RelationToPrevious = semanticGateNormalizeRelation(intentTask.RelationToPrevious)
			spec.ResolutionState = semanticGateNormalizeResolution(intentTask.ResolutionState)
			spec.SourceRefs = append([]string(nil), intentTask.SourceRefs...)
			spec.Entities = append([]callbacks.IntentEntityTraceData(nil), intentTask.Entities...)
		}
		questions = append(questions, spec)
	}
	return retrieveContextForRuntimeQuestionList(ctx, retriever, opts, query, questions)
}

func runtimeKnowledgeQuestionsFromReplyPlan(plan callbacks.ReplyPlanTraceData, intents ...callbacks.IntentTraceData) ([]runtimeKnowledgeQuestionSpec, bool) {
	questions := make([]runtimeKnowledgeQuestionSpec, 0, len(plan.TaskPlans))
	seenTaskIDs := make(map[string]struct{}, len(plan.TaskPlans))
	intent := callbacks.IntentTraceData{}
	if len(intents) > 0 {
		intent = intents[0]
	}
	for _, task := range plan.TaskPlans {
		if !runtimeReplyTaskUsesKnowledge(task) {
			continue
		}
		taskID := strings.TrimSpace(task.TaskID)
		query := strings.TrimSpace(task.ResolvedText)
		if query == "" {
			query = strings.TrimSpace(task.Text)
		}
		if query == "" {
			query = strings.TrimSpace(task.OriginalText)
		}
		if query == "" {
			query = strings.TrimSpace(task.SubIntent)
		}
		if taskID == "" || query == "" {
			return nil, false
		}
		if _, exists := seenTaskIDs[taskID]; exists {
			return nil, false
		}
		seenTaskIDs[taskID] = struct{}{}
		spec := runtimeKnowledgeQuestionSpec{
			TaskID:             taskID,
			Intent:             canonicalIntentCode(task.Intent),
			Query:              query,
			OriginalText:       strings.TrimSpace(task.OriginalText),
			SubIntent:          strings.TrimSpace(task.SubIntent),
			Objective:          semanticGateNormalizeObjective(task.Objective),
			RelationToPrevious: semanticGateNormalizeRelation(task.RelationToPrevious),
			ResolutionState:    semanticGateNormalizeResolution(task.ResolutionState),
			SourceRefs:         append([]string(nil), task.SourceRefs...),
		}
		if intentTask := runtimeKnowledgeIntentTaskForQuery(intent, query); intentTask != nil {
			if spec.OriginalText == "" {
				spec.OriginalText = strings.TrimSpace(intentTask.Text)
			}
			if spec.SubIntent == "" {
				spec.SubIntent = strings.TrimSpace(intentTask.SubIntent)
			}
			if spec.Objective == "" {
				spec.Objective = semanticGateNormalizeObjective(intentTask.Objective)
			}
			if spec.RelationToPrevious == "" {
				spec.RelationToPrevious = semanticGateNormalizeRelation(intentTask.RelationToPrevious)
			}
			if spec.ResolutionState == "" {
				spec.ResolutionState = semanticGateNormalizeResolution(intentTask.ResolutionState)
			}
			if len(spec.SourceRefs) == 0 {
				spec.SourceRefs = append([]string(nil), intentTask.SourceRefs...)
			}
			spec.Entities = append([]callbacks.IntentEntityTraceData(nil), intentTask.Entities...)
		}
		questions = append(questions, spec)
	}
	return questions, len(questions) > 0
}

func mergeRuntimeKnowledgeQueries(query string, taskQueries []string, nonKnowledgeQueries []string) []string {
	if len(taskQueries) > 0 {
		ret := make([]string, 0, len(taskQueries))
		for _, taskQuery := range taskQueries {
			ret = appendRuntimeKnowledgeQuery(ret, taskQuery)
		}
		return ret
	}
	if len(nonKnowledgeQueries) > 0 {
		return nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	// IntentDetect owns semantic task boundaries. If no model task is
	// available, retrieval may use the original turn only as one coarse query;
	// it must not reconstruct omitted tasks from punctuation or keywords.
	return []string{query}
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
		"吗", "么", "呢", "哪", "什么", "啥", "怎么", "咋", "如何", "谁", "多少", "几", "多久", "几点", "为什么", "有没有", "能不能", "可不可以", "是否", "是不是",
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
		query := strings.TrimSpace(task.ResolvedText)
		if query == "" {
			query = strings.TrimSpace(task.Text)
		}
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

func knowledgeSourceQueriesFromIntentTasks(intent callbacks.IntentTraceData) []string {
	ret := make([]string, 0, len(intent.IntentTasks))
	seen := map[string]bool{}
	for _, task := range intent.IntentTasks {
		if task.Intent != "hotel_info" && !task.NeedsKnowledge {
			continue
		}
		query := strings.TrimSpace(task.Text)
		normalized := normalizeRuntimeKnowledgeQuery(query)
		if query == "" || normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
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
	if candidates := currentTurnTaskCandidates(display); len(candidates) > 1 {
		return candidates
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

func retrieveContextForRuntimeQuestionList(ctx context.Context, retriever knowledgeContextRetriever, opts retrievers.KnowledgeRetrieveOptions, originalQuery string, questions []runtimeKnowledgeQuestionSpec) (*runtimeKnowledgeRetrieveBatch, error) {
	batch := &runtimeKnowledgeRetrieveBatch{}
	if len(questions) == 0 {
		batch.Merged = &retrievers.KnowledgeRetrieveResult{
			KnowledgeBaseIDs: append([]int64(nil), retriever.KnowledgeBaseIDs()...),
			Query:            strings.TrimSpace(originalQuery),
			Options:          opts,
		}
		return batch, nil
	}
	results := make([]*retrievers.KnowledgeRetrieveResult, len(questions))
	retrieveErrors := make([]error, len(questions))
	evidenceQueries := make([]string, len(questions))
	var wg sync.WaitGroup
	for i, question := range questions {
		wg.Add(1)
		go func(index int, spec runtimeKnowledgeQuestionSpec) {
			defer wg.Done()
			searchQuery := runtimeIntentEvidenceQuery(spec)
			if searchQuery == "" {
				searchQuery = strings.TrimSpace(spec.Query)
			}
			evidenceQueries[index] = searchQuery
			questionOpts := opts
			questionOpts.QueryPreview = preview(searchQuery, 120)
			if len(questions) > 1 {
				if questionOpts.MaxContextItems <= 0 || questionOpts.MaxContextItems > 2 {
					questionOpts.MaxContextItems = 2
				}
				if questionOpts.TopK <= 0 || questionOpts.TopK > 4 {
					questionOpts.TopK = 4
				}
			}
			result, err := retriever.RetrieveContextByOptions(ctx, questionOpts, searchQuery)
			if err != nil {
				retrieveErrors[index] = err
				return
			}
			results[index] = result
		}(i, question)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	batch.Questions = make([]runtimeKnowledgeQuestionResult, 0, len(questions))
	for index, question := range questions {
		item := runtimeKnowledgeQuestionResult{
			TaskID:             strings.TrimSpace(question.TaskID),
			Intent:             canonicalIntentCode(question.Intent),
			Query:              strings.TrimSpace(question.Query),
			OriginalText:       strings.TrimSpace(question.OriginalText),
			EvidenceQuery:      strings.TrimSpace(evidenceQueries[index]),
			SubIntent:          strings.TrimSpace(question.SubIntent),
			Objective:          semanticGateNormalizeObjective(question.Objective),
			RelationToPrevious: semanticGateNormalizeRelation(question.RelationToPrevious),
			ResolutionState:    semanticGateNormalizeResolution(question.ResolutionState),
			SourceRefs:         append([]string(nil), question.SourceRefs...),
			Entities:           append([]callbacks.IntentEntityTraceData(nil), question.Entities...),
			Result:             results[index],
			RetrieveError:      retrieveErrors[index],
		}
		if item.RetrieveError != nil {
			item.Decision = knowledgeEvidenceDecisionInsufficient
			item.Disposition = runtimeKnowledgeDispositionNoEvidenceHandoff
		}
		batch.Questions = append(batch.Questions, item)
	}
	batch.Merged = mergeRuntimeKnowledgeQuestionResults(retriever.KnowledgeBaseIDs(), opts, originalQuery, batch.Questions)
	return batch, nil
}

func runtimeKnowledgeRetrieveBatchCompleteFailure(batch *runtimeKnowledgeRetrieveBatch) (error, bool) {
	if batch == nil || len(batch.Questions) == 0 {
		return nil, false
	}
	var firstErr error
	for _, question := range batch.Questions {
		if question.RetrieveError == nil {
			return nil, false
		}
		if firstErr == nil {
			firstErr = question.RetrieveError
		}
	}
	return firstErr, firstErr != nil
}

func runtimeIntentEvidenceQuery(spec runtimeKnowledgeQuestionSpec) string {
	query := runtimeIntentRetrievalQuery(spec.Query)
	if query != "" && isExternalProxyActionClassification(spec.Intent, spec.SubIntent, spec.Objective) {
		return query + "；同一目标可用的酒店地址、联系电话、下单入口或自助步骤"
	}
	if query == "" || !runtimeIntentShortKnowledgeLabel(query) {
		return query
	}
	normalizedQuery := normalizeRuntimeKnowledgeQuery(query)
	normalizedSubIntent := strings.ToLower(strings.TrimSpace(spec.SubIntent))
	judgeTask := knowledgeEvidenceJudgeTask{
		Intent:    canonicalIntentCode(spec.Intent),
		Query:     query,
		Objective: semanticGateNormalizeObjective(spec.Objective),
		Entities:  make([]knowledgeEvidenceJudgeEntity, 0, len(spec.Entities)),
	}
	for _, entity := range spec.Entities {
		judgeTask.Entities = append(judgeTask.Entities, knowledgeEvidenceJudgeEntity{Text: entity.Text, Type: entity.Type})
	}
	aspects := requiredKnowledgeEvidenceAspects(judgeTask)
	methodAspect := runtimeKnowledgeAspectsContainAll(aspects, "method")
	switch {
	case strings.Contains(normalizedQuery, "开门") && (strings.Contains(normalizedQuery, "方式") || methodAspect),
		strings.Contains(normalizedSubIntent, "room_access") && methodAspect:
		return "酒店房门怎么打开"
	case strings.Contains(normalizedQuery, "外卖地址") || strings.Contains(normalizedSubIntent, "delivery_address"):
		return "酒店外卖地址怎么填写"
	case strings.Contains(normalizedQuery, "矿泉水") && runtimeKnowledgeAspectsContainAll(aspects, "quantity", "price"):
		return "房间矿泉水有几瓶，是否免费或收费"
	case strings.Contains(normalizedQuery, "wifi") && containsAny(normalizedQuery, []string{"账号", "密码"}):
		return "酒店WiFi账号和密码是什么"
	case strings.Contains(normalizedQuery, "入住") && (strings.Contains(normalizedQuery, "方式") || strings.Contains(normalizedQuery, "流程") || methodAspect),
		(strings.Contains(normalizedSubIntent, "checkin") || strings.Contains(normalizedSubIntent, "check_in")) && methodAspect:
		return "酒店怎么办理入住"
	case strings.Contains(normalizedQuery, "停车") && strings.Contains(normalizedQuery, "充电桩"):
		switch {
		case runtimeKnowledgeAspectsContainAll(aspects, "location"):
			return "酒店停车场和充电桩在哪里"
		case runtimeKnowledgeAspectsContainAll(aspects, "price"):
			return "酒店停车是否收费，是否有充电桩"
		case runtimeKnowledgeAspectsContainAll(aspects, "existence"):
			return "酒店是否有停车场和充电桩"
		default:
			return "酒店停车场和充电桩情况"
		}
	case strings.Contains(normalizedQuery, "发票") && (strings.Contains(normalizedQuery, "方式") || strings.Contains(normalizedQuery, "流程") || methodAspect),
		strings.Contains(normalizedSubIntent, "invoice") && methodAspect:
		return "酒店发票怎么申请"
	}
	subject := runtimeIntentEvidenceSubject(query, spec.Entities)
	if subject == "" {
		return query
	}
	switch {
	case runtimeKnowledgeAspectsContainAll(aspects, "quantity", "price"):
		return "酒店" + subject + "有多少，是否免费或收费"
	case runtimeKnowledgeAspectsContainAll(aspects, "location", "method"):
		return "酒店" + subject + "在哪里，怎么使用"
	case runtimeKnowledgeAspectsContainAll(aspects, "location"):
		return "酒店" + subject + "在哪里"
	case runtimeKnowledgeAspectsContainAll(aspects, "method"):
		return "酒店" + subject + "怎么办理或使用"
	case runtimeKnowledgeAspectsContainAll(aspects, "existence"):
		return "酒店是否有" + subject
	case runtimeKnowledgeAspectsContainAll(aspects, "time"):
		return "酒店" + subject + "时间是什么"
	case runtimeKnowledgeAspectsContainAll(aspects, "price"):
		return "酒店" + subject + "是否免费或收费"
	case runtimeKnowledgeAspectsContainAll(aspects, "quantity"):
		return "酒店" + subject + "有多少"
	default:
		return query
	}
}

func runtimeIntentShortKnowledgeLabel(query string) bool {
	compact := normalizeRuntimeKnowledgeQuery(query)
	if len([]rune(compact)) < 2 || len([]rune(compact)) > 18 {
		return false
	}
	if containsAny(compact, []string{"怎么", "如何", "是否", "有没有", "什么", "哪", "多少", "几", "吗", "呢", "为什么", "为何"}) {
		return false
	}
	return runtimeIntentTaskLabelLooksLikeTask(compact)
}

func runtimeIntentEvidenceSubject(query string, entities []callbacks.IntentEntityTraceData) string {
	normalizedQuery := normalizeRuntimeKnowledgeQuery(query)
	parts := make([]string, 0, len(entities))
	for _, entity := range entities {
		text := strings.TrimSpace(entity.Text)
		if text == "" || runtimeIntentEvidenceGenericEntity(text) || !strings.Contains(normalizedQuery, normalizeRuntimeKnowledgeQuery(text)) {
			continue
		}
		parts = appendIfMissing(parts, text)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "和")
	}
	return strings.TrimSpace(strings.NewReplacer(
		"方式", "",
		"流程", "",
		"数量", "",
		"费用", "",
		"价格", "",
		"时间", "",
		"位置", "",
	).Replace(query))
}

func runtimeIntentEvidenceGenericEntity(text string) bool {
	switch strings.TrimSpace(text) {
	case "酒店", "门店", "房间", "客房", "服务":
		return true
	default:
		return false
	}
}

func runtimeKnowledgeAspectsContainAll(aspects []string, required ...string) bool {
	for _, expected := range required {
		found := false
		for _, aspect := range aspects {
			if aspect == expected {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(required) > 0
}

func mergeRuntimeKnowledgeQuestionResults(knowledgeBaseIDs []int64, opts retrievers.KnowledgeRetrieveOptions, originalQuery string, questions []runtimeKnowledgeQuestionResult) *retrievers.KnowledgeRetrieveResult {
	merged := &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: append([]int64(nil), knowledgeBaseIDs...),
		Query:            strings.TrimSpace(originalQuery),
		Options:          opts,
	}
	seenRawHits := map[string]bool{}
	seenEffectiveHits := map[string]bool{}
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
		effectiveHits := result.EffectiveHits
		if effectiveHits == nil {
			effectiveHits = result.Hits
		}
		merged.EffectiveHits = appendUniqueRuntimeRetrieveResults(merged.EffectiveHits, effectiveHits, seenEffectiveHits)
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
	merged.Hits = append([]rag.RetrieveResult(nil), merged.EffectiveHits...)
	merged.ContextText = strings.TrimSpace(strings.Join(contextSections, "\n\n"))
	if merged.ContextText == "" && len(merged.ContextResults) > 0 {
		merged.ContextText = strings.TrimSpace(rag.Retrieve.BuildContext(context.Background(), merged.ContextResults, 1<<30))
	}
	merged.TraceSummary.HitCount = len(merged.Hits)
	merged.TraceSummary.ContextCount = len(merged.ContextResults)
	return merged
}

func buildKnowledgeEvidenceJudgeTasks(batch *runtimeKnowledgeRetrieveBatch, storeKnowledgeBaseIDs []int64, knowledgeBaseIDs []int64, messages []*schema.Message, currentText string, intents ...callbacks.IntentTraceData) []knowledgeEvidenceJudgeTask {
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
	taskObjectives := make(map[string]string, len(batch.Questions))
	intent := callbacks.IntentTraceData{}
	if len(intents) > 0 {
		intent = intents[0]
	}
	for _, question := range batch.Questions {
		if question.Result == nil {
			continue
		}
		rawHits := question.Result.RawHits
		if len(rawHits) == 0 {
			rawHits = question.Result.Hits
		}
		retrievalQuery := strings.TrimSpace(question.EvidenceQuery)
		if retrievalQuery == "" {
			retrievalQuery = strings.TrimSpace(question.Query)
		}
		item := knowledgeEvidenceJudgeTask{
			TaskID:         question.TaskID,
			Intent:         canonicalIntentCode(question.Intent),
			Query:          strings.TrimSpace(question.Query),
			RetrievalQuery: retrievalQuery,
			SubIntent:      strings.TrimSpace(question.SubIntent),
			Objective:      semanticGateNormalizeObjective(question.Objective),
			SourceContext:  buildKnowledgeEvidenceJudgeSourceContext(messages, currentText, question),
		}
		item.Entities = make([]knowledgeEvidenceJudgeEntity, 0, len(question.Entities))
		for _, entity := range question.Entities {
			item.Entities = append(item.Entities, knowledgeEvidenceJudgeEntity{Text: entity.Text, Type: entity.Type})
		}
		if intentTask := runtimeKnowledgeIntentTaskForQuery(intent, question.Query); intentTask != nil {
			if item.Intent == "" {
				item.Intent = canonicalIntentCode(intentTask.Intent)
			}
			if item.SubIntent == "" {
				item.SubIntent = strings.TrimSpace(intentTask.SubIntent)
			}
			if item.Objective == "" {
				item.Objective = semanticGateNormalizeObjective(intentTask.Objective)
			}
			if len(item.Entities) == 0 {
				item.Entities = make([]knowledgeEvidenceJudgeEntity, 0, len(intentTask.Entities))
				for _, entity := range intentTask.Entities {
					item.Entities = append(item.Entities, knowledgeEvidenceJudgeEntity{Text: entity.Text, Type: entity.Type})
				}
			}
		}
		for _, rawHit := range rawHits {
			for _, hit := range expandKnowledgeEvidenceJudgeHit(rawHit) {
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
		}
		if len(item.Candidates) > 0 {
			item.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), item.Candidates...)
			tasks = append(tasks, item)
			taskObjectives[item.TaskID] = item.Objective
		}
	}
	return limitKnowledgeEvidenceJudgeTaskCandidates(tasks, taskObjectives, knowledgeEvidenceJudgeBatchCandidateBudget)
}

func expandKnowledgeEvidenceJudgeHit(hit rag.RetrieveResult) []rag.RetrieveResult {
	if strings.TrimSpace(hit.FaqQuestion) != "" {
		return []rag.RetrieveResult{hit}
	}
	units := exactKnowledgeEvidenceFAQUnits(hit)
	if len(units) <= 1 {
		return []rag.RetrieveResult{hit}
	}
	expanded := make([]rag.RetrieveResult, 0, len(units))
	for _, unit := range units {
		question := strings.TrimSpace(unit.Question)
		answer := strings.TrimSpace(unit.Answer)
		if question == "" || answer == "" {
			continue
		}
		item := hit
		item.FaqQuestion = question
		item.Content = "问题：" + question + "\n答案：" + answer
		if len(unit.Aliases) > 0 {
			item.Content += "\n相似问法：" + strings.Join(unit.Aliases, "、")
		}
		expanded = append(expanded, item)
	}
	if len(expanded) == 0 {
		return []rag.RetrieveResult{hit}
	}
	return expanded
}

func runtimeKnowledgeObjectiveForQuery(intent callbacks.IntentTraceData, query string) string {
	if task := runtimeKnowledgeIntentTaskForQuery(intent, query); task != nil {
		return semanticGateNormalizeObjective(task.Objective)
	}
	return ""
}

func runtimeKnowledgeIntentTaskForQuery(intent callbacks.IntentTraceData, query string) *callbacks.IntentTaskTraceData {
	normalizedQuery := normalizeRuntimeKnowledgeQuery(query)
	if normalizedQuery == "" {
		return nil
	}
	for index := range intent.IntentTasks {
		task := &intent.IntentTasks[index]
		if task.Intent != "hotel_info" && !task.NeedsKnowledge {
			continue
		}
		for _, candidate := range []string{task.ResolvedText, task.Text, task.SubIntent} {
			if normalizeRuntimeKnowledgeQuery(candidate) == normalizedQuery {
				return task
			}
		}
	}
	return nil
}

func limitKnowledgeEvidenceJudgeTaskCandidates(tasks []knowledgeEvidenceJudgeTask, taskObjectives map[string]string, budget int) []knowledgeEvidenceJudgeTask {
	prepared := make([]knowledgeEvidenceJudgeTask, 0, len(tasks))
	total := 0
	for _, task := range tasks {
		item := task
		item.SourceContext = append([]knowledgeEvidenceJudgeSourceMessage(nil), task.SourceContext...)
		item.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), task.RawCandidates...)
		if len(item.RawCandidates) == 0 {
			item.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), task.Candidates...)
		}
		item.Candidates = compactKnowledgeEvidenceJudgeTaskCandidates(task.Candidates)
		if len(item.Candidates) == 0 {
			continue
		}
		prepared = append(prepared, item)
		total += len(item.Candidates)
	}
	if budget <= 0 {
		return nil
	}
	if total <= budget {
		return prepared
	}

	quotas := allocateKnowledgeEvidenceJudgeCandidateQuotas(prepared, taskObjectives, budget)

	limited := make([]knowledgeEvidenceJudgeTask, 0, len(prepared))
	for taskIndex, task := range prepared {
		item := task
		item.SourceContext = append([]knowledgeEvidenceJudgeSourceMessage(nil), task.SourceContext...)
		item.RawCandidates = append([]knowledgeEvidenceJudgeCandidate(nil), task.RawCandidates...)
		preferDiversity := semanticGateNormalizeObjective(taskObjectives[strings.TrimSpace(task.TaskID)]) == "compound_information"
		item.Candidates = selectKnowledgeEvidenceJudgeTaskCandidates(task, quotas[taskIndex], preferDiversity)
		if len(item.Candidates) > 0 {
			limited = append(limited, item)
		}
	}
	return limited
}

func compactKnowledgeEvidenceJudgeTaskCandidates(candidates []knowledgeEvidenceJudgeCandidate) []knowledgeEvidenceJudgeCandidate {
	if len(candidates) == 0 {
		return nil
	}
	layers := map[string][]knowledgeEvidenceJudgeCandidate{
		knowledgeEvidenceLayerStore:   {},
		knowledgeEvidenceLayerGeneral: {},
	}
	seenByLayer := map[string]map[string]int{
		knowledgeEvidenceLayerStore:   {},
		knowledgeEvidenceLayerGeneral: {},
	}
	for _, candidate := range candidates {
		layer := strings.TrimSpace(candidate.Layer)
		if layer != knowledgeEvidenceLayerStore && layer != knowledgeEvidenceLayerGeneral {
			continue
		}
		dedupKey := knowledgeEvidenceJudgeCandidateDedupKey(candidate.Hit)
		if dedupKey != "" {
			if existingIndex, exists := seenByLayer[layer][dedupKey]; exists {
				if candidate.Hit.Score > layers[layer][existingIndex].Hit.Score {
					layers[layer][existingIndex] = candidate
				}
				continue
			}
			seenByLayer[layer][dedupKey] = len(layers[layer])
		}
		layers[layer] = append(layers[layer], candidate)
	}

	ret := make([]knowledgeEvidenceJudgeCandidate, 0, len(candidates))
	for _, layer := range []string{knowledgeEvidenceLayerStore, knowledgeEvidenceLayerGeneral} {
		ret = append(ret, layers[layer]...)
	}
	return ret
}

func knowledgeEvidenceJudgeCandidateDedupKey(hit rag.RetrieveResult) string {
	question, answer := splitKnowledgeEvidenceFAQ(hit)
	normalizedQuestion := normalizeRuntimeKnowledgeQuery(question)
	normalizedAnswer := normalizeRuntimeKnowledgeQuery(answer)
	if normalizedQuestion != "" && normalizedAnswer != "" {
		return "faq:" + normalizedQuestion + "\x00" + normalizedAnswer
	}
	normalizedRaw := normalizeRuntimeKnowledgeQuery(hit.Content)
	if normalizedRaw == "" {
		return ""
	}
	return "raw:" + normalizedRaw
}

func allocateKnowledgeEvidenceJudgeCandidateQuotas(tasks []knowledgeEvidenceJudgeTask, taskObjectives map[string]string, budget int) []int {
	quotas := make([]int, len(tasks))
	if budget <= 0 || len(tasks) == 0 {
		return quotas
	}
	targets := make([]int, len(tasks))
	compound := make([]bool, len(tasks))
	remainingBudget := budget
	for index, task := range tasks {
		target := knowledgeEvidenceJudgeDefaultTaskCandidates
		if semanticGateNormalizeObjective(taskObjectives[strings.TrimSpace(task.TaskID)]) == "compound_information" {
			target = knowledgeEvidenceJudgeCompoundTaskCandidates
			compound[index] = true
		}
		if target > len(task.Candidates) {
			target = len(task.Candidates)
		}
		targets[index] = target
	}

	// Fill quota levels across all questions before moving to the next level.
	// This preserves broad task coverage while giving compound questions their
	// additional evidence slot without inspecting business wording.
	for level := 1; remainingBudget > 0; level++ {
		added := false
		for index := range tasks {
			if targets[index] < level {
				continue
			}
			quotas[index]++
			remainingBudget--
			added = true
			if remainingBudget == 0 {
				break
			}
		}
		if !added {
			break
		}
	}

	// If a task did not have enough candidates to consume its initial share,
	// deterministically reuse the spare capacity. Compound tasks get the first
	// pass, followed by ordinary tasks, one candidate per task and per round.
	for remainingBudget > 0 {
		added := false
		for _, compoundPass := range []bool{true, false} {
			for index, task := range tasks {
				if compound[index] != compoundPass || quotas[index] >= len(task.Candidates) {
					continue
				}
				quotas[index]++
				remainingBudget--
				added = true
				if remainingBudget == 0 {
					break
				}
			}
			if remainingBudget == 0 {
				break
			}
		}
		if !added {
			break
		}
	}
	return quotas
}

func selectKnowledgeEvidenceJudgeTaskCandidates(task knowledgeEvidenceJudgeTask, quota int, preferDiversity bool) []knowledgeEvidenceJudgeCandidate {
	if quota <= 0 || len(task.Candidates) == 0 {
		return nil
	}
	if quota >= len(task.Candidates) {
		return append([]knowledgeEvidenceJudgeCandidate(nil), task.Candidates...)
	}
	selected := make([]bool, len(task.Candidates))
	selectionOrder := make([]int, 0, quota)
	selectedCount := 0
	selectIndex := func(index int) {
		if index < 0 || index >= len(selected) || selected[index] || selectedCount >= quota {
			return
		}
		selected[index] = true
		selectionOrder = append(selectionOrder, index)
		selectedCount++
	}

	firstLayerIndex := func(layer string) int {
		for index, candidate := range task.Candidates {
			if strings.TrimSpace(candidate.Layer) == layer {
				return index
			}
		}
		return -1
	}

	storeIndex := firstLayerIndex(knowledgeEvidenceLayerStore)
	generalIndex := firstLayerIndex(knowledgeEvidenceLayerGeneral)
	storeExactIndex := bestStrictExactKnowledgeEvidenceJudgeCandidateIndex(task, knowledgeEvidenceLayerStore)
	generalExactIndex := bestStrictExactKnowledgeEvidenceJudgeCandidateIndex(task, knowledgeEvidenceLayerGeneral)
	storeHandoffIndex := bestExactKnowledgeEvidenceJudgeHandoffCandidateIndex(task, knowledgeEvidenceLayerStore)
	generalHandoffIndex := bestExactKnowledgeEvidenceJudgeHandoffCandidateIndex(task, knowledgeEvidenceLayerGeneral)
	storeReviewBodyIndex := bestKnowledgeEvidenceJudgeReviewBodyCandidateIndex(task, knowledgeEvidenceLayerStore, storeHandoffIndex)
	generalReviewBodyIndex := bestKnowledgeEvidenceJudgeReviewBodyCandidateIndex(task, knowledgeEvidenceLayerGeneral, generalHandoffIndex)
	storeCompleteIndex := bestCompleteKnowledgeEvidenceJudgeCandidateIndex(task, knowledgeEvidenceLayerStore)
	generalCompleteIndex := bestCompleteKnowledgeEvidenceJudgeCandidateIndex(task, knowledgeEvidenceLayerGeneral)
	storePairFirst, storePairSecond, storePairComplete := bestCompleteKnowledgeEvidenceJudgeCandidatePairIndexes(task, knowledgeEvidenceLayerStore)
	storeHandoffConflict := storeHandoffIndex >= 0 && storeReviewBodyIndex >= 0 && storeHandoffIndex != storeReviewBodyIndex
	generalHandoffConflict := generalHandoffIndex >= 0 && generalReviewBodyIndex >= 0 && generalHandoffIndex != generalReviewBodyIndex
	storeBestIndex := storeExactIndex
	if storeHandoffConflict {
		storeBestIndex = storeReviewBodyIndex
	}
	if storeBestIndex < 0 {
		storeBestIndex = storeCompleteIndex
	}
	if storeBestIndex < 0 {
		storeBestIndex = storeHandoffIndex
	}
	if storeBestIndex < 0 {
		storeBestIndex = storeIndex
	}
	generalBestIndex := generalExactIndex
	if generalHandoffConflict {
		generalBestIndex = generalReviewBodyIndex
	}
	if generalBestIndex < 0 {
		generalBestIndex = generalCompleteIndex
	}
	if generalBestIndex < 0 {
		generalBestIndex = generalIndex
	}

	// A same-layer pair that is mechanically required to cover the task must
	// reach the Judge together. General fallback visibility cannot consume one
	// of those two slots because the Judge is forbidden from combining layers.
	if quota >= 2 && storeHandoffConflict {
		// A credible same-layer body must reach the Judge with an exact transfer
		// rule. Its score is only a visibility gate; the Judge still owns the
		// answer decision.
		selectIndex(storeReviewBodyIndex)
		selectIndex(storeHandoffIndex)
		if selectedCount < quota {
			selectIndex(generalBestIndex)
		}
	} else if quota >= 2 && storeCompleteIndex < 0 && storePairComplete {
		selectIndex(storePairFirst)
		selectIndex(storePairSecond)
		if selectedCount < quota {
			selectIndex(generalBestIndex)
		}
	} else if quota >= 2 && storeBestIndex < 0 && generalHandoffConflict {
		selectIndex(generalReviewBodyIndex)
		selectIndex(generalHandoffIndex)
	} else if quota >= 2 && storeBestIndex >= 0 && generalBestIndex >= 0 {
		// Keep both knowledge layers visible whenever doing so does not remove
		// evidence required for a complete store-layer answer.
		selectIndex(storeBestIndex)
		selectIndex(generalBestIndex)
	} else if storeBestIndex >= 0 {
		selectIndex(storeBestIndex)
	} else {
		selectIndex(generalBestIndex)
	}

	// If the store layer contains an exact transfer rule and a competing factual
	// answer, preserve the second store candidate when another slot is available.
	// RawCandidates remains the authority for deterministic conflict checks.
	if selectedCount < quota && storeHandoffConflict {
		selectIndex(storeReviewBodyIndex)
		selectIndex(storeHandoffIndex)
	}
	if selectedCount < quota && storeBestIndex < 0 && generalHandoffConflict {
		selectIndex(generalReviewBodyIndex)
		selectIndex(generalHandoffIndex)
	}

	fillLayer := func(layer string, limit int) {
		for selectedCount < limit {
			index := nextKnowledgeEvidenceJudgeCandidateIndex(task.Candidates, selected, layer, preferDiversity)
			if index < 0 {
				break
			}
			selectIndex(index)
		}
	}
	fillLayer(knowledgeEvidenceLayerStore, quota)
	fillLayer(knowledgeEvidenceLayerGeneral, quota)

	ret := make([]knowledgeEvidenceJudgeCandidate, 0, selectedCount)
	if preferDiversity {
		for _, layer := range []string{knowledgeEvidenceLayerStore, knowledgeEvidenceLayerGeneral} {
			for _, index := range selectionOrder {
				if strings.TrimSpace(task.Candidates[index].Layer) == layer {
					ret = append(ret, task.Candidates[index])
				}
			}
		}
		return ret
	}
	for index, candidate := range task.Candidates {
		if selected[index] {
			ret = append(ret, candidate)
		}
	}
	return ret
}

func bestStrictExactKnowledgeEvidenceJudgeCandidateIndex(task knowledgeEvidenceJudgeTask, layer string) int {
	selection, ok := strictExactKnowledgeEvidenceFAQSelection(task, layer)
	if !ok || len(selection.SelectedCandidateIDs) != 1 {
		return -1
	}
	selectedCandidateID := strings.TrimSpace(selection.SelectedCandidateIDs[0])
	for index, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.CandidateID) == selectedCandidateID {
			return index
		}
	}
	return -1
}

func bestCompleteKnowledgeEvidenceJudgeCandidateIndex(task knowledgeEvidenceJudgeTask, layer string) int {
	bestIndex := -1
	bestQuestionMatch := 0.0
	for index, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		_, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		questionMatch, ok := knowledgeEvidenceJudgeCandidateCompletesTask(task, candidate)
		if !ok {
			questionMatch, ok = knowledgeEvidenceJudgeReviewCandidateCompletesTask(task, candidate)
		}
		if !ok {
			continue
		}
		if bestIndex < 0 || questionMatch > bestQuestionMatch+0.02 ||
			(questionMatch >= bestQuestionMatch-0.02 && candidate.Hit.Score > task.Candidates[bestIndex].Hit.Score) {
			bestIndex = index
			bestQuestionMatch = questionMatch
		}
	}
	return bestIndex
}

func bestCompleteKnowledgeEvidenceJudgeCandidatePairIndexes(task knowledgeEvidenceJudgeTask, layer string) (int, int, bool) {
	requiredAspects := requiredKnowledgeEvidenceAspects(task)
	requiredSubjects := requiredKnowledgeEvidenceSubjectEntities(task)
	configurationFields := knowledgeEvidenceConfigurationFields(task.Query)
	if len(requiredAspects) < 2 && len(requiredSubjects) < 2 && len(configurationFields) < 2 {
		return -1, -1, false
	}
	type candidateFacts struct {
		index    int
		question string
		answer   string
		facts    []knowledgeEvidenceFact
	}
	prepared := make([]candidateFacts, 0, len(task.Candidates))
	for index, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		question, answer := splitKnowledgeEvidenceFAQForQuery(candidate.Hit, task.Query)
		if strings.TrimSpace(question) == "" || strings.TrimSpace(answer) == "" || isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		facts := deterministicKnowledgeEvidenceFactsFromFAQ(task.TaskID, answer)
		facts = enrichKnowledgeEvidenceFactsFromFAQUnit(task, question, answer, facts)
		facts = groundedKnowledgeEvidenceFacts(task, layer, []string{candidate.CandidateID}, facts)
		facts = finalizeKnowledgeEvidenceFactsForTask(task, facts)
		if len(facts) == 0 {
			continue
		}
		prepared = append(prepared, candidateFacts{index: index, question: question, answer: answer, facts: facts})
	}
	for leftIndex := 0; leftIndex < len(prepared); leftIndex++ {
		for rightIndex := leftIndex + 1; rightIndex < len(prepared); rightIndex++ {
			left := prepared[leftIndex]
			right := prepared[rightIndex]
			if knowledgeEvidenceFAQAnswersConflict(left.answer, right.answer) ||
				knowledgeEvidenceConfigurationValuesConflict(task.Query, left.answer, right.answer) {
				continue
			}
			selectedIDs := []string{task.Candidates[left.index].CandidateID, task.Candidates[right.index].CandidateID}
			if !knowledgeEvidenceSelectedCandidatesMatchTaskSubjects(task, layer, selectedIDs, knowledgeEvidenceDecisionDirectCombined) {
				continue
			}
			facts := append(append([]knowledgeEvidenceFact(nil), left.facts...), right.facts...)
			facts = canonicalizeKnowledgeEvidenceFacts(sanitizeKnowledgeEvidenceFacts(facts))
			if len(missingRequiredKnowledgeEvidenceAspects(task, facts)) == 0 {
				return left.index, right.index, true
			}
		}
	}
	return -1, -1, false
}

func bestExactKnowledgeEvidenceJudgeHandoffCandidateIndex(task knowledgeEvidenceJudgeTask, layer string) int {
	bestIndex := -1
	bestQuestionMatch := 0.0
	for index, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		question, answer, exact := exactKnowledgeEvidenceFAQMatch(candidate.Hit, task.Query)
		if !exact || !isKnowledgeHandoffDirectiveContent(answer) {
			continue
		}
		questionMatch := knowledgeEvidenceFAQQuestionMatchScore(question, task.Query)
		if bestIndex < 0 || questionMatch > bestQuestionMatch+0.02 ||
			(questionMatch >= bestQuestionMatch-0.02 && candidate.Hit.Score > task.Candidates[bestIndex].Hit.Score) {
			bestIndex = index
			bestQuestionMatch = questionMatch
		}
	}
	return bestIndex
}

func bestKnowledgeEvidenceJudgeReviewBodyCandidateIndex(task knowledgeEvidenceJudgeTask, layer string, handoffIndex int) int {
	if handoffIndex < 0 || handoffIndex >= len(task.Candidates) {
		return -1
	}
	handoffQuestion, handoffAnswer := splitKnowledgeEvidenceFAQForQuery(task.Candidates[handoffIndex].Hit, task.Query)
	bestIndex := -1
	for index, candidate := range task.Candidates {
		if index == handoffIndex || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		if !knowledgeEvidenceJudgeReviewWorthyBodyPeer(task, candidate, handoffQuestion, handoffAnswer) {
			continue
		}
		if bestIndex < 0 || candidate.Hit.Score > task.Candidates[bestIndex].Hit.Score {
			bestIndex = index
		}
	}
	return bestIndex
}

func nextKnowledgeEvidenceJudgeCandidateIndex(candidates []knowledgeEvidenceJudgeCandidate, selected []bool, layer string, preferDiversity bool) int {
	bestIndex := -1
	if !preferDiversity {
		for index, candidate := range candidates {
			if !selected[index] && strings.TrimSpace(candidate.Layer) == strings.TrimSpace(layer) {
				return index
			}
		}
		return -1
	}

	selectedIndexes := make([]int, 0, len(candidates))
	for index, candidate := range candidates {
		if selected[index] && strings.TrimSpace(candidate.Layer) == strings.TrimSpace(layer) {
			selectedIndexes = append(selectedIndexes, index)
		}
	}
	if len(selectedIndexes) == 0 {
		for index, candidate := range candidates {
			if selected[index] || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
				continue
			}
			if bestIndex < 0 || candidate.Hit.Score > candidates[bestIndex].Hit.Score {
				bestIndex = index
			}
		}
		return bestIndex
	}

	bestDiversity := -1.0
	for index, candidate := range candidates {
		if selected[index] || strings.TrimSpace(candidate.Layer) != strings.TrimSpace(layer) {
			continue
		}
		minimumDiversity := 1.0
		for _, selectedIndex := range selectedIndexes {
			diversity := 1 - knowledgeEvidenceCandidateSemanticSimilarity(candidate, candidates[selectedIndex])
			if diversity < minimumDiversity {
				minimumDiversity = diversity
			}
		}
		if bestIndex < 0 || minimumDiversity > bestDiversity ||
			(minimumDiversity == bestDiversity && candidate.Hit.Score > candidates[bestIndex].Hit.Score) {
			bestIndex = index
			bestDiversity = minimumDiversity
		}
	}
	return bestIndex
}

func knowledgeEvidenceCandidateSemanticSimilarity(left knowledgeEvidenceJudgeCandidate, right knowledgeEvidenceJudgeCandidate) float64 {
	leftQuestion, leftAnswer := knowledgeEvidenceCandidateSemanticParts(left.Hit)
	rightQuestion, rightAnswer := knowledgeEvidenceCandidateSemanticParts(right.Hit)
	questionSimilarity := knowledgeEvidenceTextNGramSimilarity(leftQuestion, rightQuestion)
	answerSimilarity := knowledgeEvidenceTextNGramSimilarity(leftAnswer, rightAnswer)
	return answerSimilarity*0.72 + questionSimilarity*0.28
}

func knowledgeEvidenceCandidateSemanticParts(hit rag.RetrieveResult) (string, string) {
	question, answer := splitKnowledgeEvidenceFAQ(hit)
	raw := strings.TrimSpace(hit.Content)
	if strings.TrimSpace(question) == "" {
		question = raw
	}
	if strings.TrimSpace(answer) == "" {
		answer = raw
	}
	return normalizeRuntimeKnowledgeQuery(question), normalizeRuntimeKnowledgeQuery(answer)
}

func knowledgeEvidenceTextNGramSimilarity(left string, right string) float64 {
	leftSet := knowledgeEvidenceTextNGrams(left)
	rightSet := knowledgeEvidenceTextNGrams(right)
	if len(leftSet) == 0 && len(rightSet) == 0 {
		return 1
	}
	if len(leftSet) == 0 || len(rightSet) == 0 {
		return 0
	}
	intersection := 0
	for token := range leftSet {
		if _, ok := rightSet[token]; ok {
			intersection++
		}
	}
	union := len(leftSet) + len(rightSet) - intersection
	if union == 0 {
		return 1
	}
	return float64(intersection) / float64(union)
}

func knowledgeEvidenceTextNGrams(text string) map[string]struct{} {
	runes := []rune(normalizeRuntimeKnowledgeQuery(text))
	ret := make(map[string]struct{})
	if len(runes) == 0 {
		return ret
	}
	if len(runes) == 1 {
		ret[string(runes)] = struct{}{}
		return ret
	}
	for index := 0; index+1 < len(runes); index++ {
		ret[string(runes[index:index+2])] = struct{}{}
	}
	return ret
}

func buildKnowledgeEvidenceJudgeSourceContext(messages []*schema.Message, currentText string, question runtimeKnowledgeQuestionResult) []knowledgeEvidenceJudgeSourceMessage {
	primary := strings.TrimSpace(question.Query)
	if primary == "" {
		primary = strings.TrimSpace(currentText)
	}
	items := make([]knowledgeEvidenceJudgeSourceMessage, 0, runtimeAdjacentServiceReplyLimit+2)
	previousCustomer, previousAssistants, hasAdjacentExchange := knowledgeEvidenceAdjacentSourceExchange(messages)
	adjacentText := previousCustomer + "\n" + strings.Join(previousAssistants, "\n")
	if hasAdjacentExchange && knowledgeEvidenceQuestionUsesAdjacentContext(question, adjacentText) {
		if previousCustomer != "" {
			items = append(items, knowledgeEvidenceJudgeSourceMessage{Role: "customer", Content: preview(previousCustomer, 400)})
		}
		for _, previousAssistant := range previousAssistants {
			items = append(items, knowledgeEvidenceJudgeSourceMessage{Role: "assistant", Content: preview(previousAssistant, 600)})
		}
	}
	if primary != "" {
		items = append(items, knowledgeEvidenceJudgeSourceMessage{Role: "customer_current", Content: preview(primary, 600)})
	}
	return items
}

func knowledgeEvidenceAdjacentSourceExchange(messages []*schema.Message) (string, []string, bool) {
	entries := boundedGenerationHistoryEntries(adapter.HistoryBuildResult{Messages: messages})
	adjacent := adjacentGenerationHistoryEntries(entries)
	if len(adjacent) < 2 || adjacent[0].speaker != "客户" {
		return "", nil, false
	}
	replies := make([]string, 0, len(adjacent)-1)
	for _, entry := range adjacent[1:] {
		if entry.speaker != "AI客服" && entry.speaker != "人工客服" {
			return "", nil, false
		}
		replies = append(replies, entry.text)
	}
	return adjacent[0].text, replies, len(replies) > 0
}

func knowledgeEvidenceQuestionUsesAdjacentContext(question runtimeKnowledgeQuestionResult, adjacentContext string) bool {
	relation := semanticGateNormalizeRelation(question.RelationToPrevious)
	resolution := semanticGateNormalizeResolution(question.ResolutionState)
	if relation == "" && resolution == "" {
		original := normalizeRuntimeKnowledgeQuery(question.OriginalText)
		resolved := normalizeRuntimeKnowledgeQuery(question.Query)
		if original == "" || resolved == "" || original == resolved || strings.TrimSpace(adjacentContext) == "" ||
			!runtimeIntentAtomicCandidateRequiresContext(question.OriginalText) {
			return false
		}
		legacyTask := runtimeIntentTaskJSON{
			Text:         question.OriginalText,
			ResolvedText: question.Query,
		}
		for _, entity := range question.Entities {
			legacyTask.Entities = append(legacyTask.Entities, runtimeIntentEntityJSON{Text: entity.Text, Type: entity.Type})
		}
		return runtimeIntentProtocolResolvedReferenceGroundedInText(legacyTask, question.OriginalText, adjacentContext)
	}
	if resolution == runtimeIntentResolutionResolvedFromContext {
		return !(relation == "independent" && len(question.SourceRefs) > 1)
	}
	switch relation {
	case "follow_up", "clarification_answer", "reference_previous", "correction", "modify_previous", "cancel_previous", "answer_rejected":
		return true
	default:
		return false
	}
}

func applyKnowledgeEvidenceJudgeOutcome(batch *runtimeKnowledgeRetrieveBatch, tasks []knowledgeEvidenceJudgeTask, outcome knowledgeEvidenceJudgeOutcome) callbacks.KnowledgeEvidenceJudgeTraceData {
	trace := outcome.Trace
	if batch == nil {
		return trace
	}
	if !outcome.Applied {
		selections := failedKnowledgeEvidenceLayerSelections(tasks, knowledgeEvidenceDecisionMalformed)
		repaired := repairExactFAQFallbackSelections(tasks, selections)
		outcome.Applied = true
		outcome.Selections = selections
		trace.Status = knowledgeEvidenceDecisionMalformed
		trace.Reason = strings.TrimSpace(trace.Reason + fmt.Sprintf("; invalid judge outcome preserved retrieval and recovered %d strict exact-FAQ selection(s)", repaired))
	}
	repairExactFAQFallbackSelections(tasks, outcome.Selections)
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
		selections := normalizeAppliedKnowledgeEvidenceSelections(task, outcome.Selections[task.TaskID])
		outcome.Selections[task.TaskID] = selections
		allCandidates := allKnowledgeEvidenceJudgeTaskCandidates(task)
		candidateByID := make(map[string]knowledgeEvidenceJudgeCandidate, len(allCandidates))
		for _, candidate := range allCandidates {
			candidateByID[candidate.CandidateID] = candidate
		}
		for layer, selection := range selections {
			selections[layer] = reconcileSelectedFAQGuidanceFactsForTask(task, layer, selection, candidateByID)
		}
		taskTrace := callbacks.KnowledgeEvidenceJudgeTaskTraceData{
			TaskID:         task.TaskID,
			QueryPreview:   preview(task.Query, 120),
			CandidateCount: len(task.Candidates),
		}
		selectedLayer := selectKnowledgeEvidenceLayer(selections, candidateByID, task.Query)
		disposition := runtimeKnowledgeDispositionNoEvidenceHandoff
		if selectedLayer == "" && knowledgeEvidenceSelectionsNeedProtocolRetry(selections) {
			disposition = runtimeKnowledgeDispositionJudgeProtocolRetry
		}
		for _, layer := range []string{knowledgeEvidenceLayerStore, knowledgeEvidenceLayerGeneral} {
			selection, ok := selections[layer]
			if !ok {
				continue
			}
			taskTrace.Layers = append(taskTrace.Layers, callbacks.KnowledgeEvidenceJudgeLayerTraceData{
				Layer:                layer,
				CandidateCount:       knowledgeEvidenceTaskLayerCandidateCount(task, layer),
				Decision:             selection.Decision,
				DecisionSource:       selection.DecisionSource,
				SelectedCandidateIDs: append([]string(nil), selection.SelectedCandidateIDs...),
				SupportedFacts:       knowledgeEvidenceFactsToTrace(selection.SupportedFacts),
				MissingAspects:       append([]string(nil), selection.MissingAspects...),
			})
		}
		selectedHits := make([]rag.RetrieveResult, 0)
		selectedSelection := knowledgeEvidenceLayerSelection{}
		if selectedLayer != "" {
			selection := selections[selectedLayer]
			externalProxyPartial := selection.Decision == knowledgeEvidenceDecisionPartial &&
				isExternalProxyActionClassification(task.Intent, task.SubIntent, task.Objective)
			if externalProxyPartial {
				selection.MissingAspects = nil
			}
			selectedSelection = selection
			taskTrace.Decision = selection.Decision
			taskTrace.DecisionSource = selection.DecisionSource
			taskTrace.SupportedFacts = knowledgeEvidenceFactsToTrace(selection.SupportedFacts)
			taskTrace.MissingAspects = append([]string(nil), selection.MissingAspects...)
			for _, candidateID := range selection.SelectedCandidateIDs {
				candidate, ok := candidateByID[candidateID]
				if !ok || candidate.Layer != selectedLayer {
					continue
				}
				selectedHits = append(selectedHits, knowledgeEvidenceHitForQuery(candidate.Hit, task.Query))
				taskTrace.SelectedCandidateIDs = append(taskTrace.SelectedCandidateIDs, candidateID)
			}
			switch {
			case selectionHasHandoffDirective(selection, selectedLayer, candidateByID, task.Query):
				disposition = runtimeKnowledgeDispositionDirectHandoff
			case externalProxyPartial:
				disposition = runtimeKnowledgeDispositionAnswer
			case selection.Decision == knowledgeEvidenceDecisionPartial:
				disposition = runtimeKnowledgeDispositionAnswerThenHandoff
			default:
				disposition = runtimeKnowledgeDispositionAnswer
			}
		}
		taskTrace.SelectedLayer = selectedLayer
		taskTrace.Disposition = disposition
		if selectedLayer == "" {
			taskTrace.Decision, taskTrace.DecisionSource = knowledgeEvidenceTaskFailureDecisionAndSource(selections)
		}
		retrievers.RebuildKnowledgeRetrieveSelection(question.Result, selectedHits)
		appendKnowledgeEvidenceFactBoundary(question.Result, task.TaskID, selectedSelection)
		question.Decision = taskTrace.Decision
		question.Disposition = disposition
		question.MissingAspects = append([]string(nil), selectedSelection.MissingAspects...)
		trace.Tasks = append(trace.Tasks, taskTrace)
	}
	batch.Merged = mergeRuntimeKnowledgeQuestionResults(batch.Merged.KnowledgeBaseIDs, batch.Merged.Options, batch.Merged.Query, batch.Questions)
	return trace
}

func normalizeAppliedKnowledgeEvidenceSelections(task knowledgeEvidenceJudgeTask, selections map[string]knowledgeEvidenceLayerSelection) map[string]knowledgeEvidenceLayerSelection {
	expected := knowledgeEvidenceExpectedCandidatesByLayer(task)
	ret := failedKnowledgeEvidenceLayerSelectionsForExpected(expected, knowledgeEvidenceDecisionProtocolInvalid)
	for layer, expectedCandidates := range expected {
		selection, ok := selections[layer]
		if !ok {
			continue
		}
		decision := strings.TrimSpace(selection.Decision)
		switch decision {
		case knowledgeEvidenceDecisionDirectSingle, knowledgeEvidenceDecisionDirectCombined, knowledgeEvidenceDecisionPartial, knowledgeEvidenceDecisionInsufficient,
			knowledgeEvidenceDecisionProtocolInvalid, knowledgeEvidenceDecisionTimeout, knowledgeEvidenceDecisionMalformed:
		default:
			continue
		}
		validCandidates := true
		seen := make(map[string]struct{}, len(selection.SelectedCandidateIDs))
		for _, rawCandidateID := range selection.SelectedCandidateIDs {
			candidateID := strings.TrimSpace(rawCandidateID)
			if _, exists := expectedCandidates[candidateID]; !exists {
				validCandidates = false
				break
			}
			if _, duplicate := seen[candidateID]; duplicate {
				validCandidates = false
				break
			}
			seen[candidateID] = struct{}{}
		}
		if !validCandidates || (decision == knowledgeEvidenceDecisionDirectCombined && len(selection.SelectedCandidateIDs) < 2) {
			continue
		}
		if decision == knowledgeEvidenceDecisionProtocolInvalid || decision == knowledgeEvidenceDecisionTimeout || decision == knowledgeEvidenceDecisionMalformed {
			selection.SelectedCandidateIDs = nil
			selection.SupportedFacts = nil
			selection.MissingAspects = nil
			if strings.TrimSpace(selection.DecisionSource) == "" {
				selection.DecisionSource = decision
			}
		}
		for index := range selection.SupportedFacts {
			if !isKnowledgeEvidenceFactAspect(strings.TrimSpace(selection.SupportedFacts[index].Aspect)) {
				selection.SupportedFacts[index].Aspect = "other"
			}
		}
		ret[layer] = selection
	}
	return ret
}

func knowledgeEvidenceSelectionsNeedProtocolRetry(selections map[string]knowledgeEvidenceLayerSelection) bool {
	for _, layer := range []string{knowledgeEvidenceLayerStore, knowledgeEvidenceLayerGeneral} {
		switch strings.TrimSpace(selections[layer].Decision) {
		case knowledgeEvidenceDecisionProtocolInvalid, knowledgeEvidenceDecisionTimeout, knowledgeEvidenceDecisionMalformed:
			return true
		}
	}
	return false
}

func knowledgeEvidenceTaskFailureDecisionAndSource(selections map[string]knowledgeEvidenceLayerSelection) (string, string) {
	for _, decision := range []string{knowledgeEvidenceDecisionProtocolInvalid, knowledgeEvidenceDecisionTimeout, knowledgeEvidenceDecisionMalformed} {
		for _, layer := range []string{knowledgeEvidenceLayerStore, knowledgeEvidenceLayerGeneral} {
			selection := selections[layer]
			if strings.TrimSpace(selection.Decision) == decision {
				source := strings.TrimSpace(selection.DecisionSource)
				if source == "" {
					source = decision
				}
				return decision, source
			}
		}
	}
	for _, layer := range []string{knowledgeEvidenceLayerStore, knowledgeEvidenceLayerGeneral} {
		selection := selections[layer]
		if strings.TrimSpace(selection.Decision) == knowledgeEvidenceDecisionInsufficient {
			return knowledgeEvidenceDecisionInsufficient, strings.TrimSpace(selection.DecisionSource)
		}
	}
	return knowledgeEvidenceDecisionInsufficient, ""
}

func knowledgeEvidenceTaskLayerCandidateCount(task knowledgeEvidenceJudgeTask, layer string) int {
	count := 0
	for _, candidate := range task.Candidates {
		if strings.TrimSpace(candidate.Layer) == strings.TrimSpace(layer) {
			count++
		}
	}
	return count
}

func knowledgeEvidenceFactsToTrace(facts []knowledgeEvidenceFact) []callbacks.KnowledgeEvidenceFactTraceData {
	ret := make([]callbacks.KnowledgeEvidenceFactTraceData, 0, len(facts))
	for _, fact := range facts {
		ret = append(ret, callbacks.KnowledgeEvidenceFactTraceData{
			FactID:         strings.TrimSpace(fact.FactID),
			Aspect:         strings.TrimSpace(fact.Aspect),
			Statement:      strings.TrimSpace(fact.Statement),
			CriticalValues: append([]string(nil), fact.CriticalValues...),
		})
	}
	return ret
}

func selectKnowledgeEvidenceLayer(selections map[string]knowledgeEvidenceLayerSelection, candidates map[string]knowledgeEvidenceJudgeCandidate, query string) string {
	storeSelection := selections[knowledgeEvidenceLayerStore]
	generalSelection := selections[knowledgeEvidenceLayerGeneral]
	if selectionHasHandoffDirective(storeSelection, knowledgeEvidenceLayerStore, candidates, query) {
		return knowledgeEvidenceLayerStore
	}
	if selectionHasCompleteEvidence(storeSelection) {
		return knowledgeEvidenceLayerStore
	}
	if selectionHasCompleteEvidence(generalSelection) {
		return knowledgeEvidenceLayerGeneral
	}
	if selectionHasPartialEvidence(storeSelection) {
		return knowledgeEvidenceLayerStore
	}
	if selectionHasPartialEvidence(generalSelection) {
		return knowledgeEvidenceLayerGeneral
	}
	return ""
}

func selectionHasHandoffDirective(selection knowledgeEvidenceLayerSelection, layer string, candidates map[string]knowledgeEvidenceJudgeCandidate, query string) bool {
	if !selectionHasCompleteEvidence(selection) {
		return false
	}
	return selectedKnowledgeEvidenceHandoffCandidateMatches(query, layer, selection.SelectedCandidateIDs, candidates)
}

func selectionHasCompleteEvidence(selection knowledgeEvidenceLayerSelection) bool {
	switch selection.Decision {
	case knowledgeEvidenceDecisionDirectSingle, knowledgeEvidenceDecisionDirectCombined:
		return len(selection.SelectedCandidateIDs) > 0
	default:
		return false
	}
}

func selectionHasPartialEvidence(selection knowledgeEvidenceLayerSelection) bool {
	return selection.Decision == knowledgeEvidenceDecisionPartial && len(selection.SelectedCandidateIDs) > 0
}

func appendKnowledgeEvidenceFactBoundary(result *retrievers.KnowledgeRetrieveResult, taskID string, selection knowledgeEvidenceLayerSelection) {
	if result == nil || (len(selection.SupportedFacts) == 0 && len(selection.MissingAspects) == 0) {
		return
	}
	lines := []string{"【知识证据事实边界 " + strings.TrimSpace(taskID) + "】"}
	if len(selection.SupportedFacts) > 0 {
		lines = append(lines, "以下是证据明确支持的事实，只能在这些事实维度内回答：")
		for _, fact := range selection.SupportedFacts {
			line := "- " + strings.TrimSpace(fact.FactID) + " [" + strings.TrimSpace(fact.Aspect) + "] " + strings.TrimSpace(fact.Statement)
			if len(fact.CriticalValues) > 0 {
				line += "；回复不可遗漏的原文值：" + strings.Join(fact.CriticalValues, "、")
			}
			lines = append(lines, line)
		}
	}
	if len(selection.MissingAspects) > 0 {
		lines = append(lines,
			"以下方面没有被当前证据确认，不得推测、补全或作出承诺："+strings.Join(selection.MissingAspects, "；"),
		)
	}
	lines = append(lines, "存在性、数量、价格、时间、位置、方式、范围和条件是不同事实维度；一个维度不能推导另一个维度。")
	boundary := strings.TrimSpace(strings.Join(lines, "\n"))
	if contextText := strings.TrimSpace(result.ContextText); contextText != "" {
		result.ContextText = contextText + "\n\n" + boundary
	} else {
		result.ContextText = boundary
	}
}

func runtimeKnowledgeQuestionDispositions(batch *runtimeKnowledgeRetrieveBatch) []runtimeKnowledgeQuestionDisposition {
	if batch == nil {
		return nil
	}
	items := make([]runtimeKnowledgeQuestionDisposition, 0, len(batch.Questions))
	for _, question := range batch.Questions {
		item := runtimeKnowledgeQuestionDisposition{TaskID: question.TaskID, Query: question.Query, Disposition: question.Disposition}
		result := question.Result
		switch question.Disposition {
		case runtimeKnowledgeDispositionJudgeProtocolRetry:
			item.NeedsRetry = true
			items = append(items, item)
			continue
		case runtimeKnowledgeDispositionDirectHandoff:
			item.NeedsHandoff = true
			if hit, ok := topKnowledgeHandoffDirective(result); ok {
				item.HandoffHit = hit
			}
			items = append(items, item)
			continue
		case runtimeKnowledgeDispositionNoEvidenceHandoff:
			item.NeedsHandoff = true
			items = append(items, item)
			continue
		case runtimeKnowledgeDispositionAnswerThenHandoff:
			item.HasAnswer = true
			item.NeedsHandoff = true
			item.MissingAspects = append([]string(nil), question.MissingAspects...)
			items = append(items, item)
			continue
		case runtimeKnowledgeDispositionAnswer:
			item.HasAnswer = true
			items = append(items, item)
			continue
		}
		item.Disposition = runtimeKnowledgeDispositionJudgeProtocolRetry
		item.NeedsRetry = true
		items = append(items, item)
	}
	return items
}

func splitRuntimeKnowledgeQuestionDispositions(items []runtimeKnowledgeQuestionDisposition) (answered int, pending []runtimeKnowledgeQuestionDisposition, retry []runtimeKnowledgeQuestionDisposition) {
	for _, item := range items {
		if item.HasAnswer {
			answered++
		}
		if item.NeedsHandoff {
			pending = append(pending, item)
		}
		if item.NeedsRetry {
			retry = append(retry, item)
		}
	}
	return answered, pending, retry
}

func rebuildRuntimeKnowledgeReplyPlan(
	plan callbacks.ReplyPlanTraceData,
	questions []runtimeKnowledgeQuestionResult,
	pending []runtimeKnowledgeQuestionDisposition,
	excludePending bool,
) callbacks.ReplyPlanTraceData {
	if !runtimeReplyPlanHasStableKnowledgeTaskIDs(plan) {
		return rebuildLegacyRuntimeKnowledgeReplyPlan(plan, questions, pending, excludePending)
	}

	pendingTasks := make(map[string]runtimeKnowledgeQuestionDisposition, len(pending))
	if excludePending {
		for _, item := range pending {
			if !item.HasAnswer {
				pendingTasks[strings.TrimSpace(item.TaskID)] = item
			}
		}
	}
	questionsByTaskID := make(map[string]runtimeKnowledgeQuestionResult, len(questions))
	for _, question := range questions {
		taskID := strings.TrimSpace(question.TaskID)
		if taskID != "" {
			questionsByTaskID[taskID] = question
		}
	}

	rebuilt := make([]callbacks.ReplyTaskPlanTraceData, 0, len(plan.TaskPlans))
	for _, task := range plan.TaskPlans {
		if !runtimeReplyTaskUsesKnowledge(task) {
			rebuilt = append(rebuilt, task)
			continue
		}
		taskID := strings.TrimSpace(task.TaskID)
		pendingTask, deferred := pendingTasks[taskID]
		question, hasQuestion := questionsByTaskID[taskID]
		if !hasQuestion && !deferred {
			continue
		}
		query := ""
		if hasQuestion {
			query = strings.TrimSpace(question.Query)
		}
		if query == "" && deferred {
			query = strings.TrimSpace(pendingTask.Query)
		}
		if query == "" {
			query = activeGenerationTaskText(task)
		}
		if query == "" && !deferred {
			continue
		}
		if query != "" {
			if strings.TrimSpace(task.Text) == "" {
				task.Text = strings.TrimSpace(task.OriginalText)
				if task.Text == "" {
					task.Text = strings.TrimSpace(question.OriginalText)
				}
				if task.Text == "" {
					task.Text = query
				}
			}
			task.ResolvedText = query
			if strings.TrimSpace(task.OriginalText) == "" {
				task.OriginalText = task.Text
			}
		}
		if deferred {
			task.Output = runtimeKnowledgeDeferredHandoffOutput
			task.OutputKind = "handoff"
			task.ReplyRequired = false
			task.SelectedLayer = ""
			task.SelectedCandidateIDs = nil
			task.SupportedFacts = nil
			task.MissingAspects = append([]string(nil), pendingTask.MissingAspects...)
			rebuilt = append(rebuilt, task)
			continue
		}
		task.Output = "knowledge_text_reply"
		if strings.TrimSpace(task.Intent) == "" {
			task.Intent = "hotel_info"
		}
		rebuilt = append(rebuilt, task)
	}
	plan.TaskPlans = rebuilt
	plan.ActiveTaskCount = len(rebuilt)
	plan.ReplyRequiredTaskCount = countReplyRequiredTasks(rebuilt)
	return plan
}

func runtimeReplyPlanHasStableKnowledgeTaskIDs(plan callbacks.ReplyPlanTraceData) bool {
	seen := make(map[string]struct{}, len(plan.TaskPlans))
	hasKnowledgeTask := false
	for _, task := range plan.TaskPlans {
		if !runtimeReplyTaskUsesKnowledge(task) {
			continue
		}
		hasKnowledgeTask = true
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			return false
		}
		if _, exists := seen[taskID]; exists {
			return false
		}
		seen[taskID] = struct{}{}
	}
	return hasKnowledgeTask
}

func rebuildLegacyRuntimeKnowledgeReplyPlan(
	plan callbacks.ReplyPlanTraceData,
	questions []runtimeKnowledgeQuestionResult,
	pending []runtimeKnowledgeQuestionDisposition,
	excludePending bool,
) callbacks.ReplyPlanTraceData {
	pendingTasks := make(map[string]runtimeKnowledgeQuestionDisposition, len(pending))
	if excludePending {
		for _, item := range pending {
			if !item.HasAnswer {
				pendingTasks[strings.TrimSpace(item.TaskID)] = item
			}
		}
	}

	usedPlanTasks := make([]bool, len(plan.TaskPlans))
	activeKnowledgeTasks := make([]callbacks.ReplyTaskPlanTraceData, 0, len(questions)+len(pendingTasks))
	addedTaskIDs := make(map[string]struct{}, len(questions)+len(pendingTasks))
	for _, question := range questions {
		taskID := strings.TrimSpace(question.TaskID)
		pendingTask, deferred := pendingTasks[taskID]
		query := strings.TrimSpace(question.Query)
		if query == "" && !deferred {
			continue
		}
		originalText := strings.TrimSpace(question.OriginalText)
		customerText := originalText
		if customerText == "" {
			customerText = query
		}
		task := callbacks.ReplyTaskPlanTraceData{
			TaskID:             taskID,
			Intent:             canonicalIntentCode(question.Intent),
			SubIntent:          strings.TrimSpace(question.SubIntent),
			Objective:          semanticGateNormalizeObjective(question.Objective),
			RelationToPrevious: semanticGateNormalizeRelation(question.RelationToPrevious),
			ResolutionState:    semanticGateNormalizeResolution(question.ResolutionState),
			Entities:           append([]callbacks.IntentEntityTraceData(nil), question.Entities...),
			Text:               customerText,
			OriginalText:       customerText,
			ResolvedText:       query,
			SourceRefs:         append([]string(nil), question.SourceRefs...),
			NeedsKnowledge:     true,
			OutputKind:         "text",
			ReplyRequired:      true,
			Output:             "knowledge_text_reply",
		}
		if task.Intent == "" {
			task.Intent = "hotel_info"
		}
		matchedPlanIndex := -1
		for index, candidate := range plan.TaskPlans {
			if usedPlanTasks[index] || !runtimeReplyTaskUsesKnowledge(candidate) || !runtimeKnowledgeQueryCovers(activeGenerationTaskText(candidate), query) {
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
			task.TaskID = taskID
			if strings.TrimSpace(task.Text) == "" {
				task.Text = customerText
			}
			task.ResolvedText = query
			if strings.TrimSpace(task.OriginalText) == "" {
				task.OriginalText = task.Text
			}
			if strings.TrimSpace(task.Intent) == "" {
				task.Intent = canonicalIntentCode(question.Intent)
				if strings.TrimSpace(task.Intent) == "" {
					task.Intent = "hotel_info"
				}
			}
		}
		task.NeedsKnowledge = true
		if deferred {
			task.Output = runtimeKnowledgeDeferredHandoffOutput
			task.OutputKind = "handoff"
			task.ReplyRequired = false
			task.SelectedLayer = ""
			task.SelectedCandidateIDs = nil
			task.SupportedFacts = nil
			task.MissingAspects = append([]string(nil), pendingTask.MissingAspects...)
		} else {
			task.Output = "knowledge_text_reply"
			task.OutputKind = "text"
			task.ReplyRequired = true
		}
		activeKnowledgeTasks = append(activeKnowledgeTasks, task)
		if taskID != "" {
			addedTaskIDs[taskID] = struct{}{}
		}
	}
	for _, pendingTask := range pending {
		taskID := strings.TrimSpace(pendingTask.TaskID)
		if !excludePending || pendingTask.HasAnswer {
			continue
		}
		if _, exists := addedTaskIDs[taskID]; exists {
			continue
		}
		query := strings.TrimSpace(pendingTask.Query)
		task := callbacks.ReplyTaskPlanTraceData{
			TaskID:         taskID,
			Intent:         "hotel_info",
			Text:           query,
			OriginalText:   query,
			ResolvedText:   query,
			NeedsKnowledge: true,
		}
		for index, candidate := range plan.TaskPlans {
			if usedPlanTasks[index] || !runtimeReplyTaskUsesKnowledge(candidate) {
				continue
			}
			candidateTaskID := strings.TrimSpace(candidate.TaskID)
			candidateQuery := activeGenerationTaskText(candidate)
			if candidateTaskID != "" && candidateTaskID != taskID {
				continue
			}
			if query != "" && candidateQuery != "" && !runtimeKnowledgeQueryCovers(candidateQuery, query) && !runtimeKnowledgeQueryCovers(query, candidateQuery) {
				continue
			}
			usedPlanTasks[index] = true
			task = candidate
			break
		}
		task.TaskID = taskID
		if strings.TrimSpace(task.Intent) == "" {
			task.Intent = "hotel_info"
		}
		if query != "" {
			if strings.TrimSpace(task.Text) == "" {
				task.Text = strings.TrimSpace(task.OriginalText)
				if task.Text == "" {
					task.Text = query
				}
			}
			if strings.TrimSpace(task.OriginalText) == "" {
				task.OriginalText = task.Text
			}
			task.ResolvedText = query
		}
		task.NeedsKnowledge = true
		task.Output = runtimeKnowledgeDeferredHandoffOutput
		task.OutputKind = "handoff"
		task.ReplyRequired = false
		task.SelectedLayer = ""
		task.SelectedCandidateIDs = nil
		task.SupportedFacts = nil
		task.MissingAspects = append([]string(nil), pendingTask.MissingAspects...)
		activeKnowledgeTasks = append(activeKnowledgeTasks, task)
		if taskID != "" {
			addedTaskIDs[taskID] = struct{}{}
		}
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
	plan.ActiveTaskCount = len(rebuilt)
	plan.ReplyRequiredTaskCount = countReplyRequiredTasks(rebuilt)
	return plan
}

func applyKnowledgeEvidenceJudgeTraceToReplyPlan(plan callbacks.ReplyPlanTraceData, trace callbacks.KnowledgeEvidenceJudgeTraceData, questions []runtimeKnowledgeQuestionResult) callbacks.ReplyPlanTraceData {
	if len(plan.TaskPlans) == 0 || len(trace.Tasks) == 0 {
		return plan
	}
	traceByTaskID := make(map[string]callbacks.KnowledgeEvidenceJudgeTaskTraceData, len(trace.Tasks))
	questionByTaskID := make(map[string]string, len(questions))
	for _, question := range questions {
		questionByTaskID[strings.TrimSpace(question.TaskID)] = strings.TrimSpace(question.Query)
	}
	for _, task := range trace.Tasks {
		traceByTaskID[strings.TrimSpace(task.TaskID)] = task
	}
	used := make(map[string]bool, len(trace.Tasks))
	for index := range plan.TaskPlans {
		planTask := &plan.TaskPlans[index]
		if !runtimeReplyTaskUsesKnowledge(*planTask) || strings.TrimSpace(planTask.Output) == runtimeKnowledgeDeferredHandoffOutput {
			continue
		}
		planTaskID := strings.TrimSpace(planTask.TaskID)
		matched, ok := traceByTaskID[planTaskID]
		if planTaskID == "" {
			matched, ok = matchKnowledgeEvidenceTraceTask(*planTask, trace.Tasks, questionByTaskID, used)
		}
		matchedTaskID := strings.TrimSpace(matched.TaskID)
		if !ok || used[matchedTaskID] || strings.TrimSpace(matched.SelectedLayer) == "" {
			continue
		}
		used[matchedTaskID] = true
		if planTaskID == "" {
			planTask.TaskID = matchedTaskID
		}
		planTask.SelectedLayer = matched.SelectedLayer
		planTask.SelectedCandidateIDs = append([]string(nil), matched.SelectedCandidateIDs...)
		planTask.SupportedFacts = append([]callbacks.KnowledgeEvidenceFactTraceData(nil), matched.SupportedFacts...)
		for factIndex := range planTask.SupportedFacts {
			planTask.SupportedFacts[factIndex].CriticalValues = append([]string(nil), matched.SupportedFacts[factIndex].CriticalValues...)
		}
		planTask.MissingAspects = append([]string(nil), matched.MissingAspects...)
	}
	return plan
}

func matchKnowledgeEvidenceTraceTask(planTask callbacks.ReplyTaskPlanTraceData, traces []callbacks.KnowledgeEvidenceJudgeTaskTraceData, questions map[string]string, used map[string]bool) (callbacks.KnowledgeEvidenceJudgeTaskTraceData, bool) {
	query := strings.TrimSpace(planTask.ResolvedText)
	if query == "" {
		query = strings.TrimSpace(planTask.Text)
	}
	if query != "" {
		for _, task := range traces {
			taskID := strings.TrimSpace(task.TaskID)
			if used[taskID] || strings.TrimSpace(task.SelectedLayer) == "" {
				continue
			}
			candidateQuery := strings.TrimSpace(questions[taskID])
			if candidateQuery != "" && (runtimeKnowledgeQueryCovers(query, candidateQuery) || runtimeKnowledgeQueryCovers(candidateQuery, query)) {
				return task, true
			}
		}
	}
	return callbacks.KnowledgeEvidenceJudgeTaskTraceData{}, false
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
		if !item.HasAnswer {
			pendingSet[item.TaskID] = struct{}{}
		}
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
	fullLabels := make([]string, 0, len(pending))
	partialLabels := make([]string, 0, len(pending))
	for _, item := range pending {
		label := preview(strings.TrimSpace(item.Query), 80)
		if item.HasAnswer && len(item.MissingAspects) > 0 {
			missing := strings.Join(item.MissingAspects, "、")
			if label != "" {
				partialLabels = append(partialLabels, label+"（仅缺："+missing+"）")
			} else {
				partialLabels = append(partialLabels, missing)
			}
			continue
		}
		if label != "" {
			fullLabels = append(fullLabels, label)
		}
	}
	reason := "部分酒店业务问题需要门店同事接手"
	if len(fullLabels) > 0 {
		reason += "；完整待处理问题：" + strings.Join(fullLabels, "；")
	}
	if len(partialLabels) > 0 {
		reason += "；仅待确认缺失方面：" + strings.Join(partialLabels, "；")
	}
	return reason
}

func buildDeferredRuntimeKnowledgeInstruction(pending []runtimeKnowledgeQuestionDisposition, willRequestHandoff bool) string {
	fullLabels := make([]string, 0, len(pending))
	partialLabels := make([]string, 0, len(pending))
	for _, item := range pending {
		if item.HasAnswer && len(item.MissingAspects) > 0 {
			label := strings.TrimSpace(item.TaskID)
			if label == "" {
				label = "当前保留任务"
			}
			partialLabels = append(partialLabels, label+" 缺少："+strings.Join(item.MissingAspects, "、"))
			continue
		}
		if label := preview(strings.TrimSpace(item.Query), 80); label != "" {
			fullLabels = append(fullLabels, label)
		}
	}
	if len(fullLabels) == 0 && len(partialLabels) == 0 {
		return ""
	}
	if willRequestHandoff {
		parts := []string{"【部分问题处理边界】"}
		if len(fullLabels) > 0 {
			parts = append(parts,
				"部分完整无答案的原子问题已作为非文本 Deferred Task 保留，并由系统单独执行转接；若缺少必要房号，系统会先追问房号。",
				"这些 Deferred Task 仅供转接和后续恢复使用，不属于本次 Generate 的文本任务；不得猜测、复述、概括或提及。",
			)
		}
		if len(partialLabels) > 0 {
			parts = append(parts,
				"以下任务仍保留在 active ReplyPlan，必须回答其 supportedFacts；系统只对缺失方面单独转接："+strings.Join(partialLabels, "；")+"。",
				"不得猜测、补全或承诺这些 missingAspects，也不得因为存在缺失方面而省略已经确认的事实。",
			)
		}
		parts = append(parts,
			"不得声称已经记录、登记、受理、处理、联系、安排或转接。",
			"系统会在本条知识答案提交成功后单独执行直接转接或必要的房号追问，本次 Generate 不要重复输出转接、房号追问或成功话术。",
		)
		return strings.Join(parts, "\n")
	}
	parts := []string{"【部分问题处理边界】"}
	if len(fullLabels) > 0 {
		parts = append(parts,
			"以下问题当前没有可靠直接知识，或胜出知识明确要求门店同事接手："+strings.Join(fullLabels, "；")+"。",
			"本次只回答已经提供直接知识证据的其他问题；不得猜测这些待处理问题，不得把其他问题的答案挪过来。",
		)
	}
	if len(partialLabels) > 0 {
		parts = append(parts,
			"以下任务仍须回答 supportedFacts，但不得猜测其缺失方面："+strings.Join(partialLabels, "；")+"。",
			"可以自然说明缺失方面暂时无法确认，但不能省略已确认事实。",
		)
	}
	parts = append(parts, "不得声称已经记录、登记、受理、处理、联系、安排或转接。")
	parts = append(parts, "当前会话不允许自动转人工；对这些问题只可自然说明暂时无法确认，不要承诺后续动作。")
	return strings.Join(parts, "\n")
}

func buildRuntimeKnowledgeProtocolIsolationDecision(retry []runtimeKnowledgeQuestionDisposition) knowledgeGuardDecision {
	labels := make([]string, 0, len(retry))
	for _, item := range retry {
		taskID := strings.TrimSpace(item.TaskID)
		if taskID == "" {
			taskID = "当前知识任务"
		}
		if query := preview(strings.TrimSpace(item.Query), 80); query != "" {
			labels = append(labels, taskID+"（"+query+"）")
		} else {
			labels = append(labels, taskID)
		}
	}
	if len(labels) == 0 {
		return knowledgeGuardDecision{}
	}
	instruction := "【知识证据裁决隔离】\n" +
		"仅以下任务的 Judge 结果发生协议异常，不能使用这些任务的原始候选资料：" + strings.Join(labels, "；") + "。\n" +
		"这些任务只能使用 active ReplyPlan 中 runtime_safe_fallback 提供的固定安全事实，不得自行补充酒店事实，也不得把其他任务的证据挪过来。\n" +
		"此异常不代表整个知识库不可用；其他已选知识答案、结构化资源和已经确定的接待动作继续按各自计划执行。\n" +
		"不得因为该异常新增、取消或重复转人工，也不得向客户解释 Judge、协议、候选或内部处理过程。"
	return knowledgeGuardDecision{Instructions: []*schema.Message{schema.SystemMessage(instruction)}}
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

func resolveRuntimeKnowledgeResources(req RunInput, batch *runtimeKnowledgeRetrieveBatch) []callbacks.KnowledgeResourceTraceData {
	if batch == nil || batch.Merged == nil || len(batch.Merged.ContextResults) == 0 {
		return nil
	}
	result := batch.Merged
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
			TaskIDs:         runtimeKnowledgeResourceTaskIDs(batch, item.KnowledgeBaseID, item.SourceRecordID),
		})
	}
	return ret
}

func runtimeKnowledgeResourceSourceKey(knowledgeBaseID int64, sourceRecordID string) string {
	sourceRecordID = strings.TrimSpace(sourceRecordID)
	if knowledgeBaseID <= 0 || sourceRecordID == "" {
		return ""
	}
	return fmt.Sprintf("%d:%s", knowledgeBaseID, sourceRecordID)
}

func runtimeKnowledgeResourceTaskIDs(batch *runtimeKnowledgeRetrieveBatch, knowledgeBaseID int64, sourceRecordID string) []string {
	key := runtimeKnowledgeResourceSourceKey(knowledgeBaseID, sourceRecordID)
	if batch == nil || key == "" {
		return nil
	}
	ret := make([]string, 0)
	for _, question := range batch.Questions {
		taskID := strings.TrimSpace(question.TaskID)
		if taskID == "" || question.Result == nil {
			continue
		}
		for _, item := range question.Result.ContextResults {
			if runtimeKnowledgeResourceSourceKey(item.KnowledgeBaseID, item.SourceRecordID) == key {
				ret = appendIfMissing(ret, taskID)
				break
			}
		}
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
		if shouldMarkUnavailableKnowledgeHandoff(state, "当前酒店业务问题没有配置可用知识库") {
			markKnowledgeNoContextHandoffDirective(state.Input, "当前酒店业务问题没有配置可用知识库")
		}
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, configuredKnowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		appendUnavailableExternalProxyInstruction(state)
		state.recordAnswerability(answerabilityStatusNoContext, "intent requires knowledge but no knowledge configured", nil)
		return state, nil
	}
	retriever := gate.newRetriever(req.AIAgent)
	state.KnowledgeIDs = append([]int64(nil), configuredKnowledgeIDs...)
	if retriever == nil {
		if shouldMarkUnavailableKnowledgeHandoff(state, "当前酒店业务问题知识检索器不可用") {
			markKnowledgeNoContextHandoffDirective(state.Input, "当前酒店业务问题知识检索器不可用")
		}
		state.Decision = buildKnowledgeRetrievalErrorDecision(req.AIAgent, configuredKnowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		appendUnavailableExternalProxyInstruction(state)
		state.recordAnswerability(answerabilityStatusUnanswerable, "knowledge retriever unavailable", nil)
		return state, nil
	}
	knowledgeIDs := retriever.KnowledgeBaseIDs()
	state.KnowledgeIDs = append([]int64(nil), knowledgeIDs...)
	if len(knowledgeIDs) == 0 {
		if shouldMarkUnavailableKnowledgeHandoff(state, "当前酒店业务问题没有可用知识库") {
			markKnowledgeNoContextHandoffDirective(state.Input, "当前酒店业务问题没有可用知识库")
		}
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, configuredKnowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		appendUnavailableExternalProxyInstruction(state)
		state.recordAnswerability(answerabilityStatusNoContext, "intent requires knowledge but retriever has no knowledge", nil)
		return state, nil
	}
	query := currentRuntimeIntentSemanticText(req)
	if query == "" {
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, knowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		state.recordAnswerability(answerabilityStatusNoContext, "empty user question", nil)
		return state, nil
	}
	retrieveOptions := retrievers.DefaultKnowledgeRetrieveOptions()
	retrieveOptions.QueryPreview = preview(query, 120)
	replyPlans := make([]callbacks.ReplyPlanTraceData, 0, 1)
	if state.Input.Collector != nil {
		replyPlans = append(replyPlans, state.Input.Collector.Data.Pipeline.ReplyPlan)
	}
	batch, err := retrieveContextForRuntimeQuestions(ctx, retriever, retrieveOptions, query, intent, replyPlans...)
	if err != nil {
		if shouldMarkUnavailableKnowledgeHandoff(state, "当前酒店业务问题知识检索失败") {
			markKnowledgeNoContextHandoffDirective(state.Input, "当前酒店业务问题知识检索失败")
		}
		state.Decision = buildKnowledgeRetrievalErrorDecision(req.AIAgent, knowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		appendUnavailableExternalProxyInstruction(state)
		state.ErrorMessage = err.Error()
		state.recordAnswerability(answerabilityStatusUnanswerable, "knowledge retrieval failed", err)
		return state, nil
	}
	if retrieveErr, allFailed := runtimeKnowledgeRetrieveBatchCompleteFailure(batch); allFailed {
		if shouldMarkUnavailableKnowledgeHandoff(state, "当前酒店业务问题知识检索失败") {
			markKnowledgeNoContextHandoffDirective(state.Input, "当前酒店业务问题知识检索失败")
		}
		state.Decision = buildKnowledgeRetrievalErrorDecision(req.AIAgent, knowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		appendUnavailableExternalProxyInstruction(state)
		state.ErrorMessage = retrieveErr.Error()
		state.recordAnswerability(answerabilityStatusUnanswerable, "knowledge retrieval failed", retrieveErr)
		return state, nil
	}
	result := batch.Merged
	rawCandidateCount := runtimeRetrieverRawCandidateCount(result)
	syncRetrieverTrace := func(current *retrievers.KnowledgeRetrieveResult) {
		if current == nil || state.Input.Collector == nil {
			return
		}
		traceSummary := current.TraceSummary
		traceSummary.HitCount = rawCandidateCount
		traceSummary.ContextCount = len(current.ContextResults)
		state.Input.Collector.SetRetrieverSummary(traceSummary)
		state.Input.Collector.SetRetrieverItems(current.TraceItems)
	}
	if result != nil {
		if state.Input.Summary != nil {
			state.Input.Summary.RetrieverCount = rawCandidateCount
		}
		syncRetrieverTrace(result)
	}
	storeKnowledgeBaseIDs := utils.SplitInt64s(req.AIAgent.KnowledgeIDs)
	judgeTasks := buildKnowledgeEvidenceJudgeTasks(
		batch,
		storeKnowledgeBaseIDs,
		knowledgeIDs,
		state.Input.Messages,
		query,
		intent,
	)
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
	judgeTrace = appendRuntimeKnowledgeUnjudgedTaskTrace(judgeTrace, batch)
	externalProxyBoundaryTaskIDs := routeExternalProxyNoEvidenceAsCapabilityBoundary(batch, &judgeTrace)
	dispositions := runtimeKnowledgeQuestionDispositions(batch)
	batch.Merged = mergeRuntimeKnowledgeQuestionResults(batch.Merged.KnowledgeBaseIDs, batch.Merged.Options, batch.Merged.Query, batch.Questions)
	result = batch.Merged
	answeredQuestionCount, pendingQuestions, retryQuestions := splitRuntimeKnowledgeQuestionDispositions(dispositions)
	deferredInstruction := ""
	independentNonKnowledgeWork := state.Input.Collector != nil &&
		runtimeReplyPlanHasIndependentNonKnowledgeWork(state.Input.Collector.Data.Pipeline.ReplyPlan)
	autoHandoffEnabled := len(pendingQuestions) > 0 &&
		services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(req.Conversation.ID)
	if len(retryQuestions) > 0 {
		clearDeferredRuntimeKnowledgeQuestions(batch, retryQuestions)
		result = batch.Merged
	}
	if len(pendingQuestions) > 0 && answeredQuestionCount == 0 && len(retryQuestions) == 0 {
		if state.Input.Collector != nil && autoHandoffEnabled {
			activePlan := rebuildRuntimeKnowledgeReplyPlan(
				state.Input.Collector.Data.Pipeline.ReplyPlan,
				batch.Questions,
				pendingQuestions,
				true,
			)
			activePlan = applyKnowledgeEvidenceJudgeTraceToReplyPlan(activePlan, judgeTrace, batch.Questions)
			state.Input.Collector.SetReplyPlan(activePlan)
			judgeTrace.DeferredHandoff = true
			judgeTrace.DeferredHandoffReason = deferredRuntimeKnowledgeHandoffReason(pendingQuestions)
			for _, item := range pendingQuestions {
				judgeTrace.DeferredTaskIDs = appendIfMissing(judgeTrace.DeferredTaskIDs, item.TaskID)
			}
		}
		clearDeferredRuntimeKnowledgeQuestions(batch, pendingQuestions)
		result = batch.Merged
		if state.Input.Collector != nil {
			state.Input.Collector.SetKnowledgeEvidenceJudge(judgeTrace)
		}
		state.RetrieveResult = result
		syncRetrieverTrace(result)
		if independentNonKnowledgeWork && autoHandoffEnabled {
			state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, knowledgeIDs)
			state.prependDecisionInstruction(knowledgeActionInstruction)
			state.recordAnswerability(answerabilityStatusNoContext, "knowledge evidence unavailable; independent non-knowledge work was preserved before deferred handoff", nil)
			return state, nil
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
	willRequestHandoff := autoHandoffEnabled
	if state.Input.Collector != nil {
		activePlan := rebuildRuntimeKnowledgeReplyPlan(
			state.Input.Collector.Data.Pipeline.ReplyPlan,
			batch.Questions,
			pendingQuestions,
			willRequestHandoff,
		)
		activePlan = applyKnowledgeEvidenceJudgeTraceToReplyPlan(activePlan, judgeTrace, batch.Questions)
		activePlan = convertExternalProxyCapabilityBoundaryTasks(activePlan, externalProxyBoundaryTaskIDs)
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
	syncRetrieverTrace(result)
	if state.Input.Collector != nil {
		state.Input.Collector.SetKnowledgeEvidenceJudge(judgeTrace)
	}
	state.RetrieveResult = result
	if state.Input.Collector != nil && result != nil {
		state.Input.Collector.SetKnowledgeResources(resolveRuntimeKnowledgeResources(state.Input.Request, batch))
	}
	if result == nil || len(result.Hits) == 0 || strings.TrimSpace(result.ContextText) == "" {
		if len(retryQuestions) > 0 {
			state.Decision = buildRuntimeKnowledgeProtocolIsolationDecision(retryQuestions)
			state.prependDecisionInstruction(knowledgeActionInstruction)
			if deferredInstruction != "" {
				state.Decision.Instructions = append(state.Decision.Instructions, schema.SystemMessage(deferredInstruction))
			}
			state.ErrorMessage = "knowledge evidence judge protocol failed for isolated task(s)"
			state.recordAnswerability(answerabilityStatusUnanswerable, "knowledge evidence judge protocol failure was isolated to affected task(s)", nil)
			return state, nil
		}
		if len(externalProxyBoundaryTaskIDs) > 0 {
			state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, knowledgeIDs)
			state.prependDecisionInstruction(knowledgeActionInstruction)
			state.Decision.Instructions = append(state.Decision.Instructions, schema.SystemMessage(buildExternalProxyCapabilityBoundaryInstruction(externalProxyBoundaryTaskIDs)))
			if deferredInstruction != "" {
				state.Decision.Instructions = append(state.Decision.Instructions, schema.SystemMessage(deferredInstruction))
			}
			state.recordAnswerability(answerabilityStatusNoContext, "external proxy action has no self-service evidence; capability boundary reply preserved", nil)
			return state, nil
		}
		markKnowledgeNoContextHandoffDirective(state.Input, "当前酒店业务问题知识库没有可用答案")
		state.Decision = buildKnowledgeNoContextDecision(req.AIAgent, knowledgeIDs)
		state.prependDecisionInstruction(knowledgeActionInstruction)
		state.recordAnswerability(answerabilityStatusNoContext, "no retrieved context", nil)
		return state, nil
	}
	state.Decision = buildKnowledgeGuardDecision(req.AIAgent, result)
	state.prependDecisionInstruction(knowledgeActionInstruction)
	if len(externalProxyBoundaryTaskIDs) > 0 {
		state.Decision.Instructions = append(state.Decision.Instructions, schema.SystemMessage(buildExternalProxyCapabilityBoundaryInstruction(externalProxyBoundaryTaskIDs)))
	}
	if deferredInstruction != "" {
		state.Decision.Instructions = append(state.Decision.Instructions, schema.SystemMessage(deferredInstruction))
	}
	state.recordAnswerability(answerabilityStatusHasContext, "retrieved context injected", nil)
	return state, nil
}

func routeExternalProxyNoEvidenceAsCapabilityBoundary(
	batch *runtimeKnowledgeRetrieveBatch,
	trace *callbacks.KnowledgeEvidenceJudgeTraceData,
) []string {
	if batch == nil {
		return nil
	}
	taskIDs := make([]string, 0)
	for index := range batch.Questions {
		question := &batch.Questions[index]
		if question.Disposition != runtimeKnowledgeDispositionNoEvidenceHandoff ||
			!isExternalProxyActionClassification(question.Intent, question.SubIntent, question.Objective) {
			continue
		}
		taskID := strings.TrimSpace(question.TaskID)
		if taskID == "" {
			continue
		}
		question.Disposition = runtimeKnowledgeDispositionAnswer
		question.MissingAspects = nil
		taskIDs = appendIfMissing(taskIDs, taskID)
		if trace == nil {
			continue
		}
		for traceIndex := range trace.Tasks {
			if strings.TrimSpace(trace.Tasks[traceIndex].TaskID) == taskID {
				trace.Tasks[traceIndex].Disposition = runtimeKnowledgeDispositionAnswer
				break
			}
		}
	}
	return taskIDs
}

func convertExternalProxyCapabilityBoundaryTasks(plan callbacks.ReplyPlanTraceData, taskIDs []string) callbacks.ReplyPlanTraceData {
	if len(taskIDs) == 0 {
		return plan
	}
	taskSet := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		if taskID = strings.TrimSpace(taskID); taskID != "" {
			taskSet[taskID] = struct{}{}
		}
	}
	for index := range plan.TaskPlans {
		task := &plan.TaskPlans[index]
		if _, ok := taskSet[strings.TrimSpace(task.TaskID)]; !ok ||
			!isExternalProxyActionClassification(task.Intent, task.SubIntent, task.Objective) {
			continue
		}
		task.Output = "text_reply"
		task.OutputKind = "text"
		task.ReplyRequired = true
		task.NeedsKnowledge = false
		task.SelectedLayer = ""
		task.SelectedCandidateIDs = nil
		task.SupportedFacts = nil
		task.MissingAspects = nil
	}
	plan.ActiveTaskCount = len(plan.TaskPlans)
	plan.ReplyRequiredTaskCount = countReplyRequiredTasks(plan.TaskPlans)
	return plan
}

func buildExternalProxyCapabilityBoundaryInstruction(taskIDs []string) string {
	return "【外部代执行能力边界】仅适用于任务 " + strings.Join(taskIDs, "、") + "：当前没有选中可用的自助知识事实，只需礼貌说明无法直接替客户完成外部下单、叫车、代买、代订或联系商家的操作；不得承诺已经执行或稍后执行，也不得仅因此转人工。不要把这条规则用于酒店内部送物、维修、开门等服务。"
}

func appendRuntimeKnowledgeUnjudgedTaskTrace(
	trace callbacks.KnowledgeEvidenceJudgeTraceData,
	batch *runtimeKnowledgeRetrieveBatch,
) callbacks.KnowledgeEvidenceJudgeTraceData {
	if batch == nil || len(batch.Questions) == 0 {
		return trace
	}
	existing := make(map[string]callbacks.KnowledgeEvidenceJudgeTaskTraceData, len(trace.Tasks))
	for _, task := range trace.Tasks {
		existing[strings.TrimSpace(task.TaskID)] = task
	}
	ordered := make([]callbacks.KnowledgeEvidenceJudgeTaskTraceData, 0, len(trace.Tasks)+len(batch.Questions))
	used := make(map[string]struct{}, len(trace.Tasks))
	hasUnjudgedTask := false
	unjudgedCandidateTaskIDs := make([]string, 0)
	for index := range batch.Questions {
		question := &batch.Questions[index]
		taskID := strings.TrimSpace(question.TaskID)
		if task, ok := existing[taskID]; ok {
			ordered = append(ordered, task)
			used[taskID] = struct{}{}
			continue
		}

		candidateCount := runtimeRetrieverRawCandidateCount(question.Result)
		decision := knowledgeEvidenceDecisionProtocolInvalid
		decisionSource := "unjudged_candidates"
		disposition := runtimeKnowledgeDispositionJudgeProtocolRetry
		switch {
		case question.RetrieveError != nil:
			decision = knowledgeEvidenceDecisionInsufficient
			decisionSource = "source_unavailable"
			disposition = runtimeKnowledgeDispositionNoEvidenceHandoff
		case candidateCount == 0:
			decision = knowledgeEvidenceDecisionInsufficient
			decisionSource = "retriever_no_evidence"
			disposition = runtimeKnowledgeDispositionNoEvidenceHandoff
		default:
			unjudgedCandidateTaskIDs = append(unjudgedCandidateTaskIDs, taskID)
		}
		question.Decision = decision
		question.Disposition = disposition
		hasUnjudgedTask = true
		ordered = append(ordered, callbacks.KnowledgeEvidenceJudgeTaskTraceData{
			TaskID:         taskID,
			QueryPreview:   preview(question.Query, 120),
			CandidateCount: candidateCount,
			Decision:       decision,
			DecisionSource: decisionSource,
			Disposition:    disposition,
		})
		used[taskID] = struct{}{}
	}
	if !hasUnjudgedTask {
		return trace
	}
	for _, task := range trace.Tasks {
		if _, ok := used[strings.TrimSpace(task.TaskID)]; ok {
			continue
		}
		ordered = append(ordered, task)
	}
	trace.Tasks = ordered
	trace.TaskCount = len(ordered)
	if len(unjudgedCandidateTaskIDs) > 0 {
		detail := fmt.Sprintf(
			"candidate budget left %d task(s) unjudged: %s",
			len(unjudgedCandidateTaskIDs),
			strings.Join(unjudgedCandidateTaskIDs, ","),
		)
		switch strings.TrimSpace(trace.Status) {
		case "", "completed", "skipped", "fallback":
			trace.Status = knowledgeEvidenceDecisionProtocolInvalid
		}
		if strings.TrimSpace(trace.Reason) == "" {
			trace.Reason = detail
		} else {
			trace.Reason = strings.TrimSpace(trace.Reason) + "; " + detail
		}
	}
	return trace
}

func runtimeRetrieverRawCandidateCount(result *retrievers.KnowledgeRetrieveResult) int {
	if result == nil {
		return 0
	}
	if len(result.RawHits) > 0 {
		return len(result.RawHits)
	}
	if len(result.TraceItems) > 0 {
		return len(result.TraceItems)
	}
	return len(result.Hits)
}

func runtimeReplyPlanHasIndependentNonKnowledgeWork(plan callbacks.ReplyPlanTraceData) bool {
	for _, task := range plan.TaskPlans {
		if runtimeReplyTaskUsesKnowledge(task) {
			continue
		}
		if strings.TrimSpace(task.OutputKind) == "resource" || strings.TrimSpace(task.Output) == "structured_resource_commit" || strings.TrimSpace(task.Intent) == "hotel_variable" || replyTaskRequiresText(task) {
			return true
		}
	}
	return false
}

func shouldMarkUnavailableKnowledgeHandoff(state *answerabilityGateState, reason string) bool {
	if deferUnavailableKnowledgeForIndependentWork(state, reason) {
		return false
	}
	if state == nil {
		return true
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: buildReplyTaskPlans(state.Input.Intent)}
	for _, task := range plan.TaskPlans {
		if runtimeReplyTaskUsesKnowledge(task) &&
			!isExternalProxyActionClassification(task.Intent, task.SubIntent, task.Objective) {
			return true
		}
	}
	return len(externalProxyCapabilityBoundaryTaskIDs(plan)) == 0
}

func appendUnavailableExternalProxyInstruction(state *answerabilityGateState) {
	if state == nil {
		return
	}
	plan := callbacks.ReplyPlanTraceData{TaskPlans: buildReplyTaskPlans(state.Input.Intent)}
	if state.Input.Collector != nil && len(state.Input.Collector.Data.Pipeline.ReplyPlan.TaskPlans) > 0 {
		plan = state.Input.Collector.Data.Pipeline.ReplyPlan
	}
	if taskIDs := externalProxyCapabilityBoundaryTaskIDs(plan); len(taskIDs) > 0 {
		state.Decision.Instructions = append(state.Decision.Instructions, schema.SystemMessage(buildExternalProxyCapabilityBoundaryInstruction(taskIDs)))
	}
}

func externalProxyCapabilityBoundaryTaskIDs(plan callbacks.ReplyPlanTraceData) []string {
	taskIDs := make([]string, 0)
	for index, task := range plan.TaskPlans {
		if !isExternalProxyActionClassification(task.Intent, task.SubIntent, task.Objective) {
			continue
		}
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			taskID = fmt.Sprintf("task-%d", index+1)
		}
		taskIDs = appendIfMissing(taskIDs, taskID)
	}
	return taskIDs
}

func deferUnavailableKnowledgeForIndependentWork(state *answerabilityGateState, reason string) bool {
	if state == nil || state.Input.Collector == nil {
		return false
	}
	plan := state.Input.Collector.Data.Pipeline.ReplyPlan
	if len(plan.TaskPlans) == 0 {
		plan.TaskPlans = buildReplyTaskPlans(state.Input.Intent)
		plan.ActiveTaskCount = len(plan.TaskPlans)
		plan.ReplyRequiredTaskCount = countReplyRequiredTasks(plan.TaskPlans)
		state.Input.Collector.SetReplyPlan(plan)
	}
	pending := make([]runtimeKnowledgeQuestionDisposition, 0)
	externalProxyTaskIDs := make([]string, 0)
	for index, task := range plan.TaskPlans {
		if !runtimeReplyTaskUsesKnowledge(task) {
			continue
		}
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			taskID = fmt.Sprintf("task-%d", index+1)
		}
		if isExternalProxyActionClassification(task.Intent, task.SubIntent, task.Objective) {
			externalProxyTaskIDs = appendIfMissing(externalProxyTaskIDs, taskID)
			continue
		}
		query := strings.TrimSpace(task.ResolvedText)
		if query == "" {
			query = strings.TrimSpace(task.Text)
		}
		if query == "" {
			query = strings.TrimSpace(currentRuntimeIntentSemanticText(state.Input.Request))
		}
		pending = append(pending, runtimeKnowledgeQuestionDisposition{
			TaskID:       taskID,
			Query:        query,
			NeedsHandoff: true,
		})
	}
	if len(externalProxyTaskIDs) > 0 {
		plan = convertExternalProxyCapabilityBoundaryTasks(plan, externalProxyTaskIDs)
		state.Input.Collector.SetReplyPlan(plan)
	}
	hasIndependentNonKnowledgeWork := runtimeReplyPlanHasIndependentNonKnowledgeWork(plan)

	autoHandoffEnabled := services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(state.Input.Request.Conversation.ID)
	if autoHandoffEnabled && len(pending) > 0 {
		activePlan := rebuildRuntimeKnowledgeReplyPlan(plan, nil, pending, true)
		state.Input.Collector.SetReplyPlan(activePlan)
	}
	judgeTrace := state.Input.Collector.Data.Pipeline.EvidenceJudge
	if strings.TrimSpace(judgeTrace.SchemaVersion) == "" {
		judgeTrace.SchemaVersion = knowledgeEvidenceJudgeSchemaVersion
	}
	if strings.TrimSpace(judgeTrace.Status) == "" {
		judgeTrace.Status = "skipped"
	}
	if strings.TrimSpace(judgeTrace.Reason) == "" {
		judgeTrace.Reason = "knowledge source unavailable before evidence judging"
	}
	if autoHandoffEnabled && len(pending) > 0 {
		judgeTrace.DeferredHandoff = true
		judgeTrace.DeferredHandoffReason = deferredRuntimeKnowledgeHandoffReason(pending)
		if strings.TrimSpace(reason) != "" {
			judgeTrace.DeferredHandoffReason = strings.TrimSpace(reason) + "；" + judgeTrace.DeferredHandoffReason
		}
	}
	for _, taskID := range externalProxyTaskIDs {
		judgeTrace.Tasks = append(judgeTrace.Tasks, callbacks.KnowledgeEvidenceJudgeTaskTraceData{
			TaskID:         taskID,
			Decision:       knowledgeEvidenceDecisionInsufficient,
			DecisionSource: "source_unavailable",
			Disposition:    runtimeKnowledgeDispositionAnswer,
		})
	}
	for _, item := range pending {
		if autoHandoffEnabled {
			judgeTrace.DeferredTaskIDs = appendIfMissing(judgeTrace.DeferredTaskIDs, item.TaskID)
		}
		judgeTrace.Tasks = append(judgeTrace.Tasks, callbacks.KnowledgeEvidenceJudgeTaskTraceData{
			TaskID:         item.TaskID,
			QueryPreview:   preview(item.Query, 120),
			Decision:       knowledgeEvidenceDecisionInsufficient,
			DecisionSource: "source_unavailable",
			Disposition:    runtimeKnowledgeDispositionNoEvidenceHandoff,
		})
	}
	judgeTrace.TaskCount = len(judgeTrace.Tasks)
	state.Input.Collector.SetKnowledgeEvidenceJudge(judgeTrace)
	return hasIndependentNonKnowledgeWork
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
