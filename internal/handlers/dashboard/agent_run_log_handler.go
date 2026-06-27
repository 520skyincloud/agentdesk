package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func AgentRunLogAnyList(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "conversationId"},
		params.QueryFilter{ParamName: "messageId"},
		params.QueryFilter{ParamName: "requestId"},
		params.QueryFilter{ParamName: "aiAgentId"},
		params.QueryFilter{ParamName: "plannedAction"},
		params.QueryFilter{ParamName: "plannedSkillCode", Op: params.Like},
		params.QueryFilter{ParamName: "graphToolCode"},
		params.QueryFilter{ParamName: "interruptType"},
		params.QueryFilter{ParamName: "resumeSource"},
		params.QueryFilter{ParamName: "finalStatus"},
		params.QueryFilter{ParamName: "handoffReason", Op: params.Like},
		params.QueryFilter{ParamName: "finalAction"},
		params.QueryFilter{ParamName: "userMessage", Op: params.Like},
	).Desc("id")
	if hitlStatus, _ := params.Get(ctx, "hitlStatus"); hitlStatus != "" && hitlStatus != "all" {
		cnd = services.AgentRunLogService.ApplyHITLStatusFilter(cnd, hitlStatus)
	}
	queryParams := params.NewQueryParams(ctx)
	queryParams.Cnd = *cnd
	list, paging := services.AgentRunLogService.FindPageByParams(queryParams)
	results := make([]response.AgentRunLogResponse, 0, len(list))
	for _, item := range list {
		results = append(results, builders.BuildAgentRunLog(&item))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func AgentRunLogGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	item := services.AgentRunLogService.Get(id)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("Agent 运行日志不存在"))
		return
	}
	httpx.WriteJSON(ctx, builders.BuildAgentRunLog(item))
}
