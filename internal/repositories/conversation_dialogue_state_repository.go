package repositories

import (
	"errors"

	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ConversationDialogueStateRepository = &conversationDialogueStateRepository{}

type conversationDialogueStateRepository struct{}

func (r *conversationDialogueStateRepository) GetByScope(db *gorm.DB, tenantID, conversationID int64, sessionNo int) *models.ConversationDialogueState {
	if db == nil || tenantID <= 0 || conversationID <= 0 || sessionNo <= 0 {
		return nil
	}
	ret := &models.ConversationDialogueState{}
	if err := db.Take(ret, "tenant_id = ? AND conversation_id = ? AND session_no = ?", tenantID, conversationID, sessionNo).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationDialogueStateRepository) GetForUpdateByScope(db *gorm.DB, tenantID, conversationID int64, sessionNo int) (*models.ConversationDialogueState, error) {
	if db == nil || tenantID <= 0 || conversationID <= 0 || sessionNo <= 0 {
		return nil, nil
	}
	ret := &models.ConversationDialogueState{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(ret,
		"tenant_id = ? AND conversation_id = ? AND session_no = ?", tenantID, conversationID, sessionNo).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return ret, err
}

func (r *conversationDialogueStateRepository) CreateIfAbsent(db *gorm.DB, item *models.ConversationDialogueState) (bool, error) {
	if db == nil || item == nil {
		return false, nil
	}
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "conversation_id"}, {Name: "session_no"}},
		DoNothing: true,
	}).Create(item)
	return result.RowsAffected == 1, result.Error
}

func (r *conversationDialogueStateRepository) CASUpdate(db *gorm.DB, id, tenantID int64, expectedRevision int64, columns map[string]any) (bool, error) {
	if db == nil || id <= 0 || tenantID <= 0 || expectedRevision <= 0 || len(columns) == 0 {
		return false, nil
	}
	result := db.Model(&models.ConversationDialogueState{}).
		Where("id = ? AND tenant_id = ? AND revision = ?", id, tenantID, expectedRevision).
		Updates(columns)
	return result.RowsAffected == 1, result.Error
}
