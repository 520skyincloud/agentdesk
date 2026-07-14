package dashboard

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/sqls"
	"github.com/mlogclub/simple/web"
)

func RoleAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionRoleView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "code", Op: params.Like},
	).Asc("sort_no").Desc("id")
	list, paging := services.RoleService.FindPageByCnd(cnd)
	results := make([]response.RoleResponse, 0, len(list))
	for _, item := range list {
		results = append(results, buildRoleResponse(&item, operator))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func RoleGetList_all(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionRoleView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	list := services.RoleService.Find(sqls.NewCnd().Asc("sort_no").Desc("id"))
	results := make([]response.RoleResponse, 0, len(list))
	for _, item := range list {
		results = append(results, buildRoleResponse(&item, operator))
	}
	httpx.WriteJSON(ctx, results)
}

func RoleGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionRoleView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	item := services.RoleService.Get(id)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("角色不存在"))
		return
	}

	permissionCodes := make([]string, 0)
	list := services.RolePermissionService.Find(sqls.NewCnd().Eq("role_id", item.ID))
	for _, relation := range list {
		permission := services.PermissionService.Get(relation.PermissionID)
		if permission != nil {
			permissionCodes = append(permissionCodes, permission.Code)
		}
	}
	ret := buildRoleResponse(item, operator)
	ret.Permissions = permissionCodes
	httpx.WriteJSON(ctx, &ret)
}

func RolePostCreate(ctx *gin.Context) {
	operator, err := requireRolePlatformPermission(ctx, constants.PermissionRoleCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.CreateRoleRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	role, err := services.RoleService.CreateRole(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret := buildRoleResponse(role, operator)
	httpx.WriteJSON(ctx, &ret)
}

func RolePostUpdate(ctx *gin.Context) {
	operator, err := requireRolePlatformPermission(ctx, constants.PermissionRoleUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.UpdateRoleRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.RoleService.UpdateRole(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func RolePostDelete(ctx *gin.Context) {
	operator, err := requireRolePlatformPermission(ctx, constants.PermissionRoleDelete)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.DeleteRoleRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.RoleService.DeleteRole(req.ID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func RolePostUpdate_status(ctx *gin.Context) {
	operator, err := requireRolePlatformPermission(ctx, constants.PermissionRoleUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.UpdateRoleStatusRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.RoleService.UpdateStatus(req.ID, req.Status, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func RolePostAssign_permission(ctx *gin.Context) {
	operator, err := requireRolePlatformPermission(ctx, constants.PermissionRoleAssignPermission)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.AssignPermissionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.RoleService.AssignPermissions(req.RoleID, req.PermissionIDs, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func RolePostUpdate_sort(ctx *gin.Context) {
	operator, err := requireRolePlatformPermission(ctx, constants.PermissionRoleUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	var ids []int64
	if err := params.ReadJSON(ctx, &ids); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.RoleService.UpdateSort(ids, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func requireRolePlatformPermission(ctx *gin.Context, permission constants.Permission) (*dto.AuthPrincipal, error) {
	operator, err := services.AuthService.RequirePermission(ctx, permission)
	if err != nil {
		return nil, err
	}
	if !operator.IsPlatformAccount {
		return nil, errorsx.Forbidden("只有平台账号可以管理角色")
	}
	return operator, nil
}

func buildRoleResponse(item *models.Role, operator *dto.AuthPrincipal) response.RoleResponse {
	if item == nil {
		return response.RoleResponse{}
	}
	return response.RoleResponse{
		ID:             item.ID,
		Name:           item.Name,
		Code:           item.Code,
		Scope:          item.Scope,
		AuthorityLevel: item.AuthorityLevel,
		Status:         item.Status,
		IsSystem:       item.IsSystem,
		SortNo:         item.SortNo,
		Assignable:     services.RoleService.CanAssignRole(operator, item),
		Manageable:     services.RoleService.CanManageRole(operator, item),
	}
}
