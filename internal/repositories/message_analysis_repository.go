package repositories

import (
	"errors"
	"time"

	"agent-desk/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var MessageAnalysisRepository = &messageAnalysisRepository{}

type messageAnalysisRepository struct{}

func (r *messageAnalysisRepository) CreateIfAbsent(db *gorm.DB, item *models.MessageAnalysis) (bool, error) {
	if db == nil || item == nil {
		return false, nil
	}
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "message_id"}, {Name: "source_revision"}},
		DoNothing: true,
	}).Create(item)
	return result.RowsAffected > 0, result.Error
}

func (r *messageAnalysisRepository) GetByRevisionInTenant(db *gorm.DB, tenantID, messageID int64, sourceRevision int) *models.MessageAnalysis {
	if db == nil || tenantID <= 0 || messageID <= 0 || sourceRevision <= 0 {
		return nil
	}
	ret := &models.MessageAnalysis{}
	if err := db.Take(ret, "tenant_id = ? AND message_id = ? AND source_revision = ?", tenantID, messageID, sourceRevision).Error; err != nil {
		return nil
	}
	return ret
}

func (r *messageAnalysisRepository) GetLatestInTenant(db *gorm.DB, tenantID, messageID int64) *models.MessageAnalysis {
	if db == nil || tenantID <= 0 || messageID <= 0 {
		return nil
	}
	ret := &models.MessageAnalysis{}
	if err := db.Where("tenant_id = ? AND message_id = ?", tenantID, messageID).Order("source_revision DESC, id DESC").Take(ret).Error; err != nil {
		return nil
	}
	return ret
}

func (r *messageAnalysisRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.MessageAnalysis, error) {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	ret := &models.MessageAnalysis{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return ret, err
}

func (r *messageAnalysisRepository) CASStatusInTenant(db *gorm.DB, id, tenantID int64, from []string, columns map[string]any) (bool, error) {
	if db == nil || id <= 0 || tenantID <= 0 || len(from) == 0 || len(columns) == 0 {
		return false, nil
	}
	result := db.Model(&models.MessageAnalysis{}).Where("id = ? AND tenant_id = ? AND analysis_status IN ?", id, tenantID, from).Updates(columns)
	return result.RowsAffected == 1, result.Error
}

func (r *messageAnalysisRepository) MarkStaleByMessageInTenant(db *gorm.DB, tenantID, messageID int64, currentFingerprint string, now time.Time) error {
	if db == nil || tenantID <= 0 || messageID <= 0 {
		return nil
	}
	return db.Model(&models.MessageAnalysis{}).
		Where("tenant_id = ? AND message_id = ? AND content_fingerprint <> ? AND analysis_status IN ?", tenantID, messageID, currentFingerprint,
			[]string{"pending", "processing", "ready", "failed_retryable", "failed_terminal", "failed"}).
		Updates(map[string]any{
			"analysis_status":  "stale",
			"claimed_by":       "",
			"lease_expires_at": nil,
			"next_retry_at":    nil,
			"updated_at":       now,
			"update_user_name": "message_analysis",
		}).Error
}

// FindClaimable 取可领取的分析工作（pending 或到期可重试），按 due 复合索引扫描。
func (r *messageAnalysisRepository) FindClaimable(db *gorm.DB, now time.Time, limit int) []models.MessageAnalysis {
	if db == nil || limit <= 0 {
		return nil
	}
	var list []models.MessageAnalysis
	err := db.Where(
		"((analysis_status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)) OR (analysis_status = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)))",
		[]string{"pending", "failed_retryable"}, now, "processing", now).
		Order("id ASC").Limit(limit).Find(&list).Error
	if err != nil {
		return nil
	}
	return list
}

func (r *messageAnalysisRepository) FindClaimableMedia(db *gorm.DB, now time.Time, limit int) []models.MessageAnalysis {
	if db == nil || limit <= 0 {
		return nil
	}
	var list []models.MessageAnalysis
	err := db.Where("analyzer_kind IN ?", []string{"vision", "asr", "file_parser"}).
		Where(
			"((analysis_status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)) OR (analysis_status = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)))",
			[]string{"pending", "failed_retryable"}, now, "processing", now).
		Order("id ASC").Limit(limit).Find(&list).Error
	if err != nil {
		return nil
	}
	return list
}

// TryClaim 领取一条分析工作：置 processing + owner + lease。
func (r *messageAnalysisRepository) TryClaim(db *gorm.DB, id, tenantID int64, owner string, now, leaseUntil time.Time) (bool, error) {
	if db == nil || id <= 0 || tenantID <= 0 || owner == "" {
		return false, nil
	}
	result := db.Model(&models.MessageAnalysis{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Where(
			"((analysis_status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)) OR (analysis_status = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)))",
			[]string{"pending", "failed_retryable"}, now, "processing", now).
		Updates(map[string]any{
			"analysis_status":  "processing",
			"claimed_by":       owner,
			"lease_expires_at": leaseUntil,
			"attempt_count":    gorm.Expr("attempt_count + 1"),
			"next_retry_at":    nil,
			"updated_at":       now,
			"update_user_name": "message_analysis_claim",
		})
	return result.RowsAffected == 1, result.Error
}

// RenewLease 续约当前 owner 持有的分析工作。
func (r *messageAnalysisRepository) RenewLease(db *gorm.DB, id, tenantID int64, owner string, leaseUntil time.Time) (bool, error) {
	if db == nil || id <= 0 || tenantID <= 0 || owner == "" {
		return false, nil
	}
	result := db.Model(&models.MessageAnalysis{}).
		Where("id = ? AND tenant_id = ? AND claimed_by = ? AND analysis_status = ?", id, tenantID, owner, "processing").
		Updates(map[string]any{"lease_expires_at": leaseUntil, "updated_at": time.Now()})
	return result.RowsAffected == 1, result.Error
}

// CASCompleteReady 原子完成：processing -> ready + AnalysisJSON + analyzedAt。
func (r *messageAnalysisRepository) CASCompleteReady(db *gorm.DB, id, tenantID int64, owner string, analysisJSON string, now time.Time) (bool, error) {
	if db == nil || id <= 0 || tenantID <= 0 || owner == "" {
		return false, nil
	}
	result := db.Model(&models.MessageAnalysis{}).
		Where("id = ? AND tenant_id = ? AND claimed_by = ? AND analysis_status = ?", id, tenantID, owner, "processing").
		Updates(map[string]any{
			"analysis_status":  "ready",
			"analysis_json":    analysisJSON,
			"error_code":       "",
			"last_error_class": "",
			"claimed_by":       "",
			"lease_expires_at": nil,
			"next_retry_at":    nil,
			"analyzed_at":      now,
			"updated_at":       now,
			"update_user_name": "message_analysis_ready",
		})
	return result.RowsAffected == 1, result.Error
}

// CASMarkFailed 原子失败：processing -> failed_retryable/failed_terminal + nextRetry。
func (r *messageAnalysisRepository) CASMarkFailed(db *gorm.DB, id, tenantID int64, owner string, status string, errorClass string, errorCode string, nextRetryAt *time.Time, now time.Time) (bool, error) {
	if db == nil || id <= 0 || tenantID <= 0 || owner == "" {
		return false, nil
	}
	if status != "failed_retryable" && status != "failed_terminal" {
		return false, nil
	}
	updates := map[string]any{
		"analysis_status":  status,
		"error_code":       errorCode,
		"last_error_class": errorClass,
		"claimed_by":       "",
		"lease_expires_at": nil,
		"analyzed_at":      now,
		"updated_at":       now,
		"update_user_name": "message_analysis_failed",
	}
	if nextRetryAt != nil {
		updates["next_retry_at"] = *nextRetryAt
	} else {
		updates["next_retry_at"] = nil
	}
	result := db.Model(&models.MessageAnalysis{}).
		Where("id = ? AND tenant_id = ? AND claimed_by = ? AND analysis_status = ?", id, tenantID, owner, "processing").
		Updates(updates)
	return result.RowsAffected == 1, result.Error
}
