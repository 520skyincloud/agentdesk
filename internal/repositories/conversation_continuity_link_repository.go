package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ConversationContinuityLinkRepository = newConversationContinuityLinkRepository()

type conversationContinuityLinkRepository struct{}

func newConversationContinuityLinkRepository() *conversationContinuityLinkRepository {
	return &conversationContinuityLinkRepository{}
}

func (r *conversationContinuityLinkRepository) Create(db *gorm.DB, item *models.ConversationContinuityLink) error {
	return db.Create(item).Error
}

func (r *conversationContinuityLinkRepository) GetForUpdateByPredecessor(db *gorm.DB, tenantID, conversationID int64) (*models.ConversationContinuityLink, error) {
	item := &models.ConversationContinuityLink{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND predecessor_conversation_id = ?", tenantID, conversationID).
		Take(item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return item, err
}

func (r *conversationContinuityLinkRepository) GetForUpdateBySuccessor(db *gorm.DB, tenantID, conversationID int64) (*models.ConversationContinuityLink, error) {
	if db == nil || tenantID <= 0 || conversationID <= 0 {
		return nil, nil
	}
	var items []models.ConversationContinuityLink
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND successor_conversation_id = ?", tenantID, conversationID).
		Order("id ASC").Limit(2).Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) > 1 {
		return nil, fmt.Errorf("conversation %d has multiple active predecessors", conversationID)
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

func (r *conversationContinuityLinkRepository) FindByConversation(db *gorm.DB, tenantID, conversationID int64) (list []models.ConversationContinuityLink) {
	if db == nil || tenantID <= 0 || conversationID <= 0 {
		return list
	}
	db.Where("tenant_id = ? AND (predecessor_conversation_id = ? OR successor_conversation_id = ?)", tenantID, conversationID, conversationID).
		Where("status = ?", enums.StatusOk).
		Order("created_at ASC").Order("id ASC").Find(&list)
	return list
}

func (r *conversationContinuityLinkRepository) FindPredecessorChain(db *gorm.DB, tenantID, conversationID int64, limit int) (list []models.ConversationContinuityLink, err error) {
	if db == nil || tenantID <= 0 || conversationID <= 0 {
		return list, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	seen := map[int64]struct{}{conversationID: {}}
	currentID := conversationID
	for len(list) < limit {
		var batch []models.ConversationContinuityLink
		if err := db.Where("tenant_id = ? AND successor_conversation_id = ? AND status = ?", tenantID, currentID, enums.StatusOk).
			Order("created_at ASC").Order("id ASC").Limit(2).Find(&batch).Error; err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		if len(batch) > 1 {
			return nil, fmt.Errorf("conversation %d has multiple active predecessors", currentID)
		}
		item := batch[0]
		if _, exists := seen[item.PredecessorConversationID]; exists {
			return nil, fmt.Errorf("conversation continuity contains a cycle at %d", item.PredecessorConversationID)
		}
		seen[item.PredecessorConversationID] = struct{}{}
		list = append(list, item)
		currentID = item.PredecessorConversationID
	}
	if len(list) == limit {
		var count int64
		if err := db.Model(&models.ConversationContinuityLink{}).
			Where("tenant_id = ? AND successor_conversation_id = ? AND status = ?", tenantID, currentID, enums.StatusOk).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, fmt.Errorf("conversation continuity exceeds %d predecessors", limit)
		}
	}
	return list, nil
}
