package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func ReplyActionAnyList(ctx *gin.Context) {
	if _, err := requirePlatformPermission(ctx, constants.PermissionReplyActionView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildReplyActions(services.ReplyActionCatalogService.List()))
}

func ReplyActionPostUpdateStatus(ctx *gin.Context) {
	user, err := requirePlatformPermission(ctx, constants.PermissionReplyActionUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateReplyActionStatusRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ReplyActionCatalogService.SetEnabled(req.ID, req.Enabled, user); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
