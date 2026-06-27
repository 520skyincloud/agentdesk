package api

import (
	"net/http"
	"path/filepath"
	"strings"

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
	asset := services.AssetService.GetByAssetID(assetID)
	if asset == nil {
		httpx.WriteHttpStatusJSON(ctx, http.StatusNotFound, web.JsonErrorMsg("文件不存在"))
		return
	}
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
