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
	register(51, "backfill ai run log tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillAIRunLogTenants(ctx.Tx)
		})
	})
}

type aiRunLogMessageEvidence struct {
	TenantID       int64
	ConversationID int64
}

func backfillAIRunLogTenants(tx *gorm.DB) error {
	legacyTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacyTenant == nil {
		return fmt.Errorf("legacy default tenant is required before ai run log tenant backfill")
	}
	validTenantIDs, err := loadValidTenantIDs(tx)
	if err != nil {
		return err
	}
	conversationTenants, err := loadConversationDomainTenantIDs(tx, &models.Conversation{})
	if err != nil {
		return err
	}
	aiAgentTenants, err := loadConversationDomainTenantIDs(tx, &models.AIAgent{})
	if err != nil {
		return err
	}
	messageEvidence, err := loadAIRunLogMessageEvidence(tx)
	if err != nil {
		return err
	}

	if err := backfillSkillRunLogTenants(tx, legacyTenant.ID, validTenantIDs, conversationTenants, aiAgentTenants); err != nil {
		return err
	}
	return backfillAgentRunLogTenants(tx, legacyTenant.ID, validTenantIDs, conversationTenants, aiAgentTenants, messageEvidence)
}

func loadAIRunLogMessageEvidence(tx *gorm.DB) (map[int64]aiRunLogMessageEvidence, error) {
	var messages []models.Message
	if err := tx.Select("id", "tenant_id", "conversation_id").Find(&messages).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]aiRunLogMessageEvidence, len(messages))
	for i := range messages {
		result[messages[i].ID] = aiRunLogMessageEvidence{
			TenantID:       messages[i].TenantID,
			ConversationID: messages[i].ConversationID,
		}
	}
	return result, nil
}

func backfillSkillRunLogTenants(
	tx *gorm.DB,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	conversationTenants map[int64]int64,
	aiAgentTenants map[int64]int64,
) error {
	var logs []models.SkillRunLog
	if err := tx.Order("id ASC").Find(&logs).Error; err != nil {
		return err
	}
	for i := range logs {
		item := &logs[i]
		resolver := newConversationDomainTenantResolver("skill run log", item.ID, item.TenantID, validTenantIDs)
		if item.ConversationID > 0 {
			if err := resolver.mergeReference("conversation", item.ConversationID, conversationTenants); err != nil {
				return err
			}
		}
		if item.AIAgentID > 0 {
			if err := resolver.mergeReference("ai agent", item.AIAgentID, aiAgentTenants); err != nil {
				return err
			}
		}
		tenantID, err := resolver.resolve(legacyTenantID)
		if err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.SkillRunLog{}, "skill run log", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func backfillAgentRunLogTenants(
	tx *gorm.DB,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	conversationTenants map[int64]int64,
	aiAgentTenants map[int64]int64,
	messageEvidence map[int64]aiRunLogMessageEvidence,
) error {
	var logs []models.AgentRunLog
	if err := tx.Order("id ASC").Find(&logs).Error; err != nil {
		return err
	}
	for i := range logs {
		item := &logs[i]
		resolver := newConversationDomainTenantResolver("agent run log", item.ID, item.TenantID, validTenantIDs)
		if item.ConversationID > 0 {
			if err := resolver.mergeReference("conversation", item.ConversationID, conversationTenants); err != nil {
				return err
			}
		}
		if item.MessageID > 0 {
			evidence, ok := messageEvidence[item.MessageID]
			if !ok {
				return fmt.Errorf("agent run log %d references missing message %d", item.ID, item.MessageID)
			}
			if item.ConversationID > 0 && evidence.ConversationID != item.ConversationID {
				return fmt.Errorf("agent run log %d conversation %d conflicts with message %d conversation %d", item.ID, item.ConversationID, item.MessageID, evidence.ConversationID)
			}
			if err := resolver.merge("message", item.MessageID, evidence.TenantID); err != nil {
				return err
			}
		}
		if item.AIAgentID > 0 {
			if err := resolver.mergeReference("ai agent", item.AIAgentID, aiAgentTenants); err != nil {
				return err
			}
		}
		tenantID, err := resolver.resolve(legacyTenantID)
		if err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.AgentRunLog{}, "agent run log", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}
