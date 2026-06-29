package api

import (
	"encoding/json"

	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
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
	httpx.WriteJSON(ctx, response.BuildWxWorkProtocolInstanceResponse(item))
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
	resp, err := services.WxWorkProtocolService.CheckLoginQRCode(item.ID)
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
