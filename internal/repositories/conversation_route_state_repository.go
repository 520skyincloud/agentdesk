package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ConversationRouteStateRepository = newConversationRouteStateRepository()

func newConversationRouteStateRepository() *conversationRouteStateRepository {
	return &conversationRouteStateRepository{}
}

type conversationRouteStateRepository struct{}

func (r *conversationRouteStateRepository) Get(db *gorm.DB, id int64) *models.ConversationRouteState {
	ret := &models.ConversationRouteState{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationRouteStateRepository) Take(db *gorm.DB, where ...any) *models.ConversationRouteState {
	ret := &models.ConversationRouteState{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *conversationRouteStateRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.ConversationRouteState) {
	cnd.Find(db, &list)
	return
}

func (r *conversationRouteStateRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.ConversationRouteState, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *conversationRouteStateRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.ConversationRouteState, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.ConversationRouteState{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *conversationRouteStateRepository) Create(db *gorm.DB, t *models.ConversationRouteState) error {
	return db.Create(t).Error
}

func (r *conversationRouteStateRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.ConversationRouteState{}).Where("id = ?", id).Updates(columns).Error
}

func (r *conversationRouteStateRepository) ResetAIByWxWorkInstance(db *gorm.DB, wxWorkInstanceID int64, now any, operatorName string) error {
	return db.Model(&models.ConversationRouteState{}).
		Where("wx_work_instance_id = ? AND route_status <> ?", wxWorkInstanceID, "CLOSED").
		Updates(map[string]any{
			"route_status":         "AI_SERVING",
			"route_target":         "ai",
			"manual_expire_at":     nil,
			"need_human_follow_up": false,
			"handoff_reason":       "AI自动回复已开启",
			"updated_at":           now,
			"update_user_name":     operatorName,
		}).Error
}
