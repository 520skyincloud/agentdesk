package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func TenantRegistrationAnyList(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionTenantRegistrationView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	tenantContext, err := services.AuthService.RequireTenantContext(ctx)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "approvalStatus"},
		params.QueryFilter{ParamName: "username", Op: params.Like},
		params.QueryFilter{ParamName: "nickname", Op: params.Like},
	).Eq("tenant_id", tenantContext.TenantID).
		Eq("registration_source", enums.UserRegistrationSourceInvitation).
		Where("status <> ?", enums.StatusDeleted).
		Desc("id")
	if _, ok := params.Get(ctx, "approvalStatus"); !ok {
		cnd.Eq("approval_status", enums.UserApprovalStatusPending)
	}
	list, paging := services.UserService.FindPageByCnd(cnd)
	httpx.WriteJSON(ctx, &web.PageResult{Results: builders.BuildTenantRegistrationList(list), Page: paging})
}

func TenantRegistrationPostReview(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTenantRegistrationReview)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ReviewTenantRegistrationRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	user, err := services.TenantRegistrationService.Review(req, services.PublicRegistrationMeta{
		RequestID: httpx.GetRequestID(ctx), ClientIP: ctx.ClientIP(), UserAgent: ctx.GetHeader("User-Agent"),
	}, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildTenantRegistration(user))
}
