package rag

import (
	"context"
	"fmt"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type retrieve struct {
}

var Retrieve = &retrieve{}

func (s *retrieve) Retrieve(ctx context.Context, req RetrieveRequest) ([]RetrieveResult, error) {
	results, _, err := s.RetrieveWithTrace(ctx, req)
	return results, err
}

type RetrieveTrace struct {
	EmbeddingMs    int64
	VectorSearchMs int64
	HydrateMs      int64
	Providers      []string
	DatasetIDs     []string
	RequestCount   int64
	RerankCount    int64
}

func (s *retrieve) RetrieveWithTrace(ctx context.Context, req RetrieveRequest) ([]RetrieveResult, *RetrieveTrace, error) {
	trace := newRetrieveTrace()
	retrievableKnowledgeBases, _, ok := s.prepareRetrievableKnowledgeBases(req, trace)
	if !ok {
		return nil, trace, nil
	}
	if _, err := resolveRetrievableKnowledgeBaseTenant(retrievableKnowledgeBases); err != nil {
		return nil, trace, err
	}
	for _, knowledgeBase := range retrievableKnowledgeBases {
		if !isFastGPTKnowledgeBase(knowledgeBase) {
			return nil, trace, fmt.Errorf("knowledge retrieval only supports managed FastGPT datasets")
		}
	}
	results, fastGPTMs, err := s.retrieveFastGPTKnowledge(ctx, req, retrievableKnowledgeBases)
	trace.VectorSearchMs = fastGPTMs
	if err != nil {
		return nil, trace, err
	}
	trace.Providers = append(trace.Providers, enums.KnowledgeRetrievalModeFastGPT)
	appendTraceDatasetIDs(trace, retrievableKnowledgeBases)
	trace.RequestCount = int64(len(retrievableKnowledgeBases))
	for _, knowledgeBase := range retrievableKnowledgeBases {
		if knowledgeBase.DefaultRerankLimit > 0 {
			trace.RerankCount++
		}
	}
	return results, trace, nil
}

func resolveRetrievableKnowledgeBaseTenant(knowledgeBases []models.KnowledgeBase) (int64, error) {
	tenantID := int64(0)
	for i := range knowledgeBases {
		if knowledgeBases[i].TenantID <= 0 {
			return 0, fmt.Errorf("knowledge base %d has no tenant", knowledgeBases[i].ID)
		}
		if tenantID == 0 {
			tenantID = knowledgeBases[i].TenantID
			continue
		}
		if tenantID != knowledgeBases[i].TenantID {
			return 0, fmt.Errorf("knowledge retrieval cannot span multiple tenants")
		}
	}
	if tenantID <= 0 {
		return 0, fmt.Errorf("knowledge retrieval has no tenant")
	}
	return tenantID, nil
}

func appendTraceDatasetIDs(trace *RetrieveTrace, knowledgeBases []models.KnowledgeBase) {
	if trace == nil {
		return
	}
	seen := make(map[string]struct{}, len(trace.DatasetIDs)+len(knowledgeBases))
	for _, datasetID := range trace.DatasetIDs {
		seen[datasetID] = struct{}{}
	}
	for _, knowledgeBase := range knowledgeBases {
		datasetID := strings.TrimSpace(knowledgeBase.DatasetID)
		if datasetID == "" {
			continue
		}
		if _, ok := seen[datasetID]; ok {
			continue
		}
		seen[datasetID] = struct{}{}
		trace.DatasetIDs = append(trace.DatasetIDs, datasetID)
	}
}

func (s *retrieve) RetrieveWithRerank(ctx context.Context, req RetrieveRequest, rerankLimit int) ([]RetrieveResult, error) {
	results, err := s.Retrieve(ctx, req)
	if err != nil {
		return nil, err
	}

	if rerankLimit <= 0 || len(results) <= rerankLimit {
		return results, nil
	}
	return results[:rerankLimit], nil
}

func normalizeKnowledgeBaseIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(ids))
	normalized := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized
}

func resolveKnowledgeBaseSearchOptions(req RetrieveRequest, knowledgeBase *models.KnowledgeBase) (int, float32) {
	topK := req.TopK
	if topK <= 0 && knowledgeBase != nil && knowledgeBase.DefaultTopK > 0 {
		topK = knowledgeBase.DefaultTopK
	}
	if topK <= 0 {
		topK = 8
	}

	scoreThreshold := float32(req.ScoreThreshold)
	if scoreThreshold <= 0 && knowledgeBase != nil && knowledgeBase.DefaultScoreThreshold > 0 {
		scoreThreshold = float32(knowledgeBase.DefaultScoreThreshold)
	}
	if scoreThreshold <= 0 {
		scoreThreshold = 0.3
	}
	return topK, scoreThreshold
}

func (s *retrieve) loadRetrievableKnowledgeBases(ids []int64) []models.KnowledgeBase {
	if len(ids) == 0 {
		return nil
	}
	items := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().In("id", ids))
	if len(items) == 0 {
		return nil
	}
	allowed := make(map[int64]models.KnowledgeBase, len(items))
	for _, item := range items {
		if item.Status == enums.StatusOk {
			allowed[item.ID] = item
		}
	}
	filtered := make([]models.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		if item, ok := allowed[id]; ok {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
