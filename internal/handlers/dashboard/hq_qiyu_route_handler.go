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

func HQQiyuRouteGetConfig(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildHQQiyuRoute(services.HQQiyuRouteService.GetDefault()))
}

func HQQiyuRoutePostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateHQQiyuRouteRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.HQQiyuRouteService.Update(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildHQQiyuRoute(item))
}
