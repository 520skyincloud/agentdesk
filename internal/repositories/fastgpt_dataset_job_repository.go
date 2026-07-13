package repositories

import (
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
