package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var StoreStaffBindingRepository = newStoreStaffBindingRepository()

func newStoreStaffBindingRepository() *storeStaffBindingRepository {
	return &storeStaffBindingRepository{}
}

type storeStaffBindingRepository struct{}

func (r *storeStaffBindingRepository) Get(db *gorm.DB, id int64) *models.StoreStaffBinding {
	ret := &models.StoreStaffBinding{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeStaffBindingRepository) Take(db *gorm.DB, where ...any) *models.StoreStaffBinding {
	ret := &models.StoreStaffBinding{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeStaffBindingRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.StoreStaffBinding) {
	cnd.Find(db, &list)
	return
}

func (r *storeStaffBindingRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.StoreStaffBinding, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *storeStaffBindingRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.StoreStaffBinding, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.StoreStaffBinding{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *storeStaffBindingRepository) Create(db *gorm.DB, t *models.StoreStaffBinding) error {
	return db.Create(t).Error
}

func (r *storeStaffBindingRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.StoreStaffBinding{}).Where("id = ?", id).Updates(columns).Error
}
