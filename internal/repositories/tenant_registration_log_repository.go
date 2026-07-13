package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var TenantRegistrationLogRepository = newTenantRegistrationLogRepository()

func newTenantRegistrationLogRepository() *tenantRegistrationLogRepository {
	return &tenantRegistrationLogRepository{}
}

type tenantRegistrationLogRepository struct {
}

func (r *tenantRegistrationLogRepository) Get(db *gorm.DB, id int64) *models.TenantRegistrationLog {
	ret := &models.TenantRegistrationLog{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tenantRegistrationLogRepository) Take(db *gorm.DB, where ...any) *models.TenantRegistrationLog {
	ret := &models.TenantRegistrationLog{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tenantRegistrationLogRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.TenantRegistrationLog) {
	cnd.Find(db, &list)
	return
}

func (r *tenantRegistrationLogRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.TenantRegistrationLog {
	ret := &models.TenantRegistrationLog{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *tenantRegistrationLogRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.TenantRegistrationLog, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *tenantRegistrationLogRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.TenantRegistrationLog, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.TenantRegistrationLog{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *tenantRegistrationLogRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...any) (list []models.TenantRegistrationLog) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *tenantRegistrationLogRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...any) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *tenantRegistrationLogRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.TenantRegistrationLog{})
}

func (r *tenantRegistrationLogRepository) Create(db *gorm.DB, t *models.TenantRegistrationLog) (err error) {
	err = db.Create(t).Error
	return
}
