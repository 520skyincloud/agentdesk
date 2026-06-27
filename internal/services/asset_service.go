package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services/storage"
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
)

var AssetService = newAssetService()

func newAssetService() *assetService {
	return &assetService{}
}

type assetService struct {
}

func (s *assetService) Get(id int64) *models.Asset {
	return repositories.AssetRepository.Get(sqls.DB(), id)
}

func (s *assetService) GetByAssetID(assetID string) *models.Asset {
	return repositories.AssetRepository.GetByAssetID(sqls.DB(), strings.TrimSpace(assetID))
}

func (s *assetService) GetByStorageKey(storageKey string) *models.Asset {
	return repositories.AssetRepository.GetByStorageKey(sqls.DB(), strings.TrimSpace(storageKey))
}

func (s *assetService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Asset, paging *sqls.Paging) {
	return repositories.AssetRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *assetService) OpenReader(asset *models.Asset) (io.ReadCloser, error) {
	if asset == nil {
		return nil, errorsx.InvalidParam("图片资源不存在")
	}
	storageCfg := StorageSettingToConfig()
	switch asset.Provider {
	case "", enums.AssetProviderLocal:
		return storage.NewLocalStorage(storageCfg.Local).Read(asset.StorageKey)
	case enums.AssetProviderOSS:
		return storage.NewOSSStorage(storageCfg.OSS).Read(asset.StorageKey)
	default:
		return nil, errorsx.InvalidParam("当前暂不支持该存储类型的文件读取")
	}
}

func (s *assetService) UploadBytes(data []byte, prefix, filename string, principal *dto.AuthPrincipal) (*models.Asset, error) {
	src := bytes.NewReader(data)
	return s.Upload(src, storage.UploadInfo{
		Prefix:    prefix,
		Filename:  filename,
		FileSize:  int64(len(data)),
		MimeType:  http.DetectContentType(data),
		Principal: principal,
	})
}

func (s *assetService) UploadFile(file *multipart.FileHeader, prefix string, principal *dto.AuthPrincipal) (*models.Asset, error) {
	if file == nil {
		return nil, errorsx.InvalidParam("请选择上传文件")
	}

	storageCfg := StorageSettingToConfig()
	if file.Size > storageCfg.MaxUploadSizeBytes() {
		return nil, errorsx.InvalidParam("上传文件超过大小限制")
	}

	src, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()

	return s.Upload(src, storage.UploadInfo{
		Prefix:    prefix,
		Filename:  file.Filename,
		FileSize:  file.Size,
		MimeType:  file.Header.Get("Content-Type"),
		Principal: principal,
	})
}

func (s *assetService) Upload(reader io.Reader, info storage.UploadInfo) (*models.Asset, error) {
	storageCfg := StorageSettingToConfig()
	provider, err := storage.NewProviderWithConfig(storageCfg.Default, storageCfg)
	if err != nil {
		return nil, err
	}
	info.Prefix = applyStorageObjectPrefix(info.Prefix)

	assetID, key := storage.GenerateStorageKey(info)
	item := &models.Asset{
		AssetID:     assetID,
		Provider:    provider.ProviderType(),
		StorageKey:  key,
		Filename:    info.Filename,
		FileSize:    info.FileSize,
		MimeType:    info.MimeType,
		Status:      enums.AssetStatusPending,
		AuditFields: utils.BuildAuditFields(info.Principal),
	}
	if err := repositories.AssetRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}

	if _, err := provider.Upload(reader, key, storage.UploadInfo{
		Prefix:    info.Prefix,
		Filename:  info.Filename,
		FileSize:  info.FileSize,
		MimeType:  info.MimeType,
		Principal: info.Principal,
	}); err != nil {
		_ = s.markAssetStatus(item.ID, enums.AssetStatusFailed, info.Principal)
		return nil, err
	}

	item.Status = enums.AssetStatusSuccess
	_ = repositories.AssetRepository.UpdateColumn(sqls.DB(), item.ID, "status", enums.AssetStatusSuccess)

	return item, nil
}

func applyStorageObjectPrefix(prefix string) string {
	setting := GetStorageSetting()
	globalPrefix := strings.Trim(strings.TrimSpace(setting.OSSObjectPrefix), "/")
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if globalPrefix == "" {
		return prefix
	}
	if prefix == "" {
		return globalPrefix
	}
	return globalPrefix + "/" + prefix
}

func (s *assetService) RegisterExternal(prefix string, filename string, fileSize int64, mimeType string, externalURL string, principal *dto.AuthPrincipal) (*models.Asset, error) {
	provider := enums.AssetProviderLocal
	assetID, key := storage.GenerateStorageKey(storage.UploadInfo{
		Prefix:    prefix,
		Filename:  filename,
		FileSize:  fileSize,
		MimeType:  mimeType,
		Principal: principal,
	})
	if strings.TrimSpace(externalURL) != "" {
		key = strings.TrimSpace(externalURL)
	}
	item := &models.Asset{
		AssetID:     assetID,
		Provider:    provider,
		StorageKey:  key,
		Filename:    strings.TrimSpace(filename),
		FileSize:    fileSize,
		MimeType:    strings.TrimSpace(mimeType),
		Status:      enums.AssetStatusSuccess,
		AuditFields: utils.BuildAuditFields(principal),
	}
	if item.Filename == "" {
		item.Filename = s.buildFilenameFromMime(mimeType)
	}
	if err := repositories.AssetRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *assetService) GetSignedURL(id int64) (string, error) {
	item := s.Get(id)
	if item == nil {
		return "", errorsx.InvalidParam("文件不存在")
	}
	if item.Status != enums.AssetStatusSuccess {
		return "", errorsx.InvalidParam("文件不可访问")
	}

	provider, err := storage.NewProviderWithConfig(item.Provider, StorageSettingToConfig())
	if err != nil {
		return "", err
	}
	accessURL := provider.GetSignedURL(item.StorageKey)
	return accessURL, nil
}

func (s *assetService) DeleteAsset(id int64, principal *dto.AuthPrincipal) error {
	if principal == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := s.Get(id)
	if item == nil {
		return errorsx.InvalidParam("文件不存在")
	}
	return repositories.AssetRepository.Updates(sqls.DB(), id, map[string]any{
		"status":           enums.AssetStatusDeleted,
		"update_user_id":   principal.UserID,
		"update_user_name": principal.Username,
		"updated_at":       time.Now(),
	})
}

func (s *assetService) markAssetStatus(id int64, status enums.AssetStatus, principal *dto.AuthPrincipal) error {
	updates := map[string]any{
		"status":     status,
		"updated_at": time.Now(),
	}
	if principal != nil {
		updates["update_user_id"] = principal.UserID
		updates["update_user_name"] = principal.Username
	}
	return repositories.AssetRepository.Updates(sqls.DB(), id, updates)
}

func (s *assetService) buildFilenameFromMime(mimeType string) string {
	mimeType = strings.TrimSpace(strings.Split(mimeType, ";")[0])
	ext := ".bin"
	switch mimeType {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	case "application/pdf":
		ext = ".pdf"
	case "text/plain":
		ext = ".txt"
	}
	return "wxwork_" + strings.ReplaceAll(uuid.NewString(), "-", "") + ext
}
