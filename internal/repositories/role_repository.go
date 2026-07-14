package repositories

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var RoleRepository = newRoleRepository()

func newRoleRepository() *roleRepository {
	return &roleRepository{}
}

type roleRepository struct {
}

func (r *roleRepository) Get(db *gorm.DB, id int64) *models.Role {
	ret := &models.Role{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *roleRepository) GetForUpdate(db *gorm.DB, id int64) (*models.Role, error) {
	ret := &models.Role{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(ret, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *roleRepository) Take(db *gorm.DB, where ...interface{}) *models.Role {
	ret := &models.Role{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *roleRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Role) {
	cnd.Find(db, &list)
	return
}

func (r *roleRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.Role {
	ret := &models.Role{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *roleRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Role, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *roleRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Role, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Role{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *roleRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (list []models.Role) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *roleRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *roleRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.Role{})
}

func (r *roleRepository) Create(db *gorm.DB, t *models.Role) (err error) {
	err = db.Create(t).Error
	return
}

func (r *roleRepository) Update(db *gorm.DB, t *models.Role) (err error) {
	err = db.Save(t).Error
	return
}

func (r *roleRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.Role{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *roleRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.Role{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *roleRepository) Delete(db *gorm.DB, id int64) error {
	return db.Delete(&models.Role{}, "id = ?", id).Error
}

func (r *roleRepository) GetByCode(db *gorm.DB, code string) *models.Role {
	return r.FindOne(db, sqls.NewCnd().Eq("code", code))
}

func (r *roleRepository) UserHasScope(db *gorm.DB, userID int64, scope string) (bool, error) {
	var count int64
	err := db.Table("t_role AS r").
		Joins("JOIN t_user_role AS ur ON ur.role_id = r.id").
		Where("ur.user_id = ? AND r.scope = ? AND r.status = ?", userID, scope, enums.StatusOk).
		Count(&count).Error
	return count > 0, err
}
