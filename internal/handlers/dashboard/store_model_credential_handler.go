package dashboard

import (
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func StoreModelCredentialPostGet(ctx *gin.Context) {
	operator := services.AuthService.GetAuthPrincipal(ctx)
	req := request.StoreModelCredentialRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.StoreModelCredentialService.Get(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func StoreModelCredentialPostStores(ctx *gin.Context) {
	operator := services.AuthService.GetAuthPrincipal(ctx)
	ret, err := services.StoreModelCredentialService.ListStores(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func StoreModelCredentialPostUpdate(ctx *gin.Context) {
	operator := services.AuthService.GetAuthPrincipal(ctx)
	req := request.UpdateStoreModelCredentialRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.StoreModelCredentialService.Update(ctx.Request.Context(), req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func BillingQueryPostGet(ctx *gin.Context) {
	operator := services.AuthService.GetAuthPrincipal(ctx)
	req := request.BillingQueryRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.StoreModelCredentialService.QueryBilling(ctx.Request.Context(), req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}
