package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/sqls"
	"github.com/mlogclub/simple/web"
)

func QuickReplyAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQuickReplyView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "groupName"},
		params.QueryFilter{ParamName: "title", Op: params.Like},
	).Asc("sort_no").Desc("id")

	list, paging := services.QuickReplyService.FindPageInTenant(cnd, operator)
	results := builders.BuildQuickReplyResponses(list)
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func QuickReplyGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQuickReplyView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	item := services.QuickReplyService.GetInTenant(id, operator)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("快捷回复不存在"))
		return
	}
	httpx.WriteJSON(ctx, builders.BuildQuickReplyResponse(item))
}

func QuickReplyGetList_all(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQuickReplyView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list := services.QuickReplyService.FindInTenant(sqls.NewCnd().Eq("status", enums.StatusOk).Asc("sort_no").Desc("id"), operator)
	results := builders.BuildQuickReplyResponses(list)
	httpx.WriteJSON(ctx, results)
}

func QuickReplyPostCreate(ctx *gin.Context) {
	user, err := services.AuthService.RequirePermission(ctx, constants.PermissionQuickReplyCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.CreateQuickReplyRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.QuickReplyService.CreateQuickReply(req, user)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildQuickReplyResponse(item))
}

func QuickReplyPostUpdate(ctx *gin.Context) {
	user, err := services.AuthService.RequirePermission(ctx, constants.PermissionQuickReplyUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.UpdateQuickReplyRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.QuickReplyService.UpdateQuickReply(req, user); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func QuickReplyPostDelete(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionQuickReplyDelete)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.DeleteQuickReplyRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.QuickReplyService.DeleteQuickReply(req.ID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
