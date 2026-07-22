package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var StoreModelCredentialRepository = &storeModelCredentialRepository{}

type storeModelCredentialRepository struct{}

func (r *storeModelCredentialRepository) GetByStore(db *gorm.DB, tenantID, storeID int64) *models.StoreModelCredential {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return nil
	}
	item := &models.StoreModelCredential{}
	if err := db.Take(item, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error; err != nil {
		return nil
	}
	return item
}
