package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ModelProfileTemplateRepository = newModelProfileTemplateRepository()
var ModelProfileSlotRepository = newModelProfileSlotRepository()

type modelProfileTemplateRepository struct{}
type modelProfileSlotRepository struct{}

func newModelProfileTemplateRepository() *modelProfileTemplateRepository {
	return &modelProfileTemplateRepository{}
}

func newModelProfileSlotRepository() *modelProfileSlotRepository {
	return &modelProfileSlotRepository{}
}

func (r *modelProfileTemplateRepository) Get(db *gorm.DB) *models.ModelProfileTemplate {
	item := &models.ModelProfileTemplate{}
	if err := db.Order("id ASC").First(item).Error; err != nil {
		return nil
	}
	return item
}

func (r *modelProfileTemplateRepository) Save(db *gorm.DB, item *models.ModelProfileTemplate) error {
	return db.Save(item).Error
}

func (r *modelProfileSlotRepository) GetByUsageCode(db *gorm.DB, templateID int64, usageCode string) *models.ModelProfileSlot {
	item := &models.ModelProfileSlot{}
	if err := db.Take(item, "template_id = ? AND usage_code = ?", templateID, usageCode).Error; err != nil {
		return nil
	}
	return item
}

func (r *modelProfileSlotRepository) FindByTemplateID(db *gorm.DB, templateID int64) []models.ModelProfileSlot {
	var list []models.ModelProfileSlot
	sqls.NewCnd().Eq("template_id", templateID).Asc("sort_no").Asc("id").Find(db, &list)
	return list
}

func (r *modelProfileSlotRepository) ReplaceTemplateSlots(db *gorm.DB, templateID int64, list []models.ModelProfileSlot) error {
	if err := db.Where("template_id = ?", templateID).Delete(&models.ModelProfileSlot{}).Error; err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	return db.Create(&list).Error
}
