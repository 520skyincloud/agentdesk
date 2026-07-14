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
	"strconv"
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

func (s *assetService) GetInTenant(id, tenantID int64) *models.Asset {
	return repositories.AssetRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *assetService) GetByAssetID(assetID string) *models.Asset {
	return repositories.AssetRepository.GetByAssetID(sqls.DB(), strings.TrimSpace(assetID))
}

func (s *assetService) GetByAssetIDInTenant(assetID string, tenantID int64) *models.Asset {
	return repositories.AssetRepository.GetByAssetIDInTenant(sqls.DB(), strings.TrimSpace(assetID), tenantID)
}

func (s *assetService) GetByStorageKey(storageKey string) *models.Asset {
	return repositories.AssetRepository.GetByStorageKey(sqls.DB(), strings.TrimSpace(storageKey))
}

func (s *assetService) FindPageByCnd(cnd *sqls.Cnd) (list []models.Asset, paging *sqls.Paging) {
	return repositories.AssetRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *assetService) FindPageByCndInTenant(cnd *sqls.Cnd, tenantID int64) (list []models.Asset, paging *sqls.Paging) {
	if cnd == nil {
		cnd = sqls.NewCnd().Page(1, 20)
	} else if cnd.Paging == nil {
		cnd.Page(1, 20)
	}
	if tenantID <= 0 {
		return repositories.AssetRepository.FindPageByCnd(sqls.DB(), cnd.Where("1 = 0"))
	}
	return repositories.AssetRepository.FindPageByCnd(sqls.DB(), cnd.Eq("tenant_id", tenantID))
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
	return s.UploadBytesInTenant(data, prefix, filename, s.tenantIDForPrincipal(principal), principal)
}

func (s *assetService) UploadBytesInTenant(data []byte, prefix, filename string, tenantID int64, principal *dto.AuthPrincipal) (*models.Asset, error) {
	if tenantID <= 0 {
		return nil, errorsx.InvalidParam("文件缺少接入公司归属")
	}
	src := bytes.NewReader(data)
	return s.upload(src, storage.UploadInfo{
		Prefix:    prefix,
		Filename:  filename,
		FileSize:  int64(len(data)),
		MimeType:  http.DetectContentType(data),
		Principal: principal,
	}, tenantID)
}

func (s *assetService) UploadFile(file *multipart.FileHeader, prefix string, principal *dto.AuthPrincipal) (*models.Asset, error) {
	return s.UploadFileInTenant(file, prefix, s.tenantIDForPrincipal(principal), principal)
}

func (s *assetService) UploadFileInTenant(file *multipart.FileHeader, prefix string, tenantID int64, principal *dto.AuthPrincipal) (*models.Asset, error) {
	if tenantID <= 0 {
		return nil, errorsx.InvalidParam("文件缺少接入公司归属")
	}
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

	return s.upload(src, storage.UploadInfo{
		Prefix:    prefix,
		Filename:  file.Filename,
		FileSize:  file.Size,
		MimeType:  file.Header.Get("Content-Type"),
		Principal: principal,
	}, tenantID)
}

func (s *assetService) Upload(reader io.Reader, info storage.UploadInfo) (*models.Asset, error) {
	return s.upload(reader, info, s.tenantIDForPrincipal(info.Principal))
}

func (s *assetService) upload(reader io.Reader, info storage.UploadInfo, tenantID int64) (*models.Asset, error) {
	storageCfg := StorageSettingToConfig()
	provider, err := storage.NewProviderWithConfig(storageCfg.Default, storageCfg)
	if err != nil {
		return nil, err
	}
	info.Prefix = applyStorageObjectPrefix(info.Prefix, tenantID)

	assetID, key := storage.GenerateStorageKey(info)
	item := &models.Asset{
		TenantID:    tenantID,
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
		_ = s.markAssetStatus(item.ID, tenantID, enums.AssetStatusFailed, info.Principal)
		return nil, err
	}

	item.Status = enums.AssetStatusSuccess
	if tenantID > 0 {
		_ = repositories.AssetRepository.UpdateColumnInTenant(sqls.DB(), item.ID, tenantID, "status", enums.AssetStatusSuccess)
	} else {
		_ = repositories.AssetRepository.UpdateColumn(sqls.DB(), item.ID, "status", enums.AssetStatusSuccess)
	}

	return item, nil
}

func applyStorageObjectPrefix(prefix string, tenantID int64) string {
	setting := GetStorageSetting()
	globalPrefix := strings.Trim(strings.TrimSpace(setting.OSSObjectPrefix), "/")
	prefix = tenantStorageObjectPrefix(prefix, tenantID)
	if globalPrefix == "" {
		return prefix
	}
	if prefix == "" {
		return globalPrefix
	}
	return globalPrefix + "/" + prefix
}

func tenantStorageObjectPrefix(prefix string, tenantID int64) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	tenantPrefix := "platform"
	if tenantID > 0 {
		tenantPrefix = "tenants/" + strconv.FormatInt(tenantID, 10)
	}
	if prefix == "" {
		prefix = tenantPrefix
	} else {
		prefix = tenantPrefix + "/" + prefix
	}
	return prefix
}

func (s *assetService) RegisterExternal(prefix string, filename string, fileSize int64, mimeType string, externalURL string, principal *dto.AuthPrincipal) (*models.Asset, error) {
	return s.RegisterExternalInTenant(prefix, filename, fileSize, mimeType, externalURL, s.tenantIDForPrincipal(principal), principal)
}

func (s *assetService) RegisterExternalInTenant(prefix string, filename string, fileSize int64, mimeType string, externalURL string, tenantID int64, principal *dto.AuthPrincipal) (*models.Asset, error) {
	if tenantID <= 0 {
		return nil, errorsx.InvalidParam("文件缺少接入公司归属")
	}
	provider := enums.AssetProviderLocal
	externalURL = strings.TrimSpace(externalURL)
	if externalURL != "" {
		if existing := repositories.AssetRepository.GetByStorageKeyInTenant(sqls.DB(), externalURL, tenantID); existing != nil {
			return existing, nil
		}
		if existing := repositories.AssetRepository.GetByStorageKey(sqls.DB(), externalURL); existing != nil {
			return nil, errorsx.InvalidParam("外部文件已归属其他接入公司")
		}
	}
	assetID, key := storage.GenerateStorageKey(storage.UploadInfo{
		Prefix:    applyStorageObjectPrefix(prefix, tenantID),
		Filename:  filename,
		FileSize:  fileSize,
		MimeType:  mimeType,
		Principal: principal,
	})
	if externalURL != "" {
		key = externalURL
	}
	item := &models.Asset{
		TenantID:    tenantID,
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
	tenantID := s.tenantIDForPrincipal(principal)
	if tenantID <= 0 {
		return errorsx.Forbidden("请先选择接入公司")
	}
	item := s.GetInTenant(id, tenantID)
	if item == nil {
		return errorsx.InvalidParam("文件不存在")
	}
	return repositories.AssetRepository.UpdatesInTenant(sqls.DB(), id, tenantID, map[string]any{
		"status":           enums.AssetStatusDeleted,
		"update_user_id":   principal.UserID,
		"update_user_name": principal.Username,
		"updated_at":       time.Now(),
	})
}

func (s *assetService) markAssetStatus(id, tenantID int64, status enums.AssetStatus, principal *dto.AuthPrincipal) error {
	updates := map[string]any{
		"status":     status,
		"updated_at": time.Now(),
	}
	if principal != nil {
		updates["update_user_id"] = principal.UserID
		updates["update_user_name"] = principal.Username
	}
	if tenantID > 0 {
		return repositories.AssetRepository.UpdatesInTenant(sqls.DB(), id, tenantID, updates)
	}
	return repositories.AssetRepository.Updates(sqls.DB(), id, updates)
}

func (s *assetService) tenantIDForPrincipal(principal *dto.AuthPrincipal) int64 {
	if principal == nil {
		return 0
	}
	if principal.ActiveTenantID > 0 {
		return principal.ActiveTenantID
	}
	return principal.TenantID
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
