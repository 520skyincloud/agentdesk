package api

import (
	"encoding/json"
	"strings"
	"time"

	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func WxWorkProtocolRemoteSetupGetByToken(ctx *gin.Context) {
	token := ctx.Param("token")
	item, err := services.WxWorkProtocolInstanceService.GetRemoteSetupByToken(token)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret := response.BuildWxWorkProtocolInstanceResponse(item)
	if store := services.StoreService.GetInTenant(ret.StoreID, item.TenantID); store != nil {
		ret.StoreCode = store.StoreCode
		ret.StoreName = utils.RepairMojibakeText(store.Name)
		ret.StoreAddress = utils.RepairMojibakeText(store.Address)
		ret.StoreNavigationName = utils.RepairMojibakeText(store.NavigationName)
		ret.StoreLongitude = store.Longitude
		ret.StoreLatitude = store.Latitude
		ret.StoreMapProvider = store.MapProvider
		ret.StoreContactPhone = utils.RepairMojibakeText(store.ContactPhone)
		ret.KnowledgeBaseID = store.KnowledgeBaseID
	}
	if binding := services.StoreStaffBindingService.GetInTenant(ret.StoreStaffBindingID, item.TenantID); binding != nil {
		ret.StoreStaffUserID = binding.UserID
		if user := services.UserService.GetByTenantID(binding.UserID, item.TenantID); user != nil {
			ret.StoreStaffUserName = utils.RepairMojibakeText(user.Nickname)
		}
	}
	runtime := services.StoreStaffBindingService.ResolveForInstance(item)
	ret.ManagedMode = runtime.ManagedMode
	ret.ServiceHours = runtime.ServiceHours
	ret.StoreRoomConversationID = runtime.StoreRoomConversationID
	ret.StoreRoomNotifyEnabled = runtime.StoreRoomNotifyEnabled
	ret.StoreRoomAtList = runtime.StoreRoomAtList
	ret.FallbackToHQ = runtime.FallbackToHQ
	ret.ManualTimeoutMinutes = runtime.ManualTimeoutMinutes
	if job := services.FastGPTDatasetService.LatestJobByStore(ret.StoreID, item.TenantID); job != nil {
		ret.KnowledgeProvisionStatus = job.Status
		ret.KnowledgeProvisionError = utils.RepairMojibakeText(job.LastError)
	}
	httpx.WriteJSON(ctx, ret)
}

func WxWorkProtocolRemoteSetupPostUpdate(ctx *gin.Context) {
	req := request.UpdateWxWorkProtocolRemoteSetupRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.WxWorkProtocolInstanceService.UpdateRemoteSetup(req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func WxWorkProtocolRemoteSetupPostSendEmailCode(ctx *gin.Context) {
	req := request.WxWorkProtocolRemoteSetupEmailCodeRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err := services.WxWorkProtocolInstanceService.GetRemoteSetupByToken(req.Token); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	challenge, err := services.EmailVerificationService.SendCode(
		ctx.Request.Context(),
		services.EmailVerificationPurposeRemoteSetup,
		req.Email,
		req.Token,
		ctx.ClientIP(),
		ctx.GetHeader("User-Agent"),
	)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &response.EmailCodeChallengeResponse{
		ExpiresAt:         challenge.ExpiresAt.Format(time.RFC3339),
		RetryAfterSeconds: challenge.RetryAfterSecond,
	})
}

func WxWorkProtocolRemoteSetupPostVerifyEmail(ctx *gin.Context) {
	req := request.WxWorkProtocolRemoteSetupVerifyEmailRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err := services.WxWorkProtocolInstanceService.GetRemoteSetupByToken(req.Token); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	verified, err := services.EmailVerificationService.VerifyCode(
		services.EmailVerificationPurposeRemoteSetup,
		req.Email,
		req.Token,
		req.Code,
	)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &response.EmailVerificationResponse{
		VerificationToken: verified.VerificationToken,
		ExpiresAt:         verified.ExpiresAt.Format(time.RFC3339),
	})
}

func WxWorkProtocolRemoteSetupPostLoginQrcode(ctx *gin.Context) {
	req := request.WxWorkProtocolRemoteSetupTokenRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.WxWorkProtocolInstanceService.GetRemoteSetupByToken(req.Token)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.WxWorkProtocolService.GetLoginQRCode(item.ID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	services.WxWorkProtocolService.ResetLoginVerificationAttempts(item.ID)
	result, err := buildRemoteLoginQRCodeResponse(item.ID, resp)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func WxWorkProtocolRemoteSetupPostCheckLogin(ctx *gin.Context) {
	req := request.WxWorkProtocolRemoteSetupTokenRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.WxWorkProtocolInstanceService.GetRemoteSetupByToken(req.Token)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.WxWorkProtocolService.CheckLoginQRCodeStatus(item.ID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func WxWorkProtocolRemoteSetupPostVerifyLogin(ctx *gin.Context) {
	req := request.VerifyWxWorkProtocolRemoteLoginRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.WxWorkProtocolInstanceService.GetRemoteSetupByToken(req.Token)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.WxWorkProtocolService.VerifyLoginQRCodeStatus(item.ID, req.Code)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func buildRemoteLoginQRCodeResponse(instanceID int64, raw string) (map[string]any, error) {
	ret := map[string]any{"instanceId": instanceID}
	root := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return nil, errorsx.BusinessError(0, "企微员工号协议未返回可展示的登录二维码")
	}
	data := root
	if nested, ok := root["data"].(map[string]any); ok {
		data = nested
	}
	for _, key := range []string{"qrcode", "qr_code", "qrCode"} {
		if value, ok := data[key].(string); ok && value != "" {
			ret["qrcode"] = value
			break
		}
	}
	for _, key := range []string{"qrcode_content", "qrcodeContent", "qr_code_content", "qrCodeContent"} {
		if value, ok := data[key].(string); ok && value != "" {
			ret["qrcodeContent"] = value
			break
		}
	}
	qrcode, _ := ret["qrcode"].(string)
	if strings.TrimSpace(qrcode) == "" {
		return nil, errorsx.BusinessError(0, "企微员工号协议未返回可展示的登录二维码")
	}
	return ret, nil
}
