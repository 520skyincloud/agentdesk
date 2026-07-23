package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var StoreCustomerRelationRepository = newStoreCustomerRelationRepository()

type storeCustomerRelationRepository struct{}

func newStoreCustomerRelationRepository() *storeCustomerRelationRepository {
	return &storeCustomerRelationRepository{}
}

func (r *storeCustomerRelationRepository) Get(db *gorm.DB, id int64) *models.StoreCustomerRelation {
	ret := &models.StoreCustomerRelation{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeCustomerRelationRepository) Take(db *gorm.DB, where ...any) *models.StoreCustomerRelation {
	ret := &models.StoreCustomerRelation{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeCustomerRelationRepository) TakeByCustomerAndStoreInTenant(db *gorm.DB, tenantID, customerID, storeID int64) *models.StoreCustomerRelation {
	if tenantID <= 0 || customerID <= 0 || storeID <= 0 {
		return nil
	}
	ret := &models.StoreCustomerRelation{}
	if err := db.Take(ret, "tenant_id = ? AND customer_id = ? AND store_id = ?", tenantID, customerID, storeID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeCustomerRelationRepository) GetInTenant(db *gorm.DB, id, tenantID int64) (*models.StoreCustomerRelation, error) {
	if id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	ret := &models.StoreCustomerRelation{}
	err := db.Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *storeCustomerRelationRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.StoreCustomerRelation, error) {
	if id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	ret := &models.StoreCustomerRelation{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Take(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *storeCustomerRelationRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.StoreCustomerRelation) {
	cnd.Find(db, &list)
	return
}

func (r *storeCustomerRelationRepository) Create(db *gorm.DB, t *models.StoreCustomerRelation) error {
	return db.Create(t).Error
}

func (r *storeCustomerRelationRepository) CreateIfAbsent(db *gorm.DB, t *models.StoreCustomerRelation) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "customer_id"}, {Name: "store_id"}},
		DoNothing: true,
	}).Create(t).Error
}

func (r *storeCustomerRelationRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.StoreCustomerRelation{}).Where("id = ?", id).Updates(columns).Error
}

func (r *storeCustomerRelationRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.StoreCustomerRelation{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}
