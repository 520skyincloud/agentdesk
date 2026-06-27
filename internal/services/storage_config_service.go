package services

import (
	"encoding/json"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const storageSettingConfigKey = "storage.asset"

type StorageSetting struct {
	DefaultProvider    enums.AssetProvider `json:"defaultProvider"`
	MaxUploadSizeMB    int64               `json:"maxUploadSizeMb"`
	LocalRoot          string              `json:"localRoot"`
	LocalBaseURL       string              `json:"localBaseUrl"`
	OSSEndpoint        string              `json:"ossEndpoint"`
	OSSBucket          string              `json:"ossBucket"`
	OSSAccessKeyID     string              `json:"ossAccessKeyId"`
	OSSAccessKeySecret string              `json:"ossAccessKeySecret"`
	OSSBaseURL         string              `json:"ossBaseUrl"`
	OSSObjectPrefix    string              `json:"ossObjectPrefix"`
	OSSPrivate         bool                `json:"ossPrivate"`
	OSSSignedURLExpire int                 `json:"ossSignedUrlExpireSeconds"`
	WECDNBaseURL       string              `json:"wecdnBaseUrl"`
	PublicAssetBaseURL string              `json:"publicAssetBaseUrl"`
}

func DefaultStorageSetting() StorageSetting {
	cfg := config.Current().Storage
	setting := StorageSetting{
		DefaultProvider:    cfg.Default,
		MaxUploadSizeMB:    cfg.MaxUploadSizeMB,
		LocalRoot:          cfg.Local.Root,
		LocalBaseURL:       cfg.Local.BaseURL,
		OSSEndpoint:        cfg.OSS.Endpoint,
		OSSBucket:          cfg.OSS.Bucket,
		OSSAccessKeyID:     cfg.OSS.AccessKeyID,
		OSSAccessKeySecret: cfg.OSS.AccessKeySecret,
		OSSBaseURL:         cfg.OSS.BaseURL,
		OSSObjectPrefix:    "",
		OSSPrivate:         cfg.OSS.Private,
		OSSSignedURLExpire: cfg.OSS.SignedURLExpire,
	}
	if setting.OSSEndpoint == "" {
		setting.OSSEndpoint = "oss-cn-beijing.aliyuncs.com"
	}
	if setting.OSSBucket == "" {
		setting.OSSBucket = "skychucun"
	}
	if setting.OSSBaseURL == "" {
		setting.OSSBaseURL = "https://skychucun.oss-cn-beijing.aliyuncs.com"
	}
	if setting.OSSObjectPrefix == "" {
		setting.OSSObjectPrefix = "desk"
	}
	if setting.PublicAssetBaseURL == "" {
		setting.PublicAssetBaseURL = "http://kefuceshi.omnireva.com"
	}
	return setting
}

func GetStorageSetting() StorageSetting {
	setting := DefaultStorageSetting()
	item := SystemConfigService.Take("config_key = ? AND status = ?", storageSettingConfigKey, enums.StatusOk)
	if item == nil || strings.TrimSpace(item.ConfigValue) == "" {
		return normalizeStorageSetting(setting)
	}
	_ = json.Unmarshal([]byte(item.ConfigValue), &setting)
	return normalizeStorageSetting(setting)
}

func SaveStorageSetting(setting StorageSetting, operator *dto.AuthPrincipal) (StorageSetting, error) {
	if operator == nil {
		return StorageSetting{}, errorsx.Unauthorized("未登录或登录已过期")
	}
	setting = normalizeStorageSetting(setting)
	if setting.DefaultProvider == enums.AssetProviderOSS {
		if setting.OSSEndpoint == "" || setting.OSSBucket == "" || setting.OSSAccessKeyID == "" || setting.OSSAccessKeySecret == "" {
			return StorageSetting{}, errorsx.InvalidParam("启用 OSS 存储时必须填写 Endpoint、Bucket、AccessKeyID、AccessKeySecret")
		}
	}
	data, err := json.Marshal(setting)
	if err != nil {
		return StorageSetting{}, err
	}
	now := time.Now()
	if existing := SystemConfigService.Take("config_key = ?", storageSettingConfigKey); existing != nil {
		err = repositories.SystemConfigRepository.Updates(sqls.DB(), existing.ID, map[string]any{
			"config_value":     string(data),
			"group_code":       "storage",
			"title":            "文件存储设置",
			"description":      "运行时文件存储、OSS 和企微富媒体公网资产设置",
			"status":           enums.StatusOk,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		})
	} else {
		err = repositories.SystemConfigRepository.Create(sqls.DB(), &models.SystemConfig{
			ConfigKey:   storageSettingConfigKey,
			ConfigValue: string(data),
			GroupCode:   "storage",
			Title:       "文件存储设置",
			Description: "运行时文件存储、OSS 和企微富媒体公网资产设置",
			Status:      enums.StatusOk,
			AuditFields: utils.BuildAuditFields(operator),
		})
	}
	if err != nil {
		return StorageSetting{}, err
	}
	return setting, nil
}

func StorageSettingToConfig() config.StorageConfig {
	setting := GetStorageSetting()
	cfg := config.Current().Storage
	cfg.Default = setting.DefaultProvider
	cfg.MaxUploadSizeMB = setting.MaxUploadSizeMB
	cfg.Local.Root = setting.LocalRoot
	cfg.Local.BaseURL = setting.LocalBaseURL
	cfg.OSS.Endpoint = setting.OSSEndpoint
	cfg.OSS.Bucket = setting.OSSBucket
	cfg.OSS.AccessKeyID = setting.OSSAccessKeyID
	cfg.OSS.AccessKeySecret = setting.OSSAccessKeySecret
	cfg.OSS.BaseURL = setting.OSSBaseURL
	cfg.OSS.Private = setting.OSSPrivate
	cfg.OSS.SignedURLExpire = setting.OSSSignedURLExpire
	return cfg
}

func normalizeStorageSetting(setting StorageSetting) StorageSetting {
	setting.DefaultProvider = enums.AssetProvider(strings.TrimSpace(string(setting.DefaultProvider)))
	if setting.DefaultProvider == "" {
		setting.DefaultProvider = enums.AssetProviderLocal
	}
	if setting.MaxUploadSizeMB <= 0 {
		setting.MaxUploadSizeMB = 20
	}
	setting.LocalRoot = strings.TrimSpace(setting.LocalRoot)
	setting.LocalBaseURL = strings.TrimRight(strings.TrimSpace(setting.LocalBaseURL), "/")
	setting.OSSEndpoint = strings.TrimSpace(setting.OSSEndpoint)
	setting.OSSBucket = strings.TrimSpace(setting.OSSBucket)
	setting.OSSAccessKeyID = strings.TrimSpace(setting.OSSAccessKeyID)
	setting.OSSAccessKeySecret = strings.TrimSpace(setting.OSSAccessKeySecret)
	setting.OSSBaseURL = strings.TrimRight(strings.TrimSpace(setting.OSSBaseURL), "/")
	setting.OSSObjectPrefix = strings.Trim(strings.TrimSpace(setting.OSSObjectPrefix), "/")
	setting.WECDNBaseURL = strings.TrimRight(strings.TrimSpace(setting.WECDNBaseURL), "/")
	setting.PublicAssetBaseURL = strings.TrimRight(strings.TrimSpace(setting.PublicAssetBaseURL), "/")
	if setting.OSSSignedURLExpire <= 0 {
		setting.OSSSignedURLExpire = 600
	}
	return setting
}
