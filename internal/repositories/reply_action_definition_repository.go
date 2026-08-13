package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ReplyActionDefinitionRepository = newReplyActionDefinitionRepository()

func newReplyActionDefinitionRepository() *replyActionDefinitionRepository {
	return &replyActionDefinitionRepository{}
}

type replyActionDefinitionRepository struct{}

func (r *replyActionDefinitionRepository) Get(db *gorm.DB, id int64) *models.ReplyActionDefinition {
	ret := &models.ReplyActionDefinition{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *replyActionDefinitionRepository) Take(db *gorm.DB, where ...any) *models.ReplyActionDefinition {
	ret := &models.ReplyActionDefinition{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *replyActionDefinitionRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.ReplyActionDefinition) {
	cnd.Find(db, &list)
	return
}

func (r *replyActionDefinitionRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.ReplyActionDefinition, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.ReplyActionDefinition{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

// UpsertByCode 以 code 为唯一键插入或更新动作定义；用于把代码注册表种子到数据库。
func (r *replyActionDefinitionRepository) UpsertByCode(db *gorm.DB, item *models.ReplyActionDefinition) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "code"}},
		DoUpdates: clause.Assignments(map[string]any{
			"name":                 item.Name,
			"kind":                 item.Kind,
			"description":          item.Description,
			"input_schema":         item.InputSchema,
			"require_confirmation": item.RequireConfirmation,
			"executor_ref":         item.ExecutorRef,
			"sort_no":              item.SortNo,
			"updated_at":           item.UpdatedAt,
		}),
	}).Create(item).Error
}

func (r *replyActionDefinitionRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.ReplyActionDefinition{}).Where("id = ?", id).Updates(columns).Error
}
