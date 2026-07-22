package repositories

import (
	"agent-desk/internal/models"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var TagRepository = newTagRepository()

func newTagRepository() *tagRepository {
	return &tagRepository{}
}

type tagRepository struct {
}

func (r *tagRepository) Get(db *gorm.DB, id int64) *models.Tag {
	ret := &models.Tag{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tagRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.Tag {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.Tag{}
	if err := db.First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tagRepository) TakeByTemplateInTenant(db *gorm.DB, tenantID, templateDefinitionID int64) *models.Tag {
	ret := &models.Tag{}
	if err := db.Take(ret, "tenant_id = ? AND template_definition_id = ?", tenantID, templateDefinitionID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tagRepository) TakeBySemanticKeyInTenant(db *gorm.DB, tenantID int64, semanticKey string) *models.Tag {
	ret := &models.Tag{}
	if err := db.Take(ret, "tenant_id = ? AND semantic_key = ?", tenantID, semanticKey).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tagRepository) FindByProfileInTenant(db *gorm.DB, tenantID, intentProfileID int64) ([]models.Tag, error) {
	ret := make([]models.Tag, 0)
	err := db.Where("tenant_id = ? AND intent_profile_id = ?", tenantID, intentProfileID).
		Order("parent_id ASC, sort_no ASC, id ASC").Find(&ret).Error
	return ret, err
}

func (r *tagRepository) Take(db *gorm.DB, where ...interface{}) *models.Tag {
	ret := &models.Tag{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tagRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Tag) {
	cnd.Find(db, &list)
	return
}

func (r *tagRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.Tag {
	ret := &models.Tag{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *tagRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Tag, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *tagRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Tag, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Tag{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *tagRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (list []models.Tag) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *tagRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *tagRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.Tag{})
}

func (r *tagRepository) Create(db *gorm.DB, t *models.Tag) (err error) {
	err = db.Create(t).Error
	return
}

func (r *tagRepository) Update(db *gorm.DB, t *models.Tag) (err error) {
	err = db.Save(t).Error
	return
}

func (r *tagRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.Tag{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *tagRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.Tag{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *tagRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.Tag{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *tagRepository) UpdateColumnInTenant(db *gorm.DB, id, tenantID int64, name string, value any) error {
	return db.Model(&models.Tag{}).Where("id = ? AND tenant_id = ?", id, tenantID).UpdateColumn(name, value).Error
}

func (r *tagRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.Tag{}, "id = ?", id)
}

func (r *tagRepository) DeleteInTenant(db *gorm.DB, id, tenantID int64) error {
	return db.Delete(&models.Tag{}, "id = ? AND tenant_id = ?", id, tenantID).Error
}
