package rag

import (
	"context"
	"fmt"

	"agent-desk/internal/ai"
	"agent-desk/internal/ai/rag/vectordb"
	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
)

func (s *index) runDocumentIndex(ctx context.Context, document models.KnowledgeDocument, knowledgeBase models.KnowledgeBase) ([]vectordb.Vector, int, error) {
	existingChunks := repositories.KnowledgeChunkRepository.FindByDocumentIDInTenant(sqls.DB(), document.ID, document.TenantID)
	chunks, err := s.buildDocumentChunks(ctx, document, knowledgeBase)
	if err != nil {
		return nil, 0, err
	}

	collectionName := s.getCollectionName()
	provider := vectordb.GetProvider()
	if provider == nil {
		return nil, 0, fmt.Errorf("vectordb provider not initialized")
	}
	if _, err := ai.Embedding.GetModel(ctx); err != nil {
		return nil, 0, fmt.Errorf("failed to get embedding model: %w", err)
	}

	existingVectorIDs := collectExistingVectorIDs(existingChunks)
	vectors, chunkModels, dimension, err := s.prepareDocumentVectors(ctx, knowledgeBase, document, chunks)
	if err != nil {
		return nil, 0, err
	}
	if err := s.ensureCollection(ctx, provider, collectionName, dimension); err != nil {
		return nil, 0, err
	}
	if len(existingVectorIDs) > 0 {
		if err := provider.DeleteVectors(ctx, collectionName, existingVectorIDs); err != nil {
			return nil, 0, fmt.Errorf("failed to delete old vectors: %w", err)
		}
	}
	if err := provider.UpsertVectors(ctx, collectionName, vectors); err != nil {
		return nil, 0, fmt.Errorf("failed to upsert vectors: %w", err)
	}
	if err := s.replaceDocumentChunks(document.ID, document.TenantID, chunkModels); err != nil {
		return nil, 0, fmt.Errorf("failed to save chunks: %w", err)
	}
	return vectors, len(chunks), nil
}

func (s *index) runFAQIndex(ctx context.Context, faq models.KnowledgeFAQ, knowledgeBase models.KnowledgeBase) error {
	existingChunks := repositories.KnowledgeChunkRepository.FindByFaqIDInTenant(sqls.DB(), faq.ID, faq.TenantID)
	content := buildFAQChunkContent(faq)
	if content == "" {
		return fmt.Errorf("faq content is empty")
	}

	provider := vectordb.GetProvider()
	if provider == nil {
		return fmt.Errorf("vectordb provider not initialized")
	}
	if _, err := ai.Embedding.GetModel(ctx); err != nil {
		return fmt.Errorf("failed to get embedding model: %w", err)
	}
	vector, chunkModel, dimension, err := s.prepareFAQVector(ctx, knowledgeBase, faq, content)
	if err != nil {
		return err
	}

	collectionName := s.getCollectionName()
	if err := s.ensureCollection(ctx, provider, collectionName, dimension); err != nil {
		return err
	}

	existingVectorIDs := make([]string, 0, len(existingChunks))
	for _, chunk := range existingChunks {
		if strs.IsNotBlank(chunk.VectorID) {
			existingVectorIDs = append(existingVectorIDs, chunk.VectorID)
		}
	}
	if len(existingVectorIDs) > 0 {
		if err := provider.DeleteVectors(ctx, collectionName, existingVectorIDs); err != nil {
			return fmt.Errorf("failed to delete old vectors: %w", err)
		}
	}
	if err := provider.UpsertVectors(ctx, collectionName, []vectordb.Vector{vector}); err != nil {
		return fmt.Errorf("failed to upsert vectors: %w", err)
	}
	if err := s.replaceFAQChunk(faq.ID, faq.TenantID, &chunkModel); err != nil {
		return fmt.Errorf("failed to save faq chunk: %w", err)
	}
	return nil
}
