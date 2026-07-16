package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var TenantAIModelGrantRepository = newTenantAIModelGrantRepository()

type tenantAIModelGrantRepository struct{}

func newTenantAIModelGrantRepository() *tenantAIModelGrantRepository {
	return &tenantAIModelGrantRepository{}
}

func (r *tenantAIModelGrantRepository) Take(db *gorm.DB, where ...any) *models.TenantAIModelGrant {
	item := &models.TenantAIModelGrant{}
	if err := db.Take(item, where...).Error; err != nil {
		return nil
	}
	return item
}

func (r *tenantAIModelGrantRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.TenantAIModelGrant) {
	cnd.Find(db, &list)
	return
}

func (r *tenantAIModelGrantRepository) Create(db *gorm.DB, item *models.TenantAIModelGrant) error {
	return db.Create(item).Error
}

func (r *tenantAIModelGrantRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.TenantAIModelGrant{}).Where("id = ?", id).Updates(columns).Error
}
