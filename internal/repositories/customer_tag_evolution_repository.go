package repositories

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ConversationEvolutionStateRepository = &conversationEvolutionStateRepository{}
var ConversationEvolutionRunRepository = &conversationEvolutionRunRepository{}

type conversationEvolutionStateRepository struct{}
type conversationEvolutionRunRepository struct{}

func (r *conversationEvolutionStateRepository) GetByConversationSession(
	db *gorm.DB,
	tenantID, conversationID int64,
	sessionNo int,
) (*models.ConversationEvolutionState, error) {
	ret := &models.ConversationEvolutionState{}
	err := db.Take(ret,
		"tenant_id = ? AND conversation_id = ? AND session_no = ?",
		tenantID, conversationID, sessionNo,
	).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

// Observe advances the inactivity cursor only when messageID is newer. The
// two-step insert/update form works on both SQLite and MySQL and prevents an
// out-of-order observer from moving the durable cursor backwards.
func (r *conversationEvolutionStateRepository) Observe(db *gorm.DB, item *models.ConversationEvolutionState) error {
	if item == nil || item.TenantID <= 0 || item.ConversationID <= 0 || item.SessionNo <= 0 || item.LastObservedMessageID <= 0 {
		return nil
	}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "conversation_id"}, {Name: "session_no"}},
		DoNothing: true,
	}).Create(item).Error; err != nil {
		return err
	}
	return db.Model(&models.ConversationEvolutionState{}).
		Where("tenant_id = ? AND conversation_id = ? AND session_no = ? AND last_observed_message_id < ?",
			item.TenantID, item.ConversationID, item.SessionNo, item.LastObservedMessageID).
		Updates(map[string]any{
			"store_id":                   item.StoreID,
			"customer_id":                item.CustomerID,
			"store_customer_relation_id": item.StoreCustomerRelationID,
			"last_observed_message_id":   item.LastObservedMessageID,
			"next_evolution_at":          item.NextEvolutionAt,
			"last_status":                item.LastStatus,
			"attempt_count":              0,
			"next_retry_at":              nil,
			"last_error_class":           "",
			"status":                     enums.StatusOk,
			"update_user_id":             item.UpdateUserID,
			"update_user_name":           item.UpdateUserName,
			"updated_at":                 item.UpdatedAt,
		}).Error
}

func (r *conversationEvolutionStateRepository) FindDue(db *gorm.DB, now time.Time, limit int) ([]models.ConversationEvolutionState, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	stateTable := db.NamingStrategy.TableName("ConversationEvolutionState")
	storePolicyTable := db.NamingStrategy.TableName("StoreCustomerTagRuntimePolicy")
	tenantPolicyTable := db.NamingStrategy.TableName("TenantCustomerTagPolicy")
	ret := make([]models.ConversationEvolutionState, 0)
	err := db.Model(&models.ConversationEvolutionState{}).
		Select(stateTable+".*").
		Joins(fmt.Sprintf("JOIN %s AS store_policy ON store_policy.tenant_id = %s.tenant_id AND store_policy.store_id = %s.store_id", storePolicyTable, stateTable, stateTable)).
		Joins(fmt.Sprintf("JOIN %s AS tenant_policy ON tenant_policy.tenant_id = %s.tenant_id", tenantPolicyTable, stateTable)).
		Where(stateTable+".status = ?", enums.StatusOk).
		Where("store_policy.status = ? AND store_policy.customer_tag_evolution_enabled = ?", enums.StatusOk, true).
		Where("tenant_policy.status = ?", enums.StatusOk).
		Where(stateTable+".last_observed_message_id > "+stateTable+".last_evolved_message_id").
		Where("(("+stateTable+".next_retry_at IS NOT NULL AND "+stateTable+".next_retry_at <= ?) OR ("+
			stateTable+".next_retry_at IS NULL AND "+stateTable+".next_evolution_at IS NOT NULL AND "+stateTable+".next_evolution_at <= ?))", now, now).
		Where("("+stateTable+".lease_owner = '' OR "+stateTable+".lease_expires_at IS NULL OR "+stateTable+".lease_expires_at <= ?)", now).
		Order("CASE WHEN " + stateTable + ".next_retry_at IS NOT NULL THEN " + stateTable + ".next_retry_at ELSE " + stateTable + ".next_evolution_at END ASC").
		Order(stateTable + ".id ASC").
		Limit(limit).
		Find(&ret).Error
	return ret, err
}

func (r *conversationEvolutionStateRepository) RequeuePendingByTenant(db *gorm.DB, tenantID int64, now time.Time) error {
	if db == nil || tenantID <= 0 {
		return nil
	}
	return db.Model(&models.ConversationEvolutionState{}).
		Where("tenant_id = ? AND status = ? AND last_observed_message_id > last_evolved_message_id", tenantID, enums.StatusOk).
		Where("next_retry_at IS NULL").
		Updates(map[string]any{
			"next_evolution_at": now,
			"updated_at":        now,
		}).Error
}

func (r *conversationEvolutionStateRepository) Claim(
	db *gorm.DB,
	id, tenantID int64,
	owner string,
	now, expiresAt time.Time,
) (bool, error) {
	owner = strings.TrimSpace(owner)
	if id <= 0 || tenantID <= 0 || owner == "" {
		return false, nil
	}
	result := db.Model(&models.ConversationEvolutionState{}).
		Where("id = ? AND tenant_id = ? AND status = ?", id, tenantID, enums.StatusOk).
		Where("last_observed_message_id > last_evolved_message_id").
		Where("((next_retry_at IS NOT NULL AND next_retry_at <= ?) OR (next_retry_at IS NULL AND next_evolution_at IS NOT NULL AND next_evolution_at <= ?))", now, now).
		Where("(lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)", now).
		Updates(map[string]any{
			"lease_owner":      owner,
			"lease_expires_at": expiresAt,
			"last_status":      "processing",
			"updated_at":       now,
		})
	return result.RowsAffected == 1, result.Error
}

func (r *conversationEvolutionStateRepository) RenewLease(
	db *gorm.DB,
	id, tenantID int64,
	owner string,
	expectedMessageID int64,
	expiresAt, updatedAt time.Time,
) (bool, error) {
	result := db.Model(&models.ConversationEvolutionState{}).
		Where("id = ? AND tenant_id = ? AND lease_owner = ? AND last_observed_message_id = ?",
			id, tenantID, strings.TrimSpace(owner), expectedMessageID).
		Updates(map[string]any{"lease_expires_at": expiresAt, "updated_at": updatedAt})
	return result.RowsAffected == 1, result.Error
}

func (r *conversationEvolutionStateRepository) GetForUpdateOwned(
	db *gorm.DB,
	id, tenantID int64,
	owner string,
) (*models.ConversationEvolutionState, error) {
	ret := &models.ConversationEvolutionState{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Take(ret, "id = ? AND tenant_id = ? AND lease_owner = ?", id, tenantID, strings.TrimSpace(owner)).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *conversationEvolutionStateRepository) UpdatesOwned(
	db *gorm.DB,
	id, tenantID int64,
	owner string,
	columns map[string]any,
) (bool, error) {
	result := db.Model(&models.ConversationEvolutionState{}).
		Where("id = ? AND tenant_id = ? AND lease_owner = ?", id, tenantID, strings.TrimSpace(owner)).
		Updates(columns)
	return result.RowsAffected == 1, result.Error
}

func (r *conversationEvolutionStateRepository) UpdatesOwnedAtCheckpoint(
	db *gorm.DB,
	id, tenantID int64,
	owner string,
	expectedMessageID int64,
	columns map[string]any,
) (bool, error) {
	result := db.Model(&models.ConversationEvolutionState{}).
		Where("id = ? AND tenant_id = ? AND lease_owner = ? AND last_observed_message_id = ?",
			id, tenantID, strings.TrimSpace(owner), expectedMessageID).
		Updates(columns)
	return result.RowsAffected == 1, result.Error
}

func (r *conversationEvolutionStateRepository) ReleaseOwned(
	db *gorm.DB,
	id, tenantID int64,
	owner string,
	updatedAt time.Time,
) (bool, error) {
	result := db.Model(&models.ConversationEvolutionState{}).
		Where("id = ? AND tenant_id = ? AND lease_owner = ?", id, tenantID, strings.TrimSpace(owner)).
		Updates(map[string]any{
			"lease_owner": "", "lease_expires_at": nil, "updated_at": updatedAt,
		})
	return result.RowsAffected == 1, result.Error
}

func (r *conversationEvolutionStateRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.ConversationEvolutionState{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Updates(columns).Error
}

func (r *conversationEvolutionStateRepository) FindLatestCommittedMessage(
	db *gorm.DB,
	tenantID, conversationID int64,
	sessionNo int,
) (*models.Message, error) {
	ret := &models.Message{}
	err := db.Where("tenant_id = ? AND conversation_id = ? AND session_no = ?", tenantID, conversationID, sessionNo).
		Where("send_status NOT IN ?", []enums.IMMessageStatus{enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled}).
		Order("id DESC").Take(ret).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *conversationEvolutionStateRepository) FindCommittedMessages(
	db *gorm.DB,
	tenantID, conversationID int64,
	sessionNo int,
	afterMessageID, endMessageID int64,
) ([]models.Message, error) {
	ret := make([]models.Message, 0)
	err := db.Where("tenant_id = ? AND conversation_id = ? AND session_no = ? AND id > ? AND id <= ?",
		tenantID, conversationID, sessionNo, afterMessageID, endMessageID).
		Where("send_status NOT IN ?", []enums.IMMessageStatus{enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled}).
		Order("id ASC").Find(&ret).Error
	return ret, err
}

func (r *conversationEvolutionStateRepository) CreateSessionSummaryIfAbsent(db *gorm.DB, item *models.ConversationSessionSummary) (bool, error) {
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "conversation_id"}, {Name: "session_no"}},
		DoNothing: true,
	}).Create(item)
	return result.RowsAffected == 1, result.Error
}

func (r *conversationEvolutionStateRepository) UpdateSessionSummaryIfOlder(
	db *gorm.DB,
	id, tenantID, checkpoint int64,
	columns map[string]any,
) (bool, error) {
	result := db.Model(&models.ConversationSessionSummary{}).
		Where("id = ? AND tenant_id = ? AND last_message_id < ?", id, tenantID, checkpoint).
		Updates(columns)
	return result.RowsAffected == 1, result.Error
}

func (r *conversationEvolutionRunRepository) GetByCheckpoint(
	db *gorm.DB,
	tenantID, conversationID int64,
	sessionNo int,
	endMessageID int64,
) (*models.ConversationEvolutionRun, error) {
	ret := &models.ConversationEvolutionRun{}
	err := db.Take(ret,
		"tenant_id = ? AND conversation_id = ? AND session_no = ? AND end_message_id = ?",
		tenantID, conversationID, sessionNo, endMessageID,
	).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *conversationEvolutionRunRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.ConversationEvolutionRun, error) {
	ret := &models.ConversationEvolutionRun{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *conversationEvolutionRunRepository) Create(db *gorm.DB, item *models.ConversationEvolutionRun) error {
	return db.Create(item).Error
}

func (r *conversationEvolutionRunRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.ConversationEvolutionRun{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Updates(columns).Error
}
