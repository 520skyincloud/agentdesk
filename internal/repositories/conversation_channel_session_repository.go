package repositories

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ConversationChannelSessionRepository = newConversationChannelSessionRepository()

func newConversationChannelSessionRepository() *conversationChannelSessionRepository {
	return &conversationChannelSessionRepository{}
}

type conversationChannelSessionRepository struct{}

func (r *conversationChannelSessionRepository) TakeByConversationSession(db *gorm.DB, tenantID, conversationID int64, sessionNo int) *models.ConversationChannelSession {
	if db == nil || tenantID <= 0 || conversationID <= 0 || sessionNo <= 0 {
		return nil
	}
	ret := &models.ConversationChannelSession{}
	if err := db.Take(ret, "tenant_id = ? AND conversation_id = ? AND session_no = ?", tenantID, conversationID, sessionNo).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationChannelSessionRepository) GetForUpdateByConversationSession(db *gorm.DB, tenantID, conversationID int64, sessionNo int) (*models.ConversationChannelSession, error) {
	if db == nil || tenantID <= 0 || conversationID <= 0 || sessionNo <= 0 {
		return nil, nil
	}
	ret := &models.ConversationChannelSession{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Take(ret, "tenant_id = ? AND conversation_id = ? AND session_no = ?", tenantID, conversationID, sessionNo).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *conversationChannelSessionRepository) FindActiveForUpdate(db *gorm.DB, tenantID, conversationID int64) (*models.ConversationChannelSession, error) {
	if db == nil || tenantID <= 0 || conversationID <= 0 {
		return nil, nil
	}
	ret := &models.ConversationChannelSession{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND conversation_id = ? AND ended_at IS NULL AND status = ?", tenantID, conversationID, enums.StatusOk).
		Order("session_no DESC").Take(ret).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *conversationChannelSessionRepository) FindByConversation(db *gorm.DB, tenantID, conversationID int64) []models.ConversationChannelSession {
	if db == nil || tenantID <= 0 || conversationID <= 0 {
		return nil
	}
	var list []models.ConversationChannelSession
	sqls.NewCnd().Eq("tenant_id", tenantID).Eq("conversation_id", conversationID).Asc("session_no").Find(db, &list)
	return list
}

func (r *conversationChannelSessionRepository) TakeByConversationInstance(db *gorm.DB, tenantID, conversationID, instanceID int64) *models.ConversationChannelSession {
	if db == nil || tenantID <= 0 || conversationID <= 0 || instanceID <= 0 {
		return nil
	}
	ret := &models.ConversationChannelSession{}
	if err := db.Where(
		"tenant_id = ? AND conversation_id = ? AND wx_work_instance_id = ?",
		tenantID, conversationID, instanceID,
	).Order("session_no DESC").Take(ret).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationChannelSessionRepository) Create(db *gorm.DB, item *models.ConversationChannelSession) error {
	return db.Create(item).Error
}

func (r *conversationChannelSessionRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.ConversationChannelSession{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Updates(columns).Error
}
