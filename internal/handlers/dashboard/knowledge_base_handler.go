package dashboard

import (
	"agent-desk/internal/pkg/httpx"

	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func KnowledgeBaseAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "name", Op: params.Like},
	).Eq("knowledge_type", string(enums.KnowledgeBaseTypeFastGPTCloud)).Asc("sort_no").Desc("id")
	cnd = services.AgentTeamScopeService.ApplyKnowledgeBaseFilter(cnd, operator)
	list, paging := services.KnowledgeBaseService.FindPageByCnd(cnd)
	results := make([]response.KnowledgeBaseResponse, 0, len(list))
	for _, item := range list {
		resp := builders.BuildKnowledgeBase(&item)
		results = append(results, resp)
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func KnowledgeBaseAnyList_all(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	cnd := params.NewSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
	).Eq("knowledge_type", string(enums.KnowledgeBaseTypeFastGPTCloud)).Asc("sort_no").Desc("id")
	cnd = services.AgentTeamScopeService.ApplyKnowledgeBaseFilter(cnd, operator)
	list := services.KnowledgeBaseService.Find(cnd)
	results := make([]response.KnowledgeBaseResponse, 0, len(list))
	for _, item := range list {
		resp := builders.BuildKnowledgeBase(&item)
		results = append(results, resp)
	}
	httpx.WriteJSON(ctx, results)
}

func KnowledgeBaseGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	item := services.KnowledgeBaseService.GetForOperator(id, operator)
	if item == nil || item.KnowledgeType != string(enums.KnowledgeBaseTypeFastGPTCloud) {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("知识库不存在"))
		return
	}
	resp := builders.BuildKnowledgeBase(item)
	httpx.WriteJSON(ctx, resp)
}

func KnowledgeBasePostUpdate(ctx *gin.Context) {
	user, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.UpdateKnowledgeBaseRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.KnowledgeBaseService.UpdateKnowledgeBase(req, user); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func KnowledgeBasePostUpdate_sort(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	var ids []int64
	if err := params.ReadJSON(ctx, &ids); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.KnowledgeBaseService.UpdateSort(ids, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
