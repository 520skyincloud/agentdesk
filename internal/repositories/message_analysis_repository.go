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
		Where("tenant_id = ? AND message_id = ? AND content_fingerprint <> ? AND analysis_status IN ?", tenantID, messageID, currentFingerprint, []string{"pending", "ready", "failed"}).
		Updates(map[string]any{"analysis_status": "stale", "updated_at": now, "update_user_name": "message_analysis"}).Error
}
