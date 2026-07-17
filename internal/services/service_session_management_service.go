package services

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type ServiceSessionQuery struct {
	Page             int
	Limit            int
	ConversationID   int64
	SessionNo        int
	Status           string
	TeamID           int64
	SquadID          int64
	AgentID          int64
	ChannelID        int64
	StoreID          int64
	WxWorkInstanceID int64
	DataQuality      string
	ResolutionCode   string
	CategoryCode     string
	TagID            int64
	Keyword          string
	QualityStatus    string
	StartAt          *time.Time
	EndAt            *time.Time
	HumanOnly        bool
	WaitingReply     bool
	SLABreached      bool
	SLAReferenceTime time.Time
}

func (s *serviceAnalyticsService) UpdateSessionAnnotation(req request.UpdateServiceSessionAnnotationRequest, operator *dto.AuthPrincipal) (*models.ConversationServiceSession, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	item := repositories.ConversationServiceSessionRepository.GetInTenant(sqls.DB(), req.ID, tenantID)
	if item == nil || !AgentTeamScopeService.CanViewConversation(operator, item.ConversationID) {
		return nil, errorsx.InvalidParam("服务轮次不存在")
	}
	if !s.canViewAgent(operator, item.AssignedAgentID, item.AssignedTeamID) {
		return nil, errorsx.Forbidden("无权更新该服务轮次")
	}
	resolutionCode := strings.TrimSpace(req.ResolutionCode)
	categoryCode := strings.TrimSpace(req.CategoryCode)
	summary := strings.TrimSpace(req.SessionSummary)
	if len(resolutionCode) > 50 || len(categoryCode) > 50 || len(summary) > 4000 {
		return nil, errorsx.InvalidParam("解决状态、咨询分类或服务小记超出长度限制")
	}
	tagIDs := uniqueAnalyticsTagIDs(req.TagIDs)
	for _, tagID := range tagIDs {
		if repositories.TagRepository.GetInTenant(sqls.DB(), tagID, tenantID) == nil {
			return nil, errorsx.InvalidParam("服务小记包含无效标签")
		}
	}
	tagJSON, _ := json.Marshal(tagIDs)
	oldPayload, _ := json.Marshal(map[string]any{
		"resolutionCode": item.ResolutionCode,
		"categoryCode":   item.CategoryCode,
		"sessionSummary": item.SessionSummary,
		"tagIds":         jsonInt64Slice(item.TagIDsJSON),
	})
	newPayload, _ := json.Marshal(map[string]any{
		"resolutionCode": resolutionCode,
		"categoryCode":   categoryCode,
		"sessionSummary": summary,
		"tagIds":         tagIDs,
	})
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.ConversationServiceSessionRepository.GetInTenant(ctx.Tx, req.ID, tenantID)
		if current == nil || current.ConversationID != item.ConversationID ||
			current.AssignedAgentID != item.AssignedAgentID || current.AssignedTeamID != item.AssignedTeamID {
			return errorsx.InvalidParam("服务轮次不存在")
		}
		now := time.Now()
		if err := repositories.ConversationServiceSessionRepository.UpdatesInTenant(ctx.Tx, current.ID, tenantID, map[string]any{
			"resolution_code":  resolutionCode,
			"category_code":    categoryCode,
			"session_summary":  summary,
			"tag_ids_json":     string(tagJSON),
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{
			"sessionNo": current.SessionNo,
			"before":    json.RawMessage(oldPayload),
			"after":     json.RawMessage(newPayload),
		})
		return ConversationEventLogService.CreateEvent(ctx, current.ConversationID, enums.IMEventTypeRouteChange, enums.IMSenderTypeAgent, operator.UserID, "更新会话服务小记", string(payload))
	})
	if err != nil {
		return nil, err
	}
	return repositories.ConversationServiceSessionRepository.GetInTenant(sqls.DB(), req.ID, tenantID), nil
}

func (s *serviceAnalyticsService) ListSessions(query ServiceSessionQuery, operator *dto.AuthPrincipal) ([]models.ConversationServiceSession, *sqls.Paging, error) {
	cnd, err := s.buildServiceSessionCnd(query, operator)
	if err != nil {
		return nil, nil, err
	}
	page, limit := normalizeServiceSessionPaging(query.Page, query.Limit)
	cnd.Desc("started_at").Desc("id").Page(page, limit)
	list := repositories.ConversationServiceSessionRepository.Find(sqls.DB(), cnd)
	paging := &sqls.Paging{Page: page, Limit: limit, Total: repositories.ConversationServiceSessionRepository.Count(sqls.DB(), cnd)}
	return list, paging, nil
}

func (s *serviceAnalyticsService) ExportSessions(query ServiceSessionQuery, operator *dto.AuthPrincipal, limit int) ([]models.ConversationServiceSession, error) {
	cnd, err := s.buildServiceSessionCnd(query, operator)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	total := repositories.ConversationServiceSessionRepository.Count(sqls.DB(), cnd)
	if total > int64(limit) {
		return nil, errorsx.InvalidParam(fmt.Sprintf("导出结果共%d条，超过单次上限%d条，请缩小筛选范围", total, limit))
	}
	cnd.Desc("started_at").Desc("id").Limit(limit)
	return repositories.ConversationServiceSessionRepository.Find(sqls.DB(), cnd), nil
}

func (s *serviceAnalyticsService) buildServiceSessionCnd(query ServiceSessionQuery, operator *dto.AuthPrincipal) (*sqls.Cnd, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要查看会话记录的接入公司")
	}
	cnd := sqls.NewCnd().Eq("tenant_id", tenantID)
	cnd = s.applySessionScope(cnd, ServiceAnalyticsQuery{
		TeamID: query.TeamID, SquadID: query.SquadID, AgentID: query.AgentID,
		StoreID: query.StoreID, WxWorkInstanceID: query.WxWorkInstanceID, DataQuality: query.DataQuality,
	}, operator)
	if query.ConversationID > 0 {
		cnd.Eq("conversation_id", query.ConversationID)
	}
	if query.SessionNo > 0 {
		cnd.Eq("session_no", query.SessionNo)
	}
	switch enums.ServiceSessionStatus(strings.TrimSpace(query.Status)) {
	case enums.ServiceSessionStatusOpen, enums.ServiceSessionStatusClosed:
		cnd.Eq("status", strings.TrimSpace(query.Status))
	}
	if query.ChannelID > 0 {
		cnd.Eq("channel_id", query.ChannelID)
	}
	if value := strings.TrimSpace(query.ResolutionCode); value != "" {
		cnd.Eq("resolution_code", value)
	}
	if value := strings.TrimSpace(query.CategoryCode); value != "" {
		cnd.Eq("category_code", value)
	}
	if query.StartAt != nil {
		cnd.Gte("started_at", *query.StartAt)
	}
	if query.EndAt != nil {
		cnd.Lte("started_at", *query.EndAt)
	}
	if query.HumanOnly {
		cnd.Gt("human_message_count", 0)
	}
	if query.WaitingReply {
		cnd.Where(`EXISTS (
			SELECT 1 FROM t_conversation_response_span response_span
			WHERE response_span.tenant_id = t_conversation_service_session.tenant_id
			AND response_span.conversation_id = t_conversation_service_session.conversation_id
			AND response_span.session_no = t_conversation_service_session.session_no
			AND response_span.status = ?
		)`, enums.ResponseSpanStatusWaiting)
	}
	s.applyServiceSessionQualityFilter(cnd, strings.TrimSpace(query.QualityStatus))
	s.applyServiceSessionTagFilter(cnd, tenantID, query.TagID)
	s.applyServiceSessionKeywordFilter(cnd, tenantID, query.Keyword)
	if query.SLABreached {
		s.applyServiceSessionSLAFilter(cnd, tenantID, query.SLAReferenceTime)
	}
	return cnd, nil
}

func (s *serviceAnalyticsService) applyServiceSessionQualityFilter(cnd *sqls.Cnd, qualityStatus string) {
	completedInspection := `EXISTS (
		SELECT 1 FROM t_quality_inspection quality_inspection
		WHERE quality_inspection.tenant_id = assignment.tenant_id
		AND quality_inspection.assignment_id = assignment.id
		AND quality_inspection.status = ?
	)`
	eligibleAssignment := `EXISTS (
		SELECT 1 FROM t_conversation_assignment assignment
		WHERE assignment.tenant_id = t_conversation_service_session.tenant_id
		AND assignment.conversation_id = t_conversation_service_session.conversation_id
		AND assignment.session_no = t_conversation_service_session.session_no
		AND EXISTS (
			SELECT 1 FROM t_message quality_message
			WHERE quality_message.tenant_id = assignment.tenant_id
			AND quality_message.conversation_id = assignment.conversation_id
			AND quality_message.session_no = assignment.session_no
			AND quality_message.sender_type = ?
			AND quality_message.sender_id = assignment.to_user_id
			AND quality_message.created_at >= assignment.created_at
			AND (assignment.finished_at IS NULL OR quality_message.created_at <= assignment.finished_at)
		)
		AND %s
	)`
	switch qualityStatus {
	case "pending":
		cnd.Where(fmt.Sprintf(eligibleAssignment, "NOT "+completedInspection), enums.IMSenderTypeAgent, enums.QualityInspectionStatusCompleted)
	case "completed":
		cnd.Where(fmt.Sprintf(eligibleAssignment, completedInspection), enums.IMSenderTypeAgent, enums.QualityInspectionStatusCompleted)
	}
}

func (s *serviceAnalyticsService) applyServiceSessionTagFilter(cnd *sqls.Cnd, tenantID, tagID int64) {
	if tagID <= 0 {
		return
	}
	tagIDs := TagService.GetSelfAndDescendantIDsInTenant(tagID, tenantID)
	if len(tagIDs) == 0 {
		cnd.Eq("id", -1)
		return
	}
	clauses := make([]string, 0, len(tagIDs))
	args := make([]any, 0, len(tagIDs)*4)
	for _, id := range tagIDs {
		token := strconv.FormatInt(id, 10)
		clauses = append(clauses, "(tag_ids_json = ? OR tag_ids_json LIKE ? OR tag_ids_json LIKE ? OR tag_ids_json LIKE ?)")
		args = append(args, "["+token+"]", "["+token+",%", "%,"+token+",%", "%,"+token+"]")
	}
	cnd.Where("("+strings.Join(clauses, " OR ")+")", args...)
}

func (s *serviceAnalyticsService) applyServiceSessionKeywordFilter(cnd *sqls.Cnd, tenantID int64, keyword string) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return
	}
	if conversationID, err := strconv.ParseInt(keyword, 10, 64); err == nil && conversationID > 0 {
		cnd.Where("(conversation_id = ? OR conversation_id IN (SELECT id FROM t_conversation WHERE tenant_id = ? AND customer_name LIKE ?))", conversationID, tenantID, "%"+keyword+"%")
		return
	}
	cnd.Where("conversation_id IN (SELECT id FROM t_conversation WHERE tenant_id = ? AND customer_name LIKE ?)", tenantID, "%"+keyword+"%")
}

func (s *serviceAnalyticsService) applyServiceSessionSLAFilter(cnd *sqls.Cnd, tenantID int64, referenceAt time.Time) {
	if referenceAt.IsZero() {
		referenceAt = time.Now()
	}
	policy := s.GetPolicy(tenantID)
	queueTarget := int64(policy.QueueTargetSeconds)
	firstResponseTarget := int64(policy.FirstResponseTargetSeconds)
	responseTarget := int64(policy.ResponseTargetSeconds)
	cnd.Where(`(
		(queue_entered_at IS NOT NULL AND ((assigned_at IS NULL AND status = ? AND queue_entered_at < ?) OR (assigned_at IS NOT NULL AND queue_seconds > ?)))
		OR (assigned_at IS NOT NULL AND ((first_human_reply_at IS NULL AND status = ? AND assigned_at < ?) OR (first_human_reply_at IS NOT NULL AND first_response_seconds > ?)))
		OR EXISTS (
			SELECT 1 FROM t_conversation_response_span sla_span
			WHERE sla_span.tenant_id = t_conversation_service_session.tenant_id
			AND sla_span.conversation_id = t_conversation_service_session.conversation_id
			AND sla_span.session_no = t_conversation_service_session.session_no
			AND ((sla_span.status = ? AND sla_span.started_at < ?) OR (sla_span.status <> ? AND sla_span.wait_seconds > ?))
		)
	)`,
		enums.ServiceSessionStatusOpen, referenceAt.Add(-time.Duration(queueTarget)*time.Second), queueTarget,
		enums.ServiceSessionStatusOpen, referenceAt.Add(-time.Duration(firstResponseTarget)*time.Second), firstResponseTarget,
		enums.ResponseSpanStatusWaiting, referenceAt.Add(-time.Duration(responseTarget)*time.Second), enums.ResponseSpanStatusWaiting, responseTarget,
	)
}

func normalizeServiceSessionPaging(page, limit int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return page, limit
}

func uniqueAnalyticsTagIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}

func jsonInt64Slice(raw string) []int64 {
	ret := []int64{}
	_ = json.Unmarshal([]byte(raw), &ret)
	return ret
}
