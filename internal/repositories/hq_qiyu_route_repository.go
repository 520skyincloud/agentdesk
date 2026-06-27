package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var HQQiyuRouteRepository = newHQQiyuRouteRepository()

func newHQQiyuRouteRepository() *hqQiyuRouteRepository {
	return &hqQiyuRouteRepository{}
}

type hqQiyuRouteRepository struct{}

func (r *hqQiyuRouteRepository) Get(db *gorm.DB, id int64) *models.HQQiyuRoute {
	ret := &models.HQQiyuRoute{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *hqQiyuRouteRepository) Take(db *gorm.DB, where ...any) *models.HQQiyuRoute {
	ret := &models.HQQiyuRoute{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *hqQiyuRouteRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.HQQiyuRoute {
	ret := &models.HQQiyuRoute{}
	if err := cnd.FindOne(db, ret); err != nil {
		return nil
	}
	return ret
}

func (r *hqQiyuRouteRepository) Create(db *gorm.DB, t *models.HQQiyuRoute) error {
	return db.Create(t).Error
}

func (r *hqQiyuRouteRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.HQQiyuRoute{}).Where("id = ?", id).Updates(columns).Error
}
