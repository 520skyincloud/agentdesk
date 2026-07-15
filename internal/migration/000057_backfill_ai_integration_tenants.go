package migration

import (
	"fmt"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(57, "backfill integrated ai feature tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillIntegratedAIFeatureTenants(ctx.Tx)
		})
	})
}

type integratedAITenantEvidence struct {
	name      string
	id        int64
	tenantIDs map[int64]int64
}

func backfillIntegratedAIFeatureTenants(tx *gorm.DB) error {
	legacy := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacy == nil {
		return fmt.Errorf("legacy default tenant is required before integrated AI tenant backfill")
	}
	validTenantIDs, err := loadValidTenantIDs(tx)
	if err != nil {
		return err
	}
	customers, err := loadConversationDomainTenantIDs(tx, &models.Customer{})
	if err != nil {
		return err
	}
	instances, err := loadConversationDomainTenantIDs(tx, &models.WxWorkProtocolInstance{})
	if err != nil {
		return err
	}
	conversations, err := loadConversationDomainTenantIDs(tx, &models.Conversation{})
	if err != nil {
		return err
	}
	messages, err := loadConversationDomainTenantIDs(tx, &models.Message{})
	if err != nil {
		return err
	}
	companies, err := loadConversationDomainTenantIDs(tx, &models.Company{})
	if err != nil {
		return err
	}
	stores, err := loadConversationDomainTenantIDs(tx, &models.Store{})
	if err != nil {
		return err
	}
	knowledgeBases, err := loadConversationDomainTenantIDs(tx, &models.KnowledgeBase{})
	if err != nil {
		return err
	}

	if err := backfillIntegratedTenantRows(tx, legacy.ID, validTenantIDs, &models.WxWorkCustomerHandoffSetting{}, "customer handoff setting", func(item models.WxWorkCustomerHandoffSetting) (int64, int64, []integratedAITenantEvidence, error) {
		if item.CustomerID <= 0 || item.WxWorkInstanceID <= 0 {
			return 0, 0, nil, fmt.Errorf("customer handoff setting %d is missing required customer or WeCom instance", item.ID)
		}
		return item.ID, item.TenantID, []integratedAITenantEvidence{{"customer", item.CustomerID, customers}, {"WeCom instance", item.WxWorkInstanceID, instances}}, nil
	}, nil); err != nil {
		return err
	}

	if err := backfillIntegratedTenantRows(tx, legacy.ID, validTenantIDs, &models.AIManualResumeTask{}, "AI manual resume task", func(item models.AIManualResumeTask) (int64, int64, []integratedAITenantEvidence, error) {
		if item.ConversationID <= 0 {
			return 0, 0, nil, fmt.Errorf("AI manual resume task %d is missing required conversation", item.ID)
		}
		return item.ID, item.TenantID, []integratedAITenantEvidence{
			{"conversation", item.ConversationID, conversations},
			{"WeCom instance", item.WxWorkInstanceID, instances},
			{"origin message", item.OriginMessageID, messages},
			{"latest waiting message", item.LatestWaitingMessageID, messages},
		}, nil
	}, nil); err != nil {
		return err
	}

	if err := backfillIntegratedTenantRows(tx, legacy.ID, validTenantIDs, &models.KnowledgeResourceGroup{}, "knowledge resource group", func(item models.KnowledgeResourceGroup) (int64, int64, []integratedAITenantEvidence, error) {
		if item.StoreID <= 0 || item.KnowledgeBaseID <= 0 {
			return 0, 0, nil, fmt.Errorf("knowledge resource group %d is missing required store or knowledge base", item.ID)
		}
		return item.ID, item.TenantID, []integratedAITenantEvidence{
			{"company", item.CompanyID, companies}, {"store", item.StoreID, stores},
			{"knowledge base", item.KnowledgeBaseID, knowledgeBases}, {"WeCom instance", item.WxWorkInstanceID, instances},
		}, nil
	}, nil); err != nil {
		return err
	}
	resourceGroups, err := loadConversationDomainTenantIDs(tx, &models.KnowledgeResourceGroup{})
	if err != nil {
		return err
	}
	if err := backfillIntegratedTenantRows(tx, legacy.ID, validTenantIDs, &models.KnowledgeResourceItem{}, "knowledge resource item", func(item models.KnowledgeResourceItem) (int64, int64, []integratedAITenantEvidence, error) {
		if item.KnowledgeResourceGroupID <= 0 {
			return 0, 0, nil, fmt.Errorf("knowledge resource item %d is missing required group", item.ID)
		}
		return item.ID, item.TenantID, []integratedAITenantEvidence{{"knowledge resource group", item.KnowledgeResourceGroupID, resourceGroups}}, nil
	}, nil); err != nil {
		return err
	}

	if err := backfillIntegratedTenantRows(tx, legacy.ID, validTenantIDs, &models.FastGPTDatasetJob{}, "FastGPT dataset job", func(item models.FastGPTDatasetJob) (int64, int64, []integratedAITenantEvidence, error) {
		if item.StoreID <= 0 {
			return 0, 0, nil, fmt.Errorf("FastGPT dataset job %d is missing required store", item.ID)
		}
		return item.ID, item.TenantID, []integratedAITenantEvidence{
			{"company", item.CompanyID, companies}, {"store", item.StoreID, stores}, {"knowledge base", item.KnowledgeBaseID, knowledgeBases},
		}, nil
	}, nil); err != nil {
		return err
	}

	if err := backfillIntegratedTenantRows(tx, legacy.ID, validTenantIDs, &models.AIUsageEvent{}, "AI usage event", func(item models.AIUsageEvent) (int64, int64, []integratedAITenantEvidence, error) {
		return item.ID, item.TenantID, []integratedAITenantEvidence{
			{"company", item.CompanyID, companies}, {"store", item.StoreID, stores}, {"WeCom instance", item.WxWorkInstanceID, instances},
			{"conversation", item.ConversationID, conversations}, {"message", item.MessageID, messages}, {"knowledge base", item.KnowledgeBaseID, knowledgeBases},
		}, nil
	}, nil); err != nil {
		return err
	}

	return backfillIntegratedTenantRows(tx, legacy.ID, validTenantIDs, &models.AIUsageGatewayCall{}, "AI usage gateway call", func(item models.AIUsageGatewayCall) (int64, int64, []integratedAITenantEvidence, error) {
		return item.ID, item.TenantID, []integratedAITenantEvidence{
			{"company", item.CompanyID, companies}, {"store", item.StoreID, stores}, {"WeCom instance", item.WxWorkInstanceID, instances},
			{"conversation", item.ConversationID, conversations}, {"message", item.MessageID, messages},
		}, nil
	}, func(item models.AIUsageGatewayCall) bool {
		return item.TenantID == 0 && item.Stage == "fastgpt_internal_model" && item.CompanyID == 0 && item.StoreID == 0 && item.WxWorkInstanceID == 0 && item.ConversationID == 0 && item.MessageID == 0
	})
}

func backfillIntegratedTenantRows[T any](
	tx *gorm.DB,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	model any,
	resource string,
	fields func(T) (id, tenantID int64, evidence []integratedAITenantEvidence, err error),
	skip func(T) bool,
) error {
	var list []T
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		item := list[i]
		if skip != nil && skip(item) {
			continue
		}
		id, currentTenantID, evidence, err := fields(item)
		if err != nil {
			return err
		}
		resolver := newConversationDomainTenantResolver(resource, id, currentTenantID, validTenantIDs)
		for _, ref := range evidence {
			if ref.id <= 0 {
				continue
			}
			if err := resolver.mergeReference(ref.name, ref.id, ref.tenantIDs); err != nil {
				return err
			}
		}
		tenantID, err := resolver.resolve(legacyTenantID)
		if err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, model, resource, id, currentTenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}
