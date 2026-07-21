package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var PermissionRepository = newPermissionRepository()

func newPermissionRepository() *permissionRepository {
	return &permissionRepository{}
}

type permissionRepository struct {
}

func (r *permissionRepository) Get(db *gorm.DB, id int64) *models.Permission {
	ret := &models.Permission{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *permissionRepository) Take(db *gorm.DB, where ...interface{}) *models.Permission {
	ret := &models.Permission{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *permissionRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Permission) {
	cnd.Find(db, &list)
	return
}

func (r *permissionRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.Permission {
	ret := &models.Permission{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *permissionRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Permission, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *permissionRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Permission, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Permission{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *permissionRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (list []models.Permission) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *permissionRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *permissionRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.Permission{})
}

func (r *permissionRepository) FindUserIDsWithAllCodes(db *gorm.DB, userIDs []int64, codes []string) ([]int64, error) {
	ret := make([]int64, 0)
	if db == nil || len(userIDs) == 0 || len(codes) == 0 {
		return ret, nil
	}
	err := db.Table("t_user_role AS ur").
		Select("ur.user_id").
		Joins("JOIN t_role AS r ON r.id = ur.role_id").
		Joins("JOIN t_role_permission AS rp ON rp.role_id = r.id").
		Joins("JOIN t_permission AS p ON p.id = rp.permission_id").
		Where("ur.user_id IN ?", userIDs).
		Where("r.status = ? AND p.status = ?", enums.StatusOk, enums.StatusOk).
		Where("p.code IN ?", codes).
		Group("ur.user_id").
		Having("COUNT(DISTINCT p.code) = ?", len(codes)).
		Order("ur.user_id ASC").
		Scan(&ret).Error
	return ret, err
}

func (r *permissionRepository) Create(db *gorm.DB, t *models.Permission) (err error) {
	err = db.Create(t).Error
	return
}

func (r *permissionRepository) Update(db *gorm.DB, t *models.Permission) (err error) {
	err = db.Save(t).Error
	return
}

func (r *permissionRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.Permission{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *permissionRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.Permission{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *permissionRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.Permission{}, "id = ?", id)
}
