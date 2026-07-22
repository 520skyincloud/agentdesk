package repositories

import (
	"errors"

	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var StoreModelCredentialRepository = &storeModelCredentialRepository{}
var StoreCredentialPolicyRepository = &storeCredentialPolicyRepository{}
var StoreModelCredentialAuditLogRepository = &storeModelCredentialAuditLogRepository{}

type storeModelCredentialRepository struct{}
type storeCredentialPolicyRepository struct{}
type storeModelCredentialAuditLogRepository struct{}

func (r *storeModelCredentialRepository) Get(db *gorm.DB, id int64) *models.StoreModelCredential {
	if db == nil || id <= 0 {
		return nil
	}
	item := &models.StoreModelCredential{}
	if err := db.First(item, "id = ?", id).Error; err != nil {
		return nil
	}
	return item
}

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

func (r *storeModelCredentialRepository) GetForUpdateByStore(db *gorm.DB, tenantID, storeID int64) (*models.StoreModelCredential, error) {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return nil, errors.New("store model credential scope is required")
	}
	item := &models.StoreModelCredential{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(item, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *storeModelCredentialRepository) FindByTenant(db *gorm.DB, tenantID int64) (list []models.StoreModelCredential) {
	if db == nil || tenantID <= 0 {
		return list
	}
	sqls.NewCnd().Eq("tenant_id", tenantID).Asc("store_id").Find(db, &list)
	return list
}

func (r *storeModelCredentialRepository) Create(db *gorm.DB, item *models.StoreModelCredential) error {
	if db == nil || item == nil {
		return errors.New("store model credential is required")
	}
	return db.Create(item).Error
}

func (r *storeModelCredentialRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	if db == nil || id <= 0 {
		return errors.New("store model credential id is required")
	}
	return db.Model(&models.StoreModelCredential{}).Where("id = ?", id).Updates(columns).Error
}

func (r *storeCredentialPolicyRepository) GetByStore(db *gorm.DB, tenantID, storeID int64) *models.StoreCredentialPolicy {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return nil
	}
	item := &models.StoreCredentialPolicy{}
	if err := db.Take(item, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error; err != nil {
		return nil
	}
	return item
}

func (r *storeCredentialPolicyRepository) GetForUpdateByStore(db *gorm.DB, tenantID, storeID int64) (*models.StoreCredentialPolicy, error) {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return nil, errors.New("store credential policy scope is required")
	}
	item := &models.StoreCredentialPolicy{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(item, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *storeCredentialPolicyRepository) FindByTenant(db *gorm.DB, tenantID int64) (list []models.StoreCredentialPolicy) {
	if db == nil || tenantID <= 0 {
		return list
	}
	sqls.NewCnd().Eq("tenant_id", tenantID).Asc("store_id").Find(db, &list)
	return list
}

func (r *storeCredentialPolicyRepository) Create(db *gorm.DB, item *models.StoreCredentialPolicy) error {
	if db == nil || item == nil {
		return errors.New("store credential policy is required")
	}
	return db.Create(item).Error
}

func (r *storeCredentialPolicyRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	if db == nil || id <= 0 {
		return errors.New("store credential policy id is required")
	}
	return db.Model(&models.StoreCredentialPolicy{}).Where("id = ?", id).Updates(columns).Error
}

func (r *storeModelCredentialAuditLogRepository) Create(db *gorm.DB, item *models.StoreModelCredentialAuditLog) error {
	if db == nil || item == nil {
		return errors.New("store credential audit log is required")
	}
	return db.Create(item).Error
}

func (r *storeModelCredentialAuditLogRepository) FindLatestByStore(db *gorm.DB, tenantID, storeID int64, limit int) (list []models.StoreModelCredentialAuditLog) {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return list
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	db.Where("tenant_id = ? AND store_id = ?", tenantID, storeID).Order("id DESC").Limit(limit).Find(&list)
	return list
}
