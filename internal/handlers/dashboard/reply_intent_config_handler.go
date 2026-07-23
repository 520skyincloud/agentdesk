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

func ReplyIntentConfigAnyList(ctx *gin.Context) {
	if _, err := requirePlatformPermission(ctx, constants.PermissionAIConfigView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "code", Op: params.Like},
		params.QueryFilter{ParamName: "name", Op: params.Like},
		params.QueryFilter{ParamName: "resourceType"},
		params.QueryFilter{ParamName: "intentProfileId", ColumnName: "intent_profile_id"},
	).Asc("sort_no").Asc("priority").Desc("id")
	list, paging := services.ReplyIntentConfigService.FindPageByCnd(cnd)
	httpx.WriteJSON(ctx, &web.PageResult{Results: builders.BuildReplyIntentConfigs(list), Page: paging})
}

func ReplyIntentConfigGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	if _, err := requirePlatformPermission(ctx, constants.PermissionAIConfigView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item := services.ReplyIntentConfigService.Get(id)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("意图配置不存在"))
		return
	}
	httpx.WriteJSON(ctx, builders.BuildReplyIntentConfig(item))
}

func ReplyIntentConfigPostCreate(ctx *gin.Context) {
	user, err := requirePlatformPermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateReplyIntentConfigRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ReplyIntentConfigService.CreateReplyIntentConfig(req, user)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildReplyIntentConfig(item))
}

func ReplyIntentConfigPostUpdate(ctx *gin.Context) {
	user, err := requirePlatformPermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateReplyIntentConfigRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ReplyIntentConfigService.UpdateReplyIntentConfig(req, user); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ReplyIntentConfigPostDelete(ctx *gin.Context) {
	user, err := requirePlatformPermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DeleteReplyIntentConfigRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ReplyIntentConfigService.DeleteReplyIntentConfig(req.ID, user); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
