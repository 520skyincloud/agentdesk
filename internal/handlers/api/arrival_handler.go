package api

import (
	"net/http"
	"net/url"
	"strings"

	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func ArrivalPostBootstrap(ctx *gin.Context) {
	req := request.ArrivalBootstrapRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.ArrivalLinkService.Bootstrap(req)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func ArrivalGetStatus(ctx *gin.Context) {
	authorization := strings.TrimSpace(ctx.GetHeader("Authorization"))
	if len(authorization) < len("Bearer ") || !strings.EqualFold(authorization[:len("Bearer ")], "Bearer ") {
		httpx.WriteJSON(ctx, errorsx.InvalidToken("到店会话令牌无效"))
		return
	}
	result, err := services.ArrivalLinkService.Status(strings.TrimSpace(authorization[len("Bearer "):]))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func ArrivalGetContactWayQRCode(ctx *gin.Context) {
	raw, err := services.ArrivalLinkService.PublicQRCode(ctx.Param("resourceToken"))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, max-age=300")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Data(http.StatusOK, "image/png", raw)
}

func WeComProviderGetInvitation(ctx *gin.Context) {
	result, err := services.ArrivalConnectionService.ValidateInvitation(ctx.Query("token"))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func WeComProviderPostAuthorizationBegin(ctx *gin.Context) {
	req := request.BeginWeComProviderAuthorizationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.ArrivalConnectionService.BeginAuthorization(req.InvitationToken)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func WeComProviderGetAuthorizationCallback(ctx *gin.Context) {
	state := strings.TrimSpace(ctx.Query("state"))
	_, err := services.ArrivalConnectionService.CompleteAuthorization(state, ctx.Query("auth_code"))
	baseURL := strings.TrimRight(config.Current().Arrival.PublicBaseURL, "/") + "/wecom/provider/settings"
	query := url.Values{"state": []string{state}}
	if err != nil {
		query.Set("authorization", "failed")
	} else {
		query.Set("authorization", "completed")
	}
	ctx.Redirect(http.StatusFound, baseURL+"?"+query.Encode())
}

func WeComProviderGetOptions(ctx *gin.Context) {
	result, err := services.ArrivalConnectionService.ProviderOptions(ctx.Query("state"))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func WeComProviderPostComplete(ctx *gin.Context) {
	req := request.CompleteArrivalConnectionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.ArrivalConnectionService.CompleteConnection(req)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}
