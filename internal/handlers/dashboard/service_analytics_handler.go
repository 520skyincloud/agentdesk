package dashboard

import (
	"strings"
	"time"

	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pkg/i18nx"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func ServiceAnalyticsGetOverview(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionServiceAnalyticsView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	query := serviceAnalyticsQueryFromContext(ctx)
	aggregate, err := services.ServiceAnalyticsService.GetOverview(query, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildServiceAnalyticsOverview(aggregate))
}

func ServiceAnalyticsGetDimensions(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionServiceAnalyticsView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	dimensions, err := services.ServiceAnalyticsService.GetDimensions(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildServiceAnalyticsDimensions(dimensions))
}

func ServiceSessionAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationRecordView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list, paging, err := services.ServiceAnalyticsService.ListSessions(serviceSessionQueryFromContext(ctx), operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	results := make([]response.ServiceSessionResponse, 0, len(list))
	for i := range list {
		results = append(results, builders.BuildServiceSession(&list[i]))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func serviceSessionQueryFromContext(ctx *gin.Context) services.ServiceSessionQuery {
	paging := params.GetPaging(ctx)
	query := services.ServiceSessionQuery{
		Page: paging.Page, Limit: paging.Limit,
		ConversationID: params.FormValueInt64Default(ctx, "conversationId", 0),
		SessionNo:      params.FormValueIntDefault(ctx, "sessionNo", 0), Status: params.FormValue(ctx, "status"),
		TeamID: params.FormValueInt64Default(ctx, "assignedTeamId", 0), SquadID: params.FormValueInt64Default(ctx, "assignedSquadId", 0),
		AgentID: params.FormValueInt64Default(ctx, "assignedAgentId", 0), ChannelID: params.FormValueInt64Default(ctx, "channelId", 0),
		StoreID: params.FormValueInt64Default(ctx, "storeId", 0), WxWorkInstanceID: params.FormValueInt64Default(ctx, "wxWorkInstanceId", 0),
		DataQuality: params.FormValue(ctx, "dataQuality"), ResolutionCode: params.FormValue(ctx, "resolutionCode"),
		CategoryCode: params.FormValue(ctx, "categoryCode"), TagID: params.FormValueInt64Default(ctx, "tagId", 0),
		Keyword: params.FormValue(ctx, "keyword"), QualityStatus: params.FormValue(ctx, "qualityStatus"),
	}
	if value := params.GetTime(ctx, "startAt"); value != nil {
		query.StartAt = value
	}
	if value := params.GetTime(ctx, "endAt"); value != nil {
		endAt := endOfRequestedDay(*value, params.FormValue(ctx, "endAt"))
		query.EndAt = &endAt
	}
	query.HumanOnly, _ = params.GetBool(ctx, "humanOnly")
	query.WaitingReply, _ = params.GetBool(ctx, "waitingReply")
	query.SLABreached, _ = params.GetBool(ctx, "slaBreached")
	return query
}

func ServiceSessionGetDimensions(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationRecordView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	dimensions, err := services.ServiceAnalyticsService.GetDimensions(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildServiceAnalyticsDimensions(dimensions))
}

func ServiceSessionGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationRecordView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ServiceAnalyticsService.GetSession(id, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildServiceSession(item))
}

func ServiceSessionAnyMessageList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationRecordView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	sessionID := params.FormValueInt64Default(ctx, "sessionId", 0)
	session, err := services.ServiceAnalyticsService.GetSession(sessionID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "senderType"},
		params.QueryFilter{ParamName: "messageType"},
	).Eq("tenant_id", session.TenantID).Eq("conversation_id", session.ConversationID).Eq("session_no", session.SessionNo).Asc("seq_no").Asc("id")
	list, paging := services.MessageService.FindPageByCnd(cnd)
	httpx.WriteJSON(ctx, &web.PageResult{Results: builders.BuildMessagesWithLocale(list, i18nx.Locale(ctx)), Page: paging})
}

func QualityInspectionAnyPool(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQualityInspectionView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	paging := params.GetPaging(ctx)
	query := services.QualityPoolQuery{
		Page: paging.Page, Limit: paging.Limit, ConversationID: params.FormValueInt64Default(ctx, "conversationId", 0),
		SessionNo: params.FormValueIntDefault(ctx, "sessionNo", 0), AgentID: params.FormValueInt64Default(ctx, "agentId", 0),
		TeamID: params.FormValueInt64Default(ctx, "teamId", 0), Status: params.FormValue(ctx, "status"), Keyword: params.FormValue(ctx, "keyword"),
		StartAt: params.GetTime(ctx, "startAt"),
	}
	if value := params.GetTime(ctx, "endAt"); value != nil {
		endAt := endOfRequestedDay(*value, params.FormValue(ctx, "endAt"))
		query.EndAt = &endAt
	}
	list, resultPaging, err := services.QualityInspectionService.ListPool(query, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	results := make([]response.QualityPoolEntryResponse, 0, len(list))
	for _, item := range list {
		results = append(results, builders.BuildQualityPoolEntry(item))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: resultPaging})
}

func QualityInspectionGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQualityInspectionView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.QualityInspectionService.GetInspection(id, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildQualityInspection(item))
}

func QualityInspectionPostSave(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQualityInspectionManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SaveQualityInspectionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.QualityInspectionService.SaveInspection(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildQualityInspection(item))
}

func QualityTemplateAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQualityInspectionView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list, err := services.QualityInspectionService.ListTemplates(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	results := make([]response.QualityTemplateResponse, 0, len(list))
	for _, item := range list {
		results = append(results, builders.BuildQualityTemplate(item))
	}
	httpx.WriteJSON(ctx, results)
}

func QualityTemplatePostSave(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQualityTemplateManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SaveQualityTemplateRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.QualityInspectionService.SaveTemplate(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildQualityTemplate(*item))
}

func ServiceAnalyticsGetPolicy(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionServiceAnalyticsView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildServiceAnalyticsPolicy(services.ServiceAnalyticsService.GetPolicy(operator.ActiveTenantID)))
}

func ServiceAnalyticsPostUpdatePolicy(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionServiceAnalyticsManagePolicy)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SaveServiceAnalyticsPolicyRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ServiceAnalyticsService.UpdatePolicy(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildServiceAnalyticsPolicy(*item))
}

func endOfRequestedDay(value time.Time, raw string) time.Time {
	if strings.Contains(raw, ":") {
		return value
	}
	return value.Add(24*time.Hour - time.Nanosecond)
}
