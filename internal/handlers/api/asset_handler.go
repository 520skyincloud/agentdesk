package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/pkg/assetaccess"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func AssetGetFile(ctx *gin.Context) {
	assetID := strings.TrimSpace(ctx.Param("assetId"))
	if assetID == "" {
		httpx.WriteHttpStatusJSON(ctx, http.StatusBadRequest, web.JsonErrorMsg("assetId不能为空"))
		return
	}
	if strings.TrimSpace(ctx.Query("v")) != assetaccess.Version {
		httpx.WriteHttpStatusJSON(ctx, http.StatusForbidden, web.JsonErrorMsg("文件访问签名无效"))
		return
	}
	tenantID, tenantErr := strconv.ParseInt(strings.TrimSpace(ctx.Query("tenantId")), 10, 64)
	expires, expiresErr := strconv.ParseInt(strings.TrimSpace(ctx.Query("expires")), 10, 64)
	if tenantErr != nil || expiresErr != nil {
		httpx.WriteHttpStatusJSON(ctx, http.StatusForbidden, web.JsonErrorMsg("文件访问签名无效"))
		return
	}
	err := assetaccess.Verify(assetID, assetaccess.Claims{
		TenantID:  tenantID,
		Expires:   expires,
		Purpose:   ctx.Query("purpose"),
		Signature: ctx.Query("signature"),
	}, time.Now())
	if errors.Is(err, assetaccess.ErrExpired) {
		httpx.WriteHttpStatusJSON(ctx, http.StatusGone, web.JsonErrorMsg("文件访问地址已过期"))
		return
	}
	if err != nil {
		httpx.WriteHttpStatusJSON(ctx, http.StatusForbidden, web.JsonErrorMsg("文件访问签名无效"))
		return
	}
	asset := services.AssetService.GetByAssetIDInTenant(assetID, tenantID)
	if asset == nil {
		httpx.WriteHttpStatusJSON(ctx, http.StatusNotFound, web.JsonErrorMsg("文件不存在"))
		return
	}
	if asset.Status != enums.AssetStatusSuccess {
		httpx.WriteHttpStatusJSON(ctx, http.StatusNotFound, web.JsonErrorMsg("文件不可访问"))
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Header("Referrer-Policy", "no-referrer")
	reader, err := services.AssetService.OpenReader(asset)
	if err != nil {
		if strings.HasPrefix(asset.StorageKey, "http://") || strings.HasPrefix(asset.StorageKey, "https://") {
			ctx.Redirect(http.StatusFound, asset.StorageKey)
			return
		}
		httpx.WriteHttpStatusJSON(ctx, http.StatusNotFound, web.JsonErrorMsg("文件不可访问"))
		return
	}
	defer func() { _ = reader.Close() }()
	filename := strings.TrimSpace(asset.Filename)
	if filename == "" {
		filename = filepath.Base(asset.StorageKey)
	}
	contentType := strings.TrimSpace(asset.MimeType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctx.Header("Content-Disposition", `inline; filename="`+strings.ReplaceAll(filename, `"`, "")+`"`)
	ctx.DataFromReader(http.StatusOK, asset.FileSize, contentType, reader, nil)
}
