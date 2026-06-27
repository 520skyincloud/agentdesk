package third

import (
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func WxWorkCLIPostInbound(ctx *gin.Context) {
	req := request.WxWorkCLIInboundRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.WxWorkCLIBridgeService.ConsumeInbound(req)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func WxWorkCLIPostOutboxPoll(ctx *gin.Context) {
	req := request.WxWorkCLIOutboxPollRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.WxWorkCLIBridgeService.PollOutbox(req)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func WxWorkCLIPostOutboxSent(ctx *gin.Context) {
	req := request.WxWorkCLIOutboxSentRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, services.WxWorkCLIBridgeService.MarkOutboxSent(req))
}

func WxWorkCLIPostOutboxFailed(ctx *gin.Context) {
	req := request.WxWorkCLIOutboxFailedRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, services.WxWorkCLIBridgeService.MarkOutboxFailed(req))
}
