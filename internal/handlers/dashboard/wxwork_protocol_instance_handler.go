package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
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
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "guid", Op: params.Like},
		params.QueryFilter{ParamName: "channelId"},
		params.QueryFilter{ParamName: "storeId"},
		params.QueryFilter{ParamName: "knowledgeBaseId"},
	).Where("status <> ?", enums.StatusDeleted).Where("health_status <> ?", "login_qrcode").Desc("id")
	cnd = services.AgentTeamScopeService.ApplyWxWorkInstanceFilter(cnd, operator)
	list, paging := services.WxWorkProtocolInstanceService.FindPageByCnd(cnd)
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

func WxWorkProtocolInstancePostResolve_login_binding(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
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

func WxWorkProtocolInstancePostInit_ai_agent(ctx *gin.Context) {
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
	item, err := services.WxWorkProtocolInstanceService.InitAIAgent(req.ID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, buildAIAgentResponse(item))
}

func WxWorkProtocolInstancePostUpdate_ai_agent(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateWxWorkProtocolAIAgentRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.WxWorkProtocolInstanceService.UpdateBoundAIAgent(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, buildAIAgentResponse(item))
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

func WxWorkProtocolInstancePostCreate_remote_setup(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
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
	resp := buildWxWorkProtocolInstanceResponse(item)
	resp.RemoteSetupURL = buildRemoteSetupURL(ctx, item.RemoteSetupToken)
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

func WxWorkProtocolInstancePostRoom_list(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolRoomListRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.WxWorkProtocolService.GetRoomList(req.ID, req.StartIndex, req.Limit)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, parseWxWorkProtocolRoomOptions(resp))
}

func WxWorkProtocolInstancePostRoom_member_detail(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolRoomMemberDetailRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.WxWorkProtocolService.BatchGetRoomMemberDetail(req.ID, req.RoomID, req.UserList)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, parseWxWorkProtocolRoomMemberOptions(resp))
}

func WxWorkProtocolInstancePostRoom_detail(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolRoomDetailRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
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
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.WxWorkProtocolSyncRoomInfoRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
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
		ret.ChannelName = utils.RepairMojibakeText(channel.Name)
	}
	if store := services.StoreService.Get(item.StoreID); store != nil {
		ret.StoreCode = store.StoreCode
		ret.StoreName = utils.RepairMojibakeText(store.Name)
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
	if aiAgent := services.AIAgentService.Get(item.AIAgentID); aiAgent != nil && aiAgent.Status != enums.StatusDeleted {
		ret.AIAgentName = utils.RepairMojibakeText(aiAgent.Name)
		ret.AIAgentConfigured = true
		if aiConfig := services.AIConfigService.Get(aiAgent.AIConfigID); aiConfig != nil {
			ret.AIConfigName = utils.RepairMojibakeText(aiConfig.Name)
		}
	}
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
		roomID := firstString(item, "room_id", "roomId", "roomid", "id", "chatroom", "conversation_id", "conversationId")
		roomID = strings.TrimSpace(strings.TrimPrefix(roomID, "R:"))
		if roomID == "" {
			continue
		}
		if _, ok := seen[roomID]; ok {
			continue
		}
		seen[roomID] = struct{}{}
		name := utils.RepairMojibakeText(firstString(item, "name", "room_name", "roomName", "nickname", "display_name", "title"))
		if name == "" {
			name = "群聊 " + roomID
		}
		ret = append(ret, response.WxWorkProtocolRoomOptionResponse{
			RoomID:         roomID,
			ConversationID: "R:" + roomID,
			Name:           name,
			Owner:          firstString(item, "owner", "owner_id", "ownerId", "admin", "create_user"),
			MemberCount:    intFromMap(item, "member_count", "memberCount", "member_num", "memberNum", "total"),
			Raw:            item,
		})
	}
	return ret
}

func parseWxWorkProtocolRoomMemberOptions(raw string) []response.WxWorkProtocolRoomMemberOptionResponse {
	items := collectWxWorkProtocolMaps(raw)
	ret := make([]response.WxWorkProtocolRoomMemberOptionResponse, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		userID := firstString(item, "user_id", "userId", "userid", "vid", "username", "id", "acctid")
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		name := utils.RepairMojibakeText(firstString(item, "name", "display_name", "displayName", "nickname", "nickName", "remark", "real_name", "realName"))
		if name == "" {
			name = userID
		}
		ret = append(ret, response.WxWorkProtocolRoomMemberOptionResponse{
			UserID: userID,
			Name:   name,
			Avatar: firstString(item, "avatar", "avatar_url", "avatarUrl", "head_img", "headImg", "head_url", "headUrl", "portrait"),
			Raw:    item,
		})
	}
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
			for _, key := range []string{"data", "list", "items", "result", "room_list", "roomList", "member_list", "memberList", "user_list", "userList", "members", "rooms"} {
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
	for _, key := range []string{"room_id", "roomId", "roomid", "chatroom", "conversation_id", "conversationId", "user_id", "userId", "userid", "vid", "username", "acctid"} {
		if strings.TrimSpace(fmt.Sprint(item[key])) != "" && strings.TrimSpace(fmt.Sprint(item[key])) != "<nil>" {
			return true
		}
	}
	return false
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
