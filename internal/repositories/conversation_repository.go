package repositories

import (
	"errors"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

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

const pendingDispatchOldestWindowDivisor = 5

func pendingDispatchWindowSizes(limit int) (priorityLimit, oldestLimit int) {
	if limit <= 0 {
		return 0, 0
	}
	oldestLimit = limit / pendingDispatchOldestWindowDivisor
	if oldestLimit < 1 {
		oldestLimit = 1
	}
	if oldestLimit > limit {
		oldestLimit = limit
	}
	return limit - oldestLimit, oldestLimit
}

func findPendingConversationWindow(
	buildQuery func() *gorm.DB,
	idColumn, priorityOrder, oldestOrder string,
	limit int,
) ([]models.Conversation, error) {
	ret := make([]models.Conversation, 0, limit)
	if buildQuery == nil || limit <= 0 {
		return ret, nil
	}
	priorityLimit, _ := pendingDispatchWindowSizes(limit)
	if priorityLimit > 0 {
		priorityRows := make([]models.Conversation, 0, priorityLimit)
		if err := buildQuery().Order(priorityOrder).Limit(priorityLimit).Scan(&priorityRows).Error; err != nil {
			return nil, err
		}
		ret = append(ret, priorityRows...)
	}
	if len(ret) >= limit {
		return ret[:limit], nil
	}

	seenIDs := make([]int64, 0, len(ret))
	for _, conversation := range ret {
		seenIDs = append(seenIDs, conversation.ID)
	}
	oldestQuery := buildQuery()
	if len(seenIDs) > 0 {
		oldestQuery = oldestQuery.Where(idColumn+" NOT IN ?", seenIDs)
	}
	oldestRows := make([]models.Conversation, 0, limit-len(ret))
	if err := oldestQuery.Order(oldestOrder).Limit(limit - len(ret)).Scan(&oldestRows).Error; err != nil {
		return nil, err
	}
	return append(ret, oldestRows...), nil
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

func (r *conversationRepository) FindPendingUnassignedTenantIDs(db *gorm.DB, afterTenantID int64, limit int) ([]int64, error) {
	ret := make([]int64, 0)
	if db == nil || limit <= 0 {
		return ret, nil
	}
	load := func(where string, args ...any) ([]int64, error) {
		ids := make([]int64, 0)
		query := db.Model(&models.Conversation{}).
			Distinct("tenant_id").
			Where("tenant_id > ?", 0).
			Where("status = ? AND current_assignee_id = ?", enums.IMConversationStatusPending, 0)
		if where != "" {
			query = query.Where(where, args...)
		}
		err := query.Order("tenant_id ASC").Limit(limit-len(ret)).Pluck("tenant_id", &ids).Error
		return ids, err
	}
	if afterTenantID > 0 {
		ids, err := load("tenant_id > ?", afterTenantID)
		if err != nil {
			return nil, err
		}
		ret = append(ret, ids...)
	}
	if len(ret) < limit {
		where := ""
		args := []any(nil)
		if afterTenantID > 0 {
			where = "tenant_id <= ?"
			args = []any{afterTenantID}
		}
		ids, err := load(where, args...)
		if err != nil {
			return nil, err
		}
		ret = append(ret, ids...)
	}
	return ret, nil
}

func (r *conversationRepository) FindPendingUnassignedByTenant(db *gorm.DB, tenantID int64, limit int) ([]models.Conversation, error) {
	ret := make([]models.Conversation, 0)
	if db == nil || tenantID <= 0 || limit <= 0 {
		return ret, nil
	}
	return findPendingConversationWindow(func() *gorm.DB {
		return db.Model(&models.Conversation{}).
			Where("tenant_id = ? AND status = ? AND current_assignee_id = ?", tenantID, enums.IMConversationStatusPending, 0)
	}, "id",
		"priority DESC, CASE WHEN handoff_at IS NULL THEN created_at ELSE handoff_at END ASC, id ASC",
		"CASE WHEN handoff_at IS NULL THEN created_at ELSE handoff_at END ASC, id ASC",
		limit,
	)
}

func (r *conversationRepository) FindPendingUnassignedForTeam(db *gorm.DB, tenantID, teamID int64, limit int) ([]models.Conversation, error) {
	ret := make([]models.Conversation, 0)
	if db == nil || tenantID <= 0 || teamID <= 0 || limit <= 0 {
		return ret, nil
	}
	explicit, err := findPendingConversationWindow(func() *gorm.DB {
		return db.Table("t_conversation AS c").
			Select("DISTINCT c.*").
			Joins("LEFT JOIN t_conversation_route_state AS route ON route.conversation_id = c.id AND route.tenant_id = c.tenant_id").
			Joins("LEFT JOIN t_wx_work_protocol_instance AS instance ON instance.id = route.wx_work_instance_id AND instance.tenant_id = c.tenant_id").
			Joins("LEFT JOIN t_store_staff_binding AS binding ON binding.store_id = route.store_id AND binding.tenant_id = c.tenant_id AND binding.status = ?", enums.StatusOk).
			Where("c.tenant_id = ? AND c.status = ? AND c.current_assignee_id = ?", tenantID, enums.IMConversationStatusPending, 0).
			Where("c.current_team_id = ? OR instance.agent_team_id = ? OR binding.agent_team_id = ?", teamID, teamID, teamID)
	}, "c.id",
		"c.priority DESC, CASE WHEN c.handoff_at IS NULL THEN c.created_at ELSE c.handoff_at END ASC, c.id ASC",
		"CASE WHEN c.handoff_at IS NULL THEN c.created_at ELSE c.handoff_at END ASC, c.id ASC",
		limit,
	)
	if err != nil {
		return nil, err
	}
	ret = append(ret, explicit...)
	if len(ret) >= limit {
		return ret, nil
	}

	seenIDs := make([]int64, 0, len(ret))
	for _, conversation := range ret {
		seenIDs = append(seenIDs, conversation.ID)
	}
	fallback, err := findPendingConversationWindow(func() *gorm.DB {
		query := db.Model(&models.Conversation{}).
			Where("tenant_id = ? AND status = ? AND current_assignee_id = ? AND current_team_id = ?", tenantID, enums.IMConversationStatusPending, 0, 0)
		if len(seenIDs) > 0 {
			query = query.Where("id NOT IN ?", seenIDs)
		}
		return query
	}, "id",
		"priority DESC, CASE WHEN handoff_at IS NULL THEN created_at ELSE handoff_at END ASC, id ASC",
		"CASE WHEN handoff_at IS NULL THEN created_at ELSE handoff_at END ASC, id ASC",
		limit-len(ret),
	)
	if err != nil {
		return nil, err
	}
	return append(ret, fallback...), nil
}

func (r *conversationRepository) FindDispatchWorkbenchCandidates(
	db *gorm.DB,
	tenantID, assigneeID int64,
	keyword string,
	includeCurrent, includeClosed bool,
	closedSince time.Time,
	currentLimit, closedLimit int,
) ([]models.Conversation, bool, error) {
	ret := make([]models.Conversation, 0)
	if db == nil || tenantID <= 0 {
		return ret, false, nil
	}
	applyFilters := func(query *gorm.DB) *gorm.DB {
		if assigneeID > 0 {
			query = query.Where("current_assignee_id = ?", assigneeID)
		}
		if keyword != "" {
			keywordLike := "%" + keyword + "%"
			query = query.Where("(customer_name LIKE ? OR last_message_summary LIKE ?)", keywordLike, keywordLike)
		}
		return query
	}
	truncated := false
	if includeCurrent && currentLimit > 0 {
		current := make([]models.Conversation, 0)
		query := applyFilters(db.Where("tenant_id = ?", tenantID).
			Where("status IN ?", []enums.IMConversationStatus{enums.IMConversationStatusPending, enums.IMConversationStatusActive}))
		if err := query.Order("last_active_at DESC, id DESC").Limit(currentLimit + 1).Find(&current).Error; err != nil {
			return nil, false, err
		}
		if len(current) > currentLimit {
			current = current[:currentLimit]
			truncated = true
		}
		ret = append(ret, current...)
	}
	if includeClosed && closedLimit > 0 {
		closed := make([]models.Conversation, 0)
		query := applyFilters(db.Where("tenant_id = ? AND status = ?", tenantID, enums.IMConversationStatusClosed).
			Where("closed_at >= ? OR (closed_at IS NULL AND updated_at >= ?)", closedSince, closedSince))
		if err := query.Order("last_active_at DESC, id DESC").Limit(closedLimit + 1).Find(&closed).Error; err != nil {
			return nil, false, err
		}
		if len(closed) > closedLimit {
			closed = closed[:closedLimit]
			truncated = true
		}
		ret = append(ret, closed...)
	}
	return ret, truncated, nil
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
