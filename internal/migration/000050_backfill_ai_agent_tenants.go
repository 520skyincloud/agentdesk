package migration

import (
	"fmt"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(50, "backfill ai agent tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillAIAgentTenants(ctx.Tx)
		})
	})
}

func backfillAIAgentTenants(tx *gorm.DB) error {
	legacyTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacyTenant == nil {
		return fmt.Errorf("legacy default tenant is required before ai agent tenant backfill")
	}
	validTenantIDs, err := loadValidTenantIDs(tx)
	if err != nil {
		return err
	}
	teamTenants, err := loadConversationDomainTenantIDs(tx, &models.AgentTeam{})
	if err != nil {
		return err
	}
	knowledgeTenants, err := loadConversationDomainTenantIDs(tx, &models.KnowledgeBase{})
	if err != nil {
		return err
	}
	userTenants, err := loadConversationDomainTenantIDs(tx, &models.User{})
	if err != nil {
		return err
	}

	var agents []models.AIAgent
	if err := tx.Order("id ASC").Find(&agents).Error; err != nil {
		return err
	}
	resolvers := make(map[int64]*conversationDomainTenantResolver, len(agents))
	for i := range agents {
		item := &agents[i]
		resolver := newConversationDomainTenantResolver("ai agent", item.ID, item.TenantID, validTenantIDs)
		for _, teamID := range utils.SplitInt64s(item.TeamIDs) {
			if err := resolver.mergeReference("agent team", teamID, teamTenants); err != nil {
				return err
			}
		}
		for _, knowledgeID := range utils.SplitInt64s(item.KnowledgeIDs) {
			if err := resolver.mergeReference("knowledge base", knowledgeID, knowledgeTenants); err != nil {
				return err
			}
		}
		if err := mergeConversationActorTenant(resolver, "create user", item.CreateUserID, userTenants); err != nil {
			return err
		}
		if err := mergeConversationActorTenant(resolver, "update user", item.UpdateUserID, userTenants); err != nil {
			return err
		}
		resolvers[item.ID] = resolver
	}

	if err := mergeAIAgentTenantReferences[models.Channel](tx, "channel", resolvers, func(item models.Channel) (int64, int64, int64) {
		return item.ID, item.AIAgentID, item.TenantID
	}); err != nil {
		return err
	}
	if err := mergeAIAgentTenantReferences[models.Conversation](tx, "conversation", resolvers, func(item models.Conversation) (int64, int64, int64) {
		return item.ID, item.AIAgentID, item.TenantID
	}); err != nil {
		return err
	}

	for i := range agents {
		item := &agents[i]
		tenantID, err := resolvers[item.ID].resolve(legacyTenant.ID)
		if err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.AIAgent{}, "ai agent", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func mergeAIAgentTenantReferences[T any](
	tx *gorm.DB,
	resource string,
	resolvers map[int64]*conversationDomainTenantResolver,
	fields func(T) (id, aiAgentID, tenantID int64),
) error {
	var list []T
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		id, aiAgentID, tenantID := fields(list[i])
		if aiAgentID <= 0 {
			continue
		}
		resolver := resolvers[aiAgentID]
		if resolver == nil {
			return fmt.Errorf("%s %d references missing ai agent %d", resource, id, aiAgentID)
		}
		if err := resolver.merge(resource, id, tenantID); err != nil {
			return err
		}
	}
	return nil
}
