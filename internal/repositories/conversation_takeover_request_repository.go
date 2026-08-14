package repositories

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ConversationTakeoverRequestRepository = &conversationTakeoverRequestRepository{}

type conversationTakeoverRequestRepository struct{}

func (r *conversationTakeoverRequestRepository) Create(db *gorm.DB, item *models.ConversationTakeoverRequest) error {
	return db.Create(item).Error
}

func (r *conversationTakeoverRequestRepository) GetInTenant(db *gorm.DB, id, tenantID int64) (*models.ConversationTakeoverRequest, error) {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	item := &models.ConversationTakeoverRequest{}
	err := db.First(item, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return item, err
}

func (r *conversationTakeoverRequestRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.ConversationTakeoverRequest, error) {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	item := &models.ConversationTakeoverRequest{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(item, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return item, err
}

func (r *conversationTakeoverRequestRepository) FindPendingByConversationSession(db *gorm.DB, tenantID, conversationID int64, sessionNo int, forUpdate bool) (*models.ConversationTakeoverRequest, error) {
	return r.findByConversationSessionStatuses(db, tenantID, conversationID, sessionNo, []enums.ConversationTakeoverRequestStatus{
		enums.ConversationTakeoverRequestStatusPending,
	}, forUpdate)
}

func (r *conversationTakeoverRequestRepository) FindActiveByConversationSession(db *gorm.DB, tenantID, conversationID int64, sessionNo int, forUpdate bool) (*models.ConversationTakeoverRequest, error) {
	return r.findByConversationSessionStatuses(db, tenantID, conversationID, sessionNo, []enums.ConversationTakeoverRequestStatus{
		enums.ConversationTakeoverRequestStatusPending,
		enums.ConversationTakeoverRequestStatusAuthorized,
	}, forUpdate)
}

func (r *conversationTakeoverRequestRepository) findByConversationSessionStatuses(db *gorm.DB, tenantID, conversationID int64, sessionNo int, statuses []enums.ConversationTakeoverRequestStatus, forUpdate bool) (*models.ConversationTakeoverRequest, error) {
	if db == nil || tenantID <= 0 || conversationID <= 0 || sessionNo <= 0 {
		return nil, nil
	}
	if len(statuses) == 0 {
		return nil, nil
	}
	item := &models.ConversationTakeoverRequest{}
	query := db.Where("tenant_id = ? AND conversation_id = ? AND session_no = ? AND status IN ?", tenantID, conversationID, sessionNo, statuses)
	if forUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Order("id DESC").First(item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return item, err
}

func (r *conversationTakeoverRequestRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.ConversationTakeoverRequest {
	item := &models.ConversationTakeoverRequest{}
	if err := cnd.FindOne(db, item); err != nil {
		return nil
	}
	return item
}

func (r *conversationTakeoverRequestRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, updates map[string]any) error {
	return db.Model(&models.ConversationTakeoverRequest{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates).Error
}

func (r *conversationTakeoverRequestRepository) CancelPendingByConversationSession(db *gorm.DB, tenantID, conversationID int64, sessionNo int, reason string, updates map[string]any) error {
	if db == nil || tenantID <= 0 || conversationID <= 0 || sessionNo <= 0 {
		return nil
	}
	if updates == nil {
		updates = map[string]any{}
	}
	updates["status"] = enums.ConversationTakeoverRequestStatusCancelled
	updates["terminal_reason"] = reason
	updates["active_key"] = nil
	return db.Model(&models.ConversationTakeoverRequest{}).
		Where("tenant_id = ? AND conversation_id = ? AND session_no = ? AND status IN ?", tenantID, conversationID, sessionNo, []enums.ConversationTakeoverRequestStatus{
			enums.ConversationTakeoverRequestStatusPending,
			enums.ConversationTakeoverRequestStatusAuthorized,
		}).
		Updates(updates).Error
}
