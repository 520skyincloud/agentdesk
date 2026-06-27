package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func KnowledgeFAQAnyList(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeFAQView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "knowledgeBaseId"},
		params.QueryFilter{ParamName: "question", Op: params.Like},
		params.QueryFilter{ParamName: "indexStatus"},
	).Desc("id")
	list, paging := services.KnowledgeFAQService.FindPageByCnd(cnd)
	results := make([]response.KnowledgeFAQResponse, 0, len(list))
	for _, item := range list {
		results = append(results, builders.BuildKnowledgeFAQ(&item))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func KnowledgeFAQGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeFAQView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	item := services.KnowledgeFAQService.Get(id)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("FAQ不存在"))
		return
	}
	httpx.WriteJSON(ctx, builders.BuildKnowledgeFAQ(item))
}

func KnowledgeFAQPostCreate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeFAQCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateKnowledgeFAQRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.KnowledgeFAQService.CreateKnowledgeFAQ(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildKnowledgeFAQ(item))
}

func KnowledgeFAQPostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeFAQUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateKnowledgeFAQRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.KnowledgeFAQService.UpdateKnowledgeFAQ(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func KnowledgeFAQPostDelete(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeFAQDelete); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.KnowledgeFAQService.DeleteKnowledgeFAQ(req.ID); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
