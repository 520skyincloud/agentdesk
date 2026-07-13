package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"
	"strconv"
	"strings"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/sqls"
	"github.com/mlogclub/simple/web"
)

func UserAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionUserView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "username", Op: params.Like},
		params.QueryFilter{ParamName: "nickname", Op: params.Like},
	).Desc("id")
	cnd.Where("status <> ?", enums.StatusDeleted)
	applyUserRoleFilter(ctx, cnd)
	applyStoreStaffAgentTeamFilter(ctx, cnd)
	services.UserService.ApplyTenantScope(cnd, operator)
	list, paging := services.UserService.FindPageByCnd(cnd)
	results := builders.BuildUserList(list, builders.UserBuildOptions{
		Roles:                 true,
		Permissions:           false,
		StoreStaffAssignments: services.StoreStaffBindingService.FindUserAssignments(userIDs(list)),
		Operator:              operator,
	})
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func UserAnyList_all(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionUserView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	cnd := params.NewSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "username", Op: params.Like},
		params.QueryFilter{ParamName: "nickname", Op: params.Like},
	).Desc("id")
	cnd.Where("status <> ?", enums.StatusDeleted)
	applyUserRoleFilter(ctx, cnd)
	applyStoreStaffAgentTeamFilter(ctx, cnd)
	services.UserService.ApplyTenantScope(cnd, operator)

	list := services.UserService.Find(cnd)
	results := builders.BuildUserList(list, builders.UserBuildOptions{
		Roles:                 true,
		Permissions:           false,
		StoreStaffAssignments: services.StoreStaffBindingService.FindUserAssignments(userIDs(list)),
		Operator:              operator,
	})
	httpx.WriteJSON(ctx, results)
}

func applyUserRoleFilter(ctx *gin.Context, cnd *sqls.Cnd) {
	roleCode := strings.TrimSpace(ctx.Query("roleCode"))
	if roleCode == "" {
		return
	}
	cnd.Where("id IN (SELECT ur.user_id FROM t_user_role ur JOIN t_role r ON r.id = ur.role_id WHERE r.code = ? AND r.status = ?)", roleCode, enums.StatusOk)
}

func applyStoreStaffAgentTeamFilter(ctx *gin.Context, cnd *sqls.Cnd) {
	raw := strings.TrimSpace(ctx.Query("agentTeamId"))
	if raw == "" || raw == "all" {
		return
	}
	teamID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || teamID < 0 {
		return
	}
	if teamID == 0 {
		cnd.Where("id IN (SELECT ur.user_id FROM t_user_role ur JOIN t_role r ON r.id = ur.role_id WHERE r.code = ? AND r.status = ?)", constants.RoleCodeStoreStaff, enums.StatusOk)
		cnd.Where("id NOT IN (SELECT user_id FROM t_store_staff_binding WHERE status <> ? AND agent_team_id > 0)", enums.StatusDeleted)
		return
	}
	cnd.Where("id IN (SELECT user_id FROM t_store_staff_binding WHERE status <> ? AND agent_team_id = ?)", enums.StatusDeleted, teamID)
}

func userIDs(list []models.User) []int64 {
	ids := make([]int64, 0, len(list))
	for i := range list {
		ids = append(ids, list[i].ID)
	}
	return ids
}

func UserGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionUserView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	item := services.UserService.GetInScope(id, operator)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("用户不存在"))
		return
	}
	httpx.WriteJSON(ctx, builders.BuildUserResponse(item, builders.UserBuildOptions{
		Roles:                 true,
		Permissions:           true,
		StoreStaffAssignments: services.StoreStaffBindingService.FindUserAssignments([]int64{item.ID}),
		Operator:              operator,
	}))
}

func UserPostBind_agent_team(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentTeamUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.BindStoreStaffAgentTeamRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.AgentTeamService.BindStoreStaffUser(req.UserID, req.TeamID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func UserPostCreate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionUserCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.CreateUserRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if len(req.RoleIDs) > 0 && !services.AuthService.HasPermission(ctx, constants.PermissionUserAssignRole.Code) {
		httpx.WriteJSON(ctx, errorsx.Forbidden("无权限在创建账号时分配角色"))
		return
	}
	user, generatedPassword, err := services.UserService.CreateUser(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &response.CreateUserResultResponse{
		User:     builders.BuildUserResponse(user, builders.UserBuildOptions{Roles: true, Permissions: true, Operator: operator}),
		Password: generatedPassword,
	})
}

func UserPostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionUserUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.UpdateUserRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.UserService.UpdateUser(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func UserPostDelete(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionUserDelete)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.DeleteUserRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.UserService.DeleteUser(req.ID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func UserPostUpdate_status(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionUserUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.UpdateUserStatusRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.UserService.UpdateStatus(req.ID, req.Status, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func UserPostReset_password(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionUserUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	var req struct {
		UserID int64 `json:"userId"`
	}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	password, err := services.UserService.ResetPassword(req.UserID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, map[string]any{
		"password": password,
	})
}

func UserPostChange_password(ctx *gin.Context) {
	principal := services.AuthService.GetAuthPrincipal(ctx)
	if principal == nil {
		if _, err := services.AuthService.Authenticate(ctx); err != nil {
			httpx.WriteJSON(ctx, err)
			return
		}
		principal = services.AuthService.GetAuthPrincipal(ctx)
	}
	if principal == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("未登录或登录已过期"))
		return
	}

	req := request.ChangePasswordRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.UserService.ChangeOwnPassword(req.Password, principal); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func UserPostAssign_role(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionUserAssignRole)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.AssignRoleRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.UserService.AssignRoles(req.UserID, req.RoleIDs, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
