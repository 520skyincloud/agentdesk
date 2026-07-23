package migration

import (
	"fmt"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(48, "backfill knowledge domain tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillKnowledgeDomainTenants(ctx.Tx)
		})
	})
}

func backfillKnowledgeDomainTenants(tx *gorm.DB) error {
	validTenantIDs, err := loadValidTenantIDs(tx)
	if err != nil {
		return err
	}
	legacyTenant := &models.Tenant{}
	if err := tx.Take(legacyTenant, "tenant_code = ?", constants.LegacyDefaultTenantCode).Error; err != nil {
		return fmt.Errorf("knowledge tenant backfill requires %s tenant: %w", constants.LegacyDefaultTenantCode, err)
	}

	storeTenants, err := loadConversationDomainTenantIDs(tx, &models.Store{})
	if err != nil {
		return err
	}
	wxWorkTenants, err := loadConversationDomainTenantIDs(tx, &models.WxWorkProtocolInstance{})
	if err != nil {
		return err
	}
	routeTenants, err := loadConversationDomainTenantIDs(tx, &models.ConversationRouteState{})
	if err != nil {
		return err
	}
	conversationTenants, err := loadConversationDomainTenantIDs(tx, &models.Conversation{})
	if err != nil {
		return err
	}
	userTenants, err := loadConversationDomainTenantIDs(tx, &models.User{})
	if err != nil {
		return err
	}

	knowledgeBaseTenants, err := backfillKnowledgeBaseTenants(tx, legacyTenant.ID, validTenantIDs, storeTenants, wxWorkTenants, routeTenants, userTenants)
	if err != nil {
		return err
	}
	documentTenants, documentKnowledgeBases, err := backfillKnowledgeDocumentTenants(tx, validTenantIDs, knowledgeBaseTenants)
	if err != nil {
		return err
	}
	faqTenants, faqKnowledgeBases, err := backfillKnowledgeFAQTenants(tx, validTenantIDs, knowledgeBaseTenants)
	if err != nil {
		return err
	}
	chunkTenants, chunkKnowledgeBases, err := backfillKnowledgeChunkTenants(tx, validTenantIDs, knowledgeBaseTenants, documentTenants, documentKnowledgeBases, faqTenants, faqKnowledgeBases)
	if err != nil {
		return err
	}
	if err := backfillKnowledgeCandidateTenants(tx, legacyTenant.ID, validTenantIDs, knowledgeBaseTenants, storeTenants, conversationTenants, userTenants); err != nil {
		return err
	}
	retrieveLogTenants, err := backfillKnowledgeRetrieveLogTenants(tx, legacyTenant.ID, validTenantIDs, knowledgeBaseTenants, conversationTenants)
	if err != nil {
		return err
	}
	if err := backfillKnowledgeRetrieveHitTenants(tx, validTenantIDs, retrieveLogTenants, knowledgeBaseTenants, chunkTenants, chunkKnowledgeBases, documentTenants, documentKnowledgeBases, faqTenants, faqKnowledgeBases); err != nil {
		return err
	}
	if err := backfillKnowledgeFeedbackTenants(tx, validTenantIDs, retrieveLogTenants); err != nil {
		return err
	}
	return markTenantKnowledgeIndexesPending(tx)
}

func backfillKnowledgeBaseTenants(
	tx *gorm.DB,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	storeTenants, wxWorkTenants, routeTenants, userTenants map[int64]int64,
) (map[int64]int64, error) {
	var bases []models.KnowledgeBase
	if err := tx.Order("id ASC").Find(&bases).Error; err != nil {
		return nil, err
	}
	resolvers := make(map[int64]*conversationDomainTenantResolver, len(bases))
	for i := range bases {
		item := &bases[i]
		resolver := newConversationDomainTenantResolver("knowledge base", item.ID, item.TenantID, validTenantIDs)
		if err := mergeConversationActorTenant(resolver, "create user", item.CreateUserID, userTenants); err != nil {
			return nil, err
		}
		resolvers[item.ID] = resolver
	}
	mergeReference := func(resource string, resourceID, knowledgeBaseID int64, tenants map[int64]int64) error {
		if knowledgeBaseID <= 0 {
			return nil
		}
		resolver, ok := resolvers[knowledgeBaseID]
		if !ok {
			return fmt.Errorf("%s %d references missing knowledge base %d", resource, resourceID, knowledgeBaseID)
		}
		return resolver.mergeReference(resource, resourceID, tenants)
	}
	var stores []models.Store
	if err := tx.Where("knowledge_base_id > ?", 0).Order("id ASC").Find(&stores).Error; err != nil {
		return nil, err
	}
	for i := range stores {
		if err := mergeReference("store", stores[i].ID, stores[i].KnowledgeBaseID, storeTenants); err != nil {
			return nil, err
		}
	}
	var instances []models.WxWorkProtocolInstance
	if err := tx.Where("knowledge_base_id > ?", 0).Order("id ASC").Find(&instances).Error; err != nil {
		return nil, err
	}
	for i := range instances {
		if err := mergeReference("wxwork instance", instances[i].ID, instances[i].KnowledgeBaseID, wxWorkTenants); err != nil {
			return nil, err
		}
	}
	var routes []models.ConversationRouteState
	if err := tx.Where("knowledge_base_id > ?", 0).Order("id ASC").Find(&routes).Error; err != nil {
		return nil, err
	}
	for i := range routes {
		if err := mergeReference("conversation route state", routes[i].ID, routes[i].KnowledgeBaseID, routeTenants); err != nil {
			return nil, err
		}
	}
	result := make(map[int64]int64, len(bases))
	for i := range bases {
		item := &bases[i]
		tenantID, err := resolvers[item.ID].resolve(legacyTenantID)
		if err != nil {
			return nil, err
		}
		if err := assignConversationDomainTenant(tx, &models.KnowledgeBase{}, "knowledge base", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return nil, err
		}
		result[item.ID] = tenantID
	}
	return result, nil
}

func backfillKnowledgeDocumentTenants(tx *gorm.DB, validTenantIDs map[int64]struct{}, knowledgeBaseTenants map[int64]int64) (map[int64]int64, map[int64]int64, error) {
	if !tx.Migrator().HasTable(&legacyKnowledgeDocument{}) {
		return map[int64]int64{}, map[int64]int64{}, nil
	}
	var list []legacyKnowledgeDocument
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return nil, nil, err
	}
	tenantIDs := make(map[int64]int64, len(list))
	knowledgeBaseIDs := make(map[int64]int64, len(list))
	for i := range list {
		item := &list[i]
		tenantID, err := requiredConversationDomainParentTenant("knowledge document", item.ID, "knowledge base", item.KnowledgeBaseID, knowledgeBaseTenants)
		if err != nil {
			return nil, nil, err
		}
		if err := assignConversationDomainTenant(tx, &legacyKnowledgeDocument{}, "knowledge document", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return nil, nil, err
		}
		tenantIDs[item.ID] = tenantID
		knowledgeBaseIDs[item.ID] = item.KnowledgeBaseID
	}
	return tenantIDs, knowledgeBaseIDs, nil
}

func backfillKnowledgeFAQTenants(tx *gorm.DB, validTenantIDs map[int64]struct{}, knowledgeBaseTenants map[int64]int64) (map[int64]int64, map[int64]int64, error) {
	if !tx.Migrator().HasTable(&legacyKnowledgeFAQ{}) {
		return map[int64]int64{}, map[int64]int64{}, nil
	}
	var list []legacyKnowledgeFAQ
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return nil, nil, err
	}
	tenantIDs := make(map[int64]int64, len(list))
	knowledgeBaseIDs := make(map[int64]int64, len(list))
	for i := range list {
		item := &list[i]
		tenantID, err := requiredConversationDomainParentTenant("knowledge faq", item.ID, "knowledge base", item.KnowledgeBaseID, knowledgeBaseTenants)
		if err != nil {
			return nil, nil, err
		}
		if err := assignConversationDomainTenant(tx, &legacyKnowledgeFAQ{}, "knowledge faq", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return nil, nil, err
		}
		tenantIDs[item.ID] = tenantID
		knowledgeBaseIDs[item.ID] = item.KnowledgeBaseID
	}
	return tenantIDs, knowledgeBaseIDs, nil
}

func backfillKnowledgeChunkTenants(
	tx *gorm.DB,
	validTenantIDs map[int64]struct{},
	knowledgeBaseTenants, documentTenants, documentKnowledgeBases, faqTenants, faqKnowledgeBases map[int64]int64,
) (map[int64]int64, map[int64]int64, error) {
	if !tx.Migrator().HasTable(&legacyKnowledgeChunk{}) {
		return map[int64]int64{}, map[int64]int64{}, nil
	}
	var list []legacyKnowledgeChunk
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return nil, nil, err
	}
	tenantIDs := make(map[int64]int64, len(list))
	knowledgeBaseIDs := make(map[int64]int64, len(list))
	for i := range list {
		item := &list[i]
		tenantID, err := requiredConversationDomainParentTenant("knowledge chunk", item.ID, "knowledge base", item.KnowledgeBaseID, knowledgeBaseTenants)
		if err != nil {
			return nil, nil, err
		}
		if err := validateOptionalConversationDomainReference("knowledge chunk", item.ID, tenantID, "document", item.DocumentID, documentTenants); err != nil {
			return nil, nil, err
		}
		if item.DocumentID > 0 && documentKnowledgeBases[item.DocumentID] != item.KnowledgeBaseID {
			return nil, nil, fmt.Errorf("knowledge chunk %d knowledge base %d conflicts with document %d knowledge base %d", item.ID, item.KnowledgeBaseID, item.DocumentID, documentKnowledgeBases[item.DocumentID])
		}
		if err := validateOptionalConversationDomainReference("knowledge chunk", item.ID, tenantID, "faq", item.FaqID, faqTenants); err != nil {
			return nil, nil, err
		}
		if item.FaqID > 0 && faqKnowledgeBases[item.FaqID] != item.KnowledgeBaseID {
			return nil, nil, fmt.Errorf("knowledge chunk %d knowledge base %d conflicts with faq %d knowledge base %d", item.ID, item.KnowledgeBaseID, item.FaqID, faqKnowledgeBases[item.FaqID])
		}
		if err := assignConversationDomainTenant(tx, &legacyKnowledgeChunk{}, "knowledge chunk", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return nil, nil, err
		}
		tenantIDs[item.ID] = tenantID
		knowledgeBaseIDs[item.ID] = item.KnowledgeBaseID
	}
	return tenantIDs, knowledgeBaseIDs, nil
}

func backfillKnowledgeCandidateTenants(
	tx *gorm.DB,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	knowledgeBaseTenants, storeTenants, conversationTenants, userTenants map[int64]int64,
) error {
	var list []models.KnowledgeCandidate
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		item := &list[i]
		resolver := newConversationDomainTenantResolver("knowledge candidate", item.ID, item.TenantID, validTenantIDs)
		if item.KnowledgeBaseID > 0 {
			if err := resolver.mergeReference("knowledge base", item.KnowledgeBaseID, knowledgeBaseTenants); err != nil {
				return err
			}
		}
		if item.StoreID > 0 {
			if err := resolver.mergeReference("store", item.StoreID, storeTenants); err != nil {
				return err
			}
		}
		if item.ConversationID > 0 {
			if err := resolver.mergeReference("conversation", item.ConversationID, conversationTenants); err != nil {
				return err
			}
		}
		for _, actor := range []struct {
			name string
			id   int64
		}{{"create user", item.CreateUserID}, {"update user", item.UpdateUserID}, {"review user", item.ReviewUserID}} {
			if err := mergeConversationActorTenant(resolver, actor.name, actor.id, userTenants); err != nil {
				return err
			}
		}
		tenantID, err := resolver.resolve(legacyTenantID)
		if err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.KnowledgeCandidate{}, "knowledge candidate", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func backfillKnowledgeRetrieveLogTenants(
	tx *gorm.DB,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	knowledgeBaseTenants, conversationTenants map[int64]int64,
) (map[int64]int64, error) {
	var list []models.KnowledgeRetrieveLog
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]int64, len(list))
	for i := range list {
		item := &list[i]
		resolver := newConversationDomainTenantResolver("knowledge retrieve log", item.ID, item.TenantID, validTenantIDs)
		if item.KnowledgeBaseID > 0 {
			if err := resolver.mergeReference("knowledge base", item.KnowledgeBaseID, knowledgeBaseTenants); err != nil {
				return nil, err
			}
		}
		if item.ConversationID > 0 {
			if err := resolver.mergeReference("conversation", item.ConversationID, conversationTenants); err != nil {
				return nil, err
			}
		}
		tenantID, err := resolver.resolve(legacyTenantID)
		if err != nil {
			return nil, err
		}
		if err := assignConversationDomainTenant(tx, &models.KnowledgeRetrieveLog{}, "knowledge retrieve log", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return nil, err
		}
		result[item.ID] = tenantID
	}
	return result, nil
}

func backfillKnowledgeRetrieveHitTenants(
	tx *gorm.DB,
	validTenantIDs map[int64]struct{},
	retrieveLogTenants, knowledgeBaseTenants, chunkTenants, chunkKnowledgeBases, documentTenants, documentKnowledgeBases, faqTenants, faqKnowledgeBases map[int64]int64,
) error {
	var list []models.KnowledgeRetrieveHit
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		item := &list[i]
		tenantID, err := requiredConversationDomainParentTenant("knowledge retrieve hit", item.ID, "retrieve log", item.RetrieveLogID, retrieveLogTenants)
		if err != nil {
			return err
		}
		for _, reference := range []struct {
			name      string
			id        int64
			tenantIDs map[int64]int64
			baseIDs   map[int64]int64
		}{{"knowledge base", item.KnowledgeBaseID, knowledgeBaseTenants, nil}, {"chunk", item.ChunkID, chunkTenants, chunkKnowledgeBases}, {"document", item.DocumentID, documentTenants, documentKnowledgeBases}, {"faq", item.FaqID, faqTenants, faqKnowledgeBases}} {
			if err := validateOptionalConversationDomainReference("knowledge retrieve hit", item.ID, tenantID, reference.name, reference.id, reference.tenantIDs); err != nil {
				return err
			}
			if item.KnowledgeBaseID > 0 && reference.id > 0 && reference.baseIDs != nil && reference.baseIDs[reference.id] != item.KnowledgeBaseID {
				return fmt.Errorf("knowledge retrieve hit %d knowledge base %d conflicts with %s %d knowledge base %d", item.ID, item.KnowledgeBaseID, reference.name, reference.id, reference.baseIDs[reference.id])
			}
		}
		if err := assignConversationDomainTenant(tx, &models.KnowledgeRetrieveHit{}, "knowledge retrieve hit", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func backfillKnowledgeFeedbackTenants(tx *gorm.DB, validTenantIDs map[int64]struct{}, retrieveLogTenants map[int64]int64) error {
	var list []models.KnowledgeFeedback
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		item := &list[i]
		tenantID, err := requiredConversationDomainParentTenant("knowledge feedback", item.ID, "retrieve log", item.RetrieveLogID, retrieveLogTenants)
		if err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.KnowledgeFeedback{}, "knowledge feedback", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func markTenantKnowledgeIndexesPending(tx *gorm.DB) error {
	if tx.Migrator().HasTable(&legacyKnowledgeDocument{}) {
		if err := tx.Model(&legacyKnowledgeDocument{}).
			Where("tenant_id > ? AND status <> ? AND index_status <> ?", 0, enums.StatusDeleted, legacyKnowledgeIndexStatusPending).
			Updates(map[string]any{
				"index_status": legacyKnowledgeIndexStatusPending,
				"indexed_at":   nil,
				"index_error":  "",
			}).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasTable(&legacyKnowledgeFAQ{}) {
		return tx.Model(&legacyKnowledgeFAQ{}).
			Where("tenant_id > ? AND status <> ? AND index_status <> ?", 0, enums.StatusDeleted, legacyKnowledgeIndexStatusPending).
			Updates(map[string]any{
				"index_status": legacyKnowledgeIndexStatusPending,
				"indexed_at":   nil,
				"index_error":  "",
			}).Error
	}
	return nil
}
