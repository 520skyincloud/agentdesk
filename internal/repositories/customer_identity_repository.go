package repositories

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var CustomerIdentityRepository = newCustomerIdentityRepository()

func newCustomerIdentityRepository() *customerIdentityRepository {
	return &customerIdentityRepository{}
}

type customerIdentityRepository struct {
}

func (r *customerIdentityRepository) Get(db *gorm.DB, id int64) *models.CustomerIdentity {
	ret := &models.CustomerIdentity{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *customerIdentityRepository) Take(db *gorm.DB, where ...interface{}) *models.CustomerIdentity {
	ret := &models.CustomerIdentity{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

// GetBy 按外部来源 + 外部用户标识查询身份映射（与 uk_customer_external 一致）。
func (r *customerIdentityRepository) GetBy(db *gorm.DB, externalSource enums.ExternalSource, externalID string) *models.CustomerIdentity {
	if strs.IsAnyBlank(string(externalSource), externalID) {
		return nil
	}
	return r.FindOne(db, sqls.NewCnd().
		Eq("external_source", externalSource).
		Eq("external_id", externalID))
}

func (r *customerIdentityRepository) GetByInTenant(db *gorm.DB, tenantID int64, externalSource enums.ExternalSource, externalID string) *models.CustomerIdentity {
	if tenantID <= 0 {
		return nil
	}
	return r.Take(db, "tenant_id = ? AND external_source = ? AND external_id = ?", tenantID, externalSource, externalID)
}

func (r *customerIdentityRepository) GetByForUpdateInTenant(db *gorm.DB, tenantID int64, externalSource enums.ExternalSource, externalID string) (*models.CustomerIdentity, error) {
	if db == nil || tenantID <= 0 || strs.IsAnyBlank(string(externalSource), externalID) {
		return nil, nil
	}
	ret := &models.CustomerIdentity{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Take(ret, "tenant_id = ? AND external_source = ? AND external_id = ?", tenantID, externalSource, externalID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *customerIdentityRepository) FindByCustomerID(db *gorm.DB, customerID int64) []models.CustomerIdentity {
	if customerID <= 0 {
		return nil
	}
	return r.Find(db, sqls.NewCnd().Eq("customer_id", customerID).Eq("status", enums.StatusOk).Desc("id"))
}

func (r *customerIdentityRepository) FindByCustomerIDInTenant(db *gorm.DB, customerID, tenantID int64) []models.CustomerIdentity {
	if customerID <= 0 || tenantID <= 0 {
		return nil
	}
	return r.Find(db, sqls.NewCnd().Eq("customer_id", customerID).Eq("tenant_id", tenantID).Asc("id"))
}

func (r *customerIdentityRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.CustomerIdentity) {
	cnd.Find(db, &list)
	return
}

func (r *customerIdentityRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.CustomerIdentity {
	ret := &models.CustomerIdentity{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *customerIdentityRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.CustomerIdentity, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *customerIdentityRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.CustomerIdentity, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.CustomerIdentity{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *customerIdentityRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (list []models.CustomerIdentity) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *customerIdentityRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *customerIdentityRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.CustomerIdentity{})
}

func (r *customerIdentityRepository) Create(db *gorm.DB, t *models.CustomerIdentity) (err error) {
	err = db.Create(t).Error
	return
}

func (r *customerIdentityRepository) Update(db *gorm.DB, t *models.CustomerIdentity) (err error) {
	err = db.Save(t).Error
	return
}

func (r *customerIdentityRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.CustomerIdentity{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *customerIdentityRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.CustomerIdentity{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *customerIdentityRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.CustomerIdentity{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *customerIdentityRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.CustomerIdentity{}, "id = ?", id)
}
