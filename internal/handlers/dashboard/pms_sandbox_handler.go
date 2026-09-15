package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pms/sandbox"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func pmsSandboxPermission(ctx *gin.Context, storeID int64, permission constants.Permission) (*dto.AuthPrincipal, bool) {
	operator, err := services.AuthService.RequirePermission(ctx, permission)
	if err == nil {
		err = services.RequirePMSSandboxStore(operator, storeID)
	}
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return nil, false
	}
	return operator, true
}

func pmsSandboxWriteSnapshot(ctx *gin.Context, snapshot *sandbox.Snapshot, err error) {
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildPMSSandboxWorkspace(snapshot, config.PMSSandboxEnabled()))
}

func PMSSandboxGetStores(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionPMSSandboxView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	items, err := services.ListPMSSandboxStores(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, items)
}

func PMSSandboxGetList(ctx *gin.Context) {
	req := request.PMSSandboxStoreRequest{}
	if err := ctx.ShouldBindQuery(&req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, ok := pmsSandboxPermission(ctx, req.StoreID, constants.PermissionPMSSandboxView); !ok {
		return
	}
	result, err := services.PMSSandboxService.Snapshot(ctx.Request.Context(), req.StoreID)
	pmsSandboxWriteSnapshot(ctx, result, err)
}

func PMSSandboxGetCustomers(ctx *gin.Context) {
	req := request.PMSSandboxStoreRequest{}
	if err := ctx.ShouldBindQuery(&req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	operator, ok := pmsSandboxPermission(ctx, req.StoreID, constants.PermissionPMSSandboxView)
	if !ok {
		return
	}
	result, err := services.ListPMSSandboxCustomers(operator, req.StoreID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func PMSSandboxPostInitialize(ctx *gin.Context) {
	req := request.PMSSandboxStoreRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	operator, ok := pmsSandboxPermission(ctx, req.StoreID, constants.PermissionPMSSandboxManage)
	if !ok {
		return
	}
	result, err := services.PMSSandboxService.Initialize(ctx.Request.Context(), req.StoreID, operator.UserID, false)
	pmsSandboxWriteSnapshot(ctx, result, err)
}

func PMSSandboxPostReset(ctx *gin.Context) {
	req := request.PMSSandboxResetRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	operator, ok := pmsSandboxPermission(ctx, req.StoreID, constants.PermissionPMSSandboxManage)
	if !ok {
		return
	}
	result, err := services.PMSSandboxService.Reset(ctx.Request.Context(), req.StoreID, operator.UserID, req.DatasetID, req.Version)
	pmsSandboxWriteSnapshot(ctx, result, err)
}

func PMSSandboxPostUpdate(ctx *gin.Context) {
	req := request.PMSSandboxSaveRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	operator, ok := pmsSandboxPermission(ctx, req.StoreID, constants.PermissionPMSSandboxManage)
	if !ok {
		return
	}
	if req.Order != nil {
		if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionPMSSandboxExecute); err != nil {
			httpx.WriteJSON(ctx, err)
			return
		}
	}
	if req.Resource != nil {
		if err := services.ImportPMSSandboxResource(req.StoreID, req.Resource); err != nil {
			httpx.WriteJSON(ctx, err)
			return
		}
	}
	result, err := services.PMSSandboxService.Save(ctx.Request.Context(), req.StoreID, operator.UserID, req.SaveRequest)
	pmsSandboxWriteSnapshot(ctx, result, err)
}

func PMSSandboxPostBind(ctx *gin.Context) {
	req := request.PMSSandboxBindingRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	operator, ok := pmsSandboxPermission(ctx, req.StoreID, constants.PermissionPMSSandboxManage)
	if !ok {
		return
	}
	result, err := services.PMSSandboxService.Bind(ctx.Request.Context(), req.StoreID, operator.UserID, req.BindingRequest)
	pmsSandboxWriteSnapshot(ctx, result, err)
}

func PMSSandboxPostCancel(ctx *gin.Context) {
	req := request.PMSSandboxOperationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	operator, ok := pmsSandboxPermission(ctx, req.StoreID, constants.PermissionPMSSandboxExecute)
	if !ok {
		return
	}
	httpx.WriteJSON(ctx, services.CancelPMSSandboxDashboardOperation(ctx.Request.Context(), operator, req.StoreID, req.OperationID))
}
