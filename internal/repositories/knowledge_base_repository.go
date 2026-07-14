package repositories

import (
	"agent-desk/internal/models"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var KnowledgeBaseRepository = newKnowledgeBaseRepository()

func newKnowledgeBaseRepository() *knowledgeBaseRepository {
	return &knowledgeBaseRepository{}
}

type knowledgeBaseRepository struct {
}

func (r *knowledgeBaseRepository) Get(db *gorm.DB, id int64) *models.KnowledgeBase {
	ret := &models.KnowledgeBase{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *knowledgeBaseRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.KnowledgeBase {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	return r.Take(db, "id = ? AND tenant_id = ?", id, tenantID)
}

func (r *knowledgeBaseRepository) Take(db *gorm.DB, where ...interface{}) *models.KnowledgeBase {
	ret := &models.KnowledgeBase{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *knowledgeBaseRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.KnowledgeBase) {
	cnd.Find(db, &list)
	return
}

func (r *knowledgeBaseRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.KnowledgeBase {
	ret := &models.KnowledgeBase{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *knowledgeBaseRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.KnowledgeBase, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *knowledgeBaseRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.KnowledgeBase, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.KnowledgeBase{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *knowledgeBaseRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.KnowledgeBase{})
}

func (r *knowledgeBaseRepository) Create(db *gorm.DB, t *models.KnowledgeBase) (err error) {
	err = db.Create(t).Error
	return
}

func (r *knowledgeBaseRepository) Update(db *gorm.DB, t *models.KnowledgeBase) (err error) {
	err = db.Save(t).Error
	return
}

func (r *knowledgeBaseRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.KnowledgeBase{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *knowledgeBaseRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	return db.Model(&models.KnowledgeBase{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *knowledgeBaseRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.KnowledgeBase{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *knowledgeBaseRepository) UpdateColumnInTenant(db *gorm.DB, id, tenantID int64, name string, value any) error {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	return db.Model(&models.KnowledgeBase{}).Where("id = ? AND tenant_id = ?", id, tenantID).UpdateColumn(name, value).Error
}

func (r *knowledgeBaseRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.KnowledgeBase{}, "id = ?", id)
}

func (r *knowledgeBaseRepository) DeleteInTenant(db *gorm.DB, id, tenantID int64) error {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	return db.Delete(&models.KnowledgeBase{}, "id = ? AND tenant_id = ?", id, tenantID).Error
}
