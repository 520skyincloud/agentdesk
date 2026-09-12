package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var ProactiveReachoutRepository = newProactiveReachoutRepository()

type proactiveReachoutRepository struct{}

func newProactiveReachoutRepository() *proactiveReachoutRepository {
	return &proactiveReachoutRepository{}
}

func (r *proactiveReachoutRepository) FindByIdempotencyKey(db *gorm.DB, key string) *models.ProactiveReachout {
	item := &models.ProactiveReachout{}
	if db.Where("idempotency_key = ?", key).First(item).Error != nil {
		return nil
	}
	return item
}

func (r *proactiveReachoutRepository) Create(db *gorm.DB, item *models.ProactiveReachout) error {
	return db.Create(item).Error
}
