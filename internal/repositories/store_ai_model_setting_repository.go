package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var StoreAIModelSettingRepository = newStoreAIModelSettingRepository()

func newStoreAIModelSettingRepository() *storeAIModelSettingRepository {
	return &storeAIModelSettingRepository{}
}

type storeAIModelSettingRepository struct{}

func (r *storeAIModelSettingRepository) Get(db *gorm.DB, id int64) *models.StoreAIModelSetting {
	ret := &models.StoreAIModelSetting{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeAIModelSettingRepository) Take(db *gorm.DB, where ...any) *models.StoreAIModelSetting {
	ret := &models.StoreAIModelSetting{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeAIModelSettingRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.StoreAIModelSetting) {
	cnd.Find(db, &list)
	return
}

func (r *storeAIModelSettingRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.StoreAIModelSetting, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *storeAIModelSettingRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.StoreAIModelSetting, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.StoreAIModelSetting{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *storeAIModelSettingRepository) Create(db *gorm.DB, t *models.StoreAIModelSetting) error {
	return db.Create(t).Error
}

func (r *storeAIModelSettingRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.StoreAIModelSetting{}).Where("id = ?", id).Updates(columns).Error
}

func (r *storeAIModelSettingRepository) Delete(db *gorm.DB, id int64) error {
	return db.Delete(&models.StoreAIModelSetting{}, "id = ?", id).Error
}
