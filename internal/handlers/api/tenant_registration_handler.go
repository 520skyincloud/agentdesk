package api

import (
	"strings"

	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func TenantRegistrationPostValidateInvite(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	req := request.ValidateTenantInvitationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.TenantRegistrationService.ValidateInvitation(req.InvitationCode, publicRegistrationMeta(ctx, httpx.GetRequestID(ctx)))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildTenantInvitationValidation(result))
}

func TenantRegistrationPostRegister(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	explicitRequestID := tracex.NormalizeRequestID(ctx.GetHeader(tracex.RequestIDHeader))
	if explicitRequestID == "" {
		httpx.WriteJSON(ctx, errorsx.InvalidParam("注册请求必须携带有效的 X-Request-Id"))
		return
	}
	req := request.RegisterTenantUserRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.TenantRegistrationService.Register(req, publicRegistrationMeta(ctx, explicitRequestID))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildTenantUserRegistration(result))
}

func publicRegistrationMeta(ctx *gin.Context, requestID string) services.PublicRegistrationMeta {
	return services.PublicRegistrationMeta{
		RequestID: requestID,
		ClientIP:  strings.TrimSpace(ctx.ClientIP()),
		UserAgent: strings.TrimSpace(ctx.GetHeader("User-Agent")),
	}
}
