package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func TenantAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTenantView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !operator.IsPlatformAccount {
		httpx.WriteJSON(ctx, errorsx.Forbidden("只有平台账号可以查看接入公司"))
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "verificationStatus"},
		params.QueryFilter{ParamName: "tenantCode", Op: params.Like},
		params.QueryFilter{ParamName: "legalName", Op: params.Like},
		params.QueryFilter{ParamName: "registrationNo", Op: params.Like},
	).Where("status <> ?", enums.StatusDeleted).Desc("id")
	list, paging := services.TenantService.FindPageByCnd(cnd)
	tenantIDs := make([]int64, 0, len(list))
	for i := range list {
		tenantIDs = append(tenantIDs, list[i].ID)
	}
	supervisors, err := services.TenantService.FindSupervisors(tenantIDs)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	stats, err := services.TenantService.FindOperationalStats(tenantIDs)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: builders.BuildTenantList(list, supervisors, stats), Page: paging})
}

func TenantGetBy(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTenantView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !operator.IsPlatformAccount {
		httpx.WriteJSON(ctx, errorsx.Forbidden("只有平台账号可以查看接入公司"))
		return
	}
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	item := services.TenantService.Get(id)
	if item == nil || item.Status == enums.StatusDeleted {
		httpx.WriteJSON(ctx, nil)
		return
	}
	supervisors, err := services.TenantService.FindSupervisors([]int64{id})
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	stats, err := services.TenantService.FindOperationalStats([]int64{id})
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	itemStats := stats[id]
	httpx.WriteJSON(ctx, builders.BuildTenant(item, builders.TenantBuildOptions{
		Supervisor: supervisors[id],
		Stats:      &itemStats,
	}))
}

func TenantPostCreate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTenantCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateTenantRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	result, err := services.TenantService.CreateTenant(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &response.CreateTenantResultResponse{
		Tenant:             builders.BuildTenant(result.Tenant, builders.TenantBuildOptions{Supervisor: result.Supervisor}),
		SupervisorUsername: result.Supervisor.Username,
		SupervisorPassword: result.SupervisorPassword,
		DefaultAgentTeamID: result.DefaultAgentTeam.ID,
		Invitation:         builders.BuildTenantInvitation(result.Invitation, result.Tenant, result.InvitationCode),
	})
}

func TenantPostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTenantUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateTenantRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err = services.TenantService.UpdateTenant(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func TenantPostUpdateStatus(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTenantUpdateStatus)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateTenantStatusRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err = services.TenantService.UpdateTenantStatus(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
