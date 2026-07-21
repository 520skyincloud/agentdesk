package repositories

import (
	"errors"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ConversationAssignmentRepository = newConversationAssignmentRepository()

func newConversationAssignmentRepository() *conversationAssignmentRepository {
	return &conversationAssignmentRepository{}
}

type conversationAssignmentRepository struct {
}

func (r *conversationAssignmentRepository) Get(db *gorm.DB, id int64) *models.ConversationAssignment {
	ret := &models.ConversationAssignment{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationAssignmentRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.ConversationAssignment, error) {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	ret := &models.ConversationAssignment{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *conversationAssignmentRepository) Take(db *gorm.DB, where ...interface{}) *models.ConversationAssignment {
	ret := &models.ConversationAssignment{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationAssignmentRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.ConversationAssignment) {
	cnd.Find(db, &list)
	return
}

func (r *conversationAssignmentRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.ConversationAssignment {
	ret := &models.ConversationAssignment{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *conversationAssignmentRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.ConversationAssignment, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *conversationAssignmentRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.ConversationAssignment, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.ConversationAssignment{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *conversationAssignmentRepository) FindActiveRuleWithoutHumanReply(db *gorm.DB, tenantID, userID int64, limit int) (list []models.ConversationAssignment) {
	if db == nil {
		return list
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := db.Table("t_conversation_assignment AS ca").
		Select("ca.*").
		Joins("JOIN t_conversation AS c ON c.id = ca.conversation_id AND c.tenant_id = ca.tenant_id").
		Where("ca.status = ?", enums.IMAssignmentStatusActive).
		Where("ca.dispatch_mode = ?", enums.AgentTeamDispatchModeRule).
		Where("c.status = ?", enums.IMConversationStatusActive).
		Where("c.current_assignee_id = ca.to_user_id").
		Where(`NOT EXISTS (
			SELECT 1 FROM t_message AS m
			WHERE m.tenant_id = ca.tenant_id
			AND m.conversation_id = ca.conversation_id
			AND m.session_no = ca.session_no
			AND m.sender_type = ?
			AND m.sender_id = ca.to_user_id
			AND m.created_at >= ca.created_at
			AND m.send_status NOT IN (?, ?)
		)`, enums.IMSenderTypeAgent, enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled)
	if tenantID > 0 {
		query = query.Where("ca.tenant_id = ?", tenantID)
	}
	if userID > 0 {
		query = query.Where("ca.to_user_id = ?", userID)
	}
	query.Order("ca.created_at ASC").Order("ca.id ASC").Limit(limit).Scan(&list)
	return list
}

func (r *conversationAssignmentRepository) FindActiveRuleWithHumanReplyAndCustomerWaiting(db *gorm.DB, tenantID, userID int64, limit int) (list []models.ConversationAssignment) {
	if db == nil {
		return list
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := db.Table("t_conversation_assignment AS ca").
		Select("ca.*").
		Joins("JOIN t_conversation AS c ON c.id = ca.conversation_id AND c.tenant_id = ca.tenant_id").
		Where("ca.status = ?", enums.IMAssignmentStatusActive).
		Where("ca.dispatch_mode = ?", enums.AgentTeamDispatchModeRule).
		Where("c.status = ?", enums.IMConversationStatusActive).
		Where("c.current_assignee_id = ca.to_user_id").
		Where(`EXISTS (
			SELECT 1 FROM t_message AS m
			WHERE m.tenant_id = ca.tenant_id
			AND m.conversation_id = ca.conversation_id
			AND m.session_no = ca.session_no
			AND m.sender_type = ?
			AND m.sender_id = ca.to_user_id
			AND m.created_at >= ca.created_at
				AND m.send_status NOT IN (?, ?)
			)`, enums.IMSenderTypeAgent, enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Where(`EXISTS (
			SELECT 1 FROM t_message AS customer
			WHERE customer.tenant_id = ca.tenant_id
			AND customer.conversation_id = ca.conversation_id
			AND customer.session_no = ca.session_no
			AND customer.sender_type = ?
			AND customer.created_at >= ca.created_at
			AND customer.send_status NOT IN (?, ?)
			AND customer.seq_no > COALESCE((
				SELECT MAX(reply.seq_no) FROM t_message AS reply
				WHERE reply.tenant_id = ca.tenant_id
				AND reply.conversation_id = ca.conversation_id
				AND reply.session_no = ca.session_no
				AND reply.sender_type = ?
				AND reply.sender_id = ca.to_user_id
				AND reply.created_at >= ca.created_at
				AND reply.send_status NOT IN (?, ?)
			), 0)
		)`,
			enums.IMSenderTypeCustomer, enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled,
			enums.IMSenderTypeAgent, enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled,
		)
	if tenantID > 0 {
		query = query.Where("ca.tenant_id = ?", tenantID)
	}
	if userID > 0 {
		query = query.Where("ca.to_user_id = ?", userID)
	}
	query.Order("ca.created_at ASC").Order("ca.id ASC").Limit(limit).Scan(&list)
	return list
}

func (r *conversationAssignmentRepository) HasHumanReplySince(db *gorm.DB, assignment *models.ConversationAssignment) (bool, error) {
	if db == nil || assignment == nil || assignment.TenantID <= 0 || assignment.ConversationID <= 0 || assignment.SessionNo <= 0 {
		return false, nil
	}
	var count int64
	err := db.Model(&models.Message{}).
		Where("tenant_id = ? AND conversation_id = ? AND session_no = ?", assignment.TenantID, assignment.ConversationID, assignment.SessionNo).
		Where("sender_type = ? AND sender_id = ? AND created_at >= ?", enums.IMSenderTypeAgent, assignment.ToUserID, assignment.CreatedAt).
		Where("send_status NOT IN ?", []enums.IMMessageStatus{enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled}).
		Limit(1).
		Count(&count).Error
	return count > 0, err
}

// FindSuccessfulRepliedAssigneeIDs returns only assignees who actually sent a
// valid human reply during their assignment segment. The current service
// session can be excluded while retaining older sessions of a reused
// Conversation.
func (r *conversationAssignmentRepository) FindSuccessfulRepliedAssigneeIDs(
	db *gorm.DB,
	tenantID int64,
	conversationIDs, userIDs []int64,
	currentConversationID int64,
	currentSessionNo int,
) ([]int64, error) {
	ret := make([]int64, 0)
	if db == nil || tenantID <= 0 || len(conversationIDs) == 0 || len(userIDs) == 0 {
		return ret, nil
	}
	query := db.Table("t_conversation_assignment AS ca").
		Distinct("ca.to_user_id").
		Joins(`JOIN t_message AS m
			ON m.tenant_id = ca.tenant_id
			AND m.conversation_id = ca.conversation_id
			AND (ca.session_no <= 0 OR m.session_no = ca.session_no)
			AND m.sender_type = ?
			AND m.sender_id = ca.to_user_id
			AND m.created_at >= ca.created_at
			AND (ca.finished_at IS NULL OR m.created_at <= ca.finished_at)
			AND m.send_status NOT IN (?, ?)`, enums.IMSenderTypeAgent, enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Where("ca.tenant_id = ? AND ca.conversation_id IN ? AND ca.to_user_id IN ?", tenantID, conversationIDs, userIDs)
	if currentConversationID > 0 && currentSessionNo > 0 {
		query = query.Where("NOT (ca.conversation_id = ? AND ca.session_no = ?)", currentConversationID, currentSessionNo)
	}
	if err := query.Pluck("ca.to_user_id", &ret).Error; err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *conversationAssignmentRepository) CountRuleAssignmentsForSession(db *gorm.DB, tenantID, conversationID int64, sessionNo int) (int64, error) {
	if db == nil || tenantID <= 0 || conversationID <= 0 || sessionNo <= 0 {
		return 0, nil
	}
	var count int64
	err := db.Model(&models.ConversationAssignment{}).
		Where("tenant_id = ? AND conversation_id = ? AND session_no = ?", tenantID, conversationID, sessionNo).
		Where("dispatch_mode = ?", enums.AgentTeamDispatchModeRule).
		Count(&count).Error
	return count, err
}

func (r *conversationAssignmentRepository) FindRecentRuleAssignmentsForSession(db *gorm.DB, tenantID, conversationID int64, sessionNo int, since time.Time) (list []models.ConversationAssignment, err error) {
	if db == nil || tenantID <= 0 || conversationID <= 0 || sessionNo <= 0 || since.IsZero() {
		return list, nil
	}
	err = db.Where("tenant_id = ? AND conversation_id = ? AND session_no = ?", tenantID, conversationID, sessionNo).
		Where("dispatch_mode = ? AND created_at >= ?", enums.AgentTeamDispatchModeRule, since).
		Order("created_at DESC").
		Order("id DESC").
		Find(&list).Error
	return list, err
}

// FindShiftWorkAssignments returns assignment segments that still represent
// work for the assignee: the current active segment, or a finished segment in
// which that assignee sent at least one successful human reply.
func (r *conversationAssignmentRepository) FindShiftWorkAssignments(db *gorm.DB, tenantID int64, userIDs []int64, startedAt time.Time) (list []models.ConversationAssignment, err error) {
	if db == nil || tenantID <= 0 || len(userIDs) == 0 || startedAt.IsZero() {
		return list, nil
	}
	err = db.Table("t_conversation_assignment AS ca").
		Select("ca.*").
		Joins("LEFT JOIN t_conversation AS c ON c.id = ca.conversation_id AND c.tenant_id = ca.tenant_id").
		Where("ca.tenant_id = ? AND ca.to_user_id IN ? AND ca.created_at >= ?", tenantID, userIDs, startedAt).
		Where(`(
			(ca.status = ? AND c.status = ? AND c.current_assignee_id = ca.to_user_id)
			OR EXISTS (
				SELECT 1 FROM t_message AS m
				WHERE m.tenant_id = ca.tenant_id
				AND m.conversation_id = ca.conversation_id
				AND m.session_no = ca.session_no
				AND m.sender_type = ?
				AND m.sender_id = ca.to_user_id
				AND m.created_at >= ca.created_at
				AND (ca.finished_at IS NULL OR m.created_at <= ca.finished_at)
				AND m.send_status NOT IN (?, ?)
			)
		)`, enums.IMAssignmentStatusActive, enums.IMConversationStatusActive, enums.IMSenderTypeAgent, enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Order("ca.created_at ASC").
		Order("ca.id ASC").
		Scan(&list).Error
	return list, err
}

func (r *conversationAssignmentRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (list []models.ConversationAssignment) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *conversationAssignmentRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *conversationAssignmentRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.ConversationAssignment{})
}

func (r *conversationAssignmentRepository) Create(db *gorm.DB, t *models.ConversationAssignment) (err error) {
	err = db.Create(t).Error
	return
}

func (r *conversationAssignmentRepository) Update(db *gorm.DB, t *models.ConversationAssignment) (err error) {
	err = db.Save(t).Error
	return
}

func (r *conversationAssignmentRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.ConversationAssignment{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *conversationAssignmentRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.ConversationAssignment{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *conversationAssignmentRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.ConversationAssignment{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *conversationAssignmentRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.ConversationAssignment{}, "id = ?", id)
}
