package dashboard

import (
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func ConversationDispatchAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationHandover)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := readConversationDispatchListRequest(ctx)
	list, paging, err := services.ConversationDispatchWorkbenchService.ListTasks(req, operator, params.GetPaging(ctx))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: list, Page: paging})
}

func ConversationDispatchAnyStats(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationHandover)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.ConversationDispatchWorkbenchService.Stats(readConversationDispatchListRequest(ctx), operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func ConversationDispatchAnyAgent_loads(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationHandover)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	teamID, _ := params.GetInt64(ctx, "teamId")
	ret, err := services.ConversationDispatchWorkbenchService.ListAgentLoads(teamID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func ConversationDispatchPostAuto_assign(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationHandover)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ConversationDispatchAutoAssignRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationDispatchWorkbenchService.AutoAssign(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationDispatchPostAssign(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationHandover)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ConversationDispatchActionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationDispatchWorkbenchService.Assign(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationDispatchPostTransfer(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationHandover)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ConversationDispatchActionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationDispatchWorkbenchService.Transfer(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ConversationDispatchPostRelease(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationHandover)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ConversationDispatchActionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ConversationDispatchWorkbenchService.Release(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func readConversationDispatchListRequest(ctx *gin.Context) request.ConversationDispatchListRequest {
	teamID, _ := params.GetInt64(ctx, "teamId")
	assigneeID, _ := params.GetInt64(ctx, "assigneeId")
	status, _ := params.Get(ctx, "status")
	keyword, _ := params.Get(ctx, "keyword")
	onlyManageable, _ := params.GetBool(ctx, "onlyManageable")
	return request.ConversationDispatchListRequest{
		Status:         status,
		TeamID:         teamID,
		AssigneeID:     assigneeID,
		Keyword:        keyword,
		OnlyManageable: onlyManageable,
	}
}
