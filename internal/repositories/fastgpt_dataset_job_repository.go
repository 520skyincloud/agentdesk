package repositories

import (
	"errors"
	"time"

	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var FastGPTDatasetJobRepository = newFastGPTDatasetJobRepository()

type fastGPTDatasetJobRepository struct{}

func newFastGPTDatasetJobRepository() *fastGPTDatasetJobRepository {
	return &fastGPTDatasetJobRepository{}
}

func (r *fastGPTDatasetJobRepository) Get(db *gorm.DB, id int64) *models.FastGPTDatasetJob {
	item := &models.FastGPTDatasetJob{}
	if err := db.First(item, "id = ?", id).Error; err != nil {
		return nil
	}
	return item
}

func (r *fastGPTDatasetJobRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.FastGPTDatasetJob {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	item := &models.FastGPTDatasetJob{}
	if err := db.First(item, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return item
}

func (r *fastGPTDatasetJobRepository) Take(db *gorm.DB, where ...any) *models.FastGPTDatasetJob {
	item := &models.FastGPTDatasetJob{}
	if err := db.Take(item, where...).Error; err != nil {
		return nil
	}
	return item
}

func (r *fastGPTDatasetJobRepository) Find(db *gorm.DB, cnd *sqls.Cnd) []models.FastGPTDatasetJob {
	items := make([]models.FastGPTDatasetJob, 0)
	cnd.Find(db, &items)
	return items
}

func (r *fastGPTDatasetJobRepository) Create(db *gorm.DB, item *models.FastGPTDatasetJob) error {
	return db.Create(item).Error
}

func (r *fastGPTDatasetJobRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.FastGPTDatasetJob{}).Where("id = ?", id).Updates(columns).Error
}

func (r *fastGPTDatasetJobRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	return db.Model(&models.FastGPTDatasetJob{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *fastGPTDatasetJobRepository) ClaimDue(db *gorm.DB, statuses []string, now, leaseExpiresAt time.Time, leaseOwner string, limit int) ([]models.FastGPTDatasetJob, error) {
	if db == nil || len(statuses) == 0 || leaseOwner == "" || limit <= 0 {
		return nil, errors.New("FastGPT job claim scope is required")
	}
	candidates := make([]models.FastGPTDatasetJob, 0, limit)
	if err := db.Where("status IN ?", statuses).
		Where("next_retry_at IS NULL OR next_retry_at <= ?", now).
		Where("lease_expires_at IS NULL OR lease_expires_at <= ? OR lease_owner = ''", now).
		Order("id ASC").Limit(limit * 2).Find(&candidates).Error; err != nil {
		return nil, err
	}
	claimed := make([]models.FastGPTDatasetJob, 0, limit)
	for i := range candidates {
		candidate := &candidates[i]
		result := db.Model(&models.FastGPTDatasetJob{}).
			Where("id = ? AND tenant_id = ? AND status IN ?", candidate.ID, candidate.TenantID, statuses).
			Where("next_retry_at IS NULL OR next_retry_at <= ?", now).
			Where("lease_expires_at IS NULL OR lease_expires_at <= ? OR lease_owner = ''", now).
			Updates(map[string]any{"lease_owner": leaseOwner, "lease_expires_at": leaseExpiresAt, "updated_at": now})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			continue
		}
		candidate.LeaseOwner = leaseOwner
		candidate.LeaseExpiresAt = &leaseExpiresAt
		candidate.UpdatedAt = now
		claimed = append(claimed, *candidate)
		if len(claimed) == limit {
			break
		}
	}
	return claimed, nil
}

func (r *fastGPTDatasetJobRepository) UpdatesClaimed(db *gorm.DB, id, tenantID int64, leaseOwner string, columns map[string]any) error {
	if db == nil || id <= 0 || tenantID <= 0 || leaseOwner == "" {
		return errors.New("claimed FastGPT job scope is required")
	}
	result := db.Model(&models.FastGPTDatasetJob{}).
		Where("id = ? AND tenant_id = ? AND lease_owner = ?", id, tenantID, leaseOwner).
		Updates(columns)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("FastGPT job lease was lost")
	}
	return nil
}
