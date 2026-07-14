package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func TenantInvitationGetCurrent(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTenantInviteView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	tenantContext, err := services.AuthService.RequireTenantContext(ctx)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	invitation, code, err := services.TenantInvitationService.Current(tenantContext.TenantID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	tenant := services.TenantService.Get(tenantContext.TenantID)
	httpx.WriteJSON(ctx, builders.BuildTenantInvitation(invitation, tenant, code))
}

func TenantInvitationPostRotate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTenantInviteRotate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	tenantContext, err := services.AuthService.RequireTenantContext(ctx)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	invitation, code, err := services.TenantInvitationService.Rotate(tenantContext.TenantID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	tenant := services.TenantService.Get(tenantContext.TenantID)
	httpx.WriteJSON(ctx, builders.BuildTenantInvitation(invitation, tenant, code))
}
