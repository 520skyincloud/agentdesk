package repositories

import (
	"time"

	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var CustomerTagRelationRepository = newCustomerTagRelationRepository()
var CustomerTagChangeLogRepository = newCustomerTagChangeLogRepository()
var ConversationEvolutionStateRepository = newConversationEvolutionStateRepository()
var ConversationEvolutionRunRepository = newConversationEvolutionRunRepository()

type customerTagRelationRepository struct{}
type customerTagChangeLogRepository struct{}
type conversationEvolutionStateRepository struct{}
type conversationEvolutionRunRepository struct{}

func newCustomerTagRelationRepository() *customerTagRelationRepository {
	return &customerTagRelationRepository{}
}
func newCustomerTagChangeLogRepository() *customerTagChangeLogRepository {
	return &customerTagChangeLogRepository{}
}
func newConversationEvolutionStateRepository() *conversationEvolutionStateRepository {
	return &conversationEvolutionStateRepository{}
}
func newConversationEvolutionRunRepository() *conversationEvolutionRunRepository {
	return &conversationEvolutionRunRepository{}
}

func (r *customerTagRelationRepository) Get(db *gorm.DB, id int64) *models.CustomerTagRelation {
	item := &models.CustomerTagRelation{}
	if err := db.First(item, "id = ?", id).Error; err != nil {
		return nil
	}
	return item
}

func (r *customerTagRelationRepository) GetByRelationAndTag(db *gorm.DB, relationID, tagID int64) *models.CustomerTagRelation {
	item := &models.CustomerTagRelation{}
	if err := db.Take(item, "store_customer_relation_id = ? AND tag_id = ?", relationID, tagID).Error; err != nil {
		return nil
	}
	return item
}

func (r *customerTagRelationRepository) TakeByTagID(db *gorm.DB, tagID int64) *models.CustomerTagRelation {
	item := &models.CustomerTagRelation{}
	if err := db.Take(item, "tag_id = ?", tagID).Error; err != nil {
		return nil
	}
	return item
}

func (r *customerTagRelationRepository) FindActiveByRelationID(db *gorm.DB, relationID int64) []models.CustomerTagRelation {
	var list []models.CustomerTagRelation
	sqls.NewCnd().Eq("store_customer_relation_id", relationID).Eq("relation_status", "active").Asc("id").Find(db, &list)
	return list
}

func (r *customerTagRelationRepository) FindActiveByRelationIDWithError(db *gorm.DB, relationID int64) ([]models.CustomerTagRelation, error) {
	var list []models.CustomerTagRelation
	err := db.Where("store_customer_relation_id = ? AND relation_status = ?", relationID, "active").Order("id ASC").Find(&list).Error
	return list, err
}

func (r *customerTagRelationRepository) CountActiveByRelationID(db *gorm.DB, relationID int64) int64 {
	return sqls.NewCnd().Eq("store_customer_relation_id", relationID).Eq("relation_status", "active").Count(db, &models.CustomerTagRelation{})
}

func (r *customerTagRelationRepository) Create(db *gorm.DB, item *models.CustomerTagRelation) error {
	return db.Create(item).Error
}

func (r *customerTagRelationRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.CustomerTagRelation{}).Where("id = ?", id).Updates(columns).Error
}

func (r *customerTagChangeLogRepository) Create(db *gorm.DB, item *models.CustomerTagChangeLog) error {
	return db.Create(item).Error
}

func (r *customerTagChangeLogRepository) FindPageByRelationID(db *gorm.DB, relationID int64, page, limit int) ([]models.CustomerTagChangeLog, *sqls.Paging, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	var total int64
	query := db.Model(&models.CustomerTagChangeLog{}).Where("store_customer_relation_id = ?", relationID)
	if err := query.Count(&total).Error; err != nil {
		return nil, nil, err
	}
	var list []models.CustomerTagChangeLog
	if err := query.Order("id DESC").Offset((page - 1) * limit).Limit(limit).Find(&list).Error; err != nil {
		return nil, nil, err
	}
	return list, &sqls.Paging{Page: page, Limit: limit, Total: total}, nil
}

func (r *conversationEvolutionStateRepository) GetByConversationSession(db *gorm.DB, conversationID int64, sessionNo int) *models.ConversationEvolutionState {
	item := &models.ConversationEvolutionState{}
	if err := db.Take(item, "conversation_id = ? AND session_no = ?", conversationID, sessionNo).Error; err != nil {
		return nil
	}
	return item
}

func (r *conversationEvolutionStateRepository) Upsert(db *gorm.DB, item *models.ConversationEvolutionState) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "conversation_id"}, {Name: "session_no"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"company_id", "store_id", "customer_id", "store_customer_relation_id",
			"last_observed_message_id", "next_evolution_at", "last_status",
			"last_error_class", "status", "updated_at", "update_user_id", "update_user_name",
		}),
	}).Create(item).Error
}

func (r *conversationEvolutionStateRepository) FindDue(db *gorm.DB, now time.Time, storeIDs []int64, limit int) []models.ConversationEvolutionState {
	if len(storeIDs) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	var list []models.ConversationEvolutionState
	db.Where("status = ? AND next_evolution_at IS NOT NULL AND next_evolution_at <= ? AND last_observed_message_id > last_evolved_message_id", 0, now).
		Where("store_id IN ?", storeIDs).
		Order("next_evolution_at ASC, id ASC").
		Limit(limit).
		Find(&list)
	return list
}

func (r *conversationEvolutionStateRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.ConversationEvolutionState{}).Where("id = ?", id).Updates(columns).Error
}

func (r *conversationEvolutionRunRepository) GetByCheckpoint(db *gorm.DB, conversationID int64, sessionNo int, endMessageID int64) *models.ConversationEvolutionRun {
	item := &models.ConversationEvolutionRun{}
	if err := db.Take(item, "conversation_id = ? AND session_no = ? AND end_message_id = ?", conversationID, sessionNo, endMessageID).Error; err != nil {
		return nil
	}
	return item
}

func (r *conversationEvolutionRunRepository) Create(db *gorm.DB, item *models.ConversationEvolutionRun) error {
	return db.Create(item).Error
}

func (r *conversationEvolutionRunRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.ConversationEvolutionRun{}).Where("id = ?", id).Updates(columns).Error
}
