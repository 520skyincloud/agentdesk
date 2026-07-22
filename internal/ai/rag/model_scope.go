package rag

import (
	"context"
	"fmt"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func withKnowledgeModelScope(ctx context.Context, knowledgeBases []models.KnowledgeBase) (context.Context, error) {
	if len(knowledgeBases) == 0 {
		return ctx, fmt.Errorf("knowledge base is required for model call")
	}
	scope := usagex.ScopeFromContext(ctx)
	storeID := scope.StoreID
	knowledgeBaseID := scope.KnowledgeBaseID
	for _, item := range knowledgeBases {
		if item.StoreID <= 0 {
			return ctx, fmt.Errorf("knowledge base %d is not bound to a store", item.ID)
		}
		if storeID > 0 && storeID != item.StoreID {
			return ctx, fmt.Errorf("knowledge bases from different stores cannot share one model call")
		}
		storeID = item.StoreID
		if knowledgeBaseID <= 0 {
			knowledgeBaseID = item.ID
		}
	}
	scope.StoreID = storeID
	scope.KnowledgeBaseID = knowledgeBaseID
	if scope.CompanyID <= 0 {
		if store := repositories.StoreRepository.Get(sqls.DB(), storeID); store != nil {
			scope.CompanyID = store.CompanyID
		}
	}
	if scope.ModelSource == "" {
		scope.ModelSource = "store_credential"
	}
	return usagex.WithScope(ctx, scope), nil
}

func loadKnowledgeBasesForModelScope(ids []int64) []models.KnowledgeBase {
	normalized := normalizeKnowledgeBaseIDs(ids)
	if len(normalized) == 0 {
		return nil
	}
	return repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().In("id", normalized))
}
