package repositories

import (
	"agent-desk/internal/models"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var RolePermissionRepository = newRolePermissionRepository()

func newRolePermissionRepository() *rolePermissionRepository {
	return &rolePermissionRepository{}
}

type rolePermissionRepository struct {
}

type RolePermissionSnapshotItem struct {
	PermissionID   int64  `gorm:"column:permission_id"`
	PermissionCode string `gorm:"column:permission_code"`
}

type RolePermissionIDItem struct {
	RoleID       int64 `gorm:"column:role_id"`
	PermissionID int64 `gorm:"column:permission_id"`
}

func (r *rolePermissionRepository) Get(db *gorm.DB, id int64) *models.RolePermission {
	ret := &models.RolePermission{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *rolePermissionRepository) Take(db *gorm.DB, where ...interface{}) *models.RolePermission {
	ret := &models.RolePermission{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *rolePermissionRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.RolePermission) {
	cnd.Find(db, &list)
	return
}

func (r *rolePermissionRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.RolePermission {
	ret := &models.RolePermission{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *rolePermissionRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.RolePermission, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *rolePermissionRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.RolePermission, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.RolePermission{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *rolePermissionRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (list []models.RolePermission) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *rolePermissionRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *rolePermissionRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.RolePermission{})
}

func (r *rolePermissionRepository) FindSnapshotByRoleID(db *gorm.DB, roleID int64) ([]RolePermissionSnapshotItem, error) {
	list := make([]RolePermissionSnapshotItem, 0)
	err := db.Table("t_role_permission AS rp").
		Select("rp.permission_id, COALESCE(p.code, '') AS permission_code").
		Joins("LEFT JOIN t_permission AS p ON p.id = rp.permission_id").
		Where("rp.role_id = ?", roleID).
		Order("rp.permission_id ASC").
		Scan(&list).Error
	return list, err
}

func (r *rolePermissionRepository) FindPermissionIDsByRoleIDs(db *gorm.DB, roleIDs []int64) ([]RolePermissionIDItem, error) {
	list := make([]RolePermissionIDItem, 0)
	if len(roleIDs) == 0 {
		return list, nil
	}
	err := db.Model(&models.RolePermission{}).
		Select("role_id, permission_id").
		Where("role_id IN ?", roleIDs).
		Order("role_id ASC, permission_id ASC").
		Scan(&list).Error
	return list, err
}

func (r *rolePermissionRepository) Create(db *gorm.DB, t *models.RolePermission) (err error) {
	err = db.Create(t).Error
	return
}

func (r *rolePermissionRepository) DeleteByRoleID(db *gorm.DB, roleID int64) error {
	return db.Where("role_id = ?", roleID).Delete(&models.RolePermission{}).Error
}

func (r *rolePermissionRepository) Update(db *gorm.DB, t *models.RolePermission) (err error) {
	err = db.Save(t).Error
	return
}

func (r *rolePermissionRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.RolePermission{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *rolePermissionRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.RolePermission{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *rolePermissionRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.RolePermission{}, "id = ?", id)
}
