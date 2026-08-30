package retrievers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

const defaultRuntimeKnowledgeContextTokens = 4000
const defaultRuntimeKnowledgeTopK = 8
const defaultRuntimeKnowledgeScoreThreshold = 0.3
const defaultRuntimeKnowledgeMaxContextItems = 5

type KnowledgeRetriever struct {
	AIAgent models.AIAgent
}

type KnowledgeRetrieveOptions struct {
	ContextMaxTokens int
	MaxContextItems  int
	TopK             int
	ScoreThreshold   float64
	QueryPreview     string
}

type KnowledgeBaseRetrievePolicy struct {
	KnowledgeBaseID int64
	TopK            int
	ScoreThreshold  float64
}

type KnowledgeRetrieveResult struct {
	KnowledgeBaseIDs []int64
	Query            string
	Options          KnowledgeRetrieveOptions
	RawHits          []rag.RetrieveResult
	EffectiveHits    []rag.RetrieveResult
	Hits             []rag.RetrieveResult
	ContextResults   []rag.RetrieveResult
	ContextText      string
	TopScore         float64
	AnswerMode       enums.KnowledgeAnswerMode
	Trace            *rag.RetrieveTrace
	TraceItems       []callbacks.RetrieverTraceItem
	TraceSummary     callbacks.RetrieverTraceSummary
	Policies         []KnowledgeBaseRetrievePolicy
}

func NewKnowledgeRetriever(aiAgent models.AIAgent) *KnowledgeRetriever {
	return &KnowledgeRetriever{AIAgent: aiAgent}
}

func DefaultKnowledgeRetrieveOptions() KnowledgeRetrieveOptions {
	return KnowledgeRetrieveOptions{
		ContextMaxTokens: defaultRuntimeKnowledgeContextTokens,
		MaxContextItems:  defaultRuntimeKnowledgeMaxContextItems,
	}
}

func (r *KnowledgeRetriever) KnowledgeBaseIDs() []int64 {
	_, knowledgeBaseIDs := r.knowledgeBaseLayers()
	return knowledgeBaseIDs
}

func (r *KnowledgeRetriever) knowledgeBaseLayers() ([]int64, []int64) {
	storeKnowledgeBaseIDs := normalizeRuntimeKnowledgeBaseIDs(utils.SplitInt64s(r.AIAgent.KnowledgeIDs))
	knowledgeBaseIDs := services.ReplyRuntimeGeneralKnowledgeService.ResolveKnowledgeBaseIDs(storeKnowledgeBaseIDs)
	return storeKnowledgeBaseIDs, knowledgeBaseIDs
}

func (r *KnowledgeRetriever) Retrieve(ctx context.Context, query string) ([]rag.RetrieveResult, *rag.RetrieveTrace, error) {
	return r.RetrieveByOptions(ctx, DefaultKnowledgeRetrieveOptions(), query)
}

func (r *KnowledgeRetriever) RetrieveByOptions(ctx context.Context, opts KnowledgeRetrieveOptions, query string) ([]rag.RetrieveResult, *rag.RetrieveTrace, error) {
	ids := r.KnowledgeBaseIDs()
	return r.retrieveByKnowledgeBaseIDs(ctx, ids, opts, query)
}

func (r *KnowledgeRetriever) retrieveByKnowledgeBaseIDs(ctx context.Context, ids []int64, opts KnowledgeRetrieveOptions, query string) ([]rag.RetrieveResult, *rag.RetrieveTrace, error) {
	return rag.Retrieve.RetrieveWithTrace(ctx, rag.RetrieveRequest{
		Query:            query,
		KnowledgeBaseIDs: ids,
		TopK:             opts.TopK,
		ScoreThreshold:   opts.ScoreThreshold,
		ContextMaxTokens: opts.ContextMaxTokens,
	})
}

func (r *KnowledgeRetriever) RetrieveContext(ctx context.Context, query string) (*KnowledgeRetrieveResult, error) {
	return r.RetrieveContextByOptions(ctx, DefaultKnowledgeRetrieveOptions(), query)
}

func (r *KnowledgeRetriever) RetrieveContextByOptions(ctx context.Context, opts KnowledgeRetrieveOptions, query string) (*KnowledgeRetrieveResult, error) {
	query = strings.TrimSpace(query)
	storeKnowledgeBaseIDs, knowledgeBaseIDs := r.knowledgeBaseLayers()
	policies := r.resolvePolicies(knowledgeBaseIDs, opts)
	contextMaxTokens := opts.ContextMaxTokens
	if contextMaxTokens <= 0 {
		contextMaxTokens = defaultRuntimeKnowledgeContextTokens
	}
	maxContextItems := opts.MaxContextItems
	if maxContextItems <= 0 {
		maxContextItems = defaultRuntimeKnowledgeMaxContextItems
	}
	queryPreview := strings.TrimSpace(opts.QueryPreview)
	if queryPreview == "" {
		queryPreview = query
	}
	ret := &KnowledgeRetrieveResult{
		KnowledgeBaseIDs: append([]int64(nil), knowledgeBaseIDs...),
		Query:            query,
		Options: KnowledgeRetrieveOptions{
			ContextMaxTokens: contextMaxTokens,
			MaxContextItems:  maxContextItems,
			TopK:             opts.TopK,
			ScoreThreshold:   opts.ScoreThreshold,
			QueryPreview:     queryPreview,
		},
		Policies: append([]KnowledgeBaseRetrievePolicy(nil), policies...),
	}
	if query == "" || len(knowledgeBaseIDs) == 0 {
		return ret, nil
	}
	retrieveStartedAt := time.Now()
	results, trace, err := r.retrieveByKnowledgeBaseIDs(ctx, knowledgeBaseIDs, opts, query)
	retrieveMs := time.Since(retrieveStartedAt).Milliseconds()
	if err != nil {
		return nil, err
	}
	if shouldBlockGeneralKnowledgeFallback(storeKnowledgeBaseIDs, results, trace) {
		slog.Warn("store knowledge lookup failed while general knowledge returned results", "store_knowledge_base_ids", storeKnowledgeBaseIDs, "failed_knowledge_base_ids", trace.FailedKnowledgeBaseIDs)
		return nil, fmt.Errorf("store knowledge lookup failed")
	}
	applyKnowledgeBasePriority(ret, storeKnowledgeBaseIDs, knowledgeBaseIDs, results)
	ret.Trace = trace
	ret.ContextResults = rag.Retrieve.SelectContextResults(ret.Hits, contextMaxTokens)
	ret.ContextResults = limitContextResults(ret.ContextResults, maxContextItems)
	ret.ContextText = strings.TrimSpace(buildContextText(ret.ContextResults))
	ret.TopScore = resolveTopScore(ret.Hits)
	ret.AnswerMode = resolveRuntimeAnswerMode(knowledgeBaseIDs, ret.Hits)
	ret.TraceItems = buildRetrieverTraceItems(queryPreview, results, ret.ContextResults, trace)
	ret.TraceSummary = buildRetrieverTraceSummary(ret.Options, ret.Policies, ret.ContextResults, results, trace)
	r.writeRuntimeRetrieveLog(ctx, query, retrieveMs, ret)
	r.writeKnowledgeUsageEvent(ctx, retrieveMs, ret)
	return ret, nil
}

func storeKnowledgeBaseLookupFailed(storeKnowledgeBaseIDs []int64, trace *rag.RetrieveTrace) bool {
	if len(storeKnowledgeBaseIDs) == 0 || trace == nil || len(trace.FailedKnowledgeBaseIDs) == 0 {
		return false
	}
	storeSet := make(map[int64]struct{}, len(storeKnowledgeBaseIDs))
	for _, knowledgeBaseID := range storeKnowledgeBaseIDs {
		storeSet[knowledgeBaseID] = struct{}{}
	}
	for _, knowledgeBaseID := range trace.FailedKnowledgeBaseIDs {
		if _, ok := storeSet[knowledgeBaseID]; ok {
			return true
		}
	}
	return false
}

func shouldBlockGeneralKnowledgeFallback(storeKnowledgeBaseIDs []int64, rawHits []rag.RetrieveResult, trace *rag.RetrieveTrace) bool {
	return len(selectKnowledgeBaseHits(storeKnowledgeBaseIDs, rawHits)) == 0 &&
		storeKnowledgeBaseLookupFailed(storeKnowledgeBaseIDs, trace)
}

func applyKnowledgeBasePriority(result *KnowledgeRetrieveResult, storeKnowledgeBaseIDs, knowledgeBaseIDs []int64, rawHits []rag.RetrieveResult) {
	if result == nil {
		return
	}
	result.RawHits = append([]rag.RetrieveResult(nil), rawHits...)
	result.EffectiveHits = selectPrioritizedKnowledgeLayer(storeKnowledgeBaseIDs, knowledgeBaseIDs, rawHits)
	result.Hits = append([]rag.RetrieveResult(nil), result.EffectiveHits...)
}

// RebuildKnowledgeRetrieveSelection applies an already-authorized evidence
// selection without issuing another retrieval request or writing another log.
func RebuildKnowledgeRetrieveSelection(result *KnowledgeRetrieveResult, hits []rag.RetrieveResult) {
	if result == nil {
		return
	}
	rawHits := result.RawHits
	if len(rawHits) == 0 {
		rawHits = append([]rag.RetrieveResult(nil), result.Hits...)
		result.RawHits = append([]rag.RetrieveResult(nil), rawHits...)
	}
	result.EffectiveHits = append([]rag.RetrieveResult(nil), hits...)
	// Hits remains a compatibility view for the existing Generate and trace
	// pipeline. RawHits is never rebuilt from the Judge selection.
	result.Hits = append([]rag.RetrieveResult(nil), result.EffectiveHits...)
	contextMaxTokens := result.Options.ContextMaxTokens
	if contextMaxTokens <= 0 {
		contextMaxTokens = defaultRuntimeKnowledgeContextTokens
	}
	maxContextItems := result.Options.MaxContextItems
	if maxContextItems <= 0 {
		maxContextItems = defaultRuntimeKnowledgeMaxContextItems
	}
	if len(hits) > maxContextItems {
		maxContextItems = len(hits)
		result.Options.MaxContextItems = maxContextItems
	}
	result.ContextResults = rag.Retrieve.SelectContextResults(result.Hits, contextMaxTokens)
	result.ContextResults = limitContextResults(result.ContextResults, maxContextItems)
	result.ContextText = strings.TrimSpace(buildContextText(result.ContextResults))
	result.TopScore = resolveTopScore(result.Hits)
	result.AnswerMode = resolveRuntimeAnswerMode(result.KnowledgeBaseIDs, result.Hits)
	result.TraceItems = buildRetrieverTraceItems(result.Options.QueryPreview, rawHits, result.ContextResults, result.Trace)
	result.TraceSummary = buildRetrieverTraceSummary(result.Options, result.Policies, result.ContextResults, rawHits, result.Trace)
}

func selectPrioritizedKnowledgeLayer(storeKnowledgeBaseIDs, knowledgeBaseIDs []int64, rawHits []rag.RetrieveResult) []rag.RetrieveResult {
	if len(knowledgeBaseIDs) == 0 || len(rawHits) == 0 {
		return nil
	}
	if selected := selectKnowledgeBaseHits(storeKnowledgeBaseIDs, rawHits); len(selected) > 0 {
		return selected
	}
	storeSet := make(map[int64]struct{}, len(storeKnowledgeBaseIDs))
	for _, knowledgeBaseID := range storeKnowledgeBaseIDs {
		storeSet[knowledgeBaseID] = struct{}{}
	}
	generalKnowledgeBaseIDs := make([]int64, 0, len(knowledgeBaseIDs))
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		if _, ok := storeSet[knowledgeBaseID]; ok {
			continue
		}
		generalKnowledgeBaseIDs = append(generalKnowledgeBaseIDs, knowledgeBaseID)
	}
	return selectKnowledgeBaseHits(generalKnowledgeBaseIDs, rawHits)
}

func selectKnowledgeBaseHits(knowledgeBaseIDs []int64, rawHits []rag.RetrieveResult) []rag.RetrieveResult {
	if len(knowledgeBaseIDs) == 0 || len(rawHits) == 0 {
		return nil
	}
	allowed := make(map[int64]struct{}, len(knowledgeBaseIDs))
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		allowed[knowledgeBaseID] = struct{}{}
	}
	selected := make([]rag.RetrieveResult, 0, len(rawHits))
	for _, hit := range rawHits {
		if _, ok := allowed[hit.KnowledgeBaseID]; ok {
			selected = append(selected, hit)
		}
	}
	return selected
}

func normalizeRuntimeKnowledgeBaseIDs(knowledgeBaseIDs []int64) []int64 {
	ret := make([]int64, 0, len(knowledgeBaseIDs))
	seen := make(map[int64]struct{}, len(knowledgeBaseIDs))
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		if knowledgeBaseID <= 0 {
			continue
		}
		if _, ok := seen[knowledgeBaseID]; ok {
			continue
		}
		seen[knowledgeBaseID] = struct{}{}
		ret = append(ret, knowledgeBaseID)
	}
	return ret
}

func (r *KnowledgeRetriever) writeKnowledgeUsageEvent(ctx context.Context, retrieveMs int64, result *KnowledgeRetrieveResult) {
	if result == nil || len(result.KnowledgeBaseIDs) == 0 {
		return
	}
	requestID := strings.TrimSpace(tracex.RequestIDFromContext(ctx))
	scope := usagex.ScopeFromContext(ctx)
	if requestID == "" {
		requestID = strings.TrimSpace(scope.RequestID)
	}
	if requestID == "" {
		return
	}
	provider := "runtime"
	if result.Trace != nil && len(result.Trace.Providers) > 0 {
		provider = strings.Join(result.Trace.Providers, ",")
	}
	queryHash := sha256.Sum256([]byte(result.Query))
	status := "completed"
	if len(result.Hits) == 0 {
		status = "empty"
	}
	requestCount := int64(1)
	rerankCount := int64(0)
	if result.Trace != nil {
		if result.Trace.RequestCount > 0 {
			requestCount = result.Trace.RequestCount
		}
		rerankCount = result.Trace.RerankCount
	}
	_ = services.AIUsageEventService.Record(models.AIUsageEvent{
		EventKey:        requestID + ":knowledge_retrieve:" + hex.EncodeToString(queryHash[:8]),
		ConversationID:  scope.ConversationID,
		MessageID:       scope.MessageID,
		KnowledgeBaseID: result.KnowledgeBaseIDs[0], RequestID: requestID,
		Stage: "knowledge_retrieve", Provider: provider, OperationType: "knowledge_retrieve",
		RequestCount: requestCount, RerankCount: rerankCount,
		EstimatedContextTokens: int64(estimateKnowledgeContextTokens(result.ContextText)),
		MetricSource:           services.AIUsageMetricSourceProviderOperation,
		LatencyMS:              retrieveMs, Status: status,
	})
}

func estimateKnowledgeContextTokens(text string) int {
	runeCount := len([]rune(strings.TrimSpace(text)))
	if runeCount == 0 {
		return 0
	}
	return (runeCount + 1) / 2
}

func (r *KnowledgeRetriever) writeRuntimeRetrieveLog(ctx context.Context, query string, retrieveMs int64, result *KnowledgeRetrieveResult) {
	if result == nil || len(result.KnowledgeBaseIDs) == 0 {
		return
	}
	rawHits := result.RawHits
	if rawHits == nil {
		rawHits = result.Hits
	}
	hits := buildKnowledgeSearchResults(rawHits)
	answerStatus := int(enums.KnowledgeAnswerStatusNormal)
	if len(hits) == 0 {
		answerStatus = int(enums.KnowledgeAnswerStatusNoAnswer)
	}
	scope := usagex.ScopeFromContext(ctx)
	requestID := strings.TrimSpace(tracex.RequestIDFromContext(ctx))
	if requestID == "" {
		requestID = strings.TrimSpace(scope.RequestID)
	}
	if _, err := rag.RetrieveLog.CreateRetrieveLog(&rag.CreateRetrieveLogRequest{
		KnowledgeBaseID: result.KnowledgeBaseIDs[0],
		SourceType:      inferRuntimeRetrieveSourceType(hits),
		Channel:         string(enums.KnowledgeRetrieveChannelIM),
		Scene:           string(enums.KnowledgeRetrieveSceneFirstResponse),
		ConversationID:  scope.ConversationID,
		MessageID:       scope.MessageID,
		RequestID:       requestID,
		Question:        query,
		AnswerStatus:    answerStatus,
		ChunkProvider:   runtimeRetrieveChunkProvider(result),
		RerankEnabled:   false,
		Hits:            hits,
		// The runtime Judge runs after this retrieval log is written. Do not mark
		// Retriever-preselected chunks as the final evidence used in the reply.
		UsedHits:           nil,
		HitSourceRecordIDs: retrieveSourceRecordIDs(rawHits),
		UsedHitRankNos:     nil,
		RetrieveMs:         retrieveMs,
		LatencyMs:          retrieveMs,
		ModelName:          "runtime-retriever",
	}, nil); err != nil {
		slog.Warn("runtime knowledge retrieve log failed", "error", err)
	}
}

func buildKnowledgeSearchResults(items []rag.RetrieveResult) []response.KnowledgeSearchResult {
	ret := make([]response.KnowledgeSearchResult, 0, len(items))
	for _, item := range items {
		ret = append(ret, response.KnowledgeSearchResult{
			KnowledgeBaseID: item.KnowledgeBaseID,
			ChunkID:         item.ChunkID,
			DocumentID:      item.DocumentID,
			DocumentTitle:   item.DocumentTitle,
			FaqID:           item.FaqID,
			FaqQuestion:     item.FaqQuestion,
			ChunkNo:         item.ChunkNo,
			Title:           item.Title,
			SectionPath:     item.SectionPath,
			Content:         item.Content,
			Score:           float64(item.Score),
		})
	}
	return ret
}

func inferRuntimeRetrieveSourceType(hits []response.KnowledgeSearchResult) string {
	if len(hits) == 0 {
		return "local_vector"
	}
	fastGPT := false
	local := false
	for _, hit := range hits {
		if isFastGPTRetrieveResult(hit) {
			fastGPT = true
		} else {
			local = true
		}
	}
	if fastGPT && local {
		return "hybrid"
	}
	if fastGPT {
		return "fastgpt"
	}
	return "local_vector"
}

func isFastGPTRetrieveResult(hit response.KnowledgeSearchResult) bool {
	return strings.Contains(hit.SectionPath, "FastGPT知识库/") ||
		strings.Contains(hit.SectionPath, "FastGPT云端知识库") ||
		strings.Contains(hit.DocumentTitle, "FastGPT云端知识库")
}

func runtimeRetrieveChunkProvider(result *KnowledgeRetrieveResult) string {
	if result == nil {
		return "runtime"
	}
	rawHits := result.RawHits
	if rawHits == nil {
		rawHits = result.Hits
	}
	if len(rawHits) == 0 {
		return "runtime_empty"
	}
	return inferRuntimeRetrieveSourceType(buildKnowledgeSearchResults(rawHits))
}

func limitContextResults(results []rag.RetrieveResult, maxItems int) []rag.RetrieveResult {
	if len(results) == 0 {
		return nil
	}
	if maxItems <= 0 || len(results) <= maxItems {
		return append([]rag.RetrieveResult(nil), results...)
	}
	return append([]rag.RetrieveResult(nil), results[:maxItems]...)
}

func buildContextText(results []rag.RetrieveResult) string {
	if len(results) == 0 {
		return ""
	}
	return strings.TrimSpace(rag.Retrieve.BuildContext(context.Background(), results, 1<<30))
}

func resolveTopScore(results []rag.RetrieveResult) float64 {
	if len(results) == 0 {
		return 0
	}
	return float64(results[0].Score)
}

func (r *KnowledgeRetriever) resolvePolicies(knowledgeBaseIDs []int64, opts KnowledgeRetrieveOptions) []KnowledgeBaseRetrievePolicy {
	if len(knowledgeBaseIDs) == 0 {
		return nil
	}
	knowledgeBases := loadRuntimeKnowledgeBases(knowledgeBaseIDs)
	ret := make([]KnowledgeBaseRetrievePolicy, 0, len(knowledgeBaseIDs))
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		policy := KnowledgeBaseRetrievePolicy{
			KnowledgeBaseID: knowledgeBaseID,
			TopK:            defaultRuntimeKnowledgeTopK,
			ScoreThreshold:  defaultRuntimeKnowledgeScoreThreshold,
		}
		if knowledgeBase, ok := knowledgeBases[knowledgeBaseID]; ok {
			if knowledgeBase.DefaultTopK > 0 {
				policy.TopK = knowledgeBase.DefaultTopK
			}
			if knowledgeBase.DefaultScoreThreshold > 0 {
				policy.ScoreThreshold = knowledgeBase.DefaultScoreThreshold
			}
		}
		if opts.TopK > 0 {
			policy.TopK = opts.TopK
		}
		if opts.ScoreThreshold > 0 {
			policy.ScoreThreshold = opts.ScoreThreshold
		}
		ret = append(ret, policy)
	}
	return ret
}

func resolveRuntimeAnswerMode(knowledgeBaseIDs []int64, results []rag.RetrieveResult) enums.KnowledgeAnswerMode {
	knowledgeBases := loadRuntimeKnowledgeBases(knowledgeBaseIDs)
	if len(knowledgeBases) == 0 {
		return enums.KnowledgeAnswerModeStrict
	}
	if len(results) > 0 {
		if knowledgeBase, ok := knowledgeBases[results[0].KnowledgeBaseID]; ok {
			return normalizeRuntimeAnswerMode(knowledgeBase)
		}
	}
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		if knowledgeBase, ok := knowledgeBases[knowledgeBaseID]; ok {
			return normalizeRuntimeAnswerMode(knowledgeBase)
		}
	}
	return enums.KnowledgeAnswerModeStrict
}

func normalizeRuntimeAnswerMode(knowledgeBase models.KnowledgeBase) enums.KnowledgeAnswerMode {
	answerMode := enums.KnowledgeAnswerMode(knowledgeBase.AnswerMode)
	if answerMode == 0 {
		answerMode = enums.KnowledgeAnswerModeStrict
	}
	return answerMode
}

func loadRuntimeKnowledgeBases(ids []int64) map[int64]models.KnowledgeBase {
	db := sqls.DB()
	if len(ids) == 0 || db == nil {
		return nil
	}
	items := repositories.KnowledgeBaseRepository.Find(db, sqls.NewCnd().In("id", ids))
	if len(items) == 0 {
		return nil
	}
	ret := make(map[int64]models.KnowledgeBase, len(items))
	for _, item := range items {
		if item.Status != enums.StatusOk {
			continue
		}
		ret[item.ID] = item
	}
	return ret
}

func buildRetrieverTraceItems(queryPreview string, results, contextResults []rag.RetrieveResult, trace *rag.RetrieveTrace) []callbacks.RetrieverTraceItem {
	if len(results) == 0 {
		return nil
	}
	latencyMs := int64(0)
	if trace != nil {
		latencyMs = trace.EmbeddingMs + trace.VectorSearchMs + trace.HydrateMs
	}
	contextRanks := make(map[string]int, len(contextResults))
	for index, item := range contextResults {
		contextRanks[retrieveResultIdentity(item)] = index + 1
	}
	ret := make([]callbacks.RetrieverTraceItem, 0, len(results))
	for index, item := range results {
		contextRank := contextRanks[retrieveResultIdentity(item)]
		discardReason := ""
		if contextRank == 0 {
			discardReason = "context_limit_or_duplicate"
		}
		ret = append(ret, callbacks.RetrieverTraceItem{
			Query:           queryPreview,
			KnowledgeBaseID: item.KnowledgeBaseID,
			DocumentID:      item.DocumentID,
			DocumentTitle:   item.DocumentTitle,
			SourceRecordID:  item.SourceRecordID,
			RawRankNo:       index + 1,
			ContextRankNo:   contextRank,
			UsedInContext:   contextRank > 0,
			DiscardReason:   discardReason,
			Score:           float64(item.Score),
			LatencyMs:       latencyMs,
		})
	}
	return ret
}

func retrieveResultIdentity(item rag.RetrieveResult) string {
	if sourceRecordID := strings.TrimSpace(item.SourceRecordID); sourceRecordID != "" {
		return "source:" + sourceRecordID
	}
	return strings.Join([]string{
		"local",
		fmt.Sprintf("%d", item.KnowledgeBaseID),
		fmt.Sprintf("%d", item.DocumentID),
		strings.TrimSpace(item.SectionPath),
		fmt.Sprintf("%d", item.ChunkNo),
	}, "|")
}

func retrieveSourceRecordIDs(items []rag.RetrieveResult) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, strings.TrimSpace(item.SourceRecordID))
	}
	return result
}

func resolveUsedHitRankNos(hits, usedHits []rag.RetrieveResult) []int {
	used := make(map[string]struct{}, len(usedHits))
	for _, item := range usedHits {
		used[retrieveResultIdentity(item)] = struct{}{}
	}
	ranks := make([]int, 0, len(usedHits))
	for index, item := range hits {
		if _, ok := used[retrieveResultIdentity(item)]; ok {
			ranks = append(ranks, index+1)
		}
	}
	return ranks
}

func buildRetrieverTraceSummary(opts KnowledgeRetrieveOptions, policies []KnowledgeBaseRetrievePolicy, contextResults []rag.RetrieveResult, results []rag.RetrieveResult, trace *rag.RetrieveTrace) callbacks.RetrieverTraceSummary {
	ret := callbacks.RetrieverTraceSummary{
		TopK:             opts.TopK,
		ScoreThreshold:   opts.ScoreThreshold,
		ContextMaxTokens: opts.ContextMaxTokens,
		MaxContextItems:  opts.MaxContextItems,
		HitCount:         len(results),
		ContextCount:     len(contextResults),
		Policies:         buildRetrieverPolicyTraceItems(policies),
	}
	if ret.TopK <= 0 && len(policies) == 1 {
		ret.TopK = policies[0].TopK
	}
	if ret.ScoreThreshold <= 0 && len(policies) == 1 {
		ret.ScoreThreshold = policies[0].ScoreThreshold
	}
	if trace != nil {
		ret.EmbeddingMs = trace.EmbeddingMs
		ret.VectorSearchMs = trace.VectorSearchMs
		ret.HydrateMs = trace.HydrateMs
	}
	return ret
}

func buildRetrieverPolicyTraceItems(policies []KnowledgeBaseRetrievePolicy) []callbacks.RetrieverPolicyTraceItem {
	if len(policies) == 0 {
		return nil
	}
	ret := make([]callbacks.RetrieverPolicyTraceItem, 0, len(policies))
	for _, item := range policies {
		ret = append(ret, callbacks.RetrieverPolicyTraceItem{
			KnowledgeBaseID: item.KnowledgeBaseID,
			TopK:            item.TopK,
			ScoreThreshold:  item.ScoreThreshold,
		})
	}
	return ret
}
