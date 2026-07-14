package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/assetaccess"
	"agent-desk/internal/pkg/dto/response"
	"log/slog"
)

func BuildAsset(item *models.Asset) response.AssetResponse {
	ret := response.AssetResponse{
		ID:             item.ID,
		AssetID:        item.AssetID,
		Provider:       item.Provider,
		Filename:       item.Filename,
		FileSize:       item.FileSize,
		MimeType:       item.MimeType,
		StorageKey:     "",
		Status:         item.Status,
		CreatedAt:      item.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:      item.UpdatedAt.Format("2006-01-02 15:04:05"),
		CreateUserID:   item.CreateUserID,
		CreateUserName: item.CreateUserName,
		UpdateUserID:   item.UpdateUserID,
		UpdateUserName: item.UpdateUserName,
	}

	if accessURL, err := assetaccess.BuildRelativeURL(item.AssetID, item.TenantID, assetaccess.PurposeInline); err != nil {
		slog.Error("build asset access url failed", "assetId", item.AssetID, "tenantId", item.TenantID, "error", err)
	} else {
		ret.URL = accessURL
	}

	return ret
}
