package third

import (
	"encoding/json"
	"io"

	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func WxWorkProtocolAnyCallback(ctx *gin.Context) {
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolCallbackRequest{}
	if err := json.Unmarshal(body, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, services.WxWorkProtocolService.HandleCallback(req, string(body)))
}
