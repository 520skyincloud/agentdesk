package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var StoreModelCredentialRepository = newStoreModelCredentialRepository()

type storeModelCredentialRepository struct{}

func newStoreModelCredentialRepository() *storeModelCredentialRepository {
	return &storeModelCredentialRepository{}
}

func (r *storeModelCredentialRepository) Get(db *gorm.DB, id int64) *models.StoreModelCredential {
	item := &models.StoreModelCredential{}
	if err := db.First(item, "id = ?", id).Error; err != nil {
		return nil
	}
	return item
}

func (r *storeModelCredentialRepository) GetByStoreID(db *gorm.DB, storeID int64) *models.StoreModelCredential {
	item := &models.StoreModelCredential{}
	if err := db.First(item, "store_id = ?", storeID).Error; err != nil {
		return nil
	}
	return item
}

func (r *storeModelCredentialRepository) Find(db *gorm.DB, cnd *sqls.Cnd) []models.StoreModelCredential {
	var list []models.StoreModelCredential
	cnd.Find(db, &list)
	return list
}

func (r *storeModelCredentialRepository) Create(db *gorm.DB, item *models.StoreModelCredential) error {
	return db.Create(item).Error
}

func (r *storeModelCredentialRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.StoreModelCredential{}).Where("id = ?", id).Updates(columns).Error
}
