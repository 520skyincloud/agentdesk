package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func TagAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list, paging, err := services.TagService.FindPageForOperator(params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "parentId"},
		params.QueryFilter{ParamName: "name", Op: params.Like},
	).Asc("sort_no").Desc("id"), operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	results := builders.BuildTagResponses(list)
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func TagGetList_all(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list, err := services.TagService.FindAllForOperator(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	results := builders.BuildTagTreeResponses(list)
	httpx.WriteJSON(ctx, results)
}

func TagGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	item, err := services.TagService.GetForOperator(id, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result := builders.BuildTagResponse(item)
	httpx.WriteJSON(ctx, &result)
}

func TagPostCreate(ctx *gin.Context) {
	user, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.CreateTagRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.TagService.CreateTag(req, user)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result := builders.BuildTagResponse(item)
	httpx.WriteJSON(ctx, &result)
}

func TagPostUpdate(ctx *gin.Context) {
	user, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.UpdateTagRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.TagService.UpdateTag(req, user); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func TagPostDelete(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagDelete)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.DeleteTagRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.TagService.DeleteTag(req.ID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func TagPostUpdate_sort(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	var ids []int64
	if err := params.ReadJSON(ctx, &ids); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.TagService.UpdateSort(ids, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func TagPostUpdate_status(ctx *gin.Context) {
	user, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.UpdateTagStatusRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.TagService.UpdateStatus(req.ID, req.Status, user); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
