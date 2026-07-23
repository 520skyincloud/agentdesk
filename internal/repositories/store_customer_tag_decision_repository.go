package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var StoreCustomerTagDecisionRepository = &storeCustomerTagDecisionRepository{}

type storeCustomerTagDecisionRepository struct{}

func (r *storeCustomerTagDecisionRepository) Create(db *gorm.DB, item *models.StoreCustomerTagDecision) error {
	return db.Create(item).Error
}
