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

func KnowledgeRetrieveLogAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeDocumentView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "knowledgeBaseId"},
		params.QueryFilter{ParamName: "question", Op: params.Like},
		params.QueryFilter{ParamName: "sourceType"},
		params.QueryFilter{ParamName: "channel"},
		params.QueryFilter{ParamName: "scene"},
		params.QueryFilter{ParamName: "chunkProvider"},
	).Desc("id")

	if answerStatus, ok := params.GetInt64(ctx, "answerStatus"); ok && answerStatus > 0 {
		cnd.Where("answer_status = ?", answerStatus)
	}
	if rerankEnabled, ok := params.GetInt64(ctx, "rerankEnabled"); ok {
		cnd.Where("rerank_enabled = ?", rerankEnabled > 0)
	}

	cnd = services.AgentTeamScopeService.ApplyKnowledgeChildFilter(cnd, operator)
	list, paging := services.KnowledgeRetrieveLogService.FindPageInTenant(cnd, operator.ActiveTenantID)
	results := make([]response.KnowledgeRetrieveLogResponse, 0, len(list))
	for _, item := range list {
		resp := builders.BuildKnowledgeRetrieveLog(&item)
		if knowledgeBase := services.KnowledgeBaseService.GetForOperator(item.KnowledgeBaseID, operator); knowledgeBase != nil {
			resp.KnowledgeBaseName = knowledgeBase.Name
		}
		results = append(results, resp)
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func KnowledgeRetrieveLogGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeDocumentView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	logItem := services.KnowledgeRetrieveLogService.GetInTenant(id, operator.ActiveTenantID)
	if logItem == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("检索日志不存在"))
		return
	}

	logResp := builders.BuildKnowledgeRetrieveLog(logItem)
	if !services.KnowledgeBaseService.CanAccessKnowledgeBase(logItem.KnowledgeBaseID, operator) {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("无权限查看该检索日志"))
		return
	}
	if knowledgeBase := services.KnowledgeBaseService.GetForOperator(logItem.KnowledgeBaseID, operator); knowledgeBase != nil {
		logResp.KnowledgeBaseName = knowledgeBase.Name
	}

	hits := services.KnowledgeRetrieveLogService.FindHitsInTenant(id, operator.ActiveTenantID)
	hitResults := make([]response.KnowledgeRetrieveHitResponse, 0, len(hits))
	for _, item := range hits {
		hitResults = append(hitResults, builders.BuildKnowledgeRetrieveHitResponse(&item))
	}

	httpx.WriteJSON(ctx, response.KnowledgeRetrieveLogDetailResponse{
		Log:  logResp,
		Hits: hitResults,
	})
}
