package dashboard

import (
	"context"

	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func StoreModelCredentialPostGet(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.GetStoreModelCredentialRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreModelCredentialService.GetManager(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreModelCredential(data))
}

func StoreModelCredentialPostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SubmitStoreModelCredentialRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreModelCredentialService.SubmitManager(ctx.Request.Context(), req, operator, storeCredentialRequestMeta(ctx))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreModelCredential(data))
}

func StoreModelCredentialPostApprove(ctx *gin.Context) {
	storeModelCredentialDecision(ctx, services.StoreModelCredentialService.Approve)
}

func StoreModelCredentialPostReject(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DecideStoreModelCredentialRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreModelCredentialService.Reject(req, operator, storeCredentialRequestMeta(ctx))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreModelCredential(data))
}

func StoreModelCredentialPostDisable(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DecideStoreModelCredentialRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreModelCredentialService.Disable(req, operator, storeCredentialRequestMeta(ctx))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreModelCredential(data))
}

func StoreModelCredentialPostAudit(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.GetStoreModelCredentialAuditRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreModelCredentialService.GetAudit(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreModelCredentialAuditList(data.Items))
}

func StoreModelCredentialPostPolicy(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateStoreCredentialPolicyRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err = services.StoreModelCredentialService.UpdatePolicy(req, operator, storeCredentialRequestMeta(ctx)); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func StoreWorkbenchGetModelCredential(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreWorkbenchView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreModelCredentialService.GetSelf(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreModelCredential(data))
}

func StoreWorkbenchPostUpdateModelCredential(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreWorkbenchUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	selfReq := request.SubmitSelfStoreModelCredentialRequest{}
	if err = params.ReadJSON(ctx, &selfReq); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreModelCredentialService.SubmitSelf(ctx.Request.Context(), request.SubmitStoreModelCredentialRequest{
		APIKey: selfReq.APIKey, CurrentPassword: selfReq.CurrentPassword, Confirmed: selfReq.Confirmed,
	}, operator, storeCredentialRequestMeta(ctx))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreModelCredential(data))
}

func storeModelCredentialDecision(
	ctx *gin.Context,
	decide func(context.Context, request.DecideStoreModelCredentialRequest, *dto.AuthPrincipal, services.StoreCredentialRequestMeta) (*services.StoreModelCredentialData, error),
) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DecideStoreModelCredentialRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := decide(ctx.Request.Context(), req, operator, storeCredentialRequestMeta(ctx))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreModelCredential(data))
}

func storeCredentialRequestMeta(ctx *gin.Context) services.StoreCredentialRequestMeta {
	return services.StoreCredentialRequestMeta{RequestID: httpx.GetRequestID(ctx), ClientIP: ctx.ClientIP()}
}
