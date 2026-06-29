package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var WxWorkProtocolDevicePoolRepository = newWxWorkProtocolDevicePoolRepository()

func newWxWorkProtocolDevicePoolRepository() *wxWorkProtocolDevicePoolRepository {
	return &wxWorkProtocolDevicePoolRepository{}
}

type wxWorkProtocolDevicePoolRepository struct{}

func (r *wxWorkProtocolDevicePoolRepository) Get(db *gorm.DB, id int64) *models.WxWorkProtocolDevicePoolInstance {
	ret := &models.WxWorkProtocolDevicePoolInstance{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *wxWorkProtocolDevicePoolRepository) Take(db *gorm.DB, where ...any) *models.WxWorkProtocolDevicePoolInstance {
	ret := &models.WxWorkProtocolDevicePoolInstance{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *wxWorkProtocolDevicePoolRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.WxWorkProtocolDevicePoolInstance) {
	cnd.Find(db, &list)
	return
}

func (r *wxWorkProtocolDevicePoolRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.WxWorkProtocolDevicePoolInstance, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *wxWorkProtocolDevicePoolRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.WxWorkProtocolDevicePoolInstance, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.WxWorkProtocolDevicePoolInstance{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *wxWorkProtocolDevicePoolRepository) Create(db *gorm.DB, t *models.WxWorkProtocolDevicePoolInstance) error {
	return db.Create(t).Error
}

func (r *wxWorkProtocolDevicePoolRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.WxWorkProtocolDevicePoolInstance{}).Where("id = ?", id).Updates(columns).Error
}

func (r *wxWorkProtocolDevicePoolRepository) UpdateByGUID(db *gorm.DB, guid string, columns map[string]any) error {
	return db.Model(&models.WxWorkProtocolDevicePoolInstance{}).Where("guid = ?", guid).Updates(columns).Error
}

func (r *wxWorkProtocolDevicePoolRepository) Delete(db *gorm.DB, id int64) error {
	return db.Delete(&models.WxWorkProtocolDevicePoolInstance{}, "id = ?", id).Error
}
