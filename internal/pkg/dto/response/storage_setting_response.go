package response

import "agent-desk/internal/pkg/enums"

type StorageSettingResponse struct {
	DefaultProvider       enums.AssetProvider `json:"defaultProvider"`
	MaxUploadSizeMB       int64               `json:"maxUploadSizeMb"`
	LocalRoot             string              `json:"localRoot"`
	LocalBaseURL          string              `json:"localBaseUrl"`
	OSSEndpoint           string              `json:"ossEndpoint"`
	OSSBucket             string              `json:"ossBucket"`
	OSSAccessKeyID        string              `json:"ossAccessKeyId"`
	OSSAccessKeySecretSet bool                `json:"ossAccessKeySecretSet"`
	OSSBaseURL            string              `json:"ossBaseUrl"`
	OSSObjectPrefix       string              `json:"ossObjectPrefix"`
	OSSPrivate            bool                `json:"ossPrivate"`
	OSSSignedURLExpire    int                 `json:"ossSignedUrlExpireSeconds"`
	WECDNBaseURL          string              `json:"wecdnBaseUrl"`
	PublicAssetBaseURL    string              `json:"publicAssetBaseUrl"`
}
