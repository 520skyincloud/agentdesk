package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var KnowledgeActionBindingRepository = newKnowledgeActionBindingRepository()

func newKnowledgeActionBindingRepository() *knowledgeActionBindingRepository {
	return &knowledgeActionBindingRepository{}
}

type knowledgeActionBindingRepository struct{}

func (r *knowledgeActionBindingRepository) Get(db *gorm.DB, id int64) *models.KnowledgeActionBinding {
	ret := &models.KnowledgeActionBinding{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *knowledgeActionBindingRepository) Take(db *gorm.DB, where ...any) *models.KnowledgeActionBinding {
	ret := &models.KnowledgeActionBinding{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *knowledgeActionBindingRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.KnowledgeActionBinding) {
	cnd.Find(db, &list)
	return
}

func (r *knowledgeActionBindingRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.KnowledgeActionBinding, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.KnowledgeActionBinding{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

// UpsertByScope 以 (tenant, store, knowledge_base, source_record) 为唯一键插入或更新绑定。
func (r *knowledgeActionBindingRepository) UpsertByScope(db *gorm.DB, item *models.KnowledgeActionBinding) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "store_id"}, {Name: "knowledge_base_id"}, {Name: "source_record_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"action_code":      item.ActionCode,
			"enabled":          item.Enabled,
			"sort_no":          item.SortNo,
			"remark":           item.Remark,
			"update_user_id":   item.UpdateUserID,
			"update_user_name": item.UpdateUserName,
			"updated_at":       item.UpdatedAt,
		}),
	}).Create(item).Error
}

func (r *knowledgeActionBindingRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.KnowledgeActionBinding{}).Where("id = ?", id).Updates(columns).Error
}

func (r *knowledgeActionBindingRepository) Delete(db *gorm.DB, id int64) error {
	return db.Delete(&models.KnowledgeActionBinding{}, "id = ?", id).Error
}

// FindEnabledBySourceRecords 按 SourceRecordID 批量查启用绑定，返回 map[sourceRecordID]actionCode。
func (r *knowledgeActionBindingRepository) FindEnabledBySourceRecords(db *gorm.DB, tenantID, storeID, knowledgeBaseID int64, sourceRecordIDs []string) map[string]string {
	ret := map[string]string{}
	if db == nil || len(sourceRecordIDs) == 0 {
		return ret
	}
	var items []models.KnowledgeActionBinding
	if err := db.Where(
		"tenant_id = ? AND store_id = ? AND knowledge_base_id = ? AND source_record_id IN ? AND enabled = ?",
		tenantID, storeID, knowledgeBaseID, sourceRecordIDs, true,
	).Find(&items).Error; err != nil {
		return ret
	}
	for _, item := range items {
		if item.ActionCode != "" {
			ret[item.SourceRecordID] = item.ActionCode
		}
	}
	return ret
}
