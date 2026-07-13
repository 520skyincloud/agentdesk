package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var TenantInvitationRepository = newTenantInvitationRepository()

func newTenantInvitationRepository() *tenantInvitationRepository {
	return &tenantInvitationRepository{}
}

type tenantInvitationRepository struct {
}

func (r *tenantInvitationRepository) Get(db *gorm.DB, id int64) *models.TenantInvitation {
	ret := &models.TenantInvitation{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tenantInvitationRepository) Take(db *gorm.DB, where ...any) *models.TenantInvitation {
	ret := &models.TenantInvitation{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tenantInvitationRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.TenantInvitation) {
	cnd.Find(db, &list)
	return
}

func (r *tenantInvitationRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.TenantInvitation {
	ret := &models.TenantInvitation{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *tenantInvitationRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.TenantInvitation, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *tenantInvitationRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.TenantInvitation, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.TenantInvitation{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *tenantInvitationRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...any) (list []models.TenantInvitation) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *tenantInvitationRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...any) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *tenantInvitationRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.TenantInvitation{})
}

func (r *tenantInvitationRepository) Create(db *gorm.DB, t *models.TenantInvitation) (err error) {
	err = db.Create(t).Error
	return
}

func (r *tenantInvitationRepository) Update(db *gorm.DB, t *models.TenantInvitation) (err error) {
	err = db.Save(t).Error
	return
}

func (r *tenantInvitationRepository) Updates(db *gorm.DB, id int64, columns map[string]any) (err error) {
	err = db.Model(&models.TenantInvitation{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *tenantInvitationRepository) UpdateColumn(db *gorm.DB, id int64, name string, value any) (err error) {
	err = db.Model(&models.TenantInvitation{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}
