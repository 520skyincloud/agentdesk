package dashboard

import (
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func requirePlatformPermission(ctx *gin.Context, permission constants.Permission) (*dto.AuthPrincipal, error) {
	operator, err := services.AuthService.RequirePermission(ctx, permission)
	if err != nil {
		return nil, err
	}
	if !operator.IsPlatformAccount {
		return nil, errorsx.Forbidden("只有平台账号可以执行该操作")
	}
	return operator, nil
}
