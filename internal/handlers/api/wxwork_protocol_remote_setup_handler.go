package api

import (
	"encoding/json"
	"time"

	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
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
	if store := services.StoreService.Get(ret.StoreID); store != nil {
		ret.StoreCode = store.StoreCode
		ret.StoreName = utils.RepairMojibakeText(store.Name)
		if ret.CompanyID == 0 {
			ret.CompanyID = store.CompanyID
		}
	}
	if company := services.CompanyService.Get(ret.CompanyID); company != nil {
		ret.CompanyName = utils.RepairMojibakeText(company.Name)
	}
	if job := services.FastGPTDatasetService.LatestJobByStore(ret.StoreID); job != nil {
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
	httpx.WriteJSON(ctx, buildRemoteLoginQRCodeResponse(item.ID, resp))
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

func buildRemoteLoginQRCodeResponse(instanceID int64, raw string) map[string]any {
	ret := map[string]any{"instanceId": instanceID, "rawResponse": raw}
	root := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return ret
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
	return ret
}
