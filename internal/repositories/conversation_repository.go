package repositories

import (
	"errors"

	"agent-desk/internal/models"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ConversationRepository = newConversationRepository()

func newConversationRepository() *conversationRepository {
	return &conversationRepository{}
}

type conversationRepository struct {
}

func (r *conversationRepository) Get(db *gorm.DB, id int64) *models.Conversation {
	ret := &models.Conversation{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.Conversation {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.Conversation{}
	if err := db.First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.Conversation, error) {
	if id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	ret := &models.Conversation{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *conversationRepository) Take(db *gorm.DB, where ...interface{}) *models.Conversation {
	ret := &models.Conversation{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Conversation) {
	cnd.Find(db, &list)
	return
}

func (r *conversationRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.Conversation {
	ret := &models.Conversation{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *conversationRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Conversation, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *conversationRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Conversation, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Conversation{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *conversationRepository) FindPageByCndWithManualAttentionFirst(db *gorm.DB, cnd *sqls.Cnd) (list []models.Conversation, paging *sqls.Paging) {
	query := db.Order(`CASE WHEN EXISTS (
		SELECT 1 FROM t_conversation_route_state
		WHERE t_conversation_route_state.conversation_id = t_conversation.id
		AND t_conversation_route_state.tenant_id = t_conversation.tenant_id
		AND t_conversation_route_state.need_human_follow_up = 1
	) THEN 0 ELSE 1 END ASC`)
	cnd.Find(query, &list)
	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: cnd.Count(db, &models.Conversation{}),
	}
	return
}

func (r *conversationRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (list []models.Conversation) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *conversationRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *conversationRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.Conversation{})
}

func (r *conversationRepository) Create(db *gorm.DB, t *models.Conversation) (err error) {
	err = db.Create(t).Error
	return
}

func (r *conversationRepository) Update(db *gorm.DB, t *models.Conversation) (err error) {
	err = db.Save(t).Error
	return
}

func (r *conversationRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.Conversation{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *conversationRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.Conversation{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *conversationRepository) ReleaseAIServingByWxWorkInstance(db *gorm.DB, wxWorkInstanceID, tenantID int64, now any, operatorID int64, operatorName string) error {
	return db.Model(&models.Conversation{}).
		Where("tenant_id = ?", tenantID).
		Where(`id IN (
			SELECT conversation_id FROM t_conversation_route_state
			WHERE tenant_id = ? AND wx_work_instance_id = ? AND route_status = ?
		)`, tenantID, wxWorkInstanceID, "AI_SERVING").
		Updates(map[string]any{
			"status":              1,
			"current_assignee_id": 0,
			"current_team_id":     0,
			"handoff_at":          nil,
			"handoff_reason":      "",
			"updated_at":          now,
			"update_user_id":      operatorID,
			"update_user_name":    operatorName,
		}).Error
}

func (r *conversationRepository) UpdatesByCustomerID(db *gorm.DB, customerID int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.Conversation{}).Where("customer_id = ?", customerID).Updates(columns).Error
	return
}

func (r *conversationRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.Conversation{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *conversationRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.Conversation{}, "id = ?", id)
}
