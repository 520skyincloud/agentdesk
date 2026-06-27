package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var StoreRepository = newStoreRepository()

func newStoreRepository() *storeRepository {
	return &storeRepository{}
}

type storeRepository struct{}

func (r *storeRepository) Get(db *gorm.DB, id int64) *models.Store {
	ret := &models.Store{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeRepository) Take(db *gorm.DB, where ...any) *models.Store {
	ret := &models.Store{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Store) {
	cnd.Find(db, &list)
	return
}

func (r *storeRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Store, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *storeRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Store, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Store{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *storeRepository) Create(db *gorm.DB, t *models.Store) error {
	return db.Create(t).Error
}

func (r *storeRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.Store{}).Where("id = ?", id).Updates(columns).Error
}
