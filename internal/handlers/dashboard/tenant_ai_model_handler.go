package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func TenantPostModelAccess(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionTenantModelGrantView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := struct {
		TenantID int64 `json:"tenantId"`
	}{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreAIModelSettingService.LoadAccess(req.TenantID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	_ = operator
	httpx.WriteJSON(ctx, builders.BuildTenantAIModelAccess(data.TenantID, 0, data.Grants, data.Assignments, data.Configs, data.UsageStats))
}

func TenantPostUpdateModelAccess(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionTenantModelGrantUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateTenantAIModelAccessRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err = services.StoreAIModelSettingService.UpdateTenantAccess(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreAIModelSettingService.LoadAccess(req.TenantID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildTenantAIModelAccess(data.TenantID, 0, data.Grants, data.Assignments, data.Configs, data.UsageStats))
}

func WxWorkProtocolInstancePostModelAssignments(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionTenantModelAssignmentView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateTenantAIModelAssignmentsRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err = requireActiveTenantForModelAssignment(operator, req.TenantID); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	instance := services.WxWorkProtocolInstanceService.GetByTenantID(req.WxWorkInstanceID, req.TenantID)
	if instance == nil {
		httpx.WriteJSON(ctx, errorsx.InvalidParam("企微员工号不存在或不属于当前接入公司"))
		return
	}
	data, err := services.StoreAIModelSettingService.LoadAccess(req.TenantID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildTenantAIModelAccess(data.TenantID, req.WxWorkInstanceID, data.Grants, data.Assignments, data.Configs, data.UsageStats))
}

func WxWorkProtocolInstancePostUpdateModelAssignments(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionTenantModelAssignmentUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateTenantAIModelAssignmentsRequest{}
	if err = params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err = requireActiveTenantForModelAssignment(operator, req.TenantID); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err = services.StoreAIModelSettingService.UpdateEmployeeAssignments(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	data, err := services.StoreAIModelSettingService.LoadAccess(req.TenantID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildTenantAIModelAccess(data.TenantID, req.WxWorkInstanceID, data.Grants, data.Assignments, data.Configs, data.UsageStats))
}

func requirePlatformPermission(ctx *gin.Context, permission constants.Permission) (*dto.AuthPrincipal, error) {
	operator, err := services.AuthService.RequirePermission(ctx, permission)
	if err != nil {
		return nil, err
	}
	if !operator.IsPlatformAccount {
		return nil, errorsx.Forbidden("只有平台账号可以管理模型授权与分配")
	}
	return operator, nil
}

func requireActiveTenantForModelAssignment(operator *dto.AuthPrincipal, tenantID int64) error {
	if operator == nil || operator.ActiveTenantID <= 0 {
		return errorsx.Forbidden("请先进入需要管理的接入公司")
	}
	if tenantID <= 0 || operator.ActiveTenantID != tenantID {
		return errorsx.Forbidden("模型分配必须在当前接入公司上下文内进行")
	}
	return nil
}
