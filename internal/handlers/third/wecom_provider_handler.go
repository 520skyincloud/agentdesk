package third

import (
	"io"
	"log/slog"
	"net/http"
	"strings"

	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func WeComProviderCommandCallback(ctx *gin.Context) {
	handleWeComProviderCallback(ctx, "command")
}

func WeComProviderDataCallback(ctx *gin.Context) {
	handleWeComProviderCallback(ctx, "data")
}

func handleWeComProviderCallback(ctx *gin.Context, kind string) {
	signature := firstCallbackQuery(ctx, "msg_signature", "msgSignature")
	timestamp := firstCallbackQuery(ctx, "timestamp", "timeStamp")
	nonce := strings.TrimSpace(ctx.Query("nonce"))
	if ctx.Request.Method == http.MethodGet {
		echo, err := services.WeComProviderCallbackService.VerifyURL(kind, signature, timestamp, nonce, ctx.Query("echostr"))
		if err != nil {
			logWeComProviderCallbackFailure(ctx, kind, services.WeComCallbackFailureStage(err))
			ctx.String(http.StatusBadRequest, "invalid callback")
			return
		}
		ctx.String(http.StatusOK, echo)
		return
	}
	body, err := io.ReadAll(io.LimitReader(ctx.Request.Body, 2<<20))
	if err != nil {
		logWeComProviderCallbackFailure(ctx, kind, "body_read")
		ctx.String(http.StatusBadRequest, "invalid callback")
		return
	}
	if err := services.WeComProviderCallbackService.Handle(kind, signature, timestamp, nonce, body); err != nil {
		logWeComProviderCallbackFailure(ctx, kind, services.WeComCallbackFailureStage(err))
		ctx.String(http.StatusInternalServerError, "retry")
		return
	}
	ctx.String(http.StatusOK, "success")
}

func logWeComProviderCallbackFailure(ctx *gin.Context, kind, stage string) {
	slog.Warn(
		"wecom provider callback rejected",
		"kind", strings.TrimSpace(kind),
		"method", ctx.Request.Method,
		"stage", strings.TrimSpace(stage),
		"requestId", httpx.GetRequestID(ctx),
	)
}

func firstCallbackQuery(ctx *gin.Context, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(ctx.Query(name)); value != "" {
			return value
		}
	}
	return ""
}
