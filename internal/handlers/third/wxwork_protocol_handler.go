package third

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func WxWorkProtocolAnyCallback(ctx *gin.Context) {
	requestID := httpx.GetRequestID(ctx)
	if ctx.Request.Method != http.MethodPost {
		logWxWorkProtocolCallbackFailure(ctx, requestID, 0, "method", http.StatusMethodNotAllowed)
		ctx.String(http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		logWxWorkProtocolCallbackFailure(ctx, requestID, 0, "read_body", http.StatusBadRequest)
		ctx.String(http.StatusBadRequest, "invalid callback")
		return
	}
	req := request.WxWorkProtocolCallbackRequest{}
	if err := json.Unmarshal(body, &req); err != nil {
		logWxWorkProtocolCallbackFailure(ctx, requestID, 0, "decode_json", http.StatusBadRequest)
		ctx.String(http.StatusBadRequest, "invalid callback")
		return
	}
	callbackToken := strings.TrimSpace(ctx.Query("t"))
	if callbackToken == "" {
		callbackToken = strings.TrimSpace(ctx.Query("token"))
	}
	if callbackToken == "" {
		callbackToken = strings.TrimSpace(ctx.GetHeader("X-AgentDesk-Callback-Token"))
	}
	if err := services.WxWorkProtocolService.HandleCallback(req, string(body), callbackToken); err != nil {
		status, stage := services.WxWorkProtocolCallbackErrorStatus(err)
		logWxWorkProtocolCallbackFailure(ctx, requestID, req.NotifyType, stage, status)
		ctx.String(status, "callback rejected")
		return
	}
	ctx.String(http.StatusOK, "success")
}

func logWxWorkProtocolCallbackFailure(ctx *gin.Context, requestID string, notifyType int, stage string, status int) {
	method := ""
	if ctx != nil && ctx.Request != nil {
		method = ctx.Request.Method
	}
	slog.Warn(
		"wxwork protocol callback failed",
		"method", method,
		"stage", stage,
		"request_id", requestID,
		"notify_type", notifyType,
		"http_status", status,
	)
}
