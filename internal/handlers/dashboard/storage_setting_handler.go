package dashboard

import (
	"strings"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func StorageSettingGet(ctx *gin.Context) {
	if _, err := requirePlatformStoragePermission(ctx, constants.PermissionStorageSettingView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, buildStorageSettingResponse(services.GetStorageSetting()))
}

func StorageSettingPostUpdate(ctx *gin.Context) {
	operator, err := requirePlatformStoragePermission(ctx, constants.PermissionStorageSettingUpdate)
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

func requirePlatformStoragePermission(ctx *gin.Context, permission constants.Permission) (*dto.AuthPrincipal, error) {
	operator, err := services.AuthService.RequirePermission(ctx, permission)
	if err != nil {
		return nil, err
	}
	if !operator.IsPlatformAccount {
		return nil, errorsx.Forbidden("只有平台账号可以管理存储设置")
	}
	return operator, nil
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
