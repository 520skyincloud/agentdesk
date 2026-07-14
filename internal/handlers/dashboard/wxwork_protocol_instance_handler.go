package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func WxWorkProtocolInstanceAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkTenantContext(ctx) {
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "guid", Op: params.Like},
		params.QueryFilter{ParamName: "channelId"},
		params.QueryFilter{ParamName: "companyId"},
		params.QueryFilter{ParamName: "storeId"},
		params.QueryFilter{ParamName: "knowledgeBaseId"},
	).Where("status <> ?", enums.StatusDeleted).Where("health_status <> ?", "login_qrcode").Desc("id")
	cnd = services.AgentTeamScopeService.ApplyWxWorkInstanceFilter(cnd, operator)
	list, paging := services.WxWorkProtocolInstanceService.FindPageByCnd(cnd)
	results := make([]response.WxWorkProtocolInstanceResponse, 0, len(list))
	for _, item := range list {
		results = append(results, buildWxWorkProtocolInstanceResponse(&item, operator))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func WxWorkProtocolInstanceGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkTenantContext(ctx) {
		return
	}
	item := services.WxWorkProtocolInstanceService.GetInTenant(id, operator)
	if item == nil || item.Status == enums.StatusDeleted {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("企微员工号实例不存在"))
		return
	}
	if !services.AgentTeamScopeService.CanViewWxWorkInstance(operator, id) {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("无权访问该企微员工号实例"))
		return
	}
	httpx.WriteJSON(ctx, buildWxWorkProtocolInstanceResponse(item, operator))
}

func WxWorkProtocolInstancePostCreate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkTenantContext(ctx) {
		return
	}
	req := request.CreateWxWorkProtocolInstanceRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.WxWorkProtocolInstanceService.CreateInstance(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, buildWxWorkProtocolInstanceResponse(item, operator))
}

func WxWorkProtocolInstancePostStart_login(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkTenantContext(ctx) {
		return
	}
	req := request.StartWxWorkProtocolLoginRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.WxWorkProtocolInstanceService.CreateLoginInstance(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	raw, err := services.WxWorkProtocolService.GetLoginQRCode(item.ID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	qrcode, qrcodeContent, key := parseWxWorkProtocolLoginQRCode(raw)
	httpx.WriteJSON(ctx, response.StartWxWorkProtocolLoginResponse{
		Instance:      buildWxWorkProtocolInstanceResponse(item, operator),
		RawResponse:   raw,
		QRCode:        qrcode,
		QRCodeContent: qrcodeContent,
		Key:           key,
	})
}

func WxWorkProtocolInstancePostResolve_login_binding(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkTenantContext(ctx) {
		return
	}
	req := request.ResolveWxWorkProtocolLoginBindingRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.WxWorkProtocolInstanceService.ResolveLoginBinding(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func WxWorkProtocolInstancePostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateWxWorkProtocolInstanceRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	if err := services.WxWorkProtocolInstanceService.UpdateInstance(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func WxWorkProtocolInstancePostDelete(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelDelete)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DeleteWxWorkProtocolInstanceRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	if err := services.WxWorkProtocolInstanceService.DeleteInstance(req.ID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func WxWorkProtocolInstancePostSet_notify_url(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SetWxWorkProtocolNotifyURLRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	if err := services.WxWorkProtocolService.SetNotifyURL(req.ID, req.NotifyURL); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func WxWorkProtocolInstancePostSet_ai_reply_enabled(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SetWxWorkProtocolAIReplyEnabledRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	if err := services.WxWorkProtocolInstanceService.SetAIReplyEnabled(req.ID, req.Enabled, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func WxWorkProtocolInstancePostUpdate_ai_settings(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateWxWorkProtocolAISettingsRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	if err := services.WxWorkProtocolInstanceService.UpdateAISettings(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func WxWorkProtocolInstancePostStore_ai_model_settings(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateStoreAIModelSettingsRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireStoreAIModelScopeAccess(ctx, operator, req) {
		return
	}
	httpx.WriteJSON(ctx, services.StoreAIModelSettingService.ListResponses(req.CompanyID, req.StoreID, req.WxWorkInstanceID))
}

func WxWorkProtocolInstancePostUpdate_store_ai_model_settings(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateStoreAIModelSettingsRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireStoreAIModelScopeAccess(ctx, operator, req) {
		return
	}
	if err := services.StoreAIModelSettingService.UpdateStoreSettings(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, services.StoreAIModelSettingService.ListResponses(req.CompanyID, req.StoreID, req.WxWorkInstanceID))
}

func WxWorkProtocolInstancePostLogin_qrcode(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolInstanceActionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	resp, err := services.WxWorkProtocolService.GetLoginQRCode(req.ID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func WxWorkProtocolInstancePostCreate_remote_setup(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkTenantContext(ctx) {
		return
	}
	req := request.CreateWxWorkProtocolRemoteSetupRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.WxWorkProtocolInstanceService.CreateRemoteSetupInstance(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp := buildWxWorkProtocolInstanceResponse(item, operator)
	resp.RemoteSetupURL = buildRemoteSetupURL(ctx, item.RemoteSetupToken)
	httpx.WriteJSON(ctx, resp)
}

func WxWorkProtocolInstancePostCheck_login_qrcode(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CheckWxWorkProtocolLoginQRCodeRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	resp, err := services.WxWorkProtocolService.CheckLoginQRCode(req.ID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func WxWorkProtocolInstancePostVerify_login(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.VerifyWxWorkProtocolLoginRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	resp, err := services.WxWorkProtocolService.VerifyLoginQRCode(req.ID, req.Code)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func WxWorkProtocolInstancePostRecover(ctx *gin.Context) {
	writeWxWorkProtocolActionResponse(ctx, services.WxWorkProtocolService.RestoreClient)
}

func WxWorkProtocolInstancePostStop(ctx *gin.Context) {
	writeWxWorkProtocolActionResponse(ctx, services.WxWorkProtocolService.StopClient)
}

func WxWorkProtocolInstancePostLogout(ctx *gin.Context) {
	writeWxWorkProtocolActionResponse(ctx, services.WxWorkProtocolService.Logout)
}

func WxWorkProtocolInstancePostSync_profile(ctx *gin.Context) {
	writeWxWorkProtocolActionResponse(ctx, services.WxWorkProtocolService.SyncProfile)
}

func WxWorkProtocolInstancePostGet_corp_info(ctx *gin.Context) {
	writeWxWorkProtocolActionResponse(ctx, services.WxWorkProtocolService.GetCorpInfo)
}

func WxWorkProtocolInstancePostSet_proxy(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolSetProxyRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	resp, err := services.WxWorkProtocolService.SetProxy(req.ID, req.Proxy)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func WxWorkProtocolInstancePostSync_friend_requests(ctx *gin.Context) {
	writeWxWorkProtocolActionResponse(ctx, services.WxWorkProtocolService.SyncFriendRequests)
}

func WxWorkProtocolInstancePostAccept_friend_request(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.AcceptWxWorkProtocolFriendRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	resp, err := services.WxWorkProtocolService.AgreeContact(req.ID, req.Username, req.Scene)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func WxWorkProtocolInstancePostRoom_list(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolRoomListRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	resp, err := services.WxWorkProtocolService.GetRoomList(req.ID, req.StartIndex, req.Limit)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	rooms := parseWxWorkProtocolRoomOptions(resp)
	if len(rooms) > 0 {
		roomIDs := make([]string, 0, len(rooms))
		for _, room := range rooms {
			roomIDs = append(roomIDs, room.RoomID)
		}
		if detailResp, detailErr := services.WxWorkProtocolService.BatchGetRoomDetail(req.ID, roomIDs); detailErr == nil {
			rooms = mergeWxWorkProtocolRoomDetails(rooms, detailResp)
		}
	}
	httpx.WriteJSON(ctx, rooms)
}

func WxWorkProtocolInstancePostRoom_member_detail(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolRoomMemberDetailRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	if len(req.UserList) == 0 {
		if detailResp, err := services.WxWorkProtocolService.BatchGetRoomDetail(req.ID, []string{req.RoomID}); err == nil {
			req.UserList = extractWxWorkProtocolRoomMemberIDs(detailResp)
			if len(req.UserList) == 0 {
				members := parseWxWorkProtocolRoomMemberOptions(detailResp)
				if len(members) > 0 {
					httpx.WriteJSON(ctx, members)
					return
				}
			}
		}
	}
	resp, err := services.WxWorkProtocolService.BatchGetRoomMemberDetail(req.ID, req.RoomID, req.UserList)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, parseWxWorkProtocolRoomMemberOptions(resp))
}

func WxWorkProtocolInstancePostRoom_detail(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolRoomDetailRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	resp, err := services.WxWorkProtocolService.BatchGetRoomDetail(req.ID, req.RoomList)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, parseWxWorkProtocolRoomOptions(resp))
}

func WxWorkProtocolInstancePostSync_room_info(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolSyncRoomInfoRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	resp, err := services.WxWorkProtocolService.SyncRoomInfo(req.ID, req.RoomID, req.Version)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func writeWxWorkProtocolActionResponse(ctx *gin.Context, action func(int64) (string, error)) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolInstanceActionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	resp, err := action(req.ID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func WxWorkProtocolInstancePostInvite_room_member(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationSend)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.InviteWxWorkProtocolRoomMemberRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !requireWxWorkInstanceAccess(ctx, operator, req.ID) {
		return
	}
	if err := services.WxWorkProtocolService.InviteRoomMember(req.ID, req.RoomID, req.UserList); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func requireWxWorkTenantContext(ctx *gin.Context) bool {
	if _, err := services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return false
	}
	return true
}

func requireWxWorkInstanceAccess(ctx *gin.Context, operator *dto.AuthPrincipal, instanceID int64) bool {
	if !requireWxWorkTenantContext(ctx) {
		return false
	}
	item := services.WxWorkProtocolInstanceService.GetInTenant(instanceID, operator)
	if item == nil || item.Status == enums.StatusDeleted {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("企微员工号实例不存在"))
		return false
	}
	if !services.AgentTeamScopeService.CanViewWxWorkInstance(operator, instanceID) {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("无权访问该企微员工号实例"))
		return false
	}
	return true
}

func requireStoreAIModelScopeAccess(ctx *gin.Context, operator *dto.AuthPrincipal, req request.UpdateStoreAIModelSettingsRequest) bool {
	if !requireWxWorkTenantContext(ctx) {
		return false
	}
	if req.CompanyID > 0 {
		company := services.CompanyService.GetInTenant(req.CompanyID, operator)
		if company == nil || company.Status == enums.StatusDeleted {
			httpx.WriteJSON(ctx, web.JsonErrorMsg("公司不存在"))
			return false
		}
	}
	if req.StoreID > 0 {
		store := services.StoreService.GetInTenant(req.StoreID, operator.ActiveTenantID)
		if store == nil || store.Status == enums.StatusDeleted {
			httpx.WriteJSON(ctx, web.JsonErrorMsg("门店不存在"))
			return false
		}
		if req.CompanyID > 0 && store.CompanyID > 0 && store.CompanyID != req.CompanyID {
			httpx.WriteJSON(ctx, web.JsonErrorMsg("门店不属于所选公司"))
			return false
		}
	}
	if req.WxWorkInstanceID > 0 {
		if !requireWxWorkInstanceAccess(ctx, operator, req.WxWorkInstanceID) {
			return false
		}
		instance := services.WxWorkProtocolInstanceService.GetInTenant(req.WxWorkInstanceID, operator)
		if instance.CompanyID > 0 {
			company := services.CompanyService.GetInTenant(instance.CompanyID, operator)
			if company == nil || company.Status == enums.StatusDeleted {
				httpx.WriteJSON(ctx, web.JsonErrorMsg("企微员工号绑定的公司不属于当前接入公司"))
				return false
			}
		}
		if instance.StoreID > 0 {
			store := services.StoreService.GetInTenant(instance.StoreID, operator.ActiveTenantID)
			if store == nil || store.Status == enums.StatusDeleted {
				httpx.WriteJSON(ctx, web.JsonErrorMsg("企微员工号绑定的门店不属于当前接入公司"))
				return false
			}
			if instance.CompanyID > 0 && store.CompanyID > 0 && store.CompanyID != instance.CompanyID {
				httpx.WriteJSON(ctx, web.JsonErrorMsg("企微员工号绑定的公司与门店不一致"))
				return false
			}
		}
		if req.CompanyID > 0 && instance.CompanyID > 0 && instance.CompanyID != req.CompanyID {
			httpx.WriteJSON(ctx, web.JsonErrorMsg("企微员工号不属于所选公司"))
			return false
		}
		if req.StoreID > 0 && instance.StoreID > 0 && instance.StoreID != req.StoreID {
			httpx.WriteJSON(ctx, web.JsonErrorMsg("企微员工号不属于所选门店"))
			return false
		}
	}
	return true
}

func buildWxWorkProtocolInstanceResponse(item *models.WxWorkProtocolInstance, operator *dto.AuthPrincipal) response.WxWorkProtocolInstanceResponse {
	ret := response.BuildWxWorkProtocolInstanceResponse(item)
	if item == nil {
		return ret
	}
	if channel := services.ChannelService.GetInTenant(item.ChannelID, operator); channel != nil {
		ret.ChannelName = utils.RepairMojibakeText(channel.Name)
	}
	if store := services.StoreService.GetInTenant(item.StoreID, operator.ActiveTenantID); store != nil {
		ret.StoreCode = store.StoreCode
		ret.StoreName = utils.RepairMojibakeText(store.Name)
		if ret.CompanyID == 0 {
			ret.CompanyID = store.CompanyID
		}
	}
	if company := services.CompanyService.GetInTenant(ret.CompanyID, operator); company != nil {
		ret.CompanyName = utils.RepairMojibakeText(company.Name)
	}
	if runtime := services.StoreStaffBindingService.ResolveForInstance(item); runtime.ManagedMode != "" {
		ret.ManagedMode = runtime.ManagedMode
		if runtime.BindingID > 0 {
			ret.StoreStaffBindingID = runtime.BindingID
		}
	}
	if knowledgeBase := services.KnowledgeBaseService.Get(item.KnowledgeBaseID); knowledgeBase != nil {
		ret.KnowledgeBaseName = utils.RepairMojibakeText(knowledgeBase.Name)
	}
	stats := services.WxWorkProtocolInstanceService.CountStats(item.ID)
	ret.CustomerCount = stats.CustomerCount
	ret.ManualAttentionCount = stats.ManualAttentionCount
	ret.UrgentManualAttentionCount = stats.UrgentManualAttentionCount
	return ret
}

func buildRemoteSetupURL(ctx *gin.Context, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	scheme := "http"
	if ctx.Request.TLS != nil || strings.EqualFold(ctx.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := ctx.Request.Host
	if forwarded := strings.TrimSpace(ctx.GetHeader("X-Forwarded-Host")); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host + "/wxwork-remote-setup?token=" + token
}

func parseWxWorkProtocolLoginQRCode(raw string) (string, string, string) {
	root := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return "", "", ""
	}
	data := root
	if nested, ok := root["data"].(map[string]any); ok {
		data = nested
	}
	return firstString(data, "qrcode", "qr_code", "qrCode"), firstString(data, "qrcode_content", "qrcodeContent", "qr_code_content", "qrCodeContent"), firstString(data, "key")
}

func parseWxWorkProtocolRoomOptions(raw string) []response.WxWorkProtocolRoomOptionResponse {
	items := collectWxWorkProtocolMaps(raw)
	ret := make([]response.WxWorkProtocolRoomOptionResponse, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		roomID := normalizeWxWorkProtocolRoomID(firstString(item, "room_id", "roomId", "roomid", "roomID", "id", "chatroom", "chatroom_id", "chatroomId", "chat_id", "chatId", "conversation_id", "conversationId", "conversationID"))
		if roomID == "" {
			continue
		}
		if _, ok := seen[roomID]; ok {
			continue
		}
		seen[roomID] = struct{}{}
		name := utils.RepairMojibakeText(firstString(item, "name", "room_name", "roomName", "roomname", "room_title", "roomTitle", "nickname", "display_name", "displayName", "title", "remark", "roomname_remark"))
		if name == "" {
			name = "群聊 " + roomID + "（未命名）"
		}
		ret = append(ret, response.WxWorkProtocolRoomOptionResponse{
			RoomID:         roomID,
			ConversationID: "R:" + roomID,
			Name:           name,
			Owner:          firstString(item, "owner", "owner_id", "ownerId", "owner_vid", "ownerVid", "createuin", "admin", "create_user", "createUser", "creator"),
			MemberCount:    intFromMap(item, "member_count", "memberCount", "member_num", "memberNum", "member_cnt", "memberCnt", "total"),
			Raw:            item,
		})
	}
	return ret
}

func mergeWxWorkProtocolRoomDetails(rooms []response.WxWorkProtocolRoomOptionResponse, detailRaw string) []response.WxWorkProtocolRoomOptionResponse {
	if len(rooms) == 0 || strings.TrimSpace(detailRaw) == "" {
		return rooms
	}
	details := parseWxWorkProtocolRoomOptions(detailRaw)
	byID := make(map[string]response.WxWorkProtocolRoomOptionResponse, len(details))
	for _, item := range details {
		if item.RoomID != "" {
			byID[item.RoomID] = item
		}
	}
	for _, item := range parseWxWorkProtocolRoomDetailOverrides(detailRaw) {
		if item.RoomID != "" {
			byID[item.RoomID] = item
		}
	}
	ret := append([]response.WxWorkProtocolRoomOptionResponse(nil), rooms...)
	for i := range ret {
		detail, ok := byID[ret[i].RoomID]
		if !ok {
			continue
		}
		if strings.Contains(ret[i].Name, "（未命名）") && strings.TrimSpace(detail.Name) != "" && !strings.Contains(detail.Name, "（未命名）") {
			ret[i].Name = detail.Name
		}
		if strings.TrimSpace(ret[i].Owner) == "" {
			ret[i].Owner = detail.Owner
		}
		if ret[i].MemberCount == 0 && detail.MemberCount > 0 {
			ret[i].MemberCount = detail.MemberCount
		}
	}
	return ret
}

func parseWxWorkProtocolRoomDetailOverrides(raw string) []response.WxWorkProtocolRoomOptionResponse {
	root := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return nil
	}
	roomInfos, ok := nestedSlice(root, "data", "roominfos")
	if !ok {
		roomInfos, ok = nestedSlice(root, "data", "roomInfos")
	}
	if !ok {
		return nil
	}
	ret := make([]response.WxWorkProtocolRoomOptionResponse, 0, len(roomInfos))
	for _, item := range roomInfos {
		roomInfo, ok := item.(map[string]any)
		if !ok {
			continue
		}
		info, _ := roomInfo["info"].(map[string]any)
		roomID := normalizeWxWorkProtocolRoomID(firstString(info, "room_id", "roomId", "roomid", "roomID"))
		if roomID == "" {
			continue
		}
		name := utils.RepairMojibakeText(firstString(info, "room_name", "roomName", "roomname", "name"))
		members, _ := roomInfo["members"].([]any)
		if name == "" {
			for _, member := range members {
				memberMap, ok := member.(map[string]any)
				if !ok {
					continue
				}
				if candidate := utils.RepairMojibakeText(firstString(memberMap, "roomname_remark", "remark", "nickname", "name")); candidate != "" {
					name = candidate
					break
				}
			}
		}
		ret = append(ret, response.WxWorkProtocolRoomOptionResponse{
			RoomID:         roomID,
			ConversationID: "R:" + roomID,
			Name:           name,
			Owner:          firstString(info, "createuin", "owner", "owner_id", "ownerId", "owner_vid", "ownerVid"),
			MemberCount:    len(members),
		})
	}
	return ret
}

func nestedSlice(root map[string]any, keys ...string) ([]any, bool) {
	var cur any = root
	for _, key := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	items, ok := cur.([]any)
	return items, ok
}

func parseWxWorkProtocolRoomMemberOptions(raw string) []response.WxWorkProtocolRoomMemberOptionResponse {
	items := collectWxWorkProtocolMaps(raw)
	ret := make([]response.WxWorkProtocolRoomMemberOptionResponse, 0, len(items))
	seen := map[string]int{}
	for _, item := range items {
		if info, ok := item["info"].(map[string]any); ok {
			merged := make(map[string]any, len(info)+len(item))
			for key, value := range item {
				merged[key] = value
			}
			for key, value := range info {
				merged[key] = value
			}
			item = merged
		}
		userID := firstString(item, "user_id", "userId", "userid", "vid", "uin", "username", "id", "acctid")
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		realName := utils.RepairMojibakeText(firstString(item, "realname", "real_name", "realName", "name"))
		displayName := utils.RepairMojibakeText(firstString(item, "display_name", "displayName", "nickname", "nickName"))
		roomRemark := utils.RepairMojibakeText(firstString(item, "roomname_remark", "roomNameRemark", "remark"))
		accountID := strings.TrimSpace(firstString(item, "acctid", "acct_id", "account_id", "accountId"))
		name := firstNonEmptyWxWorkProtocolMemberString(realName, displayName, roomRemark, accountID)
		if name == "" {
			name = userID
		}
		member := response.WxWorkProtocolRoomMemberOptionResponse{
			UserID:      userID,
			Name:        name,
			DisplayName: displayName,
			RealName:    realName,
			RoomRemark:  roomRemark,
			AccountID:   accountID,
			Avatar:      firstString(item, "avatar", "avatar_url", "avatarUrl", "iconurl", "head_img", "headImg", "head_url", "headUrl", "portrait"),
			Raw:         item,
		}
		if index, ok := seen[userID]; ok {
			ret[index] = mergeWxWorkProtocolRoomMemberOption(ret[index], member)
			continue
		}
		seen[userID] = len(ret)
		ret = append(ret, member)
	}
	return ret
}

func mergeWxWorkProtocolRoomMemberOption(current, next response.WxWorkProtocolRoomMemberOptionResponse) response.WxWorkProtocolRoomMemberOptionResponse {
	if next.RealName != "" {
		current.RealName = next.RealName
	}
	if next.DisplayName != "" {
		current.DisplayName = next.DisplayName
	}
	if next.RoomRemark != "" {
		current.RoomRemark = next.RoomRemark
	}
	if next.AccountID != "" {
		current.AccountID = next.AccountID
	}
	if next.Avatar != "" {
		current.Avatar = next.Avatar
	}
	if name := firstNonEmptyWxWorkProtocolMemberString(current.RealName, current.DisplayName, current.RoomRemark, current.AccountID); name != "" {
		current.Name = name
	}
	if next.Raw != nil {
		current.Raw = next.Raw
	}
	return current
}

func firstNonEmptyWxWorkProtocolMemberString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func extractWxWorkProtocolRoomMemberIDs(raw string) []string {
	items := collectWxWorkProtocolMaps(raw)
	ret := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	appendID := func(userID string) {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			return
		}
		if _, ok := seen[userID]; ok {
			return
		}
		seen[userID] = struct{}{}
		ret = append(ret, userID)
	}
	for _, item := range items {
		if info, ok := item["info"].(map[string]any); ok {
			appendID(firstString(info, "user_id", "userId", "userid", "vid", "uin", "username", "acctid", "member_id", "memberId"))
		}
		appendID(firstString(item, "user_id", "userId", "userid", "vid", "uin", "username", "acctid", "member_id", "memberId"))
	}
	var root any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return ret
	}
	var walk func(value any, parentKey string)
	walk = func(value any, parentKey string) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				walk(item, parentKey)
			}
		case map[string]any:
			for key, item := range typed {
				walk(item, key)
			}
		case string:
			key := strings.ToLower(parentKey)
			if strings.Contains(key, "member") || strings.Contains(key, "user") {
				appendID(typed)
			}
		}
	}
	walk(root, "")
	return ret
}

func collectWxWorkProtocolMaps(raw string) []map[string]any {
	root := any(nil)
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return nil
	}
	ret := make([]map[string]any, 0, 16)
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			if looksLikeWxWorkRoomOrMember(typed) {
				ret = append(ret, typed)
			}
			for _, key := range []string{"data", "list", "items", "result", "roomdata", "roomData", "datas", "roominfos", "roomInfos", "persons", "person", "info", "room", "room_info", "roomInfo", "room_list", "roomList", "member_list", "memberList", "user_list", "userList", "members", "member_ids", "memberIds", "memberIDList", "member_id_list", "rooms", "chatrooms", "chat_rooms", "chatRooms"} {
				if nested, ok := typed[key]; ok {
					walk(nested)
				}
			}
		}
	}
	walk(root)
	return ret
}

func looksLikeWxWorkRoomOrMember(item map[string]any) bool {
	for _, key := range []string{"room_id", "roomId", "roomid", "roomID", "chatroom", "chatroom_id", "chatroomId", "chat_id", "chatId", "conversation_id", "conversationId", "conversationID", "user_id", "userId", "userid", "vid", "uin", "username", "acctid"} {
		if strings.TrimSpace(fmt.Sprint(item[key])) != "" && strings.TrimSpace(fmt.Sprint(item[key])) != "<nil>" {
			return true
		}
	}
	return false
}

func normalizeWxWorkProtocolRoomID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "R:")
	value = strings.TrimPrefix(value, "r:")
	return strings.TrimSpace(value)
}

func intFromMap(data map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := data[key].(type) {
		case float64:
			return int(value)
		case int:
			return value
		case string:
			var n int
			if _, err := fmt.Sscanf(value, "%d", &n); err == nil {
				return n
			}
		}
	}
	return 0
}

func stringFromAny(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func firstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if s := stringFromAny(data[key]); s != "" {
			return s
		}
	}
	return ""
}
