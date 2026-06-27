package middleware

import (
	"agent-desk/internal/pkg/i18nx"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func AuthMiddleware(ctx *gin.Context) {
	if !authenticateRequest(ctx) {
		return
	}
	ctx.Next()
}

func authenticateRequest(ctx *gin.Context) bool {
	if _, err := services.AuthService.Authenticate(ctx); err != nil {
		result := web.JsonError(err)
		result.Message = i18nx.T(ctx, "error.auth.expired", nil)
		ctx.JSON(200, result)
		ctx.Abort()
		return false
	}
	return true
}
