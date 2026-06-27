package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var WxWorkProtocolInstanceRepository = newWxWorkProtocolInstanceRepository()

func newWxWorkProtocolInstanceRepository() *wxWorkProtocolInstanceRepository {
	return &wxWorkProtocolInstanceRepository{}
}

type wxWorkProtocolInstanceRepository struct{}

func (r *wxWorkProtocolInstanceRepository) Get(db *gorm.DB, id int64) *models.WxWorkProtocolInstance {
	ret := &models.WxWorkProtocolInstance{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *wxWorkProtocolInstanceRepository) Take(db *gorm.DB, where ...any) *models.WxWorkProtocolInstance {
	ret := &models.WxWorkProtocolInstance{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *wxWorkProtocolInstanceRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.WxWorkProtocolInstance) {
	cnd.Find(db, &list)
	return
}

func (r *wxWorkProtocolInstanceRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.WxWorkProtocolInstance, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *wxWorkProtocolInstanceRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.WxWorkProtocolInstance, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.WxWorkProtocolInstance{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *wxWorkProtocolInstanceRepository) Create(db *gorm.DB, t *models.WxWorkProtocolInstance) error {
	return db.Create(t).Error
}

func (r *wxWorkProtocolInstanceRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", id).Updates(columns).Error
}

func (r *wxWorkProtocolInstanceRepository) Delete(db *gorm.DB, id int64) error {
	return db.Delete(&models.WxWorkProtocolInstance{}, "id = ?", id).Error
}
