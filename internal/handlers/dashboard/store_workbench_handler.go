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

func StoreWorkbenchGetCurrent(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreWorkbenchView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	snapshot, err := services.StoreWorkbenchService.Current(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreWorkbench(snapshot))
}

func StoreWorkbenchPostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreWorkbenchUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateStoreWorkbenchRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	snapshot, err := services.StoreWorkbenchService.UpdateCurrent(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreWorkbench(snapshot))
}

func StoreWorkbenchPostRoom_list(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreWorkbenchView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.StoreWorkbenchRoomListRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	snapshot, err := requireCurrentStoreWorkbenchInstance(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	raw, err := services.WxWorkProtocolService.GetRoomList(snapshot.WxWorkInstance.ID, req.StartIndex, req.Limit)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	rooms := parseWxWorkProtocolRoomOptions(raw)
	if len(rooms) > 0 {
		roomIDs := make([]string, 0, len(rooms))
		for _, room := range rooms {
			roomIDs = append(roomIDs, room.RoomID)
		}
		if detailRaw, detailErr := services.WxWorkProtocolService.BatchGetRoomDetail(snapshot.WxWorkInstance.ID, roomIDs); detailErr == nil {
			rooms = mergeWxWorkProtocolRoomDetails(rooms, detailRaw)
		}
	}
	httpx.WriteJSON(ctx, rooms)
}

func StoreWorkbenchPostRoom_member_list(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreWorkbenchView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.StoreWorkbenchRoomMemberListRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	snapshot, err := requireCurrentStoreWorkbenchInstance(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	detailRaw, err := services.WxWorkProtocolService.BatchGetRoomDetail(snapshot.WxWorkInstance.ID, []string{req.RoomID})
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	userIDs := extractWxWorkProtocolRoomMemberIDs(detailRaw)
	if len(userIDs) == 0 {
		httpx.WriteJSON(ctx, parseWxWorkProtocolRoomMemberOptions(detailRaw))
		return
	}
	memberRaw, err := services.WxWorkProtocolService.BatchGetRoomMemberDetail(snapshot.WxWorkInstance.ID, req.RoomID, userIDs)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, parseWxWorkProtocolRoomMemberOptions(memberRaw))
}

func requireCurrentStoreWorkbenchInstance(operator *dto.AuthPrincipal) (*services.StoreWorkbenchSnapshot, error) {
	snapshot, err := services.StoreWorkbenchService.Current(operator)
	if err != nil {
		return nil, err
	}
	if snapshot.Binding == nil {
		return nil, errorsx.InvalidParam("当前账号尚未绑定门店")
	}
	if snapshot.WxWorkInstance == nil {
		return nil, errorsx.InvalidParam("当前门店尚未绑定企微员工号")
	}
	return snapshot, nil
}
