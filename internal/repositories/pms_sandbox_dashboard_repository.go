package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

// Customer references come only from conversations routed to this store.
func PMSSandboxCustomerConversations(db *gorm.DB, storeID int64) ([]models.Conversation, error) {
	var rows []models.Conversation
	err := db.Where("id IN (?)", db.Model(&models.ConversationRouteState{}).Select("conversation_id").Where("store_id = ?", storeID)).
		Where("customer_id > 0").Order("id DESC").Limit(500).Find(&rows).Error
	return rows, err
}
