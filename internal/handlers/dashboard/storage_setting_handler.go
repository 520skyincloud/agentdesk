package dashboard

import (
	"strings"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func StorageSettingGet(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionAssetView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, buildStorageSettingResponse(services.GetStorageSetting()))
}

func StorageSettingPostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAssetCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateStorageSettingRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	current := services.GetStorageSetting()
	secret := strings.TrimSpace(req.OSSAccessKeySecret)
	if secret == "" || secret == "********" {
		secret = current.OSSAccessKeySecret
	}
	setting, err := services.SaveStorageSetting(services.StorageSetting{
		DefaultProvider:    req.DefaultProvider,
		MaxUploadSizeMB:    req.MaxUploadSizeMB,
		LocalRoot:          req.LocalRoot,
		LocalBaseURL:       req.LocalBaseURL,
		OSSEndpoint:        req.OSSEndpoint,
		OSSBucket:          req.OSSBucket,
		OSSAccessKeyID:     req.OSSAccessKeyID,
		OSSAccessKeySecret: secret,
		OSSBaseURL:         req.OSSBaseURL,
		OSSObjectPrefix:    req.OSSObjectPrefix,
		OSSPrivate:         req.OSSPrivate,
		OSSSignedURLExpire: req.OSSSignedURLExpire,
		WECDNBaseURL:       req.WECDNBaseURL,
		PublicAssetBaseURL: req.PublicAssetBaseURL,
	}, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, buildStorageSettingResponse(setting))
}

func buildStorageSettingResponse(setting services.StorageSetting) response.StorageSettingResponse {
	return response.StorageSettingResponse{
		DefaultProvider:       setting.DefaultProvider,
		MaxUploadSizeMB:       setting.MaxUploadSizeMB,
		LocalRoot:             setting.LocalRoot,
		LocalBaseURL:          setting.LocalBaseURL,
		OSSEndpoint:           setting.OSSEndpoint,
		OSSBucket:             setting.OSSBucket,
		OSSAccessKeyID:        setting.OSSAccessKeyID,
		OSSAccessKeySecretSet: strings.TrimSpace(setting.OSSAccessKeySecret) != "",
		OSSBaseURL:            setting.OSSBaseURL,
		OSSObjectPrefix:       setting.OSSObjectPrefix,
		OSSPrivate:            setting.OSSPrivate,
		OSSSignedURLExpire:    setting.OSSSignedURLExpire,
		WECDNBaseURL:          setting.WECDNBaseURL,
		PublicAssetBaseURL:    setting.PublicAssetBaseURL,
	}
}
