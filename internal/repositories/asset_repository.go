package repositories

import (
	"agent-desk/internal/models"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var AssetRepository = newAssetRepository()

func newAssetRepository() *assetRepository {
	return &assetRepository{}
}

type assetRepository struct {
}

func (r *assetRepository) Get(db *gorm.DB, id int64) *models.Asset {
	ret := &models.Asset{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *assetRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.Asset {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.Asset{}
	if err := db.First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *assetRepository) Take(db *gorm.DB, where ...interface{}) *models.Asset {
	ret := &models.Asset{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *assetRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Asset) {
	cnd.Find(db, &list)
	return
}

func (r *assetRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.Asset {
	ret := &models.Asset{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *assetRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Asset, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *assetRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Asset, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Asset{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *assetRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (list []models.Asset) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *assetRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *assetRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.Asset{})
}

func (r *assetRepository) Create(db *gorm.DB, t *models.Asset) (err error) {
	err = db.Create(t).Error
	return
}

func (r *assetRepository) Update(db *gorm.DB, t *models.Asset) (err error) {
	err = db.Save(t).Error
	return
}

func (r *assetRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.Asset{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *assetRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	return db.Model(&models.Asset{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *assetRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.Asset{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *assetRepository) UpdateColumnInTenant(db *gorm.DB, id, tenantID int64, name string, value any) error {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	return db.Model(&models.Asset{}).Where("id = ? AND tenant_id = ?", id, tenantID).UpdateColumn(name, value).Error
}

func (r *assetRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.Asset{}, "id = ?", id)
}

func (r *assetRepository) GetByAssetID(db *gorm.DB, assetID string) *models.Asset {
	ret := &models.Asset{}
	if err := db.First(ret, "asset_id = ?", assetID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *assetRepository) GetByAssetIDInTenant(db *gorm.DB, assetID string, tenantID int64) *models.Asset {
	if tenantID <= 0 {
		return nil
	}
	ret := &models.Asset{}
	if err := db.First(ret, "asset_id = ? AND tenant_id = ?", assetID, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *assetRepository) GetByStorageKey(db *gorm.DB, storageKey string) *models.Asset {
	ret := &models.Asset{}
	if err := db.First(ret, "storage_key = ?", storageKey).Error; err != nil {
		return nil
	}
	return ret
}

func (r *assetRepository) GetByStorageKeyInTenant(db *gorm.DB, storageKey string, tenantID int64) *models.Asset {
	if tenantID <= 0 {
		return nil
	}
	ret := &models.Asset{}
	if err := db.First(ret, "storage_key = ? AND tenant_id = ?", storageKey, tenantID).Error; err != nil {
		return nil
	}
	return ret
}
