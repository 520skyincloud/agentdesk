package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func AgentAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list, paging := services.AgentProfileService.FindPageInTenant(params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "userId"},
		params.QueryFilter{ParamName: "teamId"},
		params.QueryFilter{ParamName: "serviceStatus"},
		params.QueryFilter{ParamName: "agentCode", Op: params.Like},
		params.QueryFilter{ParamName: "displayName", Op: params.Like},
	).Desc("id"), operator)
	results := buildAgentProfileList(list, operator.ActiveTenantID)
	teamID, _ := params.GetInt64(ctx, "teamId")
	loads, err := services.ConversationDispatchWorkbenchService.ListAgentLoads(teamID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	loadByUserID := make(map[int64]response.ConversationDispatchAgentLoadResponse, len(loads))
	for _, load := range loads {
		loadByUserID[load.UserID] = load
	}
	for i := range results {
		load := loadByUserID[results[i].UserID]
		results[i].ActiveTaskCount = load.ActiveCount
		results[i].PendingReplyCount = load.PendingReplyCount
		results[i].ProcessingTaskCount = load.ProcessingCount
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func AgentGetList_all(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list := services.AgentProfileService.FindInTenant(params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "userId"},
		params.QueryFilter{ParamName: "teamId"},
		params.QueryFilter{ParamName: "serviceStatus"},
		params.QueryFilter{ParamName: "agentCode", Op: params.Like},
	).Desc("id"), operator)

	httpx.WriteJSON(ctx, buildAgentProfileList(list, operator.ActiveTenantID))
}

func AgentGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item := services.AgentProfileService.GetInTenant(id, operator)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("客服档案不存在"))
		return
	}
	httpx.WriteJSON(ctx, buildAgentProfile(item, operator.ActiveTenantID))
}

func AgentPostCreate(ctx *gin.Context) {
	user, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateAgentProfileRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.AgentProfileService.CreateAgentProfile(req, user)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, buildAgentProfile(item, user.ActiveTenantID))
}

func AgentPostUpdate(ctx *gin.Context) {
	user, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateAgentProfileRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.AgentProfileService.UpdateAgentProfile(req, user); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func AgentPostDelete(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAgentDelete)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DeleteAgentProfileRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.AgentProfileService.DeleteAgentProfile(req.ID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func buildAgentProfileList(items []models.AgentProfile, tenantID int64) []response.AgentProfileResponse {
	userIDs := make([]int64, 0, len(items))
	teamIDs := make([]int64, 0, len(items))
	for i := range items {
		userIDs = append(userIDs, items[i].UserID)
		teamIDs = append(teamIDs, items[i].TeamID)
	}
	users := services.UserService.FindByIdsInTenant(userIDs, tenantID)
	teams := services.AgentTeamService.FindByIdsInTenant(teamIDs, tenantID)
	return builders.BuildAgentProfileList(items, users, teams)
}

func buildAgentProfile(item *models.AgentProfile, tenantID int64) *response.AgentProfileResponse {
	if item == nil || item.TenantID != tenantID {
		return nil
	}
	user := services.UserService.GetInTenant(item.UserID, tenantID)
	team := services.AgentTeamService.FindByIdsInTenant([]int64{item.TeamID}, tenantID)
	var relatedTeam *models.AgentTeam
	if len(team) > 0 {
		relatedTeam = &team[0]
	}
	return builders.BuildAgentProfileResponse(item, user, relatedTeam)
}
