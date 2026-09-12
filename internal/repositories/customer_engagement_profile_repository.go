package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var CustomerEngagementProfileRepository = newCustomerEngagementProfileRepository()

type customerEngagementProfileRepository struct{}

func newCustomerEngagementProfileRepository() *customerEngagementProfileRepository {
	return &customerEngagementProfileRepository{}
}

func (r *customerEngagementProfileRepository) FindByCustomerStore(db *gorm.DB, customerID, storeID int64) *models.CustomerEngagementProfile {
	item := &models.CustomerEngagementProfile{}
	if db.Where("customer_id = ? AND store_id = ?", customerID, storeID).First(item).Error != nil {
		return nil
	}
	return item
}

func (r *customerEngagementProfileRepository) Create(db *gorm.DB, item *models.CustomerEngagementProfile) error {
	return db.Create(item).Error
}

func (r *customerEngagementProfileRepository) Update(db *gorm.DB, item *models.CustomerEngagementProfile) error {
	return db.Save(item).Error
}
