package repositories

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var AIReplyJobRepository = newAIReplyJobRepository()

type aiReplyJobRepository struct{}

func newAIReplyJobRepository() *aiReplyJobRepository {
	return &aiReplyJobRepository{}
}

func (r *aiReplyJobRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.AIReplyJob {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.AIReplyJob{}
	if err := db.Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *aiReplyJobRepository) GetByMessageInTenant(db *gorm.DB, tenantID, conversationID, messageID int64) *models.AIReplyJob {
	if db == nil || tenantID <= 0 || conversationID <= 0 || messageID <= 0 {
		return nil
	}
	ret := &models.AIReplyJob{}
	if err := db.Take(ret,
		"tenant_id = ? AND conversation_id = ? AND message_id = ?",
		tenantID, conversationID, messageID,
	).Error; err != nil {
		return nil
	}
	return ret
}

func (r *aiReplyJobRepository) CreateIfAbsent(db *gorm.DB, item *models.AIReplyJob) (bool, error) {
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "conversation_id"}, {Name: "message_id"}},
		DoNothing: true,
	}).Create(item)
	return result.RowsAffected == 1, result.Error
}

func (r *aiReplyJobRepository) FindClaimable(db *gorm.DB, now time.Time, limit int) ([]models.AIReplyJob, error) {
	if limit <= 0 {
		limit = 4
	}
	ret := make([]models.AIReplyJob, 0, limit)
	err := db.Where(
		"((status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)) OR (status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?))",
		[]enums.AIReplyJobStatus{enums.AIReplyJobStatusPending, enums.AIReplyJobStatusRetry}, now,
		enums.AIReplyJobStatusProcessing, now,
	).Order("COALESCE(next_retry_at, created_at) ASC, id ASC").Limit(limit).Find(&ret).Error
	return ret, err
}

func (r *aiReplyJobRepository) TryClaim(db *gorm.DB, id, tenantID int64, owner string, now, leaseExpiresAt time.Time) (bool, error) {
	result := db.Model(&models.AIReplyJob{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Where(
			"((status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)) OR (status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?))",
			[]enums.AIReplyJobStatus{enums.AIReplyJobStatusPending, enums.AIReplyJobStatusRetry}, now,
			enums.AIReplyJobStatusProcessing, now,
		).
		Updates(map[string]any{
			"status":           enums.AIReplyJobStatusProcessing,
			"attempt_count":    gorm.Expr("attempt_count + 1"),
			"next_retry_at":    nil,
			"lease_owner":      owner,
			"lease_expires_at": leaseExpiresAt,
			"started_at":       gorm.Expr("COALESCE(started_at, ?)", now),
			"updated_at":       now,
			"update_user_name": "ai_reply_worker",
		})
	return result.RowsAffected == 1, result.Error
}

func (r *aiReplyJobRepository) RenewLease(db *gorm.DB, id, tenantID int64, owner string, now, leaseExpiresAt time.Time) (bool, error) {
	result := db.Model(&models.AIReplyJob{}).
		Where("id = ? AND tenant_id = ? AND status = ? AND lease_owner = ? AND lease_expires_at > ?",
			id, tenantID, enums.AIReplyJobStatusProcessing, owner, now).
		Updates(map[string]any{
			"lease_expires_at": leaseExpiresAt,
			"updated_at":       now,
			"update_user_name": "ai_reply_worker",
		})
	return result.RowsAffected == 1, result.Error
}

func (r *aiReplyJobRepository) MarkTerminal(
	db *gorm.DB,
	id, tenantID int64,
	owner string,
	status enums.AIReplyJobStatus,
	resultCode, errorClass string,
	coveredByMessageID, coveredByTaskID int64,
	now time.Time,
) (bool, error) {
	result := db.Model(&models.AIReplyJob{}).
		Where("id = ? AND tenant_id = ? AND status = ? AND lease_owner = ?",
			id, tenantID, enums.AIReplyJobStatusProcessing, owner).
		Updates(map[string]any{
			"status":                status,
			"result_code":           resultCode,
			"last_error_class":      errorClass,
			"covered_by_message_id": coveredByMessageID,
			"covered_by_task_id":    coveredByTaskID,
			"next_retry_at":         nil,
			"lease_owner":           "",
			"lease_expires_at":      nil,
			"completed_at":          now,
			"updated_at":            now,
			"update_user_name":      "ai_reply_worker",
		})
	return result.RowsAffected == 1, result.Error
}

func (r *aiReplyJobRepository) MarkRetry(
	db *gorm.DB,
	id, tenantID int64,
	owner, resultCode, errorClass string,
	nextRetryAt, now time.Time,
	consumeAttempt bool,
) (bool, error) {
	updates := map[string]any{
		"status":           enums.AIReplyJobStatusRetry,
		"result_code":      resultCode,
		"last_error_class": errorClass,
		"next_retry_at":    nextRetryAt,
		"lease_owner":      "",
		"lease_expires_at": nil,
		"updated_at":       now,
		"update_user_name": "ai_reply_worker",
	}
	if !consumeAttempt {
		updates["attempt_count"] = gorm.Expr("CASE WHEN attempt_count > 0 THEN attempt_count - 1 ELSE 0 END")
	}
	result := db.Model(&models.AIReplyJob{}).
		Where("id = ? AND tenant_id = ? AND status = ? AND lease_owner = ?",
			id, tenantID, enums.AIReplyJobStatusProcessing, owner).
		Updates(updates)
	return result.RowsAffected == 1, result.Error
}

func (r *aiReplyJobRepository) SupersedeOlderTurnVersions(db *gorm.DB, tenantID, turnID int64, version int, excludeJobID int64, now time.Time) error {
	if db == nil || tenantID <= 0 || turnID <= 0 || version <= 0 {
		return nil
	}
	query := db.Model(&models.AIReplyJob{}).
		Where("tenant_id = ? AND turn_id = ? AND turn_version < ?", tenantID, turnID, version).
		Where("status IN ?", []enums.AIReplyJobStatus{
			enums.AIReplyJobStatusPending,
			enums.AIReplyJobStatusProcessing,
			enums.AIReplyJobStatusRetry,
		})
	if excludeJobID > 0 {
		query = query.Where("id <> ?", excludeJobID)
	}
	return query.Updates(map[string]any{
		"status":           enums.AIReplyJobStatusSuperseded,
		"result_code":      "stale_turn_version",
		"last_error_class": "",
		"next_retry_at":    nil,
		"lease_owner":      "",
		"lease_expires_at": nil,
		"completed_at":     now,
		"updated_at":       now,
		"update_user_name": "ai_reply_turn",
	}).Error
}

// SkipPendingByMessageInTenant 把某条已被确定性消费的客户消息对应 Job 标记为 skipped。
// processing 也必须覆盖：确认消息可能在事务提交后被 worker 抢先领取，若只跳过
// pending/retry，仍会出现同一句既被确认服务消费、又进入普通 AI 链路的双重处理。
func (r *aiReplyJobRepository) SkipPendingByMessageInTenant(db *gorm.DB, tenantID, conversationID, messageID int64, resultCode string, now time.Time) error {
	if db == nil || tenantID <= 0 || conversationID <= 0 || messageID <= 0 {
		return nil
	}
	return db.Model(&models.AIReplyJob{}).
		Where("tenant_id = ? AND conversation_id = ? AND message_id = ?", tenantID, conversationID, messageID).
		Where("status IN ?", []enums.AIReplyJobStatus{
			enums.AIReplyJobStatusPending,
			enums.AIReplyJobStatusRetry,
			enums.AIReplyJobStatusProcessing,
		}).
		Updates(map[string]any{
			"status":           enums.AIReplyJobStatusSkipped,
			"result_code":      strings.TrimSpace(resultCode),
			"last_error_class": "",
			"next_retry_at":    nil,
			"lease_owner":      "",
			"lease_expires_at": nil,
			"completed_at":     now,
			"updated_at":       now,
			"update_user_name": "ai_reply_handoff",
		}).Error
}

func (r *aiReplyJobRepository) FindMessagesMissingJobs(db *gorm.DB, cutoff time.Time, limit int) ([]models.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	messageTable := db.NamingStrategy.TableName("Message")
	jobTable := db.NamingStrategy.TableName("AIReplyJob")
	ret := make([]models.Message, 0, limit)
	err := db.Table(messageTable+" AS message").
		Where("message.created_at >= ?", cutoff).
		Where("message.historical_only = ?", false).
		Where("message.sender_type = ?", enums.IMSenderTypeCustomer).
		Where("message.message_type IN ?", []enums.IMMessageType{
			enums.IMMessageTypeText,
			enums.IMMessageTypeHTML,
			enums.IMMessageTypeImage,
			enums.IMMessageTypeVoice,
			enums.IMMessageTypeAttachment,
		}).
		Where("message.send_status NOT IN ?", []enums.IMMessageStatus{
			enums.IMMessageStatusFailed,
			enums.IMMessageStatusRecalled,
		}).
		Where("message.recalled_at IS NULL").
		Where("NOT EXISTS (SELECT 1 FROM " + jobTable + " AS job WHERE job.tenant_id = message.tenant_id AND job.conversation_id = message.conversation_id AND job.message_id = message.id)").
		Order("message.id ASC").Limit(limit).Find(&ret).Error
	return ret, err
}

// CASAdvanceStage 契约 22.16：阶段推进时归零 StageAttemptCount 并写入
// checkpoint fingerprint。只在当前 ResumeStage 与 from 匹配时生效。
func (r *aiReplyJobRepository) CASAdvanceStage(db *gorm.DB, id int64, from, to, checkpoint string, now time.Time) (bool, error) {
	result := db.Model(&models.AIReplyJob{}).
		Where("id = ? AND resume_stage = ?", id, from).
		Updates(map[string]any{
			"resume_stage":           to,
			"stage_attempt_count":    0,
			"checkpoint_fingerprint": checkpoint,
			"updated_at":             now,
			"update_user_name":       "ai_reply_stage",
		})
	return result.RowsAffected == 1, result.Error
}

// IncrementStageAttempt 同一阶段编排级重入时 +1。
func (r *aiReplyJobRepository) IncrementStageAttempt(db *gorm.DB, id int64, now time.Time) (bool, error) {
	result := db.Model(&models.AIReplyJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"stage_attempt_count": gorm.Expr("stage_attempt_count + 1"),
			"updated_at":          now,
			"update_user_name":    "ai_reply_stage",
		})
	return result.RowsAffected == 1, result.Error
}

// UpdateColumnsInTenant 更新 Job 指定列（如技术提示 MessageID）。
func (r *aiReplyJobRepository) UpdateColumnsInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.AIReplyJob{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}
