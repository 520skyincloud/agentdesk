package api

import (
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func CustomerPostSession_exchange(ctx *gin.Context) {
	channel := services.ChannelService.GetEnabledChannel(ctx)
	if channel == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("接入渠道不存在或已停用"))
		return
	}
	externalUser, err := openidentity.GetExternalUser(ctx, services.ChannelService.GetUserTokenSecret(channel))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.CustomerSessionService.Exchange(channel, *externalUser)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}
