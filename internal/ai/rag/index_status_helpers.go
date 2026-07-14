package rag

import (
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func (s *index) markDocumentIndexPending(documentID int64) error {
	document := repositories.KnowledgeDocumentRepository.Get(sqls.DB(), documentID)
	if document == nil || document.TenantID <= 0 {
		return fmt.Errorf("knowledge document %d is missing or has no tenant", documentID)
	}
	return repositories.KnowledgeDocumentRepository.UpdatesInTenant(sqls.DB(), documentID, document.TenantID, map[string]any{
		"index_status": enums.KnowledgeDocumentIndexStatusPending,
		"indexed_at":   nil,
		"index_error":  "",
		"updated_at":   time.Now(),
	})
}

func (s *index) markDocumentIndexIndexed(documentID int64) error {
	document := repositories.KnowledgeDocumentRepository.Get(sqls.DB(), documentID)
	if document == nil || document.TenantID <= 0 {
		return fmt.Errorf("knowledge document %d is missing or has no tenant", documentID)
	}
	now := time.Now()
	return repositories.KnowledgeDocumentRepository.UpdatesInTenant(sqls.DB(), documentID, document.TenantID, map[string]any{
		"index_status": enums.KnowledgeDocumentIndexStatusIndexed,
		"indexed_at":   &now,
		"index_error":  "",
		"updated_at":   now,
	})
}

func (s *index) markDocumentIndexFailed(documentID int64, err error) error {
	document := repositories.KnowledgeDocumentRepository.Get(sqls.DB(), documentID)
	if document == nil || document.TenantID <= 0 {
		return fmt.Errorf("knowledge document %d is missing or has no tenant", documentID)
	}
	return repositories.KnowledgeDocumentRepository.UpdatesInTenant(sqls.DB(), documentID, document.TenantID, map[string]any{
		"index_status": enums.KnowledgeDocumentIndexStatusFailed,
		"index_error":  truncateIndexError(err),
		"updated_at":   time.Now(),
	})
}

func (s *index) markKnowledgeBaseDocumentsIndexPending(knowledgeBaseID int64, documentIDs []int64) error {
	if len(documentIDs) == 0 {
		return nil
	}
	knowledgeBase := repositories.KnowledgeBaseRepository.Get(sqls.DB(), knowledgeBaseID)
	if knowledgeBase == nil || knowledgeBase.TenantID <= 0 {
		return fmt.Errorf("knowledge base %d is missing or has no tenant", knowledgeBaseID)
	}
	return sqls.DB().Model(&models.KnowledgeDocument{}).
		Where("knowledge_base_id = ? AND tenant_id = ?", knowledgeBaseID, knowledgeBase.TenantID).
		Where("id IN ?", documentIDs).
		Updates(map[string]any{
			"index_status": enums.KnowledgeDocumentIndexStatusPending,
			"indexed_at":   nil,
			"index_error":  "",
			"updated_at":   time.Now(),
		}).Error
}

func (s *index) markFAQIndexPending(faqID int64) error {
	faq := repositories.KnowledgeFAQRepository.Get(sqls.DB(), faqID)
	if faq == nil || faq.TenantID <= 0 {
		return fmt.Errorf("knowledge faq %d is missing or has no tenant", faqID)
	}
	return repositories.KnowledgeFAQRepository.UpdatesInTenant(sqls.DB(), faqID, faq.TenantID, map[string]any{
		"index_status": enums.KnowledgeDocumentIndexStatusPending,
		"indexed_at":   nil,
		"index_error":  "",
		"updated_at":   time.Now(),
	})
}

func (s *index) markFAQIndexIndexed(faqID int64) error {
	faq := repositories.KnowledgeFAQRepository.Get(sqls.DB(), faqID)
	if faq == nil || faq.TenantID <= 0 {
		return fmt.Errorf("knowledge faq %d is missing or has no tenant", faqID)
	}
	now := time.Now()
	return repositories.KnowledgeFAQRepository.UpdatesInTenant(sqls.DB(), faqID, faq.TenantID, map[string]any{
		"index_status": enums.KnowledgeDocumentIndexStatusIndexed,
		"indexed_at":   &now,
		"index_error":  "",
		"updated_at":   now,
	})
}

func (s *index) markFAQIndexFailed(faqID int64, err error) error {
	faq := repositories.KnowledgeFAQRepository.Get(sqls.DB(), faqID)
	if faq == nil || faq.TenantID <= 0 {
		return fmt.Errorf("knowledge faq %d is missing or has no tenant", faqID)
	}
	return repositories.KnowledgeFAQRepository.UpdatesInTenant(sqls.DB(), faqID, faq.TenantID, map[string]any{
		"index_status": enums.KnowledgeDocumentIndexStatusFailed,
		"index_error":  truncateIndexError(err),
		"updated_at":   time.Now(),
	})
}

func truncateIndexError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) <= 1000 {
		return message
	}
	return message[:1000]
}
