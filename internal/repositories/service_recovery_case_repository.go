package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ServiceRecoveryCaseRepository = newServiceRecoveryCaseRepository()

type serviceRecoveryCaseRepository struct{}

func newServiceRecoveryCaseRepository() *serviceRecoveryCaseRepository {
	return &serviceRecoveryCaseRepository{}
}

func (r *serviceRecoveryCaseRepository) Get(db *gorm.DB, id int64) *models.ServiceRecoveryCase {
	item := &models.ServiceRecoveryCase{}
	if db.First(item, "id = ?", id).Error != nil {
		return nil
	}
	return item
}

func (r *serviceRecoveryCaseRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.ServiceRecoveryCase {
	item := &models.ServiceRecoveryCase{}
	if cnd.FindOne(db, &item) != nil {
		return nil
	}
	return item
}

func (r *serviceRecoveryCaseRepository) Create(db *gorm.DB, item *models.ServiceRecoveryCase) error {
	return db.Create(item).Error
}

func (r *serviceRecoveryCaseRepository) Update(db *gorm.DB, item *models.ServiceRecoveryCase) error {
	return db.Save(item).Error
}
