package dashboard

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/sqls"
	"github.com/mlogclub/simple/web"
)

func AgentTeamAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentTeamView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "leaderUserId"},
		params.QueryFilter{ParamName: "name", Op: params.Like},
	).Desc("id")
	if _, ok := params.Get(ctx, "status"); !ok {
		cnd.Where("status <> ?", enums.StatusDeleted)
	}
	list := services.AgentTeamService.FindInTenant(cnd, operator)
	pendingReplyCounts := services.ConversationDispatchWorkbenchService.PendingReplyCountsByTeam(operator)
	results := make([]response.AgentTeamResponse, 0, len(list))
	for _, item := range list {
		results = append(results, buildAgentTeamResponse(&item, operator, pendingReplyCounts[item.ID]))
	}
	httpx.WriteJSON(ctx, results)
}

func AgentTeamGetList_all(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentTeamView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list := services.AgentTeamService.FindInTenant(sqls.NewCnd().Eq("status", enums.StatusOk), operator)
	pendingReplyCounts := services.ConversationDispatchWorkbenchService.PendingReplyCountsByTeam(operator)
	results := make([]response.AgentTeamResponse, 0, len(list))
	for _, item := range list {
		results = append(results, buildAgentTeamResponse(&item, operator, pendingReplyCounts[item.ID]))
	}
	httpx.WriteJSON(ctx, results)
}

func AgentTeamGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentTeamView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item := services.AgentTeamService.GetInTenant(id, operator)
	if item == nil || item.Status == enums.StatusDeleted {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("客服组不存在"))
		return
	}
	pendingReplyCount := services.ConversationDispatchWorkbenchService.PendingReplyCountsByTeam(operator)[item.ID]
	httpx.WriteJSON(ctx, buildAgentTeamResponse(item, operator, pendingReplyCount))
}

func AgentTeamPostCreate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentTeamCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateAgentTeamRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.AgentTeamService.CreateAgentTeam(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	pendingReplyCount := services.ConversationDispatchWorkbenchService.PendingReplyCountsByTeam(operator)[item.ID]
	httpx.WriteJSON(ctx, buildAgentTeamResponse(item, operator, pendingReplyCount))
}

func AgentTeamPostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentTeamUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateAgentTeamRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.AgentTeamService.UpdateAgentTeam(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func AgentTeamPostDelete(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentTeamDelete)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DeleteAgentTeamRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.AgentTeamService.DeleteAgentTeam(req.ID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func buildAgentTeamResponse(item *models.AgentTeam, operator *dto.AuthPrincipal, pendingReplyCount int) response.AgentTeamResponse {
	ret := response.AgentTeamResponse{
		ID:                     item.ID,
		Name:                   item.Name,
		LeaderUserID:           item.LeaderUserID,
		StoreStaffUserIDs:      services.AgentTeamService.FindStoreStaffUserIDsInTenant(item.ID, operator.ActiveTenantID),
		StoreScopeIDs:          utils.SplitInt64s(item.StoreScopeIDs),
		WxWorkInstanceScopeIDs: utils.SplitInt64s(item.WxWorkInstanceScopeIDs),
		DispatchMode:           item.DispatchMode,
		DispatchModeLabel:      enums.GetAgentTeamDispatchModeLabel(item.DispatchMode),
		Status:                 item.Status,
		Description:            item.Description,
		Remark:                 item.Remark,
		Manageable:             services.AgentTeamScopeService.CanManageTeam(operator, item.ID),
		PendingReplyCount:      pendingReplyCount,
		SquadCount:             services.AgentTeamSquadService.CountByTeamIDInTenant(item.ID, operator.ActiveTenantID),
	}
	if user := services.UserService.GetInScope(item.LeaderUserID, operator); user != nil {
		ret.LeaderUsername = user.Username
		ret.LeaderNickname = user.Nickname
	}
	return ret
}
