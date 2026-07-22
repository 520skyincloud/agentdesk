package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func ReplyIntentProfileAnyList(ctx *gin.Context) {
	if _, err := requireAIConfigPlatformAccess(ctx, constants.PermissionAIConfigView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "code", Op: params.Like},
		params.QueryFilter{ParamName: "name", Op: params.Like},
		params.QueryFilter{ParamName: "industryCode", ColumnName: "industry_code"},
	).Asc("sort_no").Asc("id")
	list, paging := services.ReplyIntentProfileService.FindPageByCnd(cnd)
	httpx.WriteJSON(ctx, &web.PageResult{Results: builders.BuildReplyIntentProfiles(list), Page: paging})
}

func ReplyIntentProfileGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	if _, err := requireAIConfigPlatformAccess(ctx, constants.PermissionAIConfigView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item := services.ReplyIntentProfileService.Get(id)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("意图行业配置不存在"))
		return
	}
	httpx.WriteJSON(ctx, builders.BuildReplyIntentProfile(item))
}

func ReplyIntentProfilePostCreate(ctx *gin.Context) {
	user, err := requireAIConfigPlatformAccess(ctx, constants.PermissionAIConfigCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateReplyIntentProfileRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ReplyIntentProfileService.CreateReplyIntentProfile(req, user)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildReplyIntentProfile(item))
}

func ReplyIntentProfilePostUpdate(ctx *gin.Context) {
	user, err := requireAIConfigPlatformAccess(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateReplyIntentProfileRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ReplyIntentProfileService.UpdateReplyIntentProfile(req, user); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ReplyIntentProfilePostDelete(ctx *gin.Context) {
	if _, err := requireAIConfigPlatformAccess(ctx, constants.PermissionAIConfigDelete); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DeleteReplyIntentProfileRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ReplyIntentProfileService.DeleteReplyIntentProfile(req.ID); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
