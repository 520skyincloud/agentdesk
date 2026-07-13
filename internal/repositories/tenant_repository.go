package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var TenantRepository = newTenantRepository()

func newTenantRepository() *tenantRepository {
	return &tenantRepository{}
}

type tenantRepository struct {
}

func (r *tenantRepository) GetByTenantCode(db *gorm.DB, tenantCode string) *models.Tenant {
	return r.FindOne(db, sqls.NewCnd().Eq("tenant_code", tenantCode))
}

func (r *tenantRepository) Get(db *gorm.DB, id int64) *models.Tenant {
	ret := &models.Tenant{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tenantRepository) Take(db *gorm.DB, where ...any) *models.Tenant {
	ret := &models.Tenant{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tenantRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Tenant) {
	cnd.Find(db, &list)
	return
}

func (r *tenantRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.Tenant {
	ret := &models.Tenant{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *tenantRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Tenant, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *tenantRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Tenant, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Tenant{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *tenantRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...any) (list []models.Tenant) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *tenantRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...any) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *tenantRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.Tenant{})
}

func (r *tenantRepository) Create(db *gorm.DB, t *models.Tenant) (err error) {
	err = db.Create(t).Error
	return
}

func (r *tenantRepository) Update(db *gorm.DB, t *models.Tenant) (err error) {
	err = db.Save(t).Error
	return
}

func (r *tenantRepository) Updates(db *gorm.DB, id int64, columns map[string]any) (err error) {
	err = db.Model(&models.Tenant{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *tenantRepository) UpdateColumn(db *gorm.DB, id int64, name string, value any) (err error) {
	err = db.Model(&models.Tenant{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *tenantRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.Tenant{}, "id = ?", id)
}
