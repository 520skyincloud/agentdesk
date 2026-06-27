package dashboard

import (
	"encoding/json"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func WxWorkProtocolInstanceAnyList(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list, paging := services.WxWorkProtocolInstanceService.FindPageByCnd(params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "guid", Op: params.Like},
		params.QueryFilter{ParamName: "channelId"},
		params.QueryFilter{ParamName: "storeId"},
		params.QueryFilter{ParamName: "knowledgeBaseId"},
	).Where("status <> ?", enums.StatusDeleted).Desc("id"))
	results := make([]response.WxWorkProtocolInstanceResponse, 0, len(list))
	for _, item := range list {
		results = append(results, buildWxWorkProtocolInstanceResponse(&item))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func WxWorkProtocolInstanceGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item := services.WxWorkProtocolInstanceService.Get(id)
	if item == nil || item.Status == enums.StatusDeleted {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("企微员工号实例不存在"))
		return
	}
	httpx.WriteJSON(ctx, buildWxWorkProtocolInstanceResponse(item))
}

func WxWorkProtocolInstancePostCreate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
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
	httpx.WriteJSON(ctx, buildWxWorkProtocolInstanceResponse(item))
}

func WxWorkProtocolInstancePostStart_login(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
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
		Instance:      buildWxWorkProtocolInstanceResponse(item),
		RawResponse:   raw,
		QRCode:        qrcode,
		QRCodeContent: qrcodeContent,
		Key:           key,
	})
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
	if err := services.WxWorkProtocolInstanceService.UpdateInstance(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func WxWorkProtocolInstancePostDelete(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelDelete); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DeleteWxWorkProtocolInstanceRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.WxWorkProtocolInstanceService.DeleteInstance(req.ID); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func WxWorkProtocolInstancePostSet_notify_url(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SetWxWorkProtocolNotifyURLRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
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
	if err := services.WxWorkProtocolInstanceService.UpdateAISettings(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func WxWorkProtocolInstancePostLogin_qrcode(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolInstanceActionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.WxWorkProtocolService.GetLoginQRCode(req.ID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func WxWorkProtocolInstancePostCheck_login_qrcode(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CheckWxWorkProtocolLoginQRCodeRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
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
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.VerifyWxWorkProtocolLoginRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
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
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolSetProxyRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
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
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.AcceptWxWorkProtocolFriendRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.WxWorkProtocolService.AgreeContact(req.ID, req.Username, req.Scene)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func writeWxWorkProtocolActionResponse(ctx *gin.Context, action func(int64) (string, error)) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolInstanceActionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
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
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionConversationSend); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.InviteWxWorkProtocolRoomMemberRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.WxWorkProtocolService.InviteRoomMember(req.ID, req.RoomID, req.UserList); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func buildWxWorkProtocolInstanceResponse(item *models.WxWorkProtocolInstance) response.WxWorkProtocolInstanceResponse {
	ret := response.BuildWxWorkProtocolInstanceResponse(item)
	if item == nil {
		return ret
	}
	if channel := services.ChannelService.Get(item.ChannelID); channel != nil {
		ret.ChannelName = channel.Name
	}
	if store := services.StoreService.Get(item.StoreID); store != nil {
		ret.StoreCode = store.StoreCode
		ret.StoreName = store.Name
	}
	if knowledgeBase := services.KnowledgeBaseService.Get(item.KnowledgeBaseID); knowledgeBase != nil {
		ret.KnowledgeBaseName = knowledgeBase.Name
	}
	return ret
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
