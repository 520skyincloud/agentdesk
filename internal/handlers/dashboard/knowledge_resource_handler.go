package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func KnowledgeResourceAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "knowledgeBaseId"},
		params.QueryFilter{ParamName: "wxWorkInstanceId"},
	).Desc("id")
	cnd = services.KnowledgeResourceService.ApplyAccessibleScope(cnd, operator)
	groups, paging := services.KnowledgeResourceService.FindPageByCnd(cnd)
	results := make([]response.KnowledgeResourceGroupResponse, 0, len(groups))
	for index := range groups {
		group := &groups[index]
		if !services.KnowledgeBaseService.CanAccessKnowledgeBase(group.KnowledgeBaseID, operator) {
			continue
		}
		results = append(results, builders.BuildKnowledgeResourceGroup(group, services.KnowledgeResourceService.FindItems(group.ID)))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func KnowledgeResourcePostSync(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SyncKnowledgeResourceGroupRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	group, err := services.KnowledgeResourceService.SyncFastGPTResources(ctx.Request.Context(), req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildKnowledgeResourceGroup(group, services.KnowledgeResourceService.FindItems(group.ID)))
}

func KnowledgeResourcePostDelete(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseDelete)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DeleteKnowledgeResourceGroupRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.KnowledgeResourceService.DeleteGroup(req.ID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
