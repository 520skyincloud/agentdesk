package repositories

import (
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var MessageRepository = newMessageRepository()

func newMessageRepository() *messageRepository {
	return &messageRepository{}
}

type messageRepository struct {
}

type ConversationFirstAgentReplyRow struct {
	ConversationID int64     `gorm:"column:conversation_id"`
	FirstReplyAt   time.Time `gorm:"column:first_reply_at"`
}

type ActiveAssignmentMessageStateRow struct {
	AssignmentID              int64 `gorm:"column:assignment_id"`
	ConversationID            int64 `gorm:"column:conversation_id"`
	AssigneeID                int64 `gorm:"column:assignee_id"`
	LastAssignedReplySeq      int64 `gorm:"column:last_assigned_reply_seq"`
	UnansweredCustomerCount   int   `gorm:"column:unanswered_customer_count"`
	OldestUnansweredMessageID int64 `gorm:"column:oldest_unanswered_message_id"`
}

func (r *messageRepository) Get(db *gorm.DB, id int64) *models.Message {
	ret := &models.Message{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *messageRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.Message {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.Message{}
	if err := db.First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *messageRepository) Take(db *gorm.DB, where ...interface{}) *models.Message {
	ret := &models.Message{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *messageRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Message) {
	cnd.Find(db, &list)
	return
}

func (r *messageRepository) FindLastUnrecalledByConversationID(db *gorm.DB, conversationID int64) *models.Message {
	ret := &models.Message{}
	if err := db.
		Where("conversation_id = ? AND recalled_at IS NULL AND send_status <> ?", conversationID, 6).
		Order("seq_no DESC").
		Order("id DESC").
		Limit(1).
		Take(ret).Error; err != nil {
		return nil
	}
	return ret
}

func (r *messageRepository) FindLastUnrecalledByConversationIDInTenant(db *gorm.DB, conversationID, tenantID int64) *models.Message {
	if conversationID <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.Message{}
	if err := db.Where("tenant_id = ? AND conversation_id = ? AND recalled_at IS NULL AND send_status <> ?", tenantID, conversationID, enums.IMMessageStatusRecalled).
		Order("seq_no DESC, id DESC").
		Take(ret).Error; err != nil {
		return nil
	}
	return ret
}

func (r *messageRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.Message {
	ret := &models.Message{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *messageRepository) FindMissingOutboundOutbox(db *gorm.DB, limit int) ([]models.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	messageTable := db.NamingStrategy.TableName("Message")
	outboxTable := db.NamingStrategy.TableName("ChannelMessageOutbox")
	missingOutbox := fmt.Sprintf(
		`NOT EXISTS (
			SELECT 1 FROM %s AS outbox
			WHERE outbox.tenant_id = message.tenant_id
			  AND outbox.channel_type = message.outbound_channel_type
			  AND outbox.message_id = message.id
		)`,
		outboxTable,
	)
	ret := make([]models.Message, 0)
	err := db.Table(messageTable+" AS message").
		Where("message.outbound_channel_type <> ''").
		Where("message.sender_type IN ?", []enums.IMSenderType{
			enums.IMSenderTypeAgent,
			enums.IMSenderTypeAI,
		}).
		Where(missingOutbox).
		Order("message.id ASC").
		Limit(limit).
		Find(&ret).Error
	return ret, err
}

func (r *messageRepository) FindFirstAgentReplyAtForActiveAssignments(db *gorm.DB, tenantID int64, conversationIDs []int64) ([]ConversationFirstAgentReplyRow, error) {
	ret := make([]ConversationFirstAgentReplyRow, 0)
	if db == nil || tenantID <= 0 || len(conversationIDs) == 0 {
		return ret, nil
	}
	messages := make([]models.Message, 0)
	err := db.Table("t_message AS message").
		Select("DISTINCT message.*").
		Joins("JOIN t_conversation_assignment AS assignment ON assignment.conversation_id = message.conversation_id AND assignment.tenant_id = message.tenant_id AND assignment.status = ?", enums.IMAssignmentStatusActive).
		Joins("JOIN t_conversation AS conversation ON conversation.id = assignment.conversation_id AND conversation.tenant_id = assignment.tenant_id AND conversation.status = ? AND conversation.current_assignee_id = assignment.to_user_id", enums.IMConversationStatusActive).
		Where("message.tenant_id = ? AND message.conversation_id IN ?", tenantID, conversationIDs).
		Where("message.sender_type = ? AND message.sender_id = assignment.to_user_id AND message.created_at >= assignment.created_at", enums.IMSenderTypeAgent).
		Where("assignment.session_no <= ? OR message.session_no = assignment.session_no", 0).
		Where("message.send_status NOT IN ?", []enums.IMMessageStatus{enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled}).
		Where(`NOT EXISTS (
			SELECT 1 FROM t_message AS earlier
			WHERE earlier.tenant_id = message.tenant_id
			AND earlier.conversation_id = message.conversation_id
			AND earlier.sender_type = message.sender_type
			AND earlier.sender_id = assignment.to_user_id
			AND earlier.created_at >= assignment.created_at
			AND (assignment.session_no <= 0 OR earlier.session_no = assignment.session_no)
			AND earlier.send_status NOT IN (?, ?)
			AND (earlier.created_at < message.created_at OR (earlier.created_at = message.created_at AND earlier.id < message.id))
		)`, enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Scan(&messages).Error
	if err != nil {
		return nil, err
	}
	for i := range messages {
		firstReplyAt := messages[i].CreatedAt
		if messages[i].SentAt != nil {
			firstReplyAt = *messages[i].SentAt
		}
		ret = append(ret, ConversationFirstAgentReplyRow{ConversationID: messages[i].ConversationID, FirstReplyAt: firstReplyAt})
	}
	return ret, nil
}

// FindActiveAssignmentMessageStates aggregates reply ownership and customer
// backlog for active assignments without loading full message histories.
func (r *messageRepository) FindActiveAssignmentMessageStates(db *gorm.DB, tenantID int64, userIDs []int64) ([]ActiveAssignmentMessageStateRow, error) {
	return r.findActiveAssignmentMessageStates(db, tenantID, userIDs, nil)
}

// FindActiveAssignmentMessageStatesByAssignmentIDs is the transaction-safe
// variant used when recovery must recheck a fixed set of assignments.
func (r *messageRepository) FindActiveAssignmentMessageStatesByAssignmentIDs(db *gorm.DB, tenantID int64, assignmentIDs []int64) ([]ActiveAssignmentMessageStateRow, error) {
	return r.findActiveAssignmentMessageStates(db, tenantID, nil, assignmentIDs)
}

func (r *messageRepository) findActiveAssignmentMessageStates(db *gorm.DB, tenantID int64, userIDs, assignmentIDs []int64) ([]ActiveAssignmentMessageStateRow, error) {
	ret := make([]ActiveAssignmentMessageStateRow, 0)
	if db == nil || tenantID <= 0 || (len(userIDs) == 0 && len(assignmentIDs) == 0) {
		return ret, nil
	}
	lastReply := db.Table("t_conversation_assignment AS reply_assignment").
		Select("reply_assignment.id AS assignment_id, MAX(reply.seq_no) AS last_reply_seq").
		Joins(`JOIN t_message AS reply
			ON reply.tenant_id = reply_assignment.tenant_id
			AND reply.conversation_id = reply_assignment.conversation_id
			AND (reply_assignment.session_no <= 0 OR reply.session_no = reply_assignment.session_no)
			AND reply.sender_type = ?
			AND reply.sender_id = reply_assignment.to_user_id
			AND reply.created_at >= reply_assignment.created_at
			AND reply.send_status NOT IN (?, ?)`, enums.IMSenderTypeAgent, enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Where("reply_assignment.tenant_id = ? AND reply_assignment.status = ?", tenantID, enums.IMAssignmentStatusActive)
	if len(userIDs) > 0 {
		lastReply = lastReply.Where("reply_assignment.to_user_id IN ?", userIDs)
	}
	if len(assignmentIDs) > 0 {
		lastReply = lastReply.Where("reply_assignment.id IN ?", assignmentIDs)
	}
	lastReply = lastReply.Group("reply_assignment.id")

	query := db.Table("t_conversation_assignment AS assignment").
		Select(`assignment.id AS assignment_id,
			assignment.conversation_id,
			assignment.to_user_id AS assignee_id,
			COALESCE(last_reply.last_reply_seq, 0) AS last_assigned_reply_seq,
			COALESCE(SUM(CASE
				WHEN message.sender_type = ?
				AND message.seq_no > COALESCE(last_reply.last_reply_seq, 0)
				AND message.send_status NOT IN (?, ?)
				THEN 1 ELSE 0 END), 0) AS unanswered_customer_count,
			COALESCE(MIN(CASE
				WHEN message.sender_type = ?
				AND message.seq_no > COALESCE(last_reply.last_reply_seq, 0)
				AND message.send_status NOT IN (?, ?)
				THEN message.id ELSE NULL END), 0) AS oldest_unanswered_message_id`,
			enums.IMSenderTypeCustomer, enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled,
			enums.IMSenderTypeCustomer, enums.IMMessageStatusFailed, enums.IMMessageStatusRecalled).
		Joins("JOIN t_conversation AS conversation ON conversation.id = assignment.conversation_id AND conversation.tenant_id = assignment.tenant_id AND conversation.status = ? AND conversation.current_assignee_id = assignment.to_user_id", enums.IMConversationStatusActive).
		Joins("LEFT JOIN (?) AS last_reply ON last_reply.assignment_id = assignment.id", lastReply).
		Joins(`LEFT JOIN t_message AS message
			ON message.tenant_id = assignment.tenant_id
			AND message.conversation_id = assignment.conversation_id
			AND (assignment.session_no <= 0 OR message.session_no = assignment.session_no)
			AND message.created_at >= assignment.created_at`).
		Where("assignment.tenant_id = ? AND assignment.status = ?", tenantID, enums.IMAssignmentStatusActive)
	if len(userIDs) > 0 {
		query = query.Where("assignment.to_user_id IN ?", userIDs)
	}
	if len(assignmentIDs) > 0 {
		query = query.Where("assignment.id IN ?", assignmentIDs)
	}
	err := query.
		Group("assignment.id, assignment.conversation_id, assignment.to_user_id, last_reply.last_reply_seq").
		Scan(&ret).Error
	return ret, err
}

func (r *messageRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Message, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *messageRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Message, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Message{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *messageRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (list []models.Message) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *messageRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *messageRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.Message{})
}

func (r *messageRepository) Create(db *gorm.DB, t *models.Message) (err error) {
	err = db.Create(t).Error
	return
}

func (r *messageRepository) Update(db *gorm.DB, t *models.Message) (err error) {
	err = db.Save(t).Error
	return
}

func (r *messageRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.Message{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *messageRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.Message{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *messageRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.Message{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *messageRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.Message{}, "id = ?", id)
}

// GetByClientMsgID 根据 conversationID 和 clientMsgID 获取消息
func (r *messageRepository) GetByClientMsgID(db *gorm.DB, conversationID int64, clientMsgID string) *models.Message {
	return r.FindOne(db, sqls.NewCnd().Where("conversation_id = ? AND client_msg_id = ?", conversationID, clientMsgID))
}

func (r *messageRepository) GetByClientMsgIDInTenant(db *gorm.DB, conversationID, tenantID int64, clientMsgID string) *models.Message {
	return r.FindOne(db, sqls.NewCnd().Where("tenant_id = ? AND conversation_id = ? AND client_msg_id = ?", tenantID, conversationID, clientMsgID))
}

// NextSeqNo
func (r *messageRepository) NextSeqNo(db *gorm.DB, conversationID int64) int64 {
	if last := r.FindOne(db, sqls.NewCnd().Where("conversation_id = ?", conversationID).Desc("seq_no")); last != nil {
		return last.SeqNo + 1
	}
	return 1
}

func (r *messageRepository) NextSeqNoInTenant(db *gorm.DB, conversationID, tenantID int64) int64 {
	if last := r.FindOne(db, sqls.NewCnd().Where("tenant_id = ? AND conversation_id = ?", tenantID, conversationID).Desc("seq_no")); last != nil {
		return last.SeqNo + 1
	}
	return 1
}
