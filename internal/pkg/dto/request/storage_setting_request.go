package request

import "agent-desk/internal/pkg/enums"

type UpdateStorageSettingRequest struct {
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
