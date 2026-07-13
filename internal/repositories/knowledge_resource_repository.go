package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var KnowledgeResourceGroupRepository = newKnowledgeResourceGroupRepository()
var KnowledgeResourceItemRepository = newKnowledgeResourceItemRepository()

type knowledgeResourceGroupRepository struct{}

func newKnowledgeResourceGroupRepository() *knowledgeResourceGroupRepository {
	return &knowledgeResourceGroupRepository{}
}

func (r *knowledgeResourceGroupRepository) Get(db *gorm.DB, id int64) *models.KnowledgeResourceGroup {
	ret := &models.KnowledgeResourceGroup{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *knowledgeResourceGroupRepository) Take(db *gorm.DB, where ...any) *models.KnowledgeResourceGroup {
	ret := &models.KnowledgeResourceGroup{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *knowledgeResourceGroupRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.KnowledgeResourceGroup) {
	cnd.Find(db, &list)
	return
}

func (r *knowledgeResourceGroupRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.KnowledgeResourceGroup, paging *sqls.Paging) {
	cnd.Find(db, &list)
	return list, &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: cnd.Count(db, &models.KnowledgeResourceGroup{})}
}

func (r *knowledgeResourceGroupRepository) Create(db *gorm.DB, item *models.KnowledgeResourceGroup) error {
	return db.Create(item).Error
}

func (r *knowledgeResourceGroupRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.KnowledgeResourceGroup{}).Where("id = ?", id).Updates(columns).Error
}

func (r *knowledgeResourceGroupRepository) Delete(db *gorm.DB, id int64) error {
	return db.Delete(&models.KnowledgeResourceGroup{}, "id = ?", id).Error
}

type knowledgeResourceItemRepository struct{}

func newKnowledgeResourceItemRepository() *knowledgeResourceItemRepository {
	return &knowledgeResourceItemRepository{}
}

func (r *knowledgeResourceItemRepository) FindByGroupID(db *gorm.DB, groupID int64) (list []models.KnowledgeResourceItem) {
	db.Where("knowledge_resource_group_id = ?", groupID).Order("sort_no ASC, id ASC").Find(&list)
	return
}

func (r *knowledgeResourceItemRepository) FindByAssetID(db *gorm.DB, assetID string) (list []models.KnowledgeResourceItem) {
	db.Where("asset_id = ?", assetID).Find(&list)
	return
}

func (r *knowledgeResourceItemRepository) Create(db *gorm.DB, item *models.KnowledgeResourceItem) error {
	return db.Create(item).Error
}

func (r *knowledgeResourceItemRepository) DeleteByGroupID(db *gorm.DB, groupID int64) error {
	return db.Where("knowledge_resource_group_id = ?", groupID).Delete(&models.KnowledgeResourceItem{}).Error
}
