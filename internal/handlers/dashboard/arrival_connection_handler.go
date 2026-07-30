package dashboard

import (
	"strings"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func ArrivalConnectionAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionArrivalConnectionView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx).Where("status <> ?", enums.StatusDeleted).Desc("id")
	if keyword := strings.TrimSpace(ctx.Query("keyword")); keyword != "" {
		pattern := "%" + keyword + "%"
		cnd.Where("(name LIKE ? OR store_code LIKE ?)", pattern, pattern)
	}
	results, paging, err := services.ArrivalConnectionService.ListConnections(cnd, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func ArrivalConnectionGetAuthorizationOptions(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionArrivalConnectionManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	results, err := services.ArrivalConnectionService.ListAuthorizations(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, results)
}

func ArrivalConnectionGetProtocolInstanceOptions(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionArrivalConnectionManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	storeID, ok := params.GetInt64(ctx, "storeId")
	if !ok || storeID <= 0 {
		httpx.WriteJSON(ctx, errorsx.InvalidParam("门店不能为空"))
		return
	}
	results, err := services.ArrivalConnectionService.ListProtocolInstances(storeID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, results)
}

func ArrivalConnectionPostUpdateProvider(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionArrivalConnectionManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateArrivalConnectionProviderRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.ArrivalConnectionService.UpdateProvider(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func ArrivalConnectionPostCreateInvitation(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionArrivalConnectionInvite)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateArrivalInvitationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.ArrivalConnectionService.CreateInvitation(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func ArrivalConnectionPostVerify(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionArrivalConnectionManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.VerifyArrivalConnectionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.ArrivalConnectionService.VerifyConnection(req.ConnectionID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func ArrivalConnectionPostDisable(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionArrivalConnectionManage)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DisableArrivalConnectionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ArrivalConnectionService.DisableConnection(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ArrivalConnectionAnyAuditList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionArrivalAuditView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "storeId"},
		params.QueryFilter{ParamName: "action"},
		params.QueryFilter{ParamName: "result"},
	).Desc("id")
	results, paging, err := services.ArrivalConnectionService.AuditLogs(cnd, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}
